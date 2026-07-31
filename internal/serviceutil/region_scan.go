package serviceutil

import (
	"context"
	"encoding/json"

	"github.com/Neaox/overcast/internal/state"
)

// Regioned pairs a decoded store record with the region it is stored under.
type Regioned[T any] struct {
	Region string
	Value  *T
}

// ScanRegions decodes every record in a region-keyed namespace across all
// regions. Background paths that have no request context (startup
// reconciliation, sweepers) use it instead of a region-scoped list, which
// would silently cover only the default region. Records whose region prefix
// is empty are attributed to defaultRegion. Malformed records are skipped,
// matching the per-service list helpers.
func ScanRegions[T any](ctx context.Context, st state.Store, ns, defaultRegion string) ([]Regioned[T], error) {
	pairs, err := st.Scan(ctx, ns, "")
	if err != nil {
		return nil, err
	}
	out := make([]Regioned[T], 0, len(pairs))
	for _, p := range pairs {
		var v T
		if json.Unmarshal([]byte(p.Value), &v) != nil {
			continue
		}
		region, _ := SplitRegionKey(p.Key)
		if region == "" {
			region = defaultRegion
		}
		out = append(out, Regioned[T]{Region: region, Value: &v})
	}
	return out, nil
}

// FindRegioned locates the record stored under id in any region of a
// region-keyed namespace and returns it with its region. Event callbacks that
// only carry a resource ID (Docker container events) use it because they
// cannot resolve the region the resource was created under. Returns found ==
// false when no region holds the id.
func FindRegioned[T any](ctx context.Context, st state.Store, ns, id, defaultRegion string) (value *T, region string, found bool, err error) {
	pairs, err := st.Scan(ctx, ns, "")
	if err != nil {
		return nil, "", false, err
	}
	for _, p := range pairs {
		r, rest := SplitRegionKey(p.Key)
		if rest != id {
			continue
		}
		var v T
		if json.Unmarshal([]byte(p.Value), &v) != nil {
			continue
		}
		if r == "" {
			r = defaultRegion
		}
		return &v, r, true, nil
	}
	return nil, "", false, nil
}
