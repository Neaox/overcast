//go:build dev

package transfer

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.RegisterForService(serviceName,
		capabilities.Capability{Operation: "CreateServer", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "DescribeServer", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "ListServers", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "UpdateServer", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "DeleteServer", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "CreateUser", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "DescribeUser", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "ListUsers", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "UpdateUser", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "DeleteUser", Status: capabilities.StatusInert},
	)
}
