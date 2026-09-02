package docker

// netspec_test.go — the exact-state network verification, branch by branch.
//
// The whole point of this code is that it does not trust a network it did not
// just create, so the fake daemon here is stateful: it can be seeded with a
// network in any state, it records what was created and removed, and it refuses
// anything the real Engine API would refuse. A verification tested against a
// daemon that answers "yes" to everything verifies nothing.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"
)

// specDaemon is a fake Docker daemon that holds networks and remembers what was
// done to them.
type specDaemon struct {
	mu       sync.Mutex
	networks map[string]*NetworkInspect
	created  []createNetworkRequest
	removed  []string

	// removeSucceedsWithoutRemoving models the one thing that makes a
	// concurrent rebuild dangerous: a remove that reports success while the
	// network is still there, because somebody else removed and recreated it
	// between our inspect and our remove.
	removeSucceedsWithoutRemoving bool

	// refuseCreate models a daemon that will not create the network at all —
	// address-pool exhaustion being the common cause.
	refuseCreate bool
}

func newSpecDaemon(t *testing.T, seed ...*NetworkInspect) (*Client, *specDaemon) {
	t.Helper()
	d := &specDaemon{networks: map[string]*NetworkInspect{}}
	for _, n := range seed {
		d.networks[n.Name] = n
	}
	srv := httptest.NewServer(http.HandlerFunc(d.handle))
	t.Cleanup(srv.Close)
	return &Client{
		httpClient: srv.Client(),
		host:       srv.URL,
		logger:     zap.NewNop(),
		sem:        make(chan struct{}, maxConcurrentOps),
	}, d
}

