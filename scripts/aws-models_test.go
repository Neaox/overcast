package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParsePin(t *testing.T) {
	const revision = "06544fdc2709916c72eba2f247529edf8d483d8a"
	tests := []struct {
		name     string
		contents string
		source   string
		wantErr  string
	}{
		{
			name: "the committed shape, comments and all",
			contents: "source=https://github.com/aws/api-models-aws\n" +
				"revision=" + revision + "\n" +
				"model-date=2026-08-31\n" +
				"license=Apache-2.0\n" +
				"\n" +
				"# revision=0000000000000000000000000000000000000000 in prose is not a pin\n",
			source: "https://github.com/aws/api-models-aws",
		},
		{
			name:     "surrounding whitespace",
			contents: "  source = https://example.invalid/models  \n\trevision =\t" + revision + "\t\n",
			source:   "https://example.invalid/models",
		},
		{
			name:     "no source",
			contents: "revision=" + revision + "\n",
			wantErr:  "no source= line",
		},
		{
			name:     "no revision",
			contents: "source=https://github.com/aws/api-models-aws\n",
			wantErr:  "not a full 40-character Git object name",
		},
		{
			name:     "abbreviated revision",
			contents: "source=https://github.com/aws/api-models-aws\nrevision=06544fdc\n",
			wantErr:  "not a full 40-character Git object name",
		},
		{
			name:     "a branch name is not a revision",
			contents: "source=https://github.com/aws/api-models-aws\nrevision=main\n",
			wantErr:  "not a full 40-character Git object name",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed, err := parsePin(test.contents)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("parsePin() error = %v, want it to mention %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parsePin() = %v", err)
			}
			if parsed.source != test.source {
				t.Errorf("source = %q, want %q", parsed.source, test.source)
			}
			if parsed.revision != revision {
				t.Errorf("revision = %q, want %q", parsed.revision, revision)
			}
		})
	}
}

// TestRepositoryPinParses is the one test that reads the real file: the format
// is written by awsmodelgen -version-file, and this program would be the last
// to know if it changed.
func TestRepositoryPinParses(t *testing.T) {
	parsed, err := readPin(filepath.Join("..", filepath.FromSlash(versionRelPath)))
	if err != nil {
		t.Fatalf("readPin(%s) = %v", versionRelPath, err)
	}
	if parsed.source != "https://github.com/aws/api-models-aws" {
		t.Errorf("source = %q, want the public api-models-aws repository", parsed.source)
	}
}

// TestCachePathDerivation pins the layout the issue specifies. The base is a
// parameter precisely so this holds on every GOOS: os.UserCacheDir already
// resolves to %LOCALAPPDATA% on Windows and $XDG_CACHE_HOME (or ~/.cache)
// elsewhere, so what is left to check is that the same three segments are
// appended to whatever it returns.
func TestCachePathDerivation(t *testing.T) {
	const revision = "06544fdc2709916c72eba2f247529edf8d483d8a"
	bases := []string{
		`C:\Users\dev\AppData\Local`,
		"/home/dev/.cache",
		"/Users/dev/Library/Caches",
		"relative/base",
	}
	for _, base := range bases {
		root := revisionRoot(base, revision)
		rel, err := filepath.Rel(base, root)
		if err != nil {
			t.Fatalf("revisionRoot(%q) = %q, not below the base: %v", base, root, err)
		}
		if got, want := filepath.ToSlash(rel), "overcast/aws-models/"+revision; got != want {
			t.Errorf("revisionRoot(%q) is %q below the base, want %q", base, got, want)
		}
		if parent := filepath.Dir(root); parent != revisionsDir(base) {
			t.Errorf("revisionsDir(%q) = %q, want the parent of %q", base, revisionsDir(base), root)
		}
	}
}

func TestCacheBase(t *testing.T) {
	t.Setenv(cacheEnv, "/from/the/environment")
	base, err := cacheBase("/from/the/flag")
	if err != nil {
		t.Fatalf("cacheBase() = %v", err)
	}
	if base != "/from/the/flag" {
		t.Errorf("the flag should win over %s, got %q", cacheEnv, base)
	}
	if base, err = cacheBase(""); err != nil || base != "/from/the/environment" {
		t.Errorf("cacheBase(\"\") = %q, %v, want the value of %s", base, err, cacheEnv)
	}
	t.Setenv(cacheEnv, "")
	base, err = cacheBase("")
	if err != nil {
		t.Fatalf("cacheBase() = %v", err)
	}
	userCache, err := os.UserCacheDir()
	if err != nil {
		t.Skipf("no user cache directory on this host: %v", err)
	}
	if base != userCache {
		t.Errorf("cacheBase() = %q, want os.UserCacheDir() %q", base, userCache)
	}
}

