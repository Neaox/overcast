package lambda_test

// resource_policy_enforcement_test.go — what
// OVERCAST_ENFORCE_LAMBDA_RESOURCE_POLICY does and, as importantly, what it
// does not.
//
// The knob gates invocations Overcast originates on another AWS service's
// behalf; the callers' own tests cover those. A direct client Invoke is left
// alone in both settings, because Overcast accepts credentials without
// validating them (AGENTS.md § Non-goals) and so has no caller identity to
// authorise. Gating it would refuse every SDK call ever made against the
// emulator.

import (
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

func TestInvoke_directCallIsNotGatedByTheResourcePolicy(t *testing.T) {
	// Given: enforcement on and a function with no resource policy at all
	srv := helpers.NewTestServer(t, helpers.WithEnforceLambdaResourcePolicy(true))
	createFunction(t, srv, "direct-invoke-fn")

	// When: a client invokes it directly, as any SDK does
	resp := invokeFunction(t, srv, "direct-invoke-fn", map[string]any{"hello": "world"})
	defer resp.Body.Close()

	// Then: it is not refused for want of a permission
	if resp.StatusCode == http.StatusForbidden {
		t.Fatalf("a direct Invoke was refused by the resource policy: %s", helpers.ReadBody(t, resp))
	}
}

func TestGetPolicy_enforcementDoesNotChangeTheStoredDocument(t *testing.T) {
	// Given: enforcement on and a permission added the usual way
	srv := helpers.NewTestServer(t, helpers.WithEnforceLambdaResourcePolicy(true))
	createFunction(t, srv, "enforced-policy-fn")
	addResp := doJSON(t, http.MethodPost,
		lambdaURL(srv, "/functions/enforced-policy-fn/policy"), map[string]any{
			"StatementId": "s3invoke", "Action": "lambda:InvokeFunction",
			"Principal": "s3.amazonaws.com", "SourceArn": "arn:aws:s3:::some-bucket",
		})
	addResp.Body.Close()
	helpers.AssertStatus(t, addResp, http.StatusCreated)

	// When: the policy is read back
	getResp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/enforced-policy-fn/policy"), nil)
	defer getResp.Body.Close()

	// Then: the document is exactly what it is with the knob off — enforcement
	// reads the statements, it never rewrites them
	helpers.AssertStatus(t, getResp, http.StatusOK)
	var envelope resourcePolicyEnvelope
	decodeJSON(t, getResp, &envelope)
	assertSamePolicyDocument(t, envelope.Policy,
		`{"Version":"2012-10-17","Id":"default","Statement":[{"Sid":"s3invoke","Effect":"Allow",`+
			`"Principal":{"Service":"s3.amazonaws.com"},"Action":"lambda:InvokeFunction",`+
			`"Resource":"arn:aws:lambda:us-east-1:000000000000:function:enforced-policy-fn",`+
			`"Condition":{"ArnLike":{"AWS:SourceArn":"arn:aws:s3:::some-bucket"}}}]}`)
}
