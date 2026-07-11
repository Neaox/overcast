package cognito

import "golang.org/x/crypto/bcrypt"

func hashPassword(password string) ([]byte, error) {
	// Cognito is emulated for local dev/tests, not used as a security boundary.
	// Keep bcrypt semantics while avoiding production-grade CPU cost per request.
	return bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
}
