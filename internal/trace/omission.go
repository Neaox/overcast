package trace

// OmitReason says why a body in a trace is not intact, so that a reader can
// tell a body we dropped from one that was never sent. An empty body with no
// reason is a request that had no body; an empty body with a reason is context
// this emulator deleted, and saying which is the whole point of the type.
//
// It is the single source of truth for the `requestBodyTruncated` /
// `responseBodyTruncated` booleans on the wire, which are derived from it
// during marshalling rather than stored alongside it.
//
// The reason does not say whether a prefix survived, because the body itself
// already does: OmitSize keeps what fit, while OmitTraceBudget and
// OmitStreaming keep nothing. A caller distinguishes them by looking at the
// body, not by consulting a second field that could disagree with it.
type OmitReason string

const (
	// OmitNone means the body is intact — or that there was never a body.
	OmitNone OmitReason = ""

	// OmitSize means the body exceeded the middleware's maxTraceBody cap and
	// was cut short, with the leading bytes retained.
	OmitSize OmitReason = "size"

	// OmitTraceBudget means one response had already inlined
	// MaxInlinedHopBodies worth of hop bodies, so this hop's were left out of
	// that response. Nothing was deleted: a hop is a dispatched request with a
	// trace of its own, and that trace still holds the bodies in full — which
	// is what the UI's notice links to.
	//
	// A CDK deploy is what makes this necessary. One trace dispatches thousands
	// of hops and a reader opens them one at a time, so serving every body in
	// one payload spends megabytes to render a table.
	OmitTraceBudget OmitReason = "trace-budget"

	// OmitEvicted means the hop's own trace is no longer retained, so its
	// bodies cannot be resolved. The hop's metadata — service, operation,
	// status, timing, ordering — is unaffected: that lives on the parent.
	//
	// A parent does not normally outlive the calls it made, since a child trace
	// is created during the parent's processing and is therefore newer. Pinning
	// a failed parent is what will make this reachable.
	OmitEvicted OmitReason = "evicted"

	// OmitStreaming means the response was streamed (an SSE endpoint, a
	// Lambda response stream) and no body was ever captured. Nothing was
	// dropped to save space; there was nothing to hold.
	OmitStreaming OmitReason = "streaming"
)

// Omitted reports whether any part of the body was lost. It backs the derived
// `*Truncated` booleans that Entry and Hop still emit for compatibility.
func (r OmitReason) Omitted() bool { return r != OmitNone }
