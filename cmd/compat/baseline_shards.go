package main

// Baseline storage: one JSON shard per suite under compat/baseline/.
//
// The baseline used to be a single compat/baseline.json. It reached 5,404
// entries and 850 KB before model-generated coverage (issue #1113) started
// multiplying tests per service, and a single file that size is a bad shape for
// the way this one is actually used: every suite's promotion rewrites it, so
// concurrent promotions and cherry-picks conflict across suites that never
// interact, and GitHub collapses the diff a reviewer needs to read. Sharding by
// suite makes each promotion touch only the suites whose results moved.
//
// Both layouts are read, and that is not politeness: CI's downgrade lint reads
// the *base commit's* baseline (`base/compat/baseline.json`), which is a single
// file on every commit older than the split and a directory after it. A reader
// that handled only one shape would break the lint on one side of that line or
// the other.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// baselineShardExt is the extension every shard carries; it is also what tells
// a single-file path from a directory one when the target does not exist yet.
const baselineShardExt = ".json"

// baselineShardMaxBytes is the per-shard size budget, enforced by
// --lint-baseline-size (wired into the compat workflow's gate aggregation).
//
// Measured at the split: dotnet-sdk is the largest shard at 124 KiB (787
// entries), the other SDK suites and cli sit between 114 and 124 KiB, and cdk
// is 5 KiB. 512 KiB is therefore just over 4x headroom — roughly 3,200 entries
// in one suite before it trips. The ceiling exists because the sharding
// above only helps while a shard stays reviewable: past a few hundred KB
// GitHub collapses the diff again and the promotion bot's output stops being
// something a human checks. Tripping it means shard further (by service, say),
// not raise the number.
const baselineShardMaxBytes = 512 * 1024

// errNoBaselineShards reports a baseline directory that exists but holds no
// shards. It is separate from os.ErrNotExist so that the compare and lint
// paths can refuse to run against an empty gate, while --update-baseline can
// still seed one.
var errNoBaselineShards = errors.New("no baseline shards")

// readBaselineFile reads a baseline from either layout: a directory of
// per-suite shards, or the single file that predates the split.
func readBaselineFile(path string) (*compatBaseline, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read baseline %s: %w", path, err)
	}
	if info.IsDir() {
		return readBaselineDir(path)
	}
	return readBaselineJSON(path)
}

// readBaselineFileIfExists is readBaselineFile with "not there yet" treated as
// an empty baseline, for --update-baseline seeding a path for the first time.
func readBaselineFileIfExists(path string) (*compatBaseline, error) {
	baseline, err := readBaselineFile(path)
	switch {
	case err == nil:
		return baseline, nil
	case errors.Is(err, os.ErrNotExist), errors.Is(err, errNoBaselineShards):
		return &compatBaseline{Version: baselineVersion}, nil
	default:
		return nil, err
	}
}

// readBaselineDir aggregates every shard in dir into one baseline, in the same
// canonical order a single file would have held them.
func readBaselineDir(dir string) (*compatBaseline, error) {
	shards, err := baselineShardPaths(dir)
	if err != nil {
		return nil, err
	}
	if len(shards) == 0 {
		// Not merely empty: an empty directory read as an empty baseline would
		// silently disable the ratchet, and the gate would go green on a
		// mis-typed path.
		return nil, fmt.Errorf("read baseline %s: %w (expected compat/baseline/<suite>%s)", dir, errNoBaselineShards, baselineShardExt)
	}
	var entries []baselineEntry
	for _, path := range shards {
		shard, err := readBaselineJSON(path)
		if err != nil {
			return nil, err
		}
		entries = append(entries, shard.Entries...)
	}
	return &compatBaseline{Version: baselineVersion, Entries: sortBaselineEntries(entries)}, nil
}

// readBaselineJSON reads one baseline JSON document — a whole pre-split
// baseline, or one suite's shard; the shape is identical either way.
func readBaselineJSON(path string) (*compatBaseline, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read baseline %s: %w", path, err)
	}
	var baseline compatBaseline
	if err := json.Unmarshal(b, &baseline); err != nil {
		return nil, fmt.Errorf("parse baseline %s: %w", path, err)
	}
	if baseline.Version == 0 {
		baseline.Version = baselineVersion
	}
	return &baseline, nil
}

// baselineShardPaths lists a directory's shards in name order, which is also
// suite order — so the aggregate is assembled deterministically before it is
// sorted, and a shard added tomorrow cannot reorder the ones already there.
func baselineShardPaths(dir string) ([]string, error) {
	shards, err := filepath.Glob(filepath.Join(dir, "*"+baselineShardExt))
	if err != nil {
		return nil, fmt.Errorf("list baseline shards in %s: %w", dir, err)
	}
	sort.Strings(shards)
	return shards, nil
}

// writeBaselineFile writes a baseline to path, sharded per suite when path is
// a directory.
func writeBaselineFile(path string, baseline *compatBaseline) error {
	baseline.Version = baselineVersion
	baseline.Entries = sortBaselineEntries(baseline.Entries)
	if baselinePathIsSharded(path) {
		return writeBaselineDir(path, baseline)
	}
	b, err := marshalBaseline(baseline)
	if err != nil {
		return err
	}
	return writeFileAtomically(path, b)
}

