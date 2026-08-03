package stepfunctions

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// The Amazon States Language intrinsic functions Overcast evaluates inside a
// payload template's `.$` fields. Every other `States.*(...)` call raises a
// path error, which the interpreter turns into a loud execution failure rather
// than resolving the field to null.

// ─── Intrinsic functions ──────────────────────────────────────────────────────

// supportedIntrinsics is the set of ASL intrinsic functions Overcast
// evaluates. Every other `States.*(...)` call fails the execution loudly
// rather than resolving to null.
var supportedIntrinsics = map[string]bool{
	"States.Format":       true,
	"States.StringToJson": true,
	"States.JsonToString": true,
	"States.Array":        true,
	"States.ArrayLength":  true,
	"States.MathAdd":      true,
}

func evaluateIntrinsic(expr string, doc any, ctxObj map[string]any) (any, error) {
	open := strings.Index(expr, "(")
	if open < 0 || !strings.HasSuffix(expr, ")") {
		return nil, pathErrorf("intrinsic %q is malformed — expected States.Function(args)", expr)
	}
	name := strings.TrimSpace(expr[:open])
	if !supportedIntrinsics[name] {
		return nil, pathErrorf("intrinsic function %q is not supported by Overcast (supported: %s)", name, supportedIntrinsicNames())
	}
	rawArgs, err := splitIntrinsicArgs(expr[open+1 : len(expr)-1])
	if err != nil {
		return nil, err
	}
	args := make([]any, 0, len(rawArgs))
	for _, raw := range rawArgs {
		value, argErr := resolveIntrinsicArg(raw, doc, ctxObj)
		if argErr != nil {
			return nil, argErr
		}
		args = append(args, value)
	}

	switch name {
	case "States.Format":
		return intrinsicFormat(args)
	case "States.StringToJson":
		return intrinsicStringToJSON(args)
	case "States.JsonToString":
		return intrinsicJSONToString(args)
	case "States.Array":
		return args, nil
	case "States.ArrayLength":
		if len(args) != 1 {
			return nil, pathErrorf("States.ArrayLength expects exactly 1 argument, got %d", len(args))
		}
		arr, ok := args[0].([]any)
		if !ok {
			return nil, pathErrorf("States.ArrayLength expects an array argument")
		}
		return float64(len(arr)), nil
	case "States.MathAdd":
		if len(args) != 2 {
			return nil, pathErrorf("States.MathAdd expects exactly 2 arguments, got %d", len(args))
		}
		a, aok := toNumber(args[0])
		b, bok := toNumber(args[1])
		if !aok || !bok {
			return nil, pathErrorf("States.MathAdd expects two numeric arguments")
		}
		return a + b, nil
	}
	return nil, pathErrorf("intrinsic function %q is not supported by Overcast", name)
}

func supportedIntrinsicNames() string {
	names := make([]string, 0, len(supportedIntrinsics))
	for name := range supportedIntrinsics {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func intrinsicFormat(args []any) (any, error) {
	if len(args) == 0 {
		return nil, pathErrorf("States.Format expects at least 1 argument")
	}
	format, ok := args[0].(string)
	if !ok {
		return nil, pathErrorf("States.Format expects a string template as its first argument")
	}
	rest := args[1:]
	var b strings.Builder
	used := 0
	for i := 0; i < len(format); i++ {
		if format[i] == '\\' && i+1 < len(format) {
			b.WriteByte(format[i+1])
			i++
			continue
		}
		if strings.HasPrefix(format[i:], "{}") {
			if used >= len(rest) {
				return nil, pathErrorf("States.Format has more {} placeholders than arguments")
			}
			b.WriteString(formatIntrinsicValue(rest[used]))
			used++
			i++
			continue
		}
		b.WriteByte(format[i])
	}
	if used != len(rest) {
		return nil, pathErrorf("States.Format has %d argument(s) for %d placeholder(s)", len(rest), used)
	}
	return b.String(), nil
}

func formatIntrinsicValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return "null"
	default:
		encoded, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprint(x)
		}
		return string(encoded)
	}
}

func intrinsicStringToJSON(args []any) (any, error) {
	if len(args) != 1 {
		return nil, pathErrorf("States.StringToJson expects exactly 1 argument, got %d", len(args))
	}
	raw, ok := args[0].(string)
	if !ok {
		return nil, pathErrorf("States.StringToJson expects a string argument")
	}
	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, pathErrorf("States.StringToJson: argument is not valid JSON: %v", err)
	}
	return decoded, nil
}

func intrinsicJSONToString(args []any) (any, error) {
	if len(args) != 1 {
		return nil, pathErrorf("States.JsonToString expects exactly 1 argument, got %d", len(args))
	}
	encoded, err := json.Marshal(args[0])
	if err != nil {
		return nil, pathErrorf("States.JsonToString: %v", err)
	}
	return string(encoded), nil
}

// resolveIntrinsicArg turns one raw argument token into a value: a quoted
// string literal, a number literal, a boolean/null literal, a nested intrinsic
// call, or a reference path.
func resolveIntrinsicArg(raw string, doc any, ctxObj map[string]any) (any, error) {
	token := strings.TrimSpace(raw)
	switch {
	case token == "":
		return nil, pathErrorf("intrinsic argument is empty")
	case len(token) >= 2 && token[0] == '\'' && token[len(token)-1] == '\'':
		return unescapeIntrinsicLiteral(token[1 : len(token)-1]), nil
	case token == "true":
		return true, nil
	case token == "false":
		return false, nil
	case token == "null":
		return nil, nil
	case strings.HasPrefix(token, "States."):
		return evaluateIntrinsic(token, doc, ctxObj)
	case strings.HasPrefix(token, "$"):
		return selectPath(doc, ctxObj, token)
	}
	if number, err := strconv.ParseFloat(token, 64); err == nil {
		return number, nil
	}
	return nil, pathErrorf("intrinsic argument %q is not a literal, a path or a nested intrinsic", token)
}

func unescapeIntrinsicLiteral(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			b.WriteByte(s[i+1])
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// splitIntrinsicArgs splits an intrinsic argument list on commas that are not
// inside a single-quoted literal or a nested call's parentheses.
func splitIntrinsicArgs(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var (
		args    []string
		current strings.Builder
		inQuote bool
		depth   int
	)
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if inQuote {
			current.WriteByte(ch)
			if ch == '\\' && i+1 < len(raw) {
				current.WriteByte(raw[i+1])
				i++
				continue
			}
			if ch == '\'' {
				inQuote = false
			}
			continue
		}
		switch ch {
		case '\'':
			inQuote = true
			current.WriteByte(ch)
		case '(':
			depth++
			current.WriteByte(ch)
		case ')':
			depth--
			current.WriteByte(ch)
		case ',':
			if depth == 0 {
				args = append(args, current.String())
				current.Reset()
				continue
			}
			current.WriteByte(ch)
		default:
			current.WriteByte(ch)
		}
	}
	if inQuote || depth != 0 {
		return nil, pathErrorf("intrinsic argument list %q is unbalanced", raw)
	}
	args = append(args, current.String())
	return args, nil
}