func TestUnionServices(t *testing.T) {
	tests := []struct {
		name string
		have []string
		want []string
		out  []string
	}{
		{name: "nothing cached", have: nil, want: []string{"s3", "acm"}, out: []string{"acm", "s3"}},
		{name: "widening", have: []string{"acm"}, want: []string{"s3"}, out: []string{"acm", "s3"}},
		{name: "already held", have: []string{"acm", "s3"}, want: []string{"acm"}, out: []string{"acm", "s3"}},
		{name: "duplicates collapse", have: []string{"acm"}, want: []string{"acm", "acm"}, out: []string{"acm"}},
		{name: "empty", have: nil, want: nil, out: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := unionServices(test.have, test.want); !slices.Equal(got, test.out) {
				t.Errorf("unionServices(%v, %v) = %v, want %v", test.have, test.want, got, test.out)
			}
		})
	}
}

func TestSparsePaths(t *testing.T) {
	got := sparsePaths([]string{"acm", "s3"})
	if want := []string{"models/acm", "models/s3"}; !slices.Equal(got, want) {
		t.Errorf("sparsePaths() = %v, want %v (git takes forward slashes on every platform)", got, want)
	}
}

func TestNormaliseService(t *testing.T) {
	if got, err := normaliseService("  ACM "); err != nil || got != "acm" {
		t.Errorf(`normaliseService("  ACM ") = %q, %v; want "acm"`, got, err)
	}
	for _, bad := range []string{"", "../etc", "a/b", `a\b`, "-leading", "with space"} {
		if _, err := normaliseService(bad); err == nil {
			t.Errorf("normaliseService(%q) was accepted; it would be pasted into a sparse-checkout path", bad)
		}
	}
}

func TestCheckoutCovers(t *testing.T) {
	full := checkout{Full: true}
	sparse := checkout{Services: []string{"acm", "s3"}}
	tests := []struct {
		name  string
		state checkout
		want  []string
		out   bool
	}{
		{name: "a full checkout covers the whole corpus", state: full, want: nil, out: true},
		{name: "a full checkout covers any service", state: full, want: []string{"lambda"}, out: true},
		{name: "a sparse checkout does not cover the whole corpus", state: sparse, want: nil, out: false},
		{name: "a sparse checkout covers what it holds", state: sparse, want: []string{"acm"}, out: true},
		{name: "and all of what it holds", state: sparse, want: []string{"acm", "s3"}, out: true},
		{name: "but not a service it does not hold", state: sparse, want: []string{"acm", "lambda"}, out: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.state.covers(test.want); got != test.out {
				t.Errorf("covers(%v) = %v, want %v", test.want, got, test.out)
			}
		})
	}
}

