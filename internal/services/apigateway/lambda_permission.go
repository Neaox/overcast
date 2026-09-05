package apigateway

// lambda_permission.go — the resource-policy check an API Gateway Lambda
// integration makes before it invokes.
//
// AWS grants API Gateway the right to invoke a function through the function's
// own resource-based policy: the console adds the statement for you, and every
// other route (CLI, CloudFormation, CDK, an SDK) leaves it to be added by hand.
// A missing statement is the single most common reason a working integration
// answers 500 — "If the Lambda API rejects the invocation request, API Gateway
// returns a 500 error code… the body of the response from API Gateway is
// {"message": "Internal server error"}" — so Overcast answers exactly that,
// rather than the 502 it uses for a function that ran and failed.
//
// Enforcement is opt-in: without OVERCAST_ENFORCE_LAMBDA_RESOURCE_POLICY the
// authorizer answers nil before doing any work.
//
// https://docs.aws.amazon.com/apigateway/latest/developerguide/http-api-troubleshooting-lambda.html
// https://docs.aws.amazon.com/lambda/latest/dg/services-apigateway-errors.html

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/middleware"
	"github.com/overcast-sh/overcast/internal/protocol"
)

// apigatewayServicePrincipal is the principal an API Gateway integration
// invokes a Lambda function as, and the value `add-permission --principal` is
// given for one.
const apigatewayServicePrincipal = "apigateway.amazonaws.com"

// defaultStageName is the stage an HTTP API serves on when the invoke URL names
// none.
const defaultStageName = "$default"

// authorizeLambdaIntegration reports whether this integration may invoke the
// function. It writes API Gateway's own configuration-error response and
// returns false when it may not, so a caller reads as
// `if !h.authorizeLambdaIntegration(...) { return }`.
func (h *Handler) authorizeLambdaIntegration(w http.ResponseWriter, r *http.Request, functionName, apiID, resourcePath string) bool {
	if h.lambdaAuth == nil {
		return true
	}
	sourceARN := h.executeAPISourceARN(r, apiID, resourcePath)
	aerr := h.lambdaAuth.AuthorizeServiceInvoke(r.Context(), functionName, apigatewayServicePrincipal, sourceARN, h.accountID())
	if aerr == nil {
		return true
	}
	// This is the line the AWS execution log carries for the same failure, and
	// the only place the reason is visible: the client gets a bare 500.
	h.log.WithRecorder(r.Context()).Warn("execution failed due to configuration error: invalid permissions on Lambda function",
		zap.String("function", functionName),
		zap.String("sourceArn", sourceARN),
	)
	writeGatewayError(w, http.StatusInternalServerError, "Internal server error")
	return false
}

// executeAPISourceARN renders the aws:SourceArn an integration contributes:
// arn:aws:execute-api:{region}:{account}:{api-id}/{stage}/{METHOD}/{resource-path}.
//
// https://docs.aws.amazon.com/apigateway/latest/developerguide/api-gateway-control-access-using-iam-policies-to-invoke-api.html
func (h *Handler) executeAPISourceARN(r *http.Request, apiID, resourcePath string) string {
	stage := chi.URLParam(r, "stageName")
	if stage == "" {
		stage = defaultStageName
	}
	return protocol.ARN(
		middleware.RegionFromContext(r.Context(), h.cfg.Region),
		h.accountID(),
		"execute-api",
		apiID+"/"+stage+"/"+r.Method+"/"+strings.TrimPrefix(resourcePath, "/"),
	)
}
