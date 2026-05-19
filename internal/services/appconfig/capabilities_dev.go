//go:build dev

package appconfig

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// Applications
		capabilities.Capability{Service: "appconfig", Operation: "CreateApplication", Category: "Applications",
			Status: capabilities.StatusSupported, Notes: "Creates an application"},
		capabilities.Capability{Service: "appconfig", Operation: "GetApplication", Category: "Applications",
			Status: capabilities.StatusSupported, Notes: "Returns application details"},
		capabilities.Capability{Service: "appconfig", Operation: "ListApplications", Category: "Applications",
			Status: capabilities.StatusSupported, Notes: "Lists all applications"},
		capabilities.Capability{Service: "appconfig", Operation: "DeleteApplication", Category: "Applications",
			Status: capabilities.StatusSupported, Notes: "Deletes an application"},

		// Environments
		capabilities.Capability{Service: "appconfig", Operation: "CreateEnvironment", Category: "Environments",
			Status: capabilities.StatusSupported, Notes: "Creates an environment for an application"},
		capabilities.Capability{Service: "appconfig", Operation: "GetEnvironment", Category: "Environments",
			Status: capabilities.StatusSupported, Notes: "Returns environment details"},
		capabilities.Capability{Service: "appconfig", Operation: "ListEnvironments", Category: "Environments",
			Status: capabilities.StatusSupported, Notes: "Lists environments for an application"},
		capabilities.Capability{Service: "appconfig", Operation: "DeleteEnvironment", Category: "Environments",
			Status: capabilities.StatusSupported, Notes: "Deletes an environment"},

		// Configuration Profiles
		capabilities.Capability{Service: "appconfig", Operation: "CreateConfigurationProfile", Category: "Configuration Profiles",
			Status: capabilities.StatusSupported, Notes: "Creates a configuration profile"},
		capabilities.Capability{Service: "appconfig", Operation: "GetConfigurationProfile", Category: "Configuration Profiles",
			Status: capabilities.StatusSupported, Notes: "Returns configuration profile details"},
		capabilities.Capability{Service: "appconfig", Operation: "ListConfigurationProfiles", Category: "Configuration Profiles",
			Status: capabilities.StatusSupported, Notes: "Lists configuration profiles"},
		capabilities.Capability{Service: "appconfig", Operation: "DeleteConfigurationProfile", Category: "Configuration Profiles",
			Status: capabilities.StatusSupported, Notes: "Deletes a configuration profile"},
	)
}
