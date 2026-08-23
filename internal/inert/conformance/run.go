package conformance

import "testing"

// Run executes the whole §3 contract against f and fails t with one
// t.Errorf per violated clause. This is the entry point a real service's
// test calls; Check is what this package's own meta-test (and anything
// wanting the raw violation list) calls instead.
func Run(t *testing.T, f Fixture) {
	t.Helper()
	for _, v := range Check(f) {
		t.Errorf("%s", v)
	}
}
