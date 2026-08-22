package metrics

import "errors"

// errInvalidObservation is returned by Observe for a fact missing its
// identity (Namespace/Name) — never for a storage outage, which is handled
// by the background flusher's retry instead (see (*Service).Flush).
var errInvalidObservation = errors.New("metrics: observation requires namespace and name")

// errRecorderStopped is returned by Observe once Stop has been called. It
// exists so a caller mid-shutdown gets an explicit signal rather than a
// silently swallowed observation.
var errRecorderStopped = errors.New("metrics: recorder is stopped")
