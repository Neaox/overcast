package appsync

// js_evaluator.go — APPSYNC_JS runtime implementation using goja (pure Go JS engine).
//
// Provides the CodeEvaluator interface implementation that runs JS resolver code
// with the AppSync JavaScript runtime utilities (@aws-appsync/utils).
//
// Supports:
//   - ES module syntax (export function request/response)
//   - Built-in util module: autoId, toJson, parseJson, time.nowISO8601, time.nowEpochMilliSeconds,
//     time.nowEpochSeconds, time.nowFormatted, dynamodb.toMapValues, dynamodb.toDynamoDB
//   - runtime.earlyReturn for short-circuit
//   - console.log capture
//   - ctx.stash propagation across pipeline functions

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"regexp"
	"strings"

	"github.com/dop251/goja"
	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/clock"
)

// jsEvaluator implements the CodeEvaluator interface using goja.
type jsEvaluator struct {
	clk clock.Clock
}

// jsUtilError is the structured error thrown by util.error() from JS code.
// It carries message, errorType, and data for propagation into the GraphQL response.
type jsUtilError struct {
	Message   string
	ErrorType string
	Data      any
}

func (e *jsUtilError) Error() string { return e.Message }

// newJSEvaluator creates a new APPSYNC_JS code evaluator.
func newJSEvaluator(clk clock.Clock) *jsEvaluator {
	return &jsEvaluator{clk: clk}
}

// Evaluate runs a JS code module and calls the specified function with the given context.
func (e *jsEvaluator) Evaluate(code string, function string, context map[string]any) (*EvaluationResult, error) {
	vm := goja.New()
	var logs []string

	// Set up console.log capture.
	console := vm.NewObject()
	if err := console.Set("log", func(call goja.FunctionCall) goja.Value {
		parts := make([]string, len(call.Arguments))
		for i, arg := range call.Arguments {
			parts[i] = arg.String()
		}
		logs = append(logs, strings.Join(parts, " "))
		return goja.Undefined()
	}); err != nil {
		return nil, fmt.Errorf("failed to set console.log: %w", err)
	}
	if err := vm.Set("console", console); err != nil {
		return nil, fmt.Errorf("failed to set console: %w", err)
	}

	// Set up the @aws-appsync/utils module as a global.
	if err := e.setupUtilModule(vm); err != nil {
		return nil, fmt.Errorf("failed to setup util module: %w", err)
	}

	// Set up runtime.earlyReturn.
	if err := e.setupRuntime(vm); err != nil {
		return nil, fmt.Errorf("failed to setup runtime: %w", err)
	}

	// Transform ESM export syntax to something goja can execute.
	// goja doesn't support ES modules natively, so we convert:
	//   export function request(ctx) { ... }
	// to:
	//   function request(ctx) { ... }
	//   var __exports = { request: typeof request !== 'undefined' ? request : undefined, response: typeof response !== 'undefined' ? response : undefined };
	transformed := transformESM(code)

	// Execute the code to define functions.
	_, err := vm.RunString(transformed)
	if err != nil {
		return &EvaluationResult{
			Error: &EvaluationError{
				Message: "JS compilation error: " + err.Error(),
			},
			Logs: logs,
		}, nil
	}

	// Get the exports object.
	exports := vm.Get("__exports")
	if exports == nil || goja.IsUndefined(exports) {
		return &EvaluationResult{
			Error: &EvaluationError{
				Message: "Code must export a " + function + " function",
			},
			Logs: logs,
		}, nil
	}

	exportsObj := exports.ToObject(vm)
	fnVal := exportsObj.Get(function)
	if fnVal == nil || goja.IsUndefined(fnVal) {
		return &EvaluationResult{
			Error: &EvaluationError{
				Message: "Code must export a " + function + " function",
			},
			Logs: logs,
		}, nil
	}

	fn, ok := goja.AssertFunction(fnVal)
	if !ok {
		return &EvaluationResult{
			Error: &EvaluationError{
				Message: function + " is not a callable function",
			},
			Logs: logs,
		}, nil
	}

	// Convert the context map to a JS value.
	ctxVal := vm.ToValue(context)

	// Call the function.
	result, err := fn(goja.Undefined(), ctxVal)
	if err != nil {
		// Check for earlyReturn or util.error — both surface as *goja.InterruptedError.
		if ex, ok := err.(*goja.InterruptedError); ok {
			// util.error() interrupts with a *jsUtilError
			if utilErr, ok := ex.Value().(*jsUtilError); ok {
				evalErr := &EvaluationError{
					Message:   utilErr.Message,
					ErrorType: utilErr.ErrorType,
					Data:      utilErr.Data,
				}
				return &EvaluationResult{
					Error: evalErr,
					Logs:  logs,
				}, nil
			}
			// runtime.earlyReturn() interrupts with the return value map
			if rv, ok := ex.Value().(map[string]any); ok {
				resultJSON, _ := json.Marshal(rv)
				return &EvaluationResult{
					EvaluationResult: string(resultJSON),
					Logs:             logs,
				}, nil
			}
		}
		return &EvaluationResult{
			Error: &EvaluationError{
				Message: "JS runtime error: " + err.Error(),
			},
			Logs: logs,
		}, nil
	}

	// Convert the result to JSON.
	goResult := result.Export()
	resultJSON, err := json.Marshal(goResult)
	if err != nil {
		return &EvaluationResult{
			EvaluationResult: fmt.Sprintf("%v", goResult),
			Logs:             logs,
		}, nil
	}

	return &EvaluationResult{
		EvaluationResult: string(resultJSON),
		Logs:             logs,
	}, nil
}

