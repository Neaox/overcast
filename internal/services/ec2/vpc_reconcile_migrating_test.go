package ec2

// vpc_reconcile_migrating_test.go — the startup reconcile does not run against
// a store that is still migrating.
//
// The hybrid store opens SQLite and migrates its schema in the background, and
// reads through it see none of the records it holds until that finishes. The
// Docker reconcile fires on the daemon's connected event, which does not wait
// for any of it: on an upgrade that carried a schema migration, the pass ran
// about eight seconds before the store's seed completed.
//
// Reading no VPCs, it took every EC2 network on the daemon for an orphan — by
// no VPC in any region — and removed it. The seed then loaded the VPC records
// naming networks that had just been deleted, and because the pass had set
// reconciledAll on its way out, the per-region backstop never looked again.
// Every ECS task and Lambda placed in that VPC failed with
//
//	ResourceInitializationError: … connect to VPC network <id>: 404:
//	{"message":"network <id> not found"}
//
// for the life of the process, through redeploys and rollbacks alike.

import (
	"context"
	"testing"

	"github.com/overcast-sh/overcast/internal/state"
)

// migratingStore wraps a store the way HybridStore behaves while its one-time
// schema migration is in flight: it reports NotReady, and reads return nothing
// — not an error, which the pass already handles, but an empty result
// indistinguishable from a store that genuinely holds no VPCs. That
// indistinguishability is the whole bug, so the fake reproduces it exactly.
type migratingStore struct {
	state.Store
	migrating bool
}

func (s *migratingStore) NotReady() bool { return s.migrating }

func (s *migratingStore) List(ctx context.Context, namespace, prefix string) ([]string, error) {
	if s.migrating {
		return []string{}, nil
	}
	return s.Store.List(ctx, namespace, prefix)
}

func (s *migratingStore) Scan(ctx context.Context, namespace, prefix string) ([]state.KV, error) {
	if s.migrating {
		return []state.KV{}, nil
	}
	return s.Store.Scan(ctx, namespace, prefix)
}

func (s *migratingStore) Get(ctx context.Context, namespace, key string) (string, bool, error) {
	if s.migrating {
		return "", false, nil
	}
	return s.Store.Get(ctx, namespace, key)
}

func TestReconcileNetworks_doesNotSweepWhileTheStoreIsStillMigrating(t *testing.T) {
	for _, strategy := range []string{"shared", "strict", "remapped"} {
		t.Run(strategy, func(t *testing.T) {
			// Given: a VPC backed by a network the daemon has — and a store
			// that has since been restarted into its migration window, so
			// nothing reads back through it yet.
			f := newFakeVPCDocker(t)
			store := &migratingStore{Store: state.NewMemoryStore()}
			h := vpcDockerHandlerOn(t, f, strategy, store)
			vpcID := createVPCIn(t, h, otherRegion, "10.0.0.0/16")
			netID := storedVPCIn(t, h, otherRegion, vpcID).DockerNetworkID
			if netID == "" || !f.has(netID) {
				t.Fatalf("the VPC is not backed before the restart: network %q present=%t", netID, f.has(netID))
			}
			store.migrating = true

			// When: the daemon's connected event fires the startup pass before
			// the migration has finished.
			h.reconcileNetworks(context.Background(), f.summaries())

			// Then: the network is still there. A record it could not read is
			// not a record that does not exist, and deleting the network under
			// one leaves the VPC unplaceable for the life of the process.
			if !f.has(netID) {
				t.Fatal("the VPC's network was swept as an orphan by a pass that could not read the store")
			}
			// And: the pass does not vouch for the regions it did not cover,
			// so the per-region backstop still has one to make.
			if h.reconciledAll.Load() {
				t.Error("reconciledAll is set after a pass over a migrating store; the backstop will never run")
			}
		})
	}
}

func TestReconcileNetworks_backstopHealsTheRegionOnceTheStoreFinishes(t *testing.T) {
	// Given: the same restart — a pass that ran too early, and a network that
	// went with the daemon rather than being swept, so the record is stale by
	// the time the store finishes.
	f := newFakeVPCDocker(t)
	store := &migratingStore{Store: state.NewMemoryStore()}
	h := vpcDockerHandlerOn(t, f, "shared", store)
	putStaleVPC(t, h, otherRegion, otherVPCID, "10.0.0.0/16")

	store.migrating = true
	h.reconcileNetworks(context.Background(), f.summaries())
	store.migrating = false

	// When: something is placed in that region — the first ECS RunTask or
	// Lambda invoke to ask which network the VPC is on.
	svc := &Service{handler: h, log: h.log}
	got := svc.DockerNetworkForVpc(regionCtx(otherRegion), otherVPCID)

	// Then: the backstop reconciled the region against the store as it now
	// reads, and the answer names a network the daemon actually has.
	if got == "" {
		t.Fatal("DockerNetworkForVpc returned nothing; the region was never reconciled")
	}
	if !f.has(got) {
		t.Errorf("DockerNetworkForVpc = %q, which the daemon does not have; the stale record was never healed", got)
	}
}
