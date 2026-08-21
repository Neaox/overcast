package cloudformation

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTeardownRec builds the response a dispatch helper hands back, so the
// classifier can be exercised against the exact shapes the emulator's services
// answer with.
func newTeardownRec(code int, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	rec.Code = code
	rec.Body = bytes.NewBufferString(body)
	return rec
}

// TestTeardownError pins the rule every resource handler's Delete now follows:
// a resource that is already gone is a successful teardown, and anything else
// is reported so the stack can decide what to do about it.
//
// The absent-resource cases are drawn from what the emulator's own services
// answer, across all four protocols, because that is what the classifier has to
// recognise — and because AWS does not use one shape for it. Athena answers
// HTTP 404 under a generic code, RDS and ElastiCache answer HTTP 400 under a
// specific one, and Auto Scaling answers a plain ValidationError whose message
// is the only signal that the group is gone.
func TestTeardownError(t *testing.T) {
	absent := []struct {
		name string
		rec  *httptest.ResponseRecorder
	}{
		{"REST 404", newTeardownRec(http.StatusNotFound,
			`{"__type":"ResourceNotFoundException","message":"Function not found: fn"}`)},
		{"JSON 400 ResourceNotFoundException", newTeardownRec(http.StatusBadRequest,
			`{"__type":"ResourceNotFoundException","message":"Stream not found"}`)},
		{"JSON namespaced __type", newTeardownRec(http.StatusBadRequest,
			`{"__type":"com.amazonaws.glue#EntityNotFoundException","message":"Database not found"}`)},
		{"SQS NonExistentQueue", newTeardownRec(http.StatusBadRequest,
			`{"__type":"AWS.SimpleQueueService.NonExistentQueue","message":"The specified queue does not exist."}`)},
		{"Query XML NotFoundFault", newTeardownRec(http.StatusBadRequest,
			`<ErrorResponse><Error><Code>DBSubnetGroupNotFoundFault</Code>`+
				`<Message>DBSubnetGroup not found</Message></Error></ErrorResponse>`)},
		{"IAM XML NoSuchEntity", newTeardownRec(http.StatusNotFound,
			`<ErrorResponse><Error><Code>NoSuchEntity</Code><Message>gone</Message></Error></ErrorResponse>`)},
		{"Auto Scaling ValidationError naming an absent group", newTeardownRec(http.StatusBadRequest,
			`<ErrorResponse><Error><Code>ValidationError</Code>`+
				`<Message>AutoScalingGroup name not found - AutoScalingGroup asg not found</Message>`+
				`</Error></ErrorResponse>`)},
		{"Athena 404 under a generic code", newTeardownRec(http.StatusNotFound,
			`{"__type":"InvalidRequestException","message":"WorkGroup wg not found"}`)},
		{"SES TemplateDoesNotExist", newTeardownRec(http.StatusBadRequest,
			`<ErrorResponse><Error><Code>TemplateDoesNotExist</Code>`+
				`<Message>Template t does not exist</Message></Error></ErrorResponse>`)},
	}
	for _, tc := range absent {
		t.Run("already gone: "+tc.name, func(t *testing.T) {
			if err := teardownError("DeleteThing", tc.rec, errors.New("HTTP error")); err != nil {
				t.Fatalf("got %v, want nil — a resource that is already gone is a successful teardown", err)
			}
		})
	}

	t.Run("a successful dispatch reports nothing", func(t *testing.T) {
		if err := teardownError("DeleteThing", newTeardownRec(http.StatusOK, "{}"), nil); err != nil {
			t.Fatalf("got %v, want nil", err)
		}
	})

	t.Run("a service failure is reported, naming the operation", func(t *testing.T) {
		rec := newTeardownRec(http.StatusInternalServerError,
			`{"__type":"InternalFailure","message":"boom"}`)
		err := teardownError("DeleteLogGroup", rec, errors.New("HTTP 500: boom"))
		if err == nil {
			t.Fatal("got nil, want the failure reported — the resource is still standing")
		}
		if !strings.Contains(err.Error(), "DeleteLogGroup") {
			t.Errorf("error %q does not name the operation", err)
		}
		if errors.Is(err, errDeletionBlocked) {
			t.Errorf("error %q wraps errDeletionBlocked; only a refusal by the resource may do that", err)
		}
	})

	t.Run("a refusal by the resource is reported", func(t *testing.T) {
		rec := newTeardownRec(http.StatusBadRequest,
			`<ErrorResponse><Error><Code>ResourceInUse</Code>`+
				`<Message>You cannot delete an AutoScalingGroup while there are instances</Message>`+
				`</Error></ErrorResponse>`)
		if err := teardownError("DeleteAutoScalingGroup", rec, errors.New("HTTP 400")); err == nil {
			t.Fatal("got nil, want the refusal reported")
		}
	})

	t.Run("a request that never reached the router is reported", func(t *testing.T) {
		if err := teardownError("DeleteThing", nil, errors.New("bad request")); err == nil {
			t.Fatal("got nil, want the failure reported")
		}
	})
}

