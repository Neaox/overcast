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
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
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
}

func newOwnershipDaemon(t *testing.T) *ownershipDaemon {
	t.Helper()
	d := &ownershipDaemon{networks: map[string]bool{}}
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
