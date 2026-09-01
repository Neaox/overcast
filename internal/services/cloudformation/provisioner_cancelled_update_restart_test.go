//go:build !nosqlite

// The retained record of a cancelled update matters most across a restart:
// cancellation is the shutdown path, so the process that recorded the partial
// state is exactly the one about to exit. Under -tags nosqlite the durable
// backends are error stubs (internal/state/sqlite_hybrid_nosqlite.go), so this
// file needs the tag guard; the retention itself is covered under every tag
// set by provisioner_cancelled_update_test.go.
//
// This lives at the unit level rather than beside #663's integration restart
// test (tests/integration/cloudformation/update_disable_rollback_restart_test.go)
// because the trigger differs: a resource failure can be provoked through the
// public API with a failing template, but a mid-walk context cancellation
// cannot be requested over the wire — it happens on shutdown — so the test
// drives updateStackResourcesCtx directly with a context it can cancel.

package cloudformation

import (
	"context"
	"net/http"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

func TestUpdateStack_cancelledUpdateRetentionSurvivesRestart(t *testing.T) {
	// Given: a provisioner over a durable store, and an update cancelled after
	// its first resource applied.
	handler := &cancellingUpdateHandler{cancelAfterID: "first-id"}
	resourceType := registerRollbackUpdateHandler(t, handler)
	dataDir := t.TempDir()
	first := openReadyHybridStoreForUnitTest(t, dataDir)
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	clk := clock.New()
	p := newProvisioner(cfg, newCFNStore(first, cfg.Region, clk), clk, serviceutil.NewServiceLogger(zap.NewNop(), "cloudformation"))
	p.initRouter(http.NotFoundHandler())

	stack, tmpl := cancelledUpdateFixture(resourceType)
	ctx, cancel := context.WithCancel(p.regionCtx(stack.Region))
	defer cancel()
	handler.cancel = cancel
	p.updateStackResourcesCtx(ctx, stack, tmpl, captureStackGeneration(stack))
	if stack.Status != StatusUpdateFailed {
		t.Fatalf("stack status = %q, want %q before the restart", stack.Status, StatusUpdateFailed)
	}

	// When: the process winds down and the store is reopened, as a restart
	// following the shutdown that cancelled the update would.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	p.stop(stopCtx)
	if err := first.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	second := openReadyHybridStoreForUnitTest(t, dataDir)
	t.Cleanup(func() { _ = second.Close() })
	reopened := newCFNStore(second, cfg.Region, clk)

	// Then: the failure and everything the walk reached are still recorded.
	persisted, aerr := reopened.getStack(context.Background(), stack.StackName)
	if aerr != nil || persisted == nil {
		t.Fatalf("get stack after restart: %v", aerr)
	}
	if persisted.Status != StatusUpdateFailed || persisted.StatusReason != "cancelled" {
		t.Fatalf("stack after restart = status %q reason %q, want UPDATE_FAILED / cancelled", persisted.Status, persisted.StatusReason)
	}
	if len(persisted.Resources) != 3 {
		t.Fatalf("resources after restart = %+v, want all three retained", persisted.Resources)
	}
	restartedFirst := stackResourceByLogicalID(t, persisted.Resources, "First")
	if restartedFirst.Status != ResourceUpdateComplete || restartedFirst.Properties["Version"] != "new" {
		t.Errorf("First after restart = %+v, want the applied update to survive", restartedFirst)
	}
	restartedThird := stackResourceByLogicalID(t, persisted.Resources, "Third")
	if restartedThird.Status != ResourceCreateComplete || restartedThird.Properties["Version"] != "old" {
		t.Errorf("Third after restart = %+v, want the untouched prior record to survive", restartedThird)
	}
}

// openReadyHybridStoreForUnitTest opens a durable store over dataDir and waits
// out its background schema migration, as the integration-level helper of the
// same shape does (tests/integration/cloudformation).
func openReadyHybridStoreForUnitTest(t *testing.T, dataDir string) *state.HybridStore {
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
