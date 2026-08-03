package stepfunctions

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"
)

// Task resource integrations.
//
// Every integration is dispatched through Overcast's own router, so a Task
// state exercises exactly the same handler an SDK call would — no second,
// divergent code path. What is not in the table below fails the execution with
// States.Runtime naming the resource, which is neither retriable nor catchable
// (see errorMatches): a workflow can never silently skip a step Overcast
// cannot run.

// taskIntegration is a parsed Task Resource ARN.
type taskIntegration struct {
	// service is the history-event resourceType: "lambda", "sqs", "sns",
	// "dynamodb", "states".
	service string
	// action is the history-event resource: "invoke", "sendMessage", …
	action string
	// pattern is the service-integration pattern suffix: "", "sync",
	// "sync:2" or "waitForTaskToken".
	pattern string
	// functionName is set for a Task whose Resource is a Lambda function ARN
	// rather than an optimized `arn:aws:states:::lambda:invoke` integration.
	functionName string
}

// direct reports whether the Resource named a Lambda function ARN directly,
// which AWS records with LambdaFunction* history events rather than Task*.
func (t taskIntegration) direct() bool { return t.functionName != "" }

// parseTaskResource classifies a Task state's Resource ARN.
func parseTaskResource(resource string) (taskIntegration, *stateError) {
	trimmed := strings.TrimSpace(resource)
	if trimmed == "" {
		return taskIntegration{}, newStateError(errRuntime, "Task Resource is empty")
	}
	parts := strings.Split(trimmed, ":")
	if len(parts) < 6 || parts[0] != "arn" {
		return taskIntegration{}, unsupportedError("Task Resource %q — it is not an ARN", resource)
	}
	switch parts[2] {
	case "lambda":
		// arn:aws:lambda:<region>:<account>:function:<name>[:<qualifier>]
		if parts[5] != "function" || len(parts) < 7 {
			return taskIntegration{}, unsupportedError("Lambda Task Resource %q — only function ARNs are interpreted", resource)
		}
		name := parts[6]
		if len(parts) > 7 {
			name = parts[6] + ":" + parts[7]
		}
		return taskIntegration{service: "lambda", action: "invoke", functionName: name}, nil
	case "states":
		// arn:aws:states:::<service>:<action>[.<pattern>]
		if parts[3] != "" || parts[4] != "" {
			if parts[5] == "activity" {
				return taskIntegration{}, unsupportedError("Activity tasks (%q) — they need GetActivityTask/SendTaskSuccess, which Overcast does not register", resource)
			}
			return taskIntegration{}, unsupportedError("Task Resource %q", resource)
		}
		if len(parts) < 7 {
			return taskIntegration{}, unsupportedError("Task Resource %q", resource)
		}
		service := parts[5]
		action := parts[6]
		if service == "aws-sdk" {
			return taskIntegration{}, unsupportedError("AWS SDK service integrations (%q) — only the optimized lambda/sqs/sns/dynamodb/states integrations are interpreted", resource)
		}
		pattern := ""
		if dot := strings.Index(action, "."); dot >= 0 {
			pattern = action[dot+1:]
			action = action[:dot]
		}
		// `.sync:2` arrives as a trailing ARN segment because of the colon.
		if len(parts) > 7 && pattern != "" {
			pattern = pattern + ":" + strings.Join(parts[7:], ":")
		}
		return taskIntegration{service: service, action: action, pattern: pattern}, nil
	}
	return taskIntegration{}, unsupportedError("Task Resource %q", resource)
}

