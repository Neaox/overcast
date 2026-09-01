package groups

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/overcast-sh/overcast-compat-cli/internal/awscli"
	"github.com/overcast-sh/overcast-compat-cli/internal/harness"
)

// AppConfigData returns the AppConfig Data (data plane) service group.
//
// The data plane is two operations — StartConfigurationSession and
// GetLatestConfiguration — but neither means anything without an AppConfig
// application, environment, configuration profile and hosted configuration
// version to poll for, so each group builds its own in setup and tears the lot
// down afterwards. Those control-plane calls are signed (awscli.RunSigned):
// Overcast picks the owner of /applications from the SigV4 credential scope,
// so unsigned they reach Service Catalog AppRegistry instead. The data plane
// calls themselves stay unsigned, like every other group here.
func AppConfigData() ServiceGroup {
	g := &appConfigDataGroup{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"appconfigdata-sessions:StartConfigurationSession":                 g.StartConfigurationSession,
			"appconfigdata-sessions:GetLatestConfiguration":                    g.GetLatestConfiguration,
			"appconfigdata-sessions:GetLatestConfigurationWithNextPollToken":   g.GetLatestConfigurationWithNextPollToken,
			"appconfigdata-sessions:GetLatestConfigurationAfterNewVersion":     g.GetLatestConfigurationAfterNewVersion,
			"appconfigdata-sessions:StartConfigurationSessionWithPollInterval": g.StartConfigurationSessionWithPollInterval,

			"appconfigdata-errors:StartConfigurationSessionApplicationNotFound": g.StartConfigurationSessionApplicationNotFound,
			"appconfigdata-errors:StartConfigurationSessionEnvironmentNotFound": g.StartConfigurationSessionEnvironmentNotFound,
			"appconfigdata-errors:StartConfigurationSessionProfileNotFound":     g.StartConfigurationSessionProfileNotFound,
			"appconfigdata-errors:GetLatestConfigurationInvalidToken":           g.GetLatestConfigurationInvalidToken,
			"appconfigdata-errors:GetLatestConfigurationReusedToken":            g.GetLatestConfigurationReusedToken,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"appconfigdata-sessions": g.setupSessions,
			"appconfigdata-errors":   g.setupErrors,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"appconfigdata-sessions": g.teardownFixture,
			"appconfigdata-errors":   g.teardownFixture,
		},
	}
}

type appConfigDataGroup struct{}

// Each group gets its own namer so the two never contend for one AppConfig
// application name — CreateApplication answers ConflictException on a repeat.
var (
	appConfigDataSessionsNamer = harness.NewNamer("acd-sess")
	appConfigDataErrorsNamer   = harness.NewNamer("acd-err")
)

// The configuration the sessions group deploys and then expects back over the
// data plane, byte for byte. It is deliberately not re-serialised anywhere:
// GetLatestConfiguration returns the stored bytes as the response payload, so
// the test compares bytes, not parsed JSON.
const (
	appConfigDataV1Content     = `{"featureFlag":"enabled","retries":3}`
	appConfigDataV1ContentType = "application/json"
	appConfigDataV2Content     = "featureFlag = disabled\nretries = 7\n"
	appConfigDataV2ContentType = "text/plain"
)

// ─── Setup / teardown ─────────────────────────────────────────────────────────

// setupSessions builds the application, environment, configuration profile and
// first hosted configuration version the sessions group polls for.
//
// AWS would also need a deployment before GetLatestConfiguration served
// anything; Overcast does not emulate deployments (CreateDeploymentStrategy and
// StartDeployment both answer 501) and serves the latest hosted version
// instead, so there is no deployment step to make here.
func (g *appConfigDataGroup) setupSessions(_ context.Context, t *harness.TestContext) error {
	if err := g.createFixture(t, appConfigDataSessionsNamer.Name(t)); err != nil {
		return err
	}
	return g.publishVersion(t, appConfigDataV1Content, appConfigDataV1ContentType)
}

// setupErrors builds a fixture for the negative paths that need a real
// application to hang a missing environment or profile off, and a real session
// whose token can be spent and replayed.
func (g *appConfigDataGroup) setupErrors(_ context.Context, t *harness.TestContext) error {
	if err := g.createFixture(t, appConfigDataErrorsNamer.Name(t)); err != nil {
		return err
	}
	return g.publishVersion(t, appConfigDataV1Content, appConfigDataV1ContentType)
}

