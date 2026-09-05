// Script: aws-models
// Fetches the AWS Smithy models pinned by models/aws/VERSION into a per-user
// cache and prints the directory `awsmodelgen -models` accepts.
//
// Usage:
//
//	go run ./scripts/aws-models.go --ensure                     # full checkout
//	go run ./scripts/aws-models.go --ensure --service acm       # one service
//	go run ./scripts/aws-models.go --ensure --service acm,s3    # several
//	go run ./scripts/aws-models.go --prune                      # drop other revisions
//	go run ./scripts/aws-models.go --prune --dry-run            # say what --prune would drop
//
// It composes, which is the whole point — the directory is the only thing on
// stdout, every progress line and git's own output goes to stderr:
//
//	make aws-models-check AWS_MODELS_DIR="$(go run ./scripts/aws-models.go --ensure)"
//	make generate-aws-operations AWS_MODELS_DIR="$(go run ./scripts/aws-models.go --ensure)"
//
// Why a cache outside the repository: this project's workflow puts every task
// in its own worktree, routinely ten or more at once, and the corpus is large.
// One copy per revision under the user cache directory is shared by all of
// them, stays out of `git status`, ripgrep and editor indexing, and survives
// deleting a worktree. os.UserCacheDir already encodes the per-platform
// location — %LOCALAPPDATA% on Windows, $XDG_CACHE_HOME (or ~/.cache) on
// Linux, ~/Library/Caches on macOS — so the layout is the same everywhere:
//
//	<user cache>/overcast/aws-models/<revision>/models
//
// Why it never needs updating: it fetches the exact revision recorded in
// models/aws/VERSION, never "latest". Keeping that pin current is the AWS API
// model refresh workflow's job. A revision's cache is therefore immutable and
// cannot go stale — it is either present or fetched.
//
// Completeness is a sentinel file written only after a fetch has fully
// succeeded, so a clone interrupted halfway is redone rather than trusted. The
// sentinel also records which services a sparse checkout holds, which is how a
// later --service for a service that is not there widens the same checkout
// instead of refetching it.
//
// Unlike the other programs in this directory this file is NOT `//go:build
// ignore`: it is the one with unit tests beside it, and staying in the default
// build is what lets `go test ./scripts/...`, `go vet ./...` and
// golangci-lint see both files. It is still run the same way, and it is still
// not part of the compiled binary — nothing outside scripts/ imports it.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const (
	// cacheVendorDir and cacheKindDir namespace the cache under whatever
	// os.UserCacheDir returns, so nothing else in it is ever a candidate for
	// --prune.
	cacheVendorDir = "overcast"
	cacheKindDir   = "aws-models"

	// sentinelName marks a checkout as complete. It is written last, after the
	// clone, the sparse-checkout and the detached checkout have all succeeded.
	sentinelName = ".overcast-aws-models.json"

	// versionRelPath is the pin, relative to the repository root.
	versionRelPath = "models/aws/VERSION"

	// sourceEnv and cacheEnv exist for the tests, and for a contributor who
	// has to point at a mirror. The default source is always the URL recorded
	// in models/aws/VERSION.
	sourceEnv = "OVERCAST_AWS_MODELS_SOURCE"
	cacheEnv  = "OVERCAST_AWS_MODELS_CACHE"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "aws-models: %v\n", err)
		os.Exit(1)
	}
}

// serviceList collects a repeatable --service flag. A single occurrence may
// also carry a comma-separated list.
type serviceList []string

// String renders the flag's current value for flag package diagnostics.
func (l *serviceList) String() string { return strings.Join(*l, ",") }

// Set records one --service occurrence, splitting it on commas.
func (l *serviceList) Set(value string) error {
	for _, name := range strings.Split(value, ",") {
		name, err := normaliseService(name)
		if err != nil {
			return err
		}
		*l = append(*l, name)
	}
	return nil
}