// runTask executes one attempt of a Task state.
func (in *interpreter) runTask(ctx context.Context, name string, state *aslState, effective any, ctxObj map[string]any) (any, *stateError) {
	integration, serr := parseTaskResource(state.Resource)
	if serr != nil {
		return nil, serr
	}
	if integration.pattern == "waitForTaskToken" {
		return nil, unsupportedError(
			"the .waitForTaskToken service-integration pattern — it needs SendTaskSuccess/SendTaskFailure/SendTaskHeartbeat, which Overcast does not register (state %q)", name)
	}

	payload := effective
	if len(state.Parameters) > 0 {
		rendered, err := renderPayloadTemplate(state.Parameters, effective, ctxObj)
		if err != nil {
			return nil, newStateError(errParameterPathFailure, "%s", err.Error())
		}
		payload = rendered
	}
	payloadJSON, encErr := encodeJSON(payload)
	if encErr != nil {
		return nil, newStateError(errRuntime, "%s", encErr.Error())
	}

	timeout, serr := taskTimeout(state, effective, ctxObj)
	if serr != nil {
		return nil, serr
	}
	in.recordTaskScheduled(integration, state.Resource, payloadJSON, timeout)
	in.recordTaskStarted(integration)

	// The Task's own budget, on top of the execution's. Deriving a context is
	// what makes it real rather than decorative: every integration dispatches
	// through the router with this context, so an over-running Lambda invoke
	// or a `.sync` child execution is actually interrupted. The deadline is
	// wall-clock for the same reason the execution budget in runExecution is —
	// a context has no clock to inject.
	taskCtx := ctx
	if timeout != nil {
		var cancel context.CancelFunc
		taskCtx, cancel = context.WithTimeout(ctx, time.Duration(*timeout)*time.Second)
		defer cancel()
	}

	result, serr := in.dispatchTask(taskCtx, integration, payload)
	if serr != nil {
		// Whatever the interrupted integration reported, a failure after the
		// Task's own deadline is States.Timeout — the error name AWS raises,
		// which Retry/Catch can match and which recordTaskFailed turns into a
		// TaskTimedOut event. The parent context is checked first so an
		// execution-budget unwind or a StopExecution is not relabelled.
		if timeout != nil && ctx.Err() == nil && taskCtx.Err() != nil {
			serr = newStateError(errTimeout, "the Task state %q exceeded its TimeoutSeconds of %d", name, *timeout)
		}
		in.recordTaskFailed(integration, serr)
		return nil, serr
	}
	in.recordTaskSucceeded(integration, result)
	return result, nil
}

// taskTimeout resolves a Task state's per-attempt budget from TimeoutSeconds
// or TimeoutSecondsPath, in seconds. nil means the state declared none, which
// on AWS leaves only the execution's own timeout — Overcast reads it as the
// same thing rather than inventing a default.
func taskTimeout(state *aslState, effective any, ctxObj map[string]any) (*int64, *stateError) {
	if state.TimeoutSecondsPath != "" {
		value, err := selectPath(effective, ctxObj, state.TimeoutSecondsPath)
		if err != nil {
			return nil, newStateError(errRuntime, "%s", err.Error())
		}
		seconds, ok := toNumber(value)
		if !ok || seconds <= 0 {
			return nil, newStateError(errRuntime, "TimeoutSecondsPath %q did not resolve to a positive number", state.TimeoutSecondsPath)
		}
		resolved := int64(seconds)
		return &resolved, nil
	}
	if state.TimeoutSeconds > 0 {
		seconds := int64(state.TimeoutSeconds)
		return &seconds, nil
	}
	return nil, nil
}

// dispatchTask routes a Task to the integration that runs it.
func (in *interpreter) dispatchTask(ctx context.Context, integration taskIntegration, payload any) (any, *stateError) {
	if in.handler.router == nil {
		return nil, newStateError(errRuntime,
			"Overcast's Step Functions service has no router wired, so Task states cannot reach other services")
	}
	if integration.direct() {
		return in.invokeLambdaDirect(ctx, integration.functionName, payload)
	}
	switch integration.service {
	case "lambda":
		if integration.action != "invoke" {
			return nil, unsupportedError("the lambda:%s integration", integration.action)
		}
		if integration.pattern != "" {
			return nil, unsupportedError("the lambda:invoke.%s service-integration pattern", integration.pattern)
		}
		return in.invokeLambdaOptimized(ctx, payload)
	case "sqs":
		if integration.action != "sendMessage" || integration.pattern != "" {
			return nil, unsupportedError("the sqs:%s%s integration — only sqs:sendMessage is interpreted", integration.action, patternSuffix(integration.pattern))
		}
		return in.sendSQSMessage(ctx, payload)
	case "sns":
		if integration.action != "publish" || integration.pattern != "" {
			return nil, unsupportedError("the sns:%s%s integration — only sns:publish is interpreted", integration.action, patternSuffix(integration.pattern))
		}
		return in.publishSNS(ctx, payload)
	case "dynamodb":
		return in.invokeDynamoDB(ctx, integration, payload)
	case "states":
		if integration.action != "startExecution" {
			return nil, unsupportedError("the states:%s integration — only states:startExecution is interpreted", integration.action)
		}
		return in.startNestedExecution(ctx, integration, payload)
	}
	return nil, unsupportedError("the %s:%s%s service integration", integration.service, integration.action, patternSuffix(integration.pattern))
}

func patternSuffix(pattern string) string {
	if pattern == "" {
		return ""
	}
	return "." + pattern
}

// ─── SQS ──────────────────────────────────────────────────────────────────────

