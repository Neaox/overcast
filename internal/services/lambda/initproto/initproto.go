// Package initproto is the single definition of the contract between the
// Overcast host and the in-container Lambda init (cmd/lambda-init). Both sides
// import it, so a change to a name, a path or a frame field is a change both
// halves see at compile time.
//
// The shape of the arrangement, for context: the init is PID 1 inside a Lambda
// execution environment. It launches the runtime (the image's ENTRYPOINT+CMD)
// as its child, owns the child's stdout/stderr pipes, serves the Runtime API on
// [LambdaRuntimeAPI] as a transparent proxy to the host endpoint named by
// [EnvRuntimeAPI], and ships every line it reads to that same host endpoint on
// a single long-lived chunked POST to [LogsPath] as newline-delimited [Frame]s.
// Because the init is the parent, a line is attributed to the invocation that
// was in flight when the init read it — no clocks and no silence heuristics.
// The same stream carries the init's platform-telemetry observations ([Record]
// frames), so where a `platform.initStart` sits relative to the INIT phase's
// output is a fact about the sequence rather than a race between two channels.
// See docs/plans/lambda-in-container-init.md.
package initproto

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

const (
	// EnvRuntimeAPI names the host's per-execution-environment Runtime API
	// endpoint, as "host:port". Overcast sets it on the container; the init
	// reads it and refuses to start without it. It is Overcast's own variable
	// and has no AWS counterpart: on AWS the init *is* the endpoint.
	EnvRuntimeAPI = "OVERCAST_RUNTIME_API"

	// EnvAnnotate opts into prefixing every teed line with "req=<id> " on the
	// container's own stdout/stderr. Off unless set to a value os.Getenv
	// reports as non-empty and not "0"/"false", so `docker logs` stays
	// byte-identical to what the runtime wrote for anyone scripting against it.
	EnvAnnotate = "OVERCAST_INIT_ANNOTATE"

	// LambdaRuntimeAPI is what the init serves and sets as
	// AWS_LAMBDA_RUNTIME_API for the runtime and the extensions. It is the
	// exact value AWS uses, so a runtime interface client cannot tell the
	// difference.
	LambdaRuntimeAPI = "127.0.0.1:9001"

	// InitPath is where the init binary is placed inside the container, and
	// therefore the container's entrypoint.
	InitPath = "/var/overcast/init"

	// ExtensionsDir is the directory AWS launches external extensions from.
	// Every regular executable file directly inside it is started before the
	// runtime.
	ExtensionsDir = "/opt/extensions"

	// LogsPath is the host endpoint the init POSTs its log frames to, on the
	// same per-environment listener that serves the Runtime API. One
	// long-lived chunked request per init, not one per invocation.
	LogsPath = "/overcast/v1/logs"

	// HeaderLogSeq is added by the init to three forwarded requests, and reads
	// the same on all of them: "every frame with seq <= N was published on the
	// log stream before this request was forwarded". The init drains both pipes
	// immediately before stamping it, so the number is a fact about what the
	// runtime had written, not an estimate.
	//
	// It bounds an invocation at both ends, which is what makes the host's view
	// of the log ordered rather than merely attributed:
	//
	//   - GET /runtime/invocation/next — the runtime has gone idle and is
	//     asking for work, so everything it wrote up to now belongs to what
	//     came *before* the invocation the host is about to hand out. On the
	//     first /next that is the INIT phase; on later ones it is whatever the
	//     runtime printed after answering the previous invocation. The host
	//     waits for that seq before it writes the next START, so INIT output
	//     precedes the first START exactly as it does on AWS.
	//   - POST /runtime/invocation/{id}/response and .../error — everything
	//     the runtime wrote for that invocation is published, so the host can
	//     finalise it knowing it has the whole of its output and can put END
	//     and REPORT strictly after it.
	HeaderLogSeq = "X-Overcast-Log-Seq"
)

