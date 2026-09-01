package ssm

// filters.go — what DescribeParameters does with a filter it cannot answer.
//
// It used to ignore it, and in two places: the legacy handler's matchesFilters
// and the typed path's matchesTypedFilters were the same eight lines written
// twice, both of which applied a filter only when the caller had spelled
// `Key=Name, Option=BeginsWith` exactly. Anything else fell through and the
// call answered with every parameter in the account, dressed as a filtered
// result. Three ways to get that wrong answer:
//
//   - an unimplemented Key — Type, KeyId, Path, Label, Tier and DataType are
//     all real, and all were ignored;
//   - an unimplemented Option on Name — Equals and Contains, ignored;
//   - no Option at all, which AWS reads as Equals and which was ignored, so the
//     default comparison was the one that matched everything.
//
// That is the divergence #1032 fixed for EC2, and it is less excusable here
// because SSM already models the answer: InvalidFilterKey and
// InvalidFilterOption are declared errors of DescribeParameters. A filter this
// package cannot honour is now refused with one of them, before any parameter
// is read, from a single declaration both paths share.
//
// https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_DescribeParameters.html

import (
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/overcast-sh/overcast/internal/protocol"
)

// parameterFilterKeys maps each filter key this package implements to the value
// it reads off a parameter, and is the whole of what DescribeParameters can
// answer.
//
// AWS models more. A key Overcast cannot read off the record it holds is one it
// cannot honestly accept, so those are refused rather than quietly widened to
// everything.
var parameterFilterKeys = map[string]func(*ParameterRecord) string{
	"Name": func(rec *ParameterRecord) string { return rec.Name },
	"Type": func(rec *ParameterRecord) string {
		if latest := rec.Latest(); latest != nil {
			return latest.Type
		}
		return ""
	},
}

// parameterFilterOptions are the comparisons a filter may ask for.
//
// The empty option is AWS's default, and it means Equals — not "apply nothing",
// which is what it used to mean here.
var parameterFilterOptions = map[string]func(have, want string) bool{
	"":           func(have, want string) bool { return have == want },
	"Equals":     func(have, want string) bool { return have == want },
	"BeginsWith": strings.HasPrefix,
	"Contains":   strings.Contains,
}

// validateParameterFilters refuses a key or option this package cannot honour.
//
// Both callers run it before scanning the store, so an account with no
// parameters cannot answer an unanswerable filter with an empty page — "no
// match" and "I did not understand you" look identical in an empty result.
func validateParameterFilters(filters []parameterFilter) *protocol.AWSError {
	for _, f := range filters {
		if _, ok := parameterFilterKeys[f.Key]; !ok {
			return errInvalidFilterKey(f.Key)
		}
		if _, ok := parameterFilterOptions[f.Option]; !ok {
			return errInvalidFilterOption(f.Option)
		}
	}
	return nil
}

// matchesFilters reports whether a parameter satisfies every filter. Filters
// AND together and the values within one OR together, which is AWS's rule.
func matchesFilters(rec *ParameterRecord, filters []parameterFilter) bool {
	for _, f := range filters {
		read, compare := parameterFilterKeys[f.Key], parameterFilterOptions[f.Option]
		if read == nil || compare == nil {
			continue // validateParameterFilters refused these before the scan
		}
		have := read(rec)
		if !slices.ContainsFunc(f.Values, func(want string) bool { return compare(have, want) }) {
			return false
		}
	}
	return true
}

// errInvalidFilterKey and errInvalidFilterOption are DescribeParameters' own
// declared errors for a filter it cannot answer. Both name what was refused and
// what is accepted, because the caller's next move is to pick another one.
func errInvalidFilterKey(key string) *protocol.AWSError {
	return &protocol.AWSError{
		Code: "InvalidFilterKey",
		Message: fmt.Sprintf("The filter key '%s' is not valid. Overcast implements these DescribeParameters filter keys: %s",
			key, strings.Join(slices.Sorted(maps.Keys(parameterFilterKeys)), ", ")),
		HTTPStatus: http.StatusBadRequest,
	}
}

func errInvalidFilterOption(option string) *protocol.AWSError {
	// The empty option is "unset", which means Equals; naming it as a spelling
	// a caller could type would be nonsense.
	options := slices.DeleteFunc(
		slices.Sorted(maps.Keys(parameterFilterOptions)),
		func(o string) bool { return o == "" })
	return &protocol.AWSError{
		Code: "InvalidFilterOption",
		Message: fmt.Sprintf("The filter option '%s' is not valid. Valid options are: %s",
			option, strings.Join(options, ", ")),
		HTTPStatus: http.StatusBadRequest,
	}
}
