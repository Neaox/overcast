package docker

// probe_test.go covers the InternalMode seam: NetworkSpec's static Internal
// field is the plain answer, but the control plane's isolation depends on a
// fact (native Linux daemon vs. Docker Desktop) that is only knowable once a
// client exists — see internal/dataplane.ControlPlaneInternal, which is the
// real decision this seam exists for. That decision is unit-tested on its own
// in internal/dataplane and internal/containerendpoint; what belongs here is
// that Probe actually calls InternalMode with the client it just dialled,
// that InternalMode wins over the static field, that the resolved decision
// comes back out for /_overcast/health, and that a pre-existing network whose
// isolation disagrees is recreated when that is free and reported when it is
// not (#1564).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"
)

// probeServer is a minimal fake Docker daemon: it answers ping, records every
// network-create and network-remove request, and inspects back whatever was
// last created for that name (so the drift reconciliation and a repeat
// InternalMode probe against "bridge" both get an answer).
//
// A name seeded into `existing` stands in for a network an *older* Overcast
// created: create is refused "already exists" exactly as Docker refuses it,
// and inspect answers with the isolation and attachments it was seeded with,
// not with what this run asked for. That distinction is the whole of #1564 —
// Docker never retroactively applies `--internal`.
type probeServer struct {
	mu       sync.Mutex
	created  map[string]createNetworkRequest // name -> request body
	existing map[string]NetworkInspect       // name -> a network that predates this run
	removed  []string                        // names passed to network rm, in order
	bridgeGW string                          // "" = no gateway reported for "bridge"
}

func newProbeServer(bridgeGateway string) (*httptest.Server, *probeServer) {
	ps := &probeServer{
		created:  map[string]createNetworkRequest{},
		existing: map[string]NetworkInspect{},
		bridgeGW: bridgeGateway,
	}
	srv := httptest.NewServer(http.HandlerFunc(ps.handle))
	return srv, ps
}

