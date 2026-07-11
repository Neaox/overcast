package lambda

// esm_filter.go — AWS Lambda EventSourceMapping filter criteria evaluation.
//
// Implements the EventBridge / Lambda filter rule syntax exactly:
// https://docs.aws.amazon.com/lambda/latest/dg/invocation-eventfiltering.html
// https://docs.aws.amazon.com/eventbridge/latest/userguide/eb-create-pattern-operators.html
//
// Filter semantics (per AWS spec):
//   - A nil or empty FilterCriteria passes all records (no filtering).
//   - Multiple Filters within FilterCriteria are OR'd: a record passes if
//     ANY filter matches.
//   - Within a single Filter Pattern (a JSON object), all top-level keys are
//     AND'd: a record matches only when ALL specified keys match.
//   - "$or" is the exception: it takes an array of sub-objects and requires
//     that at least one sub-object matches (OR across fields within one filter).
//   - At a leaf, the pattern value is an array of match rules; the record
//     field satisfies the rule set when ANY element matches (OR within array).
//
// Supported operators (per the comparison table in the AWS docs):
//   Exact match          "Name": [ "Alice" ]
//   Null                 "ID": [ null ]
//   Empty string         "Name": [ "" ]
//   Prefix               "Region": [ {"prefix": "us-"} ]
//   Suffix               "File": [ {"suffix": ".png"} ]
//   Equals-ignore-case   "Name": [ {"equals-ignore-case": "alice"} ]
//   Anything-but         "Weather": [ {"anything-but": ["Raining"]} ]
//   Numeric (=,>,>=,<,<=)"Price": [ {"numeric": ["=", 100]} ]
//   Numeric range        "Price": [ {"numeric": [">", 10, "<=", 20]} ]
//   Exists               "Field": [ {"exists": true} ]
//   Does not exist       "Field": [ {"exists": false} ]

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
)

// patternCache caches pre-parsed filter pattern JSON strings.
// Pattern strings are set at ESM creation time and never mutated, so cached
// values are safe to share across goroutines without additional locking.
var patternCache sync.Map // key: pattern string → value: map[string]any

// matchesFilterCriteria reports whether record matches at least one filter in
// fc. Returns true when fc is nil or contains no filters (pass-through
// behaviour: if no filter criteria is set, all records are forwarded to Lambda).
func matchesFilterCriteria(fc *FilterCriteria, record map[string]any) bool {
	if fc == nil || len(fc.Filters) == 0 {
		return true
	}
	for _, f := range fc.Filters {
		if matchPattern(f.Pattern, record) {
			return true
		}
	}
	return false
}

// matchPattern parses patternJSON and checks whether record matches it.
// An invalid or empty pattern passes all records (treated as no filter).
// Parsed patterns are cached by their JSON string to avoid repeated unmarshaling.
func matchPattern(patternJSON string, record map[string]any) bool {
	if patternJSON == "" {
		return true
	}
	var pattern map[string]any
	if cached, ok := patternCache.Load(patternJSON); ok {
		pattern = cached.(map[string]any)
	} else {
		if err := json.Unmarshal([]byte(patternJSON), &pattern); err != nil {
			return false
		}
		patternCache.Store(patternJSON, pattern)
	}
	return matchObject(pattern, record)
}

