package helpers

// Re-export testify's require and assert packages so tests can import them
// via a single helpers import rather than adding two separate imports.
//
// Usage in tests:
//
//	helpers.Require(t).NoError(err)
//	helpers.Require(t).Equal(http.StatusOK, resp.StatusCode)
//	helpers.Assert(t).Contains(body, "NoSuchBucket")
//
// require stops the test immediately on failure (like t.Fatal).
// assert records the failure and continues (like t.Error).

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Require returns a testify Assertions value that calls t.FailNow on failure.
func Require(t *testing.T) *require.Assertions {
	t.Helper()
	return require.New(t)
}

// Assert returns a testify Assertions value that calls t.Fail on failure.
func Assert(t *testing.T) *assert.Assertions {
	t.Helper()
	return assert.New(t)
}
