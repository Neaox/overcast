// Package pipes_test contains integration tests for the EventBridge Pipes emulator.
//
// Run: go test ./tests/integration/pipes/...
package pipes_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/tests/helpers"
)

// ---- Helpers ----------------------------------------------------------------

// pipesPost sends a POST /v1/pipes/{name} request.
func createPipe(t *testing.T, srv *helpers.TestServer, name, sourceARN, targetARN string) *http.Response {
	t.Helper()
	b, _ := json.Marshal(map[string]any{
		"Source": sourceARN,
		"Target": targetARN,
	})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/pipes/"+name, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("createPipe %q: %v", name, err)
	}
	return resp
}

func describePipe(t *testing.T, srv *helpers.TestServer, name string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/pipes/"+name, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("describePipe %q: %v", name, err)
	}
	return resp
}

func deletePipe(t *testing.T, srv *helpers.TestServer, name string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/v1/pipes/"+name, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("deletePipe %q: %v", name, err)
	}
	return resp
}

func listPipes(t *testing.T, srv *helpers.TestServer) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/pipes", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("listPipes: %v", err)
	}
	return resp
}

// ddbCall sends a DynamoDB API call.
func ddbCall(t *testing.T, srv *helpers.TestServer, op string, body map[string]any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "DynamoDB_20120810."+op)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ddbCall %s: %v", op, err)
	}
	return resp
}

// sqsCall sends an SQS API call.
func sqsCall(t *testing.T, srv *helpers.TestServer, action string, body map[string]any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS."+action)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sqsCall %s: %v", action, err)
	}
	return resp
}

