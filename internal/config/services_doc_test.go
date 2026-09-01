package config_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/config"
)

// docServiceNamesHeading is the section of docs/configuration.md that
// documents every service name, and so every OVERCAST_STATE_<SERVICE> suffix.
// It used to live in docs/README.md, moved here when the reference monolith
// was split into standalone pages — see docs/README.md's own "Contents" index.
const docServiceNamesHeading = "## Service names"

// docTokenRow matches a row of the Service names table, capturing the token
// from the first column: "| `s3` | S3 | `aws-s3` |".
var docTokenRow = regexp.MustCompile("^\\|\\s*`([a-z0-9]+)`\\s*\\|")

// TestServiceNamesDocMatchesAllServices checks that the committed name table
// in docs/configuration.md § Service names still describes the services this
// build has. Those names are what OVERCAST_STATE_<SERVICE> is keyed by, so a
// wrong row sends a reader to an override that fails at startup.
//
// cmd/capgen generates that table from AllServices and refuses to run when its
// own service list has drifted, so the generator is the primary guard. This
// test covers the gap the generator cannot: docs/configuration.md is a
// committed artifact, and nothing forces a contributor to regenerate it. `make
// docs-check` catches that in CI by regenerating and diffing; this catches it
// in `go test`, without needing the dev build tag or a generator run, and it
// names the offending service directly.
func TestServiceNamesDocMatchesAllServices(t *testing.T) {
	// Given: the canonical service list
	services := config.AllServices()

	// When: we read the documented names out of the Service names table
	documented := parseDocumentedServiceTokens(t)

	// Then: every real service is documented
	for _, svc := range services {
		if !documented[svc] {
			t.Errorf("service %q exists but has no row in docs/configuration.md %s", svc, docServiceNamesHeading)
		}
	}

	// And: every documented name is a real service
	for svc := range documented {
		if !slices.Contains(services, svc) {
			t.Errorf("docs/configuration.md %s lists %q, which is not a known service", docServiceNamesHeading, svc)
		}
	}
}

// parseDocumentedServiceTokens returns the set of tokens in the first column of
// the Service names table, failing the test on a duplicate row.
func parseDocumentedServiceTokens(t *testing.T) map[string]bool {
	t.Helper()

	path := filepath.Join("..", "..", "docs", "configuration.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	lines := strings.Split(string(raw), "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == docServiceNamesHeading {
			start = i
			break
		}
	}
	if start == -1 {
		t.Fatalf("docs/configuration.md no longer contains a %q section", docServiceNamesHeading)
	}

	tokens := map[string]bool{}
	for _, line := range lines[start+1:] {
		// The table ends at the next heading.
		if strings.HasPrefix(line, "#") {
			break
		}
		m := docTokenRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if tokens[m[1]] {
			t.Errorf("token %q appears more than once in docs/configuration.md %s", m[1], docServiceNamesHeading)
		}
		tokens[m[1]] = true
	}
	if len(tokens) == 0 {
		t.Fatalf("found no token rows under %q in docs/configuration.md", docServiceNamesHeading)
	}
	return tokens
}
