package eventtarget

// Input transformation applied to an event before it reaches a target.
//
// EventBridge rule targets and EventBridge Pipes share this vocabulary
// (InputPath selects a node; InputTransformer renders a template from named
// paths), so it lives beside the dispatcher rather than inside either service.

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// SelectPath evaluates the JSONPath subset EventBridge accepts against a
// decoded JSON document and returns the selected node.
//
// Supported: the root "$", dotted member access ("$.detail.orderId" — member
// names may contain dashes, as "detail-type" does), and zero-based array
// indexing ("$.resources[0]"). Filters, wildcards, recursive descent and
// slices are not supported; a path using them selects nothing, which callers
// surface rather than silently treating as an empty value.
func SelectPath(doc any, path string) (any, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, false
	}
	if path == "$" {
		return doc, true
	}
	if !strings.HasPrefix(path, "$.") && !strings.HasPrefix(path, "$[") {
		return nil, false
	}

	cur := doc
	rest := path[1:]
	for rest != "" {
		switch {
		case strings.HasPrefix(rest, "."):
			rest = rest[1:]
			end := strings.IndexAny(rest, ".[")
			if end < 0 {
				end = len(rest)
			}
			name := rest[:end]
			rest = rest[end:]
			if name == "" {
				return nil, false
			}
			obj, ok := cur.(map[string]any)
			if !ok {
				return nil, false
			}
			cur, ok = obj[name]
			if !ok {
				return nil, false
			}
		case strings.HasPrefix(rest, "["):
			closeIdx := strings.Index(rest, "]")
			if closeIdx < 0 {
				return nil, false
			}
			idx, err := strconv.Atoi(strings.TrimSpace(rest[1:closeIdx]))
			if err != nil || idx < 0 {
				return nil, false
			}
			rest = rest[closeIdx+1:]
			arr, ok := cur.([]any)
			if !ok || idx >= len(arr) {
				return nil, false
			}
			cur = arr[idx]
		default:
			return nil, false
		}
	}
	return cur, true
}

// InputTransformer renders a target's InputTemplate from named JSONPath
// selections, matching EventBridge's InputTransformer target field.
type InputTransformer struct {
	// InputPathsMap maps a placeholder name to the JSONPath selecting its
	// value from the event.
	InputPathsMap map[string]string
	// InputTemplate is the template text. Placeholders are written <name>.
	InputTemplate string
}

// Render substitutes every placeholder in the template and returns the result.
//
// A string selection is substituted without quotes — templates supply their
// own, as AWS's do (`{"id":"<order>"}`) — and any other value is substituted as
// JSON. reserved supplies EventBridge's `aws.events.*` variables; callers that
// have no rule context pass nil.
//
// A placeholder whose path selects nothing renders as an empty string, the
// same as AWS's behaviour for an absent node in a quoted position. The
// rendered template is returned verbatim: AWS does not require it to be JSON,
// and a target that wants a JSON body is responsible for writing one.
func (it InputTransformer) Render(doc any, reserved map[string]string) ([]byte, error) {
	if it.InputTemplate == "" {
		return nil, fmt.Errorf("InputTransformer requires an InputTemplate")
	}
	out := it.InputTemplate
	for name, path := range it.InputPathsMap {
		value, ok := SelectPath(doc, path)
		replacement := ""
		if ok {
			var err error
			replacement, err = templateValue(value)
			if err != nil {
				return nil, err
			}
		}
		out = strings.ReplaceAll(out, "<"+name+">", replacement)
	}
	for name, value := range reserved {
		out = strings.ReplaceAll(out, "<"+name+">", value)
	}
	return []byte(out), nil
}

// templateValue renders one selected node for substitution into a template.
func templateValue(v any) (string, error) {
	if s, ok := v.(string); ok {
		return s, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
