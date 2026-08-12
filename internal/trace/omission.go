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

	// OmitSize means the body exceeded a per-body cap and was cut short, with
	// the leading bytes retained: MaxHopBody for a hop, the middleware's
	// maxTraceBody for the request or response itself.
	OmitSize OmitReason = "size"

	// OmitTraceBudget means the trace had already retained MaxHopBodyBytes of
	// hop bodies, so this one was dropped in full. Everything else about the
	// hop — ordering, timing, service, operation, outcome — is still recorded;
	// see chargeHopBodiesLocked for why the hop is kept rather than dropped.
	//
	// This is the loss a CDK deploy actually hits: one deploy trace exhausts
	// the budget partway through, and every hop after it carries no bodies.
	OmitTraceBudget OmitReason = "trace-budget"

	// OmitStreaming means the response was streamed (an SSE endpoint, a
	// Lambda response stream) and no body was ever captured. Nothing was
	// dropped to save space; there was nothing to hold.
	OmitStreaming OmitReason = "streaming"
)

// Omitted reports whether any part of the body was lost. It backs the derived
// `*Truncated` booleans that Entry and Hop still emit for compatibility.
func (r OmitReason) Omitted() bool { return r != OmitNone }
