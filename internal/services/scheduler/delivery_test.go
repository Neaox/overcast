package scheduler

// Unit coverage for the firing path: which sink a target ARN reaches, how the
// target's parameters shape the delivery, and how RetryPolicy and
// DeadLetterConfig behave when a delivery fails. Delivery goes through the
// emulator's root router, so a recording http.Handler stands in for it and the
// assertions are on the requests the dispatcher would have made.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

// recordedCall is one delivery the scheduler made through the root router.
type recordedCall struct {
	Path   string
	Target string // X-Amz-Target, for AWS JSON 1.1 sinks
	Body   string
	Region string
}

// recordingRouter captures deliveries and answers with a caller-chosen status.
type recordingRouter struct {
	mu     sync.Mutex
	calls  []recordedCall
	status func(recordedCall) int
}

func (r *recordingRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	call := recordedCall{
		Path:   req.URL.Path,
		Target: req.Header.Get("X-Amz-Target"),
		Body:   string(body),
		Region: req.Header.Get("X-Overcast-Region"),
	}
	r.mu.Lock()
	r.calls = append(r.calls, call)
	r.mu.Unlock()
	status := http.StatusOK
	if r.status != nil {
		status = r.status(call)
	}
	w.WriteHeader(status)
}

func (r *recordingRouter) recorded() []recordedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedCall(nil), r.calls...)
}

// newFiringService returns a Service wired to router, with the engine left
// stopped so tests drive fire() directly.
func newFiringService(t *testing.T, router http.Handler) (*Service, *clock.Mock) {
	t.Helper()
	clk := clock.NewMock()
	clk.Set(time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC))
	s := New(&config.Config{Region: "us-east-1", AccountID: "000000000000"}, state.NewMemoryStore(), zap.NewNop(), clk)
	s.initDispatcher(router)
	return s, clk
}

func fireSchedule(s *Service, target scheduleTarget) {
	s.fire(context.Background(), &Schedule{Name: "s1", GroupName: defaultGroup, Target: target}, s.clk.Now())
}

// ─── Sink selection ───────────────────────────────────────────────────────────

