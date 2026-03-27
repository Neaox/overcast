package middleware

import (
	"net/http"

	"go.uber.org/zap"
)

// SigV4 is the SigV4 request signature middleware.
//
// Current behaviour: accepts all requests regardless of signature.
// The hook is wired into the middleware chain so that validation can be
// enabled by flipping cfg.SigV4Validate = true without any routing changes.
//
// TODO: Implement full SigV4 validation.
// Reference: https://docs.aws.amazon.com/general/latest/gr/sigv4_signing.html
// Implementation plan:
//  1. Parse Authorization header: AWS4-HMAC-SHA256 Credential=.../SignedHeaders=.../Signature=...
//  2. Re-derive the signing key from the secret (stored in a credentials map).
//  3. Reconstruct the canonical request and string-to-sign.
//  4. Compare HMAC-SHA256 of string-to-sign against the provided signature.
//  5. Return 403 InvalidSignatureException on mismatch.
//
// Default test credentials (accepted by all AWS SDKs in local mode):
//
//	Access key:    test  (or any non-empty string)
//	Secret key:    test  (or any non-empty string)
//	Region:        us-east-1
func SigV4(validate bool, logger *zap.Logger) func(http.Handler) http.Handler {
	if validate {
		// Placeholder — replace this block with real validation when implemented.
		logger.Warn("SigV4 validation is enabled but NOT YET IMPLEMENTED — all requests are still accepted")
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if validate { //nolint:staticcheck // TODO(priority:P2): implement SigV4 validation
				// On failure: write a 403 XML/JSON error and return without
				// calling next.ServeHTTP.
				_ = r // placeholder to avoid empty branch lint warning
			}
			next.ServeHTTP(w, r)
		})
	}
}
