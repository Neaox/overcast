package inert

import (
	"context"
	"encoding/json"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// Store is the generic metadata store behind every Tier 1 resource: one
// namespace per service+collection, records keyed by region-scoped
// identifier, persisted as JSON in the emulator's existing state.Store.
//
// This is where internal/services/route53/store.go's 362 lines of
// put/get/list/scan/skip-malformed collapse into one implementation. Route 53
// itself is deliberately *not* migrated onto it (§4.3: hand-written services
// keep their own stores) — it is read here as the specification for the
// behaviours a generic store has to preserve, malformed-record isolation
// chief among them.
//
// T is the resource's record type: the create input unioned with everything
// the update operations can set, plus the generated derivations (§3.2). It is
// a plain struct with json tags, so /_overcast/debug/state shows a readable
// record and no new storage subsystem exists.
type Store[T any] struct {
	cfg Config
	ns  string
}

// NewStore returns the store for one resource collection. namespace is the
// state.Store namespace, conventionally "<service>:<collection>" —
// "organizations:policies".
func NewStore[T any](cfg Config, namespace string) *Store[T] {
	return &Store[T]{cfg: cfg, ns: namespace}
}

// Now is the injected clock's current time, in UTC.
//
// Every timestamp a Tier 1 handler stamps must come from here. §3.5 is
// explicit that time.Now() is forbidden, and the conformance suite's
// §3.5/timestamps clause detects a handler that ignores the injected clock
// even when the resource's timestamps are only second-granular — so this is
// not a stylistic preference, it is the difference between passing and
// failing the contract.
func (s *Store[T]) Now() time.Time { return s.cfg.Clock.Now().UTC() }

// key returns the region-scoped store key for id. Global services (Config
// with a nil Region) get the bare id, per §3.5.
func (s *Store[T]) key(ctx context.Context, id string) string {
	return serviceutil.RegionKey(s.cfg.regionOf(ctx), id)
}

// prefix returns the scan prefix covering every record visible to ctx.
func (s *Store[T]) prefix(ctx context.Context) string {
	region := s.cfg.regionOf(ctx)
	if region == "" {
		return ""
	}
	return region + "/"
}

// Put persists rec under id, overwriting any existing record.
func (s *Store[T]) Put(ctx context.Context, id string, rec *T) error {
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return s.cfg.Store.Set(ctx, s.ns, s.key(ctx, id), string(raw))
}

// Get loads one record. A record that no longer unmarshals into T reads as
// absent — (nil, false, nil) — with a warning, rather than surfacing as an
// internal error: one corrupt blob must not make every operation on the
// resource a 500, and the next write replaces it wholesale.
func (s *Store[T]) Get(ctx context.Context, id string) (*T, bool, error) {
	raw, found, err := s.cfg.Store.Get(ctx, s.ns, s.key(ctx, id))
	if err != nil || !found {
		return nil, false, err
	}
	var rec T
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		s.warnMalformed(ctx, s.key(ctx, id), err)
		return nil, false, nil
	}
	return &rec, true, nil
}

// Delete removes id. Deleting a key that is not there is not an error, which
// is state.Store's own contract.
func (s *Store[T]) Delete(ctx context.Context, id string) error {
	return s.cfg.Store.Delete(ctx, s.ns, s.key(ctx, id))
}

// List returns every record visible to ctx, ordered by store key and so
// stable-sorted by identifier (§3.1's List class). Malformed records are
// skipped with a warning, exactly as Get isolates them.
//
// state.Store.Scan already returns pairs in key order, so no re-sort happens
// here; ordering is asserted in this package's tests rather than assumed.
func (s *Store[T]) List(ctx context.Context) ([]*T, error) {
	pairs, err := s.cfg.Store.Scan(ctx, s.ns, s.prefix(ctx))
	if err != nil {
		return nil, err
	}
	out := make([]*T, 0, len(pairs))
	for _, kv := range pairs {
		rec, ok := s.decode(ctx, kv)
		if !ok {
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

// Page returns one page of List, applying the operation's modeled
// limit/continuation-token members through serviceutil.Paginate.
//
// An undecodable token comes back as serviceutil.ErrInvalidPageToken —
// callers must map it onto their service's modeled invalid-token error
// (PageError does exactly that) and must never fall through to the
// zero-value Page, which would silently restart the caller at page 1.
func (s *Store[T]) Page(ctx context.Context, token string, limit int, opts serviceutil.PaginateOptions) (serviceutil.Page[*T], error) {
	items, err := s.List(ctx)
	if err != nil {
		return serviceutil.Page[*T]{}, err
	}
	return serviceutil.Paginate(items, limit, token, opts)
}

// decode unmarshals one scanned pair, reporting whether it survived.
func (s *Store[T]) decode(ctx context.Context, kv state.KV) (*T, bool) {
	var rec T
	if err := json.Unmarshal([]byte(kv.Value), &rec); err != nil {
		s.warnMalformed(ctx, kv.Key, err)
		return nil, false
	}
	return &rec, true
}

func (s *Store[T]) warnMalformed(ctx context.Context, key string, err error) {
	if s.cfg.Logger == nil {
		return
	}
	s.cfg.Logger.WithRecorder(ctx).Warn("skipping malformed inert record",
		zap.String("namespace", s.ns),
		zap.String("key", key),
		zap.Error(err),
	)
}
