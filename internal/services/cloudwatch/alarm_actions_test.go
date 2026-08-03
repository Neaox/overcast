package cloudwatch

import (
	"context"
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

// TestAlarmEvaluatorLoop_EvaluatesOnTick pins that the background loop — not
// just the extracted evaluateAlarmsOnce the other tests drive — actually
// fires, and that it is driven by the injected clock rather than wall time.
func TestAlarmEvaluatorLoop_EvaluatesOnTick(t *testing.T) {
	// Given: a running service with a breaching alarm
	svc, mock := newRunningAlarmService(t)
	storeAlarm(t, svc, baseAlarm("ticked"))
	now := mock.Now().UTC()
	publishPoint(t, svc, "TestNS", "Latency", nil, now.Add(-30*time.Second), 250)

	// When: the mock clock advances past a tick, into a closed period
	deadline := time.Now().Add(5 * time.Second)
	for {
		mock.Add(10 * time.Second)
		if readAlarm(t, svc, "ticked").StateValue == "ALARM" {
			break
		}
		if time.Now().After(deadline) {
			// Then (failure): the loop never evaluated
			t.Fatalf("alarm never reached ALARM; state = %q", readAlarm(t, svc, "ticked").StateValue)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// recordingRouter captures the in-process requests the dispatcher makes, so a
// test can assert what was delivered without standing up SNS or EventBridge.
type recordingRouter struct {
	mu       sync.Mutex
	requests []recordedRequest
}

type recordedRequest struct {
	target string
	form   url.Values
	body   string
}

func (rr *recordingRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(r.Body)
	}
	rec := recordedRequest{target: r.Header.Get("X-Amz-Target"), body: string(body)}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		if form, err := url.ParseQuery(string(body)); err == nil {
			rec.form = form
		}
	}
	rr.mu.Lock()
	rr.requests = append(rr.requests, rec)
	rr.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (rr *recordingRouter) snapshot() []recordedRequest {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	return append([]recordedRequest(nil), rr.requests...)
}

// TestAlarmTransition_FiresActionsAndPublishesEvent pins the DoD's "real side
// effects, not just a stored field": an ALARM transition publishes the
// EventBridge state-change event and publishes to the alarm's SNS action.
func TestAlarmTransition_FiresActionsAndPublishesEvent(t *testing.T) {
	// Given: an alarm with an SNS alarm action, and a dispatcher wired to a
	// router that records what it receives
	svc, mock := newAlarmTestService(t)
	router := &recordingRouter{}
	svc.InitAlarmActions(alarmaction.NewDispatcher(router, "us-east-1", nil, zap.NewNop()))

	alarm := baseAlarm("with-actions")
	alarm.AlarmActions = []string{"arn:aws:sns:us-east-1:000000000000:alerts"}
	storeAlarm(t, svc, alarm)

	now := mock.Now().UTC()
	publishPoint(t, svc, "TestNS", "Latency", nil, now.Add(-30*time.Second), 250)

	// When: the alarm transitions to ALARM
	mock.Set(now.Add(30 * time.Second))
	svc.evaluateAlarmsOnce(context.Background())
	svc.waitForDispatch()

	// Then: an SNS Publish and an EventBridge PutEvents were both issued
	var sawPublish, sawPutEvents bool
	for _, req := range router.snapshot() {
		if req.target == "AWSEvents.PutEvents" && strings.Contains(req.body, "CloudWatch Alarm State Change") {
			sawPutEvents = true
		}
		if req.form.Get("Action") == "Publish" && req.form.Get("TopicArn") == "arn:aws:sns:us-east-1:000000000000:alerts" {
			sawPublish = true
			if !strings.Contains(req.form.Get("Message"), `"NewStateValue":"ALARM"`) {
				t.Errorf("SNS message did not carry the new state: %s", req.form.Get("Message"))
			}
		}
	}
	if !sawPutEvents {
		t.Error("no CloudWatch Alarm State Change event was published to EventBridge")
	}
	if !sawPublish {
		t.Error("the alarm's SNS action was not published to")
	}
}

// TestAlarmTransition_RegisteredHandlerClaimsItsARNs pins the seam issue #474
// builds on: a service that owns an action ARN type registers a handler for
// its ARN service segment and receives the transition, with no import
// relationship in either direction.
func TestAlarmTransition_RegisteredHandlerClaimsItsARNs(t *testing.T) {
	// Given: a dispatcher with an "autoscaling" handler registered, and an
	// alarm whose action is a scaling-policy ARN
	svc, mock := newAlarmTestService(t)
	dispatcher := alarmaction.NewDispatcher(&recordingRouter{}, "us-east-1", nil, zap.NewNop())

	var mu sync.Mutex
	var seen []alarmaction.Transition
	dispatcher.Register("autoscaling", func(_ context.Context, tr alarmaction.Transition, _ string) error {
		mu.Lock()
		seen = append(seen, tr)
		mu.Unlock()
		return nil
	})
	svc.InitAlarmActions(dispatcher)

	policyARN := "arn:aws:autoscaling:us-east-1:000000000000:scalingPolicy:abc:autoScalingGroupName/web:policyName/scale-out"
	alarm := baseAlarm("scaling")
	alarm.AlarmActions = []string{policyARN}
	storeAlarm(t, svc, alarm)

	now := mock.Now().UTC()
	publishPoint(t, svc, "TestNS", "Latency", nil, now.Add(-30*time.Second), 250)

	// When: the alarm transitions
	mock.Set(now.Add(30 * time.Second))
	svc.evaluateAlarmsOnce(context.Background())
	svc.waitForDispatch()

	// Then: the handler received exactly one transition, fully populated
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 {
		t.Fatalf("handler invocations = %d, want 1", len(seen))
	}
	if seen[0].NewState != "ALARM" || seen[0].OldState != "INSUFFICIENT_DATA" {
		t.Errorf("transition = %s -> %s, want INSUFFICIENT_DATA -> ALARM", seen[0].OldState, seen[0].NewState)
	}
	if seen[0].AlarmName != "scaling" || seen[0].Threshold != 100 {
		t.Errorf("transition did not carry the alarm's identity/threshold: %+v", seen[0])
	}
}

// TestAlarmTransition_UnsupportedActionIsRecordedLoudly pins §2.1 for action
// targets: an action Overcast has no sink for does not silently vanish — it
// lands in the alarm's history saying it was not executed.
func TestAlarmTransition_UnsupportedActionIsRecordedLoudly(t *testing.T) {
	// Given: an alarm whose action is an EC2 instance action, which Overcast
	// has no sink for
	svc, mock := newAlarmTestService(t)
	svc.InitAlarmActions(alarmaction.NewDispatcher(&recordingRouter{}, "us-east-1", nil, zap.NewNop()))

	alarm := baseAlarm("ec2-action")
	alarm.AlarmActions = []string{"arn:aws:automate:us-east-1:ec2:terminate"}
	storeAlarm(t, svc, alarm)

	now := mock.Now().UTC()
	publishPoint(t, svc, "TestNS", "Latency", nil, now.Add(-30*time.Second), 250)

	// When: the alarm transitions
	mock.Set(now.Add(30 * time.Second))
	svc.evaluateAlarmsOnce(context.Background())
	svc.waitForDispatch()

	// Then: the history says the action was not executed
	items, err := svc.store.listAlarmHistory(context.Background(), "ec2-action")
	if err != nil {
		t.Fatalf("listAlarmHistory: %v", err)
	}
	var found bool
	for _, item := range items {
		if item.HistoryItemType == "Action" && strings.Contains(item.HistorySummary, "NOT executed") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no Action history item reporting the undelivered target; got %+v", items)
	}
}

// waitForDispatch blocks until every in-flight transition dispatch has
// finished. Test-only: production code never needs it because Stop drains the
// same WaitGroup.
func (s *Service) waitForDispatch() { s.wg.Wait() }
