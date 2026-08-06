// Command verify runs CI checks scoped to what the current branch changed.
// It exists so the same gate runs identically on every platform —
// Windows, macOS, and Linux — without shell-specific differences.
//
// Usage:
//
//	go run ./cmd/verify          run the outstanding checks
//	go run ./cmd/verify -force   run them even if the cache says they passed
//	go run ./cmd/verify -record go   record that you ran Go checks yourself
//
// Exit codes: 0 all passed or nothing to do, 2 a check failed.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type changeSet struct {
	goFiles  []string
	webFiles []string
}

func main() {
	force := flag.Bool("force", false, "re-run checks even when cached")
	record := flag.String("record", "", "record a passing cache entry ('go' or 'web')")
	flag.Parse()

	root := gitRoot()
	if root == "" {
		os.Exit(0)
	}

	if *record != "" {
		recordPass(root, *record)
		return
	}

	cs := findChanges(root)
	if len(cs.goFiles) == 0 && len(cs.webFiles) == 0 {
		os.Exit(0)
	}

	failed := runChecks(root, cs, *force)
	if len(failed) > 0 {
		fmt.Fprintf(os.Stderr, "verify-changed: FAILED: %s\n", strings.Join(failed, " "))
		fmt.Fprintf(os.Stderr, "\nThese are required CI jobs. Fix them before pushing, or re-run the push if you\n")
		fmt.Fprintf(os.Stderr, "have a reason to override. Full gate: `make check` (fmt vet lint test).\n")
		os.Exit(2)
	}
}

// ---- discovery ---------------------------------------------------------------