// TestSentinelRoundTrip covers the completeness marker itself: an unwritten or
// unreadable sentinel must read as "not complete", because that is what sends
// an interrupted fetch round again instead of trusting half a checkout.
func TestSentinelRoundTrip(t *testing.T) {
	root := tempDir(t)
	if _, ok := readSentinel(root); ok {
		t.Fatal("an empty directory read as a complete checkout")
	}
	state := checkout{Revision: "06544fdc2709916c72eba2f247529edf8d483d8a", Source: "https://example.invalid", Services: []string{"acm"}}
	if err := writeSentinel(root, state); err != nil {
		t.Fatalf("writeSentinel() = %v", err)
	}
	got, ok := readSentinel(root)
	if !ok {
		t.Fatal("the sentinel just written did not read back")
	}
	if got.Revision != state.Revision || got.Source != state.Source || !slices.Equal(got.Services, state.Services) {
		t.Errorf("readSentinel() = %+v, want %+v", got, state)
	}
	if err := os.WriteFile(filepath.Join(root, sentinelName), []byte("{ truncated"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := readSentinel(root); ok {
		t.Error("a truncated sentinel read as a complete checkout")
	}
}

// TestEnsureAgainstALocalRepository exercises the whole fetch path with no
// network: the source is a Git repository in a temporary directory holding the
// same models/<service>/... shape as upstream.
func TestEnsureAgainstALocalRepository(t *testing.T) {
	requireGitForTest(t)
	upstream, revision := fixtureRepository(t, "acm", "s3")
	cache := tempDir(t)
	versionFile := writePin(t, upstream, revision)
	root := revisionRoot(cache, revision)

	// The source is not passed: it comes from the pin, which is the default
	// this test is here to hold.
	stdout, _ := mustRun(t, "--ensure", "--service", "acm", "--cache-dir", cache, "--version-file", versionFile)
	if want := filepath.Join(root, "models") + "\n"; stdout != want {
		t.Fatalf("stdout = %q, want exactly %q", stdout, want)
	}
	assertDir(t, filepath.Join(root, "models", "acm"))
	assertNoDir(t, filepath.Join(root, "models", "s3"))
	assertServices(t, root, []string{"acm"}, false)

	// Second call, with the source pointed somewhere that does not exist: a
	// complete cache must be reused without any fetch at all.
	stdout, _ = mustRun(t, "--ensure", "--service", "acm", "--cache-dir", cache, "--version-file", versionFile,
		"--source", filepath.Join(upstream, "does-not-exist"))
	if want := filepath.Join(root, "models") + "\n"; stdout != want {
		t.Fatalf("the cached checkout was not reused: stdout = %q, want %q", stdout, want)
	}

	// Widening the same checkout to a second service.
	mustRun(t, "--ensure", "--service", "s3", "--cache-dir", cache, "--version-file", versionFile)
	assertDir(t, filepath.Join(root, "models", "acm"))
	assertDir(t, filepath.Join(root, "models", "s3"))
	assertServices(t, root, []string{"acm", "s3"}, false)

	// And widening it to the full corpus, which is what regenerating the
	// manifest needs.
	mustRun(t, "--ensure", "--cache-dir", cache, "--version-file", versionFile)
	assertDir(t, filepath.Join(root, "models", "acm"))
	assertDir(t, filepath.Join(root, "models", "s3"))
	assertServices(t, root, nil, true)
	assertDir(t, filepath.Join(root, "outside-models"))
}

// TestEnsureRedoesAnInterruptedFetch is the reason completeness is a sentinel
// rather than "the directory is there".
func TestEnsureRedoesAnInterruptedFetch(t *testing.T) {
	requireGitForTest(t)
	upstream, revision := fixtureRepository(t, "acm")
	cache := tempDir(t)
	versionFile := writePin(t, upstream, revision)
	root := revisionRoot(cache, revision)

	if err := os.MkdirAll(filepath.Join(root, "models"), 0o755); err != nil {
		t.Fatal(err)
	}
	leftover := filepath.Join(root, "models", "half-written.json")
	if err := os.WriteFile(leftover, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	mustRun(t, "--ensure", "--service", "acm", "--cache-dir", cache, "--version-file", versionFile)
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Errorf("%s survived: the incomplete directory was built on rather than replaced", leftover)
	}
	assertDir(t, filepath.Join(root, "models", "acm"))
	assertServices(t, root, []string{"acm"}, false)
}

func TestEnsureRejectsAnUnknownService(t *testing.T) {
	requireGitForTest(t)
	upstream, revision := fixtureRepository(t, "acm")
	cache := tempDir(t)
	versionFile := writePin(t, upstream, revision)

	var stdout, stderr bytes.Buffer
	err := run([]string{"--ensure", "--service", "nosuchservice", "--cache-dir", cache, "--version-file", versionFile}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "models/nosuchservice") {
		t.Fatalf("run() = %v, want it to report the missing service directory", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want nothing on a failure", stdout.String())
	}
}

func TestPruneKeepsThePinnedRevision(t *testing.T) {
	requireGitForTest(t)
	upstream, revision := fixtureRepository(t, "acm")
	cache := tempDir(t)
	versionFile := writePin(t, upstream, revision)
	mustRun(t, "--ensure", "--service", "acm", "--cache-dir", cache, "--version-file", versionFile)

	stale := revisionRoot(cache, "1111111111111111111111111111111111111111")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}

	_, stderr := mustRun(t, "--prune", "--dry-run", "--cache-dir", cache, "--version-file", versionFile)
	if !strings.Contains(stderr, "would remove "+stale) {
		t.Errorf("--dry-run stderr = %q, want it to name %s", stderr, stale)
	}
	assertDir(t, stale)

	stdout, stderr := mustRun(t, "--prune", "--cache-dir", cache, "--version-file", versionFile)
	if !strings.Contains(stderr, "removed "+stale) {
		t.Errorf("--prune stderr = %q, want it to name %s", stderr, stale)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing: only --ensure prints", stdout)
	}
	assertNoDir(t, stale)
	assertDir(t, revisionRoot(cache, revision))
}

func TestRunWithoutAnAction(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run(nil, &stdout, &stderr); err == nil {
		t.Fatal("run() with no flags succeeded, want the usage error")
	}
	if !strings.Contains(stderr.String(), "--ensure") {
		t.Errorf("stderr = %q, want the usage text", stderr.String())
	}
}

// --- helpers ---------------------------------------------------------------

func mustRun(t *testing.T, args ...string) (stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	if err := run(args, &out, &errOut); err != nil {
		t.Fatalf("run(%v) = %v\nstderr:\n%s", args, err, errOut.String())
	}
	return out.String(), errOut.String()
}

// tempDir is t.TempDir with a removal that copes with Git's read-only object
// files on Windows — the same reason removeTree exists.
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "aws-models-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := removeTree(dir); err != nil {
			t.Logf("could not remove %s: %v", dir, err)
		}
	})
	return dir
}

