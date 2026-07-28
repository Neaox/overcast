package awsapi

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGeneratedManifest_matchesRecordedDigest is the offline half of the model
// provenance gate.
//
// Proving the manifest matches upstream needs the Smithy corpus, which is not
// vendored, so `make aws-models-check` can only do that when a pinned checkout
// is supplied. This test closes the remaining gap on ordinary pull requests: it
// proves the committed manifest is still byte-for-byte the generator's own
// output, catching a hand-edit or a partial merge with no network and no models.
func TestGeneratedManifest_matchesRecordedDigest(t *testing.T) {
	// Given: the committed manifest and the provenance that describes it.
	manifestPath := "manifest.gen.go"
	contents, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read generated manifest: %v", err)
	}
	versionPath := filepath.Join("..", "..", "models", "aws", "VERSION")
	recorded, err := readVersionField(versionPath, "manifest-sha256")
	if err != nil {
		t.Fatalf("read model provenance: %v", err)
	}

	// When: the manifest is digested as the generator digests it.
	sum := sha256.Sum256(contents)
	got := hex.EncodeToString(sum[:])

	// Then: it matches the digest recorded alongside the pinned revision.
	if got != recorded {
		t.Fatalf("%s does not match the digest recorded in %s.\n"+
			"  recorded: %s\n  actual:   %s\n"+
			"Regenerate with `make generate-aws-operations` against the pinned "+
			"AWS model checkout rather than editing the generated file.",
			manifestPath, versionPath, recorded, got)
	}
}

// readVersionField returns a single `name=value` entry from a provenance file.
func readVersionField(path, name string) (string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	prefix := name + "="
	for _, line := range strings.Split(string(contents), "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), prefix); ok {
			return value, nil
		}
	}
	return "", os.ErrNotExist
}