func (d *specDaemon) handle(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/_ping":
		w.WriteHeader(http.StatusOK)

	case r.Method == http.MethodPost && r.URL.Path == "/v1.45/networks/create":
		d.mu.Lock()
		refuse := d.refuseCreate
		d.mu.Unlock()
		if refuse {
			http.Error(w, `{"message":"could not find an available, non-overlapping IPv4 address pool"}`,
				http.StatusInternalServerError)
			return
		}
		var req createNetworkRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		d.mu.Lock()
		d.created = append(d.created, req)
		if _, exists := d.networks[req.Name]; exists {
			d.mu.Unlock()
			http.Error(w, `{"message":"network with name `+req.Name+` already exists"}`, http.StatusConflict)
			return
		}
		info := &NetworkInspect{
			ID: "net-" + req.Name, Name: req.Name, Driver: req.Driver,
			Internal: req.Internal, EnableIPv6: req.EnableIPv6,
			Labels: req.Labels, Options: req.Options, Scope: "local",
		}
		if req.IPAM != nil {
			info.IPAM = *req.IPAM
		}
		d.networks[req.Name] = info
		d.mu.Unlock()
		_ = json.NewEncoder(w).Encode(struct {
			ID string `json:"Id"`
		}{ID: info.ID})

	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1.45/networks/"):
		d.mu.Lock()
		name := d.resolveLocked(strings.TrimPrefix(r.URL.Path, "/v1.45/networks/"))
		info, ok := d.networks[name]
		if ok && len(info.Containers) > 0 {
			d.mu.Unlock()
			// What the real daemon does, and the reason the attached case can
			// never be repaired in place.
			http.Error(w, `{"message":"network has active endpoints"}`, http.StatusForbidden)
			return
		}
		if !d.removeSucceedsWithoutRemoving {
			delete(d.networks, name)
		}
		d.removed = append(d.removed, name)
		d.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)

	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1.45/networks/"):
		d.mu.Lock()
		info, ok := d.networks[d.resolveLocked(strings.TrimPrefix(r.URL.Path, "/v1.45/networks/"))]
		d.mu.Unlock()
		if !ok {
			http.Error(w, `{"message":"no such network"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(info)

	default:
		http.Error(w, `{"message":"unexpected request"}`, http.StatusNotImplemented)
	}
}

// resolveLocked maps a name-or-id to the name this fake keys networks by. The
// real Engine API accepts either at every /networks/{id} endpoint, and the code
// under test deliberately removes by id — a fake that only understood names
// would make a correct implementation look broken. Caller holds d.mu.
func (d *specDaemon) resolveLocked(nameOrID string) string {
	if _, ok := d.networks[nameOrID]; ok {
		return nameOrID
	}
	for name, info := range d.networks {
		if info.ID == nameOrID {
			return name
		}
	}
	return nameOrID
}

func (d *specDaemon) network(name string) *NetworkInspect {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.networks[name]
}

func (d *specDaemon) removeCount(name string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, r := range d.removed {
		if r == name {
			n++
		}
	}
	return n
}

func (d *specDaemon) createCount(name string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, c := range d.created {
		if c.Name == name {
			n++
		}
	}
	return n
}

// testSpec is a fully-specified network: every field the verification compares
// is pinned, so a test that changes one is testing that one field.
func testSpec() ResolvedNetworkSpec {
	return NetworkSpec{
		Name:    "overcast_control",
		Driver:  "bridge",
		Subnet:  "10.77.0.0/16",
		Gateway: "10.77.0.1",
		Options: map[string]string{OptionIPMasquerade: "true"},
		Labels:  ManagedLabels(ServiceCore, "overcast_control"),
		Owner:   "instance-a",
		Version: "0.0.1-test",
	}.Resolve(context.Background(), nil)
}

// asCreated renders the network a spec would have produced, which is what a
// matching daemon has to hand back.
func asCreated(spec ResolvedNetworkSpec) *NetworkInspect {
	opts := spec.CreateOptions()
	info := &NetworkInspect{
		ID: "net-" + opts.Name, Name: opts.Name, Driver: opts.Driver,
		Internal: opts.Internal, EnableIPv6: opts.IPv6,
		Labels: opts.Labels, Options: opts.Options, Scope: "local",
	}
	if opts.Subnet != "" {
		info.IPAM = NetworkIPAM{Config: []NetworkIPAMConfig{{Subnet: opts.Subnet, Gateway: opts.Gateway}}}
	}
	return info
}

// ─── SpecHash ───────────────────────────────────────────────────────────────

// The hash is the identity of the desired state. It has to move when any field
// that changes how the network behaves changes, and stay put when something
// that does not — the version label, the owner — changes. Otherwise a release
// that altered nothing would mark every network on the machine as drifted.
func TestSpecHash_coversBehaviourAndNotIdentity(t *testing.T) {
	base := testSpec()

	behaviour := map[string]func(*ResolvedNetworkSpec){
		"internal": func(s *ResolvedNetworkSpec) { s.Internal = true },
		"driver":   func(s *ResolvedNetworkSpec) { s.Driver = "macvlan" },
		"ipv6":     func(s *ResolvedNetworkSpec) { s.IPv6 = true },
		"subnet":   func(s *ResolvedNetworkSpec) { s.Subnet = "10.88.0.0/16" },
		"gateway":  func(s *ResolvedNetworkSpec) { s.Gateway = "10.77.0.254" },
		"option":   func(s *ResolvedNetworkSpec) { s.Options = map[string]string{OptionICC: "false"} },
	}
	for name, mutate := range behaviour {
		t.Run(name+" changes the hash", func(t *testing.T) {
			mutated := testSpec()
			mutate(&mutated)
			if mutated.SpecHash() == base.SpecHash() {
				t.Errorf("changing %s left the hash at %s", name, base.SpecHash())
			}
		})
	}

	identity := map[string]func(*ResolvedNetworkSpec){
		"version": func(s *ResolvedNetworkSpec) { s.Version = "9.9.9" },
		"owner":   func(s *ResolvedNetworkSpec) { s.Owner = "instance-b" },
		"labels":  func(s *ResolvedNetworkSpec) { s.Labels = map[string]string{"x": "y"} },
		"egress":  func(s *ResolvedNetworkSpec) { s.EgressMode = "none" },
	}
	for name, mutate := range identity {
		t.Run(name+" leaves the hash alone", func(t *testing.T) {
			mutated := testSpec()
			mutate(&mutated)
			if mutated.SpecHash() != base.SpecHash() {
				t.Errorf("changing %s moved the hash from %s to %s — two instances asking for the "+
					"same network state must agree on it", name, base.SpecHash(), mutated.SpecHash())
			}
		})
	}
}

// Options are compared only where the spec pins them: Docker reports its own
// defaults for the rest, and calling those a mismatch would mark every network
// permanently wrong.
func TestDiff_ignoresUnpinnedIPAMAndOptions(t *testing.T) {
	spec := NetworkSpec{Name: "overcast", Labels: ManagedLabels(ServiceCore, "overcast")}.
		Resolve(context.Background(), nil)
	live := asCreated(spec)
	live.IPAM = NetworkIPAM{Config: []NetworkIPAMConfig{{Subnet: "172.20.0.0/16", Gateway: "172.20.0.1"}}}
	live.Options = map[string]string{OptionICC: "true", "com.docker.network.driver.mtu": "1500"}

	if diffs := spec.Diff(live); len(diffs) != 0 {
		t.Errorf("Diff = %v, want none: the spec pinned neither IPAM nor these options", diffs)
	}
}

// Every network created before this code carries no spec label, and that is
// exactly the population #1564 was about — networks whose settings nobody could
// account for, on machines where everything looked fine. "No label" must read
// as "unverified", never as "probably correct".
func TestDiff_treatsAnUnlabelledNetworkAsMismatched(t *testing.T) {
	spec := testSpec()
	live := asCreated(spec)
	delete(live.Labels, LabelSpecHash)

	diffs := spec.Diff(live)
	if len(diffs) != 1 || diffs[0].Field != LabelSpecHash {
		t.Fatalf("Diff = %v, want exactly a spec-hash mismatch", diffs)
	}
	if !strings.Contains(diffs[0].Got, "absent") {
		t.Errorf("Got = %q, want it to say the label is absent and why", diffs[0].Got)
	}
}

// Each field is compared, not just the one that happened to matter last time.
// A verification that checks one flag reports "matches" for a network that
// differs in four others.
func TestDiff_comparesEveryField(t *testing.T) {
	spec := testSpec()

	cases := map[string]struct {
		mutate func(*NetworkInspect)
		field  string
	}{
		"driver":   {func(n *NetworkInspect) { n.Driver = "macvlan" }, "driver"},
		"internal": {func(n *NetworkInspect) { n.Internal = true }, "internal"},
		"ipv6":     {func(n *NetworkInspect) { n.EnableIPv6 = true }, "ipv6"},
		"subnet": {func(n *NetworkInspect) {
			n.IPAM.Config[0].Subnet = "10.99.0.0/16"
		}, "ipam.subnet"},
		"gateway": {func(n *NetworkInspect) {
			n.IPAM.Config[0].Gateway = "10.77.0.254"
		}, "ipam.gateway"},
		"option": {func(n *NetworkInspect) {
			n.Options = map[string]string{OptionIPMasquerade: "false"}
		}, "option:" + OptionIPMasquerade},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			live := asCreated(spec)
			tc.mutate(live)
			diffs := spec.Diff(live)
			found := false
			for _, d := range diffs {
				if d.Field == tc.field {
					found = true
				}
			}
			if !found {
				t.Errorf("Diff = %v, want a %s mismatch", diffs, tc.field)
			}
		})
	}
}

// ─── EnsureNetwork ──────────────────────────────────────────────────────────

func TestEnsureNetwork_createsWhenAbsent(t *testing.T) {
	dc, d := newSpecDaemon(t)
	spec := testSpec()

	status, _ := EnsureNetwork(context.Background(), dc, spec, zap.NewNop())

	if !status.OK() {
		t.Errorf("status = %+v, want ok — an absent network is created, not drifted", status)
	}
	live := d.network(spec.Name)
	if live == nil {
		t.Fatal("network was not created")
	}
	if live.Labels[LabelSpecHash] != spec.SpecHash() {
		t.Errorf("spec-hash label = %q, want %q — a network created without it cannot be verified "+
			"on the next start", live.Labels[LabelSpecHash], spec.SpecHash())
	}
	if live.Labels[LabelInstance] != spec.Owner {
		t.Errorf("instance label = %q, want %q", live.Labels[LabelInstance], spec.Owner)
	}
}

func TestEnsureNetwork_leavesAMatchingNetworkAlone(t *testing.T) {
	spec := testSpec()
	dc, d := newSpecDaemon(t, asCreated(spec))

	status, _ := EnsureNetwork(context.Background(), dc, spec, zap.NewNop())

	if !status.OK() {
		t.Errorf("status = %+v, want ok", status)
	}
	if d.removeCount(spec.Name) != 0 || d.createCount(spec.Name) != 0 {
		t.Errorf("a matching network was touched: %d removes, %d creates",
			d.removeCount(spec.Name), d.createCount(spec.Name))
	}
}

// Repair is free when nothing is attached: there is by definition no connection
// to sever. This is the case that closes #1564's drift, so it has to actually
// change the network rather than only report it.
func TestEnsureNetwork_recreatesADriftedNetworkWithNothingAttached(t *testing.T) {
	spec := testSpec()
	stale := asCreated(spec)
	stale.Internal = true
	stale.Labels[LabelSpecHash] = "stale00000000"
	dc, d := newSpecDaemon(t, stale)

	status, _ := EnsureNetwork(context.Background(), dc, spec, zap.NewNop())

	if !status.OK() {
		t.Errorf("status = %+v, want ok — the drift was repaired, and reporting a healed drift "+
			"trains readers to ignore the field", status)
	}
	if d.removeCount(spec.Name) != 1 || d.createCount(spec.Name) != 1 {
		t.Fatalf("expected one remove and one create, got %d and %d",
			d.removeCount(spec.Name), d.createCount(spec.Name))
	}
	if live := d.network(spec.Name); live == nil || live.Internal {
		t.Errorf("network after repair = %+v, want Internal=false", live)
	}
}

// A network with containers on it cannot be repaired — Docker refuses the
// removal, and forcing it would drop every attached container off the network
// mid-run. So it is left exactly as it was, and the report has to carry
// everything the operator needs: which fields differ, what is in the way, and
// the command that fixes it.
func TestEnsureNetwork_reportsRatherThanBreakingAttachedContainers(t *testing.T) {
	spec := testSpec()
	stale := asCreated(spec)
	stale.Internal = true
	stale.Containers = map[string]NetworkEndpoint{
		"c1": {Name: "overcast-lambda-orders"},
		"c2": {Name: "my-compose-db"},
	}
	dc, d := newSpecDaemon(t, stale)

	status, _ := EnsureNetwork(context.Background(), dc, spec, zap.NewNop())

	if status.OK() {
		t.Fatal("status is ok, want a reported mismatch")
	}
	if d.network(spec.Name) == nil {
		t.Fatal("the network was removed out from under two running containers")
	}
	if !status.Internal {
		t.Errorf("Internal = false, want what the network *is* (true) — reporting the ask beside a " +
			"container that gets ENETUNREACH is #1564's original lie in a new place")
	}
	if got := strings.Join(DiffStrings(status.Mismatch), "; "); !strings.Contains(got, "internal") {
		t.Errorf("Mismatch = %q, want it to name the internal field", got)
	}
	if len(status.Attached) != 2 {
		t.Errorf("Attached = %v, want both container names so the warning says what to stop",
			status.Attached)
	}
	if !strings.Contains(status.Fix, "overcast network reset "+spec.Name) {
		t.Errorf("Fix = %q, want the reset command", status.Fix)
	}
}

// A network labelled for another Overcast instance is that instance's to
// manage, however wrong it looks from here. Removing it is how a running VPC
// loses its network — which has been observed on this daemon.
func TestEnsureNetwork_neverTouchesAnotherInstancesNetwork(t *testing.T) {
	spec := testSpec()
	theirs := asCreated(spec)
	theirs.Internal = true
	theirs.Labels[LabelInstance] = "instance-b"
	dc, d := newSpecDaemon(t, theirs)

	status, _ := EnsureNetwork(context.Background(), dc, spec, zap.NewNop())

	if d.removeCount(spec.Name) != 0 {
		t.Fatal("another instance's network was removed")
	}
	if status.Owner != "instance-b" {
		t.Errorf("Owner = %q, want instance-b", status.Owner)
	}
	if status.OK() {
		t.Error("status is ok, want the mismatch reported so it reaches health")
	}
	if !strings.Contains(status.Drift, "another Overcast instance") {
		t.Errorf("Drift = %q, want it to name the cause", status.Drift)
	}
}

// A swarm-scoped network is not this daemon's alone to rebuild, so it is
// reported and never removed even with nothing attached.
func TestEnsureNetwork_refusesToRebuildASwarmScopedNetwork(t *testing.T) {
	spec := testSpec()
	swarm := asCreated(spec)
	swarm.Internal = true
	swarm.Scope = "swarm"
	dc, d := newSpecDaemon(t, swarm)

	status, _ := EnsureNetwork(context.Background(), dc, spec, zap.NewNop())

	if d.removeCount(spec.Name) != 0 {
		t.Fatal("a swarm-scoped network was removed")
	}
	if status.OK() {
		t.Error("status is ok, want the mismatch reported")
	}
}

// ─── concurrency ────────────────────────────────────────────────────────────

// RemoveNetwork treats a missing network as success, because a missing network
// is normally the outcome wanted. That makes a 404 from a network somebody else
// removed and recreated indistinguishable from a removal this call performed —
// and creating on the strength of it overwrites their network with our spec.
//
// So the removal is confirmed rather than inferred. Here the daemon reports the
// remove as successful while the network is still present, which is exactly
// what a concurrent rebuild looks like from this side.
func TestEnsureNetwork_doesNotCreateOverANetworkItDidNotRemove(t *testing.T) {
	spec := testSpec()
	stale := asCreated(spec)
	stale.Internal = true
	dc, d := newSpecDaemon(t, stale)

	// A remove that reports success and changes nothing — a neighbour rebuilt
	// the network under the same name between our inspect and our remove.
	d.mu.Lock()
	d.removeSucceedsWithoutRemoving = true
	d.mu.Unlock()

	status, _ := EnsureNetwork(context.Background(), dc, spec, zap.NewNop())

	if d.createCount(spec.Name) != 0 {
		t.Fatalf("created over a network this call did not remove (%d creates)", d.createCount(spec.Name))
	}
	if status.OK() {
		t.Error("status is ok, want the unrepaired mismatch reported")
	}
}

// Two concurrent repairs of one network must not interleave into a remove that
// lands after the other's create. The lock is keyed by name because an id is
// exactly what a rebuild changes.
func TestEnsureNetwork_serialisesConcurrentRepairs(t *testing.T) {
	spec := testSpec()
	stale := asCreated(spec)
	stale.Internal = true
	dc, d := newSpecDaemon(t, stale)

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = EnsureNetwork(context.Background(), dc, spec, zap.NewNop())
		}()
	}
	wg.Wait()

	// Exactly one repair: the first sees the drift, the rest re-read under the
	// lock and find a network that already matches.
	if got := d.removeCount(spec.Name); got != 1 {
		t.Errorf("removes = %d, want exactly 1 — the rest must re-read under the lock and find it repaired", got)
	}
	if got := d.createCount(spec.Name); got != 1 {
		t.Errorf("creates = %d, want exactly 1", got)
	}
	if live := d.network(spec.Name); live == nil || live.Internal {
		t.Errorf("network = %+v, want one present and repaired", live)
	}
}

// A network that is absent and cannot be created is fatal. "A wrong-but-usable
// network is not a reason to refuse to start" is right; *no* network is a
// different case — the daemon would start and then fail every container create
// with an error naming nothing about networks.
func TestEnsureNetwork_absentAndUncreatableIsFatal(t *testing.T) {
	dc, d := newSpecDaemon(t)
	d.mu.Lock()
	d.refuseCreate = true
	d.mu.Unlock()

	status, err := EnsureNetwork(context.Background(), dc, testSpec(), zap.NewNop())
	if err == nil {
		t.Fatal("EnsureNetwork returned no error for a network that could not be created")
	}
	if !strings.Contains(status.Drift, "could not create") {
		t.Errorf("Drift = %q, want it to say the create failed", status.Drift)
	}
}

// A declined *repair* is still only a warning: the network exists and works,
// it is merely not in the state asked for.
func TestEnsureNetwork_declinedRepairIsNotFatal(t *testing.T) {
	spec := testSpec()
	stale := asCreated(spec)
	stale.Internal = true
	stale.Containers = map[string]NetworkEndpoint{"c1": {Name: "busy"}}
	dc, _ := newSpecDaemon(t, stale)

	if _, err := EnsureNetwork(context.Background(), dc, spec, zap.NewNop()); err != nil {
		t.Fatalf("a declined repair returned a fatal error: %v", err)
	}
}

// An unlabelled network under one of our names is not automatically ours. A
// Compose network called `overcast` is byte-for-byte indistinguishable from a
// plane an older Overcast made, if the only thing checked is the absence of our
// label — so the ownership marks other tools stamp are checked too, and
// "absence is not permission" is applied the way the VPC sweep applies it.
func TestEnsureNetwork_neverDestroysAnotherToolsNetwork(t *testing.T) {
	spec := testSpec()
	theirs := asCreated(spec)
	theirs.Internal = true
	theirs.Labels = map[string]string{
		"com.docker.compose.project": "my-stack",
		"com.docker.compose.network": "overcast",
	}
	dc, d := newSpecDaemon(t, theirs)

	status, err := EnsureNetwork(context.Background(), dc, spec, zap.NewNop())
	if err != nil {
		t.Fatalf("EnsureNetwork: %v", err)
	}
	if d.removeCount(spec.Name) != 0 {
		t.Fatal("a docker compose network was removed")
	}
	if status.OK() {
		t.Error("status is ok, want the collision reported")
	}
	if !strings.Contains(status.Drift, "docker compose") {
		t.Errorf("Drift = %q, want it to name the tool that owns the network", status.Drift)
	}
}

// An Overcast network that is also part of a Compose project is still
// Overcast's: our own managed label settles it, whatever else is stamped on.
func TestEnsureNetwork_stillRepairsOurOwnNetworkUnderComposeLabels(t *testing.T) {
	spec := testSpec()
	ours := asCreated(spec)
	ours.Internal = true
	ours.Labels["com.docker.compose.project"] = "my-stack"
	dc, d := newSpecDaemon(t, ours)

	if _, err := EnsureNetwork(context.Background(), dc, spec, zap.NewNop()); err != nil {
		t.Fatalf("EnsureNetwork: %v", err)
	}
	if d.removeCount(spec.Name) != 1 {
		t.Errorf("removes = %d, want the network repaired: our own managed label settles ownership",
			d.removeCount(spec.Name))
	}
}
