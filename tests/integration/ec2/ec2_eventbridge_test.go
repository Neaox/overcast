// EC2 Instance State-change Notification, delivered onto the default
// EventBridge bus the way real EC2 does. See:
// https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/monitoring-instance-state-changes.html
//
//	{
//	   "detail-type":"EC2 Instance State-change Notification",
//	   "source":"aws.ec2",
//	   "resources":["arn:aws:ec2:us-east-1:123456789012:instance/i-1234567890abcdef0"],
//	   "detail":{"instance-id":"i-1234567890abcdef0","state":"pending"}
//	}
//
// Overcast's scheduler fast-forwards a 0-delay transition inline under a real
// clock (see internal/lifecycle/scheduler.go), so RunInstances/StopInstances/
// StartInstances deliver both of their states synchronously; TerminateInstances'
// shutting-down → terminated transition carries a real 500ms delay and is
// awaited with a short poll, matching the pattern in
// tests/integration/s3/s3_eventbridge_notification_test.go.
package ec2_test

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/Neaox/overcast/tests/helpers"
)

// urlValues builds an EC2 Query-protocol parameter set from a flat map — a
// convenience wrapper around the url.Values{key: []string{value}} literal
// the rest of this package spells out by hand, since these tests build several.
func urlValues(params map[string]string) url.Values {
	v := make(url.Values, len(params))
	for key, value := range params {
		v.Set(key, value)
	}
	return v
}

// firstInstanceID extracts the first instance ID from a RunInstances
// response body without consuming it, so the caller can still close it.
func firstInstanceID(t *testing.T, resp *http.Response) string {
	t.Helper()
	var result struct {
		Instances []struct {
			InstanceID string `xml:"instanceId"`
		} `xml:"instancesSet>item"`
	}
	if err := xml.Unmarshal(readBody(t, resp), &result); err != nil {
		t.Fatalf("unmarshal RunInstancesResponse: %v", err)
	}
	if len(result.Instances) == 0 {
		t.Fatal("RunInstances response has no instances")
	}
	return result.Instances[0].InstanceID
}

// ---- EventBridge/SQS wire helpers (package-local; see the same pattern in
// tests/integration/s3/s3_eventbridge_notification_test.go and
// tests/integration/eventbridge/eventbridge_test.go) --------------------------

func eventBridgeCallForEC2(t *testing.T, srv *helpers.TestServer, operation string, body map[string]any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal EventBridge %s body: %v", operation, err)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "AWSEvents."+operation)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("EventBridge %s: %v", operation, err)
	}
	return resp
}

func sqsCallForEC2(t *testing.T, srv *helpers.TestServer, operation string, body map[string]any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal SQS %s body: %v", operation, err)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS."+operation)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SQS %s: %v", operation, err)
	}
	return resp
}

func createQueueForEC2EventBridge(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	resp := sqsCallForEC2(t, srv, "CreateQueue", map[string]any{"QueueName": name})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		QueueURL string `json:"QueueUrl"`
	}
	helpers.DecodeJSON(t, resp, &out)
	return out.QueueURL
}

func putEC2EventBridgeRule(t *testing.T, srv *helpers.TestServer, rule, pattern, targetARN string) {
	t.Helper()
	ruleResp := eventBridgeCallForEC2(t, srv, "PutRule", map[string]any{
		"Name":         rule,
		"EventPattern": pattern,
	})
	ruleResp.Body.Close()
	helpers.AssertStatus(t, ruleResp, http.StatusOK)

	targetResp := eventBridgeCallForEC2(t, srv, "PutTargets", map[string]any{
		"Rule":    rule,
		"Targets": []any{map[string]any{"Id": "queue", "Arn": targetARN}},
	})
	targetResp.Body.Close()
	helpers.AssertStatus(t, targetResp, http.StatusOK)
}

type ec2EventBridgeEnvelope struct {
	Source     string   `json:"source"`
	DetailType string   `json:"detail-type"`
	Resources  []string `json:"resources"`
	Detail     struct {
		InstanceID string `json:"instance-id"`
		State      string `json:"state"`
	} `json:"detail"`
}

