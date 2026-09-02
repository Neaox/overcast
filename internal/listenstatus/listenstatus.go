// Package listenstatus records whether the auxiliary listeners Overcast opens
// beside the AWS API — the Lambda Runtime API and the SMTP capture server —
// are actually bound, and what a caller should change when one is not.
//
// Both listeners are started off the request path (the Runtime API only once
// Docker has been probed, the SMTP server in a background goroutine), so a
// bind failure surfaces as one log line at startup and nothing else: Lambda
// invocations fall through to the stub runtime and Inbox capture silently
// stops. A second Overcast on the same host is the usual way to hit it. The
// tracker gives /_overcast/health something to report, with the fix attached.
package listenstatus

import (
	"maps"
	"sync"
)

// State is what became of a listener's bind.
type State string

const (
	// Listening means the listener is bound, on Addr.
	Listening State = "listening"
	// Failed means the bind failed and the feature behind it is unavailable.
	// Error says why and Fix says what to change.
	Failed State = "failed"
)

// Well-known listener names, the keys under which /_overcast/health reports
// them.
const (
	// LambdaRuntimeAPI is the shared Runtime API listener set (each execution
	// environment gets its own per-container port beside it).
	LambdaRuntimeAPI = "lambdaRuntimeApi"
	// SMTP is the mock SMTP capture server that feeds the Inbox.
	SMTP = "smtp"
)

// Status is one listener's outcome.
type Status struct {
	State State `json:"state"`
	// Addr is the bound address when State is Listening.
	Addr string `json:"addr,omitempty"`
	// FellBack is true when the default port was busy and an ephemeral one
	// was taken instead: the listener works, just not where the docs say.
	FellBack bool `json:"fellBack,omitempty"`
	// Error is the bind error when State is Failed.
	Error string `json:"error,omitempty"`
	// Fix names the environment variable to change and how.
	Fix string `json:"fix,omitempty"`
}

// Tracker holds the current status of every listener that has reported.
// Safe for concurrent use: the binds happen on background goroutines and the
// health handler reads from request goroutines.
type Tracker struct {
	mu sync.RWMutex
	m  map[string]Status
}

// NewTracker returns an empty Tracker.
func NewTracker() *Tracker {
	return &Tracker{m: make(map[string]Status)}
}

// Set records the outcome for one listener, replacing any earlier report.
func (t *Tracker) Set(name string, s Status) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.m[name] = s
	t.mu.Unlock()
}

// Snapshot returns a copy of every reported status, or nil when nothing has
// reported yet — so a JSON field carrying it can be omitted rather than
// rendered as an empty object.
func (t *Tracker) Snapshot() map[string]Status {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if len(t.m) == 0 {
		return nil
	}
	return maps.Clone(t.m)
}

// Degraded reports whether any listener failed to bind.
func Degraded(statuses map[string]Status) bool {
	for _, s := range statuses {
		if s.State == Failed {
			return true
		}
	}
	return false
}