func (in *interpreter) sendSQSMessage(ctx context.Context, payload any) (any, *stateError) {
	params, ok := payload.(map[string]any)
	if !ok {
		return nil, newStateError(errParameterPathFailure, "sqs:sendMessage requires Parameters with QueueUrl and MessageBody")
	}
	if _, present := params["QueueUrl"]; !present {
		return nil, newStateError(errParameterPathFailure, "sqs:sendMessage requires Parameters.QueueUrl")
	}
	body := map[string]any{}
	for key, value := range params {
		body[key] = value
	}
	// ASL lets MessageBody be any JSON value; the SQS API takes a string.
	if messageBody, present := body["MessageBody"]; present {
		if _, isString := messageBody.(string); !isString {
			encoded, err := json.Marshal(messageBody)
			if err != nil {
				return nil, newStateError(errRuntime, "MessageBody could not be encoded: %v", err)
			}
			body["MessageBody"] = string(encoded)
		}
	}
	return in.invokeTarget(ctx, "AmazonSQS.SendMessage", body, "Sqs")
}

// ─── SNS ──────────────────────────────────────────────────────────────────────

// snsScalarParameters are the sns:publish parameters Overcast forwards. SNS
// speaks the AWS Query protocol, so structured parameters (MessageAttributes)
// would need Query member encoding — they fail loudly instead of being dropped.
var snsScalarParameters = map[string]bool{
	"TopicArn": true, "TargetArn": true, "PhoneNumber": true,
	"Message": true, "Subject": true, "MessageStructure": true,
	"MessageGroupId": true, "MessageDeduplicationId": true,
}

func (in *interpreter) publishSNS(ctx context.Context, payload any) (any, *stateError) {
	params, ok := payload.(map[string]any)
	if !ok {
		return nil, newStateError(errParameterPathFailure, "sns:publish requires Parameters with a Message and a TopicArn")
	}
	form := url.Values{}
	form.Set("Action", "Publish")
	form.Set("Version", "2010-03-31")
	for key, value := range params {
		if !snsScalarParameters[key] {
			return nil, unsupportedError("the sns:publish parameter %q", key)
		}
		text, isString := value.(string)
		if !isString {
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, newStateError(errRuntime, "the sns:publish parameter %q could not be encoded: %v", key, err)
			}
			text = string(encoded)
		}
		form.Set(key, text)
	}
	if form.Get("Message") == "" {
		return nil, newStateError(errParameterPathFailure, "sns:publish requires Parameters.Message")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, newStateError(errRuntime, "%v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Overcast-Region", in.region)
	rec := httptest.NewRecorder()
	in.handler.router.ServeHTTP(rec, req)
	if rec.Code >= 400 {
		code, message := decodeXMLError(rec.Body.Bytes())
		return nil, &stateError{name: "Sns." + code, cause: message}
	}

	var response struct {
		MessageID string `xml:"PublishResult>MessageId"`
	}
	if err := xml.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		return nil, newStateError(errTaskFailed, "the SNS Publish response could not be decoded: %v", err)
	}
	return map[string]any{"MessageId": response.MessageID}, nil
}

// ─── DynamoDB ─────────────────────────────────────────────────────────────────

// dynamoDBActions maps the optimized DynamoDB integrations Overcast
// interprets to their API target. Anything else fails loudly.
var dynamoDBActions = map[string]string{
	"putItem":    "PutItem",
	"getItem":    "GetItem",
	"updateItem": "UpdateItem",
}

func (in *interpreter) invokeDynamoDB(ctx context.Context, integration taskIntegration, payload any) (any, *stateError) {
	operation, known := dynamoDBActions[integration.action]
	if !known || integration.pattern != "" {
		return nil, unsupportedError("the dynamodb:%s%s integration — Overcast interprets dynamodb:putItem, dynamodb:getItem and dynamodb:updateItem", integration.action, patternSuffix(integration.pattern))
	}
	params, ok := payload.(map[string]any)
	if !ok {
		return nil, newStateError(errParameterPathFailure, "dynamodb:%s requires Parameters", integration.action)
	}
	return in.invokeTarget(ctx, "DynamoDB_20120810."+operation, params, "DynamoDB")
}

// ─── Nested state machine executions ──────────────────────────────────────────