// receiveEC2EventBridgeMessages drains up to maxCount currently-available
// messages from the queue without waiting — used where delivery is
// synchronous (a real clock's 0-delay scheduler fast path) so nothing to wait
// for should still be there on a second poll.
func receiveEC2EventBridgeMessages(t *testing.T, srv *helpers.TestServer, queueURL string, maxCount int) []ec2EventBridgeEnvelope {
	t.Helper()
	resp := sqsCallForEC2(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":            queueURL,
		"MaxNumberOfMessages": maxCount,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Messages []struct {
			Body string `json:"Body"`
		} `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp, &out)

	envelopes := make([]ec2EventBridgeEnvelope, 0, len(out.Messages))
	for _, m := range out.Messages {
		var e ec2EventBridgeEnvelope
		if err := json.Unmarshal([]byte(m.Body), &e); err != nil {
			t.Fatalf("delivered event is not valid JSON (%q): %v", m.Body, err)
		}
		envelopes = append(envelopes, e)
	}
	return envelopes
}

// waitForEC2EventBridgeMessages polls until at least want messages have
// arrived at the queue, for transitions the scheduler runs on a real delay
// (TerminateInstances' shutting-down → terminated, 500ms) rather than inline.
func waitForEC2EventBridgeMessages(t *testing.T, srv *helpers.TestServer, queueURL string, want int) []ec2EventBridgeEnvelope {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var got []ec2EventBridgeEnvelope
	for time.Now().Before(deadline) {
		got = append(got, receiveEC2EventBridgeMessages(t, srv, queueURL, want-len(got))...)
		if len(got) >= want {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("only %d of %d expected EventBridge deliveries arrived at %s", len(got), want, queueURL)
	return got
}

// stateOf finds the envelope reporting the given state, or fails the test —
// delivery order between two events published back-to-back is not a contract
// this test should pin down, only that both arrived with the right content.
func stateOf(t *testing.T, envelopes []ec2EventBridgeEnvelope, state string) ec2EventBridgeEnvelope {
	t.Helper()
	for _, e := range envelopes {
		if e.Detail.State == state {
			return e
		}
	}
	t.Fatalf("no delivered event reports state %q; got %+v", state, envelopes)
	return ec2EventBridgeEnvelope{}
}

// ---- Delivery ---------------------------------------------------------------

func TestRunInstances_deliversStateChangeNotificationsToEventBridge(t *testing.T) {
	// Given: a rule matching EC2's instance state-change event, targeting an
	// SQS queue
	srv := helpers.NewTestServer(t)
	queueURL := createQueueForEC2EventBridge(t, srv, "ec2-run-queue")
	putEC2EventBridgeRule(t, srv, "ec2-state-change",
		`{"source":["aws.ec2"],"detail-type":["EC2 Instance State-change Notification"]}`,
		"arn:aws:sqs:us-east-1:000000000000:ec2-run-queue")

	// When: RunInstances launches one instance
	resp := ec2Query(t, srv, "RunInstances", urlValues(map[string]string{
		"ImageId":      "ami-12345678",
		"InstanceType": "t3.micro",
		"MinCount":     "1",
		"MaxCount":     "1",
	}))
	helpers.AssertStatus(t, resp, http.StatusOK)
	instanceID := firstInstanceID(t, resp)
	resp.Body.Close()

	// Then: EventBridge delivers both states the instance actually passed
	// through — pending (the initial launch) and running (the scheduler's
	// 0-delay fast-forward, which runs inline under a real clock) — each
	// carrying the instance's own ID and AWS's documented resource ARN.
	envelopes := receiveEC2EventBridgeMessages(t, srv, queueURL, 2)
	if len(envelopes) != 2 {
		t.Fatalf("expected 2 delivered events, got %d: %+v", len(envelopes), envelopes)
	}

	pending := stateOf(t, envelopes, "pending")
	running := stateOf(t, envelopes, "running")
	for _, e := range []ec2EventBridgeEnvelope{pending, running} {
		if e.Source != "aws.ec2" {
			t.Errorf("source = %q, want aws.ec2", e.Source)
		}
		if e.DetailType != "EC2 Instance State-change Notification" {
			t.Errorf("detail-type = %q, want EC2 Instance State-change Notification", e.DetailType)
		}
		wantResource := "arn:aws:ec2:us-east-1:000000000000:instance/" + instanceID
		if len(e.Resources) != 1 || e.Resources[0] != wantResource {
			t.Errorf("resources = %v, want [%s]", e.Resources, wantResource)
		}
		if e.Detail.InstanceID != instanceID {
			t.Errorf("detail.instance-id = %q, want %q", e.Detail.InstanceID, instanceID)
		}
	}
}

func TestStopInstances_deliversStateChangeNotificationsToEventBridge(t *testing.T) {
	// Given: a running instance and a rule targeting an SQS queue
	srv := helpers.NewTestServer(t)
	queueURL := createQueueForEC2EventBridge(t, srv, "ec2-stop-queue")
	putEC2EventBridgeRule(t, srv, "ec2-state-change",
		`{"source":["aws.ec2"],"detail-type":["EC2 Instance State-change Notification"]}`,
		"arn:aws:sqs:us-east-1:000000000000:ec2-stop-queue")

	runResp := ec2Query(t, srv, "RunInstances", urlValues(map[string]string{
		"ImageId":  "ami-12345678",
		"MinCount": "1",
		"MaxCount": "1",
	}))
	helpers.AssertStatus(t, runResp, http.StatusOK)
	instanceID := firstInstanceID(t, runResp)
	runResp.Body.Close()
	// Drain the launch's own pending/running deliveries so this test only
	// asserts on what StopInstances itself produced.
	receiveEC2EventBridgeMessages(t, srv, queueURL, 2)

	// When: the instance is stopped
	stopResp := ec2Query(t, srv, "StopInstances", urlValues(map[string]string{
		"InstanceId.1": instanceID,
	}))
	helpers.AssertStatus(t, stopResp, http.StatusOK)
	stopResp.Body.Close()

	// Then: EventBridge delivers stopping and stopped
	envelopes := receiveEC2EventBridgeMessages(t, srv, queueURL, 2)
	if len(envelopes) != 2 {
		t.Fatalf("expected 2 delivered events, got %d: %+v", len(envelopes), envelopes)
	}
	stopOf := stateOf(t, envelopes, "stopping")
	stoppedOf := stateOf(t, envelopes, "stopped")
	if stopOf.Detail.InstanceID != instanceID {
		t.Errorf("stopping event instance-id = %q, want %q", stopOf.Detail.InstanceID, instanceID)
	}
	if stoppedOf.Detail.InstanceID != instanceID {
		t.Errorf("stopped event instance-id = %q, want %q", stoppedOf.Detail.InstanceID, instanceID)
	}
}

func TestTerminateInstances_deliversStateChangeNotificationsToEventBridge(t *testing.T) {
	// Given: a running instance and a rule targeting an SQS queue
	srv := helpers.NewTestServer(t)
	queueURL := createQueueForEC2EventBridge(t, srv, "ec2-terminate-queue")
	putEC2EventBridgeRule(t, srv, "ec2-state-change",
		`{"source":["aws.ec2"],"detail-type":["EC2 Instance State-change Notification"]}`,
		"arn:aws:sqs:us-east-1:000000000000:ec2-terminate-queue")

	runResp := ec2Query(t, srv, "RunInstances", urlValues(map[string]string{
		"ImageId":  "ami-12345678",
		"MinCount": "1",
		"MaxCount": "1",
	}))
	helpers.AssertStatus(t, runResp, http.StatusOK)
	instanceID := firstInstanceID(t, runResp)
	runResp.Body.Close()
	receiveEC2EventBridgeMessages(t, srv, queueURL, 2) // drain launch deliveries

	// When: the instance is terminated
	termResp := ec2Query(t, srv, "TerminateInstances", urlValues(map[string]string{
		"InstanceId.1": instanceID,
	}))
	helpers.AssertStatus(t, termResp, http.StatusOK)
	termResp.Body.Close()

	// Then: EventBridge delivers shutting-down immediately and terminated once
	// the scheduler's real 500ms delay elapses.
	envelopes := waitForEC2EventBridgeMessages(t, srv, queueURL, 2)
	shuttingDown := stateOf(t, envelopes, "shutting-down")
	terminated := stateOf(t, envelopes, "terminated")
	if shuttingDown.Detail.InstanceID != instanceID {
		t.Errorf("shutting-down event instance-id = %q, want %q", shuttingDown.Detail.InstanceID, instanceID)
	}
	if terminated.Detail.InstanceID != instanceID {
		t.Errorf("terminated event instance-id = %q, want %q", terminated.Detail.InstanceID, instanceID)
	}
}

func TestEC2StateChangeRule_filtersOnDetailState(t *testing.T) {
	// Given: a rule matching only the "stopped" state — proving the delivered
	// detail is real enough for EventBridge's own content filtering to match
	// on it, not just detail-type
	srv := helpers.NewTestServer(t)
	queueURL := createQueueForEC2EventBridge(t, srv, "ec2-stopped-only-queue")
	putEC2EventBridgeRule(t, srv, "ec2-stopped-only",
		`{"source":["aws.ec2"],"detail-type":["EC2 Instance State-change Notification"],"detail":{"state":["stopped"]}}`,
		"arn:aws:sqs:us-east-1:000000000000:ec2-stopped-only-queue")

	runResp := ec2Query(t, srv, "RunInstances", urlValues(map[string]string{
		"ImageId":  "ami-12345678",
		"MinCount": "1",
		"MaxCount": "1",
	}))
	helpers.AssertStatus(t, runResp, http.StatusOK)
	instanceID := firstInstanceID(t, runResp)
	runResp.Body.Close()

	// When: the instance is stopped (pending, running, stopping, stopped all
	// occur; only "stopped" matches the rule's pattern)
	stopResp := ec2Query(t, srv, "StopInstances", urlValues(map[string]string{
		"InstanceId.1": instanceID,
	}))
	helpers.AssertStatus(t, stopResp, http.StatusOK)
	stopResp.Body.Close()

	// Then: exactly one delivery, and it is the stopped state
	envelopes := receiveEC2EventBridgeMessages(t, srv, queueURL, 5)
	if len(envelopes) != 1 {
		t.Fatalf("expected exactly 1 delivered event (stopped only), got %d: %+v", len(envelopes), envelopes)
	}
	if envelopes[0].Detail.State != "stopped" {
		t.Errorf("delivered state = %q, want stopped", envelopes[0].Detail.State)
	}
}
