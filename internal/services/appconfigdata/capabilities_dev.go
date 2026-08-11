//go:build dev

package appconfigdata

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		capabilities.Capability{Service: "appconfigdata", Operation: "StartConfigurationSession", Category: "Sessions",
			Status: capabilities.StatusSupported, Notes: "Starts a polling session; returns `InitialConfigurationToken`; honours `RequiredMinimumPollIntervalInSeconds`",
			DocsURL: "[docs](https://docs.aws.amazon.com/appconfig/2021-11-11/APIReference/API_appconfigdata_StartConfigurationSession.html)"},
		capabilities.Capability{Service: "appconfigdata", Operation: "GetLatestConfiguration", Category: "Sessions",
			Status: capabilities.StatusSupported, Notes: "Takes the token as the `configuration_token` query parameter; returns the configuration as the response payload with the session state in headers; empty payload when unchanged since the last poll",
			DocsURL: "[docs](https://docs.aws.amazon.com/appconfig/2021-11-11/APIReference/API_appconfigdata_GetLatestConfiguration.html)"},
	)
}
