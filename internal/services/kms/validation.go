package kms

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/overcast-sh/overcast/internal/protocol"
)

// KMS request-parameter constraints, taken from the AWS KMS API Reference.
const (
	// minNumberOfBytes/maxNumberOfBytes are the Valid Range shared by
	// GenerateDataKey, GenerateDataKeyWithoutPlaintext and GenerateRandom
	// ("Minimum value of 1. Maximum value of 1024").
	// https://docs.aws.amazon.com/kms/latest/APIReference/API_GenerateDataKey.html
	// https://docs.aws.amazon.com/kms/latest/APIReference/API_GenerateRandom.html
	minNumberOfBytes = 1
	maxNumberOfBytes = 1024

	// maxPlaintextBytes is Encrypt's Plaintext Length Constraints maximum
	// ("Minimum length of 1. Maximum length of 4096") — the SYMMETRIC_DEFAULT
	// limit, the only encryption this emulator performs.
	// https://docs.aws.amazon.com/kms/latest/APIReference/API_Encrypt.html
	maxPlaintextBytes = 4096

	// ScheduleKeyDeletion's PendingWindowInDays Valid Range ("Minimum value of
	// 7. Maximum value of 30"), defaulting to 30 when omitted.
	// https://docs.aws.amazon.com/kms/latest/APIReference/API_ScheduleKeyDeletion.html
	minPendingWindowInDays     = 7
	maxPendingWindowInDays     = 30
	defaultPendingWindowInDays = 30

	// CreateAlias's AliasName rules: Length Constraints "Minimum length of 1.
	// Maximum length of 256", Pattern `^alias/[a-zA-Z0-9/_-]+$`, and "The
	// alias name cannot begin with alias/aws/. The alias/aws/ prefix is
	// reserved for AWS managed keys."
	// https://docs.aws.amazon.com/kms/latest/APIReference/API_CreateAlias.html
	aliasNamePrefix     = "alias/"
	reservedAliasPrefix = "alias/aws/"
	maxAliasNameLength  = 256
)

// errValidation returns the 400 ValidationException KMS uses for a request
// that parsed cleanly but violates a modeled parameter constraint. The
// operation-specific Errors sections in the API Reference do not list it —
// constraint violations are rejected by the request-validation layer common to
// every operation (CommonErrors § ValidationError, HTTP 400).
func errValidation(msg string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ValidationException",
		Message:    msg,
		HTTPStatus: http.StatusBadRequest,
	}
}

// errRange builds the constraint-violation message shape AWS uses for a
// numeric member outside its Valid Range.
func errRange(member string, value, minimum, maximum int) *protocol.AWSError {
	bound := fmt.Sprintf("Member must have value greater than or equal to %d", minimum)
	if value > maximum {
		bound = fmt.Sprintf("Member must have value less than or equal to %d", maximum)
	}
	return errValidation(fmt.Sprintf(
		"1 validation error detected: Value '%d' at '%s' failed to satisfy constraint: %s",
		value, member, bound))
}

// dataKeyLength resolves the length of the data key a GenerateDataKey or
// GenerateDataKeyWithoutPlaintext request asks for.
//
// Per the API Reference: "You must specify either the KeySpec or the
// NumberOfBytes parameter (but not both) in every GenerateDataKey request."
// Supplying both, or neither, is a ValidationException.
// https://docs.aws.amazon.com/kms/latest/APIReference/API_GenerateDataKey.html
func dataKeyLength(keySpec string, numberOfBytes *int) (int, *protocol.AWSError) {
	switch {
	case keySpec != "" && numberOfBytes != nil,
		keySpec == "" && numberOfBytes == nil:
		return 0, errValidation("Please specify either number of bytes or key spec.")
	case numberOfBytes != nil:
		if aerr := validateNumberOfBytes(*numberOfBytes); aerr != nil {
			return 0, aerr
		}
		return *numberOfBytes, nil
	}
	switch keySpec {
	case "AES_256":
		return 32, nil
	case "AES_128":
		return 16, nil
	default:
		// KeySpec Valid Values: AES_256 | AES_128. Falling back to 32 bytes
		// for anything else is the same wrong-length-material fault as
		// ignoring NumberOfBytes.
		return 0, errValidation(fmt.Sprintf(
			"1 validation error detected: Value '%s' at 'keySpec' failed to satisfy constraint: "+
				"Member must satisfy enum value set: [AES_256, AES_128]", keySpec))
	}
}

// validateNumberOfBytes enforces the shared 1–1024 Valid Range.
func validateNumberOfBytes(n int) *protocol.AWSError {
	if n < minNumberOfBytes || n > maxNumberOfBytes {
		return errRange("numberOfBytes", n, minNumberOfBytes, maxNumberOfBytes)
	}
	return nil
}

