// Source, enrichment and target coverage beyond DynamoDB Streams → SQS (#470).
//
// Before this, CreatePipe accepted any pair of ARNs and only the DynamoDB
// Streams → SQS path did anything; every other combination was stored and
// silently inert. These tests pin both halves of the fix: the combinations that
// now actually run, and the loud CreatePipe/UpdatePipe rejection of the ones
// that do not (docs/plans/full-emulation-priority.md §2.1).
package pipes_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/tests/helpers"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// createPipeBody sends CreatePipe with a caller-supplied request body, filling
// in the RoleArn every pipe needs.
func createPipeBody(t *testing.T, srv *helpers.TestServer, name string, body map[string]any) *http.Response {
	t.Helper()
	if _, ok := body["RoleArn"]; !ok {
		body["RoleArn"] = "arn:aws:iam::000000000000:role/pipe-role"
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal CreatePipe body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/pipes/"+name, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("build CreatePipe request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("CreatePipe %q: %v", name, err)
	}
	return resp
}

// mustCreateRunningPipe creates a pipe and advances it to RUNNING.
func mustCreateRunningPipe(t *testing.T, srv *helpers.TestServer, name string, body map[string]any) {
	t.Helper()
	resp := createPipeBody(t, srv, name, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("CreatePipe %q: status %d: %s", name, resp.StatusCode, raw)
	}
	advancePast(t, srv)
}

// assertValidationRejection asserts CreatePipe answered AWS's ValidationException
// and that the message names the offending field, so a user can act on it.
func assertValidationRejection(t *testing.T, resp *http.Response, mustMention string) {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 ValidationException", resp.StatusCode)
	}
	var out struct {
		Type    string `json:"__type"`
		Message string `json:"message"`
	}
	helpers.DecodeJSON(t, resp, &out)
	if out.Type != "ValidationException" {
		t.Errorf("__type = %q, want ValidationException", out.Type)
	}
	if !strings.Contains(out.Message, mustMention) {
		t.Errorf("message = %q, want it to mention %q", out.Message, mustMention)
	}
}

// pollForMessages drains a queue, retrying until something arrives. Kinesis and
// SQS sources are polled on the emulator's clock, so the mock clock has to be
// advanced between attempts.
func pollForMessages(t *testing.T, srv *helpers.TestServer, queueURL string) []string {
	t.Helper()
	for i := 0; i < 40; i++ {
		if bodies := receiveMessages(t, srv, queueURL); len(bodies) > 0 {
			return bodies
		}
		advancePast(t, srv)
	}
	return nil
}

// seedFunction registers a Lambda function record so a target invoke resolves.
// Invoking it for real needs a container; these tests assert the invocation was
// recorded, which is what the target path is responsible for.
func seedFunction(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	arn := fmt.Sprintf("arn:aws:lambda:%s:%s:function:%s", srv.Config.Region, srv.Config.AccountID, name)
	fn, _ := json.Marshal(map[string]any{
		"name": name, "arn": arn, "runtime": "nodejs20.x", "handler": "index.handler",
		"state": "Active", "timeout": 30, "memory_size": 128,
	})
	key := srv.Config.Region + "/" + name
	if err := srv.Store.Set(t.Context(), "lambda:functions", key, string(fn)); err != nil {
		t.Fatalf("seed function %q: %v", name, err)
	}
	return arn
}

