// Package eventbridge_test contains integration tests for the EventBridge emulator.
//
// Run: go test ./tests/integration/eventbridge/...
package eventbridge_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/fxamacker/cbor/v2"

	"github.com/Neaox/overcast/tests/helpers"
)

// ebCall performs an EventBridge X-Amz-Target dispatch request.
func ebCall(t *testing.T, srv *helpers.TestServer, operation string, body map[string]any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s body: %v", operation, err)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "AWSEvents."+operation)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ebCall %s: %v", operation, err)
	}
	return resp
}

func ebCBORCall(t *testing.T, srv *helpers.TestServer, operation string, body map[string]any) *http.Response {
	t.Helper()
	payload, err := cbor.Marshal(body)
	if err != nil {
		t.Fatalf("marshal CBOR %s body: %v", operation, err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/service/EventBridge/operation/"+operation, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build CBOR %s request: %v", operation, err)
	}
	req.Header.Set("Content-Type", "application/cbor")
	req.Header.Set("Smithy-Protocol", "rpc-v2-cbor")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ebCBORCall %s: %v", operation, err)
	}
	return resp
}

// ─── CreateEventBus ───────────────────────────────────────────────────────────

func TestCreateEventBus_success(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: CreateEventBus is called
	resp := ebCall(t, srv, "CreateEventBus", map[string]any{
		"Name": "test-bus",
	})
	defer resp.Body.Close()

	// Then: 200 with EventBusArn
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		EventBusArn string `json:"EventBusArn"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.EventBusArn == "" {
		t.Error("expected EventBusArn to be set")
	}
}

func TestRPCv2CBOR_EventBusAndRuleLifecycle(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ebCBORCall(t, srv, "CreateEventBus", map[string]any{
		"Name": "cbor-bus",
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var created struct {
		EventBusArn string `cbor:"EventBusArn"`
	}
	if err := cbor.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode CBOR CreateEventBus response: %v", err)
	}
	resp.Body.Close()
	if created.EventBusArn == "" {
		t.Fatal("expected EventBusArn")
	}

	resp = ebCBORCall(t, srv, "PutRule", map[string]any{
		"Name":         "cbor-rule",
		"EventBusName": "cbor-bus",
		"EventPattern": `{"source":["overcast.test"]}`,
		"State":        "ENABLED",
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var ruleOut struct {
		RuleArn string `cbor:"RuleArn"`
	}
	if err := cbor.NewDecoder(resp.Body).Decode(&ruleOut); err != nil {
		t.Fatalf("decode CBOR PutRule response: %v", err)
	}
	resp.Body.Close()
	if ruleOut.RuleArn == "" {
		t.Fatal("expected RuleArn")
	}

	resp = ebCBORCall(t, srv, "ListRules", map[string]any{
		"EventBusName": "cbor-bus",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertHeader(t, resp, "Content-Type", "application/cbor")

	var rules struct {
		Rules []struct {
			Name  string `cbor:"Name"`
			State string `cbor:"State"`
		} `cbor:"Rules"`
	}
	if err := cbor.NewDecoder(resp.Body).Decode(&rules); err != nil {
		t.Fatalf("decode CBOR ListRules response: %v", err)
	}
	if len(rules.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules.Rules))
	}
	if rules.Rules[0].Name != "cbor-rule" {
		t.Fatalf("rule name = %q", rules.Rules[0].Name)
	}
}

func TestRPCv2CBOR_TargetsRoundTrip(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ebCBORCall(t, srv, "PutRule", map[string]any{
		"Name": "cbor-target-rule",
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	resp = ebCBORCall(t, srv, "PutTargets", map[string]any{
		"Rule": "cbor-target-rule",
		"Targets": []map[string]any{
			{"Id": "lambda", "Arn": "arn:aws:lambda:us-east-1:000000000000:function:test"},
		},
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var putOut struct {
		FailedEntryCount int   `cbor:"FailedEntryCount"`
		FailedEntries    []any `cbor:"FailedEntries"`
	}
	if err := cbor.NewDecoder(resp.Body).Decode(&putOut); err != nil {
		t.Fatalf("decode CBOR PutTargets response: %v", err)
	}
	resp.Body.Close()
	if putOut.FailedEntryCount != 0 {
		t.Fatalf("FailedEntryCount = %d", putOut.FailedEntryCount)
	}
	if len(putOut.FailedEntries) != 0 {
		t.Fatalf("FailedEntries = %#v, want empty", putOut.FailedEntries)
	}

	resp = ebCBORCall(t, srv, "ListTargetsByRule", map[string]any{
		"Rule": "cbor-target-rule",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var targets struct {
		Targets []struct {
			ID  string `cbor:"Id"`
			ARN string `cbor:"Arn"`
		} `cbor:"Targets"`
	}
	if err := cbor.NewDecoder(resp.Body).Decode(&targets); err != nil {
		t.Fatalf("decode CBOR ListTargetsByRule response: %v", err)
	}
	if len(targets.Targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets.Targets))
	}
	if targets.Targets[0].ID != "lambda" {
		t.Fatalf("target ID = %q", targets.Targets[0].ID)
	}
}

// ─── ListEventBuses ───────────────────────────────────────────────────────────

func TestListEventBuses_success(t *testing.T) {
	// Given: two event buses
	srv := helpers.NewTestServer(t)
	for _, name := range []string{"bus-a", "bus-b"} {
		r := ebCall(t, srv, "CreateEventBus", map[string]any{"Name": name})
		r.Body.Close()
	}

	// When: ListEventBuses is called
	resp := ebCall(t, srv, "ListEventBuses", map[string]any{})
	defer resp.Body.Close()

	// Then: 200 with EventBuses list (includes default + 2 created = 3)
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		EventBuses []struct {
			Name string `json:"Name"`
			Arn  string `json:"Arn"`
		} `json:"EventBuses"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.EventBuses) < 2 {
		t.Errorf("expected at least 2 event buses, got %d", len(result.EventBuses))
	}
}

