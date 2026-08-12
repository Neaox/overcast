package serviceutil_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const testNS = "test:instance"

// The identity is stable for as long as the store holding it is, which is what
// lets a sweep recognise its own resources after a restart — and what stops it
// recognising anything else's.
func TestInstanceIdentity_stableWithinAStore(t *testing.T) {
	ctx := context.Background()
	st := state.NewMemoryStore()

	first := serviceutil.InstanceIdentity(ctx, st, testNS)
	if first == "" {
		t.Fatal("expected an identity to be minted")
	}
	if again := serviceutil.InstanceIdentity(ctx, st, testNS); again != first {
		t.Fatalf("identity changed within one store: %q then %q", first, again)
	}
	// A separate store is a separate sweep domain — a memory-backed restart,
	// or another instance with its own data directory.
	if other := serviceutil.InstanceIdentity(ctx, state.NewMemoryStore(), testNS); other == first {
		t.Fatal("separate stores must not yield the same identity")
	}
}

// failingStore fails every read. The embedded nil interface would panic on any
// other method, which is the point: nothing else should be reached.
type failingStore struct{ state.Store }

func (failingStore) Get(context.Context, string, string) (string, bool, error) {
	return "", false, errors.New("store unavailable")
}

// A store that cannot answer means ownership cannot be established. Callers
// read "" as "sweep nothing" — a store being briefly unavailable is not
// evidence that anything on the Docker daemon is litter.
func TestInstanceIdentity_unavailableStoreYieldsNoIdentity(t *testing.T) {
	if got := serviceutil.InstanceIdentity(context.Background(), failingStore{}, testNS); got != "" {
		t.Fatalf("expected no identity from a failing store, got %q", got)
	}
	if got := serviceutil.InstanceIdentity(context.Background(), nil, testNS); got != "" {
		t.Fatalf("expected no identity from a nil store, got %q", got)
	}
}
