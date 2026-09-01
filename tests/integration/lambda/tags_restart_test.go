//go:build !nosqlite

// Lambda tag tests that need a real durable store across a restart. Under
// -tags nosqlite, state.NewSQLiteStore and state.NewHybridStore are stubs that
// always return "not compiled with SQLite support" (see
// internal/state/sqlite_hybrid_nosqlite.go), so a build with that tag has no
// persistent backend at all and a restart cannot preserve anything — for any
// resource, not just tags. Guarding the file mirrors
// tests/integration/router/sqlite_test.go and internal/router/debug_hybrid_test.go.
//
// The tag write itself is not build-sensitive: TestCreateEventSourceMapping_StoresTags
// in tags_test.go covers it and runs under every tag set.

package lambda_test

import (
	"context"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/internal/state"
	"github.com/overcast-sh/overcast/tests/helpers"
)

// Tags supplied at create time are ordinary persisted state: an emulator
// restart against the same data directory must still report them.
func TestCreateEventSourceMapping_TagsSurviveRestart(t *testing.T) {
	// Given: a mapping created with tags against a durable store.
	dataDir := t.TempDir()
	first := openReadyHybridStore(t, dataDir)
	srv := helpers.NewTestServer(t, helpers.WithStore(first))
	createFunction(t, srv, "esm-restart-tag-fn")
	esm := createESM(t, srv, map[string]any{
		"FunctionName":   "esm-restart-tag-fn",
		"EventSourceArn": sqsARN("esm-restart-tag-queue"),
		"Tags":           map[string]string{"team": "platform"},
	})
	esmARN, _ := esm["EventSourceMappingArn"].(string)
	if esmARN == "" {
		t.Fatalf("event source mapping has no ARN: %v", esm)
	}

	// When: the store is closed and reopened, as a restart would.
	if err := first.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	second := openReadyHybridStore(t, dataDir)
	t.Cleanup(func() { _ = second.Close() })
	restarted := helpers.NewTestServer(t, helpers.WithStore(second))

	// Then: the tags are still there. A namespace missing from the state
	// tiering registration is never seeded back into HybridStore, so this is
	// the assertion that catches it.
	if tags := listResourceTags(t, restarted, esmARN); tags["team"] != "platform" {
		t.Fatalf("tags after restart = %v, want team=platform", tags)
	}
}

// openReadyHybridStore opens a durable store over dataDir and waits out its
// background schema migration, so the first request after a (re)start is not
// answered with the "still migrating" 503.
func openReadyHybridStore(t *testing.T, dataDir string) *state.HybridStore {
	t.Helper()
	store, err := state.NewHybridStore(dataDir, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("open store %s: %v", dataDir, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := store.WaitReady(ctx); err != nil {
		t.Fatalf("store %s not ready: %v", dataDir, err)
	}
	return store
}
