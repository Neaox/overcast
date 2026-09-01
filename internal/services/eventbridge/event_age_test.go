package eventbridge

// RetryPolicy.MaximumEventAgeInSeconds was accepted, validated, stored and
// echoed back by ListTargetsByRule, and then never read: only
// MaximumRetryAttempts reached the retry loop, so a target configured to stop
// retrying after an age kept retrying until the attempt count ran out.
//
// AWS enforces a 60-second minimum on the field and Overcast's retries fire
// back to back, so the age can never elapse on its own inside one delivery.
// These tests drive the injected mock clock from the sink instead: the stub
// router advances time on every failed attempt, which is what a real target
// taking seconds to reject would do.

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/state"
)

// ageFixture is a service whose every delivery attempt fails after advancing
// the mock clock by perAttempt.
type ageFixture struct {
	svc      *Service
	clk      *clock.Mock
	attempts atomic.Int64
	dlq      atomic.Int64
}

func newAgeFixture(t *testing.T, perAttempt time.Duration) *ageFixture {
	t.Helper()
	f := &ageFixture{clk: clock.NewMock()}
	f.svc = New(&config.Config{Region: "us-east-1", AccountID: "000000000000"}, state.NewMemoryStore(), zap.NewNop(), f.clk)
	f.svc.InitRouter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("X-Amz-Target"), "SendMessage") && f.attempts.Load() > 0 {
			// The only SQS call after the first failure is the dead-letter one:
			// the target itself is a Lambda function.
			f.dlq.Add(1)
			w.WriteHeader(http.StatusOK)
			return
		}
		f.attempts.Add(1)
		f.clk.Add(perAttempt)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(func() { f.svc.Stop(context.Background()) })
	return f
}

// deliverAged runs one delivery of a freshly stamped event to a target with the
// given retry policy.
func (f *ageFixture) deliverAged(target ebTarget) deliveryRecord {
	event := map[string]any{
		"id":          "evt-1",
		"source":      "com.example.age",
		"detail-type": "AgeTest",
		"time":        f.clk.Now().UTC().Format(time.RFC3339),
		"detail":      map[string]any{},
	}
	rule := ebRule{Name: "age-rule", EventBusName: "default", ARN: "arn:aws:events:us-east-1:000000000000:rule/default/age-rule"}
	f.svc.deliverTarget(context.Background(), rule, target, event)
	records := f.svc.deliveries.snapshot("", "", "")
	if len(records) == 0 {
		return deliveryRecord{}
	}
	return records[0]
}

func TestDeliverTarget_stopsRetryingOnceTheEventIsTooOld(t *testing.T) {
	// Given: a target allowed five retries but only 60 seconds of event age,
	// against a sink that burns 30 seconds per attempt
	f := newAgeFixture(t, 30*time.Second)

	// When: every attempt fails
	rec := f.deliverAged(ebTarget{
		ID:          "fn",
		ARN:         "arn:aws:lambda:us-east-1:000000000000:function:missing",
		RetryPolicy: map[string]any{"MaximumRetryAttempts": 5, "MaximumEventAgeInSeconds": 60},
	})

	// Then: retries stop at the age limit rather than running out the attempts
	if got := f.attempts.Load(); got != 2 {
		t.Fatalf("target saw %d attempts, want 2 (one at t=0, one at t=30s; at t=60s the event has aged out)", got)
	}
	if rec.Outcome != outcomeDropped {
		t.Fatalf("outcome = %q, want dropped", rec.Outcome)
	}
	if !strings.Contains(rec.Error, "MaximumEventAgeInSeconds") {
		t.Fatalf("error = %q, want it to name MaximumEventAgeInSeconds", rec.Error)
	}
}

func TestDeliverTarget_deadLettersAnEventThatAgedOut(t *testing.T) {
	// Given: the same target, with a dead-letter queue
	f := newAgeFixture(t, 90*time.Second)

	// When: the first attempt fails and the event is already too old to retry
	rec := f.deliverAged(ebTarget{
		ID:          "fn",
		ARN:         "arn:aws:lambda:us-east-1:000000000000:function:missing",
		RetryPolicy: map[string]any{"MaximumRetryAttempts": 5, "MaximumEventAgeInSeconds": 60},
		DLQConfig:   map[string]any{"Arn": "arn:aws:sqs:us-east-1:000000000000:age-dlq"},
	})

	// Then: exactly one attempt is made and the event is dead-lettered
	if got := f.attempts.Load(); got != 1 {
		t.Fatalf("target saw %d attempts, want 1", got)
	}
	if got := f.dlq.Load(); got != 1 {
		t.Fatalf("dead-letter queue saw %d deliveries, want 1", got)
	}
	if rec.Outcome != outcomeDLQ {
		t.Fatalf("outcome = %q (%s), want dlq", rec.Outcome, rec.Error)
	}
}

func TestDeliverTarget_withoutAnAgeLimitRunsOutTheRetries(t *testing.T) {
	// Given: a target with retries but no MaximumEventAgeInSeconds
	f := newAgeFixture(t, time.Hour)

	// When: every attempt fails
	f.deliverAged(ebTarget{
		ID:          "fn",
		ARN:         "arn:aws:lambda:us-east-1:000000000000:function:missing",
		RetryPolicy: map[string]any{"MaximumRetryAttempts": 2},
	})

	// Then: the age check never fires — MaximumRetryAttempts alone decides
	if got := f.attempts.Load(); got != 3 {
		t.Fatalf("target saw %d attempts, want 3", got)
	}
}
