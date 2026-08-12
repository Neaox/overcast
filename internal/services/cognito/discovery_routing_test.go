package cognito

import (
	"strings"
	"testing"
)

// TestOIDCDiscoveryPathCannotCollideWithS3OrSQS pins the property that lets
// Cognito's discovery routes sit at the top level at all.
//
// `/{poolId}/.well-known/jwks.json` is a label-rooted route on a router whose
// unclaimed space belongs to S3, and which already carries SQS's path-style
// queue URLs at `/{accountID:[0-9]+}/{queueName}`. Normally that is a reason
// not to add one. Here the three shapes are disjoint by construction rather
// than by routing precedence:
//
//   - A Cognito pool ID is "{region}_{suffix}" and always contains "_".
//   - An S3 bucket name may not contain "_" at all (AWS bucket naming rules),
//     so no bucket can ever be spelled like a pool ID.
//   - An SQS account ID is entirely digits, which "{region}_{suffix}" is not.
//
// That is a fact about AWS's naming rules rather than about chi, which is why
// it is worth asserting: if it ever stopped holding, the symptom would be an
// S3 request answered by Cognito (or the reverse), and neither would look like
// a routing bug from the outside.
//
// See docs/plans/non-canonical-url-namespace.md section 4.9.
func TestOIDCDiscoveryPathCannotCollideWithS3OrSQS(t *testing.T) {
	poolIDs := []string{
		"us-east-1_abc123",
		"eu-west-2_A1b2C3d4",
		"ap-southeast-2_ZZZZ",
	}

	for _, poolID := range poolIDs {
		t.Run(poolID, func(t *testing.T) {
			// Given: a pool ID in the shape Cognito mints.
			if !strings.Contains(poolID, "_") {
				t.Fatalf("pool ID %q has no underscore — the disjointness argument does not hold", poolID)
			}

			// Then: no S3 bucket can be spelled this way. Bucket names are
			// limited to lowercase letters, digits, dots and hyphens.
			for _, r := range poolID {
				if r == '_' {
					break
				}
				if !isLegalS3BucketRune(r) {
					t.Fatalf("pool ID %q contains %q, which is not a legal bucket character either — "+
						"the argument still holds but for a different reason than documented", poolID, r)
				}
			}

			// And: it is not an SQS account ID, which is all digits.
			allDigits := true
			for _, r := range poolID {
				if r < '0' || r > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				t.Fatalf("pool ID %q is all digits and would be routed as an SQS queue URL", poolID)
			}
		})
	}
}

// isLegalS3BucketRune reports whether r may appear in an S3 bucket name.
func isLegalS3BucketRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		return true
	case r == '-' || r == '.':
		return true
	default:
		return false
	}
}
