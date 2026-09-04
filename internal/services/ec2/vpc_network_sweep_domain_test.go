package ec2

// vpc_network_sweep_domain_test.go — the sweep domain outlives the store that
// names it.
//
// A VPC network is removable only when it carries this instance's
// overcast.instance label (vpc_network_ownership_test.go), and the value of
// that label is the identity of the *state store* — deliberately, so that two
// Overcasts sharing a daemon each keep their hands off the other's networks.
//
// That identity is a UUID kept in a row *inside* the store it identifies
// (serviceutil.InstanceIdentity). Wipe the data directory and the next start
// mints a new one, so the networks the previous incarnation created for this
// same data directory now carry a label matching nothing. They are then
// neither adopted nor removed — the reclaim that should free their CIDR is not
// allowed to see them — and every wipe strands another /16 on the daemon:
//
//	create network overcast-vpc-vpc-b7732331: 403: invalid pool request:
//	Pool overlaps with other one on this address space
//
// after which the VPC is recorded unbacked, CreateVpc still answers 200, and
// the deploy fails minutes later somewhere unrelated (an RDS instance, whose
// launchability check is the first thing to actually read the status).
//
// The fix is for the sweep domain to be as durable as the thing it names: the
// data directory, not a row inside it. These two tests are the pair that
// pins it — the first that a wiped store still recognises its own litter, the
// second that #1568's protection is untouched, because a *different* data
// directory is still a different domain.

import (
	"context"
	"testing"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/state"
	"go.uber.org/zap"

	"strings"
)

// sweepDomainHandler is hostHandler with the two things this file varies made
// explicit: which data directory the instance is running against, and which
// store it has. A wipe is the same directory with a new store; a second
// instance is a different directory.
func sweepDomainHandler(t *testing.T, d *ownershipDaemon, dataDir string, store state.Store) *Handler {
	t.Helper()
	origInContainer := runningInContainer
	t.Cleanup(func() { runningInContainer = origInContainer })
	runningInContainer = func() bool { return false }

	cfg := &config.Config{
		Region: "us-east-1", AccountID: "000000000000",
		EC2VPCNetworkStrategy: "shared",
		Network:               "overcast",
		DataDir:               dataDir,
	}
	h := New(cfg, store, zap.NewNop(), clock.NewMock()).handler
	h.docker = docker.NewClient(strings.Replace(d.server.URL, "http://", "tcp://", 1), zap.NewNop())
	h.dockerReady.Store(true)
	return h
}

// TestReconcileNetworks_reclaimsItsOwnNetworksAfterTheStoreIsWiped is the
// regression: clearing the state directory and redeploying must not strand the
// CIDR the previous incarnation was using.
func TestReconcileNetworks_reclaimsItsOwnNetworksAfterTheStoreIsWiped(t *testing.T) {
	daemon := newOwnershipDaemon(t)
	ctx := context.Background()
	dataDir := t.TempDir()

	// Given: an instance that created a VPC network for this data directory.
	before := sweepDomainHandler(t, daemon, dataDir, state.NewMemoryStore())
	owner := before.instances.Resolve(ctx)
	if owner == "" {
		t.Fatal("the first instance has no identity; the network below would carry no label")
	}
	orphan := ec2Network("net-wiped-orphan", "vpc-3aca580d", "10.42.0.0/16", owner)
	daemon.seedFromSummary(before, orphan, true)

	// When: the same data directory comes back with an empty store — the state
	// was wiped, the daemon was not — and the startup reconcile runs. No VPC
	// record claims the network, because the wipe took the record with it.
	after := sweepDomainHandler(t, daemon, dataDir, state.NewMemoryStore())
	after.reconcileNetworks(ctx, []docker.NetworkSummary{orphan})

	// Then: it is reclaimed. This data directory made it, and this data
	// directory is what the sweep domain names — so it is our own litter, and
	// leaving it holds 10.42.0.0/16 against the very next deploy.
	if got := daemon.removedNetworks(); len(got) != 1 || got[0] != "net-wiped-orphan" {
		t.Fatalf("removed %v, want only net-wiped-orphan: a wiped store no longer recognises "+
			"the networks it created, so their CIDR is stranded for good", got)
	}
}

// TestReconcileNetworks_stillLeavesAnotherDataDirectorysNetworks is #1568's
// protection, restated against the durable domain: the reason the label exists
// at all is that running the ECS integration package on a developer machine
// deleted the live instance's network. A second instance is a second data
// directory, and must stay invisible.
func TestReconcileNetworks_stillLeavesAnotherDataDirectorysNetworks(t *testing.T) {
	daemon := newOwnershipDaemon(t)
	ctx := context.Background()

	// Given: another instance's network, created against its own data directory.
	theirs := sweepDomainHandler(t, daemon, t.TempDir(), state.NewMemoryStore())
	theirOwner := theirs.instances.Resolve(ctx)
	live := ec2Network("net-theirs-live", "vpc-7d738f2a", "10.3.0.0/16", theirOwner)
	daemon.seedFromSummary(theirs, live, true)

	// When: a different instance — a test server, say — reconciles over a
	// snapshot that includes it, with nothing in its own store claiming it.
	mine := sweepDomainHandler(t, daemon, t.TempDir(), state.NewMemoryStore())
	if mine.instances.Resolve(ctx) == theirOwner {
		t.Fatal("two data directories resolved to one sweep domain; they must not share one")
	}
	mine.reconcileNetworks(ctx, []docker.NetworkSummary{live})

	// Then: it is left alone.
	if got := daemon.removedNetworks(); len(got) != 0 {
		t.Fatalf("removed %v, want nothing: another data directory's network is not ours to take", got)
	}
}
