// Package stepfunctions_test contains integration tests for the Step Functions emulator.
//
// Run: go test ./tests/integration/stepfunctions/...
package stepfunctions_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/fxamacker/cbor/v2"

	"github.com/Neaox/overcast/tests/helpers"
)

// sfnCall performs a Step Functions X-Amz-Target dispatch request.
func sfnCall(t *testing.T, srv *helpers.TestServer, operation string, body map[string]any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s body: %v", operation, err)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AWSStepFunctions."+operation)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sfnCall %s: %v", operation, err)
	}
	return resp
}

func sfnCBORCall(t *testing.T, srv *helpers.TestServer, operation string, body map[string]any) *http.Response {
	t.Helper()
	payload, err := cbor.Marshal(body)
	if err != nil {
		t.Fatalf("marshal CBOR %s body: %v", operation, err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/service/StepFunctions/operation/"+operation, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build CBOR %s request: %v", operation, err)
	}
	req.Header.Set("Content-Type", "application/cbor")
	req.Header.Set("Smithy-Protocol", "rpc-v2-cbor")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sfnCBORCall %s: %v", operation, err)
	}
	return resp
}

const testDefinition = `{"Comment":"test","StartAt":"Pass","States":{"Pass":{"Type":"Pass","End":true}}}`

// ─── CreateStateMachine ───────────────────────────────────────────────────────

func TestCreateStateMachine_success(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: CreateStateMachine is called
	resp := sfnCall(t, srv, "CreateStateMachine", map[string]any{
		"name":       "test-sm",
		"type":       "EXPRESS",
		"definition": testDefinition,
		"roleArn":    "arn:aws:iam::000000000000:role/test-role",
	})
	defer resp.Body.Close()

	// Then: 200 with stateMachineArn
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		StateMachineArn string `json:"stateMachineArn"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.StateMachineArn == "" {
		t.Error("expected stateMachineArn to be set")
	}
}

// TestCreateStateMachine_tagsAppliedAtCreate: tags passed to
// CreateStateMachine (issue #1196, Axis B) must be stored and readable via
// ListTagsForResource immediately, without a follow-up TagResource call.
// CreateStateMachine used to model no tags request member at all — the
// legacy JSON1.0/1.1 handler and the CBOR typed path were also two
// independent implementations, so both are covered here.
func TestCreateStateMachine_tagsAppliedAtCreate(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := sfnCall(t, srv, "CreateStateMachine", map[string]any{
		"name":       "tagged-at-create",
		"type":       "EXPRESS",
		"definition": testDefinition,
		"roleArn":    "arn:aws:iam::000000000000:role/test-role",
		"tags":       []map[string]string{{"key": "env", "value": "prod"}},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var created struct {
		StateMachineArn string `json:"stateMachineArn"`
	}
	helpers.DecodeJSON(t, resp, &created)

	list := sfnCall(t, srv, "ListTagsForResource", map[string]any{"resourceArn": created.StateMachineArn})
	defer list.Body.Close()
	helpers.AssertStatus(t, list, http.StatusOK)
	var out struct {
		Tags []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"tags"`
	}
	helpers.DecodeJSON(t, list, &out)
	if len(out.Tags) != 1 || out.Tags[0].Key != "env" || out.Tags[0].Value != "prod" {
		t.Errorf("tags: got %v, want env=prod", out.Tags)
	}
}

