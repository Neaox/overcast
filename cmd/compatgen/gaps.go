//go:build dev

package main

import (
	"sort"
	"strings"
)

// The refusal report, compat/model/gaps.json.
//
// Refusals are a feature: every operation the generator could not express
// lands here with a machine-readable reason, keyed by service and operation,
// sorted. It is the exception queue a recipe author works from, and it is a
// public statement of what the model plus the recipe cannot mechanically say.

const gapsVersion = 1

type gapsDocument struct {
	Version int   `json:"version"`
	Gaps    []gap `json:"gaps"`
}

type gap struct {
	Service   string `json:"service"`
	Operation string `json:"operation"`
	// Group is the group the operation would have joined.
	Group  string `json:"group"`
	Reason string `json:"reason"`
	Detail string `json:"detail"`
}

func sortGaps(gaps []gap) {
	sort.Slice(gaps, func(i, j int) bool {
		a, b := gaps[i], gaps[j]
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		if a.Operation != b.Operation {
			return a.Operation < b.Operation
		}
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		if a.Reason != b.Reason {
			return a.Reason < b.Reason
		}
		return a.Detail < b.Detail
	})
}

// reasonCode is the part of a reason before the first ':' — the stable
// vocabulary, with the member or shape it names stripped.
func reasonCode(reason string) string {
	if i := strings.IndexByte(reason, ':'); i >= 0 {
		return reason[:i]
	}
	return reason
}

func joinSorted(items []string, sep string) string {
	sorted := append([]string(nil), items...)
	sort.Strings(sorted)
	return strings.Join(sorted, sep)
}
