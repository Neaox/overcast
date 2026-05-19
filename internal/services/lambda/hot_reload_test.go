package lambda

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeHotReloadPath_absoluteUnix(t *testing.T) {
	got, err := normalizeHotReloadPath("/Users/dev/project")
	if err != nil {
		t.Fatalf("normalizeHotReloadPath: %v", err)
	}
	if got != "/Users/dev/project" {
		t.Fatalf("path = %q, want %q", got, "/Users/dev/project")
	}
}

func TestNormalizeHotReloadPath_windowsDrive(t *testing.T) {
	got, err := normalizeHotReloadPath(`C:\Users\dev\project`)
	if err != nil {
		t.Fatalf("normalizeHotReloadPath: %v", err)
	}
	if got != "/c/Users/dev/project" {
		t.Fatalf("path = %q, want %q", got, "/c/Users/dev/project")
	}
}

func TestNormalizeHotReloadPath_relativeRejected(t *testing.T) {
	_, err := normalizeHotReloadPath("src/lambda")
	if err == nil {
		t.Fatal("expected relative path to be rejected")
	}
}

func TestValidateFunctionHotReloadConfig_layersAllowed(t *testing.T) {
	fn := &Function{
		Tags: map[string]string{hotReloadTagKey: "/workspace/fn"},
		Layers: []LayerVersionLink{{
			ARN: "arn:aws:lambda:us-east-1:000000000000:layer:demo:1",
		}},
	}
	got, err := validateFunctionHotReloadConfig(fn)
	if err != nil {
		t.Fatalf("expected layers to be allowed with hot reload, got error: %v", err)
	}
	if got != "/workspace/fn" {
		t.Fatalf("path = %q, want %q", got, "/workspace/fn")
	}
}

func TestHotReloadBindPath_disabledIgnoresTag(t *testing.T) {
	fn := &Function{Tags: map[string]string{hotReloadTagKey: "/workspace/fn"}}
	path, err := hotReloadBindPath(fn, false)
	if err != nil {
		t.Fatalf("hotReloadBindPath: %v", err)
	}
	if path != "" {
		t.Fatalf("path = %q, want empty when feature disabled", path)
	}
}

func TestDecorateHotReloadMountError_mountsDenied(t *testing.T) {
	err := decorateHotReloadMountError(errors.New("mounts denied"), "/Users/dev/proj")
	if err == nil {
		t.Fatal("expected decorated error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "File Sharing") {
		t.Fatalf("expected File Sharing hint, got: %q", msg)
	}
	if !strings.Contains(msg, "https://docs.docker.com/desktop/settings-and-maintenance/settings/#file-sharing") {
		t.Fatalf("expected Docker docs link, got: %q", msg)
	}
}

func TestTypeScriptSourceDiagnostic_tsOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "handler.ts"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	msg := typeScriptSourceDiagnostic(dir, "nodejs22.x")
	if msg == "" {
		t.Error("expected non-empty diagnostic for ts-only directory on nodejs22.x")
	}
	if !strings.Contains(msg, "nodejs24.x") {
		t.Errorf("expected upgrade hint in diagnostic, got: %s", msg)
	}
}

func TestTypeScriptSourceDiagnostic_node24SuppressesWarning(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "handler.ts"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, rt := range []string{"nodejs24.x", "nodejs26.x"} {
		if msg := typeScriptSourceDiagnostic(dir, rt); msg != "" {
			t.Errorf("runtime %s: expected no diagnostic (native type-stripping), got: %s", rt, msg)
		}
	}
}

func TestTypeScriptSourceDiagnostic_jsPresent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "handler.ts"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if msg := typeScriptSourceDiagnostic(dir, "nodejs22.x"); msg != "" {
		t.Errorf("expected no diagnostic when .js present, got: %s", msg)
	}
}

func TestTypeScriptSourceDiagnostic_noTsFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if msg := typeScriptSourceDiagnostic(dir, "nodejs22.x"); msg != "" {
		t.Errorf("expected no diagnostic for js-only directory, got: %s", msg)
	}
}

func TestTypeScriptSourceDiagnostic_nonExistentDir(t *testing.T) {
	if msg := typeScriptSourceDiagnostic("/nonexistent/path/xyz", "nodejs22.x"); msg != "" {
		t.Errorf("expected empty diagnostic for missing directory, got: %s", msg)
	}
}

func TestRuntimeSupportsTypeStripping(t *testing.T) {
	cases := []struct {
		runtime string
		want    bool
	}{
		{"nodejs24.x", true},
		{"nodejs26.x", true},
		{"nodejs22.x", false},
		{"nodejs20.x", false},
		{"python3.12", false},
		{"java21", false},
		{"", false},
	}
	for _, tc := range cases {
		got := runtimeSupportsTypeStripping(tc.runtime)
		if got != tc.want {
			t.Errorf("runtimeSupportsTypeStripping(%q) = %v, want %v", tc.runtime, got, tc.want)
		}
	}
}
