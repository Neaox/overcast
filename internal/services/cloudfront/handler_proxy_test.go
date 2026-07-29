package cloudfront

import "testing"

// TestMatchPathPattern_followsAWSSemantics is the specification for cache
// behavior path patterns, taken from the CloudFront documentation for
// CacheBehavior.PathPattern:
//
//   - "*" matches 0 or more characters, and it matches across "/" — a pattern
//     is matched against the whole path, not segment by segment.
//   - "?" matches exactly 1 character.
//   - The leading "/" is OPTIONAL: "CloudFront behavior is the same with or
//     without the leading /". "images/*.jpg" and "/images/*.jpg" are one
//     pattern.
//
// The original matcher special-cased a trailing "*" and otherwise fell back to
// path.Match, whose "*" stops at "/". Two consequences, both of which route a
// request to the wrong origin rather than failing loudly:
//
//   - A pattern without a leading slash could never match anything, because the
//     request path always has one. A behavior written "jobs*" or "api/*" was
//     dead, and its requests silently fell through to the DEFAULT behavior.
//   - "*.jpg", the documentation's own example, matched nothing outside the
//     root, and a wildcard in the middle ("/api/*/detail") never matched.
func TestMatchPathPattern_followsAWSSemantics(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
		why     string
	}{
		// ---- Everything ----------------------------------------------------
		{pattern: "*", path: "/anything/at/all", want: true},
		{pattern: "/*", path: "/anything/at/all", want: true},

		// ---- The leading slash is optional ---------------------------------
		{pattern: "jobs", path: "/jobs", want: true, why: "AWS: same behavior with or without the leading /"},
		{pattern: "/jobs", path: "/jobs", want: true},
		{pattern: "jobs*", path: "/jobs", want: true},
		{pattern: "jobs*", path: "/jobs/123", want: true},
		{pattern: "api/*", path: "/api/v1/things", want: true},
		{pattern: "/api/*", path: "/api/v1/things", want: true},
		{pattern: "images/*.jpg", path: "/images/cat.jpg", want: true},

		// ---- "*" matches across "/" ----------------------------------------
		{pattern: "*.jpg", path: "/images/deep/cat.jpg", want: true, why: "AWS's own example; path.Match's * stops at /"},
		{pattern: "/api/*/detail", path: "/api/v1/things/detail", want: true, why: "wildcard in the middle spans segments"},
		{pattern: "/assets/*", path: "/assets/js/app.min.js", want: true},

		// ---- "?" matches exactly one character -----------------------------
		{pattern: "/file?.txt", path: "/file1.txt", want: true},
		{pattern: "/file?.txt", path: "/file12.txt", want: false},
		{pattern: "/file?.txt", path: "/file.txt", want: false},

		// ---- Non-matches ----------------------------------------------------
		{pattern: "/api/*", path: "/jobs", want: false},
		{pattern: "jobs", path: "/jobs/123", want: false, why: "no wildcard: exact match only"},
		{pattern: "/images/*.jpg", path: "/images/cat.png", want: false},
		{pattern: "/jobs*", path: "/other", want: false},

		// ---- Literal regex metacharacters are literal ----------------------
		// A pattern is a glob, not a regexp: "." and "+" match themselves.
		{pattern: "/a.b", path: "/a.b", want: true},
		{pattern: "/a.b", path: "/axb", want: false, why: "'.' is a literal dot, not 'any character'"},
		{pattern: "/v1+/x", path: "/v1+/x", want: true},
	}

	for _, tc := range cases {
		name := tc.pattern + " vs " + tc.path
		t.Run(name, func(t *testing.T) {
			if got := matchPathPattern(tc.pattern, tc.path); got != tc.want {
				t.Errorf("matchPathPattern(%q, %q) = %v, want %v%s",
					tc.pattern, tc.path, got, tc.want, func() string {
						if tc.why != "" {
							return " — " + tc.why
						}
						return ""
					}())
			}
		})
	}
}