// createFixture creates the application, environment and hosted-type
// configuration profile, stashing each identifier for the tests and teardown.
func (g *appConfigDataGroup) createFixture(t *harness.TestContext, base string) error {
	app, err := awscli.RunOutputSigned(t.Endpoint, t.Region, "appconfig", "create-application",
		"--name", base,
	)
	if err != nil {
		return fmt.Errorf("setup: CreateApplication: %w", err)
	}
	appID, _ := app["Id"].(string)
	if appID == "" {
		return fmt.Errorf("setup: CreateApplication returned no Id")
	}
	t.Set("application_id", appID)

	env, err := awscli.RunOutputSigned(t.Endpoint, t.Region, "appconfig", "create-environment",
		"--application-id", appID,
		"--name", base+"-env",
	)
	if err != nil {
		return fmt.Errorf("setup: CreateEnvironment: %w", err)
	}
	envID, _ := env["Id"].(string)
	if envID == "" {
		return fmt.Errorf("setup: CreateEnvironment returned no Id")
	}
	t.Set("environment_id", envID)

	prof, err := awscli.RunOutputSigned(t.Endpoint, t.Region, "appconfig", "create-configuration-profile",
		"--application-id", appID,
		"--name", base+"-profile",
		"--location-uri", "hosted",
	)
	if err != nil {
		return fmt.Errorf("setup: CreateConfigurationProfile: %w", err)
	}
	profID, _ := prof["Id"].(string)
	if profID == "" {
		return fmt.Errorf("setup: CreateConfigurationProfile returned no Id")
	}
	t.Set("configuration_profile_id", profID)
	return nil
}

// publishVersion stores one hosted configuration version and records its number
// so teardown can remove it. The content goes in as a file: --content is a blob
// parameter and the CLI reads it with fileb://.
func (g *appConfigDataGroup) publishVersion(t *harness.TestContext, content, contentType string) error {
	contentPath, cleanupContent, err := writeTempFile("oc-appconfigdata-content-*", content)
	if err != nil {
		return err
	}
	defer cleanupContent()

	outPath, cleanupOut, err := writeTempFile("oc-appconfigdata-hcv-*", "")
	if err != nil {
		return err
	}
	defer cleanupOut()

	args := []string{"appconfig", "create-hosted-configuration-version",
		"--application-id", t.GetString("application_id"),
		"--configuration-profile-id", t.GetString("configuration_profile_id"),
		"--content", "fileb://" + filepath.ToSlash(contentPath),
		"--content-type", contentType,
	}
	// Latest-Version-Number makes the write conditional on the version this
	// group last published, which is how a real publisher avoids clobbering a
	// concurrent one.
	if prev := g.publishedVersions(t); len(prev) > 0 {
		args = append(args, "--latest-version-number", fmt.Sprintf("%d", prev[len(prev)-1]))
	}
	args = append(args, outPath)

	out, err := awscli.RunOutputSigned(t.Endpoint, t.Region, args...)
	if err != nil {
		return fmt.Errorf("CreateHostedConfigurationVersion: %w", err)
	}
	version, ok := out["VersionNumber"].(float64)
	if !ok || version == 0 {
		return fmt.Errorf("CreateHostedConfigurationVersion: expected a VersionNumber, got %v", out["VersionNumber"])
	}
	t.Set("published_versions", append(g.publishedVersions(t), int(version)))
	return nil
}

// publishedVersions returns the hosted configuration versions this group has
// created, oldest first.
func (g *appConfigDataGroup) publishedVersions(t *harness.TestContext) []int {
	v, _ := t.Get("published_versions")
	versions, _ := v.([]int)
	return versions
}

// teardownFixture removes everything createFixture and publishVersion made, in
// reverse creation order. AppConfig does not cascade: deleting an application
// leaves its environments, profiles and hosted versions behind, so each one
// needs its own delete.
func (g *appConfigDataGroup) teardownFixture(_ context.Context, t *harness.TestContext) error {
	appID := t.GetString("application_id")
	profID := t.GetString("configuration_profile_id")
	envID := t.GetString("environment_id")

	versions := g.publishedVersions(t)
	for i := len(versions) - 1; i >= 0; i-- {
		awscli.RunSigned(t.Endpoint, t.Region, "appconfig", "delete-hosted-configuration-version", //nolint:errcheck
			"--application-id", appID,
			"--configuration-profile-id", profID,
			"--version-number", fmt.Sprintf("%d", versions[i]),
		)
	}
	if profID != "" {
		awscli.RunSigned(t.Endpoint, t.Region, "appconfig", "delete-configuration-profile", //nolint:errcheck
			"--application-id", appID, "--configuration-profile-id", profID)
	}
	if envID != "" {
		awscli.RunSigned(t.Endpoint, t.Region, "appconfig", "delete-environment", //nolint:errcheck
			"--application-id", appID, "--environment-id", envID)
	}
	if appID != "" {
		awscli.RunSigned(t.Endpoint, t.Region, "appconfig", "delete-application", //nolint:errcheck
			"--application-id", appID)
	}
	return nil
}

