// Event-bus rule targets: bus-to-bus forwarding.
//
// A rule may target another event bus, and AWS republishes the event there —
// same source, detail-type, detail and resources — so a rule on the downstream
// bus can match on the fields every realistic rule matches on. Forwarding the
// whole envelope nested inside Detail with an empty Source makes the target bus
// reachable only by a catch-all pattern, which is what these tests pin against.
package eventbridge_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/tests/helpers"
)

const downstreamBusARN = "arn:aws:events:us-east-1:000000000000:event-bus/downstream"

// createBus creates a custom event bus.
func createBus(t *testing.T, srv *helpers.TestServer, name string) {
	t.Helper()
	resp := ebCall(t, srv, "CreateEventBus", map[string]any{"Name": name})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CreateEventBus %q: status %d", name, resp.StatusCode)
	}
}

// putBusRuleWithTarget creates an ENABLED rule on the named bus and attaches a
// single target to it.
func putBusRuleWithTarget(t *testing.T, srv *helpers.TestServer, bus, rule, pattern string, target map[string]any) {
	t.Helper()
	resp := ebCall(t, srv, "PutRule", map[string]any{
		"Name":         rule,
		"EventBusName": bus,
		"EventPattern": pattern,
		"State":        "ENABLED",
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PutRule %q on %q: status %d", rule, bus, resp.StatusCode)
	}

	resp = ebCall(t, srv, "PutTargets", map[string]any{
		"Rule":         rule,
		"EventBusName": bus,
		"Targets":      []any{target},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PutTargets on %q: status %d", rule, resp.StatusCode)
	}
	var out struct {
		FailedEntryCount int              `json:"FailedEntryCount"`
		FailedEntries    []map[string]any `json:"FailedEntries"`
	}
	helpers.DecodeJSON(t, resp, &out)
	if out.FailedEntryCount != 0 {
		t.Fatalf("PutTargets rejected the target: %#v", out.FailedEntries)
	}
}

func TestPutEvents_busTargetRepublishesSourceAndDetailType(t *testing.T) {
	// Given: a downstream bus whose rule matches on source and detail-type —
	// the two fields a realistic rule filters on — and an upstream rule that
	// forwards to that bus
	srv := helpers.NewTestServer(t)
	queueURL := createQueueForFanout(t, srv, "bus-hop-queue")
	createBus(t, srv, "downstream")

	putBusRuleWithTarget(t, srv, "downstream", "downstream-rule",
		`{"source":["com.example.fanout"],"detail-type":["FanoutTest"]}`,
		map[string]any{"Id": "q", "Arn": "arn:aws:sqs:us-east-1:000000000000:bus-hop-queue"})

	putRuleWithTarget(t, srv, "forward-to-bus", map[string]any{"Id": "bus", "Arn": downstreamBusARN})

	// When: a matching event is published on the default bus
	putFanoutEvent(t, srv, `{"orderId":"o-bus-1"}`)

	// Then: the downstream rule matched, and the event it saw is the original —
	// not the upstream envelope nested inside a detail string
	body := receiveOneMessage(t, srv, queueURL)
	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("forwarded body is not JSON: %s", body)
	}
	if got["source"] != "com.example.fanout" {
		t.Fatalf("forwarded source = %v, want com.example.fanout: %s", got["source"], body)
	}
	if got["detail-type"] != "FanoutTest" {
		t.Fatalf("forwarded detail-type = %v, want FanoutTest: %s", got["detail-type"], body)
	}
	detail, ok := got["detail"].(map[string]any)
	if !ok {
		t.Fatalf("forwarded detail is not an object — the envelope was nested as a string: %s", body)
	}
	if detail["orderId"] != "o-bus-1" {
		t.Fatalf("forwarded detail = %#v, want the original detail", detail)
	}
}

func TestPutEvents_busCycleTerminatesAtTheHopBudget(t *testing.T) {
	// Given: two buses whose rules forward to each other on the same source —
	// a cycle that only bites now that the forwarded event keeps its source,
	// so a source-matching rule downstream actually matches
	srv := helpers.NewTestServer(t)
	createBus(t, srv, "downstream")

	putRuleWithTarget(t, srv, "forward-out", map[string]any{"Id": "b", "Arn": downstreamBusARN})
	putBusRuleWithTarget(t, srv, "downstream", "forward-back",
		`{"source":["com.example.fanout"]}`,
		map[string]any{"Id": "a", "Arn": "arn:aws:events:us-east-1:000000000000:event-bus/default"})

	// When: a matching event is published
	done := make(chan struct{})
	go func() {
		defer close(done)
		putFanoutEvent(t, srv, `{"orderId":"o-bus-cycle"}`)
	}()

	// Then: the hop budget ends the cycle rather than the stack doing it
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("PutEvents did not return — the bus cycle was not bounded")
	}
	if !anyDeliveryMentions(t, srv, "default", "hop budget") && !anyDeliveryMentions(t, srv, "downstream", "hop budget") {
		t.Fatal("no delivery reported the hop budget — the cycle ended some other way")
	}
}

// anyDeliveryMentions reports whether any recorded delivery on a bus failed
// with an error containing want.
func anyDeliveryMentions(t *testing.T, srv *helpers.TestServer, bus, want string) bool {
	t.Helper()
	resp, err := http.Get(srv.URL + "/_overcast/eventbridge/deliveries?bus=" + bus)
	if err != nil {
		t.Fatalf("GET deliveries: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Deliveries []fanoutDelivery `json:"Deliveries"`
	}
	helpers.DecodeJSON(t, resp, &out)
	for _, d := range out.Deliveries {
		if strings.Contains(d.Error, want) {
			return true
		}
	}
	return false
}

func TestPutEvents_busTargetAppliesInputTransformerToTheDetail(t *testing.T) {
	// Given: a bus target with an InputTransformer, and a downstream rule that
	// still matches on the original source
	srv := helpers.NewTestServer(t)
	queueURL := createQueueForFanout(t, srv, "bus-hop-transformed")
	createBus(t, srv, "downstream")

	putBusRuleWithTarget(t, srv, "downstream", "downstream-transformed",
		`{"source":["com.example.fanout"]}`,
		map[string]any{"Id": "q", "Arn": "arn:aws:sqs:us-east-1:000000000000:bus-hop-transformed"})

	putRuleWithTarget(t, srv, "forward-transformed", map[string]any{
		"Id":  "bus",
		"Arn": downstreamBusARN,
		"InputTransformer": map[string]any{
			"InputPathsMap": map[string]any{"order": "$.detail.orderId"},
			"InputTemplate": `{"id":"<order>"}`,
		},
	})

	// When: a matching event is published
	putFanoutEvent(t, srv, `{"orderId":"o-bus-2"}`)

	// Then: the transformed payload becomes the forwarded event's detail, and
	// the routing fields still carry the original source and detail-type
	body := receiveOneMessage(t, srv, queueURL)
	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("forwarded body is not JSON: %s", body)
	}
	if got["source"] != "com.example.fanout" || got["detail-type"] != "FanoutTest" {
		t.Fatalf("forwarded routing fields lost: %s", body)
	}
	detail, ok := got["detail"].(map[string]any)
	if !ok || detail["id"] != "o-bus-2" {
		t.Fatalf("forwarded detail = %v, want the transformer output: %s", got["detail"], body)
	}
}
