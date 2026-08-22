// cmd/compat/shard.go — deterministic group sharding for CI parallelism.
//
// --shard i/n splits the registry's test groups into n roughly-even,
// deterministic buckets and runs only bucket i (1-based). It is implemented
// entirely in cmd/compat, over the OVERCAST_COMPAT_GROUPS env var
// compat/runner.go already sets from RunConfig.Group (a comma-separated list
// every suite already parses — see e.g. compat/suites/cli/cmd/runner/main.go's
// splitCSV) — so no suite needs to know sharding exists.
//
// Selection is group-wise and happens before any suite subprocess starts: the
// full set of group names is read once from the registry file, partitioned,
// and only the chosen shard's names are ever placed in OVERCAST_COMPAT_GROUPS.
// Each suite still does its own "slow groups first" scheduling (see
// compat/suites/registry.schema.json's "slow" field) over whatever subset it
// receives, so shard selection composes with that ordering for free.
//
// See docs/plans/compat-coverage-modelgen.md §3.6 "Volume → CI runtime" for
// the motivating design. That plan has PR runs shard only the (much larger)
// set of model-generated groups while always running every hand-written
// group; deciding which groups belong to which pool is a caller concern for
// a sibling change (generated groups don't exist yet) — this flag only
// provides the "split n ways, deterministically" primitive it depends on.
package main

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
)

// parseShardSpec parses a "--shard i/n" value: a 1-based shard index i and a
// shard count n. Both must be positive base-10 integers with i <= n.
//
// An empty spec is not a valid shard spec — it means "sharding disabled" and
// callers (resolveShardGroups) must check for it before calling this.
func parseShardSpec(spec string) (i, n int, err error) {
	parts := strings.Split(spec, "/")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid --shard %q: want \"i/n\" (e.g. \"2/4\")", spec)
	}
	iStr := strings.TrimSpace(parts[0])
	nStr := strings.TrimSpace(parts[1])
	i, iErr := strconv.Atoi(iStr)
	n, nErr := strconv.Atoi(nStr)
	if iErr != nil || nErr != nil {
		return 0, 0, fmt.Errorf("invalid --shard %q: i and n must be integers (e.g. \"2/4\")", spec)
	}
	if n < 1 {
		return 0, 0, fmt.Errorf("invalid --shard %q: n must be >= 1", spec)
	}
	if i < 1 || i > n {
		return 0, 0, fmt.Errorf("invalid --shard %q: i must satisfy 1 <= i <= n (got i=%d, n=%d)", spec, i, n)
	}
	return i, n, nil
}

// groupShardIndex returns the 0-based shard a group name belongs to, out of n
// shards.
//
// It hashes the name with FNV-1a and reduces modulo n. That specific choice
// matters: Go map iteration order is unspecified and varies run to run, and
// hash/maphash is deliberately reseeded every process — either one would put
// a group in a different shard each time compat runs. FNV-1a has neither
// problem: it is a fixed, unseeded algorithm, so the same name always hashes
// to the same value on any machine, in any process, forever. That stability
// is the entire point of sharding here — the nightly run (all shards) and a
// PR run (one shard) are only comparable if a group can't hop shards between
// them.
func groupShardIndex(name string, n int) int {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))      // hash.Hash.Write on a Go hash never errors
	return int(h.Sum64() % uint64(n)) //nolint:gosec // n > 0 is a documented precondition
}

// selectShard returns the subset of names assigned to shard i (1-based) of n,
// preserving their relative order in names.
func selectShard(names []string, i, n int) []string {
	var out []string
	for _, name := range names {
		if groupShardIndex(name, n)+1 == i {
			out = append(out, name)
		}
	}
	return out
}

// registryGroupNames returns every distinct group name in the registry file
// at path, sorted so shard membership does not depend on the file's on-disk
// group order (which changes as the registry grows).
//
// It reuses parity.go's registry types and reader rather than adding a
// second JSON schema for the same file.
func registryGroupNames(path string) ([]string, error) {
	reg, err := readParityRegistry(path)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(reg.Groups))
	names := make([]string, 0, len(reg.Groups))
	for _, g := range reg.Groups {
		if seen[g.Name] {
			continue
		}
		seen[g.Name] = true
		names = append(names, g.Name)
	}
	sort.Strings(names)
	return names, nil
}

// resolveShardGroups turns --shard's raw flag value into the comma-separated
// OVERCAST_COMPAT_GROUPS value for that shard, ready to assign straight to
// compat.RunConfig.Group.
//
// An empty spec returns "" with no error: that is "sharding disabled", which
// leaves RunConfig.Group empty and every group runs — today's behaviour,
// unchanged.
func resolveShardGroups(spec, registryPath string) (string, error) {
	if spec == "" {
		return "", nil
	}
	i, n, err := parseShardSpec(spec)
	if err != nil {
		return "", err
	}
	names, err := registryGroupNames(registryPath)
	if err != nil {
		return "", fmt.Errorf("--shard: %w", err)
	}
	shard := selectShard(names, i, n)
	if len(shard) == 0 {
		return "", fmt.Errorf("--shard %s selects zero groups out of %d in %s", spec, len(names), registryPath)
	}
	return strings.Join(shard, ","), nil
}
