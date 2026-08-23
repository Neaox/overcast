package lambda

// hot_reload_fingerprint_test.go — pins the identity contract for bind-mount
// hot reload: an edit to the mounted tree must move the function's code
// identity, because that identity is the only thing that retires the warm
// container serving the old code (#1411).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/clock"
)

// hotReloadTestFunction returns a function tagged for hot reload at dir, and an
// identity function that reads the tree afresh every time — the process-wide
// cache's rate limiter is not what these tests are pinning.
func hotReloadTestFunction(t *testing.T, dir string) (*Function, func() string) {
	t.Helper()
	fn := &Function{Name: "hot", Tags: map[string]string{hotReloadTagKey: dir}}
	saved := hotReloadFingerprints
	hotReloadFingerprints = newHotReloadFingerprintCache(clock.NewMock())
	t.Cleanup(func() { hotReloadFingerprints = saved })
	return fn, func() string { return functionInstanceIdentity(fn) }
}

func writeHotReloadFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// bumpMTime gives path an mtime a second in the future. Editing a file's
// content in place moves its mtime, but filesystem timestamp granularity is
// 1–2 s on some filesystems and Go's test loop is far faster than that, so the
// tests set the timestamp explicitly rather than sleeping to earn it. The
// granularity is a real boundary for users — see docs/services/lambda.md — but
// it is a property of the filesystem, not of the fingerprint.
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
	_, identity := hotReloadTestFunction(t, dir)
	before := identity()

	// When: an existing file is edited in place — the primary hot-reload
	// gesture, and the one the directory's own mtime never noticed.
	writeHotReloadFile(t, index, "exports.handler = async () => ({v: 2});\n")
	bumpMTime(t, index)

	// Then: the identity moves, so the pool retires the warm container and the
	// next invoke cold starts against the edited source.
	if got := identity(); got == before {
		t.Fatal("identity unchanged after an in-place edit — the warm container would serve the old module")
	}
}

func TestFunctionCodeIdentity_hotReload_movesOnSizeChangeAlone(t *testing.T) {
	// Given: a mounted source tree.
	dir := t.TempDir()
	index := filepath.Join(dir, "index.js")
	writeHotReloadFile(t, index, "// v1\n")
	_, identity := hotReloadTestFunction(t, dir)
	before := identity()

	// When: the file grows but its mtime is forced back to what it was — a
	// coarse-granularity filesystem collapsing two saves into one timestamp.
	st, err := os.Stat(index)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	writeHotReloadFile(t, index, "// v2 — a longer line than before\n")
	if err := os.Chtimes(index, st.ModTime(), st.ModTime()); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// Then: size is part of the fingerprint, so the edit is still seen.
	if got := identity(); got == before {
		t.Fatal("identity unchanged after a size change with an unchanged mtime")
	}
}

func TestFunctionCodeIdentity_hotReload_movesOnNestedEdit(t *testing.T) {
	// Given: a source tree with a subdirectory, as any real handler has.
	dir := t.TempDir()
	sub := filepath.Join(dir, "lib", "deep")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	nested := filepath.Join(sub, "util.js")
	writeHotReloadFile(t, nested, "module.exports = 1;\n")
	_, identity := hotReloadTestFunction(t, dir)
	before := identity()

	// When: a file two directories down is edited in place.
	writeHotReloadFile(t, nested, "module.exports = 2;\n")
	bumpMTime(t, nested)

	// Then: the identity moves — the walk is not limited to the top level.
	if got := identity(); got == before {
		t.Fatal("identity unchanged after an edit in a subdirectory")
	}
}