func gitRoot() string {
	out, err := runCmd("", "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func findChanges(root string) changeSet {
	base := mergeBase(root)
	if base == "" {
		return changeSet{}
	}
	paths := changedPaths(root, base)
	var cs changeSet
	for _, p := range paths {
		if strings.Contains(p, ".go") && !strings.Contains(p, "compat/suites/") {
			cs.goFiles = append(cs.goFiles, p)
		}
		if strings.HasPrefix(p, "web/src/") ||
			(strings.HasPrefix(p, "web/") && (strings.HasSuffix(p, ".ts") || strings.HasSuffix(p, ".json") || strings.HasSuffix(p, ".js"))) {
			cs.webFiles = append(cs.webFiles, p)
		}
	}
	return cs
}

func mergeBase(root string) string {
	out, err := runCmd(root, "git", "merge-base", "HEAD", "origin/main")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func changedPaths(root, base string) []string {
	var seen = make(map[string]bool)

	addLines := func(s string) {
		for _, line := range strings.Split(s, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				seen[line] = true
			}
		}
	}

	if out, err := runCmd(root, "git", "diff", "--name-only", base+"...HEAD"); err == nil {
		addLines(out)
	}
	if out, err := runCmd(root, "git", "diff", "--name-only"); err == nil {
		addLines(out)
	}
	if out, err := runCmd(root, "git", "ls-files", "--others", "--exclude-standard"); err == nil {
		addLines(out)
	}

	var result []string
	for p := range seen {
		result = append(result, p)
	}
	return result
}

// ---- checks ------------------------------------------------------------------

func runChecks(root string, cs changeSet, force bool) (failed []string) {
	golangciLintVersion := readGolangciLintVersion(root)

	if len(cs.goFiles) > 0 {
		if f := checkGoLint(root, golangciLintVersion, force); f != "" {
			failed = append(failed, f)
		}
		if fs := checkVetTags(root, force); len(fs) > 0 {
			failed = append(failed, fs...)
		}
	}
	if len(cs.webFiles) > 0 {
		if f := checkWeb(root, force); f != "" {
			failed = append(failed, f)
		}
	}
	return failed
}

func checkGoLint(root, version string, force bool) string {
	key := "golangci-lint"
	fp := fingerprintGoLint(root, version)
	if !force && cacheHit(root, key, fp) {
		fmt.Fprintf(os.Stderr, "verify-changed: already green, nothing changed since: %s (--force to re-run)\n", key)
		return ""
	}
	reason := runGolangciLint(version)
	invalidate := reason != ""
	cachePut(root, key, fp, invalidate)
	return reason
}

func checkVetTags(root string, force bool) []string {
	key := "vet-tags"
	fp := fingerprintGo(root)
	if !force && cacheHit(root, key, fp) {
		fmt.Fprintf(os.Stderr, "verify-changed: already green, nothing changed since: %s (--force to re-run)\n", key)
		return nil
	}
	var failed []string
	for _, tagset := range []string{"slim", "slim,nosqlite", "slim,dev"} {
		label := fmt.Sprintf("vet(-tags %s)", tagset)
		if err := runGoVet(root, tagset); err != nil {
			failed = append(failed, label)
		}
	}
	invalidate := len(failed) > 0
	cachePut(root, key, fp, invalidate)
	return failed
}

func checkWeb(root string, force bool) string {
	key := "web"
	fp := fingerprintWeb(root)
	if !force && cacheHit(root, key, fp) {
		fmt.Fprintf(os.Stderr, "verify-changed: already green, nothing changed since: %s (--force to re-run)\n", key)
		return ""
	}
	var parts []string
	if err := runPnpm(root, "run", "typecheck"); err != nil {
		parts = append(parts, "pnpm-typecheck")
	}
	if err := runPnpm(root, "run", "lint"); err != nil {
		parts = append(parts, "pnpm-lint")
	}
	reason := strings.Join(parts, " ")
	invalidate := reason != ""
	cachePut(root, key, fp, invalidate)
	return reason
}

// ---- tool invocations --------------------------------------------------------

func runGolangciLint(version string) string {
	pkg := fmt.Sprintf("github.com/golangci/golangci-lint/v2/cmd/golangci-lint@%s", version)
	if _, err := exec.LookPath("golangci-lint"); err == nil {
		if _, err := runCmd("", "golangci-lint", "run", "./..."); err != nil {
			return " golangci-lint"
		}
		return ""
	}
	if _, err := exec.LookPath("go"); err == nil {
		if _, err := runCmd("", "go", "run", pkg, "run", "./..."); err != nil {
			return " golangci-lint"
		}
		return ""
	}
	if _, err := exec.LookPath("docker"); err == nil {
		if _, err := runCmd("", "powershell", "-ExecutionPolicy", "Bypass", "-File", filepath.Join("scripts", "docker-go.ps1"), "run", pkg, "run", "./..."); err != nil {
			_, _ = runCmd("", "bash", filepath.Join("scripts", "docker-go.sh"), "run", pkg, "run", "./...")
		}
		return ""
	}
	fmt.Fprintf(os.Stderr, "verify-changed: could not run golangci-lint (no go/docker)\n")
	return ""
}

func runGoVet(root, tagset string) error {
	if _, err := exec.LookPath("go"); err == nil {
		return runCmdErr(root, "go", "vet", "-tags", tagset, "./...")
	}
	if _, err := exec.LookPath("docker"); err == nil {
		if _, err := exec.LookPath("powershell"); err == nil {
			return runCmdErr(root, "powershell", "-ExecutionPolicy", "Bypass", "-File", filepath.Join("scripts", "docker-go.ps1"), "vet", "-tags", tagset, "./...")
		}
		return runCmdErr(root, "bash", filepath.Join("scripts", "docker-go.sh"), "vet", "-tags", tagset, "./...")
	}
	fmt.Fprintf(os.Stderr, "verify-changed: could not run go vet (no go/docker)\n")
	return nil
}

func runPnpm(root string, args ...string) error {
	if _, err := exec.LookPath("pnpm"); err != nil {
		fmt.Fprintf(os.Stderr, "verify-changed: could not run web checks (no pnpm)\n")
		return nil
	}
	cmd := exec.Command("pnpm", args...)
	cmd.Dir = filepath.Join(root, "web")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ---- fingerprints ------------------------------------------------------------

func fingerprintGoLint(root, version string) string {
	salt := fmt.Sprintf("golangci-lint %s run ./...", version)
	paths := listGoFiles(root)
	return hash(root, salt, paths)
}

func fingerprintGo(root string) string {
	salt := "go vet -tags [slim slim,nosqlite slim,dev] ./..."
	paths := listGoFiles(root)
	return hash(root, salt, paths)
}

func fingerprintWeb(root string) string {
	salt := "pnpm run typecheck && pnpm run lint"
	paths := listFiles(root, "web/")
	return hash(root, salt, paths)
}

func listGoFiles(root string) []string {
	return listFiles(root, "*.go", "go.mod", "go.sum", ".golangci.yml", ":!compat/suites/*")
}

func listFiles(root string, pathspecs ...string) []string {
	args := make([]string, 0, 3+len(pathspecs))
	args = append(args, "ls-files", "-c", "-o", "--exclude-standard", "--")
	args = append(args, pathspecs...)
	out, err := runCmd(root, "git", args...)
	if err != nil || out == "" {
		return nil
	}
	var result []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// skip deleted files
		if _, err := runCmd(root, "git", "ls-files", "-d", "--", line); err == nil {
			continue
		}
		result = append(result, line)
	}
	sort.Strings(result)
	return result
}

func hash(root, salt string, paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	h := sha256.New()
	io.WriteString(h, salt)
	for _, p := range paths {
		io.WriteString(h, p)
	}
	for _, p := range paths {
		full := filepath.Join(root, p)
		b, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ---- cache -------------------------------------------------------------------

func cacheFile(root string) string {
	gitDir, _ := runCmd(root, "git", "rev-parse", "--git-dir")
	gitDir = strings.TrimSpace(gitDir)
	return filepath.Join(gitDir, "verify-changed.cache")
}

func cacheHit(root, key, fp string) bool {
	if fp == "" {
		return false
	}
	cached := readCache(root, key)
	return cached == fp
}

func cachePut(root, key, fp string, invalidate bool) {
	if fp == "" {
		return
	}
	if invalidate {
		writeCache(root, key, "")
	} else {
		writeCache(root, key, fp)
	}
}

func readCache(root, key string) string {
	cf := cacheFile(root)
	b, err := os.ReadFile(cf)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 || parts[0] != key {
			continue
		}
		return parts[1]
	}
	return ""
}

func writeCache(root, key, fp string) {
	cf := cacheFile(root)
	existing, _ := os.ReadFile(cf)
	var lines []string
	for _, line := range strings.Split(string(existing), "\n") {
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 || parts[0] == key {
			continue
		}
		lines = append(lines, line)
	}
	if fp != "" {
		lines = append(lines, key+" "+fp)
	}
	content := strings.Join(lines, "\n")
	_ = os.WriteFile(cf, []byte(content), 0644)
}

func recordPass(root, what string) {
	switch what {
	case "go":
		v := readGolangciLintVersion(root)
		fp := fingerprintGoLint(root, v)
		cachePut(root, "golangci-lint", fp, false)
		fp = fingerprintGo(root)
		cachePut(root, "vet-tags", fp, false)
	case "web":
		fp := fingerprintWeb(root)
		cachePut(root, "web", fp, false)
	default:
		fmt.Fprintf(os.Stderr, "verify-changed: --record takes 'go' or 'web', got: %s\n", what)
		os.Exit(2)
	}
}

// ---- helpers -----------------------------------------------------------------

func readGolangciLintVersion(root string) string {
	b, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		return "v2.8.0"
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "GOLANGCI_LINT_VERSION :=") {
			return strings.TrimSpace(strings.TrimPrefix(line, "GOLANGCI_LINT_VERSION :="))
		}
	}
	return "v2.8.0"
}

func runCmd(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	return string(out), err
}

func runCmdErr(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
