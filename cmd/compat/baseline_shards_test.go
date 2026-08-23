package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Neaox/overcast/compat"
)

// repoBaselineDir is the sharded baseline this repository ships, reached from
// the package directory `go test` runs in.
const repoBaselineDir = "../../compat/baseline"

// preSplitBaselineEnv names a recovered pre-split compat/baseline.json for
// TestBaselineShards_matchPreSplitAggregate.
const preSplitBaselineEnv = "OVERCAST_PRESPLIT_BASELINE"

// TestBaselineShards_matchPreSplitAggregate is the proof that sharding
// compat/baseline.json into compat/baseline/<suite>.json lost nothing: the
// shards aggregate, byte for byte, to the file the split deleted.
//
// It is opt-in rather than a standing gate because the thing it compares
// against does not stand still. Baseline promotion rewrites the shards on every
// improved run on main, so a hash of today's bytes pinned here would red CI on
// the first promotion — a landmine for whoever fixed a compat test that week.
// Recover the deleted file and re-run it whenever the split needs re-proving:
//
//	git show <split-commit>^:compat/baseline.json > /tmp/baseline.json
//	OVERCAST_PRESPLIT_BASELINE=/tmp/baseline.json \
//	  go test ./cmd/compat -run PreSplitAggregate -count=1
//
// The standing invariants — every shard canonical, no entry lost between the
// two layouts, an update round-tripping — are the tests below, which stay true
// as the baseline moves.
func TestBaselineShards_matchPreSplitAggregate(t *testing.T) {
	path := os.Getenv(preSplitBaselineEnv)
	if path == "" {
		t.Skipf("set %s to a recovered pre-split compat/baseline.json to run this", preSplitBaselineEnv)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pre-split baseline: %v", err)
	}

	// When: the shard directory is aggregated and rendered as a single file.
	aggregate, err := readBaselineFile(repoBaselineDir)
	if err != nil {
		t.Fatalf("read baseline shards: %v", err)
	}
	got, err := marshalBaseline(aggregate)
	if err != nil {
		t.Fatalf("marshal aggregate: %v", err)
	}

	// Then: it is the pre-split file, byte for byte.
	if string(got) != string(want) {
		t.Fatalf("aggregate of %s does not reproduce %s\n got %d bytes, sha256 %s\nwant %d bytes, sha256 %s",
			repoBaselineDir, path, len(got), sha256Hex(got), len(want), sha256Hex(want))
	}
}

