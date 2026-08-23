package lambda

// hot_reload_fingerprint.go — what a bind-mount hot-reload function's execution
// environment is keyed on.
//
// A hot-reload function has no deployment package to hash: /var/task is a bind
// mount of a directory on the developer's machine, and "the code" is whatever
// is in that directory right now. The pool still needs a code identity for it
// (functionCodeIdentity → functionInstanceIdentity), because that identity is
// the only thing that retires a warm container when the code changes.
//
// That identity used to be the mounted directory's own mtime. A directory's
// mtime moves when an entry is created, deleted or renamed inside it — and not
// when a file already in it is edited in place. Editing index.js therefore left
// the identity untouched, the pool handed the invocation the warm container it
// already had, and a runtime that caches loaded modules (Node's require cache,
// Python's sys.modules) served the old code indefinitely: issue #1411. The
// documented promise is "edit files in the configured source path, and invoke
// again", so the identity is what had to change.
//
// The fingerprint below covers the whole tree — every entry's path, and every
// file's size and mtime — so an in-place edit moves it, as create, delete and
// rename already did. The cost lands on the invoke path (takeWarm computes the
// identity on every acquire), so the walk is bounded three ways: it does not
// descend into dependency and VCS directories, it stops after
// hotReloadWalkMaxEntries entries and hotReloadWalkMaxDepth levels, and a walk
// that costs more than hotReloadWalkLatencyBudget is not repeated until a
// multiple of that cost has passed. See the constants for the measurements
// behind each bound.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Neaox/overcast/internal/clock"
)

const (
	// hotReloadWalkMaxEntries bounds one fingerprint walk. Past it the digest
	// covers a deterministic prefix of the tree (depth-first, each directory in
	// the order os.ReadDir returns, which is sorted by name) plus a truncation
	// marker, so the identity is still stable — it just stops noticing edits
	// beyond the cap.
	//
	// 20,000 is far above any hand-written source tree once the dependency
	// directories below are excluded, so it is a backstop against a tag pointed
	// at a home directory rather than a routine cost. It is set where one walk
	// stays affordable as a one-off: BenchmarkHotReloadFingerprint_atTheEntryCap
	// measures 27.6 ms on a native NTFS volume and 70.4 ms on Linux overlayfs
	// (5900X). The rate limiter below is what keeps a walk that expensive off
	// every subsequent invoke.
	hotReloadWalkMaxEntries = 20000

	// hotReloadWalkMaxDepth bounds recursion. A directory at this depth is
	// fingerprinted by name but not descended into.
	hotReloadWalkMaxDepth = 24

	// hotReloadWalkLatencyBudget is how much a walk may add to an invocation
	// before it is worth rate-limiting at all. Below it the tree is re-read
	// before every invocation and "picked up on the next invoke" is exact,
	// which is the whole promise and not worth trading for a saving nobody can
	// perceive. Above it the rate limiter takes over.
	//
	// The cost per entry spans three orders of magnitude across the filesystems
	// this runs on. Over the same 220-entry tree,
	// BenchmarkHotReloadFingerprint_sourceTree measures 0.84 ms on Linux
	// overlayfs and 2.4 ms on native NTFS — 4–11 µs an entry, so this budget
	// covers a source tree of a few thousand files exactly. A Docker Desktop
	// bind mount from Windows costs about 2 ms an entry (measured: 4.8 ms for a
	// one-file tree, 23 ms for ten, 470 ms for the same 220), and that is the
	// configuration the rate limiter exists for.
	//
	// A budget is what makes the rate limiter safe to have at all. Rate-limit
	// by cost alone and a one-millisecond walk still buys a 20 ms hold-off —
	// long enough for an edit and a re-invoke to land inside it, which is the
	// bug back again on a stopwatch.
	hotReloadWalkLatencyBudget = 25 * time.Millisecond

	// hotReloadWalkRateLimitFactor bounds the amortized cost of a walk that
	// went over the budget: after one that took d, the next walk of the same
	// tree is refused until d*factor has passed, so an expensive tree can never
	// account for more than 1/factor of the invoke path.
	hotReloadWalkRateLimitFactor = 20

	// hotReloadWalkMaxInterval caps the rate limiter, so even a pathologically
	// slow tree is re-read at least this often and the "picked up on the next
	// invoke" promise degrades to "within two seconds" rather than failing.
	// Freshness is what hot reload is for; a developer who cannot afford the
	// re-read at that cadence is better served by moving Overcast onto the host
	// or by `cdk watch`, both of which the docs point at.
	hotReloadWalkMaxInterval = 2 * time.Second

	// hotReloadCacheMaxEntries triggers a prune of fingerprints for trees
	// nothing has asked about recently, so the cache cannot grow one permanent
	// entry per function that has ever hot-reloaded.
	hotReloadCacheMaxEntries = 64
	hotReloadCacheEntryTTL   = 10 * time.Minute
)

