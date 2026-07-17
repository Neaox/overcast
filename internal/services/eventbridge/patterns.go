package eventbridge

import (
	"encoding/json"
	"fmt"
)

func eventPatternMatches(pattern string, event map[string]any) bool {
	var p map[string]any
	if json.Unmarshal([]byte(pattern), &p) != nil {
		return false
	}
	return matchPatternMap(p, event)
}

func matchPatternMap(pattern, event map[string]any) bool {
	for key, want := range pattern {
		got, ok := event[key]
		if !ok {
			return false
		}
		switch wantTyped := want.(type) {
		case []any:
			if !matchAnyValue(wantTyped, got) {
				return false
			}
		case map[string]any:
			gotMap, ok := got.(map[string]any)
			if !ok || !matchPatternMap(wantTyped, gotMap) {
				return false
			}
		default:
			if fmt.Sprint(wantTyped) != fmt.Sprint(got) {
				return false
			}
		}
	}
	return true
}

func matchAnyValue(want []any, got any) bool {
	for _, candidate := range want {
		if fmt.Sprint(candidate) == fmt.Sprint(got) {
			return true
		}
	}
	return false
}
