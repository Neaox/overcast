package scheduler

// Regression test: the cron engine scans schedules across ALL regions, but
// firing previously used a bare background context — SQS target delivery
// resolved the DEFAULT region's queue, so schedules created in any other
// region delivered into the wrong region (or nowhere).

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

func TestScheduleFire_nonDefaultRegion(t *testing.T) {
	clk := clock.NewMock()
	clk.Set(time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC))
	st := state.NewMemoryStore()
	s := New(&config.Config{Region: "us-east-1", AccountID: "123456789012"}, st, zap.NewNop(), clk)
	router := &recordingRouter{}
	s.initDispatcher(router)

	ctx := context.Background()
	sc := Schedule{
		Name:               "s1",
		GroupName:          "default",
		State:              "ENABLED",
		ScheduleExpression: "rate(1 minute)",
		Target:             scheduleTarget{Arn: "arn:aws:sqs:eu-west-1:123456789012:q1"},
	}
	raw, _ := json.Marshal(sc)
	key := s.scheduleKey("eu-west-1", "default", "s1")
	if err := st.Set(ctx, nsSchedules, key, string(raw)); err != nil {
		t.Fatalf("seed schedule: %v", err)
	}
	s.setLastFire(ctx, key, clk.Now().Add(-2*time.Minute))

	s.tick()

	calls := router.recorded()
	if len(calls) != 1 {
		t.Fatalf("fired %d deliveries, want 1", len(calls))
	}
	if calls[0].Region != "eu-west-1" {
		t.Fatalf("delivery carried region %q, want eu-west-1 (fired into the default region)", calls[0].Region)
	}
	if !strings.Contains(calls[0].Body, `"QueueUrl":"q1"`) {
		t.Fatalf("delivered to the wrong queue: %s", calls[0].Body)
	}
}
