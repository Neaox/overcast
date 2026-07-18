package cloudformation

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	benclock "github.com/benbjohnson/clock"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
)

// TestTopoSortImplicitDeps verifies that Ref, Fn::GetAtt, and Fn::Sub inside
// resource Properties are treated as implicit dependency edges — matching real
// AWS CloudFormation behaviour where these intrinsics establish ordering without
// requiring an explicit DependsOn.
func TestTopoSortImplicitDeps(t *testing.T) {
	t.Run("Ref creates implicit dependency", func(t *testing.T) {
		// TopicSubscription references Topic via Ref — must be provisioned after Topic.
		resources := map[string]TemplateResource{
			"TopicSubscription": {
				Type: "AWS::SNS::Subscription",
				Properties: map[string]any{
					"TopicArn": map[string]any{"Ref": "Topic"},
					"Protocol": "sqs",
					"Endpoint": "arn:aws:sqs:us-east-1:000000000000:queue",
				},
			},
			"Topic": {
				Type: "AWS::SNS::Topic",
				Properties: map[string]any{
					"TopicName": "my-topic",
				},
			},
		}

		order, err := topoSort(resources)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertBefore(t, order, "Topic", "TopicSubscription")
	})

	t.Run("Fn::GetAtt array form creates implicit dependency", func(t *testing.T) {
		resources := map[string]TemplateResource{
			"QueuePolicy": {
				Type: "AWS::SQS::QueuePolicy",
				Properties: map[string]any{
					"Queues": []any{
						map[string]any{"Fn::GetAtt": []any{"MyQueue", "QueueUrl"}},
					},
				},
			},
			"MyQueue": {
				Type: "AWS::SQS::Queue",
				Properties: map[string]any{
					"QueueName": "my-queue",
				},
			},
		}

		order, err := topoSort(resources)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertBefore(t, order, "MyQueue", "QueuePolicy")
	})

	t.Run("Fn::GetAtt dot-string form creates implicit dependency", func(t *testing.T) {
		resources := map[string]TemplateResource{
			"Consumer": {
				Type: "AWS::Lambda::Function",
				Properties: map[string]any{
					"Environment": map[string]any{
						"Variables": map[string]any{
							"QUEUE_ARN": map[string]any{"Fn::GetAtt": "SourceQueue.Arn"},
						},
					},
				},
			},
			"SourceQueue": {
				Type: "AWS::SQS::Queue",
				Properties: map[string]any{
					"QueueName": "source-queue",
				},
			},
		}

		order, err := topoSort(resources)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertBefore(t, order, "SourceQueue", "Consumer")
	})

	t.Run("Fn::Sub string creates implicit dependency", func(t *testing.T) {
		resources := map[string]TemplateResource{
			"StateMachine": {
				Type: "AWS::StepFunctions::StateMachine",
				Properties: map[string]any{
					"DefinitionString": map[string]any{
						"Fn::Sub": `{"StartAt":"Call","States":{"Call":{"Type":"Task","Resource":"${MyLambda.Arn}","End":true}}}`,
					},
				},
			},
			"MyLambda": {
				Type: "AWS::Lambda::Function",
				Properties: map[string]any{
					"FunctionName": "my-fn",
				},
			},
		}

		order, err := topoSort(resources)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertBefore(t, order, "MyLambda", "StateMachine")
	})

	t.Run("Fn::Sub array form creates implicit dependency", func(t *testing.T) {
		resources := map[string]TemplateResource{
			"B": {
				Type: "AWS::SSM::Parameter",
				Properties: map[string]any{
					"Value": map[string]any{
						"Fn::Sub": []any{
							"arn:aws:sqs:${AWS::Region}:${AWS::AccountId}:${QueueName}",
							map[string]any{
								"QueueName": map[string]any{"Ref": "A"},
							},
						},
					},
				},
			},
			"A": {
				Type: "AWS::SQS::Queue",
				Properties: map[string]any{
					"QueueName": "queue-a",
				},
			},
		}

		order, err := topoSort(resources)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertBefore(t, order, "A", "B")
	})

	t.Run("Explicit DependsOn still works", func(t *testing.T) {
		resources := map[string]TemplateResource{
			"B": {
				Type:       "AWS::SQS::Queue",
				Properties: map[string]any{},
				DependsOn:  "A",
			},
			"A": {
				Type:       "AWS::SQS::Queue",
				Properties: map[string]any{},
			},
		}

		order, err := topoSort(resources)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertBefore(t, order, "A", "B")
	})

	t.Run("Cycle detection still works", func(t *testing.T) {
		resources := map[string]TemplateResource{
			"A": {
				Type: "AWS::SQS::Queue",
				Properties: map[string]any{
					"QueueName": map[string]any{"Ref": "B"},
				},
			},
			"B": {
				Type: "AWS::SQS::Queue",
				Properties: map[string]any{
					"QueueName": map[string]any{"Ref": "A"},
				},
			},
		}

		_, err := topoSort(resources)
		if err == nil {
			t.Fatal("expected cycle error, got nil")
		}
	})

	t.Run("Refs to pseudo-params and template params are not treated as deps", func(t *testing.T) {
		resources := map[string]TemplateResource{
			"MyQueue": {
				Type: "AWS::SQS::Queue",
				Properties: map[string]any{
					"QueueName": map[string]any{"Fn::Sub": "${AWS::StackName}-queue"},
					"Tags": []any{
						map[string]any{
							"Key":   "Region",
							"Value": map[string]any{"Ref": "AWS::Region"},
						},
					},
				},
			},
		}

		order, err := topoSort(resources)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(order) != 1 || order[0] != "MyQueue" {
			t.Fatalf("expected [MyQueue], got %v", order)
		}
	})
}