// setupUtilModule creates the @aws-appsync/utils global module.
func (e *jsEvaluator) setupUtilModule(vm *goja.Runtime) error {
	util := vm.NewObject()

	// util.autoId() — generates a UUID.
	if err := util.Set("autoId", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(uuid.New().String())
	}); err != nil {
		return err
	}

	// util.toJson(obj) — serializes to JSON string.
	if err := util.Set("toJson", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue("")
		}
		goVal := call.Arguments[0].Export()
		b, err := json.Marshal(goVal)
		if err != nil {
			return vm.ToValue("")
		}
		return vm.ToValue(string(b))
	}); err != nil {
		return err
	}

	// util.parseJson(str) — parses JSON string.
	if err := util.Set("parseJson", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Null()
		}
		s := call.Arguments[0].String()
		var result any
		if err := json.Unmarshal([]byte(s), &result); err != nil {
			return goja.Null()
		}
		return vm.ToValue(result)
	}); err != nil {
		return err
	}

	// util.error(message, errorType, data, errorInfo)
	if err := util.Set("error", func(call goja.FunctionCall) goja.Value {
		msg := ""
		if len(call.Arguments) > 0 {
			msg = call.Arguments[0].String()
		}
		errType := ""
		if len(call.Arguments) > 1 && !goja.IsNull(call.Arguments[1]) && !goja.IsUndefined(call.Arguments[1]) {
			errType = call.Arguments[1].String()
		}
		var data any
		if len(call.Arguments) > 2 && !goja.IsNull(call.Arguments[2]) && !goja.IsUndefined(call.Arguments[2]) {
			data = call.Arguments[2].Export()
		}
		// Interrupt the VM with the structured error so the caller can detect it via *goja.InterruptedError.
		vm.Interrupt(&jsUtilError{Message: msg, ErrorType: errType, Data: data})
		return goja.Undefined()
	}); err != nil {
		return err
	}

	// util.appendError(message, errorType, data, errorInfo)
	if err := util.Set("appendError", func(call goja.FunctionCall) goja.Value {
		// In a real implementation, this would append to the errors list.
		// For now, it's a no-op.
		return goja.Undefined()
	}); err != nil {
		return err
	}

	// util.time sub-object.
	timeObj := vm.NewObject()
	if err := timeObj.Set("nowISO8601", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(e.clk.Now().UTC().Format("2006-01-02T15:04:05Z07:00"))
	}); err != nil {
		return err
	}
	if err := timeObj.Set("nowEpochMilliSeconds", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(e.clk.Now().UnixMilli())
	}); err != nil {
		return err
	}
	if err := timeObj.Set("nowEpochSeconds", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(e.clk.Now().Unix())
	}); err != nil {
		return err
	}
	if err := timeObj.Set("nowFormatted", func(call goja.FunctionCall) goja.Value {
		layout := "2006-01-02T15:04:05Z"
		if len(call.Arguments) > 0 {
			layout = call.Arguments[0].String()
		}
		return vm.ToValue(e.clk.Now().UTC().Format(layout))
	}); err != nil {
		return err
	}
	if err := util.Set("time", timeObj); err != nil {
		return err
	}

	// util.dynamodb sub-object.
	dynamodb := vm.NewObject()
	if err := dynamodb.Set("toDynamoDB", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Null()
		}
		goVal := call.Arguments[0].Export()
		return vm.ToValue(toDynamoDBAttr(goVal))
	}); err != nil {
		return err
	}
	if err := dynamodb.Set("toMapValues", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Null()
		}
		goVal := call.Arguments[0].Export()
		goMap, ok := goVal.(map[string]any)
		if !ok {
			return goja.Null()
		}
		result := make(map[string]any, len(goMap))
		for k, v := range goMap {
			result[k] = toDynamoDBAttr(v)
		}
		return vm.ToValue(result)
	}); err != nil {
		return err
	}
	if err := dynamodb.Set("toString", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Null()
		}
		s := call.Arguments[0].String()
		return vm.ToValue(map[string]any{"S": s})
	}); err != nil {
		return err
	}
	if err := dynamodb.Set("toNumber", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Null()
		}
		return vm.ToValue(map[string]any{"N": fmt.Sprintf("%v", call.Arguments[0].Export())})
	}); err != nil {
		return err
	}
	// util.dynamodb.toBoolean(val)
	if err := dynamodb.Set("toBoolean", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue(map[string]any{"BOOL": false})
		}
		return vm.ToValue(map[string]any{"BOOL": call.Arguments[0].ToBoolean()})
	}); err != nil {
		return err
	}
	// util.dynamodb.toNull()
	if err := dynamodb.Set("toNull", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(map[string]any{"NULL": true})
	}); err != nil {
		return err
	}
	// util.dynamodb.toList(val)
	if err := dynamodb.Set("toList", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue(map[string]any{"L": []any{}})
		}
		goVal := call.Arguments[0].Export()
		arr, ok := goVal.([]any)
		if !ok {
			return vm.ToValue(map[string]any{"L": []any{}})
		}
		items := make([]any, len(arr))
		for i, item := range arr {
			items[i] = toDynamoDBAttr(item)
		}
		return vm.ToValue(map[string]any{"L": items})
	}); err != nil {
		return err
	}
	// util.dynamodb.toMap(val)
	if err := dynamodb.Set("toMap", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue(map[string]any{"M": map[string]any{}})
		}
		goVal := call.Arguments[0].Export()
		goMap, ok := goVal.(map[string]any)
		if !ok {
			return vm.ToValue(map[string]any{"M": map[string]any{}})
		}
		m := make(map[string]any, len(goMap))
		for k, v := range goMap {
			m[k] = toDynamoDBAttr(v)
		}
		return vm.ToValue(map[string]any{"M": m})
	}); err != nil {
		return err
	}
	// util.dynamodb.toStringSet(val)
	if err := dynamodb.Set("toStringSet", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue(map[string]any{"SS": []any{}})
		}
		goVal := call.Arguments[0].Export()
		arr, ok := goVal.([]any)
		if !ok {
			return vm.ToValue(map[string]any{"SS": []any{}})
		}
		ss := make([]any, len(arr))
		for i, item := range arr {
			ss[i] = fmt.Sprintf("%v", item)
		}
		return vm.ToValue(map[string]any{"SS": ss})
	}); err != nil {
		return err
	}
	// util.dynamodb.toNumberSet(val)
	if err := dynamodb.Set("toNumberSet", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue(map[string]any{"NS": []any{}})
		}
		goVal := call.Arguments[0].Export()
		arr, ok := goVal.([]any)
		if !ok {
			return vm.ToValue(map[string]any{"NS": []any{}})
		}
		ns := make([]any, len(arr))
		for i, item := range arr {
			ns[i] = fmt.Sprintf("%v", item)
		}
		return vm.ToValue(map[string]any{"NS": ns})
	}); err != nil {
		return err
	}
	// util.dynamodb.toDynamoDBJson(val)
	if err := dynamodb.Set("toDynamoDBJson", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue("null")
		}
		goVal := call.Arguments[0].Export()
		attr := toDynamoDBAttr(goVal)
		b, _ := json.Marshal(attr)
		return vm.ToValue(string(b))
	}); err != nil {
		return err
	}
	// util.dynamodb.toMapValuesJson(val)
	if err := dynamodb.Set("toMapValuesJson", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue("null")
		}
		goVal := call.Arguments[0].Export()
		goMap, ok := goVal.(map[string]any)
		if !ok {
			return vm.ToValue("null")
		}
		result := make(map[string]any, len(goMap))
		for k, v := range goMap {
			result[k] = toDynamoDBAttr(v)
		}
		b, _ := json.Marshal(result)
		return vm.ToValue(string(b))
	}); err != nil {
		return err
	}
	// util.dynamodb.toStringJson(val)
	if err := dynamodb.Set("toStringJson", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue("null")
		}
		s := call.Arguments[0].String()
		b, _ := json.Marshal(map[string]any{"S": s})
		return vm.ToValue(string(b))
	}); err != nil {
		return err
	}
	// util.dynamodb.toNumberJson(val)
	if err := dynamodb.Set("toNumberJson", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue("null")
		}
		b, _ := json.Marshal(map[string]any{"N": fmt.Sprintf("%v", call.Arguments[0].Export())})
		return vm.ToValue(string(b))
	}); err != nil {
		return err
	}

	if err := util.Set("dynamodb", dynamodb); err != nil {
		return err
	}

	// util.str sub-object — string utilities.
	strObj := vm.NewObject()
	if err := strObj.Set("toLower", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue("")
		}
		return vm.ToValue(strings.ToLower(call.Arguments[0].String()))
	}); err != nil {
		return err
	}
	if err := strObj.Set("toUpper", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue("")
		}
		return vm.ToValue(strings.ToUpper(call.Arguments[0].String()))
	}); err != nil {
		return err
	}
	if err := strObj.Set("toReplace", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 3 {
			if len(call.Arguments) > 0 {
				return call.Arguments[0]
			}
			return vm.ToValue("")
		}
		s := call.Arguments[0].String()
		old := call.Arguments[1].String()
		new := call.Arguments[2].String()
		return vm.ToValue(strings.ReplaceAll(s, old, new))
	}); err != nil {
		return err
	}
	if err := strObj.Set("normalize", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue("")
		}
		// Unicode NFC normalization via strings.ToValidUTF8 (best-effort for the emulator).
		return vm.ToValue(strings.ToValidUTF8(call.Arguments[0].String(), ""))
	}); err != nil {
		return err
	}
	if err := strObj.Set("trim", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue("")
		}
		return vm.ToValue(strings.TrimSpace(call.Arguments[0].String()))
	}); err != nil {
		return err
	}
	if err := strObj.Set("isEmpty", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue(true)
		}
		return vm.ToValue(strings.TrimSpace(call.Arguments[0].String()) == "")
	}); err != nil {
		return err
	}
	if err := strObj.Set("beginsWith", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			return vm.ToValue(false)
		}
		return vm.ToValue(strings.HasPrefix(call.Arguments[0].String(), call.Arguments[1].String()))
	}); err != nil {
		return err
	}
	if err := strObj.Set("endsWith", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			return vm.ToValue(false)
		}
		return vm.ToValue(strings.HasSuffix(call.Arguments[0].String(), call.Arguments[1].String()))
	}); err != nil {
		return err
	}
	if err := strObj.Set("padStart", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			if len(call.Arguments) > 0 {
				return call.Arguments[0]
			}
			return vm.ToValue("")
		}
		s := call.Arguments[0].String()
		length := int(call.Arguments[1].ToInteger())
		pad := " "
		if len(call.Arguments) >= 3 {
			pad = call.Arguments[2].String()
		}
		if pad == "" {
			pad = " "
		}
		for len(s) < length {
			s = pad + s
		}
		if len(s) > length {
			s = s[len(s)-length:]
		}
		return vm.ToValue(s)
	}); err != nil {
		return err
	}
	if err := strObj.Set("padEnd", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			if len(call.Arguments) > 0 {
				return call.Arguments[0]
			}
			return vm.ToValue("")
		}
		s := call.Arguments[0].String()
		length := int(call.Arguments[1].ToInteger())
		pad := " "
		if len(call.Arguments) >= 3 {
			pad = call.Arguments[2].String()
		}
		if pad == "" {
			pad = " "
		}
		for len(s) < length {
			s = s + pad
		}
		if len(s) > length {
			s = s[:length]
		}
		return vm.ToValue(s)
	}); err != nil {
		return err
	}
	if err := util.Set("str", strObj); err != nil {
		return err
	}

	// util.math sub-object — math utilities.
	mathObj := vm.NewObject()
	if err := mathObj.Set("roundNum", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue(0)
		}
		return vm.ToValue(math.Round(call.Arguments[0].ToFloat()))
	}); err != nil {
		return err
	}
	if err := mathObj.Set("minVal", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			return vm.ToValue(0)
		}
		a := call.Arguments[0].ToFloat()
		b := call.Arguments[1].ToFloat()
		if a < b {
			return vm.ToValue(a)
		}
		return vm.ToValue(b)
	}); err != nil {
		return err
	}
	if err := mathObj.Set("maxVal", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			return vm.ToValue(0)
		}
		a := call.Arguments[0].ToFloat()
		b := call.Arguments[1].ToFloat()
		if a > b {
			return vm.ToValue(a)
		}
		return vm.ToValue(b)
	}); err != nil {
		return err
	}
	if err := mathObj.Set("randomDouble", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(rand.Float64()) //nolint:gosec // emulator, not crypto
	}); err != nil {
		return err
	}
	if err := mathObj.Set("randomWithinRange", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			return vm.ToValue(0)
		}
		min := int(call.Arguments[0].ToFloat())
		max := int(call.Arguments[1].ToFloat())
		if max <= min {
			return vm.ToValue(min)
		}
		return vm.ToValue(min + rand.Intn(max-min)) //nolint:gosec // emulator, not crypto
	}); err != nil {
		return err
	}
	if err := util.Set("math", mathObj); err != nil {
		return err
	}

	// util.transform sub-object — DynamoDB expression builders.
	transformObj := vm.NewObject()
	if err := transformObj.Set("toDynamoDBFilterExpression", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Null()
		}
		goVal := call.Arguments[0].Export()
		filterMap, ok := goVal.(map[string]any)
		if !ok {
			return goja.Null()
		}
		b := &dynExprBuilder{}
		expr := b.buildFilter(filterMap)
		result := map[string]any{
			"expression":       expr,
			"expressionNames":  b.names,
			"expressionValues": b.values,
		}
		return vm.ToValue(result)
	}); err != nil {
		return err
	}
	if err := transformObj.Set("toDynamoDBConditionExpression", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Null()
		}
		goVal := call.Arguments[0].Export()
		filterMap, ok := goVal.(map[string]any)
		if !ok {
			return goja.Null()
		}
		b := &dynExprBuilder{}
		expr := b.buildFilter(filterMap)
		result := map[string]any{
			"expression":       expr,
			"expressionNames":  b.names,
			"expressionValues": b.values,
		}
		return vm.ToValue(result)
	}); err != nil {
		return err
	}
	if err := util.Set("transform", transformObj); err != nil {
		return err
	}

	// util.http sub-object — URL encoding/decoding.
	httpObj := vm.NewObject()
	if err := httpObj.Set("encodeUrl", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue("")
		}
		return vm.ToValue(vtlHttpEncodeUrl([]any{call.Arguments[0].Export()}))
	}); err != nil {
		return err
	}
	if err := httpObj.Set("decodeUrl", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue("")
		}
		return vm.ToValue(vtlHttpDecodeUrl([]any{call.Arguments[0].Export()}))
	}); err != nil {
		return err
	}
	if err := util.Set("http", httpObj); err != nil {
		return err
	}

	// Type-checking functions.
	if err := util.Set("isNull", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue(true)
		}
		return vm.ToValue(goja.IsNull(call.Arguments[0]) || goja.IsUndefined(call.Arguments[0]))
	}); err != nil {
		return err
	}
	if err := util.Set("isNullOrEmpty", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue(true)
		}
		v := call.Arguments[0]
		if goja.IsNull(v) || goja.IsUndefined(v) {
			return vm.ToValue(true)
		}
		goVal := v.Export()
		switch tv := goVal.(type) {
		case string:
			return vm.ToValue(tv == "")
		case []any:
			return vm.ToValue(len(tv) == 0)
		case map[string]any:
			return vm.ToValue(len(tv) == 0)
		}
		return vm.ToValue(false)
	}); err != nil {
		return err
	}
	if err := util.Set("isString", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue(false)
		}
		_, ok := call.Arguments[0].Export().(string)
		return vm.ToValue(ok)
	}); err != nil {
		return err
	}
	if err := util.Set("isList", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue(false)
		}
		_, ok := call.Arguments[0].Export().([]any)
		return vm.ToValue(ok)
	}); err != nil {
		return err
	}
	if err := util.Set("isMap", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue(false)
		}
		_, ok := call.Arguments[0].Export().(map[string]any)
		return vm.ToValue(ok)
	}); err != nil {
		return err
	}
	if err := util.Set("isNumber", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue(false)
		}
		goVal := call.Arguments[0].Export()
		switch goVal.(type) {
		case float64, int64:
			return vm.ToValue(true)
		}
		return vm.ToValue(false)
	}); err != nil {
		return err
	}
	if err := util.Set("isBoolean", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue(false)
		}
		_, ok := call.Arguments[0].Export().(bool)
		return vm.ToValue(ok)
	}); err != nil {
		return err
	}

	// Null coalescing.
	if err := util.Set("defaultIfNull", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			if len(call.Arguments) == 1 {
				return call.Arguments[0]
			}
			return goja.Null()
		}
		v := call.Arguments[0]
		if goja.IsNull(v) || goja.IsUndefined(v) {
			return call.Arguments[1]
		}
		return v
	}); err != nil {
		return err
	}
	if err := util.Set("defaultIfNullOrEmpty", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			if len(call.Arguments) == 1 {
				return call.Arguments[0]
			}
			return goja.Null()
		}
		v := call.Arguments[0]
		if goja.IsNull(v) || goja.IsUndefined(v) {
			return call.Arguments[1]
		}
		goVal := v.Export()
		switch tv := goVal.(type) {
		case string:
			if tv == "" {
				return call.Arguments[1]
			}
		case []any:
			if len(tv) == 0 {
				return call.Arguments[1]
			}
		case map[string]any:
			if len(tv) == 0 {
				return call.Arguments[1]
			}
		}
		return v
	}); err != nil {
		return err
	}

	// util.matches(pattern, str) — regex match.
	if err := util.Set("matches", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			return vm.ToValue(false)
		}
		pattern := call.Arguments[0].String()
		s := call.Arguments[1].String()
		matched, err := regexp.MatchString(pattern, s)
		if err != nil {
			return vm.ToValue(false)
		}
		return vm.ToValue(matched)
	}); err != nil {
		return err
	}

	// util.validate(condition, message, errorType) — throws if condition is false.
	if err := util.Set("validate", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		condition := call.Arguments[0].ToBoolean()
		if !condition {
			msg := "validation error"
			if len(call.Arguments) > 1 {
				msg = call.Arguments[1].String()
			}
			panic(vm.NewGoError(fmt.Errorf("util.validate: %s", msg)))
		}
		return goja.Undefined()
	}); err != nil {
		return err
	}

	// Register util as global and as a fake module.
	if err := vm.Set("util", util); err != nil {
		return err
	}

	// Set up fake import resolution for '@aws-appsync/utils'.
	// Since goja doesn't support import, we pre-process the code to replace
	// import statements. But we also need the util available for destructuring patterns.
	return nil
}

