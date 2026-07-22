package appsync

import (
	"testing"
	"time"
)

func TestNormalizeAPIKeyExpires_cdkStyleUpperBound(t *testing.T) {
	// Given: a non-hour-aligned creation time and a CDK-style epoch expiry at 365 days
	now := time.Date(2026, 7, 22, 8, 44, 17, 0, time.UTC)
	expires := now.Add(365 * 24 * time.Hour).Unix()

	// When: AppSync validates the API key expiry
	got, err := normalizeAPIKeyExpires(now, expires)

	// Then: the value is accepted instead of being compared to a truncated upper bound
	if err != nil {
		t.Fatalf("expected expiry to be accepted, got %v", err)
	}
	if got != expires {
		t.Fatalf("expected expiry %d, got %d", expires, got)
	}
}
