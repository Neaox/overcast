package scheduler

// Regression test: the cron engine scans schedules across ALL regions, but
// firing previously used a bare background context — SQS target delivery
// resolved the DEFAULT region's queue, so schedules created in any other
// region delivered into the wrong region (or nowhere).

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/state"
)

type capturingEnqueuer struct {
	mu      sync.Mutex
	regions []string
	queues  []string
}

func (c *capturingEnqueuer) EnqueueRaw(ctx context.Context, queueName string, body string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.regions = append(c.regions, middleware.RegionFromContext(ctx, "MISSING"))
	c.queues = append(c.queues, queueName)
	return nil
}

func TestScheduleFire_nonDefaultRegion(t *testing.T) {
	clk := clock.NewMock()
	clk.Set(time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC))
	st := state.NewMemoryStore()
	s := New(&config.Config{Region: "us-east-1", AccountID: "123456789012"}, st, zap.NewNop(), clk)
	enq := &capturingEnqueuer{}
	s.targets = TargetInvoker{SQS: enq}

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

	enq.mu.Lock()
	defer enq.mu.Unlock()
	if len(enq.regions) != 1 {
		t.Fatalf("fired %d deliveries, want 1", len(enq.regions))
	}
	if enq.regions[0] != "eu-west-1" {
		t.Fatalf("delivery context carried region %q, want eu-west-1 (fired into the default region)", enq.regions[0])
	}
	if enq.queues[0] != "q1" {
		t.Fatalf("delivered to queue %q, want q1", enq.queues[0])
	}
}