// mustCreateStreamTable creates a DDB table with streams and returns the stream ARN.
func mustCreateStreamTable(t *testing.T, srv *helpers.TestServer, tableName string) string {
	t.Helper()
	resp := ddbCall(t, srv, "CreateTable", map[string]any{
		"TableName":            tableName,
		"AttributeDefinitions": []map[string]any{{"AttributeName": "id", "AttributeType": "S"}},
		"KeySchema":            []map[string]any{{"AttributeName": "id", "KeyType": "HASH"}},
		"BillingMode":          "PAY_PER_REQUEST",
		"StreamSpecification": map[string]any{
			"StreamEnabled":  true,
			"StreamViewType": "NEW_AND_OLD_IMAGES",
		},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CreateTable %q: status %d", tableName, resp.StatusCode)
	}
	var result struct {
		TableDescription struct {
			LatestStreamArn string `json:"LatestStreamArn"`
		} `json:"TableDescription"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.TableDescription.LatestStreamArn == "" {
		t.Fatalf("CreateTable %q: no LatestStreamArn", tableName)
	}
	return result.TableDescription.LatestStreamArn
}

// mustCreateQueue creates an SQS queue and returns its ARN (built from the URL).
func mustCreateQueue(t *testing.T, srv *helpers.TestServer, queueName string) (queueURL, queueARN string) {
	t.Helper()
	resp := sqsCall(t, srv, "CreateQueue", map[string]any{"QueueName": queueName})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CreateQueue %q: status %d", queueName, resp.StatusCode)
	}
	var result struct {
		QueueUrl string `json:"QueueUrl"`
	}
	helpers.DecodeJSON(t, resp, &result)
	// Build the SQS ARN from config defaults: region=us-east-1, account=000000000000
	queueURL = result.QueueUrl
	queueARN = fmt.Sprintf("arn:aws:sqs:%s:%s:%s", srv.Config.Region, srv.Config.AccountID, queueName)
	return
}

// receiveMessages polls the SQS queue and returns all available message bodies.
func receiveMessages(t *testing.T, srv *helpers.TestServer, queueURL string) []string {
	t.Helper()
	resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":            queueURL,
		"MaxNumberOfMessages": 10,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ReceiveMessage: status %d", resp.StatusCode)
	}
	var result struct {
		Messages []struct {
			Body string `json:"Body"`
		} `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp, &result)
	bodies := make([]string, len(result.Messages))
	for i, m := range result.Messages {
		bodies[i] = m.Body
	}
	return bodies
}

// advancePast waits for the AsyncStateTransitionDelay (50ms) using a mock clock
// when available, otherwise sleeps briefly.
func advancePast(t *testing.T, srv *helpers.TestServer) {
	t.Helper()
	if srv.Clock != nil {
		srv.Clock.Add(100 * time.Millisecond)
		// Yield so the goroutine triggered by the timer can run.
		time.Sleep(10 * time.Millisecond)
	} else {
		time.Sleep(100 * time.Millisecond)
	}
}

// ---- CreatePipe -------------------------------------------------------------

func TestCreatePipe_success(t *testing.T) {
	// Given: a DDB stream table and an SQS queue
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	streamARN := mustCreateStreamTable(t, srv, "orders")
	_, queueARN := mustCreateQueue(t, srv, "order-events")

	// When: we create a pipe connecting them
	resp := createPipe(t, srv, "orders-to-sqs", streamARN, queueARN)
	defer resp.Body.Close()

	// Then: 201 Created is returned with the pipe in CREATING state
	helpers.AssertStatus(t, resp, http.StatusCreated)

	var p struct {
		Name         string `json:"Name"`
		Arn          string `json:"Arn"`
		SourceArn    string `json:"Source"`
		TargetArn    string `json:"Target"`
		SourceName   string `json:"SourceName"`
		TargetName   string `json:"TargetName"`
		CurrentState string `json:"CurrentState"`
		DesiredState string `json:"DesiredState"`
	}
	helpers.DecodeJSON(t, resp, &p)

	if p.Name != "orders-to-sqs" {
		t.Errorf("Name: got %q, want %q", p.Name, "orders-to-sqs")
	}
	if !strings.Contains(p.Arn, "pipe/orders-to-sqs") {
		t.Errorf("Arn: got %q, want it to contain pipe/orders-to-sqs", p.Arn)
	}
	if p.SourceArn != streamARN {
		t.Errorf("SourceArn: got %q, want %q", p.SourceArn, streamARN)
	}
	if p.TargetArn != queueARN {
		t.Errorf("TargetArn: got %q, want %q", p.TargetArn, queueARN)
	}
	if p.SourceName != "orders" {
		t.Errorf("SourceName: got %q, want %q", p.SourceName, "orders")
	}
	if p.TargetName != "order-events" {
		t.Errorf("TargetName: got %q, want %q", p.TargetName, "order-events")
	}
	if p.CurrentState != "CREATING" {
		t.Errorf("CurrentState: got %q, want CREATING immediately after create", p.CurrentState)
	}
	if p.DesiredState != "RUNNING" {
		t.Errorf("DesiredState: got %q, want RUNNING", p.DesiredState)
	}
}

func TestCreatePipe_transitionsToRunning(t *testing.T) {
	// Given: a pipe just created (starts as CREATING)
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	streamARN := mustCreateStreamTable(t, srv, "orders")
	_, queueARN := mustCreateQueue(t, srv, "order-events")

	resp := createPipe(t, srv, "orders-to-sqs", streamARN, queueARN)
	resp.Body.Close()

	// When: the transition delay elapses
	advancePast(t, srv)

	// Then: DescribePipe returns RUNNING
	resp2 := describePipe(t, srv, "orders-to-sqs")
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)

	var p struct {
		CurrentState string `json:"CurrentState"`
	}
	helpers.DecodeJSON(t, resp2, &p)

	if p.CurrentState != "RUNNING" {
		t.Errorf("CurrentState: got %q, want RUNNING after transition", p.CurrentState)
	}
}

func TestCreatePipe_duplicate(t *testing.T) {
	// Given: a pipe already exists
	srv := helpers.NewTestServer(t)
	streamARN := mustCreateStreamTable(t, srv, "orders")
	_, queueARN := mustCreateQueue(t, srv, "order-events")

	resp := createPipe(t, srv, "my-pipe", streamARN, queueARN)
	resp.Body.Close()

	// When: we try to create a pipe with the same name
	resp2 := createPipe(t, srv, "my-pipe", streamARN, queueARN)
	defer resp2.Body.Close()

	// Then: 409 Conflict
	helpers.AssertStatus(t, resp2, http.StatusConflict)
}

