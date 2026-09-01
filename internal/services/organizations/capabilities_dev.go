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

		capabilities.Capability{Operation: "TagResource", Category: "Tag operations", Status: capabilities.StatusInert,
			Notes: "Policies only. A root, OU or account ID returns TargetNotFoundException, since none of those are stored yet."},
		capabilities.Capability{Operation: "UntagResource", Category: "Tag operations", Status: capabilities.StatusInert,
			Notes: "Policies only, as for TagResource."},
		capabilities.Capability{Operation: "ListTagsForResource", Category: "Tag operations", Status: capabilities.StatusInert,
			Notes: "Policies only, as for TagResource."},
	)
}
