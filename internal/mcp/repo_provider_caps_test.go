//go:build dev

package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

// TestRepoServiceCoverageDevUsesCapabilitiesOnly verifies that in a dev build
// toolServiceCoverage uses capabilities.AllCapabilities exclusively and does
// not read STATUS.md (the file is absent and no error is expected).
func TestRepoServiceCoverageDevUsesCapabilitiesOnly(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)
	// No STATUS.md written — dev builds skip it entirely.
	out, err := provider.toolServiceCoverage(context.Background(), nil)
	if err != nil {
		t.Fatalf("toolServiceCoverage() error = %v", err)
	}
	got := out.(map[string]any)
	services := got["services"].([]serviceCoverageEntry)
	// Dev build derives all services from AllCapabilities — must be >= 27.
	if len(services) < 27 {
		t.Fatalf("expected all services from AllCapabilities, got %d", len(services))
	}
	// Every entry must be backed by capabilities, not STATUS.md.
	for _, svc := range services {
		if svc.CoverageSource == "status-md" {
			t.Errorf("service %q has status-md source in dev build (expected capabilities)", svc.Service)
		}
	}
}

// TestRepoServiceCoverageDevLooksUpKnownService verifies that in a dev build
// toolServiceCoverage returns capabilities data for a known service (sqs).
func TestRepoServiceCoverageDevLooksUpKnownService(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)
	params, _ := json.Marshal(map[string]any{"service": "sqs"})
	out, err := provider.toolServiceCoverage(context.Background(), params)
	if err != nil {
		t.Fatalf("toolServiceCoverage(sqs) error = %v", err)
	}
	got := out.(serviceCoverageEntry)
	if got.CoverageSource != "capabilities" {
		t.Fatalf("expected capabilities coverage source, got %#v", got.CoverageSource)
	}
	if got.KnownOps == nil || *got.KnownOps == 0 {
		t.Fatalf("expected sqs to have KnownOps, got %v", got.KnownOps)
	}
}

// TestRepoServiceCapabilitiesToolReturnsSQSOps verifies the new
// repo_service_capabilities MCP tool returns per-operation details.
func TestRepoServiceCapabilitiesToolReturnsSQSOps(t *testing.T) {
	provider := NewRepoProvider(t.TempDir())
	params, _ := json.Marshal(map[string]any{"service": "sqs"})
	out, err := provider.toolServiceCapabilities(context.Background(), params)
	if err != nil {
		t.Fatalf("toolServiceCapabilities(sqs) error = %v", err)
	}
	got := out.(map[string]any)
	if got["service"] != "sqs" {
		t.Fatalf("unexpected service: %v", got["service"])
	}
	count, _ := got["count"].(int)
	if count == 0 {
		t.Fatal("expected non-zero op count for sqs")
	}
	ops, _ := got["operations"].([]struct {
		Operation string `json:"operation"`
		Category  string `json:"category"`
		Status    string `json:"status"`
		Notes     string `json:"notes,omitempty"`
	})
	_ = ops
}

// TestRepoServiceCapabilitiesToolRejectsUnknownService verifies that an unknown
// service returns an error rather than an empty result.
func TestRepoServiceCapabilitiesToolRejectsUnknownService(t *testing.T) {
	provider := NewRepoProvider(t.TempDir())
	params, _ := json.Marshal(map[string]any{"service": "notaservice"})
	_, err := provider.toolServiceCapabilities(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for unknown service, got nil")
	}
}