// hotReloadSkipDirs are directory names the walk fingerprints by name but does
// not descend into.
//
// These hold dependencies and VCS metadata: thousands to hundreds of thousands
// of files that are not what "edit the source and invoke again" means, and
// whose cost would otherwise dominate every walk. It is the single largest
// bound of the three — BenchmarkHotReloadFingerprint_dependencyTree adds 30,300
// files under node_modules to the 220-entry source tree and measures the same
// 2.4 ms (NTFS) / 0.80 ms (overlayfs) as the source tree alone.
//
// Adding, removing or renaming one of these directories still moves the
// fingerprint — only edits *inside* them are invisible, and those are picked up
// by any other change to the tree, by UpdateFunctionCode, or by restarting
// Overcast.
var hotReloadSkipDirs = map[string]struct{}{
	"node_modules":  {},
	".git":          {},
	"__pycache__":   {},
	".venv":         {},
	".mypy_cache":   {},
	".pytest_cache": {},
}

// hotReloadFingerprint returns a digest of the source tree at root, or of the
// single file when root is not a directory. A root that cannot be read at all
// digests to a stable "missing" value rather than to an error: Overcast running
// in a container usually cannot see the developer's host path (the bind mount
// is created for the sibling Lambda container, not for Overcast itself), and a
// constant identity there is exactly right — nothing that can be observed has
// changed.
func hotReloadFingerprint(root string) string {
	return hotReloadFingerprintBounded(root, hotReloadWalkMaxEntries, hotReloadWalkMaxDepth)
}

// hotReloadFingerprintBounded is hotReloadFingerprint with the two bounds
// passed in, so tests can reach the truncation and depth branches without
// building a twenty-thousand-entry tree to do it.
func hotReloadFingerprintBounded(root string, maxEntries, maxDepth int) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "hotreload:%s\n", root)

	st, err := os.Stat(root)
	switch {
	case err != nil:
		_, _ = io.WriteString(h, "missing\n")
		return hex.EncodeToString(h.Sum(nil))
	case !st.IsDir():
		// A tag pointed at a single file: mount validation rejects it later,
		// but the identity must still move when that file is edited.
		_, _ = fmt.Fprintf(h, "f:%d:%d\n", st.Size(), st.ModTime().UnixNano())
		return hex.EncodeToString(h.Sum(nil))
	}

	budget := maxEntries
	truncated := false
	var walk func(dir, rel string, depth int)
	walk = func(dir, rel string, depth int) {
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			// An unreadable directory is recorded as such: the identity must
			// not silently equal the identity of a readable one.
			_, _ = fmt.Fprintf(h, "!%s\n", rel)
			return
		}
		for _, e := range entries {
			if budget <= 0 {
				truncated = true
				return
			}
			budget--
			name := e.Name()
			childRel := rel + "/" + name
			// A symlink is fingerprinted by name only and never followed:
			// following one invites both a cycle and a walk of half the disk.
			if !e.IsDir() {
				info, infoErr := e.Info()
				if infoErr != nil {
					_, _ = fmt.Fprintf(h, "!%s\n", childRel)
					continue
				}
				_, _ = fmt.Fprintf(h, "f%s:%d:%d\n", childRel, info.Size(), info.ModTime().UnixNano())
				continue
			}
			if _, skip := hotReloadSkipDirs[name]; skip {
				_, _ = fmt.Fprintf(h, "x%s\n", childRel)
				continue
			}
			if depth >= maxDepth {
				_, _ = fmt.Fprintf(h, "+%s\n", childRel)
				continue
			}
			_, _ = fmt.Fprintf(h, "d%s\n", childRel)
			walk(filepath.Join(dir, name), childRel, depth+1)
		}
	}
	walk(root, "", 0)
	if truncated {
		_, _ = io.WriteString(h, "truncated\n")
	}
	return hex.EncodeToString(h.Sum(nil))
}

// hotReloadFingerprintCache holds the last fingerprint of each mounted tree and
// the earliest time it may be recomputed.
//
// Rate-limiting is the point: functionCodeIdentity is consulted on every
// acquire, so without it a large or slow tree would add its whole walk to every
// invocation. Within one invocation the cache also makes the several identity
// computations (takeWarm, then the container runtime on a cold start) agree
// with each other, which they would otherwise only do by luck.
type hotReloadFingerprintCache struct {
	mu      sync.Mutex
	clk     clock.Clock
	entries map[string]*hotReloadFingerprintEntry
	// walk is the fingerprint function, indirected for tests.
	walk func(root string) string
}

