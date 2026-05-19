package ssm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const storeNS = "ssm"

// ParameterVersion represents a single version of a parameter.
type ParameterVersion struct {
	Value     string
	Type      string
	Labels    []string
	CreatedAt time.Time
}

// ParameterRecord holds the full parameter history and associated metadata.
type ParameterRecord struct {
	Name        string
	Description string
	Tags        map[string]string
	Versions    []ParameterVersion // index 0 = version 1
}

// Version returns the latest version number (1-based).
func (p *ParameterRecord) Version() int64 { return int64(len(p.Versions)) }

// Latest returns the most recent ParameterVersion, or nil if none.
func (p *ParameterRecord) Latest() *ParameterVersion {
	if len(p.Versions) == 0 {
		return nil
	}
	return &p.Versions[len(p.Versions)-1]
}

// Store wraps state.Store with SSM-specific helpers.
type Store struct {
	s             state.Store
	defaultRegion string
}

func newStore(s state.Store, defaultRegion string) *Store {
	return &Store{s: s, defaultRegion: defaultRegion}
}

// region extracts the per-request region from context, falling back to the default.
func (st *Store) region(ctx context.Context) string {
	return middleware.RegionFromContext(ctx, st.defaultRegion)
}

func (st *Store) key(name string) string {
	return fmt.Sprintf("param:%s", name)
}

// Get retrieves a ParameterRecord by name. Returns nil, nil if not found.
func (st *Store) Get(ctx context.Context, name string) (*ParameterRecord, error) {
	raw, found, err := st.s.Get(ctx, storeNS, serviceutil.RegionKey(st.region(ctx), st.key(name)))
	if err != nil {
		return nil, fmt.Errorf("ssm: get param %q: %w", name, err)
	}
	if !found {
		return nil, nil
	}
	var p ParameterRecord
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, fmt.Errorf("ssm: unmarshal param %q: %w", name, err)
	}
	return &p, nil
}

// Put saves (creates or replaces) a ParameterRecord.
func (st *Store) Put(ctx context.Context, p *ParameterRecord) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("ssm: marshal param %q: %w", p.Name, err)
	}
	return st.s.Set(ctx, storeNS, serviceutil.RegionKey(st.region(ctx), st.key(p.Name)), string(raw))
}

// Delete removes a parameter by name. No-op if not found.
func (st *Store) Delete(ctx context.Context, name string) error {
	return st.s.Delete(ctx, storeNS, serviceutil.RegionKey(st.region(ctx), st.key(name)))
}

// Scan returns all ParameterRecords. If namePrefix is non-empty, only parameters
// whose names start with namePrefix are returned.
func (st *Store) Scan(ctx context.Context, namePrefix string) ([]*ParameterRecord, error) {
	pairs, err := st.s.Scan(ctx, storeNS, serviceutil.RegionKey(st.region(ctx), "param:"))
	if err != nil {
		return nil, fmt.Errorf("ssm: scan params: %w", err)
	}
	out := make([]*ParameterRecord, 0, len(pairs))
	for _, kv := range pairs {
		var p ParameterRecord
		if err := json.Unmarshal([]byte(kv.Value), &p); err != nil {
			continue
		}
		if namePrefix == "" || strings.HasPrefix(p.Name, namePrefix) {
			out = append(out, &p)
		}
	}
	return out, nil
}
