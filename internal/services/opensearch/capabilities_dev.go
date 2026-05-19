//go:build dev

package opensearch

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// Domains
		capabilities.Capability{Service: "opensearch", Operation: "CreateDomain", Category: "Domains",
			Status: capabilities.StatusSupported, Notes: "POST /domain — creates a domain"},
		capabilities.Capability{Service: "opensearch", Operation: "DescribeDomain", Category: "Domains",
			Status: capabilities.StatusSupported, Notes: "GET /domain/{name} — returns domain details"},
		capabilities.Capability{Service: "opensearch", Operation: "DescribeDomains", Category: "Domains",
			Status: capabilities.StatusSupported, Notes: "POST /domain/describe — batch describe"},
		capabilities.Capability{Service: "opensearch", Operation: "ListDomainNames", Category: "Domains",
			Status: capabilities.StatusSupported, Notes: "GET /domain — lists all domain names"},
		capabilities.Capability{Service: "opensearch", Operation: "DeleteDomain", Category: "Domains",
			Status: capabilities.StatusSupported, Notes: "DELETE /domain/{name} — deletes a domain"},

		// Tags
		capabilities.Capability{Service: "opensearch", Operation: "AddTags", Category: "Tags",
			Status: capabilities.StatusSupported, Notes: "POST /tags — adds tags to a domain"},
		capabilities.Capability{Service: "opensearch", Operation: "ListTags", Category: "Tags",
			Status: capabilities.StatusSupported, Notes: "GET /tags — lists tags for a domain"},
		capabilities.Capability{Service: "opensearch", Operation: "RemoveTags", Category: "Tags",
			Status: capabilities.StatusSupported, Notes: "POST /tags-removal — removes tags"},
	)
}
