// Scheduler target dispatch integration tests (issue #734).
//
// The cron engine used to fire Lambda and SQS targets only: every other target
// ARN was accepted by CreateSchedule, matched nothing in an ad-hoc string
// switch, and was dropped with a warn-level log line. These tests pin the
// delivery behaviour for the target kinds internal/eventtarget can reach — the
// same seam EventBridge rules and Pipes deliver through — plus the target
// parameters AWS carries on a schedule, the retry/dead-letter handling of a
// failed firing, and the loud refusal of a target kind that could never fire.
package scheduler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/tests/helpers"
)

// ─── Helpers ─────────────────────────────────────────────────────────────────

// jsonCallForScheduler performs an AWS JSON-protocol X-Amz-Target request.
func jsonCallForScheduler(t *testing.T, srv *helpers.TestServer, target string, body map[string]any) *http.Response {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s body: %v", target, err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatalf("build %s request: %v", target, err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", target)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s: %v", target, err)
	}
	return resp
}

// snsCallForScheduler performs an AWS Query-protocol SNS request.
func snsCallForScheduler(t *testing.T, srv *helpers.TestServer, action string, params url.Values) *http.Response {
	t.Helper()
	params.Set("Action", action)
	params.Set("Version", "2010-03-31")
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(params.Encode()))
	if err != nil {
		t.Fatalf("build SNS %s request: %v", action, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SNS %s: %v", action, err)
	}
	return resp
}

// createQueueForSchedule creates an SQS queue and returns its URL.
func createQueueForSchedule(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	resp := jsonCallForScheduler(t, srv, "AmazonSQS.CreateQueue", map[string]any{"QueueName": name})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		QueueURL string `json:"QueueUrl"`
	}
	helpers.DecodeJSON(t, resp, &out)
	return out.QueueURL
}

