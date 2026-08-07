//go:build dev

package waf

import "github.com/Neaox/overcast/internal/capabilities"

const catWebACLs = "Web ACLs"

func init() {
	capabilities.Default.RegisterForService(serviceName,
		// Web ACLs
		capabilities.Capability{Operation: "CreateWebACL", Category: catWebACLs, Status: capabilities.StatusSupported, Notes: "Returns Summary with Id/LockToken"},
		capabilities.Capability{Operation: "GetWebACL", Category: catWebACLs, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "ListWebACLs", Category: catWebACLs, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "DeleteWebACL", Category: catWebACLs, Status: capabilities.StatusSupported, Notes: "LockToken accepted but not checked"},
		capabilities.Capability{Operation: "TagResource", Category: catWebACLs, Status: capabilities.StatusSupported, Notes: "Adds/merges tags by WebACL ARN"},
		capabilities.Capability{Operation: "UntagResource", Category: catWebACLs, Status: capabilities.StatusSupported, Notes: "Removes tags by key from a WebACL ARN"},
		capabilities.Capability{Operation: "ListTagsForResource", Category: catWebACLs, Status: capabilities.StatusSupported, Notes: "Returns tags for a WebACL ARN"},
	)
}
