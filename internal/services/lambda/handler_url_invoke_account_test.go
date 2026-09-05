package lambda

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
)

// TestBuildFunctionURLEvent_accountIDFromConfig asserts the function-URL event's
// requestContext.accountId comes from configuration.
//
// buildFunctionURLEvent sets AccountID: "000000000000" as a literal
// (handler_url_invoke.go:190). Customer code reads
// event.requestContext.accountId — it is a documented field of the payload 2.0
// shape, and handlers use it to compose ARNs and to assert which account they
// are running in — so a function deployed against a server started with
// OVERCAST_ACCOUNT_ID set sees the wrong account.
//
// This is asserted as a unit test rather than through the invoke path because
// the only integration cover for function-URL events is Docker-gated
// (TestInvokeFunctionURL_hostRouted_success in
// tests/integration/lambdadocker), so a suite run without Docker would skip
// the finding entirely.
//
// API Gateway hardcodes the same literal in the same position, at three sites:
// internal/services/apigateway/handler_execution.go:221 (REST v1 proxy), :442
// (HTTP API v2), and :498 (v2 route on the v1 payload format). Those are only
// reachable through a Lambda invocation, so they are recorded in the audit
// findings rather than covered here.
func TestBuildFunctionURLEvent_accountIDFromConfig(t *testing.T) {
	// Given: a handler configured with a non-default account ID.
	const accountID = "111122223333"
	h := &Handler{
		cfg: &config.Config{
			AccountID: accountID,
			Region:    "us-east-1",
			Hostname:  "localhost",
		},
		clk: clock.NewMock(),
	}
	req := httptest.NewRequest(http.MethodGet, "http://abc.lambda-url.us-east-1.localhost:4566/hello", nil)

	// When: the function-URL event is built for an invocation.
	event, err := h.buildFunctionURLEvent(req, "/hello", "abc")
	if err != nil {
		t.Fatalf("buildFunctionURLEvent: %v", err)
	}

	// Then: requestContext.accountId is the configured account.
	if event.RequestContext.AccountID != accountID {
		t.Errorf("requestContext.accountId = %q, want %q (from cfg.AccountID)",
			event.RequestContext.AccountID, accountID)
	}
}
