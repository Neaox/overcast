package waf

import (
	"context"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/protocol/op"
	"github.com/overcast-sh/overcast/internal/state"
)

func TestTypedOps_matchLegacyOperationRegistry(t *testing.T) {
	h := &Handler{}
	h.initOps()

	if len(h.typedOp) != len(h.ops) {
		t.Fatalf("typed ops len = %d, legacy ops len = %d", len(h.typedOp), len(h.ops))
	}
	for name := range h.ops {
		operation, ok := h.typedOp[name]
		if !ok {
			t.Fatalf("missing typed op %q", name)
		}
		if operation.Name() != name {
			t.Fatalf("typed op %q has Name() %q", name, operation.Name())
		}
	}

	for name, operation := range h.typedOp {
		if _, ok := operation.(*op.Raw); ok {
			t.Fatalf("%s registered as raw operation", name)
		}
	}
}

func TestTypedWebACLLifecyclePublishesEvents(t *testing.T) {
	bus := events.NewBus()
	t.Cleanup(bus.Stop)
	h := newHandler(&config.Config{Region: "us-east-1", AccountID: "000000000000"}, state.NewMemoryStore(), clock.New())
	h.bus = bus

	created := make(chan events.Event, 1)
	deleted := make(chan events.Event, 1)
	bus.Subscribe(events.WAFWebACLCreated, func(_ context.Context, event events.Event) { created <- event })
	bus.Subscribe(events.WAFWebACLDeleted, func(_ context.Context, event events.Event) { deleted <- event })

	response, aerr := h.createWebACLTyped(context.Background(), &createWebACLRequest{
		Name:  "edge-acl",
		Scope: "REGIONAL",
	})
	if aerr != nil {
		t.Fatalf("createWebACLTyped returned error: %v", aerr)
	}
	assertWAFEvent(t, created, "edge-acl")

	_, aerr = h.deleteWebACLTyped(context.Background(), &deleteWebACLRequest{
		ID:    response.Summary.Id,
		Name:  response.Summary.Name,
		Scope: "REGIONAL",
	})
	if aerr != nil {
		t.Fatalf("deleteWebACLTyped returned error: %v", aerr)
	}
	assertWAFEvent(t, deleted, "edge-acl")
}

func assertWAFEvent(t *testing.T, eventsCh <-chan events.Event, wantName string) {
	t.Helper()
	select {
	case event := <-eventsCh:
		payload, ok := event.Payload.(events.ResourcePayload)
		if !ok || payload.Name != wantName || payload.ARN == "" {
			t.Fatalf("unexpected WAF event payload: %#v", event.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for WAF lifecycle event")
	}
}
