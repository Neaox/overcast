// Package conformance turns docs/plans/inert-tier-rollout.md §3 (the inert
// contract) into executable, table-driven tests.
//
// It is a library, not a test suite about itself: a future generated (or
// hand-wired) Tier 1 service imports this package, builds a Fixture that
// describes how to reach its operations at the wire level, and calls Run
// (or Check, for finer control) to assert the service satisfies §3.
//
// # Design
//
// Check runs the whole contract against a Fixture and returns every
// violated clause as a Violation carrying a clause id tied to the plan
// section (e.g. "3.2/roundtrip-fidelity"). Run is the thin testing.T
// wrapper services call directly. Splitting the two means the "does the
// suite actually detect anything?" question — the naive stub in this
// package plus TestNaiveStub_ViolatesExpectedClauses in meta_test.go — can
// be answered honestly with Check, no fake *testing.T required.
//
// A Fixture operates at the wire level: Encode turns a logical field map
// into a real *http.Request in the fixture's protocol (JSON body, form
// body, headers, target/action), and Decode turns the real HTTP response
// back into a logical field map (or a WireError). Encode/Decode are the
// only protocol-specific parts of a Fixture; everything Check asserts
// operates on the resulting map[string]any, so the same clause logic
// exercises every protocol family a Fixture is written for.
package conformance

import (
	"net/http"
	"net/http/httptest"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/protocol/codec"
)

// Class is an operation's §3.1 lifecycle classification.
type Class int

const (
	ClassCreate Class = iota
	ClassRead
	ClassUpdate
	ClassDelete
	ClassList
	ClassVerb
)

func (c Class) String() string {
	switch c {
	case ClassCreate:
		return "Create"
	case ClassRead:
		return "Read"
	case ClassUpdate:
		return "Update"
	case ClassDelete:
		return "Delete"
	case ClassList:
		return "List"
	case ClassVerb:
		return "Verb"
	default:
		return "Unknown"
	}
}

// InputKind selects which shape Fixture.Input should build. A single
// resource's Input function switches on Kind so one Fixture can drive every
// clause without a pile of one-off request builders.
type InputKind int

const (
	// InputFull is a Create input carrying every field ResourceOps.RoundtripFields
	// names, used by the fidelity and update-merge checks.
	InputFull InputKind = iota
	// InputMinimal is a Create input carrying only the resource's required
	// fields — no optional fields, so ResourceOps.OutputOnlyFields must come
	// back absent or at their modeled default (§3.2/no-fabrication).
	InputMinimal
	// InputInvalid is a Create input missing a required field, used by
	// §3.3/invalid-parameter.
	InputInvalid
	// InputUpdate is an Update input naming an existing record (via
	// ResourceOps.IDField) plus a small patch of fields to merge, used by
	// §3.1/update-merge.
	InputUpdate
	// InputIdempotent is a Create input that omits ResourceOps.IDField (so
	// the service must derive the identifier itself) and sets
	// ResourceOps.IdempotencyField to a fixed value, used by §3.5/idempotency.
	InputIdempotent
)

