// Package op defines the typed operation dispatcher used by Overcast's
// Smithy-aligned services.
//
// An Operation is a single AWS API method (e.g. SQS:SendMessage) wired
// to a typed Go function. The dispatcher decodes the request via the
// active Codec into the operation's input struct, invokes the function,
// then writes the typed output back through the same codec.
//
// Generics make this allocation-free at the call site: Typed[In, Out]
// is monomorphised per operation at compile time, so there is no
// reflection on the request hot path. See docs/plans/smithy.md §9.2.
package op

import (
	"context"
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
)

// Operation is a runtime-dispatchable, codec-agnostic AWS operation.
//
// Implementations are produced by NewTyped and stored in per-service
// operation registries keyed by the AWS operation name (e.g.
// "SendMessage"). The router or service handler resolves the codec,
// looks up the operation, and calls Invoke.
type Operation interface {
	// Name is the AWS operation name (e.g. "SendMessage", "GetItem").
	// Used for diagnostics, logging, and routing.
	Name() string

	// Invoke runs the operation: decode the request via c, call the
	// typed function, then write the response (or error) via c.
	//
	// Invoke MUST NOT return — all responses, including errors, are
	// written to w. This matches http.HandlerFunc semantics and lets
	// the caller treat operations interchangeably with raw handlers
	// during the migration.
	Invoke(w http.ResponseWriter, r *http.Request, c codec.Codec)
}

// Limitationer is implemented by an operation's Out type when it wants its
// success response to carry protocol.EmulationLimitationHeader — the same
// channel a raw handler sets by calling protocol.MarkLimitation(w, ...)
// directly. A typed Fn is not handed w (see NewTyped), so this is its only
// way to say "this succeeded, but here is what does not actually work",
// without every future typed operation needing its own bespoke plumbing.
//
// Optional: an Out that does not implement it is unaffected.
type Limitationer interface {
	// EmulationLimitations returns zero or more limitation sentences to mark
	// on the response. Called only after fn returns a non-nil Out with no
	// error — a failed call has its own channel (protocol.AWSError) and marks
	// nothing here.
	EmulationLimitations() []string
}

// markLimitations applies out's limitations, if it has any, to w. Shared by
// Typed and TypedAny so the two Invoke implementations do not drift.
func markLimitations(w http.ResponseWriter, out any) {
	lim, ok := out.(Limitationer)
	if !ok {
		return
	}
	for _, reason := range lim.EmulationLimitations() {
		protocol.MarkLimitation(w, reason)
	}
}

// Typed is the generic implementation of Operation for a function with
// a typed input and typed output.
//
// In and Out are the operation's request and response struct types
// (typically pointers to structs). Fn receives a context for
// cancellation/deadlines and the decoded input; it returns the typed
// output and an optional *protocol.AWSError. A non-nil error is
// rendered through the codec's WriteError; a nil error renders Out
// through WriteResponse with HTTP 200.
//
// Typed is an unexported struct returned through the Operation
// interface to keep the surface narrow; construct one with NewTyped.
type Typed[In any, Out any] struct {
	name string
	fn   func(ctx context.Context, in *In) (*Out, *protocol.AWSError)
}

// NewTyped builds an Operation from a typed function.
//
// name is the AWS operation name. fn is the business logic; it must
// not write to the http.ResponseWriter directly — that's the
// dispatcher's job. fn may return (nil, nil) for void operations,
// which produces an empty success response.
func NewTyped[In any, Out any](
	name string,
	fn func(ctx context.Context, in *In) (*Out, *protocol.AWSError),
) Operation {
	return &Typed[In, Out]{name: name, fn: fn}
}

// Name implements Operation.
func (t *Typed[In, Out]) Name() string { return t.name }

// Invoke implements Operation. The implementation is deliberately tiny
// to keep the hot path inlinable: one decode, one call, one write.
func (t *Typed[In, Out]) Invoke(w http.ResponseWriter, r *http.Request, c codec.Codec) {
	var in In
	if aerr := c.Decode(r, &in); aerr != nil {
		c.WriteError(w, r, aerr)
		return
	}
	out, aerr := t.fn(r.Context(), &in)
	if aerr != nil {
		c.WriteError(w, r, aerr)
		return
	}
	if out == nil {
		c.WriteResponse(w, r, http.StatusOK, nil)
		return
	}
	markLimitations(w, out)
	c.WriteResponse(w, r, http.StatusOK, out)
}

type TypedAny[In any] struct {
	name string
	fn   func(ctx context.Context, in *In) (any, *protocol.AWSError)
}

// NewTypedAny builds an Operation whose success response shape can vary.
//
// Prefer NewTyped when an operation has a single concrete output shape.
// NewTypedAny is for Smithy operations like DynamoDB Scan/Query where one
// request flag selects between distinct wire shapes that must stay byte-stable.
func NewTypedAny[In any](
	name string,
	fn func(ctx context.Context, in *In) (any, *protocol.AWSError),
) Operation {
	return &TypedAny[In]{name: name, fn: fn}
}

func (t *TypedAny[In]) Name() string { return t.name }

func (t *TypedAny[In]) Invoke(w http.ResponseWriter, r *http.Request, c codec.Codec) {
	var in In
	if aerr := c.Decode(r, &in); aerr != nil {
		c.WriteError(w, r, aerr)
		return
	}
	out, aerr := t.fn(r.Context(), &in)
	if aerr != nil {
		c.WriteError(w, r, aerr)
		return
	}
	if out == nil {
		c.WriteResponse(w, r, http.StatusOK, nil)
		return
	}
	markLimitations(w, out)
	c.WriteResponse(w, r, http.StatusOK, out)
}

type Raw struct {
	name string
	fn   http.HandlerFunc
}

// NewRaw adapts an existing http.HandlerFunc into an Operation.
//
// Raw operations are a migration escape hatch for handlers that still own
// their request decoding, response encoding, or custom headers. The codec
// argument is intentionally ignored.
func NewRaw(name string, fn http.HandlerFunc) Operation {
	return &Raw{name: name, fn: fn}
}

func (r *Raw) Name() string { return r.name }

func (r *Raw) Invoke(w http.ResponseWriter, req *http.Request, _ codec.Codec) {
	r.fn(w, req)
}
