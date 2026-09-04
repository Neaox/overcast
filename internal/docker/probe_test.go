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

	"github.com/overcast-sh/overcast/internal/events"
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
// resolveLocked maps a name-or-id to the name this fake keys networks by, as
// the real Engine API does. Caller holds ps.mu.
func (ps *probeServer) resolveLocked(nameOrID string) string {
	if _, ok := ps.existing[nameOrID]; ok {
		return nameOrID
	}
	for name, info := range ps.existing {
		if info.ID == nameOrID {
			return name
		}
	}
	return strings.TrimPrefix(nameOrID, "net-")
}

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
		// Driver and a spec-hash label so these tests isolate the isolation
		// flag. Without them every seeded network differs in three fields at
		// once and a test that meant to exercise drift on Internal proves
		// nothing about it. The hash is deliberately stale: an unlabelled or
		// wrong-labelled network *is* drifted, which is its own test in
		// netspec_test.go.
		Driver: DefaultNetworkDriver,
		Labels: map[string]string{LabelSpecHash: "stale00000000"},
	}
}

// seedMatchingSpec seeds the network a spec would have created, so a test can
// assert that a network already in the right state is left completely alone.
func (ps *probeServer) seedMatchingSpec(spec ResolvedNetworkSpec) {
	opts := spec.CreateOptions()
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.existing[opts.Name] = NetworkInspect{
		ID: "old-" + opts.Name, Name: opts.Name, Driver: opts.Driver,
		Internal: opts.Internal, Labels: opts.Labels, Options: opts.Options,
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
		ps.mu.Lock()
		// The Engine API accepts a name or an id at every /networks/{id}
		// endpoint, and the code under test removes by id on purpose — see
		// docker.recreateToSpec. A fake that understood only names would make a
		// correct implementation look like it had removed nothing.
		name := ps.resolveLocked(r.URL.Path[len("/v1.45/networks/"):])
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
		ps.mu.Lock()
		name := ps.resolveLocked(r.URL.Path[len("/v1.45/networks/"):])
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
	if got.Name != "overcast_control" || !got.Internal ||
		got.Reason != "auto: Overcast is containerised" || !got.OK() {
		t.Errorf("ProbeResult.Networks[0] = %+v, want an ok overcast_control with internal=true "+
			"and the containerised reason", got)
	}
}

// The resolved specs come back out, and they carry the answer InternalMode
// gave rather than the static field it overrode.
//
// This is what makes a later re-verification possible at all (#1599): the
// Docker event watcher sees a network name and needs the spec it was supposed
// to match. Re-resolving one from the NetworkSpec would call InternalMode a
// second time — a second decision, against a daemon that may have changed —
// where the point is to read the decision this process already made.
func TestProbe_carriesTheResolvedSpecsOut(t *testing.T) {
	srv, _ := newProbeServer("")
	defer srv.Close()

	specs := []NetworkSpec{
		{Name: "overcast", Internal: false},
		{Name: "overcast_control", Internal: false, InternalMode: func(_ context.Context, _ *Client) InternalDecision {
			return InternalDecision{Internal: true, Reason: "OVERCAST_CONTROL_PLANE_INTERNAL=true"}
		}},
	}

	result, err := Probe("tcp://"+addrOf(srv), specs, zap.NewNop())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}

	if len(result.Specs) != 2 {
		t.Fatalf("Specs = %+v, want one per network, in order", result.Specs)
	}
	if result.Specs[0].Name != "overcast" || result.Specs[0].Internal {
		t.Errorf("Specs[0] = %+v, want the data plane resolved routable", result.Specs[0])
	}
	if result.Specs[1].Name != "overcast_control" || !result.Specs[1].Internal {
		t.Errorf("Specs[1] = %+v, want InternalMode's answer, not the static field", result.Specs[1])
	}
	if result.Specs[1].Reason == "" {
		t.Error("the resolved spec lost its reason; a re-verification reports it as the probe did")
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
	spec := NetworkSpec{Name: "overcast_control", InternalMode: internalMode(true)}
	ps.seedMatchingSpec(spec.Resolve(context.Background(), nil))

	result, err := Probe("tcp://"+addrOf(srv), []NetworkSpec{spec}, zap.NewNop())
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

// The decision Probe records comes from the spec, not from the network it was
// applied to — and this is the test that keeps the call there.
//
// A plane that drifts at startup and cannot be repaired reports what it *is*:
// internal, in this case, because that is what an operator reading
// `docker.networks` needs to see. Status.Decisions answers the other question —
// what did this run resolve, and why — and Tracker.recordDecisionLocked
// deliberately refuses to answer it from a drifted observation. So the specs
// are the only remaining source, and without Supervisor.Probe recording them
// the control plane would have no decision at all: `controlPlaneRoutable` reads
// an empty Network, and vpc-egress-not-withheld goes silent by absence while
// the shortfall it reports stands unchanged.
//
// Absence is the failure mode this pins, which is why it asserts on the
// decision's contents rather than on a count.
func TestSupervisorProbe_recordsTheDecisionFromTheSpecEvenWhenTheNetworkDrifted(t *testing.T) {
	// Given: a control plane an older run left internal, with a container on
	// it — so the repair is declined and the drift stands, which is the only
	// case where the decision and the observation disagree.
	srv, ps := newProbeServer("")
	defer srv.Close()
	ps.seedExisting("overcast_control", true, 1)

	const hostVeto = "OVERCAST_VPC_EGRESS=none, overridden: host will not take an internal control plane"
	specs := []NetworkSpec{{
		Name: "overcast_control", Internal: true,
		InternalMode: func(_ context.Context, _ *Client) InternalDecision {
			return InternalDecision{Internal: false, Reason: hostVeto}
		},
	}}

	bus := events.NewBus()
	defer bus.Stop()
	tracker := NewTracker()
	sup := NewSupervisorWithTracker(bus, zap.NewNop(), tracker)
	defer sup.Close()

	// When: the supervisor probes that daemon.
	sup.Probe(context.Background(), []ServiceConfig{{Name: "lambda", Socket: "tcp://" + addrOf(srv)}}, specs)

	// Then: the decision is what this run resolved, with the reason that says
	// who resolved it.
	snap := tracker.Snapshot()
	if len(snap.Decisions) != 1 {
		t.Fatalf("decisions = %+v, want one for the control plane", snap.Decisions)
	}
	got := snap.Decisions[0]
	if got.Network != "overcast_control" {
		t.Fatalf("decision = %+v, want the control plane", got)
	}
	if got.Internal {
		t.Error("the decision took the drifted network's isolation; the egress advisory reads this and " +
			"would now report the plane as isolated when this run deliberately left it routable")
	}
	if !strings.Contains(got.Reason, "overridden: host") {
		t.Errorf("reason = %q, want the host override that decided it", got.Reason)
	}

	// And the observation is reported where observations belong: on the network
	// entry, which says what the network is and how to fix it.
	if len(snap.Networks) != 1 {
		t.Fatalf("networks = %+v, want the control plane reported", snap.Networks)
	}
	entry := snap.Networks[0]
	if !entry.Internal {
		t.Error("network entry = routable, want the drifted network's own isolation")
	}
	if entry.OK() || entry.Fix == "" {
		t.Errorf("network = %+v, want the unrepaired drift and the command that fixes it", entry)
	}
}
