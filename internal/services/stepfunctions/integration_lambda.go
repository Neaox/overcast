package stepfunctions

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
)

// The Lambda Task integrations, in both shapes AWS offers them:
//
//	"Resource": "arn:aws:lambda:…:function:Name"   → the result is the
//	                                                 function's own output
//	"Resource": "arn:aws:states:::lambda:invoke"   → the result is the Invoke
//	                                                 API response envelope
//
// Both go through Overcast's own Lambda handler, so a Task attempt behaves
// exactly like the equivalent SDK Invoke, function errors included.

// ─── Lambda ───────────────────────────────────────────────────────────────────

// invokeLambdaDirect runs a Task whose Resource is a function ARN. The state's
// result is the function's own output, unwrapped.
func (in *interpreter) invokeLambdaDirect(ctx context.Context, functionName string, payload any) (any, *stateError) {
	outcome, serr := in.invokeFunction(ctx, functionName, payload, "RequestResponse")
	if serr != nil {
		return nil, serr
	}
	return outcome.payload, nil
}

// invokeLambdaOptimized runs `arn:aws:states:::lambda:invoke`. Its result is
// the Invoke API response envelope, which is what AWS hands the next state.
func (in *interpreter) invokeLambdaOptimized(ctx context.Context, payload any) (any, *stateError) {
	params, ok := payload.(map[string]any)
	if !ok {
		return nil, newStateError(errParameterPathFailure, "lambda:invoke requires Parameters with a FunctionName")
	}
	functionName, _ := params["FunctionName"].(string)
	if functionName == "" {
		return nil, newStateError(errParameterPathFailure, "lambda:invoke requires Parameters.FunctionName")
	}
	if qualifier, _ := params["Qualifier"].(string); qualifier != "" {
		functionName += ":" + qualifier
	}
	invocationType, _ := params["InvocationType"].(string)
	if invocationType == "" {
		invocationType = "RequestResponse"
	}
	if invocationType != "RequestResponse" && invocationType != "Event" {
		return nil, unsupportedError("the %q Lambda invocation type in a Task state", invocationType)
	}

	outcome, serr := in.invokeFunction(ctx, functionName, params["Payload"], invocationType)
	if serr != nil {
		return nil, serr
	}
	response := map[string]any{
		"StatusCode":          float64(outcome.statusCode),
		"SdkHttpMetadata":     map[string]any{"HttpStatusCode": float64(outcome.statusCode)},
		"SdkResponseMetadata": map[string]any{"RequestId": outcome.requestID},
	}
	if invocationType == "RequestResponse" {
		response["ExecutedVersion"] = "$LATEST"
		response["Payload"] = outcome.payload
	}
	return response, nil
}

type lambdaOutcome struct {
	payload    any
	statusCode int
	requestID  string
}

// invokeFunction calls Lambda's Invoke endpoint through the router.
func (in *interpreter) invokeFunction(ctx context.Context, functionName string, payload any, invocationType string) (*lambdaOutcome, *stateError) {
	body := []byte("{}")
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, newStateError(errRuntime, "the Lambda payload could not be encoded: %v", err)
		}
		body = encoded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"/2015-03-31/functions/"+url.PathEscape(functionName)+"/invocations", bytes.NewReader(body))
	if err != nil {
		return nil, newStateError(errRuntime, "%v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Amz-Invocation-Type", invocationType)
	req.Header.Set("X-Overcast-Region", in.region)
	rec := httptest.NewRecorder()
	in.handler.router.ServeHTTP(rec, req)

	responseBody := rec.Body.Bytes()
	if rec.Code >= 400 {
		code, message := decodeJSONError(responseBody)
		return nil, &stateError{name: "Lambda." + code, cause: message}
	}
	if functionError := rec.Header().Get("X-Amz-Function-Error"); functionError != "" {
		return nil, &stateError{name: lambdaFunctionErrorName(responseBody), cause: string(responseBody)}
	}

	outcome := &lambdaOutcome{
		statusCode: rec.Code,
		requestID:  rec.Header().Get("x-amzn-RequestId"),
	}
	if len(bytes.TrimSpace(responseBody)) > 0 {
		if err := json.Unmarshal(responseBody, &outcome.payload); err != nil {
			// A function may legitimately return a non-JSON body; surface it
			// verbatim rather than failing the state.
			outcome.payload = string(responseBody)
		}
	}
	return outcome, nil
}

// lambdaFunctionErrorName derives the ASL error name from a Lambda function
// error payload. AWS uses the payload's errorType, falling back to
// Lambda.Unknown for an unhandled error with no type.
func lambdaFunctionErrorName(body []byte) string {
	var payload struct {
		ErrorType string `json:"errorType"`
	}
	if json.Unmarshal(body, &payload) == nil && payload.ErrorType != "" {
		return payload.ErrorType
	}
	return "Lambda.Unknown"
}
