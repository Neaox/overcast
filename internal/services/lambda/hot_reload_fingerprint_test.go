package lambda

// hot_reload_fingerprint_test.go — pins the identity contract for bind-mount
// hot reload: an edit to the mounted tree must move the function's code
// identity, because that identity is the only thing that retires the warm
// container serving the old code (#1411).

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeHotReloadFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// bumpMTime gives path an mtime two seconds in the future. Editing a file's
// content in place moves its mtime, but filesystem timestamp granularity is
// 1–2 s on some filesystems and Go's test loop is far faster than that, so the
// tests set the timestamp explicitly rather than sleeping to earn it. The
// granularity is a real boundary for users — see docs/services/lambda.md — but
// it is a property of the filesystem, not of the identity.
func bumpMTime(t *testing.T, path string) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	future := st.ModTime().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

func TestFunctionCodeIdentity_hotReload_movesOnInPlaceEdit(t *testing.T) {
	// Given: a mounted source tree that has already been invoked against.
	dir := t.TempDir()
	index := filepath.Join(dir, "index.js")
	writeHotReloadFile(t, index, "exports.handler = async () => ({v: 1});\n")
	fn := &Function{Name: "hot", Tags: map[string]string{hotReloadTagKey: dir}}
	before := functionInstanceIdentity(fn)

	// When: an existing file is edited in place — the primary hot-reload
	// gesture, and the one the directory's own mtime never notices, because a
	// directory's mtime moves only when an entry is created, deleted or
	// renamed inside it.
	writeHotReloadFile(t, index, "exports.handler = async () => ({v: 2});\n")
	bumpMTime(t, index)

	// Then: the identity moves, so the pool retires the warm container and the
	// next invoke cold starts against the edited source.
	if got := functionInstanceIdentity(fn); got == before {
		t.Fatal("identity unchanged after an in-place edit — the warm container would serve the old module")
	}
}
