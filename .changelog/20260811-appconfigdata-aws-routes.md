* [appconfigdata] both operations are served at AWS's own bindings — `POST /configurationsessions` and `GET /configuration` — so an unmodified SDK, CDK construct or `aws appconfigdata …` call reaches them instead of answering 501
* [appconfigdata] `GetLatestConfiguration` reads the session token from the `configuration_token` query parameter, which is where every AWS SDK puts it; it used to be a path segment no client sends
- [appconfigdata] the emulator-only `/_appconfigdata/*` path prefix, and the invented `AppConfigData.` `X-Amz-Target` namespace with it
  migration: use AWS's bindings — `POST /configurationsessions`, and `GET /configuration?configuration_token=<token>`
- [appconfigdata] the non-AWS `AppConfig-Configuration-Version` response header on `GetLatestConfiguration`
  migration: the configuration payload and the `Next-Poll-Configuration-Token`, `Next-Poll-Interval-In-Seconds` and `Content-Type` headers are the operation's whole modeled response; AWS never sent a version header here
+! [appconfigdata] `StartConfigurationSession` accepts `RequiredMinimumPollIntervalInSeconds`, reports it back in `Next-Poll-Interval-In-Seconds`, and refuses a poll that arrives before it has elapsed, as AWS does; the member used to be ignored
  migration: poll no more often than the interval the session asked for, or drop `RequiredMinimumPollIntervalInSeconds` from `StartConfigurationSession`
*! [appconfigdata] `StartConfigurationSession` validates that its three identifiers are present and within the modeled 128 characters, and that `RequiredMinimumPollIntervalInSeconds` is between 15 and 86400, answering `BadRequestException`
  migration: supply `ApplicationIdentifier`, `EnvironmentIdentifier` and `ConfigurationProfileIdentifier`, and keep any requested poll interval inside 15-86400 seconds
* [appconfigdata] a configuration token is single-use and expires after 24 hours, as AWS documents; a spent or stale token gets `BadRequestException`
* [appconfigdata] a store failure while reading a session is reported as `InternalError` in place of an invalid-token error, and a single undecodable session record is skipped and logged
* [appconfigdata/router] `detectService` claims `/configuration` and `/configurationsessions`, so these requests are logged and IAM-authorised as `appconfigdata` rather than falling through to S3's bucket routes
