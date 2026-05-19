//go:build dev

package acm

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		capabilities.Capability{Service: "acm", Operation: "RequestCertificate", Category: "Certificates", Status: capabilities.StatusSupported, Notes: "Creates a certificate; immediately ISSUED"},
		capabilities.Capability{Service: "acm", Operation: "DescribeCertificate", Category: "Certificates", Status: capabilities.StatusSupported, Notes: "Returns certificate details"},
		capabilities.Capability{Service: "acm", Operation: "ListCertificates", Category: "Certificates", Status: capabilities.StatusSupported, Notes: "Lists all certificates"},
		capabilities.Capability{Service: "acm", Operation: "DeleteCertificate", Category: "Certificates", Status: capabilities.StatusSupported, Notes: "Deletes a certificate by ARN"},
		capabilities.Capability{Service: "acm", Operation: "ListTagsForCertificate", Category: "Tags", Status: capabilities.StatusSupported, Notes: "Lists tags for a certificate"},
		capabilities.Capability{Service: "acm", Operation: "AddTagsToCertificate", Category: "Tags", Status: capabilities.StatusSupported, Notes: "Adds tags to a certificate"},
		capabilities.Capability{Service: "acm", Operation: "RemoveTagsFromCertificate", Category: "Tags", Status: capabilities.StatusSupported, Notes: "Removes tags from a certificate"},
	)
}
