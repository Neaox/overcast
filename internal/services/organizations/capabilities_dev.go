//go:build dev

package organizations

import "github.com/overcast-sh/overcast/internal/capabilities"

func init() {
	capabilities.Default.RegisterForService(serviceName,
		capabilities.Capability{Operation: "DescribeOrganization", Status: capabilities.StatusInert},

		// Tier 1 policy surface (docs/plans/inert-tier-rollout.md §0). Inert
		// is the honest status: policy metadata round-trips faithfully, and
		// no policy is ever attached to anything or enforced against
		// anything.
		capabilities.Capability{Operation: "CreatePolicy", Category: "Policy operations", Status: capabilities.StatusInert,
			Notes: "Stores the policy and derives its ID and ARN. The document is never evaluated."},
		capabilities.Capability{Operation: "DescribePolicy", Category: "Policy operations", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "UpdatePolicy", Category: "Policy operations", Status: capabilities.StatusInert,
			Notes: "Merges the members the caller sent; the policy ARN is stable across a rename."},
		capabilities.Capability{Operation: "DeletePolicy", Category: "Policy operations", Status: capabilities.StatusInert,
			Notes: "PolicyInUseException is unreachable: attaching a policy is not emulated, so no policy can be in use."},
		capabilities.Capability{Operation: "ListPolicies", Category: "Policy operations", Status: capabilities.StatusInert,
			Notes: "Filters by the required policy type and paginates. An invalid NextToken is rejected, never restarted."},

		// The organizational-unit tree (docs/plans/inert-tier-rollout.md §0).
		// Units are stored, nested and named exactly as AWS shapes them —
		// and nothing is ever placed in one, because accounts are not
		// emulated.
		capabilities.Capability{Operation: "ListRoots", Category: "Root operations", Status: capabilities.StatusInert,
			Notes: "One root per organization, with an ID derived from the organization ID. PolicyTypes is always empty: enabling one is not emulated."},

		capabilities.Capability{Operation: "CreateOrganizationalUnit", Category: "Organizational unit operations", Status: capabilities.StatusInert,
			Notes: "The parent must be the root or an existing unit. Derives the ID, ARN and Path; a duplicate name under one parent is refused."},
		capabilities.Capability{Operation: "DescribeOrganizationalUnit", Category: "Organizational unit operations", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "UpdateOrganizationalUnit", Category: "Organizational unit operations", Status: capabilities.StatusInert,
			Notes: "Renames the unit. Its ID, ARN and Path are stable across the rename, and a sibling's name is refused."},
		capabilities.Capability{Operation: "DeleteOrganizationalUnit", Category: "Organizational unit operations", Status: capabilities.StatusInert,
			Notes: "OrganizationalUnitNotEmptyException covers child units only; accounts are not emulated, so none can be in the way."},
		capabilities.Capability{Operation: "ListOrganizationalUnitsForParent", Category: "Organizational unit operations", Status: capabilities.StatusInert,
			Notes: "Direct children only, paginated. An unknown parent is ParentNotFoundException, not an empty list."},

		capabilities.Capability{Operation: "TagResource", Category: "Tag operations", Status: capabilities.StatusInert,
			Notes: "Policies, organizational units and the root. An account ID returns TargetNotFoundException, since accounts are not stored."},
		capabilities.Capability{Operation: "UntagResource", Category: "Tag operations", Status: capabilities.StatusInert,
			Notes: "Policies, organizational units and the root, as for TagResource."},
		capabilities.Capability{Operation: "ListTagsForResource", Category: "Tag operations", Status: capabilities.StatusInert,
			Notes: "Policies, organizational units and the root, as for TagResource."},
	)
}