// ─── appconfigdata-sessions ───────────────────────────────────────────────────

func (g *appConfigDataGroup) StartConfigurationSession(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "appconfigdata", "start-configuration-session",
		"--application-identifier", t.GetString("application_id"),
		"--environment-identifier", t.GetString("environment_id"),
		"--configuration-profile-identifier", t.GetString("configuration_profile_id"),
	)
	if err != nil {
		return err
	}
	token, _ := out["InitialConfigurationToken"].(string)
	if token == "" {
		return fmt.Errorf("StartConfigurationSession: missing InitialConfigurationToken")
	}
	// The model types the token as ^\S{1,8192}$ — a value with whitespace in it
	// would not survive the round trip through the query string.
	if strings.ContainsAny(token, " \t\r\n") {
		return fmt.Errorf("StartConfigurationSession: token %q contains whitespace", token)
	}
	t.Set("session_token", token)
	return nil
}

// GetLatestConfiguration is the end-to-end assertion: the bytes stored through
// the control plane come back over the data plane unchanged, with the content
// type they were stored under.
func (g *appConfigDataGroup) GetLatestConfiguration(_ context.Context, t *harness.TestContext) error {
	token := t.GetString("session_token")
	if token == "" {
		return fmt.Errorf("GetLatestConfiguration: no token from StartConfigurationSession")
	}
	out, body, err := g.poll(t, token)
	if err != nil {
		return err
	}
	if body != appConfigDataV1Content {
		return fmt.Errorf("GetLatestConfiguration: expected the deployed content %q, got %q",
			appConfigDataV1Content, body)
	}
	if got, _ := out["ContentType"].(string); got != appConfigDataV1ContentType {
		return fmt.Errorf("GetLatestConfiguration: expected ContentType %q, got %q",
			appConfigDataV1ContentType, got)
	}
	next, _ := out["NextPollConfigurationToken"].(string)
	if next == "" {
		return fmt.Errorf("GetLatestConfiguration: missing NextPollConfigurationToken")
	}
	if next == token {
		return fmt.Errorf("GetLatestConfiguration: NextPollConfigurationToken was not rotated")
	}
	interval, _ := out["NextPollIntervalInSeconds"].(float64)
	if interval <= 0 {
		return fmt.Errorf("GetLatestConfiguration: expected a positive NextPollIntervalInSeconds, got %v",
			out["NextPollIntervalInSeconds"])
	}
	t.Set("session_token", next)
	return nil
}

// GetLatestConfigurationWithNextPollToken proves the rotation actually works —
// the token from the previous response is accepted, and because the client is
// already on the latest version the payload comes back empty.
func (g *appConfigDataGroup) GetLatestConfigurationWithNextPollToken(_ context.Context, t *harness.TestContext) error {
	token := t.GetString("session_token")
	if token == "" {
		return fmt.Errorf("GetLatestConfigurationWithNextPollToken: no token from GetLatestConfiguration")
	}
	out, body, err := g.poll(t, token)
	if err != nil {
		return fmt.Errorf("GetLatestConfigurationWithNextPollToken: polling with the rotated token failed: %w", err)
	}
	if body != "" {
		return fmt.Errorf("GetLatestConfigurationWithNextPollToken: expected an empty payload for an unchanged configuration, got %q", body)
	}
	if got, _ := out["ContentType"].(string); got != "" {
		return fmt.Errorf("GetLatestConfigurationWithNextPollToken: expected no ContentType with an empty payload, got %q", got)
	}
	next, _ := out["NextPollConfigurationToken"].(string)
	if next == "" || next == token {
		return fmt.Errorf("GetLatestConfigurationWithNextPollToken: expected a freshly rotated NextPollConfigurationToken, got %s",
			describeRotation(next, token))
	}
	t.Set("session_token", next)
	return nil
}

