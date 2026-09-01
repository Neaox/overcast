// Package inert is the shared runtime for Tier 1 ("inert") AWS services —
// the behaviour that is identical for every resource whose whole contract is
// "remember what the caller told us and hand it back faithfully".
//
// It exists so that behaviour is written and tested once here rather than
// emitted a few hundred times by cmd/awsmodelgen (see
// docs/plans/inert-tier-rollout.md §4.3). Everything in this package is
// deliberately small and deliberately generic: a Store[T] over the existing
// state.Store, one ARN-keyed Tags store, and a sorted-slice operation table
// with a zero-allocation Lookup.
//
// # What this package is not
//
// It is not a new storage subsystem. Records are typed Go structs
// JSON-marshalled into the existing state.Store, exactly as every
// hand-written service already does (§4.2) — so there is no migration and
// /_overcast/debug/state keeps working. It replaces nothing that exists:
// hand-written services keep their own stores unless someone chooses to
// migrate them, which is explicitly out of scope.
//
// # The three rules that are not negotiable
//
//   - Timestamps come from the injected clock.Clock, never time.Now()
//     (§3.5). Store.Now is the only clock a generated handler should reach
//     for.
//   - A malformed persisted record is skipped with a warning, never
//     escalated (AGENTS.md § "Malformed persisted state must be isolated").
//     One corrupt blob must not turn a whole List into a 500.
//   - Pagination goes through serviceutil.Paginate, and an undecodable
//     continuation token surfaces as serviceutil.ErrInvalidPageToken for the
//     caller to map onto its service's modeled invalid-token error — never
//     as a silent restart at page 1 (pagination-plan H1/G3).
package inert

import (
	"context"
	"errors"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

// Config is the per-service wiring every inert Store and Tags needs. One
// Config is built once in a service's New and handed to each resource's
// store, which is what keeps NewStore's own signature down to "which
// namespace".
//
// This is the one structural divergence from §4.3's sketch, which wrote the
// dependencies as unnamed fields inline on Store[T]. A generated service has
// one of these and N stores over it; naming the group means the generator
// emits the wiring once per service instead of once per resource, and a
// future dependency (a metrics recorder, say) is added in one place rather
// than in every generated constructor call.
type Config struct {
	// Store is the emulator-wide state store records are persisted into.
	Store state.Store
	// Clock is the injected time source. Required: a nil Clock is a
	// programming error rather than a silent fall back to time.Now(),
	// because falling back is precisely the §3.5 violation the conformance
	// suite exists to catch.
	Clock clock.Clock
	// Logger is the service's logger, used for the malformed-record
	// warnings. May be nil in tests.
	Logger *serviceutil.ServiceLogger
	// Region resolves the region a request is scoped to, and is what makes
	// keys region-scoped via serviceutil.RegionKey. Nil means the service is
	// global (Organizations, IAM, Route 53, CloudFront...) and its keys
	// carry no region at all — §3.5's "global services store without a
	// region key".
	Region func(ctx context.Context) string
}

// regionOf resolves the request's region, or "" for a global service.
func (c Config) regionOf(ctx context.Context) string {
	if c.Region == nil {
		return ""
	}
	return c.Region(ctx)
}

// StorageError maps a store failure onto the wire envelope. Storage failures
// are never the caller's fault, so they are always InternalError with the
// real cause preserved for logs (protocol.Wrap).
//
// This and PageError are the second divergence from §4.3, which had the
// Store methods return a bare error. Keeping the bare error is right — Page
// has to be able to say "that token is garbage" distinguishably — but every
// generated handler would then repeat the same two-line mapping, so the
// mapping lives here instead of in the generator's templates.
func StorageError(err error) *protocol.AWSError {
	if err == nil {
		return nil
	}
	return protocol.Wrap(protocol.ErrInternalError, err)
}

// PageError maps a Store.Page failure onto the wire envelope: an
// undecodable continuation token becomes the service's own modeled
// invalid-token error, anything else is a storage failure.
//
// invalidToken is the service's modeled error for the class — per §3.3 that
// is its InvalidNextToken/InvalidPaginationToken/InvalidMarker shape, or its
// invalid-parameter error where the model declares none.
func PageError(err error, invalidToken *protocol.AWSError) *protocol.AWSError {
	if err == nil {
		return nil
	}
	if errors.Is(err, serviceutil.ErrInvalidPageToken) {
		return protocol.Wrap(invalidToken, err)
	}
	return StorageError(err)
}
