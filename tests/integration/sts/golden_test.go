// golden_test.go contains wire-byte golden tests for STS — see
// docs/plans/wire-byte-goldens.md for the harness design.
//
// STS is Query/XML only: internal/services/sts/typed_ops.go's
// SupportedProtocols returns only codec.QueryXML, and every operation is
// already served by the typed dispatcher (see typed_ops.go / typed_logic.go)
// — there is no separate legacy handler to prove byte-identical against a
// future delegation. These goldens instead pin the exact wire contract for
// every STS response shape so a future refactor of the typed handlers (or of
// the QueryXML codec itself) can be verified byte-identical with zero
// test-code changes.
//
// Determinism: STS mints real-looking-but-fake credentials
// (AccessKeyId/SecretAccessKey/SessionToken, and AssumeRole's role-session
// ID) via crypto/rand on every single call — including every time this
// golden test itself re-invokes the operation, not just between record and
// assert runs. Byte-for-byte comparison is only possible because
// normalizeSTSCredentials redacts those fields to fixed placeholders before
// the harness compares bytes; Expiration is left alone because it is
// clock-derived and deterministic under helpers.WithMockClock(). Separately,
// STS is one of the few protocols that embeds the request ID inside the
// response body (Query/XML's ResponseMetadata/RequestId element, not just a
// response header) — middleware.RequestID honours a caller-supplied
// x-amzn-requestid header specifically so tests can pin it (see
// tests/integration/lambda/request_id_test.go for the same technique), so
// stsGoldenCall sets one instead of adding another regex.
//
// Coverage — all five registered STS operations:
//
//	GetCallerIdentity, GetSessionToken, GetFederationToken, AssumeRole,
//	AssumeRoleWithWebIdentity
//
// Plus one error shape: AssumeRole with a missing RoleSessionName
// (MissingParameter), to pin the Query/XML error envelope
// (ErrorResponse/Error/{Type,Code,Message} + RequestId) as well as the
// success envelope.
//
// No STS operations are excluded — GetCallerIdentity needs no normalization
// at all (Account/UserId/Arn are all deterministic; UserId is even a
// hardcoded literal), the other four need normalizeSTSCredentials.
//
// Record: go test ./tests/integration/sts/ -run TestGolden -record
// Assert: go test ./tests/integration/sts/ -run TestGolden
package sts_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

const stsGoldenDir = "goldens"

// goldenRequestID is pinned via the x-amzn-requestid request header so the
// ResponseMetadata/RequestId element embedded in every STS response body is
// reproducible across record and assert runs (and across repeated assert
// runs) — see middleware.RequestID's doc comment.
const goldenRequestID = "00000000-0000-0000-0000-000000000000"

