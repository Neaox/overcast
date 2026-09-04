//go:build dev

package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Response paths.
//
// The IR addresses response fields with dot-plus-numeric-index paths only:
// `$`, `$.Attributes.QueueArn`, `$.Queues[0].Name`. Not JSONPath — no
// wildcards, filters, recursive descent or quoted keys — because eight
// implementations have to agree on every path, and this grammar is small
// enough to implement identically in an afternoon in any language.

type pathSegment struct {
	name  string // a structure member or map key; "" for an index segment
	index int    // a list index, or -1 for a name segment
}

type responsePath struct {
	raw      string
	segments []pathSegment
}

func (p responsePath) String() string { return p.raw }

var pathNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_\-:/]*$`)

// parsePath validates and splits a response path.
func parsePath(raw string) (responsePath, error) {
	if raw != "$" && !strings.HasPrefix(raw, "$.") && !strings.HasPrefix(raw, "$[") {
		return responsePath{}, fmt.Errorf("path %q must start with $", raw)
	}
	path := responsePath{raw: raw}
	rest := raw[1:]
	for rest != "" {
		switch rest[0] {
		case '.':
			rest = rest[1:]
			end := strings.IndexAny(rest, ".[")
			if end < 0 {
				end = len(rest)
			}
			name := rest[:end]
			if !pathNameRE.MatchString(name) {
				return responsePath{}, fmt.Errorf("path %q: %q is not a member name", raw, name)
			}
			path.segments = append(path.segments, pathSegment{name: name, index: -1})
			rest = rest[end:]
		case '[':
			end := strings.IndexByte(rest, ']')
			if end < 0 {
				return responsePath{}, fmt.Errorf("path %q: unterminated index", raw)
			}
			index, err := strconv.Atoi(rest[1:end])
			if err != nil || index < 0 || (len(rest[1:end]) > 1 && rest[1] == '0') {
				return responsePath{}, fmt.Errorf("path %q: %q is not a non-negative index", raw, rest[1:end])
			}
			path.segments = append(path.segments, pathSegment{index: index})
			rest = rest[end+1:]
		default:
			return responsePath{}, fmt.Errorf("path %q: unexpected %q", raw, rest[:1])
		}
	}
	return path, nil
}

// mustPath is for paths the generator itself composes from validated parts.
func mustPath(raw string) responsePath {
	path, err := parsePath(raw)
	if err != nil {
		panic(err)
	}
	return path
}

// joinPath appends a member name to a path (`$.Tags` + `env` → `$.Tags.env`).
func joinPath(base, member string) string {
	return base + "." + member
}

// Context paths name exported values: `<resource>.<export>`.
var contextPathRE = regexp.MustCompile(`^[a-z][a-z0-9]*\.[a-zA-Z][a-zA-Z0-9]*$`)

func validContextPath(ref string) bool { return contextPathRE.MatchString(ref) }

// Name suffixes for $name: kebab-case, so the composed resource name stays
// kebab-case too.
var nameSuffixRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func validNameSuffix(suffix string) bool { return nameSuffixRE.MatchString(suffix) }
