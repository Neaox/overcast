package metrics

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"sort"
	"time"
)

// canonicalizeDimensions drops empty-named dimensions and sorts by
// (name, value) — the same rule internal/services/cloudwatch applies to
// PutMetricData, kept identical here so a series recorded by a service and a
// series recorded by a custom PutMetricData call converge on the same
// identity when their dimensions happen to match.
func canonicalizeDimensions(in []Dimension) []Dimension {
	out := make([]Dimension, 0, len(in))
	for _, d := range in {
		if d.Name == "" {
			continue
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].Value < out[j].Value
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// seriesID computes the v1 canonical identity digest for a
// (namespace, metric name, dimension-set) series
// (service-metrics-platform.md "Canonical series identity"). Each component
// is length-prefixed rather than joined with a separator: dimension names and
// values may legally contain "/", "|", or "=", so a delimiter-joined key can
// collide between two different dimension sets. dims must already be
// canonicalized (canonicalizeDimensions) — seriesID does not sort them
// itself, since every caller already has a canonical set in hand and hashing
// is on the Observe hot path.
func seriesID(namespace, name string, dims []Dimension) string {
	h := sha256.New()
	writeLenPrefixed(h, "v1")
	writeLenPrefixed(h, namespace)
	writeLenPrefixed(h, name)
	for _, d := range dims {
		writeLenPrefixed(h, d.Name)
		writeLenPrefixed(h, d.Value)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func writeLenPrefixed(h hash.Hash, s string) {
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], uint64(len(s)))
	h.Write(lenBuf[:]) //nolint:errcheck // hash.Hash.Write never errors
	h.Write([]byte(s)) //nolint:errcheck
}

// floorToMinute floors t to the start of its UTC minute — the bucket key
// AWS's one-minute service-metric model uses. The original sub-minute
// timestamp is not retained: standard minute statistics don't need it, and a
// later percentile sample store (if ever approved) would need its own,
// separately designed and bounded storage — see the plan's "Avoid a
// metrics:observations namespace in v1" section.
func floorToMinute(t time.Time) time.Time {
	return t.UTC().Truncate(time.Minute)
}

// shardIndex picks one of activeShardCount shards for a seriesID. seriesID is
// a hex SHA-256 digest, so its leading byte is already uniformly distributed;
// parsing just that byte avoids hashing the whole 64-character string on the
// Observe hot path.
func shardIndex(id string) int {
	if len(id) < 2 {
		return 0
	}
	hi := hexNibble(id[0])
	lo := hexNibble(id[1])
	if hi < 0 || lo < 0 {
		return 0
	}
	return (hi<<4 | lo) % activeShardCount
}

func hexNibble(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	default:
		return -1
	}
}