// TestCreateStateMachineCBOR_tagsAppliedAtCreate is
// TestCreateStateMachine_tagsAppliedAtCreate over the CBOR typed path.
func TestCreateStateMachineCBOR_tagsAppliedAtCreate(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := sfnCBORCall(t, srv, "CreateStateMachine", map[string]any{
		"name":       "tagged-at-create-cbor",
		"type":       "EXPRESS",
		"definition": testDefinition,
		"roleArn":    "arn:aws:iam::000000000000:role/test-role",
		"tags":       []map[string]string{{"key": "env", "value": "prod"}},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read CBOR response: %v", err)
	}
	var created struct {
		StateMachineArn string `cbor:"stateMachineArn"`
	}
	if err := cbor.Unmarshal(body, &created); err != nil {
		t.Fatalf("unmarshal CBOR response: %v", err)
	}

	list := sfnCall(t, srv, "ListTagsForResource", map[string]any{"resourceArn": created.StateMachineArn})
	defer list.Body.Close()
	helpers.AssertStatus(t, list, http.StatusOK)
	var out struct {
		Tags []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"tags"`
	}
	helpers.DecodeJSON(t, list, &out)
	if len(out.Tags) != 1 || out.Tags[0].Key != "env" || out.Tags[0].Value != "prod" {
		t.Errorf("tags: got %v, want env=prod", out.Tags)
	}
}

// TestCreateStateMachine_invalidTagRejected: an invalid tag passed to
// CreateStateMachine must be rejected before the state machine is created.
func TestCreateStateMachine_invalidTagRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := sfnCall(t, srv, "CreateStateMachine", map[string]any{
		"name":       "tags-rejected",
		"type":       "EXPRESS",
		"definition": testDefinition,
		"roleArn":    "arn:aws:iam::000000000000:role/test-role",
		"tags":       []map[string]string{{"key": "aws:reserved", "value": "x"}},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)

	desc := sfnCall(t, srv, "DescribeStateMachine", map[string]any{
		"stateMachineArn": "arn:aws:states:us-east-1:000000000000:stateMachine:tags-rejected",
	})
	defer desc.Body.Close()
	helpers.AssertStatus(t, desc, http.StatusBadRequest)
}

func TestRPCv2CBOR_StateMachineLifecycle(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := sfnCBORCall(t, srv, "CreateStateMachine", map[string]any{
		"name":       "cbor-sm",
		"type":       "EXPRESS",
		"definition": testDefinition,
		"roleArn":    "arn:aws:iam::000000000000:role/test-role",
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var created struct {
		StateMachineArn string `cbor:"stateMachineArn"`
	}
	if err := cbor.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode CBOR CreateStateMachine response: %v", err)
	}
	resp.Body.Close()
	if created.StateMachineArn == "" {
		t.Fatal("expected stateMachineArn")
	}

	resp = sfnCBORCall(t, srv, "ListStateMachines", map[string]any{})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var listed struct {
		StateMachines []struct {
			StateMachineArn string `cbor:"stateMachineArn"`
			Name            string `cbor:"name"`
		} `cbor:"stateMachines"`
	}
	if err := cbor.NewDecoder(resp.Body).Decode(&listed); err != nil {
		t.Fatalf("decode CBOR ListStateMachines response: %v", err)
	}
	resp.Body.Close()
	if len(listed.StateMachines) != 1 {
		t.Fatalf("expected 1 state machine, got %d", len(listed.StateMachines))
	}
	if listed.StateMachines[0].Name != "cbor-sm" {
		t.Fatalf("state machine name = %q", listed.StateMachines[0].Name)
	}

	resp = sfnCBORCall(t, srv, "StartExecution", map[string]any{
		"stateMachineArn": created.StateMachineArn,
		"name":            "cbor-exec",
		"input":           `{"key":"value"}`,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var started struct {
		ExecutionArn string `cbor:"executionArn"`
	}
	if err := cbor.NewDecoder(resp.Body).Decode(&started); err != nil {
		t.Fatalf("decode CBOR StartExecution response: %v", err)
	}
	if started.ExecutionArn == "" {
		t.Fatal("expected executionArn")
	}

	resp = sfnCBORCall(t, srv, "UpdateStateMachine", map[string]any{
		"stateMachineArn":      created.StateMachineArn,
		"roleArn":              "arn:aws:iam::000000000000:role/cbor-updated",
		"tracingConfiguration": map[string]any{"enabled": true},
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var updated struct {
		UpdateDate float64 `cbor:"updateDate"`
	}
	if err := cbor.NewDecoder(resp.Body).Decode(&updated); err != nil {
		t.Fatalf("decode CBOR UpdateStateMachine response: %v", err)
	}
	resp.Body.Close()
	if updated.UpdateDate == 0 {
		t.Fatal("expected non-zero updateDate")
	}

	resp = sfnCBORCall(t, srv, "DescribeStateMachine", map[string]any{
		"stateMachineArn": created.StateMachineArn,
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var described struct {
		RoleArn              string `cbor:"roleArn"`
		TracingConfiguration struct {
			Enabled bool `cbor:"enabled"`
		} `cbor:"tracingConfiguration"`
	}
	if err := cbor.NewDecoder(resp.Body).Decode(&described); err != nil {
		t.Fatalf("decode CBOR DescribeStateMachine response: %v", err)
	}
	resp.Body.Close()
	if described.RoleArn != "arn:aws:iam::000000000000:role/cbor-updated" {
		t.Fatalf("roleArn = %q, want updated role", described.RoleArn)
	}
	if !described.TracingConfiguration.Enabled {
		t.Fatal("tracingConfiguration.enabled = false, want true")
	}
}

// ─── DescribeStateMachine ─────────────────────────────────────────────────────

func TestDescribeStateMachine_success(t *testing.T) {
	// Given: an existing state machine
	srv := helpers.NewTestServer(t)
	cr := sfnCall(t, srv, "CreateStateMachine", map[string]any{
		"name":       "my-sm",
		"definition": testDefinition,
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	var createResult struct {
		StateMachineArn string `json:"stateMachineArn"`
	}
	helpers.DecodeJSON(t, cr, &createResult)

	// When: DescribeStateMachine is called
	resp := sfnCall(t, srv, "DescribeStateMachine", map[string]any{
		"stateMachineArn": createResult.StateMachineArn,
	})
	defer resp.Body.Close()

	// Then: 200 with stateMachineArn and name
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		StateMachineArn string `json:"stateMachineArn"`
		Name            string `json:"name"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.StateMachineArn != createResult.StateMachineArn {
		t.Errorf("expected stateMachineArn=%q, got %q", createResult.StateMachineArn, result.StateMachineArn)
	}
	if result.Name != "my-sm" {
		t.Errorf("expected name=my-sm, got %q", result.Name)
	}
}

func TestDescribeStateMachine_notFound(t *testing.T) {
	// Given: no state machines
	srv := helpers.NewTestServer(t)

	// When: DescribeStateMachine is called with a non-existent ARN
	resp := sfnCall(t, srv, "DescribeStateMachine", map[string]any{
		"stateMachineArn": "arn:aws:states:us-east-1:000000000000:stateMachine:nonexistent",
	})
	defer resp.Body.Close()

	// Then: error with StateMachineDoesNotExist
	helpers.AssertJSONError(t, resp, "StateMachineDoesNotExist")
}

func TestDescribeStateMachine_loggingAndTracingDefaults(t *testing.T) {
	// Given: a state machine created without LoggingConfiguration/TracingConfiguration
	srv := helpers.NewTestServer(t)
	cr := sfnCall(t, srv, "CreateStateMachine", map[string]any{
		"name":       "no-logging-sm",
		"definition": testDefinition,
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	var createResult struct {
		StateMachineArn string `json:"stateMachineArn"`
	}
	helpers.DecodeJSON(t, cr, &createResult)

	// When: DescribeStateMachine is called
	resp := sfnCall(t, srv, "DescribeStateMachine", map[string]any{
		"stateMachineArn": createResult.StateMachineArn,
	})
	defer resp.Body.Close()

	// Then: AWS's documented defaults are echoed, not an absent field.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		LoggingConfiguration struct {
			Level                string `json:"level"`
			IncludeExecutionData bool   `json:"includeExecutionData"`
			Destinations         []any  `json:"destinations"`
		} `json:"loggingConfiguration"`
		TracingConfiguration struct {
			Enabled bool `json:"enabled"`
		} `json:"tracingConfiguration"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.LoggingConfiguration.Level != "OFF" {
		t.Errorf("loggingConfiguration.level = %q, want OFF", result.LoggingConfiguration.Level)
	}
	if result.LoggingConfiguration.Destinations == nil || len(result.LoggingConfiguration.Destinations) != 0 {
		t.Errorf("loggingConfiguration.destinations = %+v, want empty list", result.LoggingConfiguration.Destinations)
	}
	if result.TracingConfiguration.Enabled {
		t.Errorf("tracingConfiguration.enabled = true, want false")
	}
}

func TestCreateStateMachine_loggingAndTracingRoundTrip(t *testing.T) {
	// Given: a state machine created with LoggingConfiguration/TracingConfiguration
	srv := helpers.NewTestServer(t)
	logging := map[string]any{
		"level":                "ALL",
		"includeExecutionData": true,
		"destinations": []any{
			map[string]any{
				"cloudWatchLogsLogGroup": map[string]any{
					"logGroupArn": "arn:aws:logs:us-east-1:000000000000:log-group:/sfn/test:*",
				},
			},
		},
	}
	tracing := map[string]any{"enabled": true}
	cr := sfnCall(t, srv, "CreateStateMachine", map[string]any{
		"name":                 "logging-sm",
		"definition":           testDefinition,
		"loggingConfiguration": logging,
		"tracingConfiguration": tracing,
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	var createResult struct {
		StateMachineArn string `json:"stateMachineArn"`
	}
	helpers.DecodeJSON(t, cr, &createResult)

	// When: DescribeStateMachine is called
	resp := sfnCall(t, srv, "DescribeStateMachine", map[string]any{
		"stateMachineArn": createResult.StateMachineArn,
	})
	defer resp.Body.Close()

	// Then: both configurations round-trip exactly.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		LoggingConfiguration struct {
			Level string `json:"level"`
		} `json:"loggingConfiguration"`
		TracingConfiguration struct {
			Enabled bool `json:"enabled"`
		} `json:"tracingConfiguration"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.LoggingConfiguration.Level != "ALL" {
		t.Errorf("loggingConfiguration.level = %q, want ALL", result.LoggingConfiguration.Level)
	}
	if !result.TracingConfiguration.Enabled {
		t.Errorf("tracingConfiguration.enabled = false, want true")
	}
}

// ─── UpdateStateMachine ───────────────────────────────────────────────────────

func TestUpdateStateMachine_success(t *testing.T) {
	// Given: an existing state machine
	srv := helpers.NewTestServer(t)
	cr := sfnCall(t, srv, "CreateStateMachine", map[string]any{
		"name":       "update-sm",
		"definition": testDefinition,
		"roleArn":    "arn:aws:iam::000000000000:role/original",
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	var createResult struct {
		StateMachineArn string `json:"stateMachineArn"`
	}
	helpers.DecodeJSON(t, cr, &createResult)

	newDefinition := `{"Comment":"updated","StartAt":"Pass","States":{"Pass":{"Type":"Pass","End":true}}}`

	// When: UpdateStateMachine changes the definition, role and tracing config
	upd := sfnCall(t, srv, "UpdateStateMachine", map[string]any{
		"stateMachineArn":      createResult.StateMachineArn,
		"definition":           newDefinition,
		"roleArn":              "arn:aws:iam::000000000000:role/updated",
		"tracingConfiguration": map[string]any{"enabled": true},
	})
	defer upd.Body.Close()

	// Then: 200 with an updateDate
	helpers.AssertStatus(t, upd, http.StatusOK)
	var updResult struct {
		UpdateDate float64 `json:"updateDate"`
	}
	helpers.DecodeJSON(t, upd, &updResult)
	if updResult.UpdateDate == 0 {
		t.Error("expected non-zero updateDate")
	}

	// And: DescribeStateMachine reflects every changed field.
	resp := sfnCall(t, srv, "DescribeStateMachine", map[string]any{
		"stateMachineArn": createResult.StateMachineArn,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Definition           string `json:"definition"`
		RoleArn              string `json:"roleArn"`
		TracingConfiguration struct {
			Enabled bool `json:"enabled"`
		} `json:"tracingConfiguration"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Definition != newDefinition {
		t.Errorf("definition = %q, want %q", result.Definition, newDefinition)
	}
	if result.RoleArn != "arn:aws:iam::000000000000:role/updated" {
		t.Errorf("roleArn = %q, want updated role", result.RoleArn)
	}
	if !result.TracingConfiguration.Enabled {
		t.Error("tracingConfiguration.enabled = false, want true")
	}
}

func TestUpdateStateMachine_notFound(t *testing.T) {
	// Given: no state machines
	srv := helpers.NewTestServer(t)

	// When: UpdateStateMachine is called with a non-existent ARN
	resp := sfnCall(t, srv, "UpdateStateMachine", map[string]any{
		"stateMachineArn": "arn:aws:states:us-east-1:000000000000:stateMachine:nonexistent",
		"definition":      testDefinition,
	})
	defer resp.Body.Close()

	// Then: error with StateMachineDoesNotExist
	helpers.AssertJSONError(t, resp, "StateMachineDoesNotExist")
}

func TestUpdateStateMachine_invalidDefinition(t *testing.T) {
	// Given: an existing state machine
	srv := helpers.NewTestServer(t)
	cr := sfnCall(t, srv, "CreateStateMachine", map[string]any{
		"name":       "invalid-update-sm",
		"definition": testDefinition,
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	var createResult struct {
		StateMachineArn string `json:"stateMachineArn"`
	}
	helpers.DecodeJSON(t, cr, &createResult)

	// When: UpdateStateMachine is called with definition that is not valid ASL
	resp := sfnCall(t, srv, "UpdateStateMachine", map[string]any{
		"stateMachineArn": createResult.StateMachineArn,
		"definition":      `{"not": "valid ASL"}`,
	})
	defer resp.Body.Close()

	// Then: InvalidDefinition, and the original definition is left untouched
	helpers.AssertJSONError(t, resp, "InvalidDefinition")
	describeResp := sfnCall(t, srv, "DescribeStateMachine", map[string]any{
		"stateMachineArn": createResult.StateMachineArn,
	})
	defer describeResp.Body.Close()
	var result struct {
		Definition string `json:"definition"`
	}
	helpers.DecodeJSON(t, describeResp, &result)
	if result.Definition != testDefinition {
		t.Errorf("definition changed despite rejected update: %q", result.Definition)
	}
}

// ─── ListStateMachines ────────────────────────────────────────────────────────

func TestListStateMachines_success(t *testing.T) {
	// Given: two state machines
	srv := helpers.NewTestServer(t)
	for _, name := range []string{"sm-a", "sm-b"} {
		r := sfnCall(t, srv, "CreateStateMachine", map[string]any{
			"name":       name,
			"definition": testDefinition,
		})
		r.Body.Close()
	}

	// When: ListStateMachines is called
	resp := sfnCall(t, srv, "ListStateMachines", map[string]any{})
	defer resp.Body.Close()

	// Then: 200 with 2 state machines
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		StateMachines []struct {
			StateMachineArn string `json:"stateMachineArn"`
		} `json:"stateMachines"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.StateMachines) != 2 {
		t.Errorf("expected 2 state machines, got %d", len(result.StateMachines))
	}
}

// ─── StartExecution ───────────────────────────────────────────────────────────

func TestStartExecution_success(t *testing.T) {
	// Given: an existing state machine
	srv := helpers.NewTestServer(t)
	cr := sfnCall(t, srv, "CreateStateMachine", map[string]any{
		"name":       "exec-sm",
		"definition": testDefinition,
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	var createResult struct {
		StateMachineArn string `json:"stateMachineArn"`
	}
	helpers.DecodeJSON(t, cr, &createResult)

	// When: StartExecution is called
	resp := sfnCall(t, srv, "StartExecution", map[string]any{
		"stateMachineArn": createResult.StateMachineArn,
		"input":           `{"key":"value"}`,
	})
	defer resp.Body.Close()

	// Then: 200 with executionArn
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		ExecutionArn string `json:"executionArn"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.ExecutionArn == "" {
		t.Error("expected executionArn to be set")
	}
}

// ─── DeleteStateMachine ───────────────────────────────────────────────────────

func TestCreateStateMachine_idempotent(t *testing.T) {
	// Given: an existing state machine
	srv := helpers.NewTestServer(t)
	first := sfnCall(t, srv, "CreateStateMachine", map[string]any{
		"name":       "idem-sm",
		"type":       "EXPRESS",
		"definition": testDefinition,
		"roleArn":    "arn:aws:iam::000000000000:role/r",
	})
	defer first.Body.Close()
	helpers.AssertStatus(t, first, http.StatusOK)
	var firstResult struct {
		StateMachineArn string `json:"stateMachineArn"`
	}
	helpers.DecodeJSON(t, first, &firstResult)

	// When: CreateStateMachine is called again with the exact same parameters
	second := sfnCall(t, srv, "CreateStateMachine", map[string]any{
		"name":       "idem-sm",
		"type":       "EXPRESS",
		"definition": testDefinition,
		"roleArn":    "arn:aws:iam::000000000000:role/r",
	})
	defer second.Body.Close()

	// Then: 200 with the same ARN (idempotent)
	helpers.AssertStatus(t, second, http.StatusOK)
	var secondResult struct {
		StateMachineArn string `json:"stateMachineArn"`
	}
	helpers.DecodeJSON(t, second, &secondResult)
	if secondResult.StateMachineArn != firstResult.StateMachineArn {
		t.Errorf("idempotent create: expected same ARN %q, got %q", firstResult.StateMachineArn, secondResult.StateMachineArn)
	}
}

func TestCreateStateMachine_alreadyExists_differentDefinition(t *testing.T) {
	// Given: an existing state machine
	srv := helpers.NewTestServer(t)
	first := sfnCall(t, srv, "CreateStateMachine", map[string]any{
		"name":       "dup-sm",
		"type":       "EXPRESS",
		"definition": testDefinition,
		"roleArn":    "arn:aws:iam::000000000000:role/r",
	})
	defer first.Body.Close()
	helpers.AssertStatus(t, first, http.StatusOK)

	// When: CreateStateMachine is called again with a different definition
	second := sfnCall(t, srv, "CreateStateMachine", map[string]any{
		"name":       "dup-sm",
		"type":       "EXPRESS",
		"definition": `{"Comment":"different","StartAt":"Pass","States":{"Pass":{"Type":"Pass","End":true}}}`,
		"roleArn":    "arn:aws:iam::000000000000:role/r",
	})
	defer second.Body.Close()

	// Then: StateMachineAlreadyExists error
	helpers.AssertJSONError(t, second, "StateMachineAlreadyExists")
}

// ─── DeleteStateMachine ───────────────────────────────────────────────────────

func TestDeleteStateMachine_success(t *testing.T) {
	// Given: an existing state machine
	srv := helpers.NewTestServer(t)
	cr := sfnCall(t, srv, "CreateStateMachine", map[string]any{
		"name":       "to-delete",
		"definition": testDefinition,
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	var createResult struct {
		StateMachineArn string `json:"stateMachineArn"`
	}
	helpers.DecodeJSON(t, cr, &createResult)

	// When: DeleteStateMachine is called
	resp := sfnCall(t, srv, "DeleteStateMachine", map[string]any{
		"stateMachineArn": createResult.StateMachineArn,
	})
	defer resp.Body.Close()

	// Then: 200
	helpers.AssertStatus(t, resp, http.StatusOK)
}
