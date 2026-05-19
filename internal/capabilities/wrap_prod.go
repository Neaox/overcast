//go:build !dev

package capabilities

import "net/http"

// Wrap is a compile-time no-op in production builds.
// The linker eliminates this call entirely; there is zero runtime overhead.
func Wrap(_, _ string, fn http.HandlerFunc) http.HandlerFunc { return fn }
