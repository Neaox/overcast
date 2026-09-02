package ec2

// vpc_network_ownership_test.go — a reconcile on a shared daemon acts only on
// the VPC networks this instance created.
//
// The snapshot the router hands ReconcileNetworks is every network on the
// daemon carrying overcast.service=ec2, whoever created it. Each strategy's
// orphan pass used to remove whatever no VPC in *this* store claimed — which,
// for a test server with an empty in-memory store, was every VPC network on
// the machine. That is how running the ECS integration package deleted a live
// instance's overcast-vpc-vpc-7d738f2a while a developer was using it.
//
// The rule now is the container GC's: a network is removable only when it
// carries this instance's overcast.instance label. Another instance's is
// invisible, and one with no label (created before the label) is adoptable
// but never removed.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/dataplane"
	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

// ownershipDaemon records network removals and the labels of network creates.
type ownershipDaemon struct {
	server  *httptest.Server
	mu      sync.Mutex
	removed []string
	created []map[string]string // labels, per create
	// networks holds what has been created, so an inspect answers with it.
	// docker.EnsureNetwork verifies a network before creating it and reads it
	// back after, so a fake that 404s or returns nothing for every inspect is
	// not modelling the daemon closely enough to exercise the create path.
	networks map[string]bool
	// inspects answers network inspect by ID for networks a test seeded rather
	// than created, so the reconcile sees what the snapshot claims exists.
	inspects map[string]docker.NetworkInspect
}