// serviceNamePattern is what an upstream directory under models/ looks like.
// Rejecting anything else is what keeps a --service value from escaping the
// sparse-checkout path it is pasted into.
var serviceNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// normaliseService lowercases a service name — the upstream directories are
// lowercase — and rejects one that could not name a directory under models/.
func normaliseService(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !serviceNamePattern.MatchString(name) {
		return "", fmt.Errorf("invalid service name %q: expected a directory name under models/, such as acm", name)
	}
	return name, nil
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("aws-models", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var services serviceList
	ensureFlag := flags.Bool("ensure", false, "fetch the pinned models if needed and print their directory on stdout")
	pruneFlag := flags.Bool("prune", false, "delete cached revisions other than the one pinned by "+versionRelPath)
	dryRun := flags.Bool("dry-run", false, "with --prune, report what would be deleted without deleting it")
	sourceFlag := flags.String("source", "", "upstream repository to clone (default: the source recorded in "+versionRelPath+"; env "+sourceEnv+")")
	cacheFlag := flags.String("cache-dir", "", "base cache directory (default: os.UserCacheDir; env "+cacheEnv+")")
	versionFlag := flags.String("version-file", "", "path to the pin (default: "+versionRelPath+", found by walking up from the working directory)")
	flags.Var(&services, "service", "fetch only this service's models; repeatable, and accepts a comma-separated list")
	flags.Usage = func() {
		fmt.Fprintf(stderr, "Usage: go run ./scripts/aws-models.go --ensure [--service <name>]...\n")
		fmt.Fprintf(stderr, "       go run ./scripts/aws-models.go --prune [--dry-run]\n\n")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if !*ensureFlag && !*pruneFlag {
		flags.Usage()
		return errors.New("nothing to do: pass --ensure, --prune, or both")
	}
	if len(services) > 0 && !*ensureFlag {
		return errors.New("--service only applies to --ensure")
	}

	versionFile, err := locateVersionFile(*versionFlag)
	if err != nil {
		return err
	}
	pinned, err := readPin(versionFile)
	if err != nil {
		return err
	}
	base, err := cacheBase(*cacheFlag)
	if err != nil {
		return err
	}

	if *pruneFlag {
		if err := prune(base, pinned.revision, *dryRun, stderr); err != nil {
			return err
		}
	}
	if !*ensureFlag {
		return nil
	}

	source := pinned.source
	if env := strings.TrimSpace(os.Getenv(sourceEnv)); env != "" {
		source = env
	}
	if *sourceFlag != "" {
		source = *sourceFlag
	}
	modelsDir, err := ensure(base, pinned.revision, source, slices.Clone(services), stderr)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, modelsDir)
	return nil
}

// pin is what models/aws/VERSION says about the upstream corpus. Only the two
// fields this program needs are read; the digests belong to awsmodelgen.
type pin struct {
	source   string
	revision string
}

// revisionPattern is a full Git object name. The pin is always one — the
// refresh workflow writes it from `git rev-parse` — and refusing anything else
// is what keeps a mangled pin from becoming a cache directory name.
var revisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// parsePin reads the `key=value` lines of a models/aws/VERSION file, ignoring
// blank lines and `#` comments.
func parsePin(contents string) (pin, error) {
	var parsed pin
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "source":
			parsed.source = strings.TrimSpace(value)
		case "revision":
			parsed.revision = strings.TrimSpace(value)
		}
	}
	if parsed.source == "" {
		return pin{}, errors.New("no source= line")
	}
	if !revisionPattern.MatchString(parsed.revision) {
		return pin{}, fmt.Errorf("revision=%q is not a full 40-character Git object name", parsed.revision)
	}
	return parsed, nil
}

func readPin(path string) (pin, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return pin{}, fmt.Errorf("read the pin: %w", err)
	}
	parsed, err := parsePin(string(contents))
	if err != nil {
		return pin{}, fmt.Errorf("read %s: %w", path, err)
	}
	return parsed, nil
}

// locateVersionFile walks up from the working directory looking for the pin,
// so the script works from a subdirectory as well as from the repository root.
func locateVersionFile(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	rel := filepath.FromSlash(versionRelPath)
	for {
		candidate := filepath.Join(dir, rel)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no %s above the working directory: run this from the repository, or pass --version-file", versionRelPath)
		}
		dir = parent
	}
}

// cacheBase resolves the directory the per-revision checkouts live under.
func cacheBase(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if env := strings.TrimSpace(os.Getenv(cacheEnv)); env != "" {
		return env, nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate the user cache directory (set %s to choose one): %w", cacheEnv, err)
	}
	return dir, nil
}