func (in *interpreter) startNestedExecution(ctx context.Context, integration taskIntegration, payload any) (any, *stateError) {
	switch integration.pattern {
	case "", "sync", "sync:2":
	default:
		return nil, unsupportedError("the states:startExecution.%s service-integration pattern", integration.pattern)
	}
	if in.depth >= maxNestedExecutionDepth {
		return nil, newStateError(errRuntime, "nested state machine executions are limited to %d levels", maxNestedExecutionDepth)
	}
	params, ok := payload.(map[string]any)
	if !ok {
		return nil, newStateError(errParameterPathFailure, "states:startExecution requires Parameters with a StateMachineArn")
	}
	smARN, _ := params["StateMachineArn"].(string)
	if smARN == "" {
		return nil, newStateError(errParameterPathFailure, "states:startExecution requires Parameters.StateMachineArn")
	}
	execName, _ := params["Name"].(string)

	input := ""
	if raw, present := params["Input"]; present {
		if text, isString := raw.(string); isString {
			input = text
		} else {
			encoded, err := json.Marshal(raw)
			if err != nil {
				return nil, newStateError(errRuntime, "the nested execution input could not be encoded: %v", err)
			}
			input = string(encoded)
		}
	}

	// A plain states:startExecution fires and forgets, as on AWS; only the
	// .sync forms block on the child.
	mode := executionAsync
	if integration.pattern != "" {
		mode = executionSync
	}
	child, aerr := in.handler.startExecution(ctx, smARN, execName, input, in.depth+1, mode)
	if aerr != nil {
		return nil, &stateError{name: "StepFunctions." + aerr.Code, cause: aerr.Message}
	}

	if integration.pattern == "" {
		return map[string]any{
			"ExecutionArn": child.ExecutionArn,
			"StartDate":    float64(child.StartDate.UnixMilli()) / 1000.0,
		}, nil
	}
	if child.Status != statusSucceeded {
		return nil, &stateError{name: errTaskFailed, cause: nestedFailureCause(child)}
	}

	result := map[string]any{
		"ExecutionArn":    child.ExecutionArn,
		"Input":           child.Input,
		"InputDetails":    map[string]any{"Included": true},
		"Name":            child.Name,
		"OutputDetails":   map[string]any{"Included": true},
		"StartDate":       float64(child.StartDate.UnixMilli()) / 1000.0,
		"StateMachineArn": child.StateMachineArn,
		"Status":          child.Status,
	}
	if child.StopDate != nil {
		result["StopDate"] = float64(child.StopDate.UnixMilli()) / 1000.0
	}
	// `.sync` hands the child's output back as a JSON string; `.sync:2`
	// parses it first, which is the whole difference between the two.
	if integration.pattern == "sync:2" {
		var decoded any
		if child.Output != "" && json.Unmarshal([]byte(child.Output), &decoded) == nil {
			result["Output"] = decoded
		}
	} else if child.Output != "" {
		result["Output"] = child.Output
	}
	return result, nil
}

func nestedFailureCause(child *Execution) string {
	cause := fmt.Sprintf("the nested execution %s ended %s", child.ExecutionArn, child.Status)
	if child.Error != "" {
		cause += ": " + child.Error
	}
	if child.Cause != "" {
		cause += " — " + child.Cause
	}
	return cause
}

// ─── Shared router dispatch ───────────────────────────────────────────────────

// invokeTarget performs an AWS JSON 1.0 target-dispatch call against
// Overcast's own router and returns the decoded response body.
func (in *interpreter) invokeTarget(ctx context.Context, target string, body map[string]any, errorPrefix string) (any, *stateError) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, newStateError(errRuntime, "the %s request could not be encoded: %v", target, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/", bytes.NewReader(encoded))
	if err != nil {
		return nil, newStateError(errRuntime, "%v", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", target)
	req.Header.Set("X-Overcast-Region", in.region)
	rec := httptest.NewRecorder()
	in.handler.router.ServeHTTP(rec, req)

	if rec.Code >= 400 {
		code, message := decodeJSONError(rec.Body.Bytes())
		return nil, &stateError{name: errorPrefix + "." + code, cause: message}
	}
	var decoded any
	raw := bytes.TrimSpace(rec.Body.Bytes())
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, newStateError(errTaskFailed, "the %s response could not be decoded: %v", target, err)
	}
	return decoded, nil
}

// decodeJSONError pulls an AWS error code and message out of a JSON error body.
func decodeJSONError(body []byte) (code, message string) {
	var payload struct {
		Type    string `json:"__type"`
		Code    string `json:"code"`
		Message string `json:"message"`
		Msg     string `json:"Message"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return "Unknown", strings.TrimSpace(string(body))
	}
	code = payload.Type
	if code == "" {
		code = payload.Code
	}
	// Some services qualify the type with a shape prefix ("com.amazonaws#Foo").
	if hash := strings.LastIndex(code, "#"); hash >= 0 {
		code = code[hash+1:]
	}
	if code == "" {
		code = "Unknown"
	}
	message = payload.Message
	if message == "" {
		message = payload.Msg
	}
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	return code, message
}

// decodeXMLError pulls an AWS error code and message out of an XML error body.
func decodeXMLError(body []byte) (code, message string) {
	var payload struct {
		Code    string `xml:"Error>Code"`
		Message string `xml:"Error>Message"`
	}
	if xml.Unmarshal(body, &payload) != nil || payload.Code == "" {
		return "Unknown", strings.TrimSpace(string(body))
	}
	return payload.Code, payload.Message
}