// baselinePathIsSharded decides which layout a write targets. An existing path
// answers for itself; otherwise the extension does, so `--baseline-file
// out.json` still produces a file and `--baseline-file compat/baseline`
// produces a directory. Deciding by what is on disk is what keeps
// --update-baseline from silently converting a caller's single file into a
// directory (or the reverse) on the run that happens to write it.
func baselinePathIsSharded(path string) bool {
	if info, err := os.Stat(path); err == nil {
		return info.IsDir()
	}
	return !strings.EqualFold(filepath.Ext(path), baselineShardExt)
}

// writeBaselineDir writes one shard per suite.
//
// Shards for suites the baseline no longer mentions are left alone: the
// baseline only ever ratchets forward, so a suite disappearing means it did not
// run, not that its expectations were dropped — and deleting them here would
// let one selective run erase another suite's gate.
func writeBaselineDir(dir string, baseline *compatBaseline) error {
	shards := make(map[string][]baselineEntry)
	for _, entry := range baseline.Entries {
		if err := validateBaselineSuiteName(entry.Suite); err != nil {
			return err
		}
		shards[entry.Suite] = append(shards[entry.Suite], entry)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create baseline directory %s: %w", dir, err)
	}
	suites := make([]string, 0, len(shards))
	for suite := range shards {
		suites = append(suites, suite)
	}
	sort.Strings(suites)
	for _, suite := range suites {
		b, err := marshalBaseline(&compatBaseline{
			Version: baselineVersion,
			Entries: sortBaselineEntries(shards[suite]),
		})
		if err != nil {
			return err
		}
		if err := writeFileAtomically(filepath.Join(dir, suite+baselineShardExt), b); err != nil {
			return err
		}
	}
	return nil
}

// validateBaselineSuiteName refuses a suite name that would not be a plain file
// name. Suite names reach compat as NDJSON from a suite subprocess, and here
// one becomes a path — so `../..` in that field must not be a way to write
// outside the baseline directory. The character set matches the suite
// directories under compat/suites/.
func validateBaselineSuiteName(suite string) error {
	if suite == "" {
		return errors.New("baseline entry has no suite: cannot name a shard for it")
	}
	for _, r := range suite {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_':
		default:
			return fmt.Errorf("baseline suite name %q is not usable as a file name (want letters, digits, - or _)", suite)
		}
	}
	return nil
}

// marshalBaseline renders a baseline exactly as it is stored: two-space indent,
// trailing newline. Both are load-bearing — the indentation is what makes the
// promotion bot's diffs reviewable line by line, and the newline is what keeps
// git from reporting "\ No newline at end of file" on every write.
func marshalBaseline(baseline *compatBaseline) ([]byte, error) {
	b, err := json.MarshalIndent(baseline, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal baseline: %w", err)
	}
	return append(b, '\n'), nil
}

func writeFileAtomically(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write baseline: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp) //nolint:errcheck
		return fmt.Errorf("write baseline: %w", err)
	}
	return nil
}

// lintBaselineShardSizes is the --lint-baseline-size entry point: fail when any
// shard has outgrown baselineShardMaxBytes.
//
// It is its own flag rather than a fold into --lint-baseline-to because that
// lint only runs on pull requests that have a baseline on the base commit,
// while the thing that actually grows the baseline is the promotion bot on
// main. A standalone check runs on both roads.
//
// A single-file path is linted as one shard, so an un-split baseline that crept
// back in trips this immediately rather than passing for want of a directory.
func lintBaselineShardSizes(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("read baseline %s: %w", path, err)
	}
	shards := []string{path}
	if info.IsDir() {
		if shards, err = baselineShardPaths(path); err != nil {
			return err
		}
		if len(shards) == 0 {
			return fmt.Errorf("read baseline %s: %w", path, errNoBaselineShards)
		}
	}

	var over []string
	largest := int64(0)
	for _, shard := range shards {
		st, err := os.Stat(shard)
		if err != nil {
			return fmt.Errorf("stat baseline shard %s: %w", shard, err)
		}
		if st.Size() > largest {
			largest = st.Size()
		}
		if st.Size() > baselineShardMaxBytes {
			over = append(over, fmt.Sprintf(
				"compat baseline shard over size budget: %s is %d bytes (limit %d) — shard it further (by service, say) rather than raising the limit",
				shard, st.Size(), baselineShardMaxBytes))
		}
	}
	if len(over) > 0 {
		for _, issue := range over {
			fmt.Fprintln(os.Stderr, issue)
		}
		if *annotate {
			fmt.Print(errorAnnotations("Compat baseline size", over))
		}
		return fmt.Errorf("%d compat baseline shard(s) over the size budget", len(over))
	}
	fmt.Printf("compat: baseline size budget passed (%d shard(s), largest %d of %d bytes)\n",
		len(shards), largest, baselineShardMaxBytes)
	return nil
}