// Frame source values. SrcExtensionPrefix is a prefix, not a source: an
// extension's lines are tagged "ext:<basename>" — see [ExtensionSrc].
const (
	SrcStdout          = "stdout"
	SrcStderr          = "stderr"
	SrcExtensionPrefix = "ext:"
)

// Platform-telemetry observations the init reports on the frame stream. The
// names are AWS's Telemetry API event types with the "platform." prefix
// removed; the host puts it back when it marshals the record, because the host
// is the side that owns the AWS schema — see [Record].
const (
	// RecInitStart is the runtime child being spawned: the INIT phase has
	// begun. It is published before the child can have written a byte, so it
	// is the first thing in the stream.
	RecInitStart = "initStart"

	// RecInitRuntimeDone is the runtime's first GET /next: it has finished
	// initialising and is asking for work. It is published before that request
	// is forwarded, so its seq is at or below the one stamped on it — which is
	// what puts it, and everything the INIT phase printed, ahead of the host's
	// first START with no further synchronisation.
	RecInitRuntimeDone = "initRuntimeDone"

	// RecInitReport summarises the phase [RecInitRuntimeDone] closed. It is
	// published immediately after it, in that order, as AWS emits the pair.
	RecInitReport = "initReport"

	// RecInvokeDone is the runtime answering an invocation: its POST to
	// /response or /error arriving at the proxy. It carries the metrics only
	// the execution environment can measure — how long the runtime held the
	// invocation, and the size of the payload it produced — and is published
	// before the answer is forwarded, so it rides at or below the seq stamped
	// on that answer and the host has ingested it by the time it writes END.
	// The invocation it describes travels in [Frame.Req]. The host owns the
	// record's status and everything else about platform.runtimeDone.
	RecInvokeDone = "invokeDone"
)

// Phase outcomes: the subset of the Telemetry API's Status enum the init can
// report honestly. The host validates against its own set before marshalling.
const (
	// StatusSuccess — the runtime reached its first GET /next.
	StatusSuccess = "success"
	// StatusError — the runtime child exited before it ever polled for work,
	// so the phase this record closes never completed.
	StatusError = "error"
)

// Record is a platform-telemetry observation the init made, carried on the
// frame stream so that it takes its place in the very same sequence as the
// lines around it. Ordering between a record and the output either side of it
// therefore needs no synchronisation at all: it is the [Frame.Seq] invariant
// the log lines already rely on.
//
// It is deliberately *not* the AWS event. The init reports what it saw; the
// host — which knows the function, its version and how the environment was
// initialised — marshals the Telemetry API record from it. One owner for the
// AWS schema, and no AWS field names inside the container.
type Record struct {
	// Type is which observation this is: [RecInitStart], [RecInitRuntimeDone]
	// or [RecInitReport]. Empty on an ordinary line frame, which is what tells
	// the two kinds apart.
	Type string `json:"type"`

	// Status is the phase's outcome, on the records that carry one:
	// [StatusSuccess] or [StatusError].
	Status string `json:"status,omitempty"`

	// DurationMs is a span measured inside the execution environment, in
	// fractional milliseconds. On [RecInitReport]: the runtime child being
	// spawned to its first GET /next. On [RecInvokeDone]: the invocation
	// being handed to the runtime to its answer arriving back at the proxy.
	DurationMs float64 `json:"durationMs,omitempty"`

	// ProducedBytes is the size of the payload the runtime posted for the
	// invocation [RecInvokeDone] describes — the declared Content-Length of
	// its answer. Absent (nil) when the runtime streamed the answer without
	// declaring a length, in which case the host falls back to the size it
	// measures itself; zero is a real value (an empty error payload), which
	// is why this is a pointer and not an omitempty int.
	ProducedBytes *int64 `json:"producedBytes,omitempty"`

	// Spans are the sub-measurements of the phase [RecInvokeDone] describes,
	// as the init observed them. Times are the container's clock, which in
	// Docker is the host's kernel clock.
	Spans []RecSpan `json:"spans,omitempty"`
}

