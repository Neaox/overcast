package scenario

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Paths (compat/model/README.md § Paths): `$` is the response, `.Name` selects
// a structure member or map key, `[n]` selects a list element. Nothing else —
// no wildcards, filters, quoting or recursive descent.
//
// A path is walked over the *document* form of a response (document.go), not
// over the SDK struct, so member names are the modeled names every backend
// writes and a nil pointer is absence rather than null.

// pathSegment is one step of a path: a member name or a list index.
type pathSegment struct {
	member string
	index  int
	isIdx  bool
}

// parsePath splits a path into its segments. It rejects anything the IR's path
// grammar does not admit, so a malformed path fails the step rather than
// silently resolving to nothing — the two are very different bugs.
func parsePath(p string) ([]pathSegment, error) {
	if p == "" || p[0] != '$' {
		return nil, fmt.Errorf("path %q does not start with $", p)
	}
	var segs []pathSegment
	rest := p[1:]
	for rest != "" {
		switch rest[0] {
		case '.':
			rest = rest[1:]
			end := strings.IndexAny(rest, ".[")
			if end < 0 {
				end = len(rest)
			}
			if end == 0 {
				return nil, fmt.Errorf("path %q has an empty member name", p)
			}
			segs = append(segs, pathSegment{member: rest[:end]})
			rest = rest[end:]
		case '[':
			end := strings.IndexByte(rest, ']')
			if end < 0 {
				return nil, fmt.Errorf("path %q has an unterminated index", p)
			}
			n, err := strconv.Atoi(rest[1:end])
			if err != nil || n < 0 {
				return nil, fmt.Errorf("path %q has a non-numeric index %q", p, rest[1:end])
			}
			segs = append(segs, pathSegment{index: n, isIdx: true})
			rest = rest[end+1:]
		default:
			return nil, fmt.Errorf("path %q has an unexpected character %q", p, rest[0])
		}
	}
	return segs, nil
}

// resolvePath walks a path over a document. ok is false when any segment is
// absent — which is what `missing` tests for, and what makes an absent list
// count as empty for listContains and absent.
//
// A document value that is present and nil resolves: it is a value the service
// sent, not a missing member. The Go SDK cannot in fact distinguish a JSON
// null from an omitted member — both deserialize to a nil pointer — so
// document.go maps nil to absence and this branch is reached only for a null
// inside a document-typed member. The distinction is kept here anyway, because
// it is the IR's and the other backends do observe it.
func resolvePath(doc any, p string) (any, bool, error) {
	segs, err := parsePath(p)
	if err != nil {
		return nil, false, err
	}
	cur := doc
	for _, s := range segs {
		if s.isIdx {
			list, ok := cur.([]any)
			if !ok || s.index >= len(list) {
				return nil, false, nil
			}
			cur = list[s.index]
			continue
		}
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false, nil
		}
		v, ok := obj[s.member]
		if !ok {
			// The document's keys are Go field names, which smithy-go
			// capitalizes; the path's are the modeled member names, which are
			// not always capitalized (SQS's `queueUrls`). See exportedName.
			v, ok = obj[exportedName(s.member)]
		}
		if !ok {
			return nil, false, nil
		}
		cur = v
	}
	return cur, true, nil
}

// canonicalJSON renders a document in a stable form: object keys sorted
// (encoding/json does that for a map), no HTML escaping, no trailing newline.
// It is both how values are compared and how they are printed in a failure
// message, so "expected X, actual Y" reads in the same notation the scenario
// file is written in.
func canonicalJSON(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

// jsonEqual is the IR's "equal, as JSON" (compat/model/README.md § Assertions).
//
// Both sides are documents by the time they get here: a response through
// document.go, an expected value through the evaluator, which normalises Go
// literals the same way. So every JSON number is a float64 on both sides, every
// string a string, every structure a map[string]any, and comparing their
// canonical encodings is JSON equality with no coercion: "30" never equals 30,
// and true never equals 1.
func jsonEqual(a, b any) bool {
	as, aerr := canonicalJSON(a)
	bs, berr := canonicalJSON(b)
	return aerr == nil && berr == nil && as == bs
}

// render prints a value for a failure message.
func render(v any) string {
	s, err := canonicalJSON(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return s
}

// missingValue is what a failure message prints where a path did not resolve.
const missingValue = "<missing>"

// renderResolved prints a resolved-or-not value for a failure message.
func renderResolved(v any, ok bool) string {
	if !ok {
		return missingValue
	}
	return render(v)
}

// sortedKeys orders a map's keys so failure messages and check order are
// deterministic across runs — three identical runs is an acceptance criterion.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