// newTeardownRollbackFixture seeds a one-resource stack whose log group the
// rollback has to delete, wired to a router the test controls.
func newTeardownRollbackFixture(t *testing.T, router http.Handler) (*provisioner, *Stack) {
	t.Helper()
	p := newProvisionerTestFixture(t)
	p.initRouter(router)
	stack := &Stack{
		StackName: "teardown-stack",
		StackID:   "arn:aws:cloudformation:us-east-1:000000000000:stack/teardown-stack/1111",
		Region:    "us-east-1",
		Resources: []StackResource{{
			LogicalID:  "Logs",
			PhysicalID: "/aws/overcast/teardown",
			Type:       "AWS::Logs::LogGroup",
			Status:     ResourceCreateComplete,
		}},
	}
	return p, stack
}

// A rollback that cannot delete what the failed create built must say so. Real
// CloudFormation records DELETE_FAILED and leaves the stack ROLLBACK_FAILED,
// which is the signal an operator needs: the resource is still standing and
// the stack will not clean itself up.
func TestCreateRollback_failsWhenAResourceDeleteFails(t *testing.T) {
	// Given: a log group whose service refuses to delete it
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Amz-Target") == "Logs_20140328.DeleteLogGroup" {
			w.Header().Set("Content-Type", "application/x-amz-json-1.0")
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"__type":"InternalFailure","message":"log group delete failed"}`)) //nolint:errcheck
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint:errcheck
	})
	p, stack := newTeardownRollbackFixture(t, router)

	// When: the create rolls back
	p.rollbackCreate(context.Background(), stack, &resolveContext{Region: "us-east-1", StackName: stack.StackName},
		"resource creation failed", createRollbackOptions{})

	// Then: the stack reports the rollback it could not finish
	if stack.Status != StatusRollbackFailed {
		t.Errorf("stack status = %q, want %q — the log group is still standing", stack.Status, StatusRollbackFailed)
	}
	if len(stack.Resources) != 1 {
		t.Fatalf("stack resources = %+v, want the log group left in the list", stack.Resources)
	}
	if got := stack.Resources[0].Status; got != ResourceDeleteFailed {
		t.Errorf("resource status = %q, want %q", got, ResourceDeleteFailed)
	}
	if reason := stack.Resources[0].StatusReason; !strings.Contains(reason, "DeleteLogGroup") {
		t.Errorf("status reason = %q, want it to name the operation that failed", reason)
	}
}

// DeleteStack is deliberately not symmetric with rollback, and this pins the
// asymmetry so that it is changed on purpose rather than by accident.
//
// A teardown the operator asked for stops only for a resource that refuses to
// be deleted (errDeletionBlocked); a call that could not be made is recorded
// and the teardown moves on. That distinction is what deleteResource exists to
// draw, and it cannot be collapsed into "any error fails the delete" until
// every handler has the absent-resource allowance the ones here now have —
// around sixty still report a plain error for a resource that is merely gone,
// and making DeleteStack fatal today would wedge a stack over each of them.
func TestDeleteStack_completesWhenAResourceDeleteFails(t *testing.T) {
	// Given: a log group whose service cannot be reached
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"__type":"InternalFailure","message":"log group delete failed"}`)) //nolint:errcheck
	})
	p, stack := newTeardownRollbackFixture(t, router)
	if err := p.store.putStack(context.Background(), stack); err != nil {
		t.Fatalf("putStack: %v", err)
	}

	// When: the stack is deleted
	p.deleteStackResourcesCtx(context.Background(), stack)

	// Then: the teardown finishes rather than stranding the stack
	if stack.Status != StatusDeleteComplete {
		t.Errorf("stack status = %q, want %q — an unreachable service must not wedge a deliberate teardown",
			stack.Status, StatusDeleteComplete)
	}
}

// The other half of the rule: a resource that is already gone is a successful
// teardown. Nothing here may wedge a rollback over a resource that no longer
// exists — that is the behaviour the blanket error-swallowing was protecting,
// and it has to survive the handlers becoming truthful.
func TestCreateRollback_completesWhenTheResourceIsAlreadyGone(t *testing.T) {
	// Given: a log group the service says does not exist
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"__type":"ResourceNotFoundException","message":"The specified log group does not exist."}`)) //nolint:errcheck
	})
	p, stack := newTeardownRollbackFixture(t, router)

	// When: the create rolls back
	p.rollbackCreate(context.Background(), stack, &resolveContext{Region: "us-east-1", StackName: stack.StackName},
		"resource creation failed", createRollbackOptions{})

	// Then: the rollback completes
	if stack.Status != StatusRollbackComplete {
		t.Errorf("stack status = %q, want %q — an absent resource is a successful teardown",
			stack.Status, StatusRollbackComplete)
	}
	if got := stack.Resources[0].Status; got != ResourceDeleteComplete {
		t.Errorf("resource status = %q, want %q", got, ResourceDeleteComplete)
	}
}
