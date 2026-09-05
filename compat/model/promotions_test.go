package compatmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The soak is a read-modify-write over the whole ledger: cmd/compat decodes the
// file, edits one entry and writes the result back. Every property asserted
// here is a way that round trip could quietly lose something a reader never
// saw.

// TestPromotionsRoundTripIsLossless runs over the committed ledger itself, not
// a fixture: the file cmd/compat rewrites nightly is the one that has to
// survive the trip byte for byte.
func TestPromotionsRoundTripIsLossless(t *testing.T) {
	tests := []struct {
		name     string
		contents string
	}{
		{
			name:     "the committed ledger",
			contents: readFile(t, "promotions.json"),
		},
		{
			name: "a ledger carrying every field",
			contents: `{
  "$schema": "./promotions.schema.json",
  "$comment": "why this file exists",
  "version": 1,
  "groups": {
    "sqs-gen-queue": {
      "state": "gated",
      "firstSeen": "2026-09-01",
      "promotedAt": "2026-09-04",
      "runs": [
        "run-1",
        "run-2",
        "run-3"
      ]
    },
    "sqs-gen-soaking": {
      "state": "candidate",
      "firstSeen": "2026-09-05"
    }
  }
}
`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			file, err := DecodePromotions([]byte(tc.contents))
			if err != nil {
				t.Fatalf("DecodePromotions() error = %v", err)
			}
			encoded, err := EncodePromotions(file)
			if err != nil {
				t.Fatalf("EncodePromotions() error = %v", err)
			}
			if string(encoded) != tc.contents {
				t.Fatalf("the round trip changed the file:\n--- want ---\n%s\n--- got ---\n%s", tc.contents, encoded)
			}
		})
	}
}

func TestDecodePromotions(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		wantErr  string
	}{
		{
			name:     "the minimum a schema-valid ledger can be",
			contents: `{"version": 1, "groups": {}}`,
		},
		{
			// The reason the reader is strict at all: the sole writer rewrites
			// the whole file from what it decoded, so a field it skipped is a
			// field it deletes.
			name:     "an unknown field is refused rather than dropped",
			contents: `{"version": 1, "groups": {}, "retiredAt": "2026-10-01"}`,
			wantErr:  "unknown field",
		},
		{
			name:     "an unknown field inside an entry is refused too",
			contents: `{"version": 1, "groups": {"g": {"state": "gated", "firstSeen": "2026-09-01", "demotedAt": "2026-10-01"}}}`,
			wantErr:  "unknown field",
		},
		{
			name:     "a future version is refused",
			contents: `{"version": 2, "groups": {}}`,
			wantErr:  "version 2 is not the 1 this build understands",
		},
		{
			// A file with no version at all decodes to zero, which is not this
			// version either — and must not be mistaken for one.
			name:     "a missing version is refused",
			contents: `{"groups": {}}`,
			wantErr:  "version 0 is not the 1 this build understands",
		},
		{
			name:     "trailing data is refused",
			contents: `{"version": 1, "groups": {}} {"version": 1, "groups": {}}`,
			wantErr:  "trailing data",
		},
		{
			name:     "malformed JSON is refused",
			contents: `{"version": 1,`,
			wantErr:  "unexpected EOF",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			file, err := DecodePromotions([]byte(tc.contents))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("DecodePromotions() error = %v, want nil", err)
				}
				if file.Groups == nil {
					t.Fatal("Groups is nil; a decoded ledger must always be writable")
				}
				return
			}
			if err == nil {
				t.Fatalf("DecodePromotions() = %+v, want an error containing %q", file, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("DecodePromotions() error = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
}

// TestReadPromotionsTreatsAMissingFileAsEmpty — a checkout that predates the
// soak has to generate exactly what it generated before, with every group a
// candidate.
func TestReadPromotionsTreatsAMissingFileAsEmpty(t *testing.T) {
	file, err := ReadPromotions(filepath.Join(t.TempDir(), "promotions.json"))
	if err != nil {
		t.Fatalf("ReadPromotions() error = %v", err)
	}
	if file.Version != PromotionsVersion || len(file.Groups) != 0 {
		t.Fatalf("file = %+v, want an empty v%d ledger", file, PromotionsVersion)
	}
}

// TestReadPromotionsNamesTheFile: the error a nightly reads has to say which
// file it could not parse, not only that something was wrong with a ledger.
func TestReadPromotionsNamesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "promotions.json")
	if err := os.WriteFile(path, []byte(`{"version": 99, "groups": {}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadPromotions(path)
	if err == nil {
		t.Fatal("ReadPromotions() error = nil, want a version refusal")
	}
	if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "version 99") {
		t.Fatalf("ReadPromotions() error = %v, want it to name %s and the version", err, path)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}
