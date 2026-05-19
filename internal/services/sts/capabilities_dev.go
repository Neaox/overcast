//go:build dev

package sts

import "github.com/Neaox/overcast/internal/capabilities"

const catGeneral = "General"

func init() {
	capabilities.Default.RegisterForService(serviceName,
		capabilities.Capability{Operation: "AssumeRole", Category: catGeneral, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "AssumeRoleWithWebIdentity", Category: catGeneral, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "GetCallerIdentity", Category: catGeneral, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "GetFederationToken", Category: catGeneral, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "GetSessionToken", Category: catGeneral, Status: capabilities.StatusSupported},
	)
}
