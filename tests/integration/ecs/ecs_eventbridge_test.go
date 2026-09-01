// ECS Task State Change, delivered onto the default EventBridge bus the way
// real ECS does. See:
// https://docs.aws.amazon.com/AmazonECS/latest/developerguide/ecs_task_events.html
//
// AWS's own detail carries substantially more than what Overcast populates
// (capacityProviderName, attributes, ephemeralStorage, per-container network
// interfaces, the per-resource "version" counter, …) — those are omitted
// rather than invented, per internal/services/ecs/eventbridge.go's doc
// comment. These tests only assert on the fields Overcast does track.
package ecs_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// ---- EventBridge/SQS wire helpers (package-local; see the same pattern in
// tests/integration/s3/s3_eventbridge_notification_test.go and
// tests/integration/eventbridge/eventbridge_test.go) --------------------------

func eventBridgeCallForECS(t *testing.T, srv *helpers.TestServer, operation string, body map[string]any) *http.Response {
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

func sqsCallForECSEventBridge(t *testing.T, srv *helpers.TestServer, operation string, body map[string]any) *http.Response {
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

func createQueueForECSEventBridge(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	resp := sqsCallForECSEventBridge(t, srv, "CreateQueue", map[string]any{"QueueName": name})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		QueueURL string `json:"QueueUrl"`
	}
	helpers.DecodeJSON(t, resp, &out)
	return out.QueueURL
}

func putECSEventBridgeRule(t *testing.T, srv *helpers.TestServer, rule, pattern, targetARN string) {
	t.Helper()
	ruleResp := eventBridgeCallForECS(t, srv, "PutRule", map[string]any{
		"Name":         rule,
		"EventPattern": pattern,
	})
	ruleResp.Body.Close()
	helpers.AssertStatus(t, ruleResp, http.StatusOK)

	targetResp := eventBridgeCallForECS(t, srv, "PutTargets", map[string]any{
		"Rule":    rule,
		"Targets": []any{map[string]any{"Id": "queue", "Arn": targetARN}},
	})
	targetResp.Body.Close()
	helpers.AssertStatus(t, targetResp, http.StatusOK)
}

type ecsEventBridgeEnvelope struct {
	Source     string   `json:"source"`
	DetailType string   `json:"detail-type"`
	Resources  []string `json:"resources"`
	Detail     struct {
		ClusterArn    string `json:"clusterArn"`
		TaskArn       string `json:"taskArn"`
		LastStatus    string `json:"lastStatus"`
		DesiredStatus string `json:"desiredStatus"`
		StoppedReason string `json:"stoppedReason"`
	} `json:"detail"`
}

// receiveECSEventBridgeMessages drains up to maxCount currently-available
// messages from the queue without waiting — RunTask/StopTask's own state
// writes are synchronous, so nothing is left to arrive after this call
// returns.
func receiveECSEventBridgeMessages(t *testing.T, srv *helpers.TestServer, queueURL string, maxCount int) []ecsEventBridgeEnvelope {
	t.Helper()
	resp := sqsCallForECSEventBridge(t, srv, "ReceiveMessage", map[string]any{
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

	envelopes := make([]ecsEventBridgeEnvelope, 0, len(out.Messages))
	for _, m := range out.Messages {
		var e ecsEventBridgeEnvelope
		if err := json.Unmarshal([]byte(m.Body), &e); err != nil {
			t.Fatalf("delivered event is not valid JSON (%q): %v", m.Body, err)
		}
		envelopes = append(envelopes, e)
	}
	return envelopes
}

// ---- Delivery ---------------------------------------------------------------

func TestRunTask_deliversTaskStateChangeToEventBridge(t *testing.T) {
	// Given: a rule matching ECS's task state-change event, targeting an SQS
	// queue, a cluster and a registered task definition
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	queueURL := createQueueForECSEventBridge(t, srv, "ecs-run-queue")
	putECSEventBridgeRule(t, srv, "ecs-task-state-change",
		`{"source":["aws.ecs"],"detail-type":["ECS Task State Change"]}`,
		"arn:aws:sqs:us-east-1:000000000000:ecs-run-queue")

	cr := ecsCall(t, srv, "CreateCluster", map[string]any{"clusterName": "eb-run-cluster"})
	helpers.AssertStatus(t, cr, http.StatusOK)
	cr.Body.Close()

	reg := ecsCall(t, srv, "RegisterTaskDefinition", map[string]any{
		"family": "eb-web",
		"containerDefinitions": []map[string]any{
			{"name": "app", "image": "nginx:latest"},
		},
		"cpu":    "256",
		"memory": "512",
	})
	helpers.AssertStatus(t, reg, http.StatusOK)
	reg.Body.Close()

	// When: RunTask launches one task (no Docker wired: it stays at
	// PROVISIONING, never misrepresented as RUNNING — see launch.go)
	run := ecsCall(t, srv, "RunTask", map[string]any{
		"cluster":        "eb-run-cluster",
		"taskDefinition": "eb-web:1",
	})
	helpers.AssertStatus(t, run, http.StatusOK)
	var runResult struct {
		Tasks []struct {
			TaskArn    string `json:"taskArn"`
			ClusterArn string `json:"clusterArn"`
		} `json:"tasks"`
	}
	helpers.DecodeJSON(t, run, &runResult)
	run.Body.Close()
	if len(runResult.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(runResult.Tasks))
	}
	taskArn := runResult.Tasks[0].TaskArn
	clusterArn := runResult.Tasks[0].ClusterArn

	// Then: EventBridge delivers the task's first (and, without Docker, only)
	// state: PROVISIONING.
	envelopes := receiveECSEventBridgeMessages(t, srv, queueURL, 5)
	if len(envelopes) != 1 {
		t.Fatalf("expected 1 delivered event, got %d: %+v", len(envelopes), envelopes)
	}
	e := envelopes[0]
	if e.Source != "aws.ecs" {
		t.Errorf("source = %q, want aws.ecs", e.Source)
	}
	if e.DetailType != "ECS Task State Change" {
		t.Errorf("detail-type = %q, want ECS Task State Change", e.DetailType)
	}
	if len(e.Resources) != 1 || e.Resources[0] != taskArn {
		t.Errorf("resources = %v, want [%s]", e.Resources, taskArn)
	}
	if e.Detail.TaskArn != taskArn {
		t.Errorf("detail.taskArn = %q, want %q", e.Detail.TaskArn, taskArn)
	}
	if e.Detail.ClusterArn != clusterArn {
		t.Errorf("detail.clusterArn = %q, want %q", e.Detail.ClusterArn, clusterArn)
	}
	if e.Detail.LastStatus != "PROVISIONING" {
		t.Errorf("detail.lastStatus = %q, want PROVISIONING", e.Detail.LastStatus)
	}
}

func TestStopTask_deliversTaskStateChangeToEventBridge(t *testing.T) {
	// Given: a task already launched, and its own launch delivery drained
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	queueURL := createQueueForECSEventBridge(t, srv, "ecs-stop-queue")
	putECSEventBridgeRule(t, srv, "ecs-task-state-change",
		`{"source":["aws.ecs"],"detail-type":["ECS Task State Change"]}`,
		"arn:aws:sqs:us-east-1:000000000000:ecs-stop-queue")

	cr := ecsCall(t, srv, "CreateCluster", map[string]any{"clusterName": "eb-stop-cluster"})
	helpers.AssertStatus(t, cr, http.StatusOK)
	cr.Body.Close()

	reg := ecsCall(t, srv, "RegisterTaskDefinition", map[string]any{
		"family":               "eb-svc",
		"containerDefinitions": []map[string]any{{"name": "c", "image": "alpine"}},
	})
	helpers.AssertStatus(t, reg, http.StatusOK)
	reg.Body.Close()

	run := ecsCall(t, srv, "RunTask", map[string]any{
		"cluster":        "eb-stop-cluster",
		"taskDefinition": "eb-svc:1",
	})
	helpers.AssertStatus(t, run, http.StatusOK)
	var runResult struct {
		Tasks []struct {
			TaskArn string `json:"taskArn"`
		} `json:"tasks"`
	}
	helpers.DecodeJSON(t, run, &runResult)
	run.Body.Close()
	taskArn := runResult.Tasks[0].TaskArn
	receiveECSEventBridgeMessages(t, srv, queueURL, 5) // drain the launch delivery

	// When: StopTask is called
	stop := ecsCall(t, srv, "StopTask", map[string]any{
		"cluster": "eb-stop-cluster",
		"task":    taskArn,
		"reason":  "eventbridge test",
	})
	helpers.AssertStatus(t, stop, http.StatusOK)
	stop.Body.Close()

	// Then: EventBridge delivers the STOPPED transition
	envelopes := receiveECSEventBridgeMessages(t, srv, queueURL, 5)
	if len(envelopes) != 1 {
		t.Fatalf("expected 1 delivered event, got %d: %+v", len(envelopes), envelopes)
	}
	e := envelopes[0]
	if e.Detail.TaskArn != taskArn {
		t.Errorf("detail.taskArn = %q, want %q", e.Detail.TaskArn, taskArn)
	}
	if e.Detail.LastStatus != "STOPPED" {
		t.Errorf("detail.lastStatus = %q, want STOPPED", e.Detail.LastStatus)
	}
	if e.Detail.DesiredStatus != "STOPPED" {
		t.Errorf("detail.desiredStatus = %q, want STOPPED", e.Detail.DesiredStatus)
	}
	if e.Detail.StoppedReason != "eventbridge test" {
		t.Errorf("detail.stoppedReason = %q, want %q", e.Detail.StoppedReason, "eventbridge test")
	}
}

func TestECSTaskStateChangeRule_filtersOnDetailLastStatus(t *testing.T) {
	// Given: a rule matching only lastStatus=STOPPED — proving the delivered
	// detail is real enough for EventBridge's own content filtering to match
	// on it, not just detail-type
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	queueURL := createQueueForECSEventBridge(t, srv, "ecs-stopped-only-queue")
	putECSEventBridgeRule(t, srv, "ecs-stopped-only",
		`{"source":["aws.ecs"],"detail-type":["ECS Task State Change"],"detail":{"lastStatus":["STOPPED"]}}`,
		"arn:aws:sqs:us-east-1:000000000000:ecs-stopped-only-queue")

	cr := ecsCall(t, srv, "CreateCluster", map[string]any{"clusterName": "eb-filter-cluster"})
	helpers.AssertStatus(t, cr, http.StatusOK)
	cr.Body.Close()
	reg := ecsCall(t, srv, "RegisterTaskDefinition", map[string]any{
		"family":               "eb-filter",
		"containerDefinitions": []map[string]any{{"name": "c", "image": "alpine"}},
	})
	helpers.AssertStatus(t, reg, http.StatusOK)
	reg.Body.Close()

	run := ecsCall(t, srv, "RunTask", map[string]any{
		"cluster":        "eb-filter-cluster",
		"taskDefinition": "eb-filter:1",
	})
	helpers.AssertStatus(t, run, http.StatusOK)
	var runResult struct {
		Tasks []struct {
			TaskArn string `json:"taskArn"`
		} `json:"tasks"`
	}
	helpers.DecodeJSON(t, run, &runResult)
	run.Body.Close()
	taskArn := runResult.Tasks[0].TaskArn

	// When: the task is stopped (PROVISIONING then STOPPED occur; only
	// STOPPED matches the rule's pattern)
	stop := ecsCall(t, srv, "StopTask", map[string]any{
		"cluster": "eb-filter-cluster",
		"task":    taskArn,
	})
	helpers.AssertStatus(t, stop, http.StatusOK)
	stop.Body.Close()

	// Then: exactly one delivery, and it is the STOPPED state
	envelopes := receiveECSEventBridgeMessages(t, srv, queueURL, 5)
	if len(envelopes) != 1 {
		t.Fatalf("expected exactly 1 delivered event (STOPPED only), got %d: %+v", len(envelopes), envelopes)
	}
	if envelopes[0].Detail.LastStatus != "STOPPED" {
		t.Errorf("delivered lastStatus = %q, want STOPPED", envelopes[0].Detail.LastStatus)
	}
}
