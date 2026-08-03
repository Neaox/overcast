package iampolicy

import "strings"

// Pattern is a compiled IAM wildcard pattern. IAM supports exactly two
// metacharacters — `*` (any sequence of characters, including `/` and `:`) and
// `?` (exactly one character) — so matching is a byte-wise glob walk with no
// regular expression to compile and nothing to allocate per match.
//
// Patterns carrying policy variables (`${aws:username}`) are expanded against
// the request context at match time; the `vars` flag keeps that allocation off
// the path of every other pattern.
type Pattern struct {
	raw  string
	wild bool
	vars bool
	all  bool
}

// NewPattern compiles raw into a Pattern.
func NewPattern(raw string) Pattern {
	return Pattern{
		raw:  raw,
		wild: strings.ContainsAny(raw, "*?"),
		vars: strings.Contains(raw, "${"),
		all:  raw == "*",
	}
}

// String returns the pattern as it was written in the policy.
func (p Pattern) String() string { return p.raw }

// Match reports whether value matches the pattern, case-sensitively.
func (p Pattern) Match(value string) bool { return p.match(value, nil, false) }

// MatchFold reports whether value matches the pattern, ignoring ASCII case.
// IAM action names are matched case-insensitively.
func (p Pattern) MatchFold(value string) bool { return p.match(value, nil, true) }

// MatchContext expands policy variables from ctx before matching. Unknown
// variables are left unexpanded so they cannot match a real ARN segment,
// matching AWS's behaviour for a variable with no value.
func (p Pattern) MatchContext(value string, ctx map[string]string) bool {
	return p.match(value, ctx, false)
}

func (p Pattern) match(value string, ctx map[string]string, fold bool) bool {
	if p.all {
		return true
	}
	pattern := p.raw
	if p.vars && ctx != nil {
		pattern = expandPolicyVariables(pattern, ctx)
	}
	if !p.wild && !p.vars {
		if fold {
			return strings.EqualFold(pattern, value)
		}
		return pattern == value
	}
	return globMatch(pattern, value, fold)
}

// matchAny reports whether value matches any pattern in the list.
func matchAny(patterns []Pattern, value string, ctx map[string]string, fold bool) bool {
	for _, p := range patterns {
		if p.match(value, ctx, fold) {
			return true
		}
	}
	return false
}

// globMatch walks pattern against value supporting `*` and `?`. It is the
// classic two-pointer backtracking walk: linear in the common case, allocation
// free always.
func globMatch(pattern, value string, fold bool) bool {
	var (
		p, v         = 0, 0
		star         = -1
		starValueIdx = 0
	)
	for v < len(value) {
		switch {
		case p < len(pattern) && (pattern[p] == '?' || byteEqual(pattern[p], value[v], fold)):
			p++
			v++
		case p < len(pattern) && pattern[p] == '*':
			star = p
			p++
			starValueIdx = v
		case star >= 0:
			p = star + 1
			starValueIdx++
			v = starValueIdx
		default:
			return false
		}
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}

func byteEqual(a, b byte, fold bool) bool {
	if a == b {
		return true
	}
	if !fold {
		return false
	}
	return lowerASCII(a) == lowerASCII(b)
}

func lowerASCII(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

// expandPolicyVariables replaces ${varname} tokens in s with corresponding
// values from ctx (case-insensitive key lookup). Unknown variables are left
// unexpanded so they will not match any real ARN segment.
func expandPolicyVariables(s string, ctx map[string]string) string {
	if !strings.Contains(s, "${") {
		return s
	}
	var buf strings.Builder
	buf.Grow(len(s))
	for i := 0; i < len(s); {
		start := strings.Index(s[i:], "${")
		if start == -1 {
			buf.WriteString(s[i:])
			break
		}
		buf.WriteString(s[i : i+start])
		i += start + 2 // skip "${"
		end := strings.Index(s[i:], "}")
		if end == -1 {
			// Unclosed variable reference — emit literally.
			buf.WriteString("${")
			continue
		}
		rawName := s[i : i+end]
		varName := strings.ToLower(rawName)
		i += end + 1 // skip past "}"
		if val, ok := ctx[varName]; ok {
			buf.WriteString(val)
		} else {
			// Unknown variable — leave unexpanded so it won't match any ARN.
			buf.WriteString("${")
			buf.WriteString(rawName)
			buf.WriteByte('}')
		}
	}
	return buf.String()
}

// expandPolicyVariablesList returns a new slice with IAM policy variables
// substituted from ctx in every element.
func expandPolicyVariablesList(values []string, ctx map[string]string) []string {
	needsExpansion := false
	for _, v := range values {
		if strings.Contains(v, "${") {
			needsExpansion = true
			break
		}
	}
	if !needsExpansion {
		return values
	}
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = expandPolicyVariables(v, ctx)
	}
	return out
}