// revisionsDir is where every cached revision lives; revisionRoot is one of
// them. Both are pure path derivation, which is why base is a parameter.
func revisionsDir(base string) string { return filepath.Join(base, cacheVendorDir, cacheKindDir) }

func revisionRoot(base, revision string) string { return filepath.Join(revisionsDir(base), revision) }

// checkout is the sentinel: what a complete cache directory holds. Full is a
// checkout of the whole corpus, which is what regenerating the manifest needs;
// otherwise Services lists the sparse cone, sorted.
type checkout struct {
	Revision string   `json:"revision"`
	Source   string   `json:"source"`
	Full     bool     `json:"full"`
	Services []string `json:"services,omitempty"`
}

// covers reports whether this checkout already holds everything wanted. An
// empty want is a request for the whole corpus, which only a full checkout
// covers.
func (c checkout) covers(want []string) bool {
	if c.Full {
		return true
	}
	if len(want) == 0 {
		return false
	}
	for _, service := range want {
		if !slices.Contains(c.Services, service) {
			return false
		}
	}
	return true
}

func readSentinel(root string) (checkout, bool) {
	contents, err := os.ReadFile(filepath.Join(root, sentinelName))
	if err != nil {
		return checkout{}, false
	}
	var state checkout
	if err := json.Unmarshal(contents, &state); err != nil {
		return checkout{}, false
	}
	return state, true
}

func writeSentinel(root string, state checkout) error {
	contents, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, sentinelName), append(contents, '\n'), 0o644)
}

// unionServices merges the cone a checkout already holds with the one asked
// for, sorted and deduplicated, so widening is one `sparse-checkout set`.
func unionServices(have, want []string) []string {
	merged := append(slices.Clone(have), want...)
	slices.Sort(merged)
	return slices.Compact(merged)
}

// sparsePaths turns service names into the paths `git sparse-checkout set`
// takes. Git wants forward slashes on every platform.
func sparsePaths(services []string) []string {
	paths := make([]string, 0, len(services))
	for _, service := range services {
		paths = append(paths, "models/"+service)
	}
	return paths
}

// ensure returns the models directory for the pinned revision, fetching it
// first if the cache does not already hold what was asked for.
func ensure(base, revision, source string, want []string, stderr io.Writer) (string, error) {
	want = unionServices(nil, want)
	root := revisionRoot(base, revision)
	modelsDir := filepath.Join(root, "models")

	// A sentinel is the only evidence a directory here is a complete checkout,
	// so it is also the only thing that lets this return without touching the
	// network.
	state, complete := readSentinel(root)
	if complete && state.covers(want) {
		fmt.Fprintf(stderr, "aws-models: reusing the cached checkout of %s\n", revision)
		return modelsDir, verifyServices(modelsDir, want)
	}
	if err := requireGit(); err != nil {
		return "", err
	}

	target := checkout{Revision: revision, Source: source}
	if len(want) == 0 {
		target.Full = true
	} else {
		target.Services = unionServices(state.Services, want)
	}

	if complete {
		fmt.Fprintf(stderr, "aws-models: widening the cached checkout of %s\n", revision)
		if err := widen(root, target, stderr); err != nil {
			return "", err
		}
	} else {
		// No sentinel means either nothing is here or a previous fetch was
		// interrupted. A half-finished clone is not something to build on, so
		// it goes and the fetch starts again.
		if err := removeTree(root); err != nil {
			return "", fmt.Errorf("clear the incomplete cache directory %s: %w", root, err)
		}
		fmt.Fprintf(stderr, "aws-models: fetching %s at %s\n", source, revision)
		if err := clone(root, source, revision, target, stderr); err != nil {
			return "", err
		}
	}

	if err := verifyRevision(root, revision); err != nil {
		return "", err
	}
	if err := verifyServices(modelsDir, want); err != nil {
		return "", err
	}
	if err := writeSentinel(root, target); err != nil {
		return "", fmt.Errorf("mark the checkout complete: %w", err)
	}
	fmt.Fprintf(stderr, "aws-models: ready at %s\n", root)
	return modelsDir, nil
}