func TestFire_deliversEachSupportedTargetKind(t *testing.T) {
	// Given: one schedule per target kind the shared dispatcher can reach
	cases := []struct {
		name       string
		target     scheduleTarget
		wantPath   string
		wantTarget string
		wantBody   string
	}{
		{
			name:     "lambda",
			target:   scheduleTarget{Arn: "arn:aws:lambda:us-east-1:000000000000:function:fn", Input: `{"a":1}`},
			wantPath: "/2015-03-31/functions/fn/invocations",
			wantBody: `{"a":1}`,
		},
		{
			name:       "sqs",
			target:     scheduleTarget{Arn: "arn:aws:sqs:us-east-1:000000000000:q", Input: `{"a":2}`, SqsParameters: &sqsParameters{MessageGroupId: "g"}},
			wantPath:   "/",
			wantTarget: "AmazonSQS.SendMessage",
			wantBody:   `"MessageGroupId":"g"`,
		},
		{
			name:     "sns",
			target:   scheduleTarget{Arn: "arn:aws:sns:us-east-1:000000000000:topic", Input: `{"a":3}`},
			wantPath: "/",
			wantBody: "Action=Publish",
		},
		{
			name:       "states",
			target:     scheduleTarget{Arn: "arn:aws:states:us-east-1:000000000000:stateMachine:sm", Input: `{"a":4}`},
			wantPath:   "/",
			wantTarget: "AWSStepFunctions.StartExecution",
			wantBody:   `"stateMachineArn":"arn:aws:states:us-east-1:000000000000:stateMachine:sm"`,
		},
		{
			name:       "kinesis",
			target:     scheduleTarget{Arn: "arn:aws:kinesis:us-east-1:000000000000:stream/st", Input: `{"a":5}`, KinesisParameters: &kinesisParams{PartitionKey: "pk"}},
			wantPath:   "/",
			wantTarget: "Kinesis_20131202.PutRecord",
			wantBody:   `"PartitionKey":"pk"`,
		},
		{
			name:       "firehose",
			target:     scheduleTarget{Arn: "arn:aws:firehose:us-east-1:000000000000:deliverystream/ds", Input: `{"a":6}`},
			wantPath:   "/",
			wantTarget: "Firehose_20150804.PutRecord",
			wantBody:   `"DeliveryStreamName":"ds"`,
		},
		{
			name: "events",
			target: scheduleTarget{
				Arn:                   "arn:aws:events:us-east-1:000000000000:event-bus/default",
				Input:                 `{"a":7}`,
				EventBridgeParameters: &eventBridgeParams{Source: "com.example", DetailType: "Scheduled"},
			},
			wantPath:   "/",
			wantTarget: "AWSEvents.PutEvents",
			wantBody:   `"Source":"com.example"`,
		},
		{
			name: "ecs",
			target: scheduleTarget{
				Arn:           "arn:aws:ecs:us-east-1:000000000000:cluster/c",
				Input:         `{"a":8}`,
				EcsParameters: &ecsParams{TaskDefinitionArn: "arn:aws:ecs:us-east-1:000000000000:task-definition/td:1", TaskCount: 2},
			},
			wantPath:   "/",
			wantTarget: "AmazonEC2ContainerServiceV20141113.RunTask",
			wantBody:   `"taskDefinition":"arn:aws:ecs:us-east-1:000000000000:task-definition/td:1"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := &recordingRouter{}
			s, _ := newFiringService(t, router)

			// When: the schedule fires
			fireSchedule(s, tc.target)

			// Then: exactly one delivery reached the target's own sink
			calls := router.recorded()
			if len(calls) != 1 {
				t.Fatalf("made %d deliveries, want 1: %+v", len(calls), calls)
			}
			if calls[0].Path != tc.wantPath {
				t.Errorf("path = %q, want %q", calls[0].Path, tc.wantPath)
			}
			if calls[0].Target != tc.wantTarget {
				t.Errorf("X-Amz-Target = %q, want %q", calls[0].Target, tc.wantTarget)
			}
			if !strings.Contains(calls[0].Body, tc.wantBody) {
				t.Errorf("body %q does not contain %q", calls[0].Body, tc.wantBody)
			}
		})
	}
}

func TestFire_defaultInputIsDeliveredWhenTargetSetsNone(t *testing.T) {
	// Given: a target with no Input
	router := &recordingRouter{}
	s, clk := newFiringService(t, router)

	// When: the schedule fires
	fireSchedule(s, scheduleTarget{Arn: "arn:aws:sqs:us-east-1:000000000000:q"})

	// Then: the generated scheduler envelope carries the fire time
	calls := router.recorded()
	if len(calls) != 1 {
		t.Fatalf("made %d deliveries, want 1", len(calls))
	}
	var body struct {
		MessageBody string `json:"MessageBody"`
	}
	if err := json.Unmarshal([]byte(calls[0].Body), &body); err != nil {
		t.Fatalf("delivery body is not JSON: %s", calls[0].Body)
	}
	if !strings.Contains(body.MessageBody, `"source":"aws.scheduler"`) {
		t.Errorf("default payload = %s, want the aws.scheduler envelope", body.MessageBody)
	}
	if !strings.Contains(body.MessageBody, clk.Now().UTC().Format(time.RFC3339)) {
		t.Errorf("default payload = %s, want the fire time from the injected clock", body.MessageBody)
	}
}

// ─── RetryPolicy ──────────────────────────────────────────────────────────────

func TestFire_retriesUpToMaximumRetryAttempts(t *testing.T) {
	// Given: a target that always fails and asks for two retries
	router := &recordingRouter{status: func(recordedCall) int { return http.StatusNotFound }}
	s, _ := newFiringService(t, router)

	// When: the schedule fires
	fireSchedule(s, scheduleTarget{
		Arn:         "arn:aws:sqs:us-east-1:000000000000:missing",
		RetryPolicy: &retryPolicy{MaximumRetryAttempts: 2},
	})

	// Then: the delivery was attempted once plus the configured retries
	if got := len(router.recorded()); got != 3 {
		t.Fatalf("attempted %d deliveries, want 3 (initial + MaximumRetryAttempts)", got)
	}
}

func TestFire_retryAttemptsAreCapped(t *testing.T) {
	// Given: a failing target asking for far more retries than the emulator
	// will run inline
	router := &recordingRouter{status: func(recordedCall) int { return http.StatusNotFound }}
	s, _ := newFiringService(t, router)

	// When: the schedule fires
	fireSchedule(s, scheduleTarget{
		Arn:         "arn:aws:sqs:us-east-1:000000000000:missing",
		RetryPolicy: &retryPolicy{MaximumRetryAttempts: 185},
	})

	// Then: the attempt count stops at the emulator's ceiling
	if got := len(router.recorded()); got != maxDeliveryAttempts {
		t.Fatalf("attempted %d deliveries, want the %d-attempt ceiling", got, maxDeliveryAttempts)
	}
}

func TestFire_failsWithoutRetryWhenNoRetryPolicy(t *testing.T) {
	// Given: a failing target with no RetryPolicy
	router := &recordingRouter{status: func(recordedCall) int { return http.StatusNotFound }}
	s, _ := newFiringService(t, router)

	// When: the schedule fires
	fireSchedule(s, scheduleTarget{Arn: "arn:aws:sqs:us-east-1:000000000000:missing"})

	// Then: it is attempted exactly once
	if got := len(router.recorded()); got != 1 {
		t.Fatalf("attempted %d deliveries, want 1", got)
	}
}

func TestFire_stopsRetryingPastMaximumEventAge(t *testing.T) {
	// Given: a failing target whose event age budget expires between attempts
	var clk *clock.Mock
	router := &recordingRouter{status: func(recordedCall) int {
		clk.Add(90 * time.Second)
		return http.StatusNotFound
	}}
	s, mockClk := newFiringService(t, router)
	clk = mockClk

	// When: the schedule fires
	fireSchedule(s, scheduleTarget{
		Arn:         "arn:aws:sqs:us-east-1:000000000000:missing",
		RetryPolicy: &retryPolicy{MaximumRetryAttempts: 5, MaximumEventAgeInSeconds: 60},
	})

	// Then: retrying stops as soon as the payload is older than the budget
	if got := len(router.recorded()); got != 1 {
		t.Fatalf("attempted %d deliveries, want 1 (the age budget forbids a retry)", got)
	}
}

// ─── DeadLetterConfig ─────────────────────────────────────────────────────────

func TestFire_deadLettersAfterFinalFailure(t *testing.T) {
	// Given: a failing Lambda target with an SQS dead-letter queue
	router := &recordingRouter{status: func(c recordedCall) int {
		if strings.HasPrefix(c.Path, "/2015-03-31/") {
			return http.StatusNotFound
		}
		return http.StatusOK
	}}
	s, _ := newFiringService(t, router)

	// When: the schedule fires
	fireSchedule(s, scheduleTarget{
		Arn:              "arn:aws:lambda:us-east-1:000000000000:function:missing",
		Input:            `{"a":1}`,
		DeadLetterConfig: &dlqConfig{Arn: "arn:aws:sqs:us-east-1:000000000000:dlq"},
	})

	// Then: the payload is forwarded to the dead-letter queue
	calls := router.recorded()
	if len(calls) != 2 {
		t.Fatalf("made %d deliveries, want 2 (target + dead-letter)", len(calls))
	}
	if calls[1].Target != "AmazonSQS.SendMessage" || !strings.Contains(calls[1].Body, `"QueueUrl":"dlq"`) {
		t.Fatalf("second delivery was not the dead-letter send: %+v", calls[1])
	}
	if !strings.Contains(calls[1].Body, `{\"a\":1}`) {
		t.Fatalf("dead-letter message did not carry the payload: %s", calls[1].Body)
	}
}

func TestFire_nonSQSDeadLetterTargetIsNotDelivered(t *testing.T) {
	// Given: a failing target whose DeadLetterConfig names a non-SQS ARN —
	// AWS only supports SQS dead-letter queues
	router := &recordingRouter{status: func(c recordedCall) int {
		if strings.HasPrefix(c.Path, "/2015-03-31/") {
			return http.StatusNotFound
		}
		return http.StatusOK
	}}
	s, _ := newFiringService(t, router)

	// When: the schedule fires
	fireSchedule(s, scheduleTarget{
		Arn:              "arn:aws:lambda:us-east-1:000000000000:function:missing",
		DeadLetterConfig: &dlqConfig{Arn: "arn:aws:sns:us-east-1:000000000000:topic"},
	})

	// Then: nothing is published to the non-queue ARN
	calls := router.recorded()
	if len(calls) != 1 {
		t.Fatalf("made %d deliveries, want 1 (the dead-letter ARN is not a queue)", len(calls))
	}
}

// ─── Target validation ────────────────────────────────────────────────────────

func TestValidateTarget_cases(t *testing.T) {
	// Given: target ARNs spanning the deliverable, the undeliverable and the
	// malformed
	cases := []struct {
		name    string
		target  scheduleTarget
		wantErr bool
	}{
		{name: "lambda", target: scheduleTarget{Arn: "arn:aws:lambda:us-east-1:000000000000:function:fn"}},
		{name: "sqs", target: scheduleTarget{Arn: "arn:aws:sqs:us-east-1:000000000000:q"}},
		{name: "sns", target: scheduleTarget{Arn: "arn:aws:sns:us-east-1:000000000000:topic"}},
		{name: "step functions", target: scheduleTarget{Arn: "arn:aws:states:us-east-1:000000000000:stateMachine:sm"}},
		{name: "kinesis", target: scheduleTarget{Arn: "arn:aws:kinesis:us-east-1:000000000000:stream/s"}},
		{name: "firehose", target: scheduleTarget{Arn: "arn:aws:firehose:us-east-1:000000000000:deliverystream/d"}},
		{
			name: "ecs",
			target: scheduleTarget{
				Arn:           "arn:aws:ecs:us-east-1:000000000000:cluster/c",
				EcsParameters: &ecsParams{TaskDefinitionArn: "arn:aws:ecs:us-east-1:000000000000:task-definition/td:1"},
			},
		},
		{
			name: "event bus",
			target: scheduleTarget{
				Arn:                   "arn:aws:events:us-east-1:000000000000:event-bus/default",
				EventBridgeParameters: &eventBridgeParams{Source: "com.example", DetailType: "Scheduled"},
			},
		},
		{
			name:    "ecs without a task definition",
			target:  scheduleTarget{Arn: "arn:aws:ecs:us-east-1:000000000000:cluster/c"},
			wantErr: true,
		},
		{
			name:    "event bus without routing fields",
			target:  scheduleTarget{Arn: "arn:aws:events:us-east-1:000000000000:event-bus/default"},
			wantErr: true,
		},
		{name: "logs", target: scheduleTarget{Arn: "arn:aws:logs:us-east-1:000000000000:log-group:/aws/x"}, wantErr: true},
		{name: "universal target", target: scheduleTarget{Arn: "arn:aws:scheduler:::aws-sdk:sqs:sendMessage"}, wantErr: true},
		{name: "not an arn", target: scheduleTarget{Arn: "my-queue"}, wantErr: true},
		{name: "empty", target: scheduleTarget{}, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// When: the target is validated
			aerr := validateTarget(tc.target)

			// Then: only a target this emulator can actually fire is accepted
			if tc.wantErr && aerr == nil {
				t.Fatalf("validateTarget(%q) = nil, want a ValidationException", tc.target.Arn)
			}
			if !tc.wantErr && aerr != nil {
				t.Fatalf("validateTarget(%q) = %v, want nil", tc.target.Arn, aerr)
			}
			if tc.wantErr && aerr.Code != "ValidationException" {
				t.Fatalf("error code = %q, want ValidationException", aerr.Code)
			}
		})
	}
}