// ─── PutRule ──────────────────────────────────────────────────────────────────

func TestPutRule_success(t *testing.T) {
	// Given: an event bus
	srv := helpers.NewTestServer(t)
	cr := ebCall(t, srv, "CreateEventBus", map[string]any{"Name": "my-bus"})
	cr.Body.Close()

	// When: PutRule is called
	resp := ebCall(t, srv, "PutRule", map[string]any{
		"Name":         "test-rule",
		"EventBusName": "my-bus",
		"EventPattern": `{"source":["aws.ec2"]}`,
		"State":        "ENABLED",
	})
	defer resp.Body.Close()

	// Then: 200 with RuleArn
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		RuleArn string `json:"RuleArn"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.RuleArn == "" {
		t.Error("expected RuleArn to be set")
	}
}

// ─── ListRules ────────────────────────────────────────────────────────────────

func TestListRules_success(t *testing.T) {
	// Given: a rule in a bus
	srv := helpers.NewTestServer(t)
	cr := ebCall(t, srv, "CreateEventBus", map[string]any{"Name": "my-bus"})
	cr.Body.Close()
	pr := ebCall(t, srv, "PutRule", map[string]any{
		"Name":         "my-rule",
		"EventBusName": "my-bus",
		"EventPattern": `{"source":["aws.ec2"]}`,
		"State":        "ENABLED",
	})
	pr.Body.Close()

	// When: ListRules is called
	resp := ebCall(t, srv, "ListRules", map[string]any{
		"EventBusName": "my-bus",
	})
	defer resp.Body.Close()

	// Then: 200 with Rules including my-rule
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Rules []struct {
			Name string `json:"Name"`
		} `json:"Rules"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Rules) == 0 {
		t.Error("expected at least one rule")
	}
}

// ─── DeleteEventBus ───────────────────────────────────────────────────────────

func TestDeleteEventBus_success(t *testing.T) {
	// Given: an event bus
	srv := helpers.NewTestServer(t)
	cr := ebCall(t, srv, "CreateEventBus", map[string]any{"Name": "to-delete"})
	cr.Body.Close()

	// When: DeleteEventBus is called
	resp := ebCall(t, srv, "DeleteEventBus", map[string]any{"Name": "to-delete"})
	defer resp.Body.Close()

	// Then: 200
	helpers.AssertStatus(t, resp, http.StatusOK)
}
