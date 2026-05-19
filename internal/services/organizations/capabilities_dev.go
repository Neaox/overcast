//go:build dev

package organizations

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.RegisterForService(serviceName,
		capabilities.Capability{Operation: "DescribeOrganization", Status: capabilities.StatusInert},
	)
}
