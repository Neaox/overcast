// Routing tests for the shared /tags/{resourceArn} dispatch.
//
// GET /tags/{resourceArn} is bound by dozens of AWS services — Backup among
// them (ListTags) — and Overcast dispatches the path on the ARN's service
// prefix. API Gateway's ARN-keyed tag store used to be the fallback owner for
// every ARN no service claimed, which meant an operation Overcast does not
// implement read as success: `aws backup list-tags` got HTTP 200 {"tags":{}}
// from a service the caller never addressed (#976, the #963 failure mode).
// Backup itself was that example, until #1195 gave it real tag operations —
// TestTagsDispatch_backupARN_keepsRoundTripping below is its replacement in
// this file, alongside API Gateway's and AppRegistry's.
//
// An unclaimed ARN now falls back to the router's REST fallback, exactly as if
// no /tags route had matched: a signed caller reaches the generated 501, and
// unsigned traffic keeps S3's answer, which is the design the rest of the
// router follows (see tests/integration/msk/routing_test.go for why the SigV4
// scope is load-bearing). The "still unclaimed" tests below address a made-up
// service name rather than a real one, so they cannot go stale the way the
// Backup-addressed originals did the moment #1195 gave Backup real tag ops —
// dozens of real AWS services model this exact path shape and none of them
// are implemented here (docs/plans/resource-tagging-coverage.md's Axis A
// tracks the ones this emulator does carry).
//
// Run: go test ./tests/integration/router/...
package router_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// tagsSignedRequest sends a REST JSON request the way an SDK sends one: with a
// SigV4 credential scope naming the calling service's signing name.
func tagsSignedRequest(t *testing.T, srv *helpers.TestServer, method, path, signingName string, body any) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, srv.URL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20260813/us-east-1/"+signingName+"/aws4_request, SignedHeaders=host, Signature=x")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// unclaimedServiceARN names a service no /tags owner in this codebase claims
// and that Overcast does not implement at all, so this can never go stale the
// way addressing Backup here did the moment #1195 gave it real tag ops. Dozens
// of real AWS services (accessanalyzer, amp, amplify, aiops, among many
// others — see internal/awsapi/manifest.gen.go) model TagResource/ListTags at
// this exact path shape and are equally valid stand-ins; a made-up name is
// used instead so the test's intent — "a service nobody implements" — cannot
// be read as "a service Overcast merely hasn't implemented yet".
const unclaimedServiceARN = "arn:aws:unclaimed-tagging-service:us-east-1:000000000000:thing:v1"

// A signed caller addressing a service whose tag operations are not
// implemented must reach the protocol-correct 501 — the general form of the
// defect #976 found via Backup (see this file's header comment).
//
// Both ARN spellings matter: the AWS CLI percent-encodes the ARN whole into
// one path segment (botocore also appends a trailing slash for some services'
// modeled URIs, Backup's ListTags among them), while hand-written callers may
// leave the colons literal.
func TestTagsDispatch_unclaimedARNSigned_returnsNotImplemented(t *testing.T) {
	srv := helpers.NewTestServer(t)

	for name, path := range map[string]string{
		"escaped":                "/tags/" + url.PathEscape(unclaimedServiceARN),
		"escaped trailing slash": "/tags/" + url.PathEscape(unclaimedServiceARN) + "/",
		"literal":                "/tags/" + unclaimedServiceARN,
	} {
		t.Run("ListTags/"+name, func(t *testing.T) {
			resp := tagsSignedRequest(t, srv, http.MethodGet, path, "unclaimed-tagging-service", nil)
			defer resp.Body.Close()

			helpers.AssertStatus(t, resp, http.StatusNotImplemented)
			helpers.AssertHeader(t, resp, "x-emulator-unsupported", "true")
			helpers.AssertRequestID(t, resp)
			helpers.AssertJSONError(t, resp, "NotImplemented")
		})
	}

	// The write verb must not fall through to another service's tag store
	// either.
	t.Run("TagResource", func(t *testing.T) {
		resp := tagsSignedRequest(t, srv, http.MethodPost, "/tags/"+url.PathEscape(unclaimedServiceARN), "unclaimed-tagging-service",
			map[string]any{"Tags": map[string]string{"team": "platform"}})
		defer resp.Body.Close()

		helpers.AssertStatus(t, resp, http.StatusNotImplemented)
		helpers.AssertHeader(t, resp, "x-emulator-unsupported", "true")
		helpers.AssertJSONError(t, resp, "NotImplemented")
	})
}

