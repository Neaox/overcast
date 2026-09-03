package ec2

// vpc_network_reset_test.go — a state reset must not orphan the networks this
// instance created (#1605).
//
// The sweep removes only what carries *this* instance's identity, which is what
// stops one Overcast deleting another's live VPC networks (#1569). It also
// means the identity is the only thing standing between "this is my litter" and
// "this is somebody else's, leave it alone" — so a reset that wiped the
// identity along with the state turned every network the instance had created
// into litter nothing would ever collect.
//
// These tests work against the reset's *contract* rather than calling the
// handler: internal/router imports this package, so a test here cannot call
// resetStore. wipeState models what the router does, both ways round, and the
// pair shows which half does the damage.

import (
	"context"
	"testing"

	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

// wipeState deletes every key in the handler's store, as POST /_overcast/reset
// does. keepIdentity models the exemption the router applies to
// serviceutil.IsInstanceNamespace — pass false for the behaviour before #1605.
//
// **It is a model, and the seam is worth naming.** The router *captures the
// identities and restores them* around the wipe, because MemoryStore.Reset
// drops every namespace at once and cannot skip anything; this *skips* them
// during it. The two are equivalent for everything asserted below — what the
// store holds afterwards — and not equivalent in shape, so these tests would
// not notice if resetStore's shape changed under them. That is deliberate:
// what these cover is the consequence for the sweep, and the router's own
// tests (internal/router/reset_instance_identity_test.go) cover the mechanism,
// on both wipe paths.
func wipeState(t *testing.T, h *Handler, keepIdentity bool) {
	t.Helper()
	ctx := context.Background()
	st := h.store.store
	namespaces, err := st.ListNamespaces(ctx)
	if err != nil {
		t.Fatalf("ListNamespaces: %v", err)
	}
	for _, ns := range namespaces {
		if keepIdentity && serviceutil.IsInstanceNamespace(ns) {
			continue
		}
		keys, err := st.List(ctx, ns, "")
		if err != nil {
			t.Fatalf("List %s: %v", ns, err)
		}
		for _, k := range keys {
			if err := st.Delete(ctx, ns, k); err != nil {
				t.Fatalf("Delete %s/%s: %v", ns, k, err)
			}
		}
	}
}

// restart models the next start against the same store: a fresh InstanceDomain,
// since the running process has the identity memoized and would look fine
// either way. That memoization is why the bug reported clean until a restart.
func restart(h *Handler) string {
	h.instances = serviceutil.NewInstanceDomain(h.store.store, nsInstance)
	return h.instances.Resolve(context.Background())
}

func TestReset_networksCreatedBeforeItAreStillOursAfterIt(t *testing.T) {
	for _, strategy := range []string{"shared", "strict", "remapped"} {
		t.Run(strategy, func(t *testing.T) {
			daemon := newOwnershipDaemon(t)
			h := hostHandler(t, daemon, strategy)
			ctx := context.Background()
			mine := h.instances.Resolve(ctx)
			if mine == "" {
				t.Fatal("the handler has no instance identity; nothing below would be removable")
			}

			// Given: a VPC of ours, backed by a network stamped with our identity.
			vpc := &VPC{VpcID: "vpc-before-reset", CidrBlock: "10.1.0.0/16",
				DockerNetworkID: "net-before-reset", NetworkStatus: vpcNetworkStatusOK}
			if aerr := h.store.putVPC(ctx, vpc); aerr != nil {
				t.Fatalf("putVPC: %v", aerr.Message)
			}
			snapshot := []docker.NetworkSummary{
				ec2Network("net-before-reset", "vpc-before-reset", "10.1.0.0/16", mine),
			}
			for _, n := range snapshot {
				daemon.seedFromSummary(h, n, true)
			}

			// When: the state is reset and Overcast starts again.
			wipeState(t, h, true)
			if got := restart(h); got != mine {
				t.Fatalf("identity after reset = %q, want %q — every network it created is now unclaimable",
					got, mine)
			}
			h.reconcileNetworks(ctx, snapshot)

			// Then: the network the reset orphaned is recognised as ours and
			// swept, rather than left on the daemon for ever.
			if got := daemon.removedNetworks(); len(got) != 1 || got[0] != "net-before-reset" {
				t.Fatalf("removed %v, want net-before-reset — the reset's own litter was not collected", got)
			}
		})
	}
}

// The other half, and the reason the fix is where it is. With the identity gone
// the network is indistinguishable from a live neighbour's, so the sweep leaves
// it alone — correctly, on the evidence available to it. That conservatism is
// right and is not what changed; what changed is that a reset no longer
// destroys the evidence.
func TestReset_withoutTheIdentityTheSameNetworkIsStranded(t *testing.T) {
	daemon := newOwnershipDaemon(t)
	h := hostHandler(t, daemon, "strict")
	ctx := context.Background()
	mine := h.instances.Resolve(ctx)

	vpc := &VPC{VpcID: "vpc-before-reset", CidrBlock: "10.1.0.0/16",
		DockerNetworkID: "net-before-reset", NetworkStatus: vpcNetworkStatusOK}
	if aerr := h.store.putVPC(ctx, vpc); aerr != nil {
		t.Fatalf("putVPC: %v", aerr.Message)
	}
	snapshot := []docker.NetworkSummary{
		ec2Network("net-before-reset", "vpc-before-reset", "10.1.0.0/16", mine),
	}
	for _, n := range snapshot {
		daemon.seedFromSummary(h, n, true)
	}

	wipeState(t, h, false) // the pre-#1605 reset: the identity goes too
	if got := restart(h); got == mine || got == "" {
		t.Fatalf("identity after reset = %q, want a freshly minted one different from %q", got, mine)
	}
	h.reconcileNetworks(ctx, snapshot)

	if got := daemon.removedNetworks(); len(got) != 0 {
		t.Fatalf("removed %v, want nothing — a network it cannot prove it created is never removed", got)
	}
}
