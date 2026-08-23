package inert_test

import (
	"context"
	"testing"

	"github.com/Neaox/overcast/internal/inert"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/op"
)

type emptyIn struct{}
type emptyOut struct{}

func stubOp(name string) op.Operation {
	return op.NewTyped[emptyIn, emptyOut](name, func(context.Context, *emptyIn) (*emptyOut, *protocol.AWSError) {
		return &emptyOut{}, nil
	})
}

// sortedBindings is a realistically sized table: eight operations, the shape
// a single-resource Tier 1 service produces.
func sortedBindings() []inert.Binding {
	names := []string{
		"CreateWidget", "DeleteWidget", "DescribeWidget", "ListTagsForResource",
		"ListWidgets", "TagResource", "UntagResource", "UpdateWidget",
	}
	out := make([]inert.Binding, 0, len(names))
	for _, n := range names {
		out = append(out, inert.Binding{Op: n, Class: inert.ClassRead, Resource: "widget", Invoke: stubOp(n)})
	}
	return out
}

func TestLookup_FindsEveryBinding(t *testing.T) {
	bindings := sortedBindings()
	for _, want := range bindings {
		got, ok := inert.Lookup(bindings, want.Op)
		if !ok {
			t.Fatalf("Lookup(%q) reported not found", want.Op)
		}
		if got.Op != want.Op {
			t.Fatalf("Lookup(%q) returned the binding for %q", want.Op, got.Op)
		}
	}
}

func TestLookup_MissesAreReportedNotGuessed(t *testing.T) {
	bindings := sortedBindings()
	// Names chosen to land before, between and after real entries, since a
	// broken binary search typically returns a *neighbour* rather than
	// nothing.
	for _, name := range []string{"", "AttachWidget", "CreateWidgetX", "ListWidget", "ZzzWidget"} {
		if got, ok := inert.Lookup(bindings, name); ok {
			t.Fatalf("Lookup(%q) = %q, want not found", name, got.Op)
		}
	}
}

func TestLookup_EmptyTable(t *testing.T) {
	if _, ok := inert.Lookup(nil, "CreateWidget"); ok {
		t.Fatal("Lookup over a nil table reported found")
	}
}

// TestSorted_CatchesAnUnorderedOrDuplicatedTable is the guard §6.1's
// sorted-slice design needs. Lookup's precondition is invisible at the call
// site, so a generator template that emits bindings out of order would
// otherwise resolve some operations to the wrong handler in silence.
func TestSorted_CatchesAnUnorderedOrDuplicatedTable(t *testing.T) {
	if !inert.Sorted(sortedBindings()) {
		t.Fatal("Sorted rejected a correctly ordered table")
	}

	unordered := sortedBindings()
	unordered[0], unordered[1] = unordered[1], unordered[0]
	if inert.Sorted(unordered) {
		t.Fatal("Sorted accepted a table whose first two entries are swapped")
	}

	duplicated := append(sortedBindings(), inert.Binding{Op: "CreateWidget"})
	if inert.Sorted(duplicated) {
		t.Fatal("Sorted accepted a table with a duplicate Op — one of the two entries is unreachable")
	}
}

// TestCollisions_AreReviewedDataNotSilent is §4.5(3): a binding that a
// hand-written operation shadows is dead code, and dead code that looks live
// is how a reviewer comes to believe an operation behaves in a way it does
// not.
func TestCollisions_AreReviewedDataNotSilent(t *testing.T) {
	bindings := sortedBindings()

	if got := inert.Collisions(bindings, []string{"DescribeOrganization"}, nil); len(got) != 0 {
		t.Fatalf("Collisions with no overlap = %v, want none", got)
	}

	got := inert.Collisions(bindings, []string{"ListWidgets", "TagResource"}, nil)
	want := []string{"ListWidgets", "TagResource"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Collisions = %v, want %v", got, want)
	}

	if got := inert.Collisions(bindings, want, want); len(got) != 0 {
		t.Fatalf("Collisions = %v after both were declared in inert_overrides.txt, want none", got)
	}
}

// BenchmarkLookup pins §6.1's claim: dispatch through the binding table
// allocates nothing. Run with -benchmem.
func BenchmarkLookup(b *testing.B) {
	bindings := sortedBindings()
	names := []string{"CreateWidget", "ListWidgets", "UntagResource", "NoSuchOperation"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		binding, ok := inert.Lookup(bindings, names[i%len(names)])
		if ok && binding.Op == "" {
			b.Fatal("impossible: found binding with an empty Op")
		}
	}
}

// BenchmarkLookupMap is the map this design deliberately does not use, at
// the same table size — the number §6.1's "static sorted slices, not
// init-time maps" decision is measured against. The interesting column is
// not this benchmark's ns/op but the init cost a map carries and a static
// slice does not, which BenchmarkBindingTableInit measures.
func BenchmarkLookupMap(b *testing.B) {
	bindings := sortedBindings()
	table := make(map[string]inert.Binding, len(bindings))
	for _, binding := range bindings {
		table[binding.Op] = binding
	}
	names := []string{"CreateWidget", "ListWidgets", "UntagResource", "NoSuchOperation"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		binding, ok := table[names[i%len(names)]]
		if ok && binding.Op == "" {
			b.Fatal("impossible: found binding with an empty Op")
		}
	}
}

// BenchmarkBindingTableInit is the startup cost §6.1 is actually about: what
// one service pays to have an operation table at all. A static sorted slice
// pays nothing (it is compile-time data); the map every service builds in
// New today pays this on every process start, once per service.
//
// The result is parked in a package-level sink on purpose: a map that never
// leaves the loop body is stack-allocated by escape analysis and reports
// 0 B/op, which is not what a service's New actually pays for a table it
// keeps on a struct field.
func BenchmarkBindingTableInit(b *testing.B) {
	bindings := sortedBindings()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		table := make(map[string]inert.Binding, len(bindings))
		for _, binding := range bindings {
			table[binding.Op] = binding
		}
		tableSink = table
	}
	if len(tableSink) == 0 {
		b.Fatal("impossible: empty table")
	}
}

// tableSink keeps BenchmarkBindingTableInit's map alive past the loop so it
// is heap-allocated, the way a real service's operation table is.
var tableSink map[string]inert.Binding
