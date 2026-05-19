package groups

import (
	"encoding/base64"
	"math/big"
	"strings"
)

// encodeBase64 returns the standard base64 encoding of b.
func encodeBase64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

// hashMidpointStr returns the decimal string midpoint between two decimal hash key strings.
func hashMidpointStr(start, end string) string {
	s := new(big.Int)
	e := new(big.Int)
	s.SetString(start, 10)
	e.SetString(end, 10)
	sum := new(big.Int).Add(s, e)
	mid := new(big.Int).Rsh(sum, 1) // divide by 2
	return mid.String()
}

// isAlreadyExists reports whether the error is an AWS "already exists" error,
// used to make create-* operations idempotent across test runs when the
// emulator retains state from a previous run.
func isAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "EntityAlreadyExists") ||
		strings.Contains(s, "BucketAlreadyOwnedByYou") ||
		strings.Contains(s, "ResourceInUseException") ||
		strings.Contains(s, "StateMachineAlreadyExists") ||
		strings.Contains(s, "AlreadyExistsException") ||
		strings.Contains(s, "already exists")
}
