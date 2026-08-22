package docker

// probe_test.go covers the InternalMode seam: NetworkSpec's static Internal
// field is the plain answer, but the control plane's isolation depends on a
// fact (native Linux daemon vs. Docker Desktop) that is only knowable once a
// client exists — see internal/dataplane.ControlPlaneInternal, which is the
// real decision this seam exists for. That decision is unit-tested on its own
// in internal/dataplane and internal/containerendpoint; what belongs here is
// that Probe actually calls InternalMode with the client it just dialled,
// that InternalMode wins over the static field, and that the drift warning
// compares against the resolved value rather than the static one.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"go.uber.org/zap"
)

// probeServer is a minimal fake Docker daemon: it answers ping, records every
// network-create request, and inspects back whatever was last created for
// that name (so warnOnInternalDrift and a repeat InternalMode probe against
// "bridge" both get an answer).
type probeServer struct {
	mu       sync.Mutex
	created  map[string]createNetworkRequest // name -> request body
	bridgeGW string                          // "" = no gateway reported for "bridge"
}

func newProbeServer(bridgeGateway string) (*httptest.Server, *probeServer) {
	ps := &probeServer{created: map[string]createNetworkRequest{}, bridgeGW: bridgeGateway}
	srv := httptest.NewServer(http.HandlerFunc(ps.handle))
	return srv, ps
}

func (ps *probeServer) handle(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/_ping":
		w.WriteHeader(http.StatusOK)

	case r.Method == http.MethodPost && r.URL.Path == "/v1.45/networks/create":
		var req createNetworkRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		ps.mu.Lock()
		ps.created[req.Name] = req
		ps.mu.Unlock()
		_ = json.NewEncoder(w).Encode(struct {
			ID string `json:"Id"`
		}{ID: "net-" + req.Name})

	case r.Method == http.MethodGet && r.URL.Path == "/v1.45/networks/bridge":
		if ps.bridgeGW == "" {
			http.Error(w, `{"message":"no such network"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(NetworkInspect{
			ID: "bridge", Name: "bridge",
			IPAM: NetworkIPAM{Config: []NetworkIPAMConfig{{Subnet: "172.17.0.0/16", Gateway: ps.bridgeGW}}},
		})

	case r.Method == http.MethodGet:
		// Post-create drift-check inspect, keyed by name/ID.
		name := r.URL.Path[len("/v1.45/networks/"):]
		ps.mu.Lock()
		req, ok := ps.created[name]
		ps.mu.Unlock()
		if !ok {
			http.Error(w, `{"message":"no such network"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(NetworkInspect{ID: "net-" + name, Name: name, Internal: req.Internal})

	default:
		http.Error(w, `{"message":"unexpected request"}`, http.StatusNotImplemented)
	}
}

func TestProbe_internalModeTakesPrecedenceOverTheStaticField(t *testing.T) {
	// Given: a spec whose static Internal says false but whose InternalMode
	// says true.
	srv, ps := newProbeServer("")
	defer srv.Close()

	var gotDC *Client
	specs := []NetworkSpec{
		{Name: "overcast_control", Internal: false, InternalMode: func(_ context.Context, dc *Client) bool {
			gotDC = dc
			return true
		}},
	}

	// When: Probe creates it.
	if _, err := Probe("tcp://"+addrOf(srv), specs, zap.NewNop()); err != nil {
		t.Fatalf("Probe: %v", err)
	}

	// Then: the network was created internal, and InternalMode was handed the
	// very client Probe just dialled and verified — the seam this exists for.
	ps.mu.Lock()
	req, ok := ps.created["overcast_control"]
	ps.mu.Unlock()
	if !ok {
		t.Fatal("overcast_control was never created")
	}
	if !req.Internal {
		t.Error("created network Internal = false, want true — InternalMode should have won")
	}
	if gotDC == nil {
		t.Error("InternalMode was not called with a live client")
	}
}

func TestProbe_staticInternalAppliesWithoutInternalMode(t *testing.T) {
	// Given: a spec with no InternalMode — the default data plane's shape.
	srv, ps := newProbeServer("")
	defer srv.Close()

	specs := []NetworkSpec{{Name: "overcast", Internal: false}}

	// When: Probe creates it.
	if _, err := Probe("tcp://"+addrOf(srv), specs, zap.NewNop()); err != nil {
		t.Fatalf("Probe: %v", err)
	}

	// Then: the static field is what was sent.
	ps.mu.Lock()
	req := ps.created["overcast"]
	ps.mu.Unlock()
	if req.Internal {
		t.Error("created network Internal = true, want false")
	}
}

func TestProbe_bothPlanesGetIndependentInternalDecisions(t *testing.T) {
	// Given: the real shape dataplane.PlaneSpecs produces — the default data
	// plane with a static (always-false) Internal, and the control plane with
	// an InternalMode that this test drives directly, standing in for
	// ControlPlaneInternal without needing a real daemon platform to probe.
	srv, ps := newProbeServer("")
	defer srv.Close()

	specs := []NetworkSpec{
		{Name: "overcast"},
		{Name: "overcast_control", InternalMode: func(context.Context, *Client) bool { return true }},
	}

	if _, err := Probe("tcp://"+addrOf(srv), specs, zap.NewNop()); err != nil {
		t.Fatalf("Probe: %v", err)
	}

	ps.mu.Lock()
	data, control := ps.created["overcast"], ps.created["overcast_control"]
	ps.mu.Unlock()
	if data.Internal {
		t.Error("default data plane created Internal = true, want false")
	}
	if !control.Internal {
		t.Error("control plane created Internal = false, want true")
	}
}

// addrOf returns the host:port an httptest.Server is listening on, for
// building a "tcp://" endpoint NewClient accepts.
func addrOf(srv *httptest.Server) string {
	return srv.Listener.Addr().String()
}