// seedExisting registers a network as already present, with the given
// isolation and number of attached containers.
func (ps *probeServer) seedExisting(name string, internal bool, attached int) {
	containers := map[string]NetworkEndpoint{}
	for i := 0; i < attached; i++ {
		id := fmt.Sprintf("container-%d", i)
		containers[id] = NetworkEndpoint{Name: id}
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.existing[name] = NetworkInspect{
		ID: "old-" + name, Name: name, Internal: internal, Containers: containers,
	}
}

func (ps *probeServer) handle(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/_ping":
		w.WriteHeader(http.StatusOK)

	case r.Method == http.MethodPost && r.URL.Path == "/v1.45/networks/create":
		var req createNetworkRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		ps.mu.Lock()
		_, clash := ps.existing[req.Name]
		if !clash {
			ps.created[req.Name] = req
		}
		ps.mu.Unlock()
		if clash {
			// Docker's own refusal, which CreateNetworkWithOptions treats as
			// success after looking the existing network up.
			http.Error(w,
				`{"message":"network with name `+req.Name+` already exists"}`,
				http.StatusConflict)
			return
		}
		_ = json.NewEncoder(w).Encode(struct {
			ID string `json:"Id"`
		}{ID: "net-" + req.Name})

	case r.Method == http.MethodDelete:
		name := r.URL.Path[len("/v1.45/networks/"):]
		ps.mu.Lock()
		ps.removed = append(ps.removed, name)
		delete(ps.existing, name)
		delete(ps.created, name)
		ps.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)

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
		old, isOld := ps.existing[name]
		req, ok := ps.created[name]
		ps.mu.Unlock()
		if isOld {
			_ = json.NewEncoder(w).Encode(old)
			return
		}
		if !ok {
			http.Error(w, `{"message":"no such network"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(NetworkInspect{ID: "net-" + name, Name: name, Internal: req.Internal})

	default:
		http.Error(w, `{"message":"unexpected request"}`, http.StatusNotImplemented)
	}
}

// internalMode is an InternalMode that always answers `internal`, for tests
// that care about what Probe does with the answer rather than how it is
// reached.
func internalMode(internal bool) func(context.Context, *Client) InternalDecision {
	return func(context.Context, *Client) InternalDecision {
		return InternalDecision{Internal: internal, Reason: "test"}
	}
}

func TestProbe_internalModeTakesPrecedenceOverTheStaticField(t *testing.T) {
	// Given: a spec whose static Internal says false but whose InternalMode
	// says true.
	srv, ps := newProbeServer("")
	defer srv.Close()

	var gotDC *Client
	specs := []NetworkSpec{
		{Name: "overcast_control", Internal: false, InternalMode: func(_ context.Context, dc *Client) InternalDecision {
			gotDC = dc
			return InternalDecision{Internal: true, Reason: "auto: Overcast is containerised"}
		}},
	}

	// When: Probe creates it.
	result, err := Probe("tcp://"+addrOf(srv), specs, zap.NewNop())
	if err != nil {
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

	// And: the decision and its reason came back out for /_overcast/health,
	// which is where a caller who cannot read the startup log looks (#1564).
	if len(result.Networks) != 1 {
		t.Fatalf("ProbeResult.Networks = %+v, want one entry", result.Networks)
	}
	got := result.Networks[0]
	want := NetworkStatus{Name: "overcast_control", Internal: true, Reason: "auto: Overcast is containerised"}
	if got != want {
		t.Errorf("ProbeResult.Networks[0] = %+v, want %+v", got, want)
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
		{Name: "overcast_control", InternalMode: internalMode(true)},
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

// A network created by an older Overcast keeps the isolation it was born with
// — Docker will not change it in place — and that is exactly how two engineers
// on one pinned version ended up with different behaviour (#1564). When
// nothing is attached, recreating it costs nothing and is the only way to make
// the pinned answer actually apply.
func TestProbe_recreatesADriftedNetworkWithNothingAttached(t *testing.T) {
	srv, ps := newProbeServer("")
	defer srv.Close()
	ps.seedExisting("overcast_control", false, 0) // pre-alpha.37: not internal

	result, err := Probe("tcp://"+addrOf(srv),
		[]NetworkSpec{{Name: "overcast_control", InternalMode: internalMode(true)}}, zap.NewNop())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}

	ps.mu.Lock()
	removed := append([]string(nil), ps.removed...)
	req, created := ps.created["overcast_control"]
	ps.mu.Unlock()

	if len(removed) != 1 || removed[0] != "overcast_control" {
		t.Errorf("removed = %v, want [overcast_control]", removed)
	}
	if !created || !req.Internal {
		t.Errorf("recreated network = %+v (created=%v), want Internal=true", req, created)
	}
	if len(result.Networks) != 1 || result.Networks[0].Drift != "" {
		t.Errorf("Networks = %+v, want no reported drift once it is repaired", result.Networks)
	}
}

// The other half of the same rule. Recreating a network that still has
// containers on it would drop every one of them off the plane mid-run — a
// worse startup than an isolation property one command away from correct — so
// it is left alone, reported, and warned about.
func TestProbe_leavesADriftedNetworkWithContainersAttached(t *testing.T) {
	srv, ps := newProbeServer("")
	defer srv.Close()
	ps.seedExisting("overcast_control", false, 2)

	result, err := Probe("tcp://"+addrOf(srv),
		[]NetworkSpec{{Name: "overcast_control", InternalMode: internalMode(true)}}, zap.NewNop())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}

	ps.mu.Lock()
	removed := append([]string(nil), ps.removed...)
	ps.mu.Unlock()
	if len(removed) != 0 {
		t.Errorf("removed = %v, want nothing removed while containers are attached", removed)
	}

	if len(result.Networks) != 1 {
		t.Fatalf("Networks = %+v, want one entry", result.Networks)
	}
	got := result.Networks[0]
	if got.Drift == "" {
		t.Error("Networks[0].Drift is empty, want the surviving drift reported for /_overcast/health")
	}
	// The reported Internal is what the network *is*, not what was asked for.
	// Reporting the ask here would repeat #1564's original lie in a new place:
	// `internal: true` beside a function that still reaches the internet.
	if got.Internal {
		t.Errorf("Networks[0].Internal = true, want the network's actual isolation (false)")
	}
	if !strings.Contains(got.Drift, "2 container(s) attached") {
		t.Errorf("Networks[0].Drift = %q, want it to name the attached containers", got.Drift)
	}
}

// Drift is not a control-plane-only concern: the data plane is asked for
// Internal=false, and a network that somehow has it set has the same class of
// silent divergence. The reconciliation is generic over specs for that reason.
func TestProbe_reconcilesDriftOnTheDataPlaneToo(t *testing.T) {
	srv, ps := newProbeServer("")
	defer srv.Close()
	ps.seedExisting("overcast", true, 0) // internal, which the data plane never wants

	if _, err := Probe("tcp://"+addrOf(srv), []NetworkSpec{{Name: "overcast"}}, zap.NewNop()); err != nil {
		t.Fatalf("Probe: %v", err)
	}

	ps.mu.Lock()
	removed := append([]string(nil), ps.removed...)
	req := ps.created["overcast"]
	ps.mu.Unlock()
	if len(removed) != 1 || req.Internal {
		t.Errorf("removed = %v, recreated = %+v; want the data plane recreated non-internal", removed, req)
	}
}

// A network that already matches is left completely alone — no remove, no
// recreate. Churning the plane on every startup would drop containers for no
// reason at all.
func TestProbe_leavesAMatchingNetworkUntouched(t *testing.T) {
	srv, ps := newProbeServer("")
	defer srv.Close()
	ps.seedExisting("overcast_control", true, 0)

	result, err := Probe("tcp://"+addrOf(srv),
		[]NetworkSpec{{Name: "overcast_control", InternalMode: internalMode(true)}}, zap.NewNop())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}

	ps.mu.Lock()
	removed := append([]string(nil), ps.removed...)
	ps.mu.Unlock()
	if len(removed) != 0 {
		t.Errorf("removed = %v, want nothing removed when the network already agrees", removed)
	}
	if len(result.Networks) != 1 || result.Networks[0].Drift != "" {
		t.Errorf("Networks = %+v, want no drift", result.Networks)
	}
}

// addrOf returns the host:port an httptest.Server is listening on, for
// building a "tcp://" endpoint NewClient accepts.
func addrOf(srv *httptest.Server) string {
	return srv.Listener.Addr().String()
}
