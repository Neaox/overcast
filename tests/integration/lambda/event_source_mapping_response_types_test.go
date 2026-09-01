package lambda_test

// event_source_mapping_response_types_test.go — the management-plane half of
// #512. The poller's behaviour is pinned in internal/services/lambda; what is
// pinned here is that a template or SDK call asking for partial-batch failure
// reporting is accepted, stored and echoed back, and that a value AWS's enum
// does not have is refused rather than stored.

import (
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// responseTypesOf reads FunctionResponseTypes out of a decoded mapping.
func responseTypesOf(t *testing.T, mapping map[string]any) []string {
	t.Helper()
	raw, present := mapping["FunctionResponseTypes"]
	if !present {
		return nil
	}
	values, ok := raw.([]any)
	if !ok {
		t.Fatalf("FunctionResponseTypes = %#v, want a list", raw)
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			t.Fatalf("FunctionResponseTypes entry = %#v, want a string", value)
		}
		out = append(out, text)
	}
	return out
}

func TestCreateEventSourceMapping_ReportBatchItemFailures(t *testing.T) {
	// Given: a function to map onto.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "report-batch-item-failures-fn")

	// When: a mapping asks for partial-batch failure reporting — what CDK's
	// `reportBatchItemFailures: true` sends.
	created := createESM(t, srv, map[string]any{
		"FunctionName":          "report-batch-item-failures-fn",
		"EventSourceArn":        sqsARN("report-batch-item-failures-queue"),
		"FunctionResponseTypes": []string{"ReportBatchItemFailures"},
	})

	// Then: it is accepted and echoed back, because the poller honours it.
	if got := responseTypesOf(t, created); len(got) != 1 || got[0] != "ReportBatchItemFailures" {
		t.Fatalf("created FunctionResponseTypes = %v, want [ReportBatchItemFailures]", got)
	}

	// And: it survives a read, so a `cdk diff` sees no drift.
	id, _ := created["UUID"].(string)
	get := doJSON(t, http.MethodGet, lambdaURL(srv, "/event-source-mappings/"+id), nil)
	var fetched map[string]any
	decodeJSON(t, get, &fetched)
	if got := responseTypesOf(t, fetched); len(got) != 1 || got[0] != "ReportBatchItemFailures" {
		t.Fatalf("fetched FunctionResponseTypes = %v, want [ReportBatchItemFailures]", got)
	}
}

func TestUpdateEventSourceMapping_ReportBatchItemFailures(t *testing.T) {
	// Given: a mapping created without partial-batch failure reporting.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "update-response-types-fn")
	created := createESM(t, srv, map[string]any{
		"FunctionName":   "update-response-types-fn",
		"EventSourceArn": sqsARN("update-response-types-queue"),
	})
	id, _ := created["UUID"].(string)

	// When: an update turns it on.
	resp := doJSON(t, http.MethodPut, lambdaURL(srv, "/event-source-mappings/"+id), map[string]any{
		"FunctionResponseTypes": []string{"ReportBatchItemFailures"},
	})
	helpers.AssertStatus(t, resp, http.StatusAccepted)
	var updated map[string]any
	decodeJSON(t, resp, &updated)
	if got := responseTypesOf(t, updated); len(got) != 1 || got[0] != "ReportBatchItemFailures" {
		t.Fatalf("updated FunctionResponseTypes = %v, want [ReportBatchItemFailures]", got)
	}

	// And: an explicit empty list turns it back off — the value CloudFormation
	// sends to clear the property when a template drops it.
	cleared := doJSON(t, http.MethodPut, lambdaURL(srv, "/event-source-mappings/"+id), map[string]any{
		"FunctionResponseTypes": []any{},
	})
	helpers.AssertStatus(t, cleared, http.StatusAccepted)
	var afterClear map[string]any
	decodeJSON(t, cleared, &afterClear)
	if got := responseTypesOf(t, afterClear); len(got) != 0 {
		t.Fatalf("cleared FunctionResponseTypes = %v, want none", got)
	}

	// And: an update that says nothing about it leaves whatever is there alone.
	on := doJSON(t, http.MethodPut, lambdaURL(srv, "/event-source-mappings/"+id), map[string]any{
		"FunctionResponseTypes": []string{"ReportBatchItemFailures"},
	})
	helpers.AssertStatus(t, on, http.StatusAccepted)
	on.Body.Close()
	untouched := doJSON(t, http.MethodPut, lambdaURL(srv, "/event-source-mappings/"+id), map[string]any{
		"BatchSize": 5,
	})
	helpers.AssertStatus(t, untouched, http.StatusAccepted)
	var afterUnrelated map[string]any
	decodeJSON(t, untouched, &afterUnrelated)
	if got := responseTypesOf(t, afterUnrelated); len(got) != 1 || got[0] != "ReportBatchItemFailures" {
		t.Fatalf("FunctionResponseTypes = %v after an unrelated update, want [ReportBatchItemFailures]", got)
	}
}

func TestEventSourceMapping_FunctionResponseTypesEnumIsValidated(t *testing.T) {
	// Given: a function to map onto.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "response-types-enum-fn")

	// When: a value outside AWS's one-member enum is sent.
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/event-source-mappings/"), map[string]any{
		"FunctionName":          "response-types-enum-fn",
		"EventSourceArn":        sqsARN("response-types-enum-queue"),
		"FunctionResponseTypes": []string{"ReportEverything"},
	})

	// Then: it is refused with AWS's own constraint message, rather than stored
	// as a response type nothing will ever read.
	assertLambdaValidationMessage(t, resp,
		"1 validation error detected: Value '[ReportEverything]' at 'functionResponseTypes' failed to satisfy constraint: Member must satisfy enum value set: [ReportBatchItemFailures]")
}
