package stepfunctions

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// This file implements the Amazon States Language data-flow primitives:
// reference paths (`$.a.b[0]`), the context object (`$$`) and payload templates
// (objects whose keys end in `.$`). The intrinsic functions those templates may
// call live in intrinsics.go. Anything outside the interpreted subset raises a
// runtime error so the execution fails loudly rather than quietly producing
// wrong data.

// pathError is a data-flow failure. It maps to States.Runtime, which is what
// AWS raises when a path cannot be resolved against the effective input.
type pathError struct{ msg string }

func (e *pathError) Error() string { return e.msg }

func pathErrorf(format string, args ...any) error {
	return &pathError{msg: fmt.Sprintf(format, args...)}
}

// pathSegment is one step of a reference path: a field name or an array index.
type pathSegment struct {
	field string
	index int
	isIdx bool
}

// parseReferencePath splits an ASL reference path into segments.
//
// Supported: `$`, `$$`, `$.a.b`, `$['a']['b']`, `$.a[0]`, and the same forms
// rooted at `$$` for the context object. Wildcards, descendants, slices and
// filter expressions are rejected — they are valid JSONPath but Overcast does
// not evaluate them, and silently returning the wrong node would be worse than
// failing.
func parseReferencePath(path string) (context bool, segments []pathSegment, err error) {
	raw := strings.TrimSpace(path)
	switch {
	case raw == "":
		return false, nil, pathErrorf("path is empty")
	case strings.HasPrefix(raw, "$$"):
		context = true
		raw = raw[2:]
	case strings.HasPrefix(raw, "$"):
		raw = raw[1:]
	default:
		return false, nil, pathErrorf("path %q must start with $ or $$", path)
	}
	if strings.ContainsAny(raw, "*?@,") || strings.Contains(raw, "..") {
		return false, nil, pathErrorf("path %q uses a JSONPath feature Overcast does not evaluate (wildcards, descendants, slices and filters are unsupported)", path)
	}

	for raw != "" {
		switch {
		case strings.HasPrefix(raw, "."):
			raw = raw[1:]
			end := strings.IndexAny(raw, ".[")
			if end < 0 {
				end = len(raw)
			}
			name := raw[:end]
			if name == "" {
				return false, nil, pathErrorf("path %q has an empty field name", path)
			}
			segments = append(segments, pathSegment{field: name})
			raw = raw[end:]
		case strings.HasPrefix(raw, "["):
			end := strings.Index(raw, "]")
			if end < 0 {
				return false, nil, pathErrorf("path %q has an unterminated [", path)
			}
			inner := strings.TrimSpace(raw[1:end])
			raw = raw[end+1:]
			if len(inner) >= 2 && (inner[0] == '\'' || inner[0] == '"') && inner[len(inner)-1] == inner[0] {
				segments = append(segments, pathSegment{field: inner[1 : len(inner)-1]})
				continue
			}
			idx, convErr := strconv.Atoi(inner)
			if convErr != nil {
				return false, nil, pathErrorf("path %q has an unsupported subscript %q", path, inner)
			}
			segments = append(segments, pathSegment{index: idx, isIdx: true})
		default:
			return false, nil, pathErrorf("path %q is malformed at %q", path, raw)
		}
	}
	return context, segments, nil
}

// selectPath resolves a reference path against the effective input (doc) and
// the execution context object (ctxObj). A path that does not resolve is an
// error, matching AWS's behaviour for InputPath/ItemsPath and for `.$` fields.
func selectPath(doc any, ctxObj map[string]any, path string) (any, error) {
	isContext, segments, err := parseReferencePath(path)
	if err != nil {
		return nil, err
	}
	var current any = doc
	if isContext {
		current = ctxObj
	}
	for i, seg := range segments {
		if seg.isIdx {
			arr, ok := current.([]any)
			if !ok {
				return nil, pathErrorf("path %q: %s is not an array", path, pathPrefix(segments, i))
			}
			if seg.index < 0 || seg.index >= len(arr) {
				return nil, pathErrorf("path %q: index %d is out of range", path, seg.index)
			}
			current = arr[seg.index]
			continue
		}
		obj, ok := current.(map[string]any)
		if !ok {
			return nil, pathErrorf("path %q: %s is not an object", path, pathPrefix(segments, i))
		}
		value, ok := obj[seg.field]
		if !ok {
			return nil, pathErrorf("path %q: field %q is not present", path, seg.field)
		}
		current = value
	}
	return current, nil
}

