package cloudformation

// internal_dispatch_test.go — the internalRequest / internalJSON /
// internalQuery helpers that dispatch CloudFormation resource operations to
// the emulator router. Every dispatch must propagate the parent request ID so
// child traces link back to the CloudFormation request that triggered them.

import (
	"net/http"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/trace"
)

// parentCapturingRouter records the X-Overcast-Parent-Request-Id header of
// the last request it served.
type parentCapturingRouter struct {
	parentID string
}

func (h *parentCapturingRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.parentID = r.Header.Get("X-Overcast-Parent-Request-Id")
	w.WriteHeader(http.StatusOK)
}

func TestInternalDispatchPropagatesParentRequestID(t *testing.T) {
	// Given a context carrying a trace recorder, When a CloudFormation
	// resource handler dispatches through any internal helper, Then the
	// child request carries the parent request ID for hop linking.
	rec := trace.NewRecorder("parent-req-1", time.Now(), "POST", "/", "host", "", http.Header{})
	ctx := trace.ContextWithRecorder(t.Context(), rec)

	t.Run("internalRequest", func(t *testing.T) {
		router := &parentCapturingRouter{}
		if _, err := internalRequest(ctx, router, "us-east-1", http.MethodPut, "/my-bucket", "", nil); err != nil {
			t.Fatalf("internalRequest: %v", err)
		}
		if router.parentID != "parent-req-1" {
			t.Errorf("expected parent request ID parent-req-1, got %q", router.parentID)
		}
	})

	t.Run("internalJSON", func(t *testing.T) {
		router := &parentCapturingRouter{}
		if _, err := internalJSON(ctx, router, "us-east-1", "AmazonSQS.CreateQueue", map[string]any{"QueueName": "q"}); err != nil {
			t.Fatalf("internalJSON: %v", err)
		}
		if router.parentID != "parent-req-1" {
			t.Errorf("expected parent request ID parent-req-1, got %q", router.parentID)
		}
	})

	// internalGET is the one that could most easily have been written as a
	// second dispatch implementation rather than another entry point onto
	// internalCall — the emulator-only endpoint it reaches shares no shape with
	// the JSON and Query calls above. Asserting it here is what keeps it honest.
	t.Run("internalGET", func(t *testing.T) {
		router := &parentCapturingRouter{}
		if _, err := internalGET(ctx, router, "ecs", "us-east-1", "/_overcast/ecs/tasks/abc/logs/app"); err != nil {
			t.Fatalf("internalGET: %v", err)
		}
		if router.parentID != "parent-req-1" {
			t.Errorf("expected parent request ID parent-req-1, got %q", router.parentID)
		}
	})

	t.Run("internalQuery", func(t *testing.T) {
		router := &parentCapturingRouter{}
		if _, err := internalQuery(ctx, router, "us-east-1", map[string]string{"Action": "CreateTopic"}); err != nil {
			t.Fatalf("internalQuery: %v", err)
		}
		if router.parentID != "parent-req-1" {
			t.Errorf("expected parent request ID parent-req-1, got %q", router.parentID)
		}
	})
}

func TestInternalDispatchNoRecorderNoParentHeader(t *testing.T) {
	// Given a context without a trace recorder (debug off), When dispatching,
	// Then no parent header is set.
	router := &parentCapturingRouter{}
	if _, err := internalRequest(t.Context(), router, "us-east-1", http.MethodPut, "/my-bucket", "", nil); err != nil {
		t.Fatalf("internalRequest: %v", err)
	}
	if router.parentID != "" {
		t.Errorf("expected no parent request ID, got %q", router.parentID)
	}
}
