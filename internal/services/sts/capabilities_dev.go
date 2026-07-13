//go:build dev

package sts

import "github.com/Neaox/overcast/internal/capabilities"

const (
	catGeneral = "General"
	catUnsup   = "Unsupported"
)

func init() {
	capabilities.Default.RegisterForService(serviceName,
		capabilities.Capability{Operation: "AssumeRole", Category: catGeneral, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "AssumeRoleWithWebIdentity", Category: catGeneral, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "GetCallerIdentity", Category: catGeneral, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "GetFederationToken", Category: catGeneral, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "GetSessionToken", Category: catGeneral, Status: capabilities.StatusSupported},

		// Unsupported operations: no dispatch entry, so requests fall through to
		// the router's Query fallback and return NotImplemented (HTTP 400).
		// DocOnly because they are documented but not dispatched.
		capabilities.Capability{Operation: "AssumeRoleWithSAML", Category: catUnsup, Status: capabilities.StatusUnsupported, Notes: "Returns NotImplemented", DocOnly: true},
		capabilities.Capability{Operation: "AssumeRoot", Category: catUnsup, Status: capabilities.StatusUnsupported, Notes: "Returns NotImplemented", DocOnly: true},
		capabilities.Capability{Operation: "DecodeAuthorizationMessage", Category: catUnsup, Status: capabilities.StatusUnsupported, Notes: "Returns NotImplemented", DocOnly: true},
		capabilities.Capability{Operation: "GetAccessKeyInfo", Category: catUnsup, Status: capabilities.StatusUnsupported, Notes: "Returns NotImplemented", DocOnly: true},
		capabilities.Capability{Operation: "GetDelegatedAccessToken", Category: catUnsup, Status: capabilities.StatusUnsupported, Notes: "Returns NotImplemented", DocOnly: true},
		capabilities.Capability{Operation: "GetWebIdentityToken", Category: catUnsup, Status: capabilities.StatusUnsupported, Notes: "Returns NotImplemented", DocOnly: true},
	)
}