// createScheduleWithTarget creates an ENABLED rate(1 minute) schedule in the
// default group. A never-fired rate schedule is due on the next engine tick, so
// callers only have to advance the mock clock past one tick to fire it once.
func createScheduleWithTarget(t *testing.T, srv *helpers.TestServer, name string, target map[string]any) {
	t.Helper()
	resp := schDo(t, srv, http.MethodPost, "/schedules/"+name, map[string]any{
		"ScheduleExpression": "rate(1 minute)",
		"FlexibleTimeWindow": map[string]any{"Mode": "OFF"},
		"Target":             target,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// fireOnce advances the mock clock past a single engine tick.
func fireOnce(t *testing.T, srv *helpers.TestServer) {
	t.Helper()
	srv.Clock.Add(2 * time.Second)
}

// receiveOneScheduledMessage polls an SQS queue until a message arrives.
func receiveOneScheduledMessage(t *testing.T, srv *helpers.TestServer, queueURL string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp := jsonCallForScheduler(t, srv, "AmazonSQS.ReceiveMessage", map[string]any{
			"QueueUrl":            queueURL,
			"MaxNumberOfMessages": 1,
		})
		helpers.AssertStatus(t, resp, http.StatusOK)
		var out struct {
			Messages []struct {
				Body string `json:"Body"`
			} `json:"Messages"`
		}
		helpers.DecodeJSON(t, resp, &out)
		resp.Body.Close()
		if len(out.Messages) == 1 {
			return out.Messages[0].Body
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no message delivered to %s within the timeout", queueURL)
	return ""
}

// ─── SNS target ───────────────────────────────────────────────────────────────

func TestSchedule_deliversToSNSTarget(t *testing.T) {
	// Given: an SNS topic with an SQS subscription, targeted by a schedule
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	queueURL := createQueueForSchedule(t, srv, "sched-sns-queue")

	resp := snsCallForScheduler(t, srv, "CreateTopic", url.Values{"Name": {"sched-topic"}})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	topicARN := "arn:aws:sns:us-east-1:000000000000:sched-topic"

	resp = snsCallForScheduler(t, srv, "Subscribe", url.Values{
		"TopicArn": {topicARN},
		"Protocol": {"sqs"},
		"Endpoint": {"arn:aws:sqs:us-east-1:000000000000:sched-sns-queue"},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	createScheduleWithTarget(t, srv, "sns-schedule", map[string]any{
		"Arn":     topicARN,
		"RoleArn": "arn:aws:iam::000000000000:role/scheduler-role",
		"Input":   `{"orderId":"o-1"}`,
	})

	// When: the schedule becomes due
	fireOnce(t, srv)

	// Then: the subscribed queue receives the schedule's input through SNS
	body := receiveOneScheduledMessage(t, srv, queueURL)
	if !strings.Contains(body, "o-1") {
		t.Fatalf("SNS-delivered message did not carry the schedule input: %s", body)
	}
}

// ─── Step Functions target ────────────────────────────────────────────────────

func TestSchedule_deliversToStepFunctionsTarget(t *testing.T) {
	// Given: a state machine targeted by a schedule
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	resp := jsonCallForScheduler(t, srv, "AWSStepFunctions.CreateStateMachine", map[string]any{
		"name":       "sched-machine",
		"definition": `{"StartAt":"Done","States":{"Done":{"Type":"Succeed"}}}`,
		"roleArn":    "arn:aws:iam::000000000000:role/sfn",
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	createScheduleWithTarget(t, srv, "sfn-schedule", map[string]any{
		"Arn":     "arn:aws:states:us-east-1:000000000000:stateMachine:sched-machine",
		"RoleArn": "arn:aws:iam::000000000000:role/scheduler-role",
		"Input":   `{"orderId":"o-2"}`,
	})

	// When: the schedule becomes due
	fireOnce(t, srv)

	// Then: an execution is started with the schedule's input
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		kvs, err := srv.Store.Scan(context.Background(), "stepfunctions", "us-east-1/exec:")
		if err == nil && len(kvs) > 0 {
			if !strings.Contains(kvs[0].Value, "o-2") {
				t.Fatalf("execution input missing the schedule input: %s", kvs[0].Value)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no Step Functions execution started — the Step Functions target never fired")
}

// ─── Kinesis target ───────────────────────────────────────────────────────────

func TestSchedule_deliversToKinesisTarget(t *testing.T) {
	// Given: a Kinesis stream targeted by a schedule carrying a partition key
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	resp := jsonCallForScheduler(t, srv, "Kinesis_20131202.CreateStream", map[string]any{
		"StreamName": "sched-stream",
		"ShardCount": 1,
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	createScheduleWithTarget(t, srv, "kinesis-schedule", map[string]any{
		"Arn":               "arn:aws:kinesis:us-east-1:000000000000:stream/sched-stream",
		"RoleArn":           "arn:aws:iam::000000000000:role/scheduler-role",
		"Input":             `{"orderId":"o-3"}`,
		"KinesisParameters": map[string]any{"PartitionKey": "pk-3"},
	})

	// When: the schedule becomes due
	fireOnce(t, srv)

	// Then: the stream holds one record carrying the input, keyed as configured
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		records := readStreamRecords(t, srv, "sched-stream")
		if len(records) == 0 {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if !strings.Contains(string(records[0].Data), "o-3") {
			t.Fatalf("Kinesis record data missing the schedule input: %s", records[0].Data)
		}
		if records[0].PartitionKey != "pk-3" {
			t.Fatalf("PartitionKey = %q, want pk-3 (from KinesisParameters)", records[0].PartitionKey)
		}
		return
	}
	t.Fatal("no Kinesis record written — the Kinesis target never fired")
}

type scheduledKinesisRecord struct {
	Data         []byte `json:"Data"`
	PartitionKey string `json:"PartitionKey"`
}

func readStreamRecords(t *testing.T, srv *helpers.TestServer, stream string) []scheduledKinesisRecord {
	t.Helper()
	resp := jsonCallForScheduler(t, srv, "Kinesis_20131202.GetShardIterator", map[string]any{
		"StreamName":        stream,
		"ShardId":           "shardId-000000000000",
		"ShardIteratorType": "TRIM_HORIZON",
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var iter struct {
		ShardIterator string `json:"ShardIterator"`
	}
	helpers.DecodeJSON(t, resp, &iter)
	resp.Body.Close()

	resp = jsonCallForScheduler(t, srv, "Kinesis_20131202.GetRecords", map[string]any{
		"ShardIterator": iter.ShardIterator,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Records []scheduledKinesisRecord `json:"Records"`
	}
	helpers.DecodeJSON(t, resp, &out)
	return out.Records
}

// ─── EventBridge bus target ───────────────────────────────────────────────────

func TestSchedule_deliversToEventBusTarget(t *testing.T) {
	// Given: a rule on the default bus forwarding a schedule's own source to SQS
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	queueURL := createQueueForSchedule(t, srv, "sched-bus-queue")

	resp := jsonCallForScheduler(t, srv, "AWSEvents.PutRule", map[string]any{
		"Name":         "sched-bus-rule",
		"EventPattern": `{"source":["com.example.schedule"]}`,
		"State":        "ENABLED",
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	resp = jsonCallForScheduler(t, srv, "AWSEvents.PutTargets", map[string]any{
		"Rule": "sched-bus-rule",
		"Targets": []any{map[string]any{
			"Id":  "q",
			"Arn": "arn:aws:sqs:us-east-1:000000000000:sched-bus-queue",
		}},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	createScheduleWithTarget(t, srv, "bus-schedule", map[string]any{
		"Arn":     "arn:aws:events:us-east-1:000000000000:event-bus/default",
		"RoleArn": "arn:aws:iam::000000000000:role/scheduler-role",
		"Input":   `{"orderId":"o-4"}`,
		"EventBridgeParameters": map[string]any{
			"Source":     "com.example.schedule",
			"DetailType": "ScheduledEvent",
		},
	})

	// When: the schedule becomes due
	fireOnce(t, srv)

	// Then: the rule matched the republished event and forwarded it
	body := receiveOneScheduledMessage(t, srv, queueURL)
	if !strings.Contains(body, "o-4") {
		t.Fatalf("event-bus delivery did not carry the schedule input: %s", body)
	}
}

// ─── Undeliverable target kinds are refused, never dropped ────────────────────

func TestCreateSchedule_unsupportedTargetType(t *testing.T) {
	// Given: a running server
	srv := helpers.NewTestServer(t)

	// When: a schedule names a target kind the emulator cannot deliver to
	resp := schDo(t, srv, http.MethodPost, "/schedules/unsupported-target", map[string]any{
		"ScheduleExpression": "rate(1 minute)",
		"FlexibleTimeWindow": map[string]any{"Mode": "OFF"},
		"Target": map[string]any{
			"Arn":     "arn:aws:logs:us-east-1:000000000000:log-group:/aws/scheduler/demo",
			"RoleArn": "arn:aws:iam::000000000000:role/scheduler-role",
		},
	})
	defer resp.Body.Close()

	// Then: it fails loudly rather than being accepted and silently dropped
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ValidationException")

	// And: the schedule is not stored
	get := schDo(t, srv, http.MethodGet, "/schedules/unsupported-target", nil)
	defer get.Body.Close()
	helpers.AssertStatus(t, get, http.StatusNotFound)
}

func TestCreateSchedule_malformedTargetARN(t *testing.T) {
	// Given: a running server
	srv := helpers.NewTestServer(t)

	// When: a schedule names a target that is not ARN-shaped
	resp := schDo(t, srv, http.MethodPost, "/schedules/bad-arn", map[string]any{
		"ScheduleExpression": "rate(1 minute)",
		"FlexibleTimeWindow": map[string]any{"Mode": "OFF"},
		"Target": map[string]any{
			"Arn":     "not-an-arn",
			"RoleArn": "arn:aws:iam::000000000000:role/scheduler-role",
		},
	})
	defer resp.Body.Close()

	// Then: AWS's synchronous ARN-shape check rejects the call
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ValidationException")
}

func TestUpdateSchedule_unsupportedTargetType(t *testing.T) {
	// Given: a schedule with a deliverable target
	srv := helpers.NewTestServer(t)
	createScheduleWithTarget(t, srv, "retargeted", map[string]any{
		"Arn":     "arn:aws:sqs:us-east-1:000000000000:sched-q",
		"RoleArn": "arn:aws:iam::000000000000:role/scheduler-role",
	})

	// When: it is updated to a target kind the emulator cannot deliver to
	resp := schDo(t, srv, http.MethodPut, "/schedules/retargeted", map[string]any{
		"ScheduleExpression": "rate(1 minute)",
		"FlexibleTimeWindow": map[string]any{"Mode": "OFF"},
		"Target": map[string]any{
			"Arn":     "arn:aws:logs:us-east-1:000000000000:log-group:/aws/scheduler/demo",
			"RoleArn": "arn:aws:iam::000000000000:role/scheduler-role",
		},
	})
	defer resp.Body.Close()

	// Then: the update is refused and the stored target is unchanged
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ValidationException")

	get := schDo(t, srv, http.MethodGet, "/schedules/retargeted", nil)
	defer get.Body.Close()
	var stored struct {
		Target struct {
			Arn string `json:"Arn"`
		} `json:"Target"`
	}
	helpers.DecodeJSON(t, get, &stored)
	if !strings.HasPrefix(stored.Target.Arn, "arn:aws:sqs:") {
		t.Fatalf("stored target ARN = %q, want the original SQS target", stored.Target.Arn)
	}
}

// ─── Retry / dead-letter ──────────────────────────────────────────────────────

func TestSchedule_failedDeliveryGoesToDeadLetterQueue(t *testing.T) {
	// Given: a schedule whose target does not exist, with a DLQ configured
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	dlqURL := createQueueForSchedule(t, srv, "sched-dlq")

	createScheduleWithTarget(t, srv, "dlq-schedule", map[string]any{
		"Arn":              "arn:aws:lambda:us-east-1:000000000000:function:does-not-exist",
		"RoleArn":          "arn:aws:iam::000000000000:role/scheduler-role",
		"Input":            `{"orderId":"o-5"}`,
		"RetryPolicy":      map[string]any{"MaximumRetryAttempts": 2},
		"DeadLetterConfig": map[string]any{"Arn": "arn:aws:sqs:us-east-1:000000000000:sched-dlq"},
	})

	// When: the schedule becomes due
	fireOnce(t, srv)

	// Then: the undeliverable payload lands on the dead-letter queue
	body := receiveOneScheduledMessage(t, srv, dlqURL)
	if !strings.Contains(body, "o-5") {
		t.Fatalf("DLQ message did not carry the schedule input: %s", body)
	}
}
