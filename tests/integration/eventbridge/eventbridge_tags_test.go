package eventbridge_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// ebPutTagRule creates a rule and returns its ARN.
func ebPutTagRule(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	resp := ebCall(t, srv, "PutRule", map[string]any{
		"Name":         name,
		"EventPattern": `{"source":["test"]}`,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		RuleArn string `json:"RuleArn"`
	}
	helpers.DecodeJSON(t, resp, &out)
	if out.RuleArn == "" {
		t.Fatal("PutRule returned no RuleArn")
	}
	return out.RuleArn
}

// TestEventBridgeTagResource_roundTrip tags an existing rule with the SDK
// list shape and reads it back.
func TestEventBridgeTagResource_roundTrip(t *testing.T) {
	// Given: a rule
	srv := helpers.NewTestServer(t)
	arn := ebPutTagRule(t, srv, "tagged-rule")

	// When: TagResource is called
	resp := ebCall(t, srv, "TagResource", map[string]any{
		"ResourceARN": arn,
		"Tags":        []map[string]string{{"Key": "env", "Value": "prod"}},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: ListTagsForResource returns the tag
	list := ebCall(t, srv, "ListTagsForResource", map[string]any{"ResourceARN": arn})
	defer list.Body.Close()
	helpers.AssertStatus(t, list, http.StatusOK)
	var out struct {
		Tags []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"Tags"`
	}
	helpers.DecodeJSON(t, list, &out)
	if len(out.Tags) != 1 || out.Tags[0].Key != "env" || out.Tags[0].Value != "prod" {
		t.Errorf("Tags: got %v, want env=prod", out.Tags)
	}
}

// TestEventBridgeTagResource_missingResource: real EventBridge refuses to tag
// a rule or event bus that does not exist.
func TestEventBridgeTagResource_missingResource(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ebCall(t, srv, "TagResource", map[string]any{
		"ResourceARN": "arn:aws:events:us-east-1:000000000000:rule/no-such-rule",
		"Tags":        []map[string]string{{"Key": "env", "Value": "prod"}},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")

	bus := ebCall(t, srv, "TagResource", map[string]any{
		"ResourceARN": "arn:aws:events:us-east-1:000000000000:event-bus/no-such-bus",
		"Tags":        []map[string]string{{"Key": "env", "Value": "prod"}},
	})
	defer bus.Body.Close()
	helpers.AssertStatus(t, bus, http.StatusBadRequest)
	helpers.AssertJSONError(t, bus, "ResourceNotFoundException")
}

// TestEventBridgeTagResource_invalidTag: reserved aws: keys are rejected.
func TestEventBridgeTagResource_invalidTag(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := ebPutTagRule(t, srv, "validated-rule")

	resp := ebCall(t, srv, "TagResource", map[string]any{
		"ResourceARN": arn,
		"Tags":        []map[string]string{{"Key": "aws:reserved", "Value": "x"}},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ValidationException")
}

// TestEventBridgeTagResource_corruptTagBlobIsIsolated: a corrupt persisted
// tag blob must not 500 (or panic) the tag operations — malformed persisted
// state is isolated and treated as empty.
func TestEventBridgeTagResource_corruptTagBlob(t *testing.T) {
	// Given: a rule whose stored tag blob is corrupt
	srv := helpers.NewTestServer(t)
	arn := ebPutTagRule(t, srv, "corrupt-rule")
	if err := srv.Store.Set(context.Background(), "eb:tags", arn, "{not json"); err != nil {
		t.Fatalf("seed corrupt blob: %v", err)
	}

	// When: the rule is tagged
	resp := ebCall(t, srv, "TagResource", map[string]any{
		"ResourceARN": arn,
		"Tags":        []map[string]string{{"Key": "env", "Value": "prod"}},
	})
	defer resp.Body.Close()

	// Then: the write succeeds — the corrupt blob is isolated, not a 500
	helpers.AssertStatus(t, resp, http.StatusOK)

	list := ebCall(t, srv, "ListTagsForResource", map[string]any{"ResourceARN": arn})
	defer list.Body.Close()
	helpers.AssertStatus(t, list, http.StatusOK)
	var out struct {
		Tags []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"Tags"`
	}
	helpers.DecodeJSON(t, list, &out)
	if len(out.Tags) != 1 || out.Tags[0].Key != "env" {
		t.Errorf("Tags: got %v, want env=prod after corrupt blob overwrite", out.Tags)
	}
}
