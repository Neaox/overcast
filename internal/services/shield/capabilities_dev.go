//go:build dev

package shield

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// Subscription
		capabilities.Capability{Service: "shield", Operation: "DescribeSubscription", Category: "Subscription", Status: capabilities.StatusSupported, Notes: "Returns a minimal active subscription object"},
		// Protections
		capabilities.Capability{Service: "shield", Operation: "CreateProtection", Category: "Protections", Status: capabilities.StatusSupported, Notes: "Creates a protection; requires Name and ARN"},
		capabilities.Capability{Service: "shield", Operation: "DescribeProtection", Category: "Protections", Status: capabilities.StatusSupported, Notes: "Lookup by ProtectionId or ResourceArn"},
		capabilities.Capability{Service: "shield", Operation: "ListProtections", Category: "Protections", Status: capabilities.StatusSupported, Notes: "Lists all protections"},
		capabilities.Capability{Service: "shield", Operation: "DeleteProtection", Category: "Protections", Status: capabilities.StatusSupported, Notes: "Deletes a protection by ID"},
	)
}