// stsGoldenCall is stsCall (see sts_test.go) plus a pinned x-amzn-requestid
// header. stsCall itself has no way to set request headers, and golden
// determinism needs one, so this is a small, deliberate duplication rather
// than a change to the shared non-golden helper.
func stsGoldenCall(t *testing.T, srv *helpers.TestServer, action string, params url.Values) *http.Response {
	t.Helper()
	if params == nil {
		params = url.Values{}
	}
	params.Set("Action", action)
	params.Set("Version", "2011-06-15")
	body := strings.NewReader(params.Encode())
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", body)
	if err != nil {
		t.Fatalf("stsGoldenCall: new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("x-amzn-requestid", goldenRequestID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("stsGoldenCall: do: %v", err)
	}
	return resp
}

// Redaction patterns for the crypto/rand-minted fields in
// internal/services/sts/typed_logic.go's typedCredentials and
// assumeRoleTyped/assumeRoleWithWebIdentityTyped. AccessKeyId is "ASIA" + 16
// chars from [A-Z0-9] (handler.go's randID); SecretAccessKey/SessionToken are
// standard-base64 of 30/48 random bytes respectively (handler.go's
// randBase64); AssumedRoleId is "AROA" + 16 chars from [A-Z0-9], a colon,
// then the caller-supplied (non-random) RoleSessionName — only the AROA
// prefix up to the colon is redacted so the session name stays visible in
// the fixture.
var (
	accessKeyIDRE     = regexp.MustCompile(`<AccessKeyId>ASIA[A-Z0-9]+</AccessKeyId>`)
	secretAccessKeyRE = regexp.MustCompile(`<SecretAccessKey>[A-Za-z0-9+/=]+</SecretAccessKey>`)
	sessionTokenRE    = regexp.MustCompile(`<SessionToken>[A-Za-z0-9+/=]+</SessionToken>`)
	assumedRoleIDRE   = regexp.MustCompile(`<AssumedRoleId>AROA[A-Z0-9]+:`)
)

// normalizeSTSCredentials redacts every crypto/rand-minted field in an STS
// response body to a fixed placeholder so repeated calls — including the
// call the assert-mode test itself makes — compare byte-for-byte identical.
func normalizeSTSCredentials(body []byte) []byte {
	body = accessKeyIDRE.ReplaceAll(body, []byte("<AccessKeyId>ASIAREDACTEDREDACTED</AccessKeyId>"))
	body = secretAccessKeyRE.ReplaceAll(body, []byte("<SecretAccessKey>REDACTEDREDACTEDREDACTEDREDACTEDREDACTED</SecretAccessKey>"))
	body = sessionTokenRE.ReplaceAll(body, []byte("<SessionToken>REDACTEDREDACTEDREDACTEDREDACTEDREDACTEDREDACTEDREDACTEDREDACTED</SessionToken>"))
	body = assumedRoleIDRE.ReplaceAll(body, []byte("<AssumedRoleId>AROAREDACTEDREDACT:"))
	return body
}

func TestGolden_GetCallerIdentity(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())

	resp := stsGoldenCall(t, srv, "GetCallerIdentity", nil)
	helpers.GoldenTest(t, stsGoldenDir, "GetCallerIdentity", resp, nil)
}

func TestGolden_GetSessionToken(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())

	resp := stsGoldenCall(t, srv, "GetSessionToken", url.Values{
		"DurationSeconds": {"3600"},
	})
	helpers.GoldenTest(t, stsGoldenDir, "GetSessionToken", resp, normalizeSTSCredentials)
}

func TestGolden_GetFederationToken(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())

	resp := stsGoldenCall(t, srv, "GetFederationToken", url.Values{
		"Name":            {"golden-federated-user"},
		"DurationSeconds": {"3600"},
	})
	helpers.GoldenTest(t, stsGoldenDir, "GetFederationToken", resp, normalizeSTSCredentials)
}

func TestGolden_AssumeRole(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())

	resp := stsGoldenCall(t, srv, "AssumeRole", url.Values{
		"RoleArn":         {"arn:aws:iam::000000000000:role/GoldenRole"},
		"RoleSessionName": {"golden-session"},
		"DurationSeconds": {"3600"},
	})
	helpers.GoldenTest(t, stsGoldenDir, "AssumeRole", resp, normalizeSTSCredentials)
}

func TestGolden_AssumeRoleWithWebIdentity(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())

	resp := stsGoldenCall(t, srv, "AssumeRoleWithWebIdentity", url.Values{
		"RoleArn":          {"arn:aws:iam::000000000000:role/GoldenWebRole"},
		"RoleSessionName":  {"golden-web-session"},
		"WebIdentityToken": {"golden.jwt.token"},
		"DurationSeconds":  {"3600"},
	})
	helpers.GoldenTest(t, stsGoldenDir, "AssumeRoleWithWebIdentity", resp, normalizeSTSCredentials)
}

// TestGolden_AssumeRole_MissingRoleSessionName pins the Query/XML error
// envelope (ErrorResponse/Error/{Type,Code,Message} + RequestId) — see
// assumeRoleTyped's validation in internal/services/sts/typed_logic.go.
func TestGolden_AssumeRole_MissingRoleSessionName(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())

	resp := stsGoldenCall(t, srv, "AssumeRole", url.Values{
		"RoleArn": {"arn:aws:iam::000000000000:role/GoldenRole"},
	})
	helpers.GoldenTest(t, stsGoldenDir, "AssumeRole_MissingRoleSessionName", resp, nil)
}
