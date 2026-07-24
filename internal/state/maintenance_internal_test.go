//go:build !nosqlite

package state

import "testing"

// TestShouldVacuum exercises the freelist-ratio gating logic (3.5) in
// isolation, without a real *sql.DB — see shouldVacuum's doc comment for why
// it's a pure function.
func TestShouldVacuum(t *testing.T) {
	tests := []struct {
		name          string
		freelistCount int64
		pageCount     int64
		want          bool
	}{
		{"empty database has no pages", 0, 0, false},
		{"zero free pages never vacuums", 0, 1000, false},
		{"just under the threshold", 149, 1000, false},                    // 14.9%
		{"exactly at the threshold is not > threshold", 150, 1000, false}, // 15.0%
		{"just over the threshold", 151, 1000, true},                      // 15.1%
		{"heavily bloated database", 900, 1000, true},
		{"entire database is free pages", 1000, 1000, true},
		{"negative page count is treated as empty", 10, -1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldVacuum(tt.freelistCount, tt.pageCount); got != tt.want {
				t.Errorf("shouldVacuum(%d, %d) = %v, want %v", tt.freelistCount, tt.pageCount, got, tt.want)
			}
		})
	}
}
