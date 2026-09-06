package scenario

import (
	"fmt"
	"math"
	"strings"
	"sync"
)

// Value expressions (compat/model/README.md § Values), as Go.
//
// The IR's five forms are five constructors here. A value is ordinary Go data
// — a string, a number, a bool, a []any, a map[string]any — and a Value
// anywhere inside it is an expression to evaluate, exactly as an object with
// one $-prefixed key is an expression in the JSON the interpreters read. There
// are no conditionals, no arithmetic and no scripting: eight implementations
// have to agree on every value.
//
//	{"$lit": v}        → the Go literal itself
//	{"$ref": "q.url"}  → Ref("q.url")
//	{"$name": "q"}     → Name("q")
//	{"$concat": [...]} → Concat(...)
//	{"$index": [v, n]} → Index(v, n)

// A Value is one deferred value expression. It is deferred rather than
// evaluated where it is written because a clause is built before the test's
// primary call runs, and a $ref that call exports must not be read until it
// has been.
type Value func(b *Binder) (any, error)

// Lit wraps a literal that would otherwise be mistaken for something else. It
// is rarely needed — a bare Go literal is already a literal — and exists for
// the IR's `$lit`, whose whole job is to stop an object being read as an
// expression.
func Lit(v any) Value { return func(*Binder) (any, error) { return v, nil } }

// Ref reads a context path a previous call exported.
func Ref(path string) Value {
	return func(b *Binder) (any, error) {
		v, ok := b.bag.get(path)
		if !ok {
			return nil, &refError{path: path}
		}
		return v, nil
	}
}

// Name is the IR's only way to name a resource: {runId}-{group}-{suffix},
// with the group token the whole group name and no shortening anywhere. That
// is what makes the name-hygiene convention hold by construction, and what
// lets the orphan sweep find anything a crashed run left behind.
func Name(suffix string) Value {
	return func(b *Binder) (any, error) { return b.runID + "-" + b.group + "-" + suffix, nil }
}

// Concat joins its parts. A part that is a bare string is a literal; anything
// else is an expression that must evaluate to a string.
func Concat(parts ...any) Value {
	return func(b *Binder) (any, error) {
		var out strings.Builder
		for _, part := range parts {
			v, err := b.eval(part)
			if err != nil {
				return nil, err
			}
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("concat part evaluated to %s, which is not a string", render(v))
			}
			out.WriteString(s)
		}
		return out.String(), nil
	}
}

// Index takes element n of a list-valued expression.
func Index(list any, n int) Value {
	return func(b *Binder) (any, error) {
		v, err := b.eval(list)
		if err != nil {
			return nil, err
		}
		items, ok := v.([]any)
		if !ok {
			return nil, fmt.Errorf("index applies to a list, got %s", render(v))
		}
		if n < 0 || n >= len(items) {
			return nil, fmt.Errorf("index %d is past the end of a list of %d", n, len(items))
		}
		return items[n], nil
	}
}

// refError is an unresolvable $ref: an error for the step that carries it, and
// the one failure a teardown step is allowed to be skipped for.
type refError struct{ path string }

func (e *refError) Error() string { return fmt.Sprintf("context path %q is not set", e.path) }

// contextBag is the map from context path ("queue.url") to value that a
// group's exports fill in and its refs read. It lives on the harness
// TestContext for exactly one group run, so it has the lifetime the IR gives a
// group's context — and it is mutex-guarded because the harness may run
// several groups concurrently, each with its own bag reached through the same
// TestContext API.
type contextBag struct {
	mu     sync.Mutex
	values map[string]any
}

func newContextBag() *contextBag { return &contextBag{values: map[string]any{}} }

func (c *contextBag) get(path string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.values[path]
	return v, ok
}

func (c *contextBag) set(path string, v any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values[path] = v
}

// A Binder resolves the deferred parts of one call's typed input. The emitted
// code assigns member by member and never checks an error: a failure is
// recorded here and the whole call is abandoned before anything is sent, which
// is what keeps an emitted Build body a flat list of assignments.
type Binder struct {
	runID string
	group string
	bag   *contextBag
	// member is the member name of the assignment that failed, for
	// failure-message field 4.
	member string
	err    error
}

// Bindable is every Go scalar type an emitted call binds an expression into.
// It is smithy-go's scalar mapping — byte→int8, short→int16, integer→int32,
// long→int64, float→float32, double→float64, plus string and bool — because
// that is the set of field types the generator can spell. An enum reaches this
// as its underlying string and is converted in the emitted source, which is
// what keeps this list of predeclared types closed.
type Bindable interface {
	string | bool | int8 | int16 | int32 | int64 | float32 | float64
}

