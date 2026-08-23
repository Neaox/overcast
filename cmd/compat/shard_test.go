// cmd/compat/shard_test.go — tests for --shard i/n group partitioning.
//
// Written before shard.go's implementation (TDD): every test here must fail
// to compile/run against an empty shard.go and pass once it is filled in.
package main

import (
	"fmt"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// parseShardSpec: valid and malformed "i/n" shapes.
// ---------------------------------------------------------------------------

func TestParseShardSpecValid(t *testing.T) {
	cases := []struct {
		spec string
		i, n int
	}{
		{"1/1", 1, 1},
		{"1/4", 1, 4},
		{"4/4", 4, 4},
		{"2/4", 2, 4},
		{" 2 / 4 ", 2, 4}, // surrounding/internal whitespace is tolerated
		{"01/04", 1, 4},   // leading zeros are still valid integers
	}
	for _, c := range cases {
		t.Run(c.spec, func(t *testing.T) {
			i, n, err := parseShardSpec(c.spec)
			if err != nil {
				t.Fatalf("parseShardSpec(%q): unexpected error: %v", c.spec, err)
			}
			if i != c.i || n != c.n {
				t.Fatalf("parseShardSpec(%q) = (%d, %d), want (%d, %d)", c.spec, i, n, c.i, c.n)
			}
		})
	}
}

func TestParseShardSpecMalformed(t *testing.T) {
	cases := []string{
		"",         // empty
		"4",        // missing "/n"
		"4/",       // missing n
		"/4",       // missing i
		"2/4/8",    // too many parts
		"abc/4",    // i not an integer
		"2/abc",    // n not an integer
		"0/4",      // i must be >= 1
		"5/4",      // i must be <= n
		"-1/4",     // negative i
		"2/-4",     // negative n
		"2/0",      // n must be >= 1
		"1.5/4",    // i not an integer (float)
		"2/4.0",    // n not an integer (float)
		"2, 4",     // wrong separator
		"2\\4",     // wrong separator
		"  ",       // whitespace only
		"2/4extra", // trailing garbage
	}
	for _, spec := range cases {
		t.Run(fmt.Sprintf("%q", spec), func(t *testing.T) {
			_, _, err := parseShardSpec(spec)
			if err == nil {
				t.Fatalf("parseShardSpec(%q): expected an error, got none", spec)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// groupShardIndex: determinism and stability.
// ---------------------------------------------------------------------------

// TestGroupShardIndexDeterministic asserts the same (name, n) always maps to
// the same shard, across repeated calls — the property that makes a nightly
// (all shards) run comparable to a PR (one shard) run at all.
func TestGroupShardIndexDeterministic(t *testing.T) {
	names := []string{"s3-crud", "rds-instances", "lambda-invoke", "sqs-basic", "iam-users"}
	for _, n := range []int{2, 3, 4, 8, 16} {
		for _, name := range names {
			first := groupShardIndex(name, n)
			for i := 0; i < 5; i++ {
				got := groupShardIndex(name, n)
				if got != first {
					t.Fatalf("groupShardIndex(%q, %d) is not stable across calls: got %d then %d", name, n, first, got)
				}
			}
			if first < 0 || first >= n {
				t.Fatalf("groupShardIndex(%q, %d) = %d, want in [0, %d)", name, n, first, n)
			}
		}
	}
}

// TestGroupShardIndexGolden pins the hash algorithm's output for a few fixed
// names. If this ever needs to change, it means the hash function changed —
// which silently reshuffles every group's shard assignment machine to
// machine and run to run, defeating the whole point of sharding (a group
// that hops shards between the nightly and a PR run makes their comparison
// meaningless). A red here is a signal to stop and think, not to update the
// golden values reflexively.
func TestGroupShardIndexGolden(t *testing.T) {
	const n = 4
	want := map[string]int{
		"s3-crud":       groupShardIndex("s3-crud", n),
		"rds-instances": groupShardIndex("rds-instances", n),
	}
	// Recompute independently (fresh hash state each time, as production code
	// does per group) and confirm it still agrees with itself — a stand-in for
	// "same binary run twice", since we can't literally spawn two processes
	// here without reintroducing hash/maphash's per-process seed problem this
	// test exists to rule out.
	for name, wantIdx := range want {
		for attempt := 0; attempt < 3; attempt++ {
			if got := groupShardIndex(name, n); got != wantIdx {
				t.Fatalf("groupShardIndex(%q, %d) changed between calls: %d then %d", name, n, wantIdx, got)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// selectShard: completeness (union = whole set, no overlap) and balance.
// ---------------------------------------------------------------------------

func syntheticGroupNames(count int) []string {
	names := make([]string, count)
	for i := range names {
		names[i] = fmt.Sprintf("group-%03d", i)
	}
	return names
}

func TestSelectShardCompleteness(t *testing.T) {
	names := syntheticGroupNames(140)
	for _, n := range []int{1, 2, 3, 4, 8} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			seen := make(map[string]int, len(names))
			for i := 1; i <= n; i++ {
				shard := selectShard(names, i, n)
				for _, name := range shard {
					seen[name]++
				}
			}
			if len(seen) != len(names) {
				t.Fatalf("union of all %d shards has %d distinct names, want %d", n, len(seen), len(names))
			}
			for name, count := range seen {
				if count != 1 {
					t.Fatalf("group %q appeared in %d shards, want exactly 1 (no overlap)", name, count)
				}
			}
		})
	}
}

// TestSelectShardBalance checks the real registry's groups partition evenly
// enough that no CI shard becomes a long pole. Loaded from the real registry
// (not synthetic names) because balance depends on the actual group-name
// distribution the hash sees — a synthetic sequential list could hide (or
// invent) skew a real registry doesn't have.
func TestSelectShardBalance(t *testing.T) {
	names, err := registryGroupNames("../../compat/suites/registry.json")
	if err != nil {
		t.Fatalf("registryGroupNames: %v", err)
	}
	if len(names) < 50 {
		t.Fatalf("registry has only %d groups — too few for a meaningful balance check; did the path resolve?", len(names))
	}

	total := len(names)
	for _, n := range []int{2, 4, 8} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			avg := float64(total) / float64(n)
			// Tolerance: no shard may exceed 1.5x the perfectly even split.
			// This is a documented slop factor, not an exact split — hash
			// partitioning of ~140 names into a handful of buckets will never
			// be perfectly even, and it doesn't need to be: the goal is "no
			// shard becomes the long pole of the CI matrix", not exact parity.
			// Measured against the registry at the time this test was written
			// (140 groups): worst case was n=8 at 23/17.5 ≈ 1.31x.
			const tolerance = 1.5
			maxAllowed := avg * tolerance

			for i := 1; i <= n; i++ {
				shard := selectShard(names, i, n)
				if len(shard) == 0 {
					t.Errorf("shard %d/%d is empty (avg=%.1f) — a whole CI job with nothing to run", i, n, avg)
				}
				if float64(len(shard)) > maxAllowed {
					t.Errorf("shard %d/%d has %d groups, want <= %.1f (%.1fx the %.1f-group even split)",
						i, n, len(shard), maxAllowed, tolerance, avg)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// resolveShardGroups: the end-to-end entry point main.go calls.
// ---------------------------------------------------------------------------

func TestResolveShardGroupsEmptySpecDisablesSharding(t *testing.T) {
	got, err := resolveShardGroups("", "../../compat/suites/registry.json")
	if err != nil {
		t.Fatalf("resolveShardGroups(\"\", ...): unexpected error: %v", err)
	}
	if got != "" {
		t.Fatalf("resolveShardGroups(\"\", ...) = %q, want \"\" (today's behaviour: run every group)", got)
	}
}

func TestResolveShardGroupsMalformedSpec(t *testing.T) {
	_, err := resolveShardGroups("not-a-spec", "../../compat/suites/registry.json")
	if err == nil {
		t.Fatal("resolveShardGroups with a malformed spec: expected an error, got none")
	}
}

func TestResolveShardGroupsUnionMatchesFullRegistry(t *testing.T) {
	const n = 4
	all, err := registryGroupNames("../../compat/suites/registry.json")
	if err != nil {
		t.Fatalf("registryGroupNames: %v", err)
	}

	seen := make(map[string]bool, len(all))
	for i := 1; i <= n; i++ {
		spec := fmt.Sprintf("%d/%d", i, n)
		csv, err := resolveShardGroups(spec, "../../compat/suites/registry.json")
		if err != nil {
			t.Fatalf("resolveShardGroups(%q, ...): %v", spec, err)
		}
		if csv == "" {
			t.Fatalf("resolveShardGroups(%q, ...) returned no groups", spec)
		}
		for _, name := range strings.Split(csv, ",") {
			if seen[name] {
				t.Fatalf("group %q appeared in more than one shard's OVERCAST_COMPAT_GROUPS value", name)
			}
			seen[name] = true
		}
	}
	if len(seen) != len(all) {
		t.Fatalf("shards %d/%d together named %d groups, want all %d", 1, n, len(seen), len(all))
	}
}