// RecSpan is one measured slice of an invocation, matching the Telemetry API
// Span object the host renders it into: a name, when it started, how long it
// took.
type RecSpan struct {
	Name       string  `json:"name"`
	StartMs    int64   `json:"startMs"`
	DurationMs float64 `json:"durationMs"`
}

// ExtensionSrc returns the [Frame.Src] value for an external extension's
// output.
func ExtensionSrc(name string) string { return SrcExtensionPrefix + name }

// ExtensionName reports the extension a [Frame.Src] belongs to. It returns
// false for the runtime's own sources and for a malformed or empty extension
// name.
func ExtensionName(src string) (string, bool) {
	name, ok := strings.CutPrefix(src, SrcExtensionPrefix)
	if !ok || name == "" {
		return "", false
	}
	return name, true
}

// Frame is one line of container output, as it travels from the init to the
// host. Frames are newline-delimited JSON: exactly one object per line, in
// publication order.
type Frame struct {
	// Seq is the frame's position in this init's stream, starting at 1 and
	// increasing by one per frame. It is the only ordering authority: the
	// host de-duplicates a replayed backlog by Seq, and X-Overcast-Log-Seq
	// refers to it.
	Seq uint64 `json:"seq"`

	// Req is the invocation this line belongs to: the request that was in
	// flight when the init read the line. Empty during INIT and between
	// invocations — a line the runtime writes after it has POSTed its
	// response and before it asks for the next invocation belongs to neither,
	// exactly as on AWS, where it surfaces under the next request.
	Req string `json:"req,omitempty"`

	// Src is where the line came from: [SrcStdout], [SrcStderr] or
	// "ext:<name>".
	Src string `json:"src,omitempty"`

	// T is the init's clock when the line was read, in Unix milliseconds. It
	// is for display only; ordering comes from Seq.
	T int64 `json:"t"`

	// Msg is the line, without its trailing newline.
	Msg string `json:"msg,omitempty"`

	// Gap is non-zero only on a gap frame: the number of frames that were
	// dropped from the init's replay backlog because the log connection was
	// down for long enough to overflow it. A gap frame carries the Seq of the
	// last frame that was lost, so a host tracking contiguity still sees an
	// unbroken sequence and learns exactly how much it will never receive.
	// Zero — and omitted — on every ordinary frame.
	Gap uint64 `json:"gap,omitempty"`

	// Rec makes this frame a platform-telemetry record rather than a line of
	// output: [Record.Type] names the observation and Msg is empty. It is
	// omitted entirely on a line frame, so a line looks on the wire exactly as
	// it always did.
	//
	// Version skew is not something this field has to survive. The host and
	// the init are compiled into one binary and shipped together — the init is
	// embedded in the Overcast executable (internal/services/lambda/initbin) —
	// so a host never meets a frame written by a different initproto than its
	// own, and there is no compatibility machinery here for that reason. What
	// the decoder does tolerate is the general case it always has: unknown
	// members are ignored, absent ones take their zero value.
	Rec Record `json:"rec,omitzero"`
}

// Encode writes f to w as one NDJSON line, terminating newline included.
func (f Frame) Encode(w io.Writer) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

// Decode reads the next frame from r. Blank lines are skipped; unknown fields
// are ignored, so a newer init can add one without breaking an older host. It
// returns [io.EOF] when the stream ends cleanly between frames.
//
// r is a *bufio.Reader rather than an io.Reader because framing is the
// caller's stream, not the frame's: a decoder that wrapped the reader itself
// would strand buffered bytes between calls. The host reads a chunked request
// body with bufio.NewReader(req.Body) and calls this in a loop.
func Decode(r *bufio.Reader) (Frame, error) {
	for {
		line, err := r.ReadBytes('\n')
		line = bytes.TrimRight(line, "\r\n")
		if len(line) == 0 {
			if err != nil {
				return Frame{}, err
			}
			continue // blank line between frames
		}
		if err != nil && err != io.EOF {
			return Frame{}, err
		}
		var f Frame
		if err := json.Unmarshal(line, &f); err != nil {
			return Frame{}, err
		}
		return f, nil
	}
}
