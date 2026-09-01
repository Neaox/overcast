package inert_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/inert"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

// widget is a stand-in for a generated record type: a plain struct with json
// tags, which is all §4.2 asks a Tier 1 record to be.
type widget struct {
	Id        string    `json:"Id"`
	Name      string    `json:"Name"`
	CreatedAt time.Time `json:"CreatedAt"`
}

const ns = "test:widgets"

func newConfig(t *testing.T, clk clock.Clock, region func(context.Context) string) (inert.Config, state.Store) {
	t.Helper()
	st := state.NewMemoryStore()
	t.Cleanup(func() { _ = st.Close() })
	return inert.Config{Store: st, Clock: clk, Region: region}, st
}

func TestStore_PutGetRoundTrips(t *testing.T) {
	// Given: a store over an empty memory backend.
	ctx := context.Background()
	cfg, _ := newConfig(t, clock.NewMock(), nil)
	store := inert.NewStore[widget](cfg, ns)

	// When: a record is written and read back.
	want := &widget{Id: "w-1", Name: "first"}
	if err := store.Put(ctx, "w-1", want); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, found, err := store.Get(ctx, "w-1")

	// Then: it round-trips field for field.
	if err != nil || !found {
		t.Fatalf("Get = (%v, %v, %v), want found", got, found, err)
	}
	if got.Name != want.Name || got.Id != want.Id {
		t.Fatalf("Get = %+v, want %+v", got, want)
	}
}

func TestStore_GetMissingIsNotAnError(t *testing.T) {
	ctx := context.Background()
	cfg, _ := newConfig(t, clock.NewMock(), nil)
	store := inert.NewStore[widget](cfg, ns)

	got, found, err := store.Get(ctx, "nope")
	if err != nil || found || got != nil {
		t.Fatalf("Get of a missing id = (%v, %v, %v), want (nil, false, nil)", got, found, err)
	}
}

