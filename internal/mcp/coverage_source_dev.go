//go:build dev

package mcp

// readServiceCoverage returns nil in dev builds. The capabilities.AllCapabilities
// static snapshot is the authoritative source of coverage data; STATUS.md parsing
// is unnecessary and skipped.
func (p *RepoProvider) readServiceCoverage() ([]serviceCoverageEntry, error) {
	return nil, nil
}
