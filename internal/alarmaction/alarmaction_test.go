package alarmaction_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/alarmaction"
)

// capturingRouter stands in for the emulator's root handler.
type capturingRouter struct {
	mu       sync.Mutex
	targets  []string
	forms    []url.Values
	bodies   []string
	failWith int
}

func (c *capturingRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	c.targets = append(c.targets, r.Header.Get("X-Amz-Target"))
	c.bodies = append(c.bodies, string(body))
	if form, err := url.ParseQuery(string(body)); err == nil {
		c.forms = append(c.forms, form)
	} else {
		c.forms = append(c.forms, nil)
	}
	status := c.failWith
	c.mu.Unlock()
	if status != 0 {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"__type":"NotFoundException","message":"Topic does not exist"}`))
		return
	}
	w.WriteHeader(http.StatusOK)
}

func transition() alarmaction.Transition {
	return alarmaction.Transition{
		AlarmName:          "cpu-high",
		AlarmARN:           "arn:aws:cloudwatch:us-east-1:000000000000:alarm:cpu-high",
		Region:             "us-east-1",
		AccountID:          "000000000000",
		OldState:           "OK",
		NewState:           "ALARM",
		Reason:             "Threshold Crossed",
		Timestamp:          time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC),
		ActionsEnabled:     true,
		Namespace:          "AWS/EC2",
		MetricName:         "CPUUtilization",
		Statistic:          "Average",
		Period:             60,
		EvaluationPeriods:  1,
		Threshold:          80,
		ComparisonOperator: "GreaterThanThreshold",
	}
}

// TestDispatch_PublishesStateChangeEvent pins that every transition produces
// the EventBridge event real CloudWatch produces, whether or not the alarm
// has any actions configured.
func TestDispatch_PublishesStateChangeEvent(t *testing.T) {
	// Given: a dispatcher and a transition with no actions
	router := &capturingRouter{}
	d := alarmaction.NewDispatcher(router, "us-east-1", nil, zap.NewNop())

	// When: it is dispatched
	outcomes := d.Dispatch(context.Background(), transition())

	// Then: no action outcomes, but a PutEvents carrying AWS's detail-type
	if len(outcomes) != 0 {
		t.Fatalf("outcomes = %d, want 0 for an alarm with no actions", len(outcomes))
	}
	router.mu.Lock()
	defer router.mu.Unlock()
	if len(router.targets) != 1 || router.targets[0] != "AWSEvents.PutEvents" {
		t.Fatalf("targets = %v, want one AWSEvents.PutEvents", router.targets)
	}

	var envelope struct {
		Entries []struct {
			Source     string   `json:"Source"`
			DetailType string   `json:"DetailType"`
			Detail     string   `json:"Detail"`
			Resources  []string `json:"Resources"`
		} `json:"Entries"`
	}
	if err := json.Unmarshal([]byte(router.bodies[0]), &envelope); err != nil {
		t.Fatalf("PutEvents body is not JSON: %v", err)
	}
	if len(envelope.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(envelope.Entries))
	}
	entry := envelope.Entries[0]
	if entry.Source != "aws.cloudwatch" || entry.DetailType != "CloudWatch Alarm State Change" {
		t.Errorf("source/detail-type = %q/%q, want aws.cloudwatch/CloudWatch Alarm State Change", entry.Source, entry.DetailType)
	}
	if len(entry.Resources) != 1 || entry.Resources[0] != transition().AlarmARN {
		t.Errorf("resources = %v, want the alarm ARN", entry.Resources)
	}
	if !strings.Contains(entry.Detail, `"alarmName":"cpu-high"`) || !strings.Contains(entry.Detail, `"value":"ALARM"`) {
		t.Errorf("detail did not carry the alarm name and new state: %s", entry.Detail)
	}
}

// TestDispatch_SNSActionPublishesNotification pins the SNS notification body
// against the shape real CloudWatch publishes.
func TestDispatch_SNSActionPublishesNotification(t *testing.T) {
	// Given: a transition with an SNS alarm action
	router := &capturingRouter{}
	d := alarmaction.NewDispatcher(router, "us-east-1", nil, zap.NewNop())
	tr := transition()
	tr.Actions = []string{"arn:aws:sns:us-east-1:000000000000:alerts"}

	// When: it is dispatched
	outcomes := d.Dispatch(context.Background(), tr)

	// Then: the action was delivered
	if len(outcomes) != 1 || outcomes[0].Err != nil || outcomes[0].Unsupported {
		t.Fatalf("outcomes = %+v, want one successful delivery", outcomes)
	}

	router.mu.Lock()
	defer router.mu.Unlock()
	var publish url.Values
	for _, form := range router.forms {
		if form.Get("Action") == "Publish" {
			publish = form
		}
	}
	if publish == nil {
		t.Fatalf("no SNS Publish was issued; bodies = %v", router.bodies)
	}
	if got := publish.Get("TopicArn"); got != tr.Actions[0] {
		t.Errorf("TopicArn = %q, want %q", got, tr.Actions[0])
	}
	if got := publish.Get("Subject"); got != `ALARM: "cpu-high" in us-east-1` {
		t.Errorf("Subject = %q", got)
	}

	var msg map[string]any
	if err := json.Unmarshal([]byte(publish.Get("Message")), &msg); err != nil {
		t.Fatalf("SNS message is not JSON: %v", err)
	}
	for field, want := range map[string]any{
		"AlarmName":     "cpu-high",
		"NewStateValue": "ALARM",
		"OldStateValue": "OK",
		"AWSAccountId":  "000000000000",
		"Region":        "us-east-1",
	} {
		if msg[field] != want {
			t.Errorf("message[%s] = %v, want %v", field, msg[field], want)
		}
	}
	trigger, ok := msg["Trigger"].(map[string]any)
	if !ok {
		t.Fatalf("message has no Trigger object: %v", msg)
	}
	if trigger["MetricName"] != "CPUUtilization" || trigger["Statistic"] != "AVERAGE" {
		t.Errorf("Trigger = %v, want CPUUtilization/AVERAGE", trigger)
	}
}

// TestDispatch_ActionsDisabledSkipsDelivery pins AWS's ActionsEnabled flag:
// the transition still happens and is still published, but nothing is
// delivered.
func TestDispatch_ActionsDisabledSkipsDelivery(t *testing.T) {
	// Given: a transition with actions but ActionsEnabled false
	router := &capturingRouter{}
	d := alarmaction.NewDispatcher(router, "us-east-1", nil, zap.NewNop())
	tr := transition()
	tr.ActionsEnabled = false
	tr.Actions = []string{"arn:aws:sns:us-east-1:000000000000:alerts"}

	// When: it is dispatched
	outcomes := d.Dispatch(context.Background(), tr)

	// Then: nothing was delivered, but the event was still published
	if len(outcomes) != 0 {
		t.Fatalf("outcomes = %+v, want none with ActionsEnabled=false", outcomes)
	}
	router.mu.Lock()
	defer router.mu.Unlock()
	for _, form := range router.forms {
		if form.Get("Action") == "Publish" {
			t.Fatalf("an SNS Publish was issued despite ActionsEnabled=false")
		}
	}
	if len(router.targets) != 1 {
		t.Errorf("expected the state-change event to still be published; targets = %v", router.targets)
	}
}

// TestDispatch_RegisteredHandlerWinsOverBuiltIns pins the extension seam: a
// registered service claim takes the ARN, and gets the whole transition.
func TestDispatch_RegisteredHandlerWinsOverBuiltIns(t *testing.T) {
	// Given: a handler registered for "autoscaling"
	router := &capturingRouter{}
	d := alarmaction.NewDispatcher(router, "us-east-1", nil, zap.NewNop())
	var got []string
	d.Register("autoscaling", func(_ context.Context, tr alarmaction.Transition, arn string) error {
		got = append(got, tr.NewState+"@"+arn)
		return nil
	})
	tr := transition()
	policyARN := "arn:aws:autoscaling:us-east-1:000000000000:scalingPolicy:1:autoScalingGroupName/web:policyName/out"
	tr.Actions = []string{policyARN}

	// When: it is dispatched
	outcomes := d.Dispatch(context.Background(), tr)

	// Then: the handler ran, and the outcome is a successful delivery
	if len(got) != 1 || got[0] != "ALARM@"+policyARN {
		t.Fatalf("handler invocations = %v, want one ALARM@<policy arn>", got)
	}
	if len(outcomes) != 1 || outcomes[0].Err != nil || outcomes[0].Unsupported {
		t.Fatalf("outcomes = %+v, want one successful delivery", outcomes)
	}
}

// TestDispatch_UnsupportedTargetIsReported pins §2.1 for action targets: an
// ARN with no sink is reported as unsupported rather than treated as
// delivered.
func TestDispatch_UnsupportedTargetIsReported(t *testing.T) {
	// Given: an alarm action naming a target Overcast has no sink for
	d := alarmaction.NewDispatcher(&capturingRouter{}, "us-east-1", nil, zap.NewNop())
	tr := transition()
	tr.Actions = []string{"arn:aws:ssm:us-east-1:000000000000:opsitem:3", "not-an-arn"}

	// When: it is dispatched
	outcomes := d.Dispatch(context.Background(), tr)

	// Then: both are reported unsupported, neither as delivered
	if len(outcomes) != 2 {
		t.Fatalf("outcomes = %d, want 2", len(outcomes))
	}
	for _, outcome := range outcomes {
		if !outcome.Unsupported {
			t.Errorf("outcome for %q was not marked unsupported: %+v", outcome.ARN, outcome)
		}
	}
}

// TestDispatch_SinkErrorIsReported pins that a sink's own refusal — a missing
// topic, say — surfaces as an error rather than a silent success.
func TestDispatch_SinkErrorIsReported(t *testing.T) {
	// Given: a router that rejects everything
	router := &capturingRouter{failWith: http.StatusNotFound}
	d := alarmaction.NewDispatcher(router, "us-east-1", nil, zap.NewNop())
	tr := transition()
	tr.Actions = []string{"arn:aws:sns:us-east-1:000000000000:missing"}

	// When: it is dispatched
	outcomes := d.Dispatch(context.Background(), tr)

	// Then: the outcome carries the sink's error
	if len(outcomes) != 1 || outcomes[0].Err == nil {
		t.Fatalf("outcomes = %+v, want one carrying the sink's error", outcomes)
	}
	if !strings.Contains(outcomes[0].Err.Error(), "Topic does not exist") {
		t.Errorf("error did not carry the sink's message: %v", outcomes[0].Err)
	}
}

// TestDispatch_NilDispatcherIsSafe pins that a Service constructed without a
// dispatcher — as unit tests do — never panics on a transition.
func TestDispatch_NilDispatcherIsSafe(t *testing.T) {
	// Given: no dispatcher
	var d *alarmaction.Dispatcher

	// When/Then: dispatching and registering are both no-ops
	d.Register("autoscaling", func(context.Context, alarmaction.Transition, string) error { return nil })
	if outcomes := d.Dispatch(context.Background(), transition()); outcomes != nil {
		t.Fatalf("outcomes = %+v, want nil from a nil dispatcher", outcomes)
	}
}
