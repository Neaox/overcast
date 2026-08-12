package trace

import "time"

// RetentionPolicy decides what a buffer keeps. Three rules, and a developer
// should be able to hold all of them at once:
//
//  1. The newest Floor traces are always retained.
//  2. Beyond the floor, traces are retained for Window, up to Ceiling.
//  3. Traces that went wrong are exempt from both, up to Pinned.
//
// The rules exist to keep one promise: that the request explaining a failure is
// still there when someone opens the trace UI, without their having configured
// anything in advance. Rule 1 is what a quiet emulator needs, rule 2 is what a
// CDK deploy needs, and rule 3 is what the deploy that *failed* needs — because
// a failed deploy does not stop, it rolls back, and the rollback is chatty
// enough to push the error out of a purely recency-ordered buffer.
type RetentionPolicy struct {
	// Floor is the number of user-facing traces retained unconditionally.
	Floor int
	// Ceiling bounds how far a burst may grow the live ring past the floor.
	// Below or equal to Floor disables growth.
	Ceiling int
	// Window is how long overflow above the floor survives. Zero disables the
	// age cull, so overflow is bounded only by Ceiling.
	Window time.Duration
	// Pinned caps the traces kept because they went wrong. Zero disables
	// pinning, which makes retention purely recency-ordered again.
	Pinned int
}

// DefaultRetention is what ships. Nothing here is expected to be configured:
// the numbers are chosen so that the common failures — a deploy overrunning the
// buffer, and a human taking a while to look — are already covered.
//
// The hour is not sized for a deploy. In overcast a deploy is not waiting on
// real CloudFront or RDS provisioning, so it finishes in minutes; the hour is
// sized for the gap between the deploy failing and somebody opening the UI. It
// covers a coffee break and a meeting. It does not cover overnight, which is
// what rule 3 is for.
func DefaultRetention(floor int) RetentionPolicy {
	if floor <= 0 {
		floor = 1000
	}
	return RetentionPolicy{
		Floor:   floor,
		Ceiling: max(floor*10, floor),
		Window:  time.Hour,
		Pinned:  1000,
	}
}

// normalise fills in the combinations that would otherwise be nonsense — a
// ceiling below the floor, a negative cap — so that a hand-built policy cannot
// produce a buffer that silently retains nothing.
func (p RetentionPolicy) normalise() RetentionPolicy {
	if p.Floor <= 0 {
		p.Floor = 1000
	}
	if p.Ceiling < p.Floor {
		p.Ceiling = p.Floor
	}
	if p.Window < 0 {
		p.Window = 0
	}
	if p.Pinned < 0 {
		p.Pinned = 0
	}
	return p
}

// qualifiesPinned reports whether a trace is one rule 3 keeps.
//
// Internal polling is excluded whatever it returns: a health check that failed
// is not what anybody is looking for, and the internal ring exists precisely to
// keep that traffic out of the way.
func qualifiesPinned(rec *Recorder) bool {
	if rec == nil || rec.internal {
		return false
	}
	rec.mu.RLock()
	defer rec.mu.RUnlock()
	if rec.entry.StatusCode >= 400 || rec.entry.AWSErrorCode != "" {
		return true
	}
	for i := range rec.entry.Hops {
		if hopFailed(rec.entry.Hops[i]) {
			return true
		}
	}
	return false
}
