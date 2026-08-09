//go:build !nosqlite

// The retained state of an update that failed with rollback disabled is only
// worth anything if it outlives the process that recorded it. Under
// -tags nosqlite, state.NewSQLiteStore and state.NewHybridStore are stubs that
// always error (internal/state/sqlite_hybrid_nosqlite.go), so such a build has
// no durable backend and a restart preserves nothing at all — see
// tests/AGENTS.md § Build-tag-sensitive tests.
//
// The retention itself is not build-sensitive: TestUpdateStack_disableRollback
// in update_disable_rollback_test.go covers it under every tag set.

package cloudformation_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/state"
	"github.com/Neaox/overcast/tests/helpers"
)

func TestUpdateStack_disableRollbackRetentionSurvivesRestart(t *testing.T) {
	// Given: a stack whose update failed with rollback disabled, on a durable store.
	dataDir := t.TempDir()
	first := openReadyHybridStore(t, dataDir)
	srv := helpers.NewTestServer(t, helpers.WithStore(first))
	stackName := "upd-disable-rollback-restart"
	logGroup := "upd-disable-rollback-restart-logs"

	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {fmt.Sprintf(disableRollbackBaseTemplate, logGroup)},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":       {stackName},
		"TemplateBody":    {fmt.Sprintf(disableRollbackFailingTemplate, logGroup)},
		"DisableRollback": {"true"},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_FAILED")
	before := describeStackResources(t, srv, stackName)

	// When: the store is closed and reopened, as a restart would.
	if err := first.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	second := openReadyHybridStore(t, dataDir)
	t.Cleanup(func() { _ = second.Close() })
	restarted := helpers.NewTestServer(t, helpers.WithStore(second))

	// Then: the failure and everything the attempt reached are still reported.
	if got := describeStackStatus(t, restarted, stackName); got != "UPDATE_FAILED" {
		t.Fatalf("stack status after restart = %q, want UPDATE_FAILED", got)
	}
	after := describeStackResources(t, restarted, stackName)
	if len(after) != len(before) {
		t.Fatalf("resources after restart = %+v, want the %d records recorded before it", after, len(before))
	}
	if got := after["Logs"].Status; got != "UPDATE_COMPLETE" {
		t.Errorf("Logs status after restart = %q, want UPDATE_COMPLETE", got)
	}
	failed := after["SubB"]
	if failed.Status != "CREATE_FAILED" || failed.StatusReason != before["SubB"].StatusReason {
		t.Errorf("SubB after restart = %+v, want %+v", failed, before["SubB"])
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