// assertBefore asserts that `a` appears before `b` in the ordering slice.
func assertBefore(t *testing.T, order []string, a, b string) {
	t.Helper()
	posA, posB := -1, -1
	for i, name := range order {
		if name == a {
			posA = i
		}
		if name == b {
			posB = i
		}
	}
	if posA < 0 {
		t.Fatalf("%q not found in order %v", a, order)
	}
	if posB < 0 {
		t.Fatalf("%q not found in order %v", b, order)
	}
	if posA >= posB {
		t.Fatalf("expected %q (pos %d) before %q (pos %d) in %v", a, posA, b, posB, order)
	}
}

func TestProvisionerAwaitBriefly_done(t *testing.T) {
	// Given: a provisioner with a one-second synchronous wait budget
	clk := clock.NewMock()
	p := &provisioner{cfg: &config.Config{CFNSyncWait: time.Second}, clk: clk}
	done := make(chan struct{})
	close(done)

	// When: provisioning is already complete
	returned := make(chan struct{})
	go func() {
		p.awaitBriefly(done)
		close(returned)
	}()

	// Then: the wait returns without advancing the clock to the budget
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("awaitBriefly did not return after done closed")
	}
}

func TestProvisionerAwaitBriefly_budget(t *testing.T) {
	// Given: a provisioner with a one-second synchronous wait budget and pending work
	clk := newControlledClock()
	p := &provisioner{cfg: &config.Config{CFNSyncWait: time.Second}, clk: clk}
	done := make(chan struct{})
	returned := make(chan struct{})

	// When: the wait starts and the budget elapses before provisioning completes
	go func() {
		p.awaitBriefly(done)
		close(returned)
	}()
	if got := <-clk.afterCalled; got != time.Second {
		t.Fatalf("After called with %v, want 1s", got)
	}
	clk.after <- time.Unix(1, 0)

	// Then: the wait returns even though provisioning continues in the background
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("awaitBriefly did not return when budget elapsed")
	}
	select {
	case <-done:
		t.Fatal("test setup closed done unexpectedly")
	default:
	}
}

type controlledClock struct {
	after       chan time.Time
	afterCalled chan time.Duration
}

func newControlledClock() *controlledClock {
	return &controlledClock{
		after:       make(chan time.Time, 1),
		afterCalled: make(chan time.Duration, 1),
	}
}

