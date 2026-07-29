package config_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/config"
)

// docServiceNamesHeading is the section of docs/README.md that documents every
// accepted OVERCAST_SERVICES token. docs/cdk.md links into it by anchor.
const docServiceNamesHeading = "### Service names"

// docTokenRow matches a row of the Service names table, capturing the token
// from the first column: "| `s3` | S3 | `aws-s3` |".
var docTokenRow = regexp.MustCompile("^\\|\\s*`([a-z0-9]+)`\\s*\\|")

// TestServiceNamesDocMatchesAllServices is a drift tripwire for the
// hand-maintained token table in docs/README.md § Service names.
//
// That table can't be generated the way the service index is: capgen keys
// services by doc filename ("cloudwatch-logs", "elb") while OVERCAST_SERVICES
// keys them by config name ("logs", "elbv2"), and the CDK module column has no
// source in the codebase at all. So the table is written by hand, and this
// test pins it to reality — a service added to allServices without a matching
// row, or a row left behind after a rename, fails here instead of sending
// users to a token that doesn't exist.
//
// cfg.Services with OVERCAST_SERVICES unset is the full allServices set; see
// TestLoad_allServicesEnabled.
func TestServiceNamesDocMatchesAllServices(t *testing.T) {
	// Given: the default config, which enables every known service
	clearEnv(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// When: we read the documented token list out of the Service names table
	documented := parseDocumentedServiceTokens(t)

	// Then: every accepted token is documented
	for svc := range cfg.Services {
		if !documented[svc] {
			t.Errorf("service %q is accepted by OVERCAST_SERVICES but has no row in docs/README.md %s", svc, docServiceNamesHeading)
		}
	}

	// And: every documented token is actually accepted
	for svc := range documented {
		if !cfg.Services[svc] {
			t.Errorf("docs/README.md %s lists token %q, which OVERCAST_SERVICES rejects", docServiceNamesHeading, svc)
		}
	}
}

// parseDocumentedServiceTokens returns the set of tokens in the first column of
// the Service names table, failing the test on a duplicate row.
func parseDocumentedServiceTokens(t *testing.T) map[string]bool {
	t.Helper()

	path := filepath.Join("..", "..", "docs", "README.md")
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
		t.Fatalf("docs/README.md no longer contains a %q section; docs/cdk.md links to its anchor", docServiceNamesHeading)
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
			t.Errorf("token %q appears more than once in docs/README.md %s", m[1], docServiceNamesHeading)
		}
		tokens[m[1]] = true
	}
	if len(tokens) == 0 {
		t.Fatalf("found no token rows under %q in docs/README.md", docServiceNamesHeading)
	}
	return tokens
}
