package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTypedOpsFixture writes a minimal typed_ops.go-shaped file at dir that
// scanServices/extractOps/extractProtocols can parse: one op.NewTyped
// registration plus a []codec.Codec protocol list.
func writeTypedOpsFixture(t *testing.T, dir, pkgName, opName string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	src := `package ` + pkgName + `

var supportedProtocols = []codec.Codec{codec.JSON11{}}

var typedOps = map[string]op.Operation{
	"` + opName + `": op.NewTyped[fooRequest, fooResponse]("` + opName + `", handleFoo),
}
`
	if err := os.WriteFile(filepath.Join(dir, "typed_ops.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write typed_ops.go: %v", err)
	}
}

// TestScanServices_RecursesIntoNestedServiceDirs pins the fix for
// cmd/stub-report missing nested service packages such as
// internal/services/cloudwatch/logs. Before the fix, scanServices only
// looked one level deep under root, so a typed_ops.go living in a
// subdirectory of a top-level service directory (declared via subServices)
// was silently skipped and never appeared in docs/operation-manifest.md.
func TestScanServices_RecursesIntoNestedServiceDirs(t *testing.T) {
	root := t.TempDir()

	// A normal top-level service.
	writeTypedOpsFixture(t, filepath.Join(root, "widgets"), "widgets", "ListWidgets")

	// A nested service package, mirroring cloudwatch/logs: the parent
	// directory ("cloudwatch") exists but has no typed_ops.go of its own,
	// and the real package lives one level deeper.
	if err := os.MkdirAll(filepath.Join(root, "cloudwatch"), 0o755); err != nil {
		t.Fatalf("mkdir cloudwatch: %v", err)
	}
	writeTypedOpsFixture(t, filepath.Join(root, "cloudwatch", "logs"), "logs", "PutLogEvents")

	// Register the nested service the same way the real subServices map
	// declares cloudwatch/logs, without mutating package state permanently.
	origSubServices := subServices
	subServices = map[string]string{
		"cloudwatch-logs": filepath.Join("cloudwatch", "logs"),
	}
	t.Cleanup(func() { subServices = origSubServices })

	svcs, err := scanServices(root)
	if err != nil {
		t.Fatalf("scanServices: %v", err)
	}

	byName := make(map[string]serviceOps, len(svcs))
	for _, s := range svcs {
		byName[s.name] = s
	}

	if _, ok := byName["widgets"]; !ok {
		t.Errorf("expected top-level service %q in results, got %v", "widgets", names(svcs))
	}

	nested, ok := byName["cloudwatch-logs"]
	if !ok {
		t.Fatalf("expected nested service %q in results, got %v", "cloudwatch-logs", names(svcs))
	}
	if len(nested.ops) != 1 || nested.ops[0].name != "PutLogEvents" {
		t.Errorf("expected cloudwatch-logs to report PutLogEvents, got %+v", nested.ops)
	}

	// The bare parent directory (no typed_ops.go of its own) must not be
	// reported as a service.
	if _, ok := byName["cloudwatch"]; ok {
		t.Errorf("did not expect a bare %q entry (it has no typed_ops.go)", "cloudwatch")
	}
}

func names(svcs []serviceOps) []string {
	out := make([]string, 0, len(svcs))
	for _, s := range svcs {
		out = append(out, s.name)
	}
	return out
}

// TestFindWorkspaceRoot_WalksUpToGoMod pins the portable-path fix: the tool
// must locate the workspace root by walking up for go.mod instead of
// assuming a hardcoded container path like /workspace.
func TestFindWorkspaceRoot_WalksUpToGoMod(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/fixture\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	nested := filepath.Join(root, "internal", "services", "widgets")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got, err := findWorkspaceRoot(nested)
	if err != nil {
		t.Fatalf("findWorkspaceRoot: %v", err)
	}

	wantAbs, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if got != wantAbs {
		t.Errorf("findWorkspaceRoot(%q) = %q, want %q", nested, got, wantAbs)
	}
}
