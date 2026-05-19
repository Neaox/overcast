//go:build dev

package appconfigdata

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		capabilities.Capability{Service: "appconfigdata", Operation: "StartConfigurationSession", Category: "Sessions",
			Status: capabilities.StatusSupported, Notes: "Starts a polling session; returns `InitialConfigurationToken`",
			DocsURL: "[docs](https://docs.aws.amazon.com/appconfig/2021-11-11/APIReference/API_appconfigdata_StartConfigurationSession.html)"},
		capabilities.Capability{Service: "appconfigdata", Operation: "GetLatestConfiguration", Category: "Sessions",
			Status: capabilities.StatusSupported, Notes: "Returns current config content; empty body when unchanged since last poll",
			DocsURL: "[docs](https://docs.aws.amazon.com/appconfig/2021-11-11/APIReference/API_appconfigdata_GetLatestConfiguration.html)"},
		capabilities.Capability{Service: "appconfigdata", Operation: "AppConfig-Configuration-Version", Category: "Response headers",
			Status: capabilities.StatusSupported, Notes: "Integer version number (only when content is returned)", DocOnly: true,
			DocsURL: "[docs](https://docs.aws.amazon.com/appconfig/2021-11-11/APIReference/Welcome.html)"},
	)
}