// selectPathOptional is selectPath but reports absence instead of erroring.
// Choice's Is* operators and IsPresent need "missing" as a value, not a fault.
func selectPathOptional(doc any, ctxObj map[string]any, path string) (any, bool) {
	value, err := selectPath(doc, ctxObj, path)
	if err != nil {
		return nil, false
	}
	return value, true
}

func pathPrefix(segments []pathSegment, upto int) string {
	var b strings.Builder
	b.WriteString("$")
	for _, seg := range segments[:upto] {
		if seg.isIdx {
			fmt.Fprintf(&b, "[%d]", seg.index)
			continue
		}
		b.WriteString("." + seg.field)
	}
	return b.String()
}

// insertPath returns doc with value placed at the reference path. A `$` path
// replaces the document outright; missing intermediate objects are created,
// which is what ResultPath does on AWS.
func insertPath(doc any, path string, value any) (any, error) {
	isContext, segments, err := parseReferencePath(path)
	if err != nil {
		return nil, err
	}
	if isContext {
		return nil, pathErrorf("path %q: the context object is read-only", path)
	}
	if len(segments) == 0 {
		return value, nil
	}
	for _, seg := range segments {
		if seg.isIdx {
			return nil, pathErrorf("path %q: array subscripts are not supported when writing a result", path)
		}
	}
	root, ok := doc.(map[string]any)
	if !ok {
		if doc == nil {
			root = map[string]any{}
		} else {
			return nil, pathErrorf("path %q: cannot write into a non-object payload", path)
		}
	}
	cloned := cloneJSON(root).(map[string]any)
	cursor := cloned
	for _, seg := range segments[:len(segments)-1] {
		next, ok := cursor[seg.field].(map[string]any)
		if !ok {
			next = map[string]any{}
			cursor[seg.field] = next
		}
		cursor = next
	}
	cursor[segments[len(segments)-1].field] = value
	return cloned, nil
}

// cloneJSON deep-copies a decoded JSON value so that a mutation on one state's
// data cannot alias another's.
func cloneJSON(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, item := range x {
			out[k] = cloneJSON(item)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = cloneJSON(item)
		}
		return out
	default:
		return v
	}
}

// ─── Payload templates ────────────────────────────────────────────────────────

// renderPayloadTemplate evaluates a Parameters/ResultSelector/ItemSelector
// template: every object key ending in `.$` is replaced by the value its path
// or intrinsic function resolves to, and the `.$` suffix is dropped.
func renderPayloadTemplate(tmpl json.RawMessage, doc any, ctxObj map[string]any) (any, error) {
	if len(tmpl) == 0 {
		return nil, nil
	}
	var decoded any
	if err := json.Unmarshal(tmpl, &decoded); err != nil {
		return nil, pathErrorf("payload template is not valid JSON: %v", err)
	}
	return renderTemplateValue(decoded, doc, ctxObj)
}

func renderTemplateValue(node any, doc any, ctxObj map[string]any) (any, error) {
	switch x := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for key, value := range x {
			if !strings.HasSuffix(key, ".$") {
				rendered, err := renderTemplateValue(value, doc, ctxObj)
				if err != nil {
					return nil, err
				}
				out[key] = rendered
				continue
			}
			expr, ok := value.(string)
			if !ok {
				return nil, pathErrorf("payload template key %q must have a string value", key)
			}
			resolved, err := evaluateTemplateExpression(expr, doc, ctxObj)
			if err != nil {
				return nil, err
			}
			out[strings.TrimSuffix(key, ".$")] = resolved
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			rendered, err := renderTemplateValue(item, doc, ctxObj)
			if err != nil {
				return nil, err
			}
			out[i] = rendered
		}
		return out, nil
	default:
		return node, nil
	}
}

// evaluateTemplateExpression resolves the right-hand side of a `.$` key:
// either a reference path or an intrinsic function call.
func evaluateTemplateExpression(expr string, doc any, ctxObj map[string]any) (any, error) {
	trimmed := strings.TrimSpace(expr)
	if strings.HasPrefix(trimmed, "States.") {
		return evaluateIntrinsic(trimmed, doc, ctxObj)
	}
	return selectPath(doc, ctxObj, trimmed)
}

func toNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	return 0, false
}
