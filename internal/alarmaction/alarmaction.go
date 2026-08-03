// Package alarmaction delivers CloudWatch alarm state transitions to whatever
// the alarm's action ARNs name.
//
// It is the "an alarm changed state → notify its actions" seam, extracted as a
// standalone package rather than buried inside internal/services/cloudwatch so
// that a second service can both consume transitions and own a target type
// without either service importing the other. Auto Scaling (#474) is the
// intended next consumer: a target-tracking or step-scaling policy ARN appears
// in an alarm's AlarmActions, and Auto Scaling claims it with
// Dispatcher.Register("autoscaling", …) — see the package example in
// alarmaction_test.go. This mirrors what internal/eventtarget does for
// EventBridge rule targets (#467).
//
// Delivery goes through the emulator's own root router rather than through
// per-service Go interfaces, so an SNS notification takes exactly the path an
// SDK client's Publish would: a missing topic produces SNS's own AWS error
// instead of a silent no-op, which is what makes an undelivered action
// reportable at all.
//
// Nothing here blocks or does I/O at construction time: NewDispatcher only
// stores handles.
package alarmaction

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
)

// Transition is one alarm state change, and everything a consumer needs to
// react to it without calling back into CloudWatch.
type Transition struct {
	// AlarmName and AlarmARN identify the alarm.
	AlarmName string
	AlarmARN  string
	// Region and AccountID scope the EventBridge event this produces.
	Region    string
	AccountID string

	// OldState and NewState are AWS state values: OK, ALARM or
	// INSUFFICIENT_DATA.
	OldState string
	NewState string
	// Reason is the human-readable StateReason; ReasonData is the
	// StateReasonData JSON document (may be empty).
	Reason     string
	ReasonData string
	// Timestamp is when the transition happened, from the injected clock.
	Timestamp time.Time

	// Actions are the action ARNs configured for NewState. Empty when the
	// alarm configures none for that state.
	Actions []string
	// ActionsEnabled mirrors the alarm's flag. When false the transition is
	// still published as an event but no action is delivered, matching AWS.
	ActionsEnabled bool

	// AlarmDescription is echoed into the SNS notification body.
	AlarmDescription string

	// Namespace, MetricName, Statistic, Period, EvaluationPeriods, Threshold
	// and ComparisonOperator describe what was evaluated. They populate the
	// EventBridge event's detail.configuration block and the SNS
	// notification's Trigger.
	Namespace          string
	MetricName         string
	Statistic          string
	Period             int
	EvaluationPeriods  int
	Threshold          float64
	ComparisonOperator string
}

// Outcome records what happened to one action ARN.
type Outcome struct {
	// ARN is the action target.
	ARN string
	// Unsupported is true when no sink claims this ARN's service. The
	// transition still happened; the action did not.
	Unsupported bool
	// Err is the sink's own error when delivery was attempted and failed.
	Err error
}

// Handler delivers a transition to one ARN of a service a consumer owns.
// Registered handlers run on the dispatching goroutine and should not block
// for long.
type Handler func(ctx context.Context, t Transition, arn string) error

// Dispatcher fans an alarm transition out to its action ARNs, and publishes
// the state-change event real CloudWatch publishes.
//
// The zero value is not usable; construct one with NewDispatcher. A Dispatcher
// is safe for concurrent use.
type Dispatcher struct {
	router        http.Handler
	defaultRegion string
	bus           *events.Bus
	log           *zap.Logger

	mu       sync.RWMutex
	handlers map[string]Handler
}

// NewDispatcher returns a Dispatcher delivering through router — the
// emulator's root handler. defaultRegion is used when neither the request
// context nor the transition names one. bus and log may be nil; delivery still
// works, it is just not observable.
func NewDispatcher(router http.Handler, defaultRegion string, bus *events.Bus, log *zap.Logger) *Dispatcher {
	if log == nil {
		log = zap.NewNop()
	}
	return &Dispatcher{
		router:        router,
		defaultRegion: defaultRegion,
		bus:           bus,
		log:           log,
		handlers:      make(map[string]Handler),
	}
}

// Register claims every action ARN whose service segment is service (e.g.
// "autoscaling") for h, replacing any previous claim. This is the extension
// point for a service that owns an alarm action type: it needs no import of
// the CloudWatch package, and CloudWatch needs no import of it.
func (d *Dispatcher) Register(service string, h Handler) {
	if d == nil || h == nil || service == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.handlers[service] = h
}

