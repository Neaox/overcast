package cognito

import (
	"unicode"

	"github.com/Neaox/overcast/internal/protocol"
)

// effectivePasswordPolicy returns the pool's password policy with defaults applied.
// AWS default: 8-character minimum, no complexity requirements.
func effectivePasswordPolicy(pool *UserPool) PasswordPolicy {
	if pool.Policies != nil && pool.Policies.PasswordPolicy != nil {
		pp := *pool.Policies.PasswordPolicy
		if pp.MinimumLength == 0 {
			pp.MinimumLength = 8
		}
		return pp
	}
	return PasswordPolicy{MinimumLength: 8}
}

// validatePassword checks a plaintext password against the pool's password policy.
// Returns nil if valid, or an InvalidPasswordException AWSError otherwise.
func validatePassword(pool *UserPool, password string) *protocol.AWSError {
	pp := effectivePasswordPolicy(pool)

	if len(password) < pp.MinimumLength {
		return &protocol.AWSError{
			Code:       "InvalidPasswordException",
			Message:    "Password did not conform with policy: Password not long enough",
			HTTPStatus: 400,
		}
	}

	if pp.RequireUppercase {
		hasUpper := false
		for _, ch := range password {
			if unicode.IsUpper(ch) {
				hasUpper = true
				break
			}
		}
		if !hasUpper {
			return &protocol.AWSError{
				Code:       "InvalidPasswordException",
				Message:    "Password did not conform with policy: Password must have uppercase characters",
				HTTPStatus: 400,
			}
		}
	}

	if pp.RequireLowercase {
		hasLower := false
		for _, ch := range password {
			if unicode.IsLower(ch) {
				hasLower = true
				break
			}
		}
		if !hasLower {
			return &protocol.AWSError{
				Code:       "InvalidPasswordException",
				Message:    "Password did not conform with policy: Password must have lowercase characters",
				HTTPStatus: 400,
			}
		}
	}

	if pp.RequireNumbers {
		hasDigit := false
		for _, ch := range password {
			if unicode.IsDigit(ch) {
				hasDigit = true
				break
			}
		}
		if !hasDigit {
			return &protocol.AWSError{
				Code:       "InvalidPasswordException",
				Message:    "Password did not conform with policy: Password must have numeric characters",
				HTTPStatus: 400,
			}
		}
	}

	if pp.RequireSymbols {
		hasSymbol := false
		for _, ch := range password {
			if !unicode.IsLetter(ch) && !unicode.IsDigit(ch) {
				hasSymbol = true
				break
			}
		}
		if !hasSymbol {
			return &protocol.AWSError{
				Code:       "InvalidPasswordException",
				Message:    "Password did not conform with policy: Password must have symbol characters",
				HTTPStatus: 400,
			}
		}
	}

	return nil
}
