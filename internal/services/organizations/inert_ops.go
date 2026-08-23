package organizations

import (
	"github.com/Neaox/overcast/internal/inert"
	"github.com/Neaox/overcast/internal/protocol/op"
)

// handWrittenOps is Organizations' pre-existing, hand-written operation
// surface — the table §4.5 says always wins.
//
// It is a declared list rather than something derived from the dispatch
// switch because the precedence rule needs to be checkable: TestInertBindings
// asserts nothing in inertBindings collides with a name here unless
// inert_overrides.txt says so, which turns a shadowed binding from silent
// dead code into a review comment.
var handWrittenOps = []string{
	"DescribeOrganization",
}

// inertBindings is Organizations' Tier 1 operation table, sorted by Op —
// inert.Lookup binary-searches it, and TestInertBindings_AreSorted holds the
// ordering invariant so a future reordering fails loudly instead of quietly
// resolving operations to the wrong handler.
//
// Divergence from §6.1, recorded rather than hidden: the plan wants this as a
// package-level `var bindings = []inert.Binding{…}` — compile-time data with
// no init cost at all. It is built per-Service here because every Invoke is a
// closure over the service's store, which a hand-wired pilot cannot avoid and
// I3's generator can (its handlers take the service explicitly, so the table
// becomes a true package-level literal). What the pilot already buys is the
// larger half of §6.1's argument: one slice literal per service instead of a
// map's bucket allocation plus one insert and one interface boxing per
// operation — BenchmarkBindingTableInit in internal/inert measures the map at
// 688 B/op over an eight-operation table.
func (s *Service) inertBindings() []inert.Binding {
	return []inert.Binding{
		{Op: "CreatePolicy", Class: inert.ClassCreate, Resource: "policy",
			Invoke: op.NewTyped("CreatePolicy", s.createPolicy)},
		{Op: "DeletePolicy", Class: inert.ClassDelete, Resource: "policy",
			Invoke: op.NewTyped("DeletePolicy", s.deletePolicy)},
		{Op: "DescribePolicy", Class: inert.ClassRead, Resource: "policy",
			Invoke: op.NewTyped("DescribePolicy", s.describePolicy)},
		{Op: "ListPolicies", Class: inert.ClassList, Resource: "policy",
			Invoke: op.NewTyped("ListPolicies", s.listPolicies)},
		{Op: "ListTagsForResource", Class: inert.ClassListTags, Resource: "policy",
			Invoke: op.NewTyped("ListTagsForResource", s.listTagsForResource)},
		{Op: "TagResource", Class: inert.ClassTag, Resource: "policy",
			Invoke: op.NewTyped("TagResource", s.tagResource)},
		{Op: "UntagResource", Class: inert.ClassUntag, Resource: "policy",
			Invoke: op.NewTyped("UntagResource", s.untagResource)},
		{Op: "UpdatePolicy", Class: inert.ClassUpdate, Resource: "policy",
			Invoke: op.NewTyped("UpdatePolicy", s.updatePolicy)},
	}
}