// GetLatestConfigurationAfterNewVersion publishes a second hosted configuration
// version and asserts the next poll on the same session delivers it, with the
// new content type. Without this, an implementation that cached the first
// payload would still pass everything above.
func (g *appConfigDataGroup) GetLatestConfigurationAfterNewVersion(_ context.Context, t *harness.TestContext) error {
	token := t.GetString("session_token")
	if token == "" {
		return fmt.Errorf("GetLatestConfigurationAfterNewVersion: no token from the previous poll")
	}
	if err := g.publishVersion(t, appConfigDataV2Content, appConfigDataV2ContentType); err != nil {
		return fmt.Errorf("GetLatestConfigurationAfterNewVersion: %w", err)
	}
	out, body, err := g.poll(t, token)
	if err != nil {
		return err
	}
	if body != appConfigDataV2Content {
		return fmt.Errorf("GetLatestConfigurationAfterNewVersion: expected the newly published content %q, got %q",
			appConfigDataV2Content, body)
	}
	if got, _ := out["ContentType"].(string); got != appConfigDataV2ContentType {
		return fmt.Errorf("GetLatestConfigurationAfterNewVersion: expected ContentType %q, got %q",
			appConfigDataV2ContentType, got)
	}
	next, _ := out["NextPollConfigurationToken"].(string)
	if next == "" || next == token {
		return fmt.Errorf("GetLatestConfigurationAfterNewVersion: expected a freshly rotated NextPollConfigurationToken, got %s",
			describeRotation(next, token))
	}
	t.Set("session_token", next)
	return nil
}

// StartConfigurationSessionWithPollInterval asserts the session honours
// RequiredMinimumPollIntervalInSeconds. The only place that value is observable
// is the poll response, so the verification is one GetLatestConfiguration on
// the new session rather than a Describe.
func (g *appConfigDataGroup) StartConfigurationSessionWithPollInterval(_ context.Context, t *harness.TestContext) error {
	const wantInterval = 15 // the model's RequiredMinimumPollIntervalInSeconds minimum
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "appconfigdata", "start-configuration-session",
		"--application-identifier", t.GetString("application_id"),
		"--environment-identifier", t.GetString("environment_id"),
		"--configuration-profile-identifier", t.GetString("configuration_profile_id"),
		"--required-minimum-poll-interval-in-seconds", fmt.Sprintf("%d", wantInterval),
	)
	if err != nil {
		return err
	}
	token, _ := out["InitialConfigurationToken"].(string)
	if token == "" {
		return fmt.Errorf("StartConfigurationSessionWithPollInterval: missing InitialConfigurationToken")
	}
	polled, _, err := g.poll(t, token)
	if err != nil {
		return fmt.Errorf("StartConfigurationSessionWithPollInterval: first poll on the new session failed: %w", err)
	}
	interval, _ := polled["NextPollIntervalInSeconds"].(float64)
	if int(interval) != wantInterval {
		return fmt.Errorf("StartConfigurationSessionWithPollInterval: expected NextPollIntervalInSeconds %d, got %v",
			wantInterval, polled["NextPollIntervalInSeconds"])
	}
	return nil
}

// ─── appconfigdata-errors ─────────────────────────────────────────────────────

func (g *appConfigDataGroup) StartConfigurationSessionApplicationNotFound(_ context.Context, t *harness.TestContext) error {
	return expectAWSFailure(t, "StartConfigurationSessionApplicationNotFound",
		"ResourceNotFoundException", 404,
		"appconfigdata", "start-configuration-session",
		"--application-identifier", "oc-missing-"+t.RunID,
		"--environment-identifier", t.GetString("environment_id"),
		"--configuration-profile-identifier", t.GetString("configuration_profile_id"),
	)
}

func (g *appConfigDataGroup) StartConfigurationSessionEnvironmentNotFound(_ context.Context, t *harness.TestContext) error {
	return expectAWSFailure(t, "StartConfigurationSessionEnvironmentNotFound",
		"ResourceNotFoundException", 404,
		"appconfigdata", "start-configuration-session",
		"--application-identifier", t.GetString("application_id"),
		"--environment-identifier", "oc-missing-"+t.RunID,
		"--configuration-profile-identifier", t.GetString("configuration_profile_id"),
	)
}

func (g *appConfigDataGroup) StartConfigurationSessionProfileNotFound(_ context.Context, t *harness.TestContext) error {
	return expectAWSFailure(t, "StartConfigurationSessionProfileNotFound",
		"ResourceNotFoundException", 404,
		"appconfigdata", "start-configuration-session",
		"--application-identifier", t.GetString("application_id"),
		"--environment-identifier", t.GetString("environment_id"),
		"--configuration-profile-identifier", "oc-missing-"+t.RunID,
	)
}

