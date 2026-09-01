//go:build !nosqlite

// Router integration tests that need a real SQLite-backed store. Under
// -tags nosqlite, state.NewSQLiteStore and state.NewHybridStore are stubs that
// always return "not compiled with SQLite support" (see
// internal/state/sqlite_hybrid_nosqlite.go), so these tests cannot run there.
// Guarding the file mirrors internal/router/debug_hybrid_test.go and
// internal/services/dynamodb/index_store_sqlite_test.go.
package router_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/internal/state"
	"github.com/overcast-sh/overcast/tests/helpers"
)

// TestReset_withSQLiteStore covers the non-MemoryStore branch of resetHandler
// (the resetAllNamespaces code path).
func TestReset_withSQLiteStore(t *testing.T) {
	sqliteStore, err := state.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { sqliteStore.Close() })

	// Wait for the background schema migration to finish before issuing any
	// requests. Without this, a request that races migration now correctly
	// gets a fast 503 (middleware.NotReady, storage-plan.md) instead of
	// silently blocking-then-succeeding the way it used to — this test cares
	// about debug reset behavior against a SQLiteStore backend, not about
	// exercising that race, so synchronize past it explicitly. SQLiteStore
	// has no ReadyAwaiter of its own; any real operation blocks on the same
	// internal gate migration completion closes.
	if _, _, err := sqliteStore.Get(context.Background(), "warmup", "warmup"); err != nil {
		t.Fatalf("warm-up Get (waiting for migration): %v", err)
	}

	srv := helpers.NewTestServer(t, helpers.WithStore(sqliteStore))

	// Create a queue to populate state.
	body, _ := json.Marshal(map[string]any{"QueueName": "sqlite-reset-queue"})
	createReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(body))
	createReq.Header.Set("Content-Type", "application/x-amz-json-1.0")
	createReq.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")

	createResp, err := http.DefaultClient.Do(createReq)
	if err != nil {
		t.Fatal(err)
	}
	createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)

	// Reset via the always-on endpoint — exercises resetAllNamespaces.
	resetResp, err := http.Post(srv.URL+"/_overcast/reset", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resetResp.Body.Close()

	helpers.AssertStatus(t, resetResp, http.StatusOK)
	var result map[string]string
	helpers.DecodeJSON(t, resetResp, &result)
	if result["status"] != "reset" {
		t.Errorf("expected status 'reset', got %q", result["status"])
	}
}
