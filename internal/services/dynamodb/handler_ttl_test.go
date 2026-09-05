package dynamodb

// handler_ttl_test.go covers the parts of the asynchronous TTL lifecycle that
// have no client-visible trigger: the sweeper's ENABLED gate (the hourly tick
// is far longer than the transition window, so an integration test can never
// catch a sweep mid-ENABLING) and the startup re-arm of a transition that was
// in flight when the process stopped.

import (
	"context"
	"strconv"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/state"
)

// newTTLTestService builds a DynamoDB service on a mock clock wound to a
// fixed date, plus the store it persists tables into so a restart can be
// simulated against the same data.
func newTTLTestService(t *testing.T, store state.Store, mock *clock.Mock) *Service {
	t.Helper()
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	svc := New(cfg, store, zap.NewNop(), mock, events.NewBus())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		svc.Stop(ctx)
	})
	return svc
}

func newTTLTestClock() *clock.Mock {
	mock := clock.NewMock()
	mock.Set(time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC))
	return mock
}

func mustUpdateTTL(t *testing.T, svc *Service, table string, enabled bool, attribute string) {
	t.Helper()
	_, aerr := svc.handler.updateTimeToLiveTyped(context.Background(), &updateTimeToLiveRequest{
		TableName: table,
		TimeToLiveSpecification: &timeToLiveSpecificationInput{
			Enabled:       &enabled,
			AttributeName: &attribute,
		},
	})
	if aerr != nil {
		t.Fatalf("UpdateTimeToLive(%q, enabled=%v): %v", table, enabled, aerr)
	}
}

func mustPutTTLItem(t *testing.T, svc *Service, table, id string, expiresAt int64) {
	t.Helper()
	_, aerr := svc.handler.putItemTyped(context.Background(), &putItemRequest{
		TableName: table,
		Item: Item{
			"id":  attrValue{"S": id},
			"ttl": attrValue{"N": strconv.FormatInt(expiresAt, 10)},
		},
	})
	if aerr != nil {
		t.Fatalf("PutItem %q: %v", id, aerr)
	}
}

func itemExists(t *testing.T, svc *Service, table, id string) bool {
	t.Helper()
	resp, aerr := svc.handler.getItemTyped(context.Background(), &getItemRequest{
		TableName: table,
		Key:       Item{"id": attrValue{"S": id}},
	})
	if aerr != nil {
		t.Fatalf("GetItem %q: %v", id, aerr)
	}
	return resp.Item != nil
}

// TestSweepExpiredItems_onlyExpiresWhileEnabled pins that the sweeper leaves
// items alone while the enable is still processing. AWS only starts expiring
// items once TimeToLiveStatus reaches ENABLED, so a sweep that fired during
// ENABLING would delete data the table is not yet expiring.
func TestSweepExpiredItems_onlyExpiresWhileEnabled(t *testing.T) {
	// Given: a table whose TTL enable is still in flight, holding an item
	// whose expiry is already in the past
	mock := newTTLTestClock()
	svc := newTTLTestService(t, state.NewMemoryStore(), mock)
	mustCreateTable(t, svc, "sweep-gate")
	mustUpdateTTL(t, svc, "sweep-gate", true, "ttl")
	mustPutTTLItem(t, svc, "sweep-gate", "expired", mock.Now().Add(-time.Hour).Unix())

	// When: a sweep runs while the table is still ENABLING
	svc.handler.sweepExpiredItems(context.Background())

	// Then: the item survives
	if !itemExists(t, svc, "sweep-gate", "expired") {
		t.Fatal("sweeper expired an item while TimeToLiveStatus was ENABLING")
	}

	// And: once the transition settles to ENABLED, the same sweep deletes it
	mock.Add(ttlTransitionDuration + time.Second)
	svc.handler.sweepExpiredItems(context.Background())
	if itemExists(t, svc, "sweep-gate", "expired") {
		t.Fatal("sweeper did not expire an item while TimeToLiveStatus was ENABLED")
	}
}