// validatePlaintextLength enforces Encrypt's 4096-byte Plaintext limit. That
// limit is the whole reason envelope encryption exists, so an emulator that
// accepts more lets someone build a design AWS refuses.
func validatePlaintextLength(n int) *protocol.AWSError {
	if n > maxPlaintextBytes {
		return errValidation(fmt.Sprintf(
			"1 validation error detected: Value at 'plaintext' failed to satisfy constraint: "+
				"Member must have length less than or equal to %d", maxPlaintextBytes))
	}
	return nil
}

// pendingWindowInDays resolves ScheduleKeyDeletion's waiting period: omitted
// means 30, a supplied value must be 7-30 inclusive.
func pendingWindowInDays(days *int) (int, *protocol.AWSError) {
	if days == nil {
		return defaultPendingWindowInDays, nil
	}
	if *days < minPendingWindowInDays || *days > maxPendingWindowInDays {
		return 0, errRange("pendingWindowInDays", *days, minPendingWindowInDays, maxPendingWindowInDays)
	}
	return *days, nil
}

// errIncorrectKey reports that the caller named a key other than the one that
// produced the ciphertext — Decrypt's KeyId, or ReEncrypt's SourceKeyId. Both
// operations raise the same exception, and its documented description names
// both: "The request was rejected because the specified KMS key cannot decrypt
// the data. The KeyId in a Decrypt request and the SourceKeyId in a ReEncrypt
// request must identify the same KMS key that was used to encrypt the
// ciphertext." (HTTP 400).
// https://docs.aws.amazon.com/kms/latest/APIReference/API_Decrypt.html
// https://docs.aws.amazon.com/kms/latest/APIReference/API_ReEncrypt.html
func errIncorrectKey() *protocol.AWSError {
	return &protocol.AWSError{
		Code: "IncorrectKeyException",
		Message: "The request was rejected because the specified KMS key cannot decrypt the data. " +
			"The KeyId in a Decrypt request and the SourceKeyId in a ReEncrypt request " +
			"must identify the same KMS key that was used to encrypt the ciphertext.",
		HTTPStatus: http.StatusBadRequest,
	}
}

// errInvalidAliasName returns CreateAlias's documented
// InvalidAliasNameException (HTTP 400, "The request was rejected because the
// specified alias name is not valid").
//
// AWS's request-validation layer can also reject a badly shaped name with the
// generic ValidationException raised by the AliasName pattern constraint. The
// operation-modeled exception is used here because it is the one the reference
// documents for CreateAlias and the one SDK error handling branches on
// (types.InvalidAliasNameException), and it covers the reserved alias/aws/
// prefix too, which no pattern constraint can express.
// https://docs.aws.amazon.com/kms/latest/APIReference/API_CreateAlias.html
func errInvalidAliasName(name, reason string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "InvalidAliasNameException",
		Message:    fmt.Sprintf("Invalid alias name %q: %s", name, reason),
		HTTPStatus: http.StatusBadRequest,
	}
}

// errAliasExists returns CreateAlias's AlreadyExistsException (HTTP 400, "The
// request was rejected because it attempted to create a resource that already
// exists"). Re-pointing an existing alias goes through UpdateAlias, and that
// distinction is what makes alias-based key rotation safe.
func errAliasExists(aliasARN string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "AlreadyExistsException",
		Message:    fmt.Sprintf("An alias with the name %s already exists", aliasARN),
		HTTPStatus: http.StatusBadRequest,
	}
}

// validateAliasName enforces CreateAlias's AliasName rules: the alias/ prefix,
// the 256-character maximum, the `^alias/[a-zA-Z0-9/_-]+$` pattern, and the
// alias/aws/ prefix reserved for AWS managed keys.
func validateAliasName(name string) *protocol.AWSError {
	if len(name) > maxAliasNameLength {
		return errInvalidAliasName(name, fmt.Sprintf("alias names are at most %d characters", maxAliasNameLength))
	}
	if !strings.HasPrefix(name, aliasNamePrefix) || len(name) == len(aliasNamePrefix) {
		return errInvalidAliasName(name, `alias names must begin with "alias/" followed by a name, such as "alias/ExampleAlias"`)
	}
	if strings.HasPrefix(name, reservedAliasPrefix) {
		return errInvalidAliasName(name, `the "alias/aws/" prefix is reserved for AWS managed keys`)
	}
	for _, c := range name[len(aliasNamePrefix):] {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '/', c == '_', c == '-':
		default:
			return errInvalidAliasName(name,
				"alias names may contain only alphanumeric characters, forward slashes (/), underscores (_) and dashes (-)")
		}
	}
	return nil
}
