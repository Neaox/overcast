package appsync

// handler_evaluate.go — EvaluateCode and EvaluateMappingTemplate.
//
// These are the only two AppSync operations AWS binds outside the /v1/apis
// subtree, and the pinned model is unambiguous about it:
//
//	EvaluateCode             POST /v1/dataplane-evaluatecode
//	EvaluateMappingTemplate  POST /v1/dataplane-evaluatetemplate
//
// Neither input shape has an apiId member, in the path or in the body: both are
// API-independent evaluation sandboxes, so nothing here looks up an API and
// nothing here requires one to exist. Overcast served them under
// /v1/apis/{apiId}/… for 34 releases, where no SDK ever sent them (#860).
//
// They share a file because they are siblings, not because they share a
// handler. EvaluateMappingTemplateRequest is {template, context};
// EvaluateCodeRequest is {runtime, code, context, function}. The only thing
// genuinely common is the `context` member, and that is the one helper below.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

// The two modeled bindings, named once so the routes, the path prefixes a
// subset-registered router claims, and this file's documentation cannot drift
// apart.
const (
	evaluateCodePath            = "/v1/dataplane-evaluatecode"
	evaluateMappingTemplatePath = "/v1/dataplane-evaluatetemplate"
)

// Shape constraints from the pinned Smithy model (models/aws/VERSION records
// the revision; the raw models are not vendored, so these are transcribed from
// it rather than generated). Context, Code and Template are all string shapes
// carrying a @length trait, and Context is a String on both operations — not a
// map. Sending the modeled form used to be answered with a
// SerializationException, because the REST handler typed it map[string]any
// while the deleted typed path had it right.
const (
	evaluationContextMinLen = 2
	evaluationContextMaxLen = 28000
	evaluationCodeMinLen    = 1
	evaluationCodeMaxLen    = 32768
	evaluationTemplateMin   = 2
	evaluationTemplateMax   = 65536

	// runtimeNameAppSyncJS is the sole member of the model's RuntimeName enum.
	runtimeNameAppSyncJS = "APPSYNC_JS"

	// evaluationFunctionRequest is the entry point evaluated when the optional
	// `function` member is omitted. AWS documents request and response as the
	// valid values; which of them it defaults to is not stated in the model.
	evaluationFunctionRequest  = "request"
	evaluationFunctionResponse = "response"
)

// appSyncRuntime is the model's AppSyncRuntime structure. Both members are
// required, which is why it is a pointer on the request: absent and
// zero-valued have to be told apart to report the right error.
type appSyncRuntime struct {
	Name           string `json:"name"`
	RuntimeVersion string `json:"runtimeVersion"`
}

// evaluateCodeRequest is EvaluateCodeRequest. runtime, code and context are
// required; function is not.
type evaluateCodeRequest struct {
	Runtime  *appSyncRuntime `json:"runtime"`
	Code     string          `json:"code"`
	Context  string          `json:"context"`
	Function string          `json:"function"`
}

// evaluateMappingTemplateRequest is EvaluateMappingTemplateRequest. Both
// members are required, and there is no runtime and no function member.
type evaluateMappingTemplateRequest struct {
	Template string `json:"template"`
	Context  string `json:"context"`
}

// The two response envelopes carry the modeled members Overcast can answer
// truthfully, and omit the ones it cannot produce rather than sending an empty
// value that would assert something:
//
//   - outErrors (both operations) — the list of runtime errors added to the
//     GraphQL response. util.appendError is a no-op in the JS evaluator, and
//     the VTL evaluator collects $util.appendError calls in its scope but
//     MappingTemplateEvaluator returns only the rendered string. There is
//     nothing to report until an evaluator collects them, and an empty member
//     would read as "the evaluation raised none".
//   - logs on EvaluateMappingTemplate — the VTL evaluator captures no output.
//     The JS evaluator does, so EvaluateCode carries logs.
//
// stash and outErrors are String shapes in the model, not structures.

// evaluateCodeResponse is EvaluateCodeResponse.
type evaluateCodeResponse struct {
	EvaluationResult string                   `json:"evaluationResult,omitempty"`
	Error            *evaluateCodeErrorDetail `json:"error,omitempty"`
	Logs             []string                 `json:"logs,omitempty"`
	Stash            string                   `json:"stash,omitempty"`
}

// evaluateCodeErrorDetail is EvaluateCodeErrorDetail — message plus the
// line-level codeErrors only the code operation carries.
type evaluateCodeErrorDetail struct {
	Message    string      `json:"message,omitempty"`
	CodeErrors []CodeError `json:"codeErrors,omitempty"`
}

// evaluateMappingTemplateResponse is EvaluateMappingTemplateResponse. Its error
// member is ErrorDetail, which has a message and nothing else — the difference
// from the code operation's envelope is modeled, not incidental.
type evaluateMappingTemplateResponse struct {
	EvaluationResult string       `json:"evaluationResult,omitempty"`
	Error            *errorDetail `json:"error,omitempty"`
	Stash            string       `json:"stash,omitempty"`
}

// errorDetail is the model's ErrorDetail structure.
type errorDetail struct {
	Message string `json:"message,omitempty"`
}

