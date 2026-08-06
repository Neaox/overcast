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

		// Hosted Configuration Versions
		capabilities.Capability{Service: "appconfig", Operation: "CreateHostedConfigurationVersion", Category: "Hosted Configuration Versions",
			Status: capabilities.StatusSupported, Notes: "Stores configuration content against a profile; `VersionNumber` auto-increments, `ContentType` defaults to `application/octet-stream`, and content over 1 MB is rejected with `BadRequestException`"},
		capabilities.Capability{Service: "appconfig", Operation: "GetHostedConfigurationVersion", Category: "Hosted Configuration Versions",
			Status: capabilities.StatusSupported, Notes: "Returns the raw content as the response body with the `AppConfig-*` metadata headers"},
		capabilities.Capability{Service: "appconfig", Operation: "ListHostedConfigurationVersions", Category: "Hosted Configuration Versions",
			Status: capabilities.StatusSupported, Notes: "Returns version metadata without content; no pagination (`NextToken` never returned)"},
		capabilities.Capability{Service: "appconfig", Operation: "DeleteHostedConfigurationVersion", Category: "Hosted Configuration Versions",
			Status: capabilities.StatusSupported, Notes: "Deletes a single version; other versions of the profile are untouched"},

		// Tags
		capabilities.Capability{Service: "appconfig", Operation: "TagResource", Category: "Tags",
			Status: capabilities.StatusSupported, Notes: "Adds or overwrites tags on applications, environments, and configuration profiles"},
		capabilities.Capability{Service: "appconfig", Operation: "UntagResource", Category: "Tags",
			Status: capabilities.StatusSupported, Notes: "Removes tags by key from applications, environments, and configuration profiles"},
		capabilities.Capability{Service: "appconfig", Operation: "ListTagsForResource", Category: "Tags",
			Status: capabilities.StatusSupported, Notes: "Returns tags for applications, environments, and configuration profiles"},
	)
}
