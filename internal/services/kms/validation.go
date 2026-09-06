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
