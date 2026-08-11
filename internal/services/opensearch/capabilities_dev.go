//go:build dev

package opensearch

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// Domains
		capabilities.Capability{Service: "opensearch", Operation: "CreateDomain", Category: "Domains",
			Status: capabilities.StatusWIP, Notes: "creates a domain; Overcast does not serve the binding AWS models, so no SDK reaches it (#856)"},
		capabilities.Capability{Service: "opensearch", Operation: "DescribeDomain", Category: "Domains",
			Status: capabilities.StatusWIP, Notes: "returns domain details; Overcast does not serve the binding AWS models, so no SDK reaches it (#856)"},
		capabilities.Capability{Service: "opensearch", Operation: "DescribeDomains", Category: "Domains",
			Status: capabilities.StatusWIP, Notes: "batch describe; Overcast does not serve the binding AWS models, so no SDK reaches it (#856)"},
		capabilities.Capability{Service: "opensearch", Operation: "ListDomainNames", Category: "Domains",
			Status: capabilities.StatusWIP, Notes: "lists all domain names; Overcast does not serve the binding AWS models, so no SDK reaches it (#856)"},
		capabilities.Capability{Service: "opensearch", Operation: "DeleteDomain", Category: "Domains",
			Status: capabilities.StatusWIP, Notes: "deletes a domain; Overcast does not serve the binding AWS models, so no SDK reaches it (#856)"},

		// Tags
		capabilities.Capability{Service: "opensearch", Operation: "AddTags", Category: "Tags",
			Status: capabilities.StatusWIP, Notes: "adds tags to a domain; Overcast does not serve the binding AWS models, so no SDK reaches it (#856)"},
		capabilities.Capability{Service: "opensearch", Operation: "ListTags", Category: "Tags",
			Status: capabilities.StatusWIP, Notes: "lists tags for a domain; Overcast does not serve the binding AWS models, so no SDK reaches it (#856)"},
		capabilities.Capability{Service: "opensearch", Operation: "RemoveTags", Category: "Tags",
			Status: capabilities.StatusWIP, Notes: "removes tags; Overcast does not serve the binding AWS models, so no SDK reaches it (#856)"},
	)
}
