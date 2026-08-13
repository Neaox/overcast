package lambda

import (
	"regexp"
	"sync"
)

// lazyRegexp keeps validation patterns for request-only features off the
// package-init path. sync.OnceValue makes first use concurrency-safe and
// retains MustCompile's programmer-error panic for malformed literals.
func lazyRegexp(pattern string) func() *regexp.Regexp {
	return sync.OnceValue(func() *regexp.Regexp {
		return regexp.MustCompile(pattern)
	})
}