func TestCreatePipe_missingSourceArn(t *testing.T) {
	srv := helpers.NewTestServer(t)
	_, queueARN := mustCreateQueue(t, srv, "q")

	resp := createPipe(t, srv, "bad-pipe", "", queueARN)
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestCreatePipe_missingTargetArn(t *testing.T) {
	srv := helpers.NewTestServer(t)
	streamARN := mustCreateStreamTable(t, srv, "tbl")

	resp := createPipe(t, srv, "bad-pipe", streamARN, "")
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

// ---- DescribePipe -----------------------------------------------------------

func TestDescribePipe_notFound(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := describePipe(t, srv, "no-such-pipe")
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

// ---- DeletePipe -------------------------------------------------------------

func TestDeletePipe_success(t *testing.T) {
	// Given: a RUNNING pipe
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	streamARN := mustCreateStreamTable(t, srv, "orders")
	_, queueARN := mustCreateQueue(t, srv, "order-events")

	resp := createPipe(t, srv, "orders-to-sqs", streamARN, queueARN)
	resp.Body.Close()
	advancePast(t, srv) // → RUNNING

	// When: we delete it
	resp2 := deletePipe(t, srv, "orders-to-sqs")
	defer resp2.Body.Close()

	// Then: 200 with DELETING state
	helpers.AssertStatus(t, resp2, http.StatusOK)
	var p struct {
		CurrentState string `json:"CurrentState"`
	}
	helpers.DecodeJSON(t, resp2, &p)
	if p.CurrentState != "DELETING" {
		t.Errorf("CurrentState: got %q, want DELETING", p.CurrentState)
	}
}

func TestDeletePipe_removedAfterDelay(t *testing.T) {
	// Given: a RUNNING pipe
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	streamARN := mustCreateStreamTable(t, srv, "orders")
	_, queueARN := mustCreateQueue(t, srv, "order-events")

	resp := createPipe(t, srv, "orders-to-sqs", streamARN, queueARN)
	resp.Body.Close()
	advancePast(t, srv) // → RUNNING

	resp2 := deletePipe(t, srv, "orders-to-sqs")
	resp2.Body.Close()

	// When: the deletion delay elapses
	advancePast(t, srv)

	// Then: DescribePipe returns 404
	resp3 := describePipe(t, srv, "orders-to-sqs")
	defer resp3.Body.Close()
	helpers.AssertStatus(t, resp3, http.StatusNotFound)
}

func TestDeletePipe_notFound(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := deletePipe(t, srv, "no-such-pipe")
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

// ---- ListPipes --------------------------------------------------------------

func TestListPipes_empty(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := listPipes(t, srv)
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Pipes []any `json:"Pipes"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Pipes) != 0 {
		t.Errorf("expected 0 pipes, got %d", len(result.Pipes))
	}
}

func TestListPipes_multiple(t *testing.T) {
	// Given: two pipes
	srv := helpers.NewTestServer(t)
	streamARN := mustCreateStreamTable(t, srv, "orders")
	_, queueARN1 := mustCreateQueue(t, srv, "q1")
	_, queueARN2 := mustCreateQueue(t, srv, "q2")

	resp1 := createPipe(t, srv, "pipe-a", streamARN, queueARN1)
	resp1.Body.Close()
	resp2 := createPipe(t, srv, "pipe-b", streamARN, queueARN2)
	resp2.Body.Close()

	// When: we list pipes
	resp := listPipes(t, srv)
	defer resp.Body.Close()

	// Then: both pipes appear
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Pipes []struct {
			Name string `json:"Name"`
		} `json:"Pipes"`
	}
	helpers.DecodeJSON(t, resp, &result)

	if len(result.Pipes) != 2 {
		t.Errorf("expected 2 pipes, got %d", len(result.Pipes))
	}
}

// ---- UpdatePipe -------------------------------------------------------------

func updatePipe(t *testing.T, srv *helpers.TestServer, name string, body map[string]any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/v1/pipes/"+name, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("updatePipe %q: %v", name, err)
	}
	return resp
}

func TestUpdatePipe_stopRunningPipe(t *testing.T) {
	// Given: a RUNNING pipe
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	streamARN := mustCreateStreamTable(t, srv, "tbl")
	_, queueARN := mustCreateQueue(t, srv, "q")

	resp := createPipe(t, srv, "my-pipe", streamARN, queueARN)
	resp.Body.Close()
	advancePast(t, srv) // → RUNNING

	// When: we stop it
	resp2 := updatePipe(t, srv, "my-pipe", map[string]any{"DesiredState": "STOPPED"})
	defer resp2.Body.Close()

	// Then: 200, pipe is STOPPING
	helpers.AssertStatus(t, resp2, http.StatusOK)
	var p struct {
		CurrentState string `json:"CurrentState"`
		DesiredState string `json:"DesiredState"`
	}
	helpers.DecodeJSON(t, resp2, &p)
	if p.CurrentState != "STOPPING" {
		t.Errorf("CurrentState: got %q, want STOPPING", p.CurrentState)
	}
	if p.DesiredState != "STOPPED" {
		t.Errorf("DesiredState: got %q, want STOPPED", p.DesiredState)
	}

	// After transition: pipe should be STOPPED
	advancePast(t, srv)
	resp3 := describePipe(t, srv, "my-pipe")
	defer resp3.Body.Close()
	helpers.AssertStatus(t, resp3, http.StatusOK)
	var p2 struct {
		CurrentState string `json:"CurrentState"`
	}
	helpers.DecodeJSON(t, resp3, &p2)
	if p2.CurrentState != "STOPPED" {
		t.Errorf("CurrentState after transition: got %q, want STOPPED", p2.CurrentState)
	}
}

func TestUpdatePipe_startStoppedPipe(t *testing.T) {
	// Given: a STOPPED pipe
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	streamARN := mustCreateStreamTable(t, srv, "tbl")
	_, queueARN := mustCreateQueue(t, srv, "q")

	resp := createPipe(t, srv, "my-pipe", streamARN, queueARN)
	resp.Body.Close()
	advancePast(t, srv) // → RUNNING

	// Stop it
	resp2 := updatePipe(t, srv, "my-pipe", map[string]any{"DesiredState": "STOPPED"})
	resp2.Body.Close()
	advancePast(t, srv) // → STOPPED

	// When: we start it again
	resp3 := updatePipe(t, srv, "my-pipe", map[string]any{"DesiredState": "RUNNING"})
	defer resp3.Body.Close()
	helpers.AssertStatus(t, resp3, http.StatusOK)
	var p struct {
		CurrentState string `json:"CurrentState"`
	}
	helpers.DecodeJSON(t, resp3, &p)
	if p.CurrentState != "STARTING" {
		t.Errorf("CurrentState: got %q, want STARTING", p.CurrentState)
	}

	// After transition: pipe is RUNNING
	advancePast(t, srv)
	resp4 := describePipe(t, srv, "my-pipe")
	defer resp4.Body.Close()
	var p2 struct {
		CurrentState string `json:"CurrentState"`
	}
	helpers.DecodeJSON(t, resp4, &p2)
	if p2.CurrentState != "RUNNING" {
		t.Errorf("CurrentState after restart: got %q, want RUNNING", p2.CurrentState)
	}
}

func TestUpdatePipe_updateDescription(t *testing.T) {
	// Given: a pipe
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	streamARN := mustCreateStreamTable(t, srv, "tbl")
	_, queueARN := mustCreateQueue(t, srv, "q")

	resp := createPipe(t, srv, "my-pipe", streamARN, queueARN)
	resp.Body.Close()
	advancePast(t, srv)

	// When: we set a description
	desc := "orders processing pipe"
	resp2 := updatePipe(t, srv, "my-pipe", map[string]any{"Description": desc})
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)

	// Then: description persists
	resp3 := describePipe(t, srv, "my-pipe")
	defer resp3.Body.Close()
	var p struct {
		Description string `json:"Description"`
	}
	helpers.DecodeJSON(t, resp3, &p)
	if p.Description != desc {
		t.Errorf("Description: got %q, want %q", p.Description, desc)
	}
}

func TestUpdatePipe_notFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := updatePipe(t, srv, "does-not-exist", map[string]any{"DesiredState": "STOPPED"})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

func TestUpdatePipe_noDeliveryWhileStopped(t *testing.T) {
	// Given: a pipe that is stopped
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	streamARN := mustCreateStreamTable(t, srv, "tbl")
	queueURL, queueARN := mustCreateQueue(t, srv, "q")

	resp := createPipe(t, srv, "my-pipe", streamARN, queueARN)
	resp.Body.Close()
	advancePast(t, srv) // RUNNING

	// Stop it
	resp2 := updatePipe(t, srv, "my-pipe", map[string]any{"DesiredState": "STOPPED"})
	resp2.Body.Close()
	advancePast(t, srv) // STOPPED

	// When: PutItem
	putResp := ddbCall(t, srv, "PutItem", map[string]any{
		"TableName": "tbl",
		"Item":      map[string]any{"id": map[string]any{"S": "x"}},
	})
	putResp.Body.Close()
	time.Sleep(20 * time.Millisecond)

	// Then: no message delivered
	msgs := receiveMessages(t, srv, queueURL)
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages for stopped pipe, got %d", len(msgs))
	}
}

// ---- Delivery: DynamoDB Streams → SQS ---------------------------------------

func TestPipeDelivery_putItemEnqueuesMessage(t *testing.T) {
	// Given: a DDB table with streams, an SQS queue, and a RUNNING pipe
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	streamARN := mustCreateStreamTable(t, srv, "orders")
	queueURL, queueARN := mustCreateQueue(t, srv, "order-events")

	resp := createPipe(t, srv, "orders-to-sqs", streamARN, queueARN)
	resp.Body.Close()
	advancePast(t, srv) // → RUNNING

	// When: we PutItem into the table
	putResp := ddbCall(t, srv, "PutItem", map[string]any{
		"TableName": "orders",
		"Item": map[string]any{
			"id":    map[string]any{"S": "order-1"},
			"total": map[string]any{"N": "42"},
		},
	})
	putResp.Body.Close()
	helpers.AssertStatus(t, putResp, http.StatusOK)

	// Then: one message appears in the SQS queue
	// Bus delivery is async (goroutine), yield briefly.
	time.Sleep(20 * time.Millisecond)
	bodies := receiveMessages(t, srv, queueURL)
	if len(bodies) != 1 {
		t.Fatalf("expected 1 SQS message, got %d", len(bodies))
	}

	// And: the message body is a valid DynamoDB stream record
	var record struct {
		EventName      string `json:"eventName"`
		EventSource    string `json:"eventSource"`
		EventSourceARN string `json:"eventSourceARN"`
		DynamoDB       struct {
			Keys           map[string]any `json:"Keys"`
			NewImage       map[string]any `json:"NewImage"`
			SequenceNumber string         `json:"SequenceNumber"`
			StreamViewType string         `json:"StreamViewType"`
		} `json:"dynamodb"`
	}
	if err := json.Unmarshal([]byte(bodies[0]), &record); err != nil {
		t.Fatalf("unmarshal message body: %v\nbody: %s", err, bodies[0])
	}

	if record.EventName != "INSERT" {
		t.Errorf("eventName: got %q, want INSERT", record.EventName)
	}
	if record.EventSource != "aws:dynamodb" {
		t.Errorf("eventSource: got %q, want aws:dynamodb", record.EventSource)
	}
	if record.EventSourceARN != streamARN {
		t.Errorf("eventSourceARN: got %q, want %q", record.EventSourceARN, streamARN)
	}
	if record.DynamoDB.Keys["id"] == nil {
		t.Error("dynamodb.Keys: expected 'id' key to be present")
	}
	if record.DynamoDB.NewImage["id"] == nil {
		t.Error("dynamodb.NewImage: expected 'id' to be present")
	}
	if record.DynamoDB.SequenceNumber == "" {
		t.Error("dynamodb.SequenceNumber: expected non-empty")
	}
	if record.DynamoDB.StreamViewType != "NEW_AND_OLD_IMAGES" {
		t.Errorf("dynamodb.StreamViewType: got %q, want NEW_AND_OLD_IMAGES", record.DynamoDB.StreamViewType)
	}
}

func TestPipeDelivery_deleteItemEnqueuesRemoveEvent(t *testing.T) {
	// Given: a RUNNING pipe and an existing item
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	streamARN := mustCreateStreamTable(t, srv, "orders")
	queueURL, queueARN := mustCreateQueue(t, srv, "order-events")

	resp := createPipe(t, srv, "orders-to-sqs", streamARN, queueARN)
	resp.Body.Close()
	advancePast(t, srv) // → RUNNING

	putResp := ddbCall(t, srv, "PutItem", map[string]any{
		"TableName": "orders",
		"Item":      map[string]any{"id": map[string]any{"S": "order-1"}},
	})
	putResp.Body.Close()
	time.Sleep(20 * time.Millisecond)
	receiveMessages(t, srv, queueURL) // drain the INSERT message

	// When: we delete the item
	delResp := ddbCall(t, srv, "DeleteItem", map[string]any{
		"TableName": "orders",
		"Key":       map[string]any{"id": map[string]any{"S": "order-1"}},
	})
	delResp.Body.Close()
	helpers.AssertStatus(t, delResp, http.StatusOK)

	// Then: a REMOVE message arrives
	time.Sleep(20 * time.Millisecond)
	bodies := receiveMessages(t, srv, queueURL)
	if len(bodies) != 1 {
		t.Fatalf("expected 1 REMOVE message, got %d", len(bodies))
	}

	var record struct {
		EventName string `json:"eventName"`
	}
	if err := json.Unmarshal([]byte(bodies[0]), &record); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if record.EventName != "REMOVE" {
		t.Errorf("eventName: got %q, want REMOVE", record.EventName)
	}
}

func TestPipeDelivery_noDeliveryWhileCreating(t *testing.T) {
	// Given: a pipe still in CREATING state
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	streamARN := mustCreateStreamTable(t, srv, "orders")
	queueURL, queueARN := mustCreateQueue(t, srv, "order-events")

	// Create pipe but do NOT advance the clock — pipe stays CREATING
	resp := createPipe(t, srv, "orders-to-sqs", streamARN, queueARN)
	resp.Body.Close()

	// When: we PutItem
	putResp := ddbCall(t, srv, "PutItem", map[string]any{
		"TableName": "orders",
		"Item":      map[string]any{"id": map[string]any{"S": "order-1"}},
	})
	putResp.Body.Close()
	time.Sleep(20 * time.Millisecond)

	// Then: no message delivered (pipe not yet RUNNING)
	bodies := receiveMessages(t, srv, queueURL)
	if len(bodies) != 0 {
		t.Errorf("expected 0 messages while pipe is CREATING, got %d", len(bodies))
	}
}

func TestPipeDelivery_noDeliveryAfterDelete(t *testing.T) {
	// Given: a RUNNING pipe
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	streamARN := mustCreateStreamTable(t, srv, "orders")
	queueURL, queueARN := mustCreateQueue(t, srv, "order-events")

	resp := createPipe(t, srv, "orders-to-sqs", streamARN, queueARN)
	resp.Body.Close()
	advancePast(t, srv) // → RUNNING

	// Delete the pipe and wait for it to be fully removed
	resp2 := deletePipe(t, srv, "orders-to-sqs")
	resp2.Body.Close()
	advancePast(t, srv) // → deleted from store

	// When: we PutItem
	putResp := ddbCall(t, srv, "PutItem", map[string]any{
		"TableName": "orders",
		"Item":      map[string]any{"id": map[string]any{"S": "order-1"}},
	})
	putResp.Body.Close()
	time.Sleep(20 * time.Millisecond)

	// Then: no messages (pipe is gone)
	bodies := receiveMessages(t, srv, queueURL)
	if len(bodies) != 0 {
		t.Errorf("expected 0 messages after pipe deleted, got %d", len(bodies))
	}
}

func TestPipeDelivery_multipleTablesIsolated(t *testing.T) {
	// Given: two tables, two queues, each with its own pipe
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	streamARN1 := mustCreateStreamTable(t, srv, "orders")
	streamARN2 := mustCreateStreamTable(t, srv, "products")
	queueURL1, queueARN1 := mustCreateQueue(t, srv, "orders-q")
	queueURL2, queueARN2 := mustCreateQueue(t, srv, "products-q")

	r1 := createPipe(t, srv, "pipe-orders", streamARN1, queueARN1)
	r1.Body.Close()
	r2 := createPipe(t, srv, "pipe-products", streamARN2, queueARN2)
	r2.Body.Close()
	advancePast(t, srv) // both → RUNNING

	// When: we PutItem in the orders table only
	putResp := ddbCall(t, srv, "PutItem", map[string]any{
		"TableName": "orders",
		"Item":      map[string]any{"id": map[string]any{"S": "order-1"}},
	})
	putResp.Body.Close()
	time.Sleep(20 * time.Millisecond)

	// Then: only the orders queue receives a message
	ordersMessages := receiveMessages(t, srv, queueURL1)
	productsMessages := receiveMessages(t, srv, queueURL2)

	if len(ordersMessages) != 1 {
		t.Errorf("orders queue: expected 1 message, got %d", len(ordersMessages))
	}
	if len(productsMessages) != 0 {
		t.Errorf("products queue: expected 0 messages, got %d", len(productsMessages))
	}
}