// TestStore_MalformedRecordIsSkippedNotFatal pins the behaviour route53
// hand-rolls and AGENTS.md mandates: one corrupt blob reads as absent, and a
// List over a namespace containing it still returns everything else.
func TestStore_MalformedRecordIsSkippedNotFatal(t *testing.T) {
	// Given: a namespace holding one good record and one that is not JSON.
	ctx := context.Background()
	cfg, raw := newConfig(t, clock.NewMock(), nil)
	store := inert.NewStore[widget](cfg, ns)
	if err := store.Put(ctx, "w-2", &widget{Id: "w-2", Name: "good"}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := raw.Set(ctx, ns, "w-1", "{not json"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// When: the corrupt record is read directly.
	got, found, err := store.Get(ctx, "w-1")

	// Then: it reads as absent rather than as an error.
	if err != nil || found || got != nil {
		t.Fatalf("Get of a malformed record = (%v, %v, %v), want (nil, false, nil)", got, found, err)
	}

	// And: List skips it and still returns the healthy record.
	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Id != "w-2" {
		t.Fatalf("List = %+v, want just w-2", list)
	}
}

func TestStore_ListIsStableSortedByIdentifier(t *testing.T) {
	// Given: records written in an order that is not sorted.
	ctx := context.Background()
	cfg, _ := newConfig(t, clock.NewMock(), nil)
	store := inert.NewStore[widget](cfg, ns)
	for _, id := range []string{"w-3", "w-1", "w-2"} {
		if err := store.Put(ctx, id, &widget{Id: id}); err != nil {
			t.Fatalf("Put %s: %v", id, err)
		}
	}

	// When: List is called twice with no writes in between.
	first, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	second, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	// Then: both are sorted by identifier and identical.
	want := []string{"w-1", "w-2", "w-3"}
	for i, rec := range first {
		if rec.Id != want[i] {
			t.Fatalf("List = %v, want %v", ids(first), want)
		}
		if second[i].Id != rec.Id {
			t.Fatalf("List order changed between calls: %v then %v", ids(first), ids(second))
		}
	}
}

func TestStore_DeleteRemovesAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	cfg, _ := newConfig(t, clock.NewMock(), nil)
	store := inert.NewStore[widget](cfg, ns)
	if err := store.Put(ctx, "w-1", &widget{Id: "w-1"}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := store.Delete(ctx, "w-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, found, _ := store.Get(ctx, "w-1"); found {
		t.Fatal("record still present after Delete")
	}
	if err := store.Delete(ctx, "w-1"); err != nil {
		t.Fatalf("second Delete: %v, want nil (deleting an absent key is not an error)", err)
	}
}

func TestStore_PageWalksEveryRecordExactlyOnce(t *testing.T) {
	// Given: five records and a page size of two.
	ctx := context.Background()
	cfg, _ := newConfig(t, clock.NewMock(), nil)
	store := inert.NewStore[widget](cfg, ns)
	for _, id := range []string{"w-1", "w-2", "w-3", "w-4", "w-5"} {
		if err := store.Put(ctx, id, &widget{Id: id}); err != nil {
			t.Fatalf("Put %s: %v", id, err)
		}
	}

	// When: the whole page sequence is walked.
	seen := map[string]int{}
	token, pages := "", 0
	for {
		page, err := store.Page(ctx, token, 2, serviceutil.PaginateOptions{DefaultLimit: 20, MaxLimit: 20})
		if err != nil {
			t.Fatalf("Page: %v", err)
		}
		for _, rec := range page.Items {
			seen[rec.Id]++
		}
		pages++
		if page.NextToken == "" || pages > 10 {
			break
		}
		token = page.NextToken
	}

	// Then: it took three pages and each record appeared exactly once.
	if pages != 3 {
		t.Fatalf("walked %d pages at size 2 over 5 records, want 3", pages)
	}
	for _, id := range []string{"w-1", "w-2", "w-3", "w-4", "w-5"} {
		if seen[id] != 1 {
			t.Fatalf("record %s appeared %d times, want exactly 1", id, seen[id])
		}
	}
}

// TestStore_PageRejectsAGarbageToken is pagination-plan H1/G3: an
// undecodable token must surface, not silently restart at page 1.
func TestStore_PageRejectsAGarbageToken(t *testing.T) {
	ctx := context.Background()
	cfg, _ := newConfig(t, clock.NewMock(), nil)
	store := inert.NewStore[widget](cfg, ns)
	if err := store.Put(ctx, "w-1", &widget{Id: "w-1"}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	page, err := store.Page(ctx, "not-a-real-token", 0, serviceutil.PaginateOptions{})
	if !errors.Is(err, serviceutil.ErrInvalidPageToken) {
		t.Fatalf("Page with a garbage token = (%v, %v), want ErrInvalidPageToken", page.Items, err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("Page returned %d items alongside the error — a caller ignoring the error would restart at page 1", len(page.Items))
	}
}

// TestStore_RegionScopingIsolatesRecords covers §3.5's region rule from both
// sides: a regional service's records are invisible across regions, and a
// global service (nil Region) sees one flat namespace.
func TestStore_RegionScopingIsolatesRecords(t *testing.T) {
	// Given: a store whose region comes from the request context.
	type regionKey struct{}
	regionOf := func(ctx context.Context) string {
		r, _ := ctx.Value(regionKey{}).(string)
		return r
	}
	cfg, _ := newConfig(t, clock.NewMock(), regionOf)
	store := inert.NewStore[widget](cfg, ns)

	east := context.WithValue(context.Background(), regionKey{}, "us-east-1")
	west := context.WithValue(context.Background(), regionKey{}, "eu-west-1")

	// When: the same identifier is written in two regions.
	if err := store.Put(east, "w-1", &widget{Id: "w-1", Name: "east"}); err != nil {
		t.Fatalf("Put east: %v", err)
	}
	if err := store.Put(west, "w-1", &widget{Id: "w-1", Name: "west"}); err != nil {
		t.Fatalf("Put west: %v", err)
	}

	// Then: each region reads its own record and lists only its own.
	got, found, err := store.Get(east, "w-1")
	if err != nil || !found || got.Name != "east" {
		t.Fatalf("Get in us-east-1 = (%+v, %v, %v), want the east record", got, found, err)
	}
	list, err := store.List(west)
	if err != nil || len(list) != 1 || list[0].Name != "west" {
		t.Fatalf("List in eu-west-1 = (%+v, %v), want only the west record", list, err)
	}
}

// TestStore_NowComesFromTheInjectedClock is §3.5's timestamp rule at the
// runtime level. The conformance suite catches a handler that ignores the
// clock only when the resource models a timestamp member; Organizations
// models none, so this is where the rule is proven for the runtime itself.
func TestStore_NowComesFromTheInjectedClock(t *testing.T) {
	clk := clock.NewMock()
	fixed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clk.Set(fixed)
	cfg, _ := newConfig(t, clk, nil)
	store := inert.NewStore[widget](cfg, ns)

	if got := store.Now(); !got.Equal(fixed) {
		t.Fatalf("Now = %v, want the injected %v", got, fixed)
	}
	clk.Add(72 * time.Hour)
	if got := store.Now(); !got.Equal(fixed.Add(72 * time.Hour)) {
		t.Fatalf("Now = %v after advancing the injected clock 72h, want %v — the store is not reading clock.Clock", got, fixed.Add(72*time.Hour))
	}
}

func ids(recs []*widget) []string {
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.Id)
	}
	return out
}