// ResourceOps names the operations and field bindings of one resource under
// test, in the vocabulary op.Typed / the generator's inert.Binding will
// eventually use (§4.3).
type ResourceOps struct {
	// Create, Read, Update, Delete, List are the AWS operation names for
	// this resource's CRUD surface, dispatched through Fixture.Encode/Handler.
	Create, Read, Update, Delete, List string

	// Verb, if set, is a plain (non-CRUD) operation name expected to stay
	// Tier 0 — checked by §3.6/verb-default.
	Verb string

	// IDField is the field name — present in both Create/Update inputs and
	// every output — that addresses one record for Read/Update/Delete.
	IDField string
	// ArnField is the output field name carrying the resource's ARN.
	ArnField string
	// CreationTimeField and ModifiedTimeField are the output field names
	// carrying the two §3.5 timestamps. ModifiedTimeField may be empty for
	// a resource with no LastModifiedTime member.
	CreationTimeField string
	ModifiedTimeField string
	// RoundtripFields are every optional field a Create caller may send
	// that the output shape also carries — §3.2/roundtrip-fidelity asserts
	// each one comes back equal to what was sent.
	RoundtripFields []string
	// OutputOnlyFields are fields the caller never sends (derived / status
	// fields) — §3.2/no-fabrication asserts each one is either absent from
	// a minimal record's output or equal to Defaults[field].
	OutputOnlyFields []string
	// Defaults maps an OutputOnlyFields entry to its modeled default value,
	// when one is declared. A field with no entry here must be absent when
	// unset.
	Defaults map[string]any

	// ItemsField is the List response field holding the array of records.
	ItemsField string
	// TokenRequestField / TokenResponseField are the pagination
	// continuation-token member names on the List request and response
	// respectively (may differ, e.g. Marker vs NextMarker).
	TokenRequestField, TokenResponseField string
	// LimitField is the List request field capping page size (MaxResults,
	// PageSize, Limit, ...).
	LimitField string

	// IdempotencyField is the Create request field (ClientToken,
	// CallerReference, ...) checked by §3.5/idempotency. Empty means the
	// resource models no such field and the check is skipped.
	IdempotencyField string
}

// ErrorCodes are the modeled error code/HTTP-status pairs a Fixture's
// service declares for the four §3.3 error classes.
type ErrorCodes struct {
	NotFound, AlreadyExists, InvalidParameter, InvalidToken                         string
	NotFoundStatus, AlreadyExistsStatus, InvalidParameterStatus, InvalidTokenStatus int
}

// WireError is a protocol-agnostic view of an error response, produced by
// Fixture.Decode. It intentionally carries only what §3.3 asserts on:
// the AWS error code and the HTTP status.
type WireError struct {
	Code       string
	HTTPStatus int
}

// Fixture describes one (service, protocol) pair under test at the wire
// level. One Fixture exercises one Codec; a service with more than one
// supported protocol family gets one Fixture per family.
type Fixture struct {
	// Service is the service key (e.g. "organizations"), used by
	// §3.5/arn's ARN template check.
	Service string
	// Codec is the protocol family this Fixture exercises. Used for
	// diagnostics; Encode/Decode do the actual wire work.
	Codec codec.Codec
	// Handler dispatches a request the way the router would — a real
	// service Handler, or (for the naive stub) a synthetic one exercising
	// the same op.Typed / codec machinery real services use.
	Handler http.Handler
	// Resource names this Fixture's operations and field bindings.
	Resource ResourceOps
	// Errors are the modeled error codes this Fixture's service declares.
	Errors ErrorCodes
	// Input builds a logical field map for one InputKind/seed pair. seed
	// varies the identifying fields across calls so checks can create
	// multiple independent records.
	Input func(kind InputKind, seed int) map[string]any
	// Reset clears all state between checks so each one runs against a
	// clean store, independent of the others.
	Reset func()
	// Clock is the fixture's injectable time source (§3.5). May be nil for
	// a Fixture that skips the timestamp check (Resource.CreationTimeField
	// left empty has the same effect).
	Clock *clock.Mock
	// Encode turns a logical field map into a real, protocol-correct
	// *http.Request for op (an AWS operation name).
	Encode func(op string, fields map[string]any) *http.Request
	// Decode turns a real *http.Response back into a logical field map, or
	// a WireError when the response is an error envelope.
	Decode func(resp *http.Response) (fields map[string]any, wireErr *WireError)
}

// call is the shared request/response round-trip every check uses: encode,
// dispatch through Handler, decode.
func (f Fixture) call(op string, fields map[string]any) (map[string]any, *WireError) {
	req := f.Encode(op, fields)
	rec := httptest.NewRecorder()
	f.Handler.ServeHTTP(rec, req)
	resp := rec.Result()
	return f.Decode(resp)
}
