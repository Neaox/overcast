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

	// HeaderLogSeq is added by the init to the forwarded
	// POST /runtime/invocation/{id}/response and .../error requests. It reads
	// "every frame with seq <= N was published on the log stream before this
	// request was forwarded", which is what lets the host finalise an
	// invocation knowing it has the whole of that invocation's output.
	HeaderLogSeq = "X-Overcast-Log-Seq"
)

// Frame source values. SrcExtensionPrefix is a prefix, not a source: an
// extension's lines are tagged "ext:<basename>" — see [ExtensionSrc].
const (
	SrcStdout          = "stdout"
	SrcStderr          = "stderr"
	SrcExtensionPrefix = "ext:"
)

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
