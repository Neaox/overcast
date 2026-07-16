package eventbridge

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/state"
)

func TestTypedOps_matchDispatchSurface(t *testing.T) {
	s := &Service{}
	ops := s.typedOps()
	expected := []string{
		"CreateEventBus",
		"DescribeEventBus",
		"ListEventBuses",
		"TagResource",
		"ListTagsForResource",
		"DeleteEventBus",
		"PutRule",
		"DescribeRule",
		"ListRules",
		"PutTargets",
		"ListTargetsByRule",
		"RemoveTargets",
		"DisableRule",
		"EnableRule",
		"DeleteRule",
		"PutEvents",
	}

	if len(ops) != len(expected) {
		t.Fatalf("typed ops len = %d, expected %d", len(ops), len(expected))
	}
	for _, name := range expected {
		operation, ok := ops[name]
		if !ok {
			t.Fatalf("missing typed op %q", name)
		}
		if operation.Name() != name {
			t.Fatalf("typed op %q has Name() %q", name, operation.Name())
		}
	}
	for name, operation := range ops {
		if _, ok := operation.(*op.Raw); ok {
			t.Fatalf("%s registered as raw operation", name)
		}
	}
}

func TestPutEventsEnvelope_usesRequestRegion(t *testing.T) {
	// Given: EventBridge has a default region but the request carries another region.
	s := New(&config.Config{Region: "us-east-1", AccountID: "000000000000"}, state.NewMemoryStore(), zap.NewNop(), clock.NewMock())
	ctx := middleware.ContextWithRegion(context.Background(), "eu-west-1")

	// When: an EventBridge envelope is built.
	event := s.putEventsEnvelope(ctx, "evt-1", map[string]any{
		"Source":     "com.example",
		"DetailType": "Example",
		"Detail":     `{"ok":true}`,
	})

	// Then: the event region matches the request region, not the configured fallback.
	if event["region"] != "eu-west-1" {
		t.Fatalf("region = %#v, want eu-west-1", event["region"])
	}
}

func TestLowerEventBridgeKeys_emptyKey(t *testing.T) {
	// Given: a map contains an empty JSON object key.
	in := map[string]any{"": "kept", "Subnets": []any{"subnet-1"}}

	// When: EventBridge target keys are normalized.
	out, ok := lowerEventBridgeKeys(in).(map[string]any)
	if !ok {
		t.Fatalf("expected map output")
	}

	// Then: no panic occurs and the empty key is preserved.
	if out[""] != "kept" {
		t.Fatalf("empty key value = %#v, want kept", out[""])
	}
	if _, ok := out["subnets"]; !ok {
		t.Fatalf("expected Subnets to be normalized: %#v", out)
	}
}

func TestNextRateFire_missingLastFire(t *testing.T) {
	// Given: a persisted rate rule has no last-fire record.
	now := time.Unix(120, 0).UTC()

	// When: the next fire time is calculated.
	next, err := nextRuleFire("rate(1 minute)", time.Time{}, now)
	if err != nil {
		t.Fatalf("nextRuleFire returned error: %v", err)
	}

	// Then: the rule is due now rather than becoming permanently inert.
	if !next.Equal(now) {
		t.Fatalf("next = %s, want %s", next, now)
	}
}

func TestMatchCronDay_awsSunday(t *testing.T) {
	// Given: AWS cron day-of-week 1 means Sunday.
	sunday := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)

	// When/Then: Sunday matches 1 and not 7.
	if !matchCronDay("?", "1", sunday) {
		t.Fatal("expected AWS day-of-week 1 to match Sunday")
	}
	if matchCronDay("?", "7", sunday) {
		t.Fatal("did not expect AWS day-of-week 7 to match Sunday")
	}
}

func TestTargetPayload_inputPathUnsupported(t *testing.T) {
	// Given: a target uses InputPath, which is not implemented yet.
	target := ebTarget{InputPath: "$.detail"}

	// When: a target payload is built.
	_, err := targetPayload(target, map[string]any{"detail": map[string]any{"id": "1"}})

	// Then: delivery fails explicitly instead of silently sending the wrong payload.
	if err == nil {
		t.Fatal("expected InputPath error")
	}
}