// handlerFor returns the handler claiming service, if any.
func (d *Dispatcher) handlerFor(service string) (Handler, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	h, ok := d.handlers[service]
	return h, ok
}

// Dispatch publishes t's state-change event and delivers t to each of its
// action ARNs, returning one Outcome per ARN in the order they were
// configured. It never returns an error of its own: an alarm transition has
// already happened by the time this runs, so a failed action is reported, not
// unwound.
//
// A nil Dispatcher is a no-op, which is what makes the CloudWatch service
// usable in unit tests that never wire one.
func (d *Dispatcher) Dispatch(ctx context.Context, t Transition) []Outcome {
	if d == nil {
		return nil
	}

	d.publishEventBridge(ctx, t)
	d.publishBus(ctx, t)

	if !t.ActionsEnabled || len(t.Actions) == 0 {
		return nil
	}

	outcomes := make([]Outcome, 0, len(t.Actions))
	for _, arn := range t.Actions {
		outcomes = append(outcomes, d.deliver(ctx, t, arn))
	}
	return outcomes
}

// deliver routes one action ARN to its sink.
func (d *Dispatcher) deliver(ctx context.Context, t Transition, arn string) Outcome {
	svc, ok := arnService(arn)
	if !ok {
		d.log.Warn("cloudwatch alarm action target is not an ARN",
			zap.String("alarm", t.AlarmName), zap.String("target", arn))
		return Outcome{ARN: arn, Unsupported: true}
	}

	if h, found := d.handlerFor(svc); found {
		if err := h(ctx, t, arn); err != nil {
			d.log.Warn("cloudwatch alarm action failed",
				zap.String("alarm", t.AlarmName), zap.String("target", arn), zap.Error(err))
			return Outcome{ARN: arn, Err: err}
		}
		return Outcome{ARN: arn}
	}

	if svc == "sns" {
		if err := d.deliverSNS(ctx, t, arn); err != nil {
			d.log.Warn("cloudwatch alarm action failed",
				zap.String("alarm", t.AlarmName), zap.String("target", arn), zap.Error(err))
			return Outcome{ARN: arn, Err: err}
		}
		return Outcome{ARN: arn}
	}

	// Real CloudWatch also accepts EC2 instance actions, Systems Manager
	// OpsItems and incident-manager response plans. Overcast has no sink for
	// those, and an action that looks configured but never fires is exactly
	// what docs/plans/full-emulation-priority.md §2.1 forbids — so say so
	// loudly rather than dropping it.
	d.log.Warn("cloudwatch alarm action target is not supported; the alarm transitioned but no action was taken",
		zap.String("alarm", t.AlarmName),
		zap.String("target", arn),
		zap.String("target_service", svc))
	return Outcome{ARN: arn, Unsupported: true}
}