func requireGitForTest(t *testing.T) {
	t.Helper()
	if err := requireGit(); err != nil {
		t.Skipf("skipping: %v", err)
	}
}

// fixtureRepository builds a Git repository shaped like api-models-aws — a
// models/ directory with one directory per service, plus a file outside it so
// a sparse checkout is visibly narrower than a full one — and returns its path
// and the commit the pin should name.
func fixtureRepository(t *testing.T, services ...string) (dir, revision string) {
	t.Helper()
	dir = filepath.Join(tempDir(t), "api-models-aws")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, service := range services {
		modelDir := filepath.Join(dir, "models", service, "service", "2015-12-08")
		if err := os.MkdirAll(modelDir, 0o755); err != nil {
			t.Fatal(err)
		}
		model := `{"smithy":"2.0","shapes":{"com.amazonaws.` + service + `#Service":{"type":"service"}}}` + "\n"
		if err := os.WriteFile(filepath.Join(modelDir, service+"-2015-12-08.json"), []byte(model), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "outside-models"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "outside-models", "README.md"), []byte("not a model\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fixtureGit(t, dir, "init", "--initial-branch=main")
	fixtureGit(t, dir, "config", "user.email", "fixture@example.invalid")
	fixtureGit(t, dir, "config", "user.name", "Fixture")
	fixtureGit(t, dir, "config", "commit.gpgsign", "false")
	// Without this a partial clone falls back to a full one with a warning.
	fixtureGit(t, dir, "config", "uploadpack.allowfilter", "true")
	fixtureGit(t, dir, "add", "-A")
	fixtureGit(t, dir, "commit", "-m", "fixture models")
	out, err := gitOutput(dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return dir, strings.TrimSpace(out)
}

func fixtureGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func writePin(t *testing.T, source, revision string) string {
	t.Helper()
	path := filepath.Join(tempDir(t), "VERSION")
	contents := "source=" + source + "\nrevision=" + revision + "\nmodel-date=2026-08-31\n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertServices(t *testing.T, root string, services []string, full bool) {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(root, sentinelName))
	if err != nil {
		t.Fatalf("no sentinel in %s: %v", root, err)
	}
	var state checkout
	if err := json.Unmarshal(contents, &state); err != nil {
		t.Fatalf("sentinel is not JSON: %v", err)
	}
	if state.Full != full {
		t.Errorf("sentinel full = %v, want %v", state.Full, full)
	}
	if !slices.Equal(state.Services, services) {
		t.Errorf("sentinel services = %v, want %v", state.Services, services)
	}
}

func assertDir(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		t.Errorf("expected a directory at %s (%v)", path, err)
	}
}

func assertNoDir(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected %s not to exist (%v)", path, err)
	}
}
