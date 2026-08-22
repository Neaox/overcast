package lambda

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// A handler error reported through POST /runtime/invocation/{id}/error is what
// AWS surfaces as X-Amz-Function-Error: Unhandled on the Invoke response — for
// every error type a modern runtime reports, not only Runtime.ExitError. The
// emulator used to default to "Handled", which no current AWS runtime produces
// for a thrown exception; the compat suites' InvokeWithError caught it.
func TestHandleInvocationAction_errorIsUnhandled(t *testing.T) {
	for _, errorType := range []string{"Error", "RuntimeError", "Runtime.ExitError", ""} {
		t.Run("type="+errorType, func(t *testing.T) {
			// Given: one in-flight invocation waiting on its result channel.
			s := &RuntimeAPIServer{pending: map[string]*pendingInvocation{}, logger: zap.NewNop()}
			ch := make(chan invokeResponse, 1)
			s.pending["req-1"] = &pendingInvocation{RequestID: "req-1", ResultCh: ch}

			// When: the runtime reports the handler's exception.
			req := httptest.NewRequest(http.MethodPost, "/2018-06-01/runtime/invocation/req-1/error",
				strings.NewReader(`{"errorMessage":"compat: intentional failure","errorType":"Error"}`))
			if errorType != "" {
				req.Header.Set("Lambda-Runtime-Function-Error-Type", errorType)
			}
			rec := httptest.NewRecorder()
			s.handleInvocationAction(rec, req)

			// Then: the invoker sees FunctionError=Unhandled with the error document.
			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want 202", rec.Code)
			}
			resp := <-ch
			if resp.FunctionError != "Unhandled" {
				t.Fatalf("FunctionError = %q, want Unhandled", resp.FunctionError)
			}
			if !strings.Contains(string(resp.ErrorPayload), "errorMessage") {
				t.Fatalf("ErrorPayload = %s, want the error document", resp.ErrorPayload)
			}
		})
	}
}