type hotReloadFingerprintEntry struct {
	digest   string
	nextWalk time.Time
	lastUsed time.Time
}

func newHotReloadFingerprintCache(clk clock.Clock) *hotReloadFingerprintCache {
	return &hotReloadFingerprintCache{
		clk:     clk,
		entries: make(map[string]*hotReloadFingerprintEntry),
		walk:    hotReloadFingerprint,
	}
}

// hotReloadFingerprints is the process-wide cache. It is keyed by path rather
// than by function so two functions mounting the same tree share one walk, and
// it is package-level because functionCodeIdentity is a pure function of a
// *Function — the pool, the container runtime and the handlers all call it
// without a service to hang state off.
var hotReloadFingerprints = newHotReloadFingerprintCache(clock.New())

// digest returns the fingerprint of root, walking the tree unless a recent
// enough walk is on record.
func (c *hotReloadFingerprintCache) digest(root string) string {
	now := c.clk.Now()

	c.mu.Lock()
	entry, ok := c.entries[root]
	if ok {
		entry.lastUsed = now
		if now.Before(entry.nextWalk) {
			digest := entry.digest
			c.mu.Unlock()
			return digest
		}
	}
	c.mu.Unlock()

	// Walk outside the lock: a slow tree must not block every other function's
	// invocations. Two concurrent invocations of the same function may both
	// walk; they compute the same digest, so the duplicate costs time and not
	// correctness.
	digest := c.walk(root)
	var interval time.Duration
	if cost := c.clk.Now().Sub(now); cost > hotReloadWalkLatencyBudget {
		interval = time.Duration(hotReloadWalkRateLimitFactor) * cost
		if interval > hotReloadWalkMaxInterval {
			interval = hotReloadWalkMaxInterval
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok = c.entries[root]; !ok {
		c.pruneLocked(now)
		entry = &hotReloadFingerprintEntry{}
		c.entries[root] = entry
	}
	entry.digest = digest
	entry.lastUsed = now
	entry.nextWalk = now.Add(interval)
	return digest
}

// pruneLocked drops fingerprints nothing has asked about for a while, once the
// cache is big enough to be worth pruning. Caller must hold c.mu.
func (c *hotReloadFingerprintCache) pruneLocked(now time.Time) {
	if len(c.entries) < hotReloadCacheMaxEntries {
		return
	}
	cutoff := now.Add(-hotReloadCacheEntryTTL)
	for path, entry := range c.entries {
		if entry.lastUsed.Before(cutoff) {
			delete(c.entries, path)
		}
	}
}

// hotReloadLocalPath returns the path at which this process can read the
// mounted source tree, which is not always the path handed to the Docker
// daemon. hostpath.Normalize rewrites `C:\src\app` to `/c/src/app` because that
// is the mount source the daemon wants; on a Windows host that string names
// nothing, so the original is what os.ReadDir has to be given. Everywhere else
// the two are the same string.
func hotReloadLocalPath(raw, normalized string) string {
	if raw == "" || raw == normalized {
		return normalized
	}
	if _, err := os.Stat(raw); err == nil {
		return raw
	}
	return normalized
}

// hotReloadVisibilityDiagnostic returns a non-empty warning when Overcast
// cannot read the mounted tree itself. The caller should log it at Warn.
//
// The bind mount is created for the Lambda container by the Docker daemon, so
// it works whether or not Overcast can see the path — but the fingerprint that
// retires a warm environment after an edit is read by Overcast, from Overcast's
// own filesystem. When Overcast runs in a container and the source directory is
// not also mounted into it, hot reload still serves the mounted source on a
// cold start and never notices an edit, which is exactly the failure this
// message exists to name rather than leave the developer to discover.
func hotReloadVisibilityDiagnostic(localPath string) string {
	if localPath == "" {
		return ""
	}
	if _, err := os.Stat(localPath); err == nil {
		return ""
	}
	return fmt.Sprintf(
		"hot-reload: %q is mounted into the function's container but Overcast cannot read it itself, "+
			"so edits under it cannot retire the warm execution environment — the first invoke will use the "+
			"mounted source and later ones will keep using that container. Mount the same directory into the "+
			"Overcast container at the same path, or call UpdateFunctionCode (for example via `cdk watch`) "+
			"after each edit",
		localPath,
	)
}