// clone fetches the corpus without its blobs and then materialises exactly the
// cone that was asked for. --no-checkout keeps the clone from populating the
// default branch's tree before the sparse cone is known; the detached checkout
// afterwards is what puts the working tree at the pinned revision, which is
// what awsmodelgen re-checks with `git rev-parse HEAD`.
func clone(root, source, revision string, target checkout, stderr io.Writer) error {
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		return err
	}
	if err := git(stderr, "", "clone", "--filter=blob:none", "--no-checkout", source, root); err != nil {
		return fmt.Errorf("clone %s: %w", source, err)
	}
	if !target.Full {
		if err := git(stderr, root, append([]string{"sparse-checkout", "set", "--cone"}, sparsePaths(target.Services)...)...); err != nil {
			return err
		}
	}
	if err := git(stderr, root, "-c", "advice.detachedHead=false", "checkout", "--detach", revision); err != nil {
		return fmt.Errorf("check out %s (the pin may name a commit this source does not have): %w", revision, err)
	}
	return nil
}

// widen re-sets the sparse cone of a checkout that is already at the pinned
// revision. Git fetches the newly-visible blobs from the promisor remote as it
// updates the working tree.
func widen(root string, target checkout, stderr io.Writer) error {
	if target.Full {
		return git(stderr, root, "sparse-checkout", "disable")
	}
	return git(stderr, root, append([]string{"sparse-checkout", "set", "--cone"}, sparsePaths(target.Services)...)...)
}

// verifyRevision asserts what awsmodelgen will assert, at the point where the
// error can still say something useful.
func verifyRevision(root, revision string) error {
	out, err := gitOutput(root, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("read the cached checkout's revision: %w", err)
	}
	if got := strings.TrimSpace(out); got != revision {
		return fmt.Errorf("the cached checkout is at %s, not the pinned %s", got, revision)
	}
	return nil
}

// verifyServices catches the mistake sparse-checkout does not: a cone naming a
// directory the corpus does not have is accepted silently and produces an
// empty checkout.
func verifyServices(modelsDir string, want []string) error {
	if len(want) == 0 {
		if info, err := os.Stat(modelsDir); err != nil || !info.IsDir() {
			return fmt.Errorf("%s is missing from the checkout", modelsDir)
		}
		return nil
	}
	for _, service := range want {
		dir := filepath.Join(modelsDir, service)
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			return fmt.Errorf("the pinned corpus has no models/%s: check the service directory name upstream", service)
		}
	}
	return nil
}

// prune deletes every cached revision except the pinned one. Deliberately
// conservative: the pin on the current branch is the only revision it can be
// sure is wanted, so it keeps that one and nothing else.
func prune(base, keep string, dryRun bool, stderr io.Writer) error {
	dir := revisionsDir(base)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(stderr, "aws-models: nothing cached under %s\n", dir)
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", dir, err)
	}
	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == keep {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if dryRun {
			fmt.Fprintf(stderr, "aws-models: would remove %s\n", path)
			removed++
			continue
		}
		if err := removeTree(path); err != nil {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		fmt.Fprintf(stderr, "aws-models: removed %s\n", path)
		removed++
	}
	if removed == 0 {
		fmt.Fprintf(stderr, "aws-models: nothing to prune; %s is the only cached revision\n", keep)
	}
	return nil
}

// removeTree is os.RemoveAll with one retry that clears the read-only bit
// first. Git marks pack files and loose objects read-only, and on Windows a
// read-only file cannot be deleted — so the plain RemoveAll that works on
// macOS and Linux fails with "Access is denied" on exactly the directories
// this program creates.
func removeTree(path string) error {
	err := os.RemoveAll(path)
	if err == nil {
		return nil
	}
	if _, statErr := os.Lstat(path); errors.Is(statErr, os.ErrNotExist) {
		return nil
	}
	_ = filepath.WalkDir(path, func(name string, _ os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		_ = os.Chmod(name, 0o700)
		return nil
	})
	return os.RemoveAll(path)
}

// requireGit fails with something a contributor can act on rather than letting
// exec report "executable file not found".
func requireGit() error {
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("git is not on PATH: install Git to fetch the pinned AWS models")
	}
	return nil
}

func git(stderr io.Writer, dir string, args ...string) error {
	fmt.Fprintf(stderr, "aws-models: git %s\n", strings.Join(args, " "))
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// Everything git says goes to stderr, including what it writes to stdout:
	// stdout carries the models directory and nothing else.
	cmd.Stdout = stderr
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}