func newOwnershipDaemon(t *testing.T) *ownershipDaemon {
	t.Helper()
	d := &ownershipDaemon{networks: map[string]bool{}, inspects: map[string]docker.NetworkInspect{}}
	d.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/networks/create"):
			var req struct {
				Name   string            `json:"Name"`
				Labels map[string]string `json:"Labels"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			d.created = append(d.created, req.Labels)
			d.networks[req.Name] = true
			_, _ = w.Write([]byte(`{"Id":"net-created"}`))
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1.45/networks/"):
			name := strings.TrimPrefix(r.URL.Path, "/v1.45/networks/")
			d.removed = append(d.removed, name)
			delete(d.networks, name)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1.45/networks/"):
			name := strings.TrimPrefix(r.URL.Path, "/v1.45/networks/")
			if seeded, ok := d.inspects[name]; ok {
				_ = json.NewEncoder(w).Encode(seeded)
				return
			}
			if !d.networks[name] {
				http.Error(w, `{"message":"no such network"}`, http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(docker.NetworkInspect{
				ID: "net-created", Name: name, Driver: "bridge", Scope: "local",
			})
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(d.server.Close)
	return d
}

func (d *ownershipDaemon) removedNetworks() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.removed...)
}

// hostHandler returns a Docker-ready handler running on the host (so no
// self-attachment traffic muddies the daemon's call log), using the named VPC
// network strategy.
func hostHandler(t *testing.T, d *ownershipDaemon, strategy string) *Handler {
	t.Helper()
	origInContainer := runningInContainer
	t.Cleanup(func() { runningInContainer = origInContainer })
	runningInContainer = func() bool { return false }

	cfg := &config.Config{
		Region: "us-east-1", AccountID: "000000000000",
		EC2VPCNetworkStrategy: strategy,
		// VPC network names derive from OVERCAST_NETWORK, so a Config built
		// directly has to carry the default the loader would have given it or
		// the names come out as "-vpc-…".
		Network: "overcast",
	}
	h := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.NewMock()).handler
	h.docker = docker.NewClient(strings.Replace(d.server.URL, "http://", "tcp://", 1), zap.NewNop())
	h.dockerReady.Store(true)
	return h
}

func ec2Network(id, vpcID, subnet, instance string) docker.NetworkSummary {
	labels := docker.ManagedLabels("ec2", vpcID)
	labels["overcast.vpc-id"] = vpcID
	if instance != "" {
		labels[docker.LabelInstance] = instance
	}
	return docker.NetworkSummary{
		ID: id, Name: "overcast-vpc-" + vpcID, Labels: labels,
		IPAM: docker.NetworkIPAM{Config: []docker.NetworkIPAMConfig{{Subnet: subnet}}},
	}
}

// seedFromSummary makes the fake daemon actually hold a network the snapshot
// names, in the exact state this configuration would have created it in.
//
// Without it the reconcile's isolation pass inspects a network the fake does
// not have, treats the record as naming a network that has vanished, and heals
// it by creating a new one — correct behaviour, and not what these tests are
// about. The spec hash is computed the way production computes it, so the
// network reads as verified rather than drifted.
func (d *ownershipDaemon) seedFromSummary(h *Handler, n docker.NetworkSummary, internal bool) {
	spec := dataplane.VPCNetworkSpec(h.cfg, dataplane.VPCNetwork{
		VPCID: n.Labels["overcast.vpc-id"], Subnet: n.Subnet(), Owner: n.Instance(),
		Internal: internal,
	}).Resolve(context.Background(), nil)
	labels := make(map[string]string, len(n.Labels)+1)
	for k, v := range n.Labels {
		labels[k] = v
	}
	labels[docker.LabelSpecHash] = spec.SpecHash()
	d.mu.Lock()
	defer d.mu.Unlock()
	d.inspects[n.ID] = docker.NetworkInspect{
		ID: n.ID, Name: n.Name, Driver: docker.DefaultNetworkDriver, Internal: internal,
		Labels: labels, Scope: "local",
		IPAM: docker.NetworkIPAM{Config: []docker.NetworkIPAMConfig{{Subnet: n.Subnet()}}},
	}
	d.networks[n.Name] = true
}

func TestReconcileNetworks_removesOnlyThisInstancesOrphans(t *testing.T) {
	for _, strategy := range []string{"shared", "strict", "remapped"} {
		t.Run(strategy, func(t *testing.T) {
			daemon := newOwnershipDaemon(t)
			h := hostHandler(t, daemon, strategy)
			ctx := context.Background()
			mine := h.instances.Resolve(ctx)
			if mine == "" {
				t.Fatal("the handler has no instance identity; nothing below would be removable")
			}

			// Given: one VPC of ours, backed by net-mine-live.
			live := &VPC{VpcID: "vpc-mine-live", CidrBlock: "10.1.0.0/16",
				DockerNetworkID: "net-mine-live", NetworkStatus: vpcNetworkStatusOK}
			if aerr := h.store.putVPC(ctx, live); aerr != nil {
				t.Fatalf("putVPC: %v", aerr.Message)
			}
			// And: a daemon-wide snapshot — ours (live and orphaned), another
			// instance's, and one from before the label existed.
			snapshot := []docker.NetworkSummary{
				ec2Network("net-mine-live", "vpc-mine-live", "10.1.0.0/16", mine),
				ec2Network("net-mine-orphan", "vpc-mine-gone", "10.2.0.0/16", mine),
				ec2Network("net-theirs", "vpc-7d738f2a", "10.3.0.0/16", "another-instance"),
				ec2Network("net-legacy", "vpc-legacy", "10.4.0.0/16", ""),
			}

			for _, n := range snapshot {
				daemon.seedFromSummary(h, n, true) // no gateway attached
			}

			// When: the startup reconcile runs.
			h.reconcileNetworks(ctx, snapshot)

			// Then: only our own orphan goes.
			if got := daemon.removedNetworks(); len(got) != 1 || got[0] != "net-mine-orphan" {
				t.Fatalf("removed %v, want only net-mine-orphan", got)
			}
			// And: the live VPC still names its network.
			got, aerr := h.store.getVPC(ctx, "vpc-mine-live")
			if aerr != nil {
				t.Fatalf("getVPC: %v", aerr.Message)
			}
			if got.DockerNetworkID != "net-mine-live" {
				t.Fatalf("after reconcile the live VPC names %q, want net-mine-live", got.DockerNetworkID)
			}
		})
	}
}

func TestReconcileNetworks_removesNothingWithoutAnIdentity(t *testing.T) {
	daemon := newOwnershipDaemon(t)
	h := hostHandler(t, daemon, "strict")
	// Given: a store that cannot say who this instance is.
	h.instances = serviceutil.NewInstanceDomain(nil, nsInstance)

	// When: the reconcile sees an unclaimed network, even one whose label
	// would have matched an identity had one resolved.
	h.reconcileNetworks(context.Background(), []docker.NetworkSummary{
		ec2Network("net-unclaimed", "vpc-gone", "10.5.0.0/16", ""),
		ec2Network("net-theirs", "vpc-7d738f2a", "10.3.0.0/16", "another-instance"),
	})

	// Then: nothing is removed. Ownership cannot be established, and an
	// unremovable orphan is the cheaper mistake.
	if got := daemon.removedNetworks(); len(got) != 0 {
		t.Fatalf("removed %v, want nothing", got)
	}
}

func TestCreateDockerVPCNetwork_stampsTheInstanceLabel(t *testing.T) {
	daemon := newOwnershipDaemon(t)
	h := hostHandler(t, daemon, "shared")
	ctx := context.Background()

	if _, err := h.createDockerVPCNetwork(ctx, &VPC{VpcID: "vpc-1", CidrBlock: "10.9.0.0/16"}); err != nil {
		t.Fatalf("createDockerVPCNetwork: %v", err)
	}

	daemon.mu.Lock()
	defer daemon.mu.Unlock()
	if len(daemon.created) != 1 {
		t.Fatalf("saw %d creates, want 1", len(daemon.created))
	}
	labels := daemon.created[0]
	if want := h.instances.Resolve(ctx); labels[docker.LabelInstance] != want {
		t.Errorf("%s = %q, want this instance's identity %q", docker.LabelInstance, labels[docker.LabelInstance], want)
	}
	if labels[docker.LabelManaged] != "true" || labels[docker.LabelService] != "ec2" ||
		labels[docker.LabelResourceID] != "vpc-1" || labels["overcast.vpc-id"] != "vpc-1" {
		t.Errorf("the standard labels are not intact: %v", labels)
	}
}

// ─── the flip lock's key ────────────────────────────────────────────────────

// A flip removes the network and creates it again, so the ID the lock was taken
// on is gone by the time renameVPCNetwork moves the records onto the successor.
// Keyed by ID, the holder would go on holding a mutex nothing will ever look up
// again — and a sharer arriving in the window between the recreate and the
// record write would take a different, free mutex and walk straight into the
// same endpoints.
//
// Keyed by the network's name, which recreateDockerVPCNetwork preserves, the
// successor resolves to the same key and the exclusion holds all the way
// through record().
func TestLockVPCNetwork_holdsAcrossTheSuccessorIDWindow(t *testing.T) {
	daemon := newOwnershipDaemon(t)
	h := hostHandler(t, daemon, "shared")
	ctx := context.Background()

	// Two VPCs sharing one Docker network, as the shared strategy produces: both
	// records name the *owner's* network name, which is what the lock keys on.
	const netName = "overcast-vpc-vpc-a"
	for _, id := range []string{"vpc-a", "vpc-b"} {
		if aerr := h.store.putVPC(ctx, &VPC{
			VpcID: id, CidrBlock: "10.1.0.0/16",
			DockerNetworkID: "net-1", DockerNetworkName: netName,
			NetworkStatus: vpcNetworkStatusOK,
		}); aerr != nil {
			t.Fatalf("putVPC: %v", aerr.Message)
		}
	}
	// The network is deliberately absent from the daemon. That is the state
	// during a flip — removed, successor not yet recorded — and it is the branch
	// a Docker-resolved key got wrong: the inspect 404s, the fallback keys on
	// the id, and the sharer takes a free mutex. Keying off the record needs no
	// inspect, so the absence changes nothing.

	_, unlock, aerr := h.lockVPCNetwork(ctx, "vpc-a")
	if aerr != nil {
		t.Fatalf("lockVPCNetwork(vpc-a): %v", aerr.Message)
	}

	// The flip completes: the network is recreated under the same name with a
	// new ID, and the records move. This is the window — the holder is still
	// inside record(), and its lock was taken on net-1.
	for _, id := range []string{"vpc-a", "vpc-b"} {
		vpc, _ := h.store.getVPC(ctx, id)
		vpc.DockerNetworkID = "net-2"
		if aerr := h.store.putVPC(ctx, vpc); aerr != nil {
			t.Fatalf("putVPC: %v", aerr.Message)
		}
	}

	// The sharer arrives now, resolving net-2.
	acquired := make(chan struct{})
	go func() {
		_, unlockB, aerr := h.lockVPCNetwork(context.Background(), "vpc-b")
		if aerr == nil {
			unlockB()
		}
		close(acquired)
	}()

	select {
	case <-acquired:
		t.Fatal("the sharer acquired the lock while the flip still held it — the successor ID " +
			"resolved to a different mutex, which is the bug this keying exists to prevent")
	case <-time.After(150 * time.Millisecond):
	}

	unlock()
	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("the sharer never acquired the lock after it was released")
	}
}

// A VPC on a genuinely different network must not be made to wait on this one.
func TestLockVPCNetwork_differentNetworksDoNotContend(t *testing.T) {
	daemon := newOwnershipDaemon(t)
	h := hostHandler(t, daemon, "strict")
	ctx := context.Background()

	for i, id := range []string{"vpc-a", "vpc-b"} {
		netID := fmt.Sprintf("net-%d", i+1)
		if aerr := h.store.putVPC(ctx, &VPC{
			VpcID: id, CidrBlock: fmt.Sprintf("10.%d.0.0/16", i+1),
			DockerNetworkID: netID, DockerNetworkName: "overcast-vpc-" + id,
			NetworkStatus: vpcNetworkStatusOK,
		}); aerr != nil {
			t.Fatalf("putVPC: %v", aerr.Message)
		}
	}

	_, unlockA, aerr := h.lockVPCNetwork(ctx, "vpc-a")
	if aerr != nil {
		t.Fatalf("lockVPCNetwork(vpc-a): %v", aerr.Message)
	}
	defer unlockA()

	done := make(chan struct{})
	go func() {
		_, unlockB, aerr := h.lockVPCNetwork(context.Background(), "vpc-b")
		if aerr == nil {
			unlockB()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a VPC on a different network waited on this one's lock")
	}
}