// TestSweepExpiredItems_stopsExpiringWhileDisabling pins the other half of the
// gate: a disable that is still processing has already stopped expiry.
func TestSweepExpiredItems_stopsExpiringWhileDisabling(t *testing.T) {
	// Given: a table whose TTL is ENABLED, then disabled
	mock := newTTLTestClock()
	svc := newTTLTestService(t, state.NewMemoryStore(), mock)
	mustCreateTable(t, svc, "sweep-disabling")
	mustUpdateTTL(t, svc, "sweep-disabling", true, "ttl")
	mock.Add(ttlTransitionDuration + time.Second)
	mustUpdateTTL(t, svc, "sweep-disabling", false, "ttl")
	mustPutTTLItem(t, svc, "sweep-disabling", "expired", mock.Now().Add(-time.Hour).Unix())

	// When: a sweep runs while the table is DISABLING
	svc.handler.sweepExpiredItems(context.Background())

	// Then: the item survives
	if !itemExists(t, svc, "sweep-disabling", "expired") {
		t.Fatal("sweeper expired an item while TimeToLiveStatus was DISABLING")
	}
}

// TestRearmTTLTransitions_completesATransitionInterruptedByRestart covers the
// two restart cases: a window that elapsed while the process was down settles
// on startup, and one still open is re-armed for its remaining time.
func TestRearmTTLTransitions_completesATransitionInterruptedByRestart(t *testing.T) {
	// Given: a store holding a table whose TTL enable is still in flight
	store := state.NewMemoryStore()
	mock := newTTLTestClock()
	first := newTTLTestService(t, store, mock)
	mustCreateTable(t, first, "ttl-restart")
	mustUpdateTTL(t, first, "ttl-restart", true, "expiresAt")
	if got := ttlStatusOf(t, first, "ttl-restart"); got != ttlStatusEnabling {
		t.Fatalf("test setup: status = %q, want %q", got, ttlStatusEnabling)
	}

	// When: the process restarts after the window has elapsed
	mock.Add(ttlTransitionDuration + time.Second)
	second := newTTLTestService(t, store, mock)
	second.handler.rearmTTLTransitions(context.Background())

	// Then: the transition has completed and left no pending marker behind
	if got := ttlStatusOf(t, second, "ttl-restart"); got != ttlStatusEnabled {
		t.Errorf("status after restart = %q, want %q", got, ttlStatusEnabled)
	}
	table, aerr := second.handler.store.getTable(context.Background(), "ttl-restart")
	if aerr != nil {
		t.Fatalf("getTable: %v", aerr)
	}
	if table.TTLTransitionAt != 0 {
		t.Errorf("TTLTransitionAt = %d, want it cleared by the settle", table.TTLTransitionAt)
	}
}

func TestRearmTTLTransitions_reArmsATransitionStillInFlight(t *testing.T) {
	// Given: a store holding a table whose TTL disable is still in flight
	store := state.NewMemoryStore()
	mock := newTTLTestClock()
	first := newTTLTestService(t, store, mock)
	mustCreateTable(t, first, "ttl-rearm")
	mustUpdateTTL(t, first, "ttl-rearm", true, "expiresAt")
	mock.Add(ttlTransitionDuration + time.Second)
	mustUpdateTTL(t, first, "ttl-rearm", false, "expiresAt")

	// When: the process restarts while the window is still open
	second := newTTLTestService(t, store, mock)
	second.handler.rearmTTLTransitions(context.Background())

	// Then: the table is still DISABLING, and settles when the window closes
	if got := ttlStatusOf(t, second, "ttl-rearm"); got != ttlStatusDisabling {
		t.Fatalf("status after restart = %q, want %q", got, ttlStatusDisabling)
	}
	second.handler.ttlSched.AdvanceAndSettle(mock, ttlTransitionDuration+time.Second)
	table, aerr := second.handler.store.getTable(context.Background(), "ttl-rearm")
	if aerr != nil {
		t.Fatalf("getTable: %v", aerr)
	}
	if table.TTL != nil || table.TTLTransitionAt != 0 {
		t.Errorf("record after settle = %+v, want the TTL configuration dropped", table)
	}
	if got := ttlStatusOf(t, second, "ttl-rearm"); got != ttlStatusDisabled {
		t.Errorf("status after settle = %q, want %q", got, ttlStatusDisabled)
	}
}

func ttlStatusOf(t *testing.T, svc *Service, table string) string {
	t.Helper()
	resp, aerr := svc.handler.describeTimeToLiveTyped(context.Background(), &describeTimeToLiveRequest{TableName: table})
	if aerr != nil {
		t.Fatalf("DescribeTimeToLive %q: %v", table, aerr)
	}
	return resp.TimeToLiveDescription.TimeToLiveStatus
}
