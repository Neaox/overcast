package kms

import (
	"fmt"
	"net/http"

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

// errIncorrectKey reports that Decrypt's KeyId parameter names a key other
// than the one that produced the ciphertext. Per the API Reference: "When you
// use the KeyId parameter to specify a KMS key, AWS KMS only uses the KMS key
// you specify. If the ciphertext was encrypted under a different KMS key, the
// Decrypt operation fails" with IncorrectKeyException (HTTP 400).
// https://docs.aws.amazon.com/kms/latest/APIReference/API_Decrypt.html
func errIncorrectKey() *protocol.AWSError {
	return &protocol.AWSError{
		Code: "IncorrectKeyException",
		Message: "The key ID in your request is not valid for this ciphertext. " +
			"The KeyId in a Decrypt request must identify the same KMS key that was used to encrypt the ciphertext.",
		HTTPStatus: http.StatusBadRequest,
	}
}