func (c *controlledClock) After(d time.Duration) <-chan time.Time {
	c.afterCalled <- d
	return c.after
}

func (c *controlledClock) AfterFunc(time.Duration, func()) *benclock.Timer {
	panic("AfterFunc not implemented")
}

func (c *controlledClock) Now() time.Time { return time.Unix(0, 0) }

func (c *controlledClock) Since(t time.Time) time.Duration { return c.Now().Sub(t) }

func (c *controlledClock) Until(t time.Time) time.Duration { return t.Sub(c.Now()) }

func (c *controlledClock) Sleep(time.Duration) { panic("Sleep not implemented") }

func (c *controlledClock) Tick(time.Duration) <-chan time.Time { panic("Tick not implemented") }

func (c *controlledClock) Ticker(time.Duration) *benclock.Ticker { panic("Ticker not implemented") }

func (c *controlledClock) Timer(time.Duration) *benclock.Timer { panic("Timer not implemented") }

func (c *controlledClock) WithDeadline(parent context.Context, deadline time.Time) (context.Context, context.CancelFunc) {
	return context.WithDeadline(parent, deadline)
}

func (c *controlledClock) WithTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, timeout)
}

func TestProvisionerAwaitBriefly_disabled(t *testing.T) {
	// Given: a provisioner with synchronous waiting disabled
	clk := clock.NewMock()
	p := &provisioner{cfg: &config.Config{CFNSyncWait: 0}, clk: clk}
	done := make(chan struct{})
	returned := make(chan struct{})

	// When: provisioning has not completed
	go func() {
		p.awaitBriefly(done)
		close(returned)
	}()

	// Then: the wait returns immediately without waiting for done
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("awaitBriefly did not return when disabled")
	}
}

func TestEventsRuleHandler_CustomBusTargetUpdateAndDelete(t *testing.T) {
	// Given: a CloudFormation EventBridge rule handler with a custom bus ARN
	handler := &eventsRuleHandler{}
	physicalID := "arn:aws:events:us-east-1:000000000000:rule/custom-bus/custom-rule"
	var calls []struct {
		target string
		body   map[string]any
	}
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), "AWSEvents.")
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode %s body: %v", target, err)
		}
		calls = append(calls, struct {
			target string
			body   map[string]any
		}{target: target, body: body})
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
	rCtx := &resolveContext{Region: "us-east-1", AccountID: "000000000000"}

	// When: CloudFormation updates targets and then deletes the rule
	_, _, err := handler.Update(context.Background(), router, nil, physicalID, map[string]any{
		"Targets": []any{map[string]any{"Id": "new-target", "Arn": "arn:aws:sqs:us-east-1:000000000000:new"}},
	}, map[string]any{
		"Targets": []any{map[string]any{"Id": "old-target", "Arn": "arn:aws:sqs:us-east-1:000000000000:old"}},
	}, rCtx)
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if err := handler.Delete(context.Background(), router, nil, physicalID, rCtx); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}

	// Then: EventBridge mutations address the custom bus parsed from the rule ARN
	wantBusByTarget := map[string]string{
		"PutRule":       "custom-bus",
		"RemoveTargets": "custom-bus",
		"PutTargets":    "custom-bus",
		"DeleteRule":    "custom-bus",
	}
	seen := map[string]bool{}
	for _, call := range calls {
		wantBus, ok := wantBusByTarget[call.target]
		if !ok {
			continue
		}
		seen[call.target] = true
		if got, _ := call.body["EventBusName"].(string); got != wantBus {
			t.Fatalf("%s EventBusName = %q, want %q; body=%#v", call.target, got, wantBus, call.body)
		}
		nameKey := "Rule"
		if call.target == "PutRule" || call.target == "DeleteRule" {
			nameKey = "Name"
		}
		if got, _ := call.body[nameKey].(string); got != "custom-rule" {
			t.Fatalf("%s %s = %q, want custom-rule; body=%#v", call.target, nameKey, got, call.body)
		}
	}
	for target := range wantBusByTarget {
		if !seen[target] {
			t.Fatalf("expected %s call, got %#v", target, calls)
		}
	}
}