// setupRuntime sets up the runtime global object.
func (e *jsEvaluator) setupRuntime(vm *goja.Runtime) error {
	runtimeObj := vm.NewObject()
	if err := runtimeObj.Set("earlyReturn", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) > 0 {
			vm.Interrupt(call.Arguments[0].Export())
		}
		return goja.Undefined()
	}); err != nil {
		return err
	}
	return vm.Set("runtime", runtimeObj)
}

// toDynamoDBAttr converts a Go value to a DynamoDB attribute value map.
func toDynamoDBAttr(v any) map[string]any {
	if v == nil {
		return map[string]any{"NULL": true}
	}
	switch val := v.(type) {
	case string:
		return map[string]any{"S": val}
	case float64:
		return map[string]any{"N": fmt.Sprintf("%v", val)}
	case int64:
		return map[string]any{"N": fmt.Sprintf("%d", val)}
	case bool:
		return map[string]any{"BOOL": val}
	case []any:
		items := make([]any, len(val))
		for i, item := range val {
			items[i] = toDynamoDBAttr(item)
		}
		return map[string]any{"L": items}
	case map[string]any:
		m := make(map[string]any, len(val))
		for k, item := range val {
			m[k] = toDynamoDBAttr(item)
		}
		return map[string]any{"M": m}
	default:
		return map[string]any{"S": fmt.Sprintf("%v", val)}
	}
}

// transformESM converts ES module syntax to plain JS that goja can execute.
// Handles:
//   - import { util } from '@aws-appsync/utils'  → (removed, util is already global)
//   - export function name(...)                  → function name(...)
//   - export const name = ...                    → const name = ...
//   - export default function/class              → function/class (anonymous not supported)
func transformESM(code string) string {
	var lines []string
	for _, line := range strings.Split(code, "\n") {
		trimmed := strings.TrimSpace(line)

		// Remove import statements for @aws-appsync/utils.
		if strings.HasPrefix(trimmed, "import ") && strings.Contains(trimmed, "@aws-appsync/utils") {
			continue
		}

		// Remove all 'export ' keywords (handles multiple exports on same line and
		// at start of line).
		if strings.Contains(line, "export ") {
			line = strings.ReplaceAll(line, "export ", "")
		}

		lines = append(lines, line)
	}

	// Append exports object.
	lines = append(lines, "")
	lines = append(lines, "var __exports = {};")
	lines = append(lines, "try { __exports.request = request; } catch(e) {}")
	lines = append(lines, "try { __exports.response = response; } catch(e) {}")

	return strings.Join(lines, "\n")
}