func TestFunctionCodeIdentity_hotReload_movesOnCreateDeleteRename(t *testing.T) {
	// Given: a mounted source tree. Directory-mtime identity already handled
	// these three; the fingerprint must be a superset of it, not a swap.
	dir := t.TempDir()
	writeHotReloadFile(t, filepath.Join(dir, "index.js"), "// index\n")
	_, identity := hotReloadTestFunction(t, dir)

	base := identity()
	writeHotReloadFile(t, filepath.Join(dir, "added.js"), "// added\n")
	created := identity()
	if created == base {
		t.Fatal("identity unchanged after a file was created")
	}

	if err := os.Rename(filepath.Join(dir, "added.js"), filepath.Join(dir, "renamed.js")); err != nil {
		t.Fatalf("rename: %v", err)
	}
	renamed := identity()
	if renamed == created {
		t.Fatal("identity unchanged after a file was renamed")
	}

	if err := os.Remove(filepath.Join(dir, "renamed.js")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if deleted := identity(); deleted == renamed {
		t.Fatal("identity unchanged after a file was deleted")
	}
}

func TestFunctionCodeIdentity_hotReload_stableWhenNothingChanged(t *testing.T) {
	// Given: a source tree with a few files and a subdirectory.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "lib"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeHotReloadFile(t, filepath.Join(dir, "index.js"), "// index\n")
	writeHotReloadFile(t, filepath.Join(dir, "lib", "util.js"), "// util\n")
	_, identity := hotReloadTestFunction(t, dir)

	// Then: repeated reads agree. An identity that flapped would cold start
	// every single invocation, which is worse than the bug being fixed.
	first := identity()
	for i := 0; i < 5; i++ {
		if got := identity(); got != first {
			t.Fatalf("identity moved on read %d with nothing changed: %q != %q", i, got, first)
		}
	}
}

func TestFunctionCodeIdentity_hotReload_missingPathIsStable(t *testing.T) {
	// Given: a tag pointing at a path this process cannot see — Overcast in a
	// container, where the bind mount is created for the sibling Lambda
	// container and never for Overcast itself.
	_, identity := hotReloadTestFunction(t, filepath.Join(t.TempDir(), "does-not-exist"))

	// Then: the identity is stable rather than erroring or flapping.
	if first, second := identity(), identity(); first != second {
		t.Fatalf("identity for an unreadable path is not stable: %q != %q", first, second)
	}
}

func TestHotReloadFingerprint_ignoresDependencyDirectoryContents(t *testing.T) {
	// Given: a source tree with a node_modules directory in it.
	dir := t.TempDir()
	writeHotReloadFile(t, filepath.Join(dir, "index.js"), "// index\n")
	deps := filepath.Join(dir, "node_modules", "left-pad")
	if err := os.MkdirAll(deps, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dep := filepath.Join(deps, "index.js")
	writeHotReloadFile(t, dep, "// v1\n")
	before := hotReloadFingerprint(dir)

	// When: a file inside node_modules is edited.
	writeHotReloadFile(t, dep, "// v2 — patched by hand\n")
	bumpMTime(t, dep)

	// Then: the fingerprint does not move — the documented boundary that keeps
	// a 30,000-file dependency tree off the invoke path.
	if got := hotReloadFingerprint(dir); got != before {
		t.Fatal("fingerprint moved on an edit inside node_modules — the walk is descending into it")
	}

	// And: the directory itself is still part of the fingerprint, so deleting
	// the dependencies is noticed.
	if err := os.RemoveAll(filepath.Join(dir, "node_modules")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got := hotReloadFingerprint(dir); got == before {
		t.Fatal("fingerprint unchanged after node_modules was removed entirely")
	}
}

func TestHotReloadFingerprint_truncatesBeyondTheEntryCap(t *testing.T) {
	// Given: a tree larger than the cap it is walked with. The cap is passed in
	// rather than building 20,000 files to reach the real one; what is being
	// pinned is the branch, which is the same branch either way.
	const maxEntries = 20
	dir := t.TempDir()
	for i := 0; i < maxEntries+10; i++ {
		writeHotReloadFile(t, filepath.Join(dir, fmt.Sprintf("f%03d.js", i)), "// x\n")
	}
	fingerprint := func() string { return hotReloadFingerprintBounded(dir, maxEntries, hotReloadWalkMaxDepth) }
	before := fingerprint()

	// When: a file well inside the cap is edited.
	early := filepath.Join(dir, "f001.js")
	writeHotReloadFile(t, early, "// edited\n")
	bumpMTime(t, early)
	if got := fingerprint(); got == before {
		t.Fatal("fingerprint unchanged for a file inside the entry cap")
	}
	before = fingerprint()

	// Then: an edit past the cap is invisible — the documented boundary — and,
	// more importantly, the fingerprint is still stable rather than flapping.
	// A fingerprint that moved on every read would cold start every invocation.
	late := filepath.Join(dir, fmt.Sprintf("f%03d.js", maxEntries+9))
	writeHotReloadFile(t, late, "// edited past the cap\n")
	bumpMTime(t, late)
	if got := fingerprint(); got != before {
		t.Fatal("fingerprint moved for a file past the entry cap — the walk is not bounded")
	}
	if a, b := fingerprint(), fingerprint(); a != b {
		t.Fatal("truncated fingerprint is not stable between reads")
	}
}

func TestHotReloadFingerprint_stopsAtTheDepthCap(t *testing.T) {
	// Given: a tree deeper than the depth cap it is walked with.
	const maxDepth = 2
	dir := t.TempDir()
	deep := filepath.Join(dir, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	shallow := filepath.Join(dir, "a", "b", "shallow.js")
	writeHotReloadFile(t, shallow, "// v1\n")
	buried := filepath.Join(deep, "buried.js")
	writeHotReloadFile(t, buried, "// v1\n")
	fingerprint := func() string { return hotReloadFingerprintBounded(dir, hotReloadWalkMaxEntries, maxDepth) }
	before := fingerprint()

	// When: a file at the last covered depth is edited, then one below it.
	writeHotReloadFile(t, shallow, "// v2\n")
	bumpMTime(t, shallow)
	if got := fingerprint(); got == before {
		t.Fatal("fingerprint unchanged for a file at the deepest covered level")
	}
	before = fingerprint()
	writeHotReloadFile(t, buried, "// v2\n")
	bumpMTime(t, buried)

	// Then: the edit below the cap is invisible and the fingerprint is stable.
	if got := fingerprint(); got != before {
		t.Fatal("fingerprint moved for a file below the depth cap — recursion is not bounded")
	}
}

func TestHotReloadFingerprintCache_rateLimitsByWhatTheWalkCost(t *testing.T) {
	// Given: a cache whose walk is expensive — 100 ms of the mock clock per
	// call — and counts how often it runs.
	mock := clock.NewMock()
	c := newHotReloadFingerprintCache(mock)
	walks := 0
	digest := "a"
	c.walk = func(string) string {
		walks++
		mock.Add(100 * time.Millisecond) // well over hotReloadWalkLatencyBudget
		return digest
	}

	// When: the tree is fingerprinted repeatedly.
	if got := c.digest("/src"); got != "a" {
		t.Fatalf("digest = %q, want %q", got, "a")
	}
	digest = "b" // the tree changed under us; only a re-walk can see it
	for i := 0; i < 10; i++ {
		mock.Add(100 * time.Millisecond)
		if got := c.digest("/src"); got != "a" {
			t.Fatalf("digest re-walked after %d ms; the rate limiter is not holding", (i+1)*100)
		}
	}

	// Then: it re-walks once the interval — the walk's own cost times the
	// factor — has passed, and not before.
	mock.Add(hotReloadWalkRateLimitFactor * 100 * time.Millisecond)
	if got := c.digest("/src"); got != "b" {
		t.Fatalf("digest = %q after the rate-limit interval elapsed, want a fresh walk", got)
	}
	if walks != 2 {
		t.Fatalf("walks = %d, want 2", walks)
	}
}

func TestHotReloadFingerprintCache_rateLimitIsCapped(t *testing.T) {
	// Given: a walk so slow that its cost times the factor exceeds the cap.
	mock := clock.NewMock()
	c := newHotReloadFingerprintCache(mock)
	digest := "a"
	c.walk = func(string) string {
		mock.Add(hotReloadWalkMaxInterval)
		return digest
	}
	c.digest("/src")
	digest = "b"

	// When: the cap has elapsed but the uncapped interval has not.
	mock.Add(hotReloadWalkMaxInterval)

	// Then: the tree is read again anyway — "the next invoke" degrades to
	// "within two seconds" and never to "never".
	if got := c.digest("/src"); got != "b" {
		t.Fatalf("digest = %q, want a re-walk once the interval cap elapsed", got)
	}
}

func TestHotReloadFingerprintCache_walkWithinTheBudgetIsNotRateLimited(t *testing.T) {
	// Given: a walk that costs real time but stays inside the latency budget —
	// a source tree on a local disk, which is the case the feature exists for.
	mock := clock.NewMock()
	c := newHotReloadFingerprintCache(mock)
	digest := "a"
	c.walk = func(string) string {
		mock.Add(hotReloadWalkLatencyBudget - time.Millisecond)
		return digest
	}
	c.digest("/src")

	// When: the tree changes and is read again immediately.
	digest = "b"

	// Then: nothing holds the re-read off. Rate-limiting a walk this cheap
	// would cost the "picked up on the next invoke" promise to save time
	// nobody can perceive — which is exactly how the first cut of this got it
	// wrong, and why the end-to-end test kept seeing the pre-edit handler.
	if got := c.digest("/src"); got != "b" {
		t.Fatalf("digest = %q, want the edit to be visible on the very next read", got)
	}
}

func TestFunctionCodeIdentity_nonHotReloadUntouched(t *testing.T) {
	// Given: an ordinary zip-packaged function and an image-packaged one.
	zipped := &Function{Name: "zip"}
	zipped.setCode([]byte("package-bytes"))
	image := &Function{Name: "img", PackageType: "Image", ImageUri: "example.com/fn:1"}

	// Then: identity is still the stored code hash / image hash — no walk, no
	// cache, no filesystem access on the invoke path for these.
	if got := functionCodeIdentity(zipped); got != zipped.CodeHash {
		t.Fatalf("zip identity = %q, want the stored CodeHash %q", got, zipped.CodeHash)
	}
	if got := functionCodeIdentity(image); got != imageHash("example.com/fn:1") {
		t.Fatalf("image identity = %q, want the image hash", got)
	}
}

func TestHotReloadLocalPath_prefersTheReadableForm(t *testing.T) {
	// Given: a real directory, and the Docker-form path hostpath.Normalize
	// produces for it — which on Windows names nothing on disk.
	dir := t.TempDir()
	normalized, err := normalizeHotReloadPath(dir)
	if err != nil {
		t.Fatalf("normalizeHotReloadPath: %v", err)
	}

	// Then: the path used to read the tree is one this process can actually
	// open, whichever of the two that is.
	local := hotReloadLocalPath(dir, normalized)
	if _, err := os.Stat(local); err != nil {
		t.Fatalf("hotReloadLocalPath returned %q, which cannot be read: %v", local, err)
	}

	// And: a raw path that does not resolve falls back to the normalized form
	// rather than to something unreadable of its own.
	if got := hotReloadLocalPath("/no/such/raw", "/no/such/normalized"); got != "/no/such/normalized" {
		t.Fatalf("hotReloadLocalPath = %q, want the normalized form as the fallback", got)
	}
}

func TestHotReloadVisibilityDiagnostic(t *testing.T) {
	// Given: a directory this process can read, and one it cannot.
	dir := t.TempDir()

	// Then: nothing is said about the readable one.
	if msg := hotReloadVisibilityDiagnostic(dir); msg != "" {
		t.Fatalf("unexpected warning for a readable path: %q", msg)
	}

	// And: the unreadable one is named, along with what it costs — this is the
	// containerized-Overcast case, where the mount works and change detection
	// silently cannot.
	msg := hotReloadVisibilityDiagnostic(filepath.Join(dir, "not-mounted-here"))
	if msg == "" {
		t.Fatal("expected a warning when Overcast cannot read the mounted tree")
	}
	for _, want := range []string{"not-mounted-here", "warm execution environment", "same path"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("warning %q does not mention %q", msg, want)
		}
	}
}

// ─── Benchmarks — the numbers behind the constants ───────────────────────────

func benchmarkTree(b *testing.B, dirs, filesPerDir int) string {
	b.Helper()
	root := b.TempDir()
	for d := 0; d < dirs; d++ {
		dir := filepath.Join(root, fmt.Sprintf("pkg%03d", d))
		if err := os.MkdirAll(dir, 0o750); err != nil {
			b.Fatalf("mkdir: %v", err)
		}
		for f := 0; f < filesPerDir; f++ {
			path := filepath.Join(dir, fmt.Sprintf("mod%04d.js", f))
			if err := os.WriteFile(path, []byte("module.exports = {};\n"), 0o600); err != nil {
				b.Fatalf("write: %v", err)
			}
		}
	}
	return root
}

// BenchmarkHotReloadFingerprint_sourceTree measures the shape the feature is
// for: a hand-written handler, 20 directories of 10 modules.
func BenchmarkHotReloadFingerprint_sourceTree(b *testing.B) {
	root := benchmarkTree(b, 20, 10)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = hotReloadFingerprint(root)
	}
}

// BenchmarkHotReloadFingerprint_dependencyTree measures the hazard: 30,000
// files under node_modules next to the same handler. The skip list is what
// keeps this at the cost of the tree above.
func BenchmarkHotReloadFingerprint_dependencyTree(b *testing.B) {
	root := benchmarkTree(b, 20, 10)
	deps := filepath.Join(root, "node_modules")
	for d := 0; d < 300; d++ {
		dir := filepath.Join(deps, fmt.Sprintf("dep%03d", d))
		if err := os.MkdirAll(dir, 0o750); err != nil {
			b.Fatalf("mkdir: %v", err)
		}
		for f := 0; f < 100; f++ {
			path := filepath.Join(dir, fmt.Sprintf("f%03d.js", f))
			if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil {
				b.Fatalf("write: %v", err)
			}
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = hotReloadFingerprint(root)
	}
}

// BenchmarkHotReloadFingerprint_atTheEntryCap measures the backstop: a tree
// large enough to hit hotReloadWalkMaxEntries, which is what one walk costs
// when a tag is pointed somewhere it should not be.
func BenchmarkHotReloadFingerprint_atTheEntryCap(b *testing.B) {
	root := benchmarkTree(b, 220, 100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = hotReloadFingerprint(root)
	}
}
