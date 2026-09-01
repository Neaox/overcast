package lifecycle

import (
	"context"
	"time"

	"github.com/overcast-sh/overcast/internal/middleware"
)

// ScopedKey builds the region-scoped scheduler key AfterScoped and
// CancelScoped use. Exported for callers that need to inspect or test
// pending transitions.
func ScopedKey(region, id, transition string) string {
	return region + "/" + id + ":" + transition
}

// AfterScoped schedules fn to run after delay for a resource that lives in a
// region-keyed store. It exists because the plain After callbacks run outside
// any request context: a bare context.Background() resolves to the DEFAULT
// region, so for a resource created in any other region the callback reads or
// writes the wrong store key and silently no-ops (statuses stuck at
// "creating"/"stopping", deferred deletes that never land).
//
// AfterScoped closes both halves of that bug at once:
//   - fn receives a background context carrying region, so region-keyed store
//     lookups inside the callback resolve the same record the request wrote;
//   - the scheduler key is scoped by region, so same-named resources in
//     different regions cannot cancel or replace each other's pending
//     transitions.
//
// Callers pass the region resolved from the request context at schedule time
// (e.g. store.region(ctx)).
func (s *Scheduler) AfterScoped(region, id, transition string, delay time.Duration, fn func(ctx context.Context)) {
	s.After(ScopedKey(region, id, transition), delay, func() {
		fn(middleware.ContextWithRegion(context.Background(), region))
	})
}

// CancelScoped cancels a pending transition scheduled with AfterScoped.
func (s *Scheduler) CancelScoped(region, id, transition string) bool {
	return s.Cancel(ScopedKey(region, id, transition))
}