// The same call unsigned must not fake success either. Unsigned traffic on an
// unclaimed path is S3's by design — addressesNonS3 treats a request that
// names no service as S3's — so the honest answer here is S3's 404, never a
// 200 with an empty tag map from a service nobody addressed.
func TestTagsDispatch_unclaimedARNUnsigned_doesNotFakeSuccess(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp, err := http.Get(srv.URL + "/tags/" + url.PathEscape(unclaimedServiceARN))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusNotFound)
	if body := helpers.ReadBody(t, resp); strings.Contains(body, `"tags"`) {
		t.Errorf("unsigned unclaimed ARN answered a tag body: %s", body)
	}
}

// Backup is no longer an example of the #976 defect — #1195 gave it real
// TagResource/ListTags — so its ARN must now round-trip through this shared
// dispatch exactly as API Gateway's and AppRegistry's already do below.
// UntagResource is deliberately not exercised here: it lives at Backup's own
// /untag/{ResourceArn}, not this path (tests/integration/backup/tags_test.go
// covers it, and TestBackupUntagResource_isNotServedUnderTags there pins that
// /tags itself never answers it).
func TestTagsDispatch_backupARN_keepsRoundTripping(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const vaultARN = "arn:aws:backup:us-east-1:000000000000:backup-vault:v1"

	createResp := tagsSignedRequest(t, srv, http.MethodPut, "/backup-vaults/v1", "backup", map[string]any{})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)

	tag := tagsSignedRequest(t, srv, http.MethodPost, "/tags/"+url.PathEscape(vaultARN), "backup",
		map[string]any{"Tags": map[string]string{"env": "prod"}})
	defer tag.Body.Close()
	helpers.AssertStatus(t, tag, http.StatusOK)

	get := tagsSignedRequest(t, srv, http.MethodGet, "/tags/"+url.PathEscape(vaultARN), "backup", nil)
	defer get.Body.Close()
	helpers.AssertStatus(t, get, http.StatusOK)
	var result struct {
		Tags map[string]string `json:"Tags"`
	}
	helpers.DecodeJSON(t, get, &result)
	if result.Tags["env"] != "prod" {
		t.Errorf("expected tag env=prod, got %v", result.Tags)
	}
}

// Narrowing the fallback must not cost the services that legitimately answer
// at /tags/{resourceArn}. API Gateway's own ARNs stay with its tag store.
func TestTagsDispatch_apigatewayARN_keepsRoundTripping(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := "arn:aws:apigateway:us-east-1::/restapis/abc123"

	put := tagsSignedRequest(t, srv, http.MethodPut, "/tags/"+arn, "apigateway",
		map[string]any{"tags": map[string]string{"env": "test"}})
	defer put.Body.Close()
	helpers.AssertStatus(t, put, http.StatusNoContent)

	get := tagsSignedRequest(t, srv, http.MethodGet, "/tags/"+arn, "apigateway", nil)
	defer get.Body.Close()
	helpers.AssertStatus(t, get, http.StatusOK)
	var result struct {
		Tags map[string]string `json:"tags"`
	}
	helpers.DecodeJSON(t, get, &result)
	if result.Tags["env"] != "test" {
		t.Errorf("expected tag env=test, got %v", result.Tags)
	}
}

// AppRegistry has no tag routes of its own: its SDK shares API Gateway's
// endpoint and the ARN-keyed store answers its "servicecatalog" ARNs (POST is
// its TagResource verb). That worked only through the blanket fallback before;
// it must keep working now that the store is keyed by ARN service.
func TestTagsDispatch_appregistryARN_keepsRoundTripping(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := "arn:aws:servicecatalog:us-east-1:000000000000:/applications/abc"

	post := tagsSignedRequest(t, srv, http.MethodPost, "/tags/"+arn, "servicecatalog",
		map[string]any{"tags": map[string]string{"team": "platform"}})
	defer post.Body.Close()
	helpers.AssertStatus(t, post, http.StatusNoContent)

	get := tagsSignedRequest(t, srv, http.MethodGet, "/tags/"+arn, "servicecatalog", nil)
	defer get.Body.Close()
	helpers.AssertStatus(t, get, http.StatusOK)
	var result struct {
		Tags map[string]string `json:"tags"`
	}
	helpers.DecodeJSON(t, get, &result)
	if result.Tags["team"] != "platform" {
		t.Errorf("expected tag team=platform, got %v", result.Tags)
	}
}
