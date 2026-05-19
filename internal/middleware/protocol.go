package middleware

import (
	"net/http"

	"github.com/Neaox/overcast/internal/protocol/codec"
)

// Protocol is the wire-protocol detection middleware. It walks a list of
// codec.Identifiers in precision order; on the first match it stashes
// the codec and operation name in the request context (retrievable via
// codec.FromContext) and forwards the request unchanged.
//
// The middleware NEVER:
//   - consumes the request body,
//   - rejects a request, or
//   - alters the request in any way other than adding context values.
//
// On no match it forwards the request unchanged, so legacy handlers
// continue to function exactly as before. Rejection of unsupported
// protocols for opted-in services happens at the dispatcher boundary,
// not here.
//
// This middleware is always-on as of Phase 6 completion.
func Protocol(identifiers []codec.Identifier) func(http.Handler) http.Handler {
	if len(identifiers) == 0 {
		// No identifiers: middleware is a no-op. Returning a passthrough
		// (rather than skipping registration) keeps the chain shape
		// identical regardless of configuration, which simplifies tests.
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, id := range identifiers {
				c, op, ok := id.Claim(r)
				if !ok {
					continue
				}
				ctx := codec.WithDispatch(r.Context(), c, op)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