// matchObject checks that every key in pattern matches the corresponding
// field in event. Returns true when pattern is empty.
//
// Special key "$or" takes an array of sub-patterns; the event must satisfy
// at least one sub-pattern (OR across multiple field conditions).
func matchObject(pattern, event map[string]any) bool {
	for key, patternVal := range pattern {
		if key == "$or" {
			rules, ok := patternVal.([]any)
			if !ok {
				return false
			}
			matched := false
			for _, r := range rules {
				sub, ok := r.(map[string]any)
				if ok && matchObject(sub, event) {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
			continue
		}
		eventVal, exists := event[key]
		if !matchField(patternVal, eventVal, exists) {
			return false
		}
	}
	return true
}

// matchField checks whether eventVal (which may or may not exist in the event)
// satisfies patternVal.
//
// patternVal can be:
//   - []any            — array of match rules (leaf, evaluated with matchRuleArray)
//   - map[string]any   — nested object drill-down (recursive matchObject)
func matchField(patternVal, eventVal any, exists bool) bool {
	switch pv := patternVal.(type) {
	case []any:
		return matchRuleArray(pv, eventVal, exists)
	case map[string]any:
		if !exists {
			return false
		}
		// Event value may be a raw map or a JSON-encoded string (e.g. SQS body).
		eventMap, ok := toEventMap(eventVal)
		if !ok {
			return false
		}
		return matchObject(pv, eventMap)
	}
	return false
}

// matchRuleArray returns true if eventVal satisfies any rule in rules.
func matchRuleArray(rules []any, eventVal any, exists bool) bool {
	for _, rule := range rules {
		if matchRule(rule, eventVal, exists) {
			return true
		}
	}
	return false
}

// matchRule checks whether eventVal satisfies a single match rule.
//
// Rule types:
//   - nil             → matches when field is absent or value is null
//   - string          → exact string equality
//   - float64         → numeric equality (JSON numbers decode as float64)
//   - bool            → boolean equality
//   - map[string]any  → compound operator (prefix, suffix, exists, numeric, …)
func matchRule(rule, eventVal any, exists bool) bool {
	switch rv := rule.(type) {
	case nil:
		return !exists || eventVal == nil
	case string:
		s, ok := eventVal.(string)
		return ok && s == rv
	case float64:
		return matchNumericEqual(rv, eventVal)
	case bool:
		b, ok := eventVal.(bool)
		return ok && b == rv
	case map[string]any:
		return matchCompound(rv, eventVal, exists)
	}
	return false
}

// matchCompound handles the object-form comparison operators.
func matchCompound(op map[string]any, eventVal any, exists bool) bool {
	// exists / does-not-exist
	if existsVal, ok := op["exists"]; ok {
		wantExists, ok := existsVal.(bool)
		if !ok {
			return false
		}
		return exists == wantExists
	}

	// prefix
	if prefix, ok := op["prefix"]; ok {
		p, ok := prefix.(string)
		if !ok {
			return false
		}
		s, ok := eventVal.(string)
		return ok && strings.HasPrefix(s, p)
	}

	// suffix
	if suffix, ok := op["suffix"]; ok {
		p, ok := suffix.(string)
		if !ok {
			return false
		}
		s, ok := eventVal.(string)
		return ok && strings.HasSuffix(s, p)
	}

	// equals-ignore-case
	if eic, ok := op["equals-ignore-case"]; ok {
		p, ok := eic.(string)
		if !ok {
			return false
		}
		s, ok := eventVal.(string)
		return ok && strings.EqualFold(s, p)
	}

	// anything-but: passes when eventVal is NOT in the exclusion set
	if ab, ok := op["anything-but"]; ok {
		return matchAnythingBut(ab, eventVal)
	}

	// numeric comparison
	if num, ok := op["numeric"]; ok {
		return matchNumericOp(num, eventVal)
	}

	return false
}

// matchAnythingBut returns true when eventVal is NOT matched by the
// anything-but exclusion specification.
func matchAnythingBut(ab, eventVal any) bool {
	switch v := ab.(type) {
	case []any:
		for _, item := range v {
			if matchRule(item, eventVal, eventVal != nil) {
				return false // eventVal IS in the exclusion list
			}
		}
		return true // eventVal is NOT in the exclusion list
	case string:
		s, ok := eventVal.(string)
		return ok && s != v
	case float64:
		return !matchNumericEqual(v, eventVal)
	}
	return false
}

// matchNumericEqual reports whether eventVal numerically equals n.
// AWS compares numbers as their decimal string representations are equal
// (e.g. 300 and 3.0e2 are NOT equal under AWS rules).
func matchNumericEqual(n float64, eventVal any) bool {
	switch ev := eventVal.(type) {
	case float64:
		return ev == n
	case string:
		f, err := strconv.ParseFloat(ev, 64)
		return err == nil && f == n
	}
	return false
}

// matchNumericOp evaluates a numeric comparison operator sequence.
// Format: ["op", number, "op", number, …].
// Example: [">", 10, "<=", 20]  (range: price > 10 AND price <= 20).
func matchNumericOp(raw, eventVal any) bool {
	ops, ok := raw.([]any)
	if !ok || len(ops) < 2 {
		return false
	}

	var eventNum float64
	switch ev := eventVal.(type) {
	case float64:
		eventNum = ev
	case string:
		n, err := strconv.ParseFloat(ev, 64)
		if err != nil {
			return false
		}
		eventNum = n
	default:
		return false
	}

	for i := 0; i+1 < len(ops); i += 2 {
		op, ok := ops[i].(string)
		if !ok {
			return false
		}
		threshold, ok := ops[i+1].(float64)
		if !ok {
			return false
		}
		switch op {
		case "=":
			if eventNum != threshold {
				return false
			}
		case ">":
			if eventNum <= threshold {
				return false
			}
		case ">=":
			if eventNum < threshold {
				return false
			}
		case "<":
			if eventNum >= threshold {
				return false
			}
		case "<=":
			if eventNum > threshold {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// toEventMap coerces v to a map[string]any.
// If v is a JSON-encoded string (e.g. an SQS message body), it is parsed
// first so that nested field filtering works transparently on string payloads.
func toEventMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case map[string]map[string]any:
		// DynamoDB Items (Item = map[string]map[string]any) arrive here when
		// buildDynamoDBRecord places NewImage/OldImage/Keys directly in the
		// record. Each attribute value is already map[string]any, so a shallow
		// copy is sufficient for filter patterns to drill into them.
		out := make(map[string]any, len(m))
		for k, val := range m {
			out[k] = val
		}
		return out, true
	case string:
		// SQS message body may be a JSON-encoded string.
		var parsed map[string]any
		if err := json.Unmarshal([]byte(m), &parsed); err == nil {
			return parsed, true
		}
	}
	return nil, false
}