// mustCreateKinesisStream creates a Kinesis stream and returns its ARN.
func mustCreateKinesisStream(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	resp := jsonCall(t, srv, "Kinesis_20131202.CreateStream", map[string]any{
		"StreamName": name, "ShardCount": 1,
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	return fmt.Sprintf("arn:aws:kinesis:%s:%s:stream/%s", srv.Config.Region, srv.Config.AccountID, name)
}

// jsonCall performs an AWS JSON 1.1 X-Amz-Target request against the emulator.
func jsonCall(t *testing.T, srv *helpers.TestServer, target string, body map[string]any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s body: %v", target, err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(raw))
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

// ─── loud rejection of unsupported wiring ─────────────────────────────────────

func TestCreatePipe_rejectsUnsupportedTargetService(t *testing.T) {
	// Given: a supported source and a well-formed target ARN naming a service
	// the emulator has no sink for.
	srv := helpers.NewTestServer(t)
	streamARN := mustCreateStreamTable(t, srv, "orders")

	// When: the pipe is created
	resp := createPipeBody(t, srv, "to-logs", map[string]any{
		"Source": streamARN,
		"Target": "arn:aws:logs:us-east-1:000000000000:log-group:/aws/pipes/x",
	})

	// Then: it is refused up front rather than stored and never fired
	assertValidationRejection(t, resp, "Target")
}

func TestCreatePipe_rejectsUnsupportedSourceService(t *testing.T) {
	srv := helpers.NewTestServer(t)
	_, queueARN := mustCreateQueue(t, srv, "q")

	resp := createPipeBody(t, srv, "from-mq", map[string]any{
		"Source": "arn:aws:mq:us-east-1:000000000000:broker:b:b-1",
		"Target": queueARN,
	})

	assertValidationRejection(t, resp, "Source")
}

func TestCreatePipe_rejectsMalformedArns(t *testing.T) {
	srv := helpers.NewTestServer(t)
	_, queueARN := mustCreateQueue(t, srv, "q")
	streamARN := mustCreateStreamTable(t, srv, "orders")

	assertValidationRejection(t, createPipeBody(t, srv, "bad-source", map[string]any{
		"Source": "not-an-arn", "Target": queueARN,
	}), "Source")
	assertValidationRejection(t, createPipeBody(t, srv, "bad-target", map[string]any{
		"Source": streamARN, "Target": "not-an-arn",
	}), "Target")
}

func TestCreatePipe_rejectsUnsupportedEnrichment(t *testing.T) {
	// Given: an enrichment ARN naming a resource the emulator cannot invoke
	// synchronously. Accepting it would drop every batch on the floor.
	srv := helpers.NewTestServer(t)
	streamARN := mustCreateStreamTable(t, srv, "orders")
	_, queueARN := mustCreateQueue(t, srv, "q")

	resp := createPipeBody(t, srv, "enriched", map[string]any{
		"Source":     streamARN,
		"Target":     queueARN,
		"Enrichment": "arn:aws:execute-api:us-east-1:000000000000:abc123/prod/GET/items",
	})

	assertValidationRejection(t, resp, "Enrichment")
}

func TestCreatePipe_rejectsFilterCriteria(t *testing.T) {
	// Given: a filter the emulator does not evaluate. Storing it would drop
	// nothing and look like every event matched.
	srv := helpers.NewTestServer(t)
	streamARN := mustCreateStreamTable(t, srv, "orders")
	_, queueARN := mustCreateQueue(t, srv, "q")

	resp := createPipeBody(t, srv, "filtered", map[string]any{
		"Source": streamARN,
		"Target": queueARN,
		"SourceParameters": map[string]any{
			"FilterCriteria": map[string]any{
				"Filters": []any{map[string]any{"Pattern": `{"eventName":["INSERT"]}`}},
			},
		},
	})

	assertValidationRejection(t, resp, "FilterCriteria")
}

func TestCreatePipe_requiresRoleArn(t *testing.T) {
	srv := helpers.NewTestServer(t)
	streamARN := mustCreateStreamTable(t, srv, "orders")
	_, queueARN := mustCreateQueue(t, srv, "q")

	resp := createPipeBody(t, srv, "no-role", map[string]any{
		"Source": streamARN, "Target": queueARN, "RoleArn": "",
	})

	assertValidationRejection(t, resp, "RoleArn")
}

func TestUpdatePipe_rejectsUnsupportedEnrichment(t *testing.T) {
	// Given: a running, supported pipe
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	streamARN := mustCreateStreamTable(t, srv, "orders")
	_, queueARN := mustCreateQueue(t, srv, "q")
	mustCreateRunningPipe(t, srv, "p", map[string]any{"Source": streamARN, "Target": queueARN})

	// When: an unsupported enrichment is added by UpdatePipe
	resp := updatePipe(t, srv, "p", map[string]any{
		"Enrichment": "arn:aws:logs:us-east-1:000000000000:log-group:/x",
	})

	// Then: the update is refused, not stored
	assertValidationRejection(t, resp, "Enrichment")
}

// UpdatePipe is routed on PUT by AWS — every SDK and the CLI send it that way,
// and a PATCH-only route answered them 405. Pinned here because nothing in the
// Go tests would notice: they build the request themselves.
func TestUpdatePipe_isRoutedOnPut(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	streamARN := mustCreateStreamTable(t, srv, "orders")
	_, queueARN := mustCreateQueue(t, srv, "q")
	mustCreateRunningPipe(t, srv, "p", map[string]any{"Source": streamARN, "Target": queueARN})

	// PUT is the AWS method.
	resp := updatePipe(t, srv, "p", map[string]any{"DesiredState": "STOPPED"})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And RoleArn is required, as it is on CreatePipe.
	bad := updatePipe(t, srv, "p", map[string]any{"DesiredState": "RUNNING", "RoleArn": ""})
	defer bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Errorf("UpdatePipe without RoleArn: status %d, want 400", bad.StatusCode)
	}
}

// ─── how a batch maps onto a target ───────────────────────────────────────────

func TestPipeDelivery_batchMapsToTheTargetsBatchAPI(t *testing.T) {
	// Given: a pipe from a queue to a queue, batching three source messages
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	sourceURL, sourceARN := mustCreateQueue(t, srv, "batch-source")
	targetURL, targetARN := mustCreateQueue(t, srv, "batch-target")
	mustCreateRunningPipe(t, srv, "batched", map[string]any{"Source": sourceARN, "Target": targetARN})

	for _, body := range []string{"one", "two", "three"} {
		resp := jsonCall(t, srv, "AmazonSQS.SendMessage", map[string]any{
			"QueueUrl": sourceURL, "MessageBody": body,
		})
		resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusOK)
	}

	// When: the poller reads them
	// Then: SQS has a batch API, so the target gets one message per record —
	// each an object, not the array (AWS maps the batch onto the target's
	// batch API rather than sending the array as one payload).
	seen := map[string]bool{}
	for i := 0; i < 40 && len(seen) < 3; i++ {
		for _, body := range receiveMessages(t, srv, targetURL) {
			var record map[string]any
			if err := json.Unmarshal([]byte(body), &record); err != nil {
				t.Fatalf("target message is not a JSON object: %v\nbody: %s", err, body)
			}
			if record["eventSource"] != "aws:sqs" {
				t.Errorf("eventSource = %v, want aws:sqs", record["eventSource"])
			}
			seen[fmt.Sprint(record["body"])] = true
		}
		advancePast(t, srv)
	}
	for _, want := range []string{"one", "two", "three"} {
		if !seen[want] {
			t.Errorf("target never received the record for %q (saw %v)", want, seen)
		}
	}
}

func TestPipeDelivery_wholeBatchTargetsReceiveTheArray(t *testing.T) {
	// Given: a Step Functions target, which has no batch API and so receives
	// the whole JSON array in one request — "even if the batch size is 1"
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	streamARN := mustCreateStreamTable(t, srv, "orders")
	resp := jsonCall(t, srv, "AWSStepFunctions.CreateStateMachine", map[string]any{
		"name":       "pipe-machine",
		"definition": `{"StartAt":"Done","States":{"Done":{"Type":"Succeed"}}}`,
		"roleArn":    "arn:aws:iam::000000000000:role/sfn",
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	smARN := fmt.Sprintf("arn:aws:states:%s:%s:stateMachine:pipe-machine", srv.Config.Region, srv.Config.AccountID)
	mustCreateRunningPipe(t, srv, "to-sfn", map[string]any{"Source": streamARN, "Target": smARN})

	// When: an item is written
	putResp := ddbCall(t, srv, "PutItem", map[string]any{
		"TableName": "orders",
		"Item":      map[string]any{"id": map[string]any{"S": "order-1"}},
	})
	putResp.Body.Close()

	// Then: an execution starts whose input is the record array
	for i := 0; i < 40; i++ {
		kvs, err := srv.Store.Scan(t.Context(), "stepfunctions", srv.Config.Region+"/exec:")
		if err == nil && len(kvs) > 0 {
			var exec struct {
				Input string `json:"Input"`
			}
			if err := json.Unmarshal([]byte(kvs[0].Value), &exec); err != nil {
				t.Fatalf("decode execution: %v", err)
			}
			var batch []map[string]any
			if err := json.Unmarshal([]byte(exec.Input), &batch); err != nil {
				t.Fatalf("execution input is not a JSON array: %v\ninput: %s", err, exec.Input)
			}
			if len(batch) != 1 || batch[0]["eventSource"] != "aws:dynamodb" {
				t.Fatalf("execution input = %s, want a one-record DynamoDB batch", exec.Input)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no Step Functions execution started — the Step Functions target never fired")
}

// ─── SQS source ───────────────────────────────────────────────────────────────

func TestPipeDelivery_sqsSourceToSQSTarget(t *testing.T) {
	// Given: a pipe from one queue to another
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	sourceURL, sourceARN := mustCreateQueue(t, srv, "pipe-source")
	targetURL, targetARN := mustCreateQueue(t, srv, "pipe-target")
	mustCreateRunningPipe(t, srv, "sqs-to-sqs", map[string]any{"Source": sourceARN, "Target": targetARN})

	// When: a message is sent to the source queue
	resp := jsonCall(t, srv, "AmazonSQS.SendMessage", map[string]any{
		"QueueUrl": sourceURL, "MessageBody": `{"orderId":"o-1"}`,
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the poller forwards it to the target as an SQS-shaped record
	bodies := pollForMessages(t, srv, targetURL)
	if len(bodies) != 1 {
		t.Fatalf("expected 1 message on the target queue, got %d", len(bodies))
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &record); err != nil {
		t.Fatalf("target body is not a JSON object: %v\nbody: %s", err, bodies[0])
	}
	if record["eventSource"] != "aws:sqs" {
		t.Errorf("eventSource = %v, want aws:sqs", record["eventSource"])
	}
	if record["body"] != `{"orderId":"o-1"}` {
		t.Errorf("body = %v, want the original message body", record["body"])
	}
	if record["eventSourceARN"] != sourceARN {
		t.Errorf("eventSourceARN = %v, want %q", record["eventSourceARN"], sourceARN)
	}

	// And: the source message is consumed, not left to be redelivered forever
	if left := receiveMessages(t, srv, sourceURL); len(left) != 0 {
		t.Errorf("source queue still holds %d messages after a successful delivery", len(left))
	}
}

// ─── Kinesis source ───────────────────────────────────────────────────────────

func TestPipeDelivery_kinesisSourceToSQSTarget(t *testing.T) {
	// Given: a pipe from a Kinesis stream to an SQS queue
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	streamARN := mustCreateKinesisStream(t, srv, "events")
	targetURL, targetARN := mustCreateQueue(t, srv, "kinesis-target")
	mustCreateRunningPipe(t, srv, "kinesis-to-sqs", map[string]any{"Source": streamARN, "Target": targetARN})

	// When: a record is put on the stream
	resp := jsonCall(t, srv, "Kinesis_20131202.PutRecord", map[string]any{
		"StreamName":   "events",
		"PartitionKey": "pk-1",
		"Data":         []byte(`{"orderId":"o-1"}`),
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the poller forwards it as a Kinesis-shaped record
	bodies := pollForMessages(t, srv, targetURL)
	if len(bodies) != 1 {
		t.Fatalf("expected 1 message on the target queue, got %d", len(bodies))
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &rec); err != nil {
		t.Fatalf("target body is not a JSON object: %v\nbody: %s", err, bodies[0])
	}
	if rec["eventSource"] != "aws:kinesis" {
		t.Errorf("eventSource = %v, want aws:kinesis", rec["eventSource"])
	}
	if rec["partitionKey"] != "pk-1" {
		t.Errorf("partitionKey = %v, want pk-1", rec["partitionKey"])
	}
	// Kinesis record data stays base64-encoded on the wire, as AWS sends it.
	if rec["data"] != "eyJvcmRlcklkIjoiby0xIn0=" {
		t.Errorf("data = %v, want the base64 of the record", rec["data"])
	}
	if rec["kinesisSchemaVersion"] != "1.0" {
		t.Errorf("kinesisSchemaVersion = %v, want 1.0", rec["kinesisSchemaVersion"])
	}

	// And: the poller does not redeliver the same record on the next tick
	advancePast(t, srv)
	advancePast(t, srv)
	if again := receiveMessages(t, srv, targetURL); len(again) != 0 {
		t.Errorf("record redelivered %d times — the shard cursor did not advance", len(again))
	}
}

// ─── Lambda target ────────────────────────────────────────────────────────────

func TestPipeDelivery_lambdaTarget(t *testing.T) {
	// Given: a pipe whose target is a Lambda function
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	streamARN := mustCreateStreamTable(t, srv, "orders")
	fnARN := seedFunction(t, srv, "pipe-target-fn")
	mustCreateRunningPipe(t, srv, "to-lambda", map[string]any{"Source": streamARN, "Target": fnARN})

	// When: an item is written
	putResp := ddbCall(t, srv, "PutItem", map[string]any{
		"TableName": "orders",
		"Item":      map[string]any{"id": map[string]any{"S": "order-1"}},
	})
	putResp.Body.Close()

	// Then: the function is invoked
	invoked := false
	for i := 0; i < 40 && !invoked; i++ {
		kvs, err := srv.Store.Scan(t.Context(), "lambda:invocations", srv.Config.Region+"/pipe-target-fn:")
		if err == nil && len(kvs) > 0 {
			invoked = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !invoked {
		t.Fatal("no Lambda invocation recorded — the Lambda target never fired")
	}
}

// ─── EventBridge bus target ───────────────────────────────────────────────────

func TestPipeDelivery_eventBridgeBusTarget(t *testing.T) {
	// Given: a rule on the default bus forwarding to a queue, and a pipe whose
	// target is that bus
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	streamARN := mustCreateStreamTable(t, srv, "orders")
	queueURL, queueARN := mustCreateQueue(t, srv, "bus-sink")

	resp := jsonCall(t, srv, "AWSEvents.PutRule", map[string]any{
		"Name":         "pipe-events",
		"EventPattern": `{"source":["com.example.pipe"]}`,
		"State":        "ENABLED",
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp = jsonCall(t, srv, "AWSEvents.PutTargets", map[string]any{
		"Rule":    "pipe-events",
		"Targets": []any{map[string]any{"Id": "q", "Arn": queueARN}},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	busARN := fmt.Sprintf("arn:aws:events:%s:%s:event-bus/default", srv.Config.Region, srv.Config.AccountID)
	mustCreateRunningPipe(t, srv, "to-bus", map[string]any{
		"Source": streamARN,
		"Target": busARN,
		"TargetParameters": map[string]any{
			"EventBridgeEventBusParameters": map[string]any{
				"Source":     "com.example.pipe",
				"DetailType": "OrderChanged",
			},
		},
	})

	// When: an item is written
	putResp := ddbCall(t, srv, "PutItem", map[string]any{
		"TableName": "orders",
		"Item":      map[string]any{"id": map[string]any{"S": "order-1"}},
	})
	putResp.Body.Close()
	time.Sleep(50 * time.Millisecond)

	// Then: the batch reached the bus and the rule fanned it out
	bodies := receiveMessages(t, srv, queueURL)
	if len(bodies) != 1 {
		t.Fatalf("expected 1 message via the bus, got %d", len(bodies))
	}
	if !strings.Contains(bodies[0], `"detail-type":"OrderChanged"`) {
		t.Errorf("event = %s, want the pipe's DetailType", bodies[0])
	}
	if !strings.Contains(bodies[0], "aws:dynamodb") {
		t.Errorf("event = %s, want the pipe's record batch in the detail", bodies[0])
	}
}

// ─── input transformation ─────────────────────────────────────────────────────

func TestPipeDelivery_targetInputTemplate(t *testing.T) {
	// Given: a pipe whose target parameters reshape each record
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	sourceURL, sourceARN := mustCreateQueue(t, srv, "tmpl-source")
	targetURL, targetARN := mustCreateQueue(t, srv, "tmpl-target")
	mustCreateRunningPipe(t, srv, "templated", map[string]any{
		"Source": sourceARN,
		"Target": targetARN,
		"TargetParameters": map[string]any{
			"InputTemplate": `{"id":<$.messageId>,"pipe":<aws.pipes.pipe-name>}`,
		},
	})

	// When: a message flows through
	resp := jsonCall(t, srv, "AmazonSQS.SendMessage", map[string]any{
		"QueueUrl": sourceURL, "MessageBody": "hello",
	})
	resp.Body.Close()

	// Then: the target receives the rendered records, not the raw ones
	bodies := pollForMessages(t, srv, targetURL)
	if len(bodies) != 1 {
		t.Fatalf("expected 1 transformed message, got %d", len(bodies))
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &record); err != nil {
		t.Fatalf("body is not a JSON object: %v\nbody: %s", err, bodies[0])
	}
	if record["pipe"] != "templated" {
		t.Errorf("pipe = %v, want the reserved aws.pipes.pipe-name value", record["pipe"])
	}
	if id, _ := record["id"].(string); id == "" {
		t.Errorf("id = %v, want the source messageId", record["id"])
	}
	if _, unexpected := record["body"]; unexpected {
		t.Errorf("record still carries the untransformed fields: %v", record)
	}
}

// ─── console wiring feed ──────────────────────────────────────────────────────

func TestPipesConsole_reportsResolvedWiring(t *testing.T) {
	// Given: a pipe with a source, an enrichment and a target
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	streamARN := mustCreateStreamTable(t, srv, "orders")
	fnARN := seedFunction(t, srv, "enricher")
	_, queueARN := mustCreateQueue(t, srv, "q")
	mustCreateRunningPipe(t, srv, "wired", map[string]any{
		"Source": streamARN, "Enrichment": fnARN, "Target": queueARN,
	})

	// When: the console asks what the pipe is wired to
	resp, err := http.Get(srv.URL + "/_overcast/pipes/wiring")
	if err != nil {
		t.Fatalf("GET wiring: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var out struct {
		Pipes []struct {
			Name           string `json:"Name"`
			SourceType     string `json:"SourceType"`
			EnrichmentType string `json:"EnrichmentType"`
			TargetType     string `json:"TargetType"`
			Wired          bool   `json:"Wired"`
		} `json:"Pipes"`
	}
	helpers.DecodeJSON(t, resp, &out)

	// Then: each leg is reported with its resolved type, and the pipe is wired
	if len(out.Pipes) != 1 {
		t.Fatalf("wiring returned %d pipes, want 1", len(out.Pipes))
	}
	got := out.Pipes[0]
	if got.SourceType != "DynamoDB Streams" {
		t.Errorf("SourceType = %q, want DynamoDB Streams", got.SourceType)
	}
	if got.EnrichmentType != "Lambda" {
		t.Errorf("EnrichmentType = %q, want Lambda", got.EnrichmentType)
	}
	if got.TargetType != "SQS" {
		t.Errorf("TargetType = %q, want SQS", got.TargetType)
	}
	if !got.Wired {
		t.Error("Wired = false, want true for a fully supported combination")
	}
}

func TestPipesConsole_reportsAStoredButInertPipe(t *testing.T) {
	// Given: a pipe persisted before the wiring check existed, naming a target
	// this emulator cannot deliver to. CreatePipe refuses that combination now,
	// so the only way it can exist is in state written by an older build.
	srv := helpers.NewTestServer(t)
	stored, _ := json.Marshal(map[string]any{
		"Name":         "legacy",
		"Arn":          "arn:aws:pipes:us-east-1:000000000000:pipe/legacy",
		"Source":       "arn:aws:dynamodb:us-east-1:000000000000:table/t/stream/2024",
		"Target":       "arn:aws:logs:us-east-1:000000000000:log-group:/x",
		"CurrentState": "RUNNING",
		"DesiredState": "RUNNING",
	})
	if err := srv.Store.Set(t.Context(), "pipes:pipes", "us-east-1/legacy", string(stored)); err != nil {
		t.Fatalf("seed pipe: %v", err)
	}

	// When: the console reads the wiring
	resp, err := http.Get(srv.URL + "/_overcast/pipes/wiring")
	if err != nil {
		t.Fatalf("GET wiring: %v", err)
	}
	defer resp.Body.Close()

	var out struct {
		Pipes []struct {
			Name       string `json:"Name"`
			TargetType string `json:"TargetType"`
			Wired      bool   `json:"Wired"`
			Reason     string `json:"Reason"`
		} `json:"Pipes"`
	}
	helpers.DecodeJSON(t, resp, &out)

	// Then: it is reported as stored-but-inert with a reason, not as running
	if len(out.Pipes) != 1 {
		t.Fatalf("wiring returned %d pipes, want 1", len(out.Pipes))
	}
	if out.Pipes[0].Wired {
		t.Error("Wired = true for a target the emulator cannot deliver to")
	}
	if out.Pipes[0].Reason == "" {
		t.Error("Reason is empty — an inert pipe must say why")
	}
}
