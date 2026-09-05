//go:build dev

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParsePath(t *testing.T) {
	cases := []struct {
		path     string
		segments int
		bad      bool
	}{
		{"$", 0, false},
		{"$.QueueUrl", 1, false},
		{"$.Attributes.QueueArn", 2, false},
		{"$.Queues[0].Name", 3, false},
		{"$[0]", 1, false},
		{"$.Tags.compat-key", 2, false},
		{"QueueUrl", 0, true},
		{"$.", 0, true},
		{"$..A", 0, true},
		{"$.A[x]", 0, true},
		{"$.A[01]", 0, true},
		{"$.A[-1]", 0, true},
		{"$.A[0", 0, true},
		{"$.*", 0, true},
		{"$.A.B.", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			path, err := parsePath(tc.path)
			if tc.bad {
				if err == nil {
					t.Fatalf("accepted %q", tc.path)
				}
				return
			}
			if err != nil {
				t.Fatalf("rejected %q: %v", tc.path, err)
			}
			if len(path.segments) != tc.segments {
				t.Fatalf("%q parsed into %d segments, want %d", tc.path, len(path.segments), tc.segments)
			}
		})
	}
}

func TestValidateValue(t *testing.T) {
	cases := []struct {
		name string
		json string
		bad  string
	}{
		{"scalar", `"x"`, ""},
		{"structural object", `{"A": {"B": [1, true, null]}}`, ""},
		{"lit", `{"$lit": {"$anything": 1}}`, ""},
		{"ref", `{"$ref": "queue.url"}`, ""},
		{"name", `{"$name": "my-queue"}`, ""},
		{"concat", `{"$concat": ["a", {"$ref": "queue.url"}, {"$name": "q"}]}`, ""},
		{"index", `{"$index": [{"$ref": "batch.ids"}, 0]}`, ""},
		{"unknown expression", `{"$todo": "x"}`, "unknown expression"},
		{"two keys", `{"$ref": "a.b", "$name": "c"}`, "exactly one"},
		{"expression beside a member", `{"$ref": "a.b", "Name": "c"}`, "exactly one"},
		{"bad ref", `{"$ref": "queue"}`, "context path"},
		{"bad name", `{"$name": "Queue_1"}`, "kebab-case"},
		{"empty concat", `{"$concat": []}`, "non-empty"},
		{"concat with a number", `{"$concat": [1]}`, "string or an expression"},
		{"index without position", `{"$index": [{"$ref": "a.b"}]}`, "$index takes"},
		{"negative index", `{"$index": [{"$ref": "a.b"}, -1]}`, "non-negative"},
		{"nested fault is located", `{"Entries": [{"Id": "1", "Handle": {"$nope": 1}}]}`, "Entries[0].Handle"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var v any
			decoder := json.NewDecoder(strings.NewReader(tc.json))
			decoder.UseNumber()
			if err := decoder.Decode(&v); err != nil {
				t.Fatal(err)
			}
			err := validateValue(v, "params")
			if tc.bad == "" {
				if err != nil {
					t.Fatalf("rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.bad) {
				t.Fatalf("err = %v, want %q", err, tc.bad)
			}
		})
	}
}

func TestRefsAndNames(t *testing.T) {
	var v any
	if err := json.Unmarshal([]byte(`{"A": {"$ref": "queue.url"}, "B": [{"$concat": ["x", {"$ref": "dlq.arn"}]}], "C": {"$index": [{"$ref": "batch.ids"}, 1]}, "D": {"$name": "q"}, "E": {"$lit": {"$ref": "not.counted"}}}`), &v); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(refsIn(v), ","); got != "batch.ids,dlq.arn,queue.url" {
		t.Fatalf("refs = %s", got)
	}
	if got := strings.Join(namesIn(v), ","); got != "q" {
		t.Fatalf("names = %s", got)
	}
}

func TestSetMemberPath(t *testing.T) {
	params := map[string]any{"Attributes": map[string]any{"RedrivePolicy": "x"}}
	if err := setMemberPath(params, "Attributes.VisibilityTimeout", "30"); err != nil {
		t.Fatal(err)
	}
	if params["Attributes"].(map[string]any)["VisibilityTimeout"] != "30" || params["Attributes"].(map[string]any)["RedrivePolicy"] != "x" {
		t.Fatalf("params = %v", params)
	}
	if err := setMemberPath(params, "Description", "d"); err != nil || params["Description"] != "d" {
		t.Fatalf("top-level set: %v %v", err, params)
	}
	expr := map[string]any{"Attributes": map[string]any{"$ref": "x.y"}}
	if err := setMemberPath(expr, "Attributes.VisibilityTimeout", "30"); err == nil {
		t.Fatal("descended into an expression")
	}
}

func TestSnakeAndKebab(t *testing.T) {
	cases := map[string][2]string{
		"SendMessageBatch":     {"send_message_batch", "send-message-batch"},
		"GetQueueUrl":          {"get_queue_url", "get-queue-url"},
		"ListAWSServiceAccess": {"list_aws_service_access", "list-aws-service-access"},
		"Cost Explorer":        {"cost_explorer", "cost-explorer"},
		"SQS":                  {"sqs", "sqs"},
	}
	for in, want := range cases {
		if got := snake(strings.ReplaceAll(in, " ", "")); got != want[0] {
			t.Errorf("snake(%q) = %q, want %q", in, got, want[0])
		}
		if got := kebab(in); got != want[1] {
			t.Errorf("kebab(%q) = %q, want %q", in, got, want[1])
		}
	}
}