// deliverSNS publishes the alarm notification to an SNS topic through the
// Query protocol, the only wire format the SNS service exposes Publish on.
// Subject and message body match real CloudWatch's notification.
func (d *Dispatcher) deliverSNS(ctx context.Context, t Transition, arn string) error {
	if d.router == nil {
		return fmt.Errorf("alarmaction: no router configured")
	}
	message, err := json.Marshal(snsNotification(t))
	if err != nil {
		return err
	}
	form := url.Values{
		"Action":   {"Publish"},
		"Version":  {"2010-03-31"},
		"TopicArn": {arn},
		"Subject":  {snsSubject(t)},
		"Message":  {string(message)},
	}
	req, err := d.newRequest(ctx, "/", []byte(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return d.do(req)
}

// snsSubject reproduces the subject line real CloudWatch puts on an alarm
// notification: `ALARM: "my-alarm" in US East (N. Virginia)` — with the region
// code in place of its display name, which Overcast does not carry a table for.
func snsSubject(t Transition) string {
	return t.NewState + `: "` + t.AlarmName + `" in ` + t.Region
}

// snsNotification builds the JSON body real CloudWatch publishes to an
// alarm's SNS action.
func snsNotification(t Transition) map[string]any {
	return map[string]any{
		"AlarmName":        t.AlarmName,
		"AlarmDescription": t.AlarmDescription,
		"AWSAccountId":     t.AccountID,
		"AlarmArn":         t.AlarmARN,
		"NewStateValue":    t.NewState,
		"NewStateReason":   t.Reason,
		"StateChangeTime":  t.Timestamp.UTC().Format(TimestampLayout),
		"Region":           t.Region,
		"OldStateValue":    t.OldState,
		"Trigger": map[string]any{
			"MetricName":         t.MetricName,
			"Namespace":          t.Namespace,
			"Statistic":          strings.ToUpper(t.Statistic),
			"Period":             t.Period,
			"EvaluationPeriods":  t.EvaluationPeriods,
			"ComparisonOperator": t.ComparisonOperator,
			"Threshold":          t.Threshold,
		},
	}
}

// TimestampLayout is the millisecond-precision UTC layout AWS uses in alarm
// notification bodies and StateReasonData documents.
const TimestampLayout = "2006-01-02T15:04:05.000-0700"

// publishEventBridge emits the `CloudWatch Alarm State Change` event real
// CloudWatch puts on the default event bus, through the emulator's own
// PutEvents so that any rule matching it is evaluated exactly as it would be
// for an SDK-published event.
func (d *Dispatcher) publishEventBridge(ctx context.Context, t Transition) {
	if d.router == nil {
		return
	}
	detail := map[string]any{
		"alarmName": t.AlarmName,
		"state": map[string]any{
			"value":     t.NewState,
			"reason":    t.Reason,
			"timestamp": t.Timestamp.UTC().Format(TimestampLayout),
		},
		"previousState": map[string]any{
			"value":     t.OldState,
			"timestamp": t.Timestamp.UTC().Format(TimestampLayout),
		},
		"configuration": map[string]any{
			"metrics": []any{
				map[string]any{
					"id": "m1",
					"metricStat": map[string]any{
						"metric": map[string]any{
							"namespace": t.Namespace,
							"name":      t.MetricName,
						},
						"period": t.Period,
						"stat":   t.Statistic,
					},
					"returnData": true,
				},
			},
		},
	}
	if t.ReasonData != "" {
		// AWS carries the StateReasonData document verbatim, as a string.
		if state, ok := detail["state"].(map[string]any); ok {
			state["reasonData"] = t.ReasonData
		}
	}
	detailJSON, err := json.Marshal(detail)
	if err != nil {
		return
	}

	body, err := json.Marshal(map[string]any{
		"Entries": []any{
			map[string]any{
				"Source":     "aws.cloudwatch",
				"DetailType": "CloudWatch Alarm State Change",
				"Detail":     string(detailJSON),
				"Resources":  []string{t.AlarmARN},
				"Time":       t.Timestamp.UTC().Format(time.RFC3339),
			},
		},
	})
	if err != nil {
		return
	}
	req, err := d.newRequest(ctx, "/", body)
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "AWSEvents.PutEvents")
	if err := d.do(req); err != nil {
		d.log.Debug("cloudwatch alarm state-change event was not published",
			zap.String("alarm", t.AlarmName), zap.Error(err))
	}
}

// publishBus mirrors the transition onto the internal event bus so the web
// UI's live event console shows alarms changing state without polling.
func (d *Dispatcher) publishBus(_ context.Context, t Transition) {
	if d.bus == nil {
		return
	}
	d.bus.Publish(context.Background(), events.Event{
		Type:   events.CloudWatchAlarmStateChange,
		Source: "cloudwatch",
		Payload: events.CloudWatchAlarmStateChangePayload{
			AlarmName: t.AlarmName,
			ARN:       t.AlarmARN,
			OldState:  t.OldState,
			NewState:  t.NewState,
			Reason:    t.Reason,
		},
	})
}

// arnService extracts the service segment of an ARN, validating the overall
// arn:partition:service:region:account:resource shape. Deliberately the same
// check internal/eventtarget applies to rule targets.
func arnService(arn string) (string, bool) {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) != 6 {
		return "", false
	}
	if parts[0] != "arn" || parts[1] == "" || parts[2] == "" || parts[5] == "" {
		return "", false
	}
	return parts[2], true
}

// newRequest builds an in-process request carrying the transition's region, so
// a target outside the default region resolves against its own regional state.
func (d *Dispatcher) newRequest(ctx context.Context, path string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	region := d.defaultRegion
	if r := middleware.RegionFromContext(ctx, ""); r != "" {
		region = r
	}
	req.Header.Set("X-Overcast-Region", region)
	return req, nil
}

// do runs the request against the root router and turns any non-2xx response
// into an error carrying the sink's own message.
func (d *Dispatcher) do(req *http.Request) error {
	rec := httptest.NewRecorder()
	d.router.ServeHTTP(rec, req)
	if rec.Code >= 400 {
		return fmt.Errorf("HTTP %d: %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	return nil
}