// TestBaselineShardDir_isCanonical guards the shipped shards against a hand
// edit that the aggregate gates would happily read but the promotion bot would
// then rewrite, turning every later promotion diff into noise. Each shard has
// to be exactly what writeBaselineDir would produce for the entries it holds.
func TestBaselineShardDir_isCanonical(t *testing.T) {
	shards, err := baselineShardPaths(repoBaselineDir)
	if err != nil {
		t.Fatalf("list baseline shards: %v", err)
	}
	if len(shards) == 0 {
		t.Fatalf("no shards under %s", repoBaselineDir)
	}

	seen := make(map[string]string)
	for _, path := range shards {
		suite := strings.TrimSuffix(filepath.Base(path), baselineShardExt)
		shard, err := readBaselineJSON(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if len(shard.Entries) == 0 {
			t.Errorf("%s holds no entries — an empty shard silently weakens the gate", path)
		}
		for _, entry := range shard.Entries {
			if entry.Suite != suite {
				t.Errorf("%s holds a %q entry (%s) — shards are per suite", path, entry.Suite, baselineKey(entry))
			}
			key := baselineKey(entry)
			if other, dup := seen[key]; dup {
				t.Errorf("%s duplicates %s from %s", path, key, other)
			}
			seen[key] = path
		}

		want, err := marshalBaseline(&compatBaseline{Version: baselineVersion, Entries: sortBaselineEntries(shard.Entries)})
		if err != nil {
			t.Fatalf("marshal %s: %v", path, err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s is not canonical — re-run `go run ./cmd/compat --update-baseline` rather than hand-editing it", path)
		}
	}
}

// TestReadBaselineFile_fileAndDirectoryAgree pins the back-compat half of the
// split: --baseline-file still accepts the old single file, and both shapes
// yield the same baseline. CI's downgrade lint reads the base commit's
// baseline, which is a file on any commit older than the split.
func TestReadBaselineFile_fileAndDirectoryAgree(t *testing.T) {
	// Given: the same entries written both ways.
	baseline := &compatBaseline{Version: baselineVersion, Entries: []baselineEntry{
		{Suite: "go-sdk", Service: "sqs", Group: "sqs-basic", Test: "SendMessage", Op: "SendMessage", Status: compat.StatusPass},
		{Suite: "cli", Service: "s3", Group: "s3-basic", Test: "PutObject", Status: compat.StatusPass},
		{Suite: "cli", Service: "s3", Group: "s3-basic", Test: "GetObject", Status: compat.StatusFail},
	}}
	dir := t.TempDir()
	filePath := filepath.Join(dir, "baseline.json")
	shardDir := filepath.Join(dir, "baseline")
	writeBaselineOrFatal(t, filePath, cloneBaseline(baseline))
	writeBaselineOrFatal(t, shardDir, cloneBaseline(baseline))

	// When: each is read back.
	fromFile, err := readBaselineFile(filePath)
	if err != nil {
		t.Fatalf("read file baseline: %v", err)
	}
	fromDir, err := readBaselineFile(shardDir)
	if err != nil {
		t.Fatalf("read sharded baseline: %v", err)
	}

	// Then: they are the same baseline, and the shard layout is one file per suite.
	if !reflect.DeepEqual(fromFile, fromDir) {
		t.Fatalf("file and directory baselines differ:\nfile = %#v\ndir  = %#v", fromFile, fromDir)
	}
	shards, err := baselineShardPaths(shardDir)
	if err != nil {
		t.Fatalf("list shards: %v", err)
	}
	if len(shards) != 2 {
		t.Fatalf("shard count = %d, want 2 (cli, go-sdk): %v", len(shards), shards)
	}
	if _, err := os.Stat(filepath.Join(shardDir, "go-sdk.json")); err != nil {
		t.Fatalf("expected a go-sdk shard: %v", err)
	}
}

// TestWriteBaselineFile_shardsAreDeterministic keeps the promotion bot's diffs
// minimal: an unchanged baseline written again must produce identical bytes,
// or every promotion would churn every shard.
func TestWriteBaselineFile_shardsAreDeterministic(t *testing.T) {
	baseline := &compatBaseline{Version: baselineVersion, Entries: []baselineEntry{
		{Suite: "cli", Group: "s3-basic", Test: "PutObject", Status: compat.StatusPass},
		{Suite: "cli", Group: "s3-basic", Test: "GetObject", Status: compat.StatusPass},
	}}
	dir := filepath.Join(t.TempDir(), "baseline")
	writeBaselineOrFatal(t, dir, cloneBaseline(baseline))
	first, err := os.ReadFile(filepath.Join(dir, "cli.json"))
	if err != nil {
		t.Fatalf("read shard: %v", err)
	}

	// When: the same baseline is read back and written again.
	reread, err := readBaselineFile(dir)
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	writeBaselineOrFatal(t, dir, reread)
	second, err := os.ReadFile(filepath.Join(dir, "cli.json"))
	if err != nil {
		t.Fatalf("read shard: %v", err)
	}

	// Then: nothing moved, and the shard ends with the newline git wants.
	if string(first) != string(second) {
		t.Fatalf("shard bytes changed on a no-op rewrite:\nfirst  = %s\nsecond = %s", first, second)
	}
	if !strings.HasSuffix(string(first), "}\n") {
		t.Fatalf("shard does not end with a newline: %q", string(first[max(0, len(first)-8):]))
	}
}

// TestUpdateBaselineFile_roundTripsThroughShards is the promotion path end to
// end: read shards, ratchet an improvement in, write shards back.
func TestUpdateBaselineFile_roundTripsThroughShards(t *testing.T) {
	// Given: a sharded baseline recording a failure, and results that fixed it.
	dir := t.TempDir()
	shardDir := filepath.Join(dir, "baseline")
	writeBaselineOrFatal(t, shardDir, &compatBaseline{Version: baselineVersion, Entries: []baselineEntry{
		{Suite: "go-sdk", Service: "sqs", Group: "sqs-basic", Test: "SendMessage", Status: compat.StatusFail},
	}})
	resultsPath := filepath.Join(dir, "compat-results.json")
	report := reportWithResults(
		resultSpec{suite: "go-sdk", service: "sqs", group: "sqs-basic", test: "SendMessage", status: compat.StatusPass},
		resultSpec{suite: "cli", service: "s3", group: "s3-basic", test: "PutObject", status: compat.StatusPass},
	)
	if err := writeRunReportFile(resultsPath, report); err != nil {
		t.Fatalf("write results: %v", err)
	}

	// When: the baseline is updated.
	if err := updateBaselineFile(shardDir, resultsPath); err != nil {
		t.Fatalf("update baseline: %v", err)
	}

	// Then: the improvement landed, and the new suite got its own shard.
	updated, err := readBaselineFile(shardDir)
	if err != nil {
		t.Fatalf("read updated baseline: %v", err)
	}
	entries := baselineEntryMap(updated.Entries)
	if got := entries["go-sdk/sqs-basic/SendMessage"].Status; got != compat.StatusPass {
		t.Fatalf("SendMessage status = %s, want pass", got)
	}
	if _, err := os.Stat(filepath.Join(shardDir, "cli.json")); err != nil {
		t.Fatalf("expected a cli shard for the newly seen suite: %v", err)
	}
}

// TestUpdateBaselineFile_seedsAMissingDirectory covers the first run against a
// path that does not exist yet: --update-baseline seeds it rather than failing.
func TestUpdateBaselineFile_seedsAMissingDirectory(t *testing.T) {
	dir := t.TempDir()
	shardDir := filepath.Join(dir, "baseline")
	resultsPath := filepath.Join(dir, "compat-results.json")
	if err := writeRunReportFile(resultsPath, reportWithResults(
		resultSpec{suite: "cli", service: "s3", group: "s3-basic", test: "PutObject", status: compat.StatusPass},
	)); err != nil {
		t.Fatalf("write results: %v", err)
	}

	if err := updateBaselineFile(shardDir, resultsPath); err != nil {
		t.Fatalf("update baseline: %v", err)
	}

	seeded, err := readBaselineFile(shardDir)
	if err != nil {
		t.Fatalf("read seeded baseline: %v", err)
	}
	if len(seeded.Entries) != 1 {
		t.Fatalf("seeded entries = %d, want 1", len(seeded.Entries))
	}
}

// TestUpdateBaselineFile_keepsASingleFileSingle is the other half of
// back-compat: pointed at an existing file, --update-baseline must not turn it
// into a directory behind the caller's back.
func TestUpdateBaselineFile_keepsASingleFileSingle(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "baseline.json")
	writeBaselineOrFatal(t, filePath, &compatBaseline{Version: baselineVersion, Entries: []baselineEntry{
		{Suite: "cli", Group: "s3-basic", Test: "PutObject", Status: compat.StatusFail},
	}})
	resultsPath := filepath.Join(dir, "compat-results.json")
	if err := writeRunReportFile(resultsPath, reportWithResults(
		resultSpec{suite: "cli", service: "s3", group: "s3-basic", test: "PutObject", status: compat.StatusPass},
	)); err != nil {
		t.Fatalf("write results: %v", err)
	}

	if err := updateBaselineFile(filePath, resultsPath); err != nil {
		t.Fatalf("update baseline: %v", err)
	}

	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("stat baseline: %v", err)
	}
	if info.IsDir() {
		t.Fatalf("%s became a directory", filePath)
	}
}

