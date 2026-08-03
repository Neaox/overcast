package eventtarget

import (
	"encoding/json"
	"testing"
)

func testEvent(t *testing.T) map[string]any {
	t.Helper()
	var event map[string]any
	raw := `{
	  "version":"0",
	  "id":"e-1",
	  "detail-type":"OrderCreated",
	  "source":"com.example.orders",
	  "resources":["arn:aws:events:us-east-1:0:rule/default/r"],
	  "detail":{"orderId":"o-1","total":42,"items":["a","b"]}
	}`
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return event
}

func TestSelectPath(t *testing.T) {
	event := testEvent(t)

	// Given/When/Then: the supported JSONPath subset resolves.
	cases := map[string]any{
		"$.detail.orderId":  "o-1",
		"$.source":          "com.example.orders",
		"$.detail-type":     "OrderCreated",
		"$.detail.items[1]": "b",
		"$.resources[0]":    "arn:aws:events:us-east-1:0:rule/default/r",
	}
	for path, want := range cases {
		got, ok := SelectPath(event, path)
		if !ok {
			t.Fatalf("SelectPath(%q) selected nothing", path)
		}
		if got != want {
			t.Fatalf("SelectPath(%q) = %#v, want %#v", path, got, want)
		}
	}

	// The root selects the whole document.
	if got, ok := SelectPath(event, "$"); !ok || got == nil {
		t.Fatal("SelectPath($) should select the whole event")
	}

	// Paths that do not resolve report that rather than yielding a zero value,
	// so a caller can fail the delivery instead of sending an empty payload.
	for _, path := range []string{"", "detail", "$.missing", "$.detail.missing", "$.detail.items[9]", "$.detail.orderId.deeper", "$..detail", "$.detail.items[x]"} {
		if _, ok := SelectPath(event, path); ok {
			t.Fatalf("SelectPath(%q) unexpectedly selected a value", path)
		}
	}
}

func TestInputTransformer_Render(t *testing.T) {
	event := testEvent(t)

	// Given: a template mixing a string selection, a numeric selection, an
	// array selection and a reserved variable.
	it := InputTransformer{
		InputPathsMap: map[string]string{
			"order": "$.detail.orderId",
			"total": "$.detail.total",
			"items": "$.detail.items",
		},
		InputTemplate: `{"id":"<order>","total":<total>,"items":<items>,"rule":"<aws.events.rule-name>"}`,
	}

	// When: the template is rendered
	got, err := it.Render(event, map[string]string{"aws.events.rule-name": "orders"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	// Then: strings substitute unquoted (the template quotes them) and other
	// values substitute as JSON, matching AWS.
	want := `{"id":"o-1","total":42,"items":["a","b"],"rule":"orders"}`
	if string(got) != want {
		t.Fatalf("Render = %s, want %s", got, want)
	}
}

func TestInputTransformer_missingPathRendersEmpty(t *testing.T) {
	it := InputTransformer{
		InputPathsMap: map[string]string{"missing": "$.detail.nope"},
		InputTemplate: `{"v":"<missing>"}`,
	}
	got, err := it.Render(testEvent(t), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if string(got) != `{"v":""}` {
		t.Fatalf("Render = %s", got)
	}
}

func TestInputTransformer_requiresTemplate(t *testing.T) {
	empty := InputTransformer{}
	if _, err := empty.Render(testEvent(t), nil); err == nil {
		t.Fatal("expected an error without an InputTemplate")
	}
}

func TestInputTransformer_nonJSONTemplateIsPassedThrough(t *testing.T) {
	// AWS does not require the rendered template to be JSON.
	it := InputTransformer{
		InputPathsMap: map[string]string{"order": "$.detail.orderId"},
		InputTemplate: "order <order> received",
	}
	got, err := it.Render(testEvent(t), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if string(got) != "order o-1 received" {
		t.Fatalf("Render = %s", got)
	}
}