// Bind evaluates one value expression into the Go type the SDK gave the member
// it is being assigned to. member is the modeled member name, for
// failure-message field 4.
//
// The emitted source writes the pointer, the enum conversion and every
// composite around this call itself — cmd/compatgen resolved the field's type
// from the vendored SDK before writing the file — so all that is left at run
// time is the one thing that cannot be known before the run: what a `$ref`
// into the group's context, or a `$name` built from the run id, evaluates to.
// A mismatch is recorded and abandons the call rather than being coerced: "30"
// is not 30 anywhere else in the IR.
//
// It returns the zero value on failure, which the caller ignores — nothing is
// sent once b.err is set.
func Bind[T Bindable](b *Binder, member string, v any) T {
	var zero T
	if b.err != nil {
		return zero
	}
	value, err := b.eval(v)
	if err != nil {
		b.fail(member, err)
		return zero
	}
	out, err := convert[T](value)
	if err != nil {
		b.fail(member, err)
		return zero
	}
	return out
}

// convert narrows an evaluated document value to one Bindable type.
//
// The type switch is over `any(zero)`, so every branch is a concrete
// predeclared type and the whole thing is decided at run time without
// reflection — which is the point of this file replacing document.go's
// reflective assign.
func convert[T Bindable](v any) (T, error) {
	var zero T
	var out any
	var err error
	switch any(zero).(type) {
	case string:
		out, err = asString(v)
	case bool:
		out, err = asBool(v)
	case int8:
		var n int64
		n, err = asInt(v, math.MinInt8, math.MaxInt8, "int8")
		out = int8(n)
	case int16:
		var n int64
		n, err = asInt(v, math.MinInt16, math.MaxInt16, "int16")
		out = int16(n)
	case int32:
		var n int64
		n, err = asInt(v, math.MinInt32, math.MaxInt32, "int32")
		out = int32(n)
	case int64:
		var n int64
		n, err = asInt(v, math.MinInt64, math.MaxInt64, "int64")
		out = n
	case float32:
		var n float64
		n, err = asFloat(v, "float32")
		out = float32(n)
	case float64:
		out, err = asFloat(v, "float64")
	default:
		// Unreachable: Bindable admits nothing else, and adding a member to it
		// without a branch here would be caught by the suite's own tests.
		err = fmt.Errorf("internal: no conversion to %T", zero)
	}
	if err != nil {
		return zero, err
	}
	return out.(T), nil
}

func asString(v any) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("wanted a string, got %s", render(v))
	}
	return s, nil
}

func asBool(v any) (bool, error) {
	out, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("wanted a boolean, got %s", render(v))
	}
	return out, nil
}

func asInt(v any, min, max float64, kind string) (int64, error) {
	n, ok := numberOf(v)
	if !ok {
		return 0, fmt.Errorf("wanted a number, got %s", render(v))
	}
	if n != math.Trunc(n) {
		return 0, fmt.Errorf("wanted a whole number, got %s", render(v))
	}
	if n < min || n > max {
		return 0, fmt.Errorf("wanted a number in range for %s, got %s", kind, render(v))
	}
	return int64(n), nil
}

func asFloat(v any, kind string) (float64, error) {
	n, ok := numberOf(v)
	if !ok {
		return 0, fmt.Errorf("wanted a %s, got %s", kind, render(v))
	}
	return n, nil
}

// numberOf accepts every spelling of a number a value expression can produce:
// a Go literal a scenario.Lit carries, and a float64 read back out of a
// document. A string is deliberately not accepted — see Bind.
func numberOf(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

func (b *Binder) fail(member string, err error) {
	b.member, b.err = member, err
}

// eval evaluates one value: a Value is an expression, a []any is a list of
// values, a map[string]any is a structure or map of values, and anything else
// is itself — normalised the way a document is, so a Go literal and a value
// read back out of a response compare in the same type system.
func (b *Binder) eval(v any) (any, error) {
	switch t := v.(type) {
	case Value:
		out, err := t(b)
		if err != nil {
			return nil, err
		}
		return b.eval(out)
	case []any:
		out := make([]any, 0, len(t))
		for _, item := range t {
			ev, err := b.eval(item)
			if err != nil {
				return nil, err
			}
			out = append(out, ev)
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, member := range t {
			ev, err := b.eval(member)
			if err != nil {
				return nil, err
			}
			out[k] = ev
		}
		return out, nil
	}
	if doc, ok := toDocument(v); ok {
		return doc, nil
	}
	return nil, nil
}
