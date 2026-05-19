//go:build !dev

package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRepoServiceCoverageParsesStatus(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)
	writeTestFile(t, root, "STATUS.md", `## Service coverage

### Comprehensive — core + advanced features
| Service | Ops | Highlights |
| --- | --- | --- |
| S3 | 27 | Bucket CRUD |

### Core operations — basic CRUD + common features
| Service | Ops | Highlights |
| --- | --- | --- |
| IAM | 33 | Users and roles |
`)
	out, err := provider.toolServiceCoverage(context.Background(), nil)
	if err != nil {
		t.Fatalf("toolServiceCoverage() error = %v", err)
	}
	got := out.(map[string]any)
	services := got["services"].([]serviceCoverageEntry)
	if len(services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(services))
	}
	if services[0].Service != "S3" || services[0].Tier != "comprehensive" {
		t.Fatalf("unexpected first coverage entry: %#v", services[0])
	}
	if services[1].Service != "IAM" || services[1].Tier != "core" {
		t.Fatalf("unexpected second coverage entry: %#v", services[1])
	}
}

func TestRepoServiceCoverageFallsBackToCodeDerivedInventory(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)
	writeTestFile(t, root, "internal/services/demo/handler.go", `package demo

import "net/http"

type Handler struct { ops map[string]http.HandlerFunc }

func (h *Handler) initOps() {
h.ops = map[string]http.HandlerFunc{
"CreateDemo": h.CreateDemo,
"GetDemo": h.GetDemo,
"DeleteDemo": h.DeleteDemo,
}
}
`)
	writeTestFile(t, root, "internal/services/demo/handler_stubs.go", `package demo

import "net/http"

func (h *Handler) DeleteDemo(w http.ResponseWriter, r *http.Request) {}
`)
	params, _ := json.Marshal(map[string]any{"service": "demo"})
	out, err := provider.toolServiceCoverage(context.Background(), params)
	if err != nil {
		t.Fatalf("toolServiceCoverage() error = %v", err)
	}
	got := out.(serviceCoverageEntry)
	if got.CoverageSource != "code" {
		t.Fatalf("expected code coverage source, got %#v", got)
	}
	if got.KnownOps == nil || *got.KnownOps != 3 {
		t.Fatalf("unexpected known ops: %#v", got.KnownOps)
	}
	if got.ImplementedOps == nil || *got.ImplementedOps != 2 {
		t.Fatalf("unexpected implemented ops: %#v", got.ImplementedOps)
	}
	if got.Ops == nil || *got.Ops != 2 {
		t.Fatalf("unexpected ops value: %#v", got.Ops)
	}
}

func TestRepoServiceCoverageMergesStatusAndCodeDerivedCounts(t *testing.T) {
	root := t.TempDir()
	provider := NewRepoProvider(root)
	writeTestFile(t, root, "STATUS.md", `## Service coverage

### Core operations — basic CRUD + common features
| Service | Ops | Highlights |
| --- | --- | --- |
| Demo | 9 | Demo summary |
`)
	writeTestFile(t, root, "internal/services/demo/handler.go", `package demo

import "net/http"

type Handler struct { ops map[string]http.HandlerFunc }

func (h *Handler) initOps() {
h.ops = map[string]http.HandlerFunc{
"CreateDemo": h.CreateDemo,
"GetDemo": h.GetDemo,
"DeleteDemo": h.DeleteDemo,
}
}
`)
	writeTestFile(t, root, "internal/services/demo/handler_stubs.go", `package demo

import "net/http"

func (h *Handler) DeleteDemo(w http.ResponseWriter, r *http.Request) {}
`)
	params, _ := json.Marshal(map[string]any{"service": "demo"})
	out, err := provider.toolServiceCoverage(context.Background(), params)
	if err != nil {
		t.Fatalf("toolServiceCoverage() error = %v", err)
	}
	got := out.(serviceCoverageEntry)
	if got.CoverageSource != "status-md+code" {
		t.Fatalf("expected merged coverage source, got %#v", got)
	}
	if got.Ops == nil || *got.Ops != 9 {
		t.Fatalf("expected STATUS.md ops to be preserved, got %#v", got.Ops)
	}
	if got.KnownOps == nil || *got.KnownOps != 3 {
		t.Fatalf("unexpected known ops: %#v", got.KnownOps)
	}
	if got.ImplementedOps == nil || *got.ImplementedOps != 2 {
		t.Fatalf("unexpected implemented ops: %#v", got.ImplementedOps)
	}
	if got.Highlights != "Demo summary" {
		t.Fatalf("unexpected highlights: %#v", got.Highlights)
	}
}
