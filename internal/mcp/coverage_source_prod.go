//go:build !dev

package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// readServiceCoverage parses the hand-maintained tier tables in STATUS.md.
// Used in non-dev builds where capabilities.AllCapabilities is not available.
func (p *RepoProvider) readServiceCoverage() ([]serviceCoverageEntry, error) {
	b, err := os.ReadFile(filepath.Join(p.workspaceRoot, "STATUS.md"))
	if err != nil {
		return nil, fmt.Errorf("read STATUS.md: %w", err)
	}
	var coverage []serviceCoverageEntry
	var tier string
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(raw)
		switch line {
		case "### Comprehensive — core + advanced features":
			tier = "comprehensive"
			continue
		case "### Core operations — basic CRUD + common features":
			tier = "core"
			continue
		case "### Minimal / Stub":
			tier = "minimal"
			continue
		}
		if tier == "" || !strings.HasPrefix(line, "|") {
			continue
		}
		parts := splitMarkdownRow(line)
		if len(parts) < 3 || parts[0] == "Service" || strings.HasPrefix(parts[0], "---") {
			continue
		}
		entry := serviceCoverageEntry{Service: parts[0], Tier: tier, Highlights: parts[len(parts)-1]}
		if len(parts) >= 2 {
			if ops, err := strconv.Atoi(parts[1]); err == nil {
				entry.Ops = &ops
			}
		}
		entry.CoverageSource = "status-md"
		coverage = append(coverage, entry)
	}
	return coverage, nil
}
