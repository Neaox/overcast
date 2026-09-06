//go:build dev

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// What every source emitter needs, in the IR's vocabulary rather than in one
// language's.
//
// cmd/compatgen writes real source for more than one typed backend — Go
// (emit_go.go) and Java (emit_java.go) today, with .NET and Rust behind them —
// and each walks the same IR asking the same questions: which calls does this
// group make, what JSON type is this value, what number does this literal
// carry, and do two names fold to one identifier. Answered once here they
// cannot drift between backends, and the fourth copy per language that a
// per-emitter helper invites never gets written.
//
// A helper belongs here when it reads the IR and nothing else. Anything that
// spells a type, a name or a literal is that backend's own and stays in its
// emit_<lang>*.go.

// callsOf collects every call a group makes: setup, each test's primary call,
// every clause's call however deeply an eventually nests it, and teardown.
func callsOf(g group) []call {
	var out []call
	out = append(out, g.Setup...)
	var fromClause func(a assertion)
	fromClause = func(a assertion) {
		if a.Call != nil {
			out = append(out, *a.Call)
		}
		if a.Assert != nil {
			fromClause(*a.Assert)
		}
	}
	for _, t := range g.Tests {
		out = append(out, t.Call)
		for _, a := range t.Assert {
			fromClause(a)
		}
	}
	out = append(out, g.Teardown...)
	return out
}

// valueKind names an IR value's JSON type for an error message.
func valueKind(v any) string {
	switch value := v.(type) {
	case nil:
		return "null"
	case string:
		return "a string"
	case bool:
		return "a boolean"
	case json.Number, float64, int:
		return "a number"
	case []any:
		return "a list"
	case map[string]any:
		if len(value) == 0 {
			return "an object"
		}
		return "an object (" + strings.Join(sortedKeys(value), ", ") + ")"
	}
	return fmt.Sprintf("a %T", v)
}

// ---------------------------------------------------------------------------
// Numbers
// ---------------------------------------------------------------------------

// irNumber is one IR number, kept as the scenario file wrote it.
//
// The text is parsed as an integer first and only then as a float, because a
// float64 is not a faithful carrier of a JSON number: it rounds silently above
// 2^53 (9007199254740993 becomes …992) and saturates at 2^63, so a backend that
// converted through one would emit a literal that compiles and is not the
// number the scenario asked for. Both values are within reach of a scenario
// file — an account id is sixteen digits — so this is a wrong request waiting
// rather than a theoretical loss.
type irNumber struct {
	// Text is the literal as written, so an error message quotes what the
	// scenario said rather than what a conversion made of it.
	Text string
	// Float is the value as a float64; ±Inf when the literal overflows one.
	Float float64
	// Int is the exact value, valid only when Whole and Fits are both true.
	Int int64
	// Whole says the literal denotes a whole number, whether or not it fits.
	Whole bool
	// Fits says that whole number is exactly an int64 — the widest integer any
	// backend emits, so a literal wider than one is out of range everywhere.
	Fits bool
	// Representable says a float64 carries the literal at all. A literal that
	// overflows or underflows one is still a number, and saying "out of range"
	// about it is more use than saying it is not one.
	Representable bool
}

// numberOf reports the number an IR value carries, and whether it is one.
func numberOf(v any) (irNumber, bool) {
	switch n := v.(type) {
	case json.Number:
		return parseIRNumber(n.String())
	case float64:
		return parseIRNumber(strconv.FormatFloat(n, 'g', -1, 64))
	case int:
		return irNumber{
			Text: strconv.Itoa(n), Float: float64(n), Int: int64(n),
			Whole: true, Fits: true, Representable: true,
		}, true
	}
	return irNumber{}, false
}

func parseIRNumber(text string) (irNumber, bool) {
	if i, err := strconv.ParseInt(text, 10, 64); err == nil {
		return irNumber{
			Text: text, Float: float64(i), Int: i,
			Whole: true, Fits: true, Representable: true,
		}, true
	}
	out := irNumber{Text: text}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil {
		var numErr *strconv.NumError
		if !errors.As(err, &numErr) || !errors.Is(numErr.Err, strconv.ErrRange) {
			return irNumber{}, false
		}
		// Out of a float64's range in either direction. ParseFloat still hands
		// back its nearest value (±Inf, or a zero), which is deliberately not
		// kept: emitting it would silently change the number.
		out.Float = f
		return out, true
	}
	out.Float, out.Representable = f, true
	// A whole number too wide for an int64 — 2^63, or 1e300 — is still whole,
	// and reporting it as such is what lets an integer member refuse it as out
	// of range rather than as "not a whole number", which it is not.
	out.Whole = f == math.Trunc(f)
	return out, true
}

// ---------------------------------------------------------------------------
// Identifier collisions
// ---------------------------------------------------------------------------

// nameClaim is one emitted identifier and what claimed it.
type nameClaim struct{ name, owner string }

// uniqueNames refuses a service whose group and test names collide once folded
// into one backend's identifiers. Two names differing only in where their
// hyphens fall would otherwise emit two methods of the same name, and the suite
// would fail to build with no indication of which pair caused it.
//
// language is the noun the message uses ("Go", "Java"): the fault is the same
// in every backend, and only the identifier scheme that produced it differs.
func uniqueNames(service, language string, claims []nameClaim) error {
	seen := map[string]string{}
	for _, c := range claims {
		if first, dup := seen[c.name]; dup {
			return fmt.Errorf("%s: %s and %s both emit the %s method %s; rename one",
				service, first, c.owner, language, c.name)
		}
		seen[c.name] = c.owner
	}
	return nil
}

// ---------------------------------------------------------------------------
// Refusals
// ---------------------------------------------------------------------------

// refusalChecks is one backend's answer to "can this be emitted", which is the
// only part of refusal detection that is language-specific.
//
// Both hooks report by returning an error. Each backend answers by attempting
// the very spelling emission would write, through a throwaway speller, so one
// code path decides "can this be emitted" and "how" and the two cannot drift.
type refusalChecks struct {
	// call refuses a whole call before its members are looked at, naming the
	// member the refusal is recorded under. May be nil.
	call func(op string) (member string, err error)
	// member refuses one of a call's input members.
	member func(op, member string, v any) error
}

// refusals reports the members of a group's calls a backend cannot express,
// as gaps carrying reason. A group with any is not emitted and is scoped away
// from that suite: a suite that cannot execute a group must not be listed as
// able to.
func refusals(gen *generation, g group, reason string, checks refusalChecks) []gap {
	var out []gap
	seen := map[string]bool{}
	record := func(op, member, detail string) {
		key := op + "." + member
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, gap{
			Service:   gen.scenario.Service,
			Operation: op,
			Group:     g.Name,
			Reason:    reason + ":" + member,
			Detail:    detail,
		})
	}
	for _, c := range callsOf(g) {
		if checks.call != nil {
			if member, err := checks.call(c.Op); err != nil {
				record(c.Op, member, err.Error())
				continue
			}
		}
		for _, member := range sortedValueKeys(c.Params) {
			if err := checks.member(c.Op, member, c.Params[member]); err != nil {
				record(c.Op, member, err.Error())
			}
		}
	}
	sortGaps(out)
	return out
}