func (g *appConfigDataGroup) GetLatestConfigurationInvalidToken(_ context.Context, t *harness.TestContext) error {
	outPath, cleanup, err := writeTempFile("oc-appconfigdata-poll-*", "")
	if err != nil {
		return err
	}
	defer cleanup()
	return expectAWSFailure(t, "GetLatestConfigurationInvalidToken",
		"BadRequestException", 400,
		"appconfigdata", "get-latest-configuration",
		"--configuration-token", "oc-not-a-configuration-token-"+t.RunID,
		outPath,
	)
}

// GetLatestConfigurationReusedToken spends a token and then presents it a
// second time. AWS documents a configuration token as valid for exactly one
// call, and answers BadRequestException when a spent or expired one is used.
func (g *appConfigDataGroup) GetLatestConfigurationReusedToken(_ context.Context, t *harness.TestContext) error {
	started, err := awscli.RunOutput(t.Endpoint, t.Region, "appconfigdata", "start-configuration-session",
		"--application-identifier", t.GetString("application_id"),
		"--environment-identifier", t.GetString("environment_id"),
		"--configuration-profile-identifier", t.GetString("configuration_profile_id"),
	)
	if err != nil {
		return fmt.Errorf("GetLatestConfigurationReusedToken: starting a session failed: %w", err)
	}
	token, _ := started["InitialConfigurationToken"].(string)
	if token == "" {
		return fmt.Errorf("GetLatestConfigurationReusedToken: missing InitialConfigurationToken")
	}
	if _, _, err := g.poll(t, token); err != nil {
		return fmt.Errorf("GetLatestConfigurationReusedToken: the first poll failed: %w", err)
	}

	outPath, cleanup, err := writeTempFile("oc-appconfigdata-poll-*", "")
	if err != nil {
		return err
	}
	defer cleanup()
	return expectAWSFailure(t, "GetLatestConfigurationReusedToken",
		"BadRequestException", 400,
		"appconfigdata", "get-latest-configuration",
		"--configuration-token", token,
		outPath,
	)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// poll runs one GetLatestConfiguration and returns both halves of the response:
// the JSON the CLI prints (the modeled header members) and the configuration
// itself, which the CLI writes to the outfile argument rather than to stdout.
func (g *appConfigDataGroup) poll(t *harness.TestContext, token string) (map[string]any, string, error) {
	outPath, cleanup, err := writeTempFile("oc-appconfigdata-poll-*", "")
	if err != nil {
		return nil, "", err
	}
	defer cleanup()

	out, err := awscli.RunOutput(t.Endpoint, t.Region, "appconfigdata", "get-latest-configuration",
		"--configuration-token", token,
		outPath,
	)
	if err != nil {
		return nil, "", err
	}
	body, err := os.ReadFile(outPath)
	if err != nil {
		return nil, "", fmt.Errorf("GetLatestConfiguration: reading the configuration the CLI wrote: %w", err)
	}
	return out, string(body), nil
}

// expectAWSFailure asserts an unsigned command fails with the error code and
// HTTP status the model binds. The assertion itself is shared with the services
// that can only be reached signed — see assertAWSFailure in util.go.
func expectAWSFailure(t *harness.TestContext, testName, wantCode string, wantStatus int, args ...string) error {
	return assertAWSFailure(awscli.RunStatus, t, testName, wantCode, wantStatus, args...)
}

// describeRotation says how a NextPollConfigurationToken disappointed without
// quoting it. The harness classifies any error whose text contains "501" as an
// HTTP 501 — a token is opaque and could contain those three characters — and
// that would report a genuine failure as an unimplemented operation.
func describeRotation(next, previous string) string {
	if next == "" {
		return "an empty token"
	}
	if next == previous {
		return "the token that was just spent"
	}
	return "an unexpected token"
}

// writeTempFile creates a temp file holding content and returns its path with a
// cleanup func. Used for the CLI's blob parameters and its outfile arguments,
// neither of which accepts data on stdin.
func writeTempFile(pattern, content string) (string, func(), error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", nil, fmt.Errorf("creating a temp file: %w", err)
	}
	if content != "" {
		if _, err := f.WriteString(content); err != nil {
			f.Close()           //nolint:errcheck
			os.Remove(f.Name()) //nolint:errcheck
			return "", nil, fmt.Errorf("writing a temp file: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name()) //nolint:errcheck
		return "", nil, fmt.Errorf("closing a temp file: %w", err)
	}
	return f.Name(), func() { os.Remove(f.Name()) }, nil //nolint:errcheck
}
