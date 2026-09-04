package router

// reset_instance_identity_test.go — a reset wipes the emulated state and keeps
// the instance (#1605).
//
// The identity is stamped into `overcast.instance` on every Docker network,
// container and volume Overcast creates, and it is what the orphan sweep
// compares against to decide whether it created a resource. Wiping it with the
// state made every one of those resources unclaimable: the next start minted a
// fresh identity, could no longer prove it had created them, and left them
// alone for ever. One leaked Docker network per VPC per reset.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

// identityOf reads a service's sweep-domain identity straight out of a store.
func identityOf(t *testing.T, st state.Store, namespace string) string {
	t.Helper()
	id, _, err := st.Get(context.Background(), namespace, serviceutil.InstanceKey)
	if err != nil {
		t.Fatalf("read %s: %v", namespace, err)
	}
	return id
}

// plainStore is a state.Store that is not a *state.MemoryStore, so resetStore
// takes the generic list-and-delete path instead of the MemoryStore fast path.
//
// It exists because the branch that matters in production is the one the fast
// path skips. #1605 is by nature a persistent-backend bug — on a MemoryStore
// the identity is lost at the next start whatever this code does, so the only
// deployments the fix changes anything for (SQLite, WAL, hybrid) are exactly
// the ones running resetAllNamespaces. Two MemoryStores in the subtest below
// would have left it uncovered while the comment claimed otherwise.
type plainStore struct{ state.Store }

func postReset(t *testing.T, st state.Store) {
	t.Helper()
	rec := httptest.NewRecorder()
	resetHandler(st, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/_overcast/reset", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body.String())
	}
}

func TestReset_keepsEveryServiceIdentityAndWipesEverythingElse(t *testing.T) {
	store := state.NewMemoryStore()
	ctx := context.Background()

	// Given: two services that stamp docker.LabelInstance have resolved an
	// identity, and there is ordinary emulated state beside it.
	ec2ID := serviceutil.InstanceIdentity(ctx, store, "ec2:instance", serviceutil.Anchor{})
	lambdaID := serviceutil.InstanceIdentity(ctx, store, "lambda:instance", serviceutil.Anchor{})
	if ec2ID == "" || lambdaID == "" {
		t.Fatal("no identity was minted; the rest of this test proves nothing")
	}
	if err := store.Set(ctx, "ec2:vpcs", "vpc-1", `{"VpcId":"vpc-1"}`); err != nil {
		t.Fatal(err)
	}

	postReset(t, store)

	// Then: the VPC is gone and both identities are exactly what they were.
	if _, found, _ := store.Get(ctx, "ec2:vpcs", "vpc-1"); found {
		t.Error("the reset did not wipe emulated state")
	}
	if got := identityOf(t, store, "ec2:instance"); got != ec2ID {
		t.Errorf("ec2 identity = %q, want %q — every network it created is now unclaimable", got, ec2ID)
	}
	if got := identityOf(t, store, "lambda:instance"); got != lambdaID {
		t.Errorf("lambda identity = %q, want %q", got, lambdaID)
	}
}

// The value a later start reads has to be the same one, which is the thing the
// bug actually broke: the running process had it memoized and looked fine.
func TestReset_theNextStartReadsTheSameIdentity(t *testing.T) {
	store := state.NewMemoryStore()
	ctx := context.Background()
	before := serviceutil.InstanceIdentity(ctx, store, "ec2:instance", serviceutil.Anchor{})

	postReset(t, store)

	// A fresh InstanceDomain, as a restarted process would build.
	after := serviceutil.NewInstanceDomain(store, "ec2:instance").Resolve(ctx)
	if after != before {
		t.Fatalf("identity after reset = %q, want %q — a new one orphans every labelled resource", after, before)
	}
}

// Both wipe paths, because they lose the identity in different ways.
// MemoryStore.Reset drops every namespace at once and cannot skip anything;
// resetAllNamespaces walks and deletes, and is the path every persistent
// backend takes — see plainStore.
func TestReset_keepsTheIdentityOnBothWipePaths(t *testing.T) {
	t.Run("memory fast path", func(t *testing.T) {
		store := state.NewMemoryStore()
		before := serviceutil.InstanceIdentity(context.Background(), store, "ec2:instance", serviceutil.Anchor{})
		postReset(t, store)
		if got := identityOf(t, store, "ec2:instance"); got != before {
			t.Errorf("identity = %q, want %q", got, before)
		}
	})

	t.Run("generic path", func(t *testing.T) {
		store := &plainStore{state.NewMemoryStore()}
		ctx := context.Background()
		before := serviceutil.InstanceIdentity(ctx, store, "ec2:instance", serviceutil.Anchor{})
		if err := store.Set(ctx, "ec2:vpcs", "vpc-1", `{"VpcId":"vpc-1"}`); err != nil {
			t.Fatal(err)
		}

		postReset(t, store)

		if _, found, _ := store.Get(ctx, "ec2:vpcs", "vpc-1"); found {
			t.Error("the generic wipe did not clear emulated state")
		}
		if got := identityOf(t, store, "ec2:instance"); got != before {
			t.Errorf("identity = %q, want %q", got, before)
		}
	})

	t.Run("namespaced store, per underlying store", func(t *testing.T) {
		// Each backend has to come back with its own identity: a
		// NamespacedStore can route two services to two different stores. One
		// of them is not a MemoryStore, so the two branches of resetStore run
		// side by side over one wrapper — which is the arrangement a real
		// deployment with a per-service backend override actually has.
		defaultStore := state.NewMemoryStore()
		lambdaStore := &plainStore{state.NewMemoryStore()}
		ns := state.NewNamespacedStore(defaultStore, map[string]state.Store{
			"lambda": lambdaStore,
		})
		ctx := context.Background()
		ec2ID := serviceutil.InstanceIdentity(ctx, ns, "ec2:instance", serviceutil.Anchor{})
		lambdaID := serviceutil.InstanceIdentity(ctx, ns, "lambda:instance", serviceutil.Anchor{})
		if err := ns.Set(ctx, "lambda:functions", "f1", `{}`); err != nil {
			t.Fatal(err)
		}

		postReset(t, ns)

		if _, found, _ := lambdaStore.Get(ctx, "lambda:functions", "f1"); found {
			t.Error("the routed store's state was not wiped")
		}
		if got := identityOf(t, defaultStore, "ec2:instance"); got != ec2ID {
			t.Errorf("ec2 identity = %q, want %q", got, ec2ID)
		}
		if got := identityOf(t, lambdaStore, "lambda:instance"); got != lambdaID {
			t.Errorf("lambda identity on the routed store = %q, want %q", got, lambdaID)
		}
	})
}

// `overcast reset ec2` reaches "ec2:instance" too — it matches the same prefix
// as every other ec2 namespace — so it needs the same exemption.
func TestResetService_keepsTheIdentityOfTheServiceItWipes(t *testing.T) {
	store := state.NewMemoryStore()
	ctx := context.Background()
	before := serviceutil.InstanceIdentity(ctx, store, "ec2:instance", serviceutil.Anchor{})
	if err := store.Set(ctx, "ec2:vpcs", "vpc-1", `{"VpcId":"vpc-1"}`); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/_overcast/reset/ec2", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("service", "ec2")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	resetServiceHandler(store, nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset ec2: %d %s", rec.Code, rec.Body.String())
	}

	if _, found, _ := store.Get(ctx, "ec2:vpcs", "vpc-1"); found {
		t.Error("the service reset did not wipe the service's state")
	}
	if got := identityOf(t, store, "ec2:instance"); got != before {
		t.Errorf("identity = %q, want %q", got, before)
	}
}
