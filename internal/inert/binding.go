package inert

import (
	"slices"

	"github.com/Neaox/overcast/internal/protocol/op"
)

// Class is an operation's §3.1 lifecycle classification. It is carried on
// the binding rather than inferred from the operation name at runtime,
// because §3.1 is explicit that the name-prefix fallback has to be
// materialised as reviewed data and never guessed while serving a request.
type Class uint8

const (
	// ClassCreate validates required fields, derives the identifier and ARN,
	// stamps the creation timestamp and persists the whole decoded input.
	ClassCreate Class = iota
	// ClassRead loads by identifier and projects onto the modeled output.
	ClassRead
	// ClassUpdate merges the non-nil input fields over the stored record.
	ClassUpdate
	// ClassDelete removes the record and its tags.
	ClassDelete
	// ClassList scans the collection, stable-sorts and paginates.
	ClassList
	// ClassTag, ClassUntag and ClassListTags are the shared tag surface.
	ClassTag
	ClassUntag
	ClassListTags
	// ClassTransition is §3.6's first verb exception: a state-transition verb
	// bound to a resource this service stores, whose whole effect is a field
	// update on that record. Every other verb stays Tier 0 and gets no
	// binding at all.
	ClassTransition
)

// String returns the class name, for diagnostics and test failures.
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
	case ClassTag:
		return "Tag"
	case ClassUntag:
		return "Untag"
	case ClassListTags:
		return "ListTags"
	case ClassTransition:
		return "Transition"
	default:
		return "Unknown"
	}
}

// Binding is one Tier 1 operation's declarative description: what it is
// called on the wire, what it does to which resource, and the typed
// op.Operation that runs it.
//
// A service's bindings are a package-level `var bindings = []Binding{…}` in
// Op order — compile-time data, not a map built in New. See Lookup.
type Binding struct {
	// Op is the AWS operation name, e.g. "CreatePolicy".
	Op string
	// Class is the operation's §3.1 lifecycle class.
	Class Class
	// Resource names the collection the operation acts on, e.g. "policy".
	Resource string
	// Invoke is the typed operation the dispatcher runs.
	Invoke op.Operation
}

// Lookup binary-searches a sorted []Binding for name. It allocates nothing.
//
// A map would be the obvious choice for a single service and is the wrong
// one at the scale this runtime is built for (§6.1): every service builds
// its operation table in New today, and at ~300 services times dozens of
// operations that is thousands of map inserts and interface allocations on
// every process start, for a table that is entirely known at compile time. A
// sorted slice costs nothing at init and one binary search per dispatch,
// mirroring how internal/awsapi's generated indexes are searched.
//
// The search is hand-rolled rather than sort.Search or slices.BinarySearchFunc
// because both take a closure over name, which escapes to the heap and puts
// an allocation on the dispatch hot path — the exact cost this design exists
// to avoid. BenchmarkLookup pins that at 0 allocs/op.
//
// bindings MUST be sorted by Op. An unsorted slice makes this silently
// return the wrong operation, or none; Sorted plus a per-service test is how
// that is caught at build time rather than in production.
func Lookup(bindings []Binding, name string) (Binding, bool) {
	lo, hi := 0, len(bindings)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if bindings[mid].Op < name {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(bindings) && bindings[lo].Op == name {
		return bindings[lo], true
	}
	return Binding{}, false
}

// Sorted reports whether bindings is strictly ordered by Op — Lookup's
// precondition, and also a duplicate check, since a repeated Op means one of
// the two entries is unreachable.
//
// Every service carrying bindings should assert this in a test. When I3's
// generator starts emitting these slices, a template change that reorders
// them has to fail loudly rather than quietly resolve some operations to the
// wrong handler.
func Sorted(bindings []Binding) bool {
	for i := 1; i < len(bindings); i++ {
		if bindings[i-1].Op >= bindings[i].Op {
			return false
		}
	}
	return true
}

// Collisions returns every binding whose operation is also implemented by
// hand and is not listed in allowed, sorted.
//
// §4.5 is absolute: a hand-written implementation always wins, with no
// configuration and no exception. Dispatch enforces that by consulting the
// hand-written table first, which makes a colliding binding dead code — and
// dead code that looks live is how a reviewer comes to believe an operation
// behaves in a way it does not. So a collision is *reviewed data*: it has to
// appear in the service's checked-in inert_overrides.txt, and this function
// is what a service's test uses to prove no unlisted one has appeared.
func Collisions(bindings []Binding, handWritten, allowed []string) []string {
	var out []string
	for _, b := range bindings {
		if !slices.Contains(handWritten, b.Op) {
			continue
		}
		if slices.Contains(allowed, b.Op) {
			continue
		}
		out = append(out, b.Op)
	}
	slices.Sort(out)
	return out
}
