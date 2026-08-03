package eventbridge

// Regression test: the schedule engine scans rules across ALL regions, but
// target delivery previously ran on a bare background context — the target
// lookup (region-keyed) resolved the DEFAULT region, so a scheduled rule in
// any other region fired with zero targets and delivery silently no-oped.

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

func TestScheduledRuleDelivery_nonDefaultRegion(t *testing.T) {
	clk := clock.NewMock()
	st := state.NewMemoryStore()
	s := New(&config.Config{Region: "us-east-1", AccountID: "123456789012"}, st, zap.NewNop(), clk)

	// Fake root router capturing the internal dispatch of target delivery.
	var mu sync.Mutex
	var deliveredRegions []string
	s.InitRouter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		deliveredRegions = append(deliveredRegions, r.Header.Get("X-Overcast-Region"))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(func() { s.Stop(context.Background()) })

	// Seed a scheduled rule + SQS target under eu-west-1, due immediately.
	ctx := context.Background()
	key := serviceutil.RegionKey("eu-west-1", "default/r1")
	rule := ebRule{
		Name:         "r1",
		ARN:          "arn:aws:events:eu-west-1:123456789012:rule/default/r1",
		EventBusName: "default",
		State:        "ENABLED",
		ScheduleExpr: "rate(1 minute)",
	}
	raw, _ := json.Marshal(rule)
	if err := st.Set(ctx, nsRules, key, string(raw)); err != nil {
		t.Fatalf("seed rule: %v", err)
	}
	targets, _ := json.Marshal([]ebTarget{{ID: "t1", ARN: "arn:aws:sqs:eu-west-1:123456789012:q1"}})
	if err := st.Set(ctx, nsTargets, key, string(targets)); err != nil {
		t.Fatalf("seed targets: %v", err)
	}
	s.setLastFire(ctx, key, clk.Now().Add(-2*time.Minute))

	s.tickSchedules()

	mu.Lock()
	defer mu.Unlock()
	if len(deliveredRegions) != 1 {
		t.Fatalf("target delivery dispatched %d requests, want 1 (rule outside the default region fired with no targets)", len(deliveredRegions))
	}
	if deliveredRegions[0] != "eu-west-1" {
		t.Fatalf("delivery carried region %q, want eu-west-1", deliveredRegions[0])
	}
}