// TestLintBaselineShardSizes_firesOverBudget proves the size budget is a gate
// and not decoration.
func TestLintBaselineShardSizes_firesOverBudget(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baseline")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	oversized := filepath.Join(dir, "cli.json")
	if err := os.WriteFile(oversized, make([]byte, baselineShardMaxBytes+1), 0o644); err != nil {
		t.Fatalf("write shard: %v", err)
	}

	err := lintBaselineShardSizes(dir)
	if err == nil {
		t.Fatalf("oversized shard passed the size budget")
	}
	if !strings.Contains(err.Error(), "size budget") {
		t.Fatalf("error = %v, want it to name the size budget", err)
	}
}

// TestLintBaselineShardSizes_passesUnderBudget guards against a lint that
// fails on everything, which is the same as no lint at all.
func TestLintBaselineShardSizes_passesUnderBudget(t *testing.T) {
	if err := lintBaselineShardSizes(repoBaselineDir); err != nil {
		t.Fatalf("the shipped baseline is over its size budget: %v", err)
	}
}

// TestWriteBaselineDir_rejectsUnsafeSuiteName closes the path-traversal door:
// suite names arrive from suite subprocesses over NDJSON, and one of them is a
// file name here.
func TestWriteBaselineDir_rejectsUnsafeSuiteName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baseline")
	err := writeBaselineFile(dir, &compatBaseline{Version: baselineVersion, Entries: []baselineEntry{
		{Suite: "../escape", Group: "g", Test: "T", Status: compat.StatusPass},
	}})
	if err == nil {
		t.Fatalf("a suite name of %q was accepted as a file name", "../escape")
	}
}

func writeBaselineOrFatal(t *testing.T, path string, baseline *compatBaseline) {
	t.Helper()
	if err := writeBaselineFile(path, baseline); err != nil {
		t.Fatalf("write baseline %s: %v", path, err)
	}
}

func cloneBaseline(baseline *compatBaseline) *compatBaseline {
	out := &compatBaseline{Version: baseline.Version}
	out.Entries = append(out.Entries, baseline.Entries...)
	return out
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