// EvaluateCode handles POST /v1/dataplane-evaluatecode.
func (h *Handler) EvaluateCode(w http.ResponseWriter, r *http.Request) {
	if h.jsEvaluator == nil {
		protocol.NotImplementedJSON(w, r)
		return
	}

	var req evaluateCodeRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if aerr := req.validate(); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	evalCtx, aerr := decodeEvaluationContext(req.Context)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	function := req.Function
	if function == "" {
		function = evaluationFunctionRequest
	}

	result, err := h.jsEvaluator.Evaluate(req.Code, function, evalCtx)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	// A fault in the *evaluated* code is not a failed request: the operation is
	// modeled with an HTTP code of 200 and an `error` member precisely so the
	// caller can see what the resolver would have done.
	resp := evaluateCodeResponse{
		EvaluationResult: result.EvaluationResult,
		Logs:             result.Logs,
		Stash:            evaluationStash(evalCtx),
	}
	if result.Error != nil {
		resp.Error = &evaluateCodeErrorDetail{
			Message:    result.Error.Message,
			CodeErrors: result.Error.CodeErrors,
		}
	}
	writeJSON(w, r, http.StatusOK, resp)
}

// EvaluateMappingTemplate handles POST /v1/dataplane-evaluatetemplate.
func (h *Handler) EvaluateMappingTemplate(w http.ResponseWriter, r *http.Request) {
	if h.vtlEvaluator == nil {
		protocol.NotImplementedJSON(w, r)
		return
	}

	var req evaluateMappingTemplateRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if aerr := req.validate(); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	evalCtx, aerr := decodeEvaluationContext(req.Context)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	result, err := h.vtlEvaluator.Evaluate(req.Template, evalCtx)
	if err != nil {
		// $util.error() and a template that will not render are both outcomes
		// of the evaluation, reported in the envelope rather than as a 4xx.
		writeJSON(w, r, http.StatusOK, evaluateMappingTemplateResponse{
			Error: &errorDetail{Message: evaluationErrorMessage(err)},
			Stash: evaluationStash(evalCtx),
		})
		return
	}

	writeJSON(w, r, http.StatusOK, evaluateMappingTemplateResponse{
		EvaluationResult: result,
		Stash:            evaluationStash(evalCtx),
	})
}

func (req evaluateCodeRequest) validate() *protocol.AWSError {
	if req.Runtime == nil {
		return badRequestError("runtime is required.")
	}
	if req.Runtime.Name != runtimeNameAppSyncJS {
		return badRequestError("runtime.name must be " + runtimeNameAppSyncJS + ".")
	}
	if req.Runtime.RuntimeVersion == "" {
		return badRequestError("runtime.runtimeVersion is required.")
	}
	if aerr := checkModeledLength("code", req.Code, evaluationCodeMinLen, evaluationCodeMaxLen); aerr != nil {
		return aerr
	}
	switch req.Function {
	case "", evaluationFunctionRequest, evaluationFunctionResponse:
		return nil
	default:
		return badRequestError("function must be " + evaluationFunctionRequest +
			" or " + evaluationFunctionResponse + ".")
	}
}

func (req evaluateMappingTemplateRequest) validate() *protocol.AWSError {
	return checkModeledLength("template", req.Template, evaluationTemplateMin, evaluationTemplateMax)
}

// decodeEvaluationContext validates and parses the `context` member both
// operations share. The model makes it a required String bounded at 2..28000
// characters; its content is the $context / ctx map the evaluators work on, so
// it has to be JSON to be usable, and a caller who sends something else is
// better told than silently given an empty context.
func decodeEvaluationContext(raw string) (map[string]any, *protocol.AWSError) {
	if aerr := checkModeledLength("context", raw, evaluationContextMinLen, evaluationContextMaxLen); aerr != nil {
		return nil, aerr
	}
	evalCtx := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &evalCtx); err != nil {
		return nil, badRequestError("context is not valid JSON: " + err.Error())
	}
	return evalCtx, nil
}

// checkModeledLength enforces a modeled @length trait on a required string
// member. BadRequestException is what the model binds to both operations for a
// malformed request; neither declares ValidationException.
func checkModeledLength(member, value string, minLen, maxLen int) *protocol.AWSError {
	switch {
	case value == "":
		return badRequestError(member + " is required.")
	case len(value) < minLen:
		return badRequestError(member + " must be at least " + strconv.Itoa(minLen) + " characters.")
	case len(value) > maxLen:
		return badRequestError(member + " must be at most " + strconv.Itoa(maxLen) + " characters.")
	}
	return nil
}

// evaluationStash serialises the resolver stash left behind by the evaluation,
// which the model carries as a String rather than a map.
//
// goja writes through to the Go map the context was built from, so code that
// assigns to ctx.stash is visible here — the same property resolver execution
// depends on in syncStash. A context that carried no stash produces no member
// rather than an empty object, because "{}" would assert the evaluation
// observed one.
func evaluationStash(evalCtx map[string]any) string {
	stash, ok := evalCtx["stash"]
	if !ok || stash == nil {
		return ""
	}
	encoded, err := json.Marshal(stash)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// evaluationErrorMessage unwraps the structured error $util.error() raises so
// the envelope carries the caller's message rather than Go's wrapping of it.
func evaluationErrorMessage(err error) string {
	var vtlErr *vtlError
	if errors.As(err, &vtlErr) {
		return vtlErr.Message
	}
	return err.Error()
}
