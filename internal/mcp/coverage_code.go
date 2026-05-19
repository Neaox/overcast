//go:build !dev

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// mergeCodeDerivedCoverage augments the coverage list with op counts derived
// from AST scanning of handler files. This regex-based approach is the fallback
// used in non-dev builds where capabilities.AllCapabilities is not available.
func (p *RepoProvider) mergeCodeDerivedCoverage(existing []serviceCoverageEntry) []serviceCoverageEntry {
	byService := make(map[string]serviceCoverageEntry, len(existing))
	for _, entry := range existing {
		byService[strings.ToLower(entry.Service)] = entry
	}
	servicesRoot := filepath.Join(p.workspaceRoot, "internal", "services")
	entries, err := os.ReadDir(servicesRoot)
	if err != nil {
		return existing
	}
	for _, dirEntry := range entries {
		if !dirEntry.IsDir() {
			continue
		}
		service := strings.ToLower(dirEntry.Name())
		codeEntry, ok := p.codeDerivedCoverageForService(service)
		if !ok {
			continue
		}
		if existingEntry, found := byService[service]; found {
			existingEntry.KnownOps = codeEntry.KnownOps
			existingEntry.ImplementedOps = codeEntry.ImplementedOps
			existingEntry.CoverageSource = "status-md+code"
			if existingEntry.Ops == nil && codeEntry.ImplementedOps != nil {
				existingEntry.Ops = codeEntry.ImplementedOps
			}
			byService[service] = existingEntry
			continue
		}
		byService[service] = codeEntry
	}
	merged := make([]serviceCoverageEntry, 0, len(byService))
	for _, entry := range byService {
		merged = append(merged, entry)
	}
	sort.Slice(merged, func(i, j int) bool {
		return strings.ToLower(merged[i].Service) < strings.ToLower(merged[j].Service)
	})
	return merged
}

func (p *RepoProvider) codeDerivedCoverageForService(service string) (serviceCoverageEntry, bool) {
	service = strings.ToLower(strings.TrimSpace(service))
	if service == "" {
		return serviceCoverageEntry{}, false
	}
	serviceRoot := filepath.Join(p.workspaceRoot, "internal", "services", service)
	if !fileExists(serviceRoot) {
		return serviceCoverageEntry{}, false
	}
	known, implemented, ok := countServiceOperations(serviceRoot)
	if !ok {
		return serviceCoverageEntry{}, false
	}
	entry := serviceCoverageEntry{
		Service:        service,
		Tier:           inferTierFromImplementedOps(implemented),
		Highlights:     "code-derived operation inventory",
		CoverageSource: "code",
	}
	entry.KnownOps = &known
	entry.ImplementedOps = &implemented
	entry.Ops = &implemented
	return entry, true
}

func countServiceOperations(serviceRoot string) (known int, implemented int, ok bool) {
	files, err := os.ReadDir(serviceRoot)
	if err != nil {
		return 0, 0, false
	}
	mapEntryRE := regexp.MustCompile(`"([A-Za-z0-9_]+)"\s*:\s*[A-Za-z_][A-Za-z0-9_]*\.([A-Za-z0-9_]+)`)
	methodRE := regexp.MustCompile(`func \([^)]+\) ([A-Za-z0-9_]+)\(`)
	stubTargets := map[string]struct{}{
		"notImplemented": {},
		"stub":           {},
	}
	stubMethods := map[string]struct{}{}
	ops := map[string]string{}
	for _, entry := range files {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		abs := filepath.Join(serviceRoot, entry.Name())
		b, readErr := os.ReadFile(abs)
		if readErr != nil {
			continue
		}
		source := string(b)
		for _, match := range methodRE.FindAllStringSubmatch(source, -1) {
			if strings.Contains(entry.Name(), "handler_stubs") {
				stubMethods[match[1]] = struct{}{}
			}
		}
		for _, match := range mapEntryRE.FindAllStringSubmatch(source, -1) {
			ops[match[1]] = match[2]
		}
	}
	if len(ops) == 0 {
		return 0, 0, false
	}
	known = len(ops)
	implemented = known
	for _, target := range ops {
		if _, isGenericStub := stubTargets[target]; isGenericStub {
			implemented--
			continue
		}
		if _, isStubMethod := stubMethods[target]; isStubMethod {
			implemented--
		}
	}
	if implemented < 0 {
		implemented = 0
	}
	return known, implemented, true
}

func inferTierFromImplementedOps(implemented int) string {
	switch {
	case implemented >= 25:
		return "comprehensive"
	case implemented >= 8:
		return "core"
	default:
		return "minimal"
	}
}

// toolServiceCapabilities is a stub in non-dev builds.
func (p *RepoProvider) toolServiceCapabilities(_ context.Context, _ json.RawMessage) (any, error) {
	return nil, fmt.Errorf("repo_service_capabilities requires a dev build (built with -tags dev)")
}
