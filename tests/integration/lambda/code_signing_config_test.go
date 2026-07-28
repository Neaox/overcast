package lambda_test

// code_signing_config_test.go — the code signing configuration resource.
//
// The configuration is a standalone resource on the 2020-04-22 API version,
// referenced by functions through their CodeSigningConfigArn. Overcast stores
// it faithfully but never validates a signature: signing is a security
// control and Overcast is not a security boundary. What has to be right is
// that a configuration reads back exactly as written, and that a function
// referencing one that does not exist is rejected the way AWS rejects it —
// deploy tooling depends on both.

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

type cscEnvelope struct {
	CodeSigningConfig struct {
		CodeSigningConfigID  string `json:"CodeSigningConfigId"`
		CodeSigningConfigArn string `json:"CodeSigningConfigArn"`
		Description          string `json:"Description"`
		AllowedPublishers    struct {
			SigningProfileVersionArns []string `json:"SigningProfileVersionArns"`
		} `json:"AllowedPublishers"`
		CodeSigningPolicies struct {
			UntrustedArtifactOnDeployment string `json:"UntrustedArtifactOnDeployment"`
		} `json:"CodeSigningPolicies"`
		LastModified string `json:"LastModified"`
	} `json:"CodeSigningConfig"`
}

const testSigningProfileArn = "arn:aws:signer:us-east-1:000000000000:/signing-profiles/TestProfile/abcdef1234"

var cscIDPattern = regexp.MustCompile(`^csc-[a-z0-9]{17}$`)

func cscURL(srv *helpers.TestServer, suffix string) string {
	return srv.URL + "/2020-04-22/code-signing-configs" + suffix
}

func createCSC(t *testing.T, srv *helpers.TestServer) cscEnvelope {
	t.Helper()
	resp := doJSON(t, http.MethodPost, cscURL(srv, "/"), map[string]any{
		"Description":       "test profile",
		"AllowedPublishers": map[string]any{"SigningProfileVersionArns": []string{testSigningProfileArn}},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var env cscEnvelope
	decodeJSON(t, resp, &env)
	return env
}

func TestCreateCodeSigningConfig_shapeAndDefaults(t *testing.T) {
	// Given a fresh server
	srv := helpers.NewTestServer(t)

	// When a configuration is created without policies
	csc := createCSC(t, srv).CodeSigningConfig

	// Then the id and ARN carry AWS's shape
	if !cscIDPattern.MatchString(csc.CodeSigningConfigID) {
		t.Errorf("CodeSigningConfigId = %q, want AWS's csc- shape", csc.CodeSigningConfigID)
	}
	wantArn := "arn:aws:lambda:us-east-1:000000000000:code-signing-config:" + csc.CodeSigningConfigID
	if csc.CodeSigningConfigArn != wantArn {
		t.Errorf("CodeSigningConfigArn = %q, want %q", csc.CodeSigningConfigArn, wantArn)
	}
	// And the policy falls back to AWS's documented default
	if csc.CodeSigningPolicies.UntrustedArtifactOnDeployment != "Warn" {
		t.Errorf("UntrustedArtifactOnDeployment = %q, want the documented default Warn",
			csc.CodeSigningPolicies.UntrustedArtifactOnDeployment)
	}
	if csc.LastModified == "" {
		t.Error("LastModified is empty; AWS marks it required")
	}
}

func TestCreateCodeSigningConfig_requiresAllowedPublishers(t *testing.T) {
	srv := helpers.NewTestServer(t)

	// AllowedPublishers is the one required member
	for _, body := range []map[string]any{
		{"Description": "no publishers at all"},
		{"AllowedPublishers": map[string]any{"SigningProfileVersionArns": []string{}}},
	} {
		resp := doJSON(t, http.MethodPost, cscURL(srv, "/"), body)
		helpers.AssertStatus(t, resp, http.StatusBadRequest)
		resp.Body.Close()
	}
}

func TestCreateCodeSigningConfig_rejectsUnknownPolicy(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := doJSON(t, http.MethodPost, cscURL(srv, "/"), map[string]any{
		"AllowedPublishers":   map[string]any{"SigningProfileVersionArns": []string{testSigningProfileArn}},
		"CodeSigningPolicies": map[string]any{"UntrustedArtifactOnDeployment": "Nope"},
	})
	defer resp.Body.Close()

	// AWS models this as an enum of Warn | Enforce
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestCodeSigningConfig_getUpdateListDelete(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := createCSC(t, srv).CodeSigningConfig.CodeSigningConfigArn

	// Get returns what was stored
	get := doJSON(t, http.MethodGet, cscURL(srv, "/"+arn), nil)
	helpers.AssertStatus(t, get, http.StatusOK)
	var got cscEnvelope
	decodeJSON(t, get, &got)
	get.Body.Close()
	if got.CodeSigningConfig.Description != "test profile" {
		t.Errorf("Description = %q, want %q", got.CodeSigningConfig.Description, "test profile")
	}

	// Update is a partial update: omitted members keep their stored value
	upd := doJSON(t, http.MethodPut, cscURL(srv, "/"+arn), map[string]any{
		"CodeSigningPolicies": map[string]any{"UntrustedArtifactOnDeployment": "Enforce"},
	})
	helpers.AssertStatus(t, upd, http.StatusOK)
	var updated cscEnvelope
	decodeJSON(t, upd, &updated)
	upd.Body.Close()
	if updated.CodeSigningConfig.CodeSigningPolicies.UntrustedArtifactOnDeployment != "Enforce" {
		t.Error("UntrustedArtifactOnDeployment was not updated to Enforce")
	}
	if updated.CodeSigningConfig.Description != "test profile" {
		t.Errorf("Description was clobbered by a partial update: %q", updated.CodeSigningConfig.Description)
	}
	if len(updated.CodeSigningConfig.AllowedPublishers.SigningProfileVersionArns) != 1 {
		t.Error("AllowedPublishers was clobbered by a partial update")
	}

	// List returns it
	list := doJSON(t, http.MethodGet, cscURL(srv, "/"), nil)
	helpers.AssertStatus(t, list, http.StatusOK)
	var listed struct {
		CodeSigningConfigs []struct {
			CodeSigningConfigArn string `json:"CodeSigningConfigArn"`
		} `json:"CodeSigningConfigs"`
	}
	decodeJSON(t, list, &listed)
	list.Body.Close()
	if len(listed.CodeSigningConfigs) != 1 || listed.CodeSigningConfigs[0].CodeSigningConfigArn != arn {
		t.Errorf("ListCodeSigningConfigs = %+v, want exactly the one created", listed.CodeSigningConfigs)
	}

	// Delete answers 204 with no body, and the resource is then gone
	del := doJSON(t, http.MethodDelete, cscURL(srv, "/"+arn), nil)
	helpers.AssertStatus(t, del, http.StatusNoContent)
	del.Body.Close()

	after := doJSON(t, http.MethodGet, cscURL(srv, "/"+arn), nil)
	defer after.Body.Close()
	helpers.AssertStatus(t, after, http.StatusNotFound)
}

// TestCodeSigningConfig_percentEncodedArnInPath pins the form the AWS SDKs
// actually send. The ARN is a path parameter, so the SDKs percent-encode its
// colons (arn%3Aaws%3A…); a handler that only splits on a literal ':' takes
// the whole encoded string for the id and finds nothing. Building the URL from
// a raw ARN, as the other tests do, does not exercise this.
func TestCodeSigningConfig_percentEncodedArnInPath(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := createCSC(t, srv).CodeSigningConfig.CodeSigningConfigArn

	resp := doJSON(t, http.MethodGet, cscURL(srv, "/"+url.PathEscape(arn)), nil)
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var got cscEnvelope
	decodeJSON(t, resp, &got)
	if got.CodeSigningConfig.CodeSigningConfigArn != arn {
		t.Errorf("CodeSigningConfigArn = %q, want %q", got.CodeSigningConfig.CodeSigningConfigArn, arn)
	}
}

func TestCodeSigningConfig_unknownArnIsNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	missing := "arn:aws:lambda:us-east-1:000000000000:code-signing-config:csc-ffffffffffffffff0"

	for _, tc := range []struct {
		method string
		body   any
	}{
		{http.MethodGet, nil},
		{http.MethodPut, map[string]any{"Description": "x"}},
		{http.MethodDelete, nil},
	} {
		resp := doJSON(t, tc.method, cscURL(srv, "/"+missing), tc.body)
		helpers.AssertStatus(t, resp, http.StatusNotFound)
		resp.Body.Close()
	}
}

// TestCreateFunction_unknownCodeSigningConfigIsRejected covers the behaviour
// that only becomes possible once configurations are real resources: a
// function may reference one only if it exists. AWS declares
// CodeSigningConfigNotFoundException on CreateFunction for exactly this.
func TestCreateFunction_unknownCodeSigningConfigIsRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), map[string]any{
		"FunctionName":         "csc-missing-fn",
		"Runtime":              "nodejs20.x",
		"Handler":              "index.handler",
		"Role":                 "arn:aws:iam::000000000000:role/lambda-role",
		"Code":                 map[string]any{},
		"CodeSigningConfigArn": "arn:aws:lambda:us-east-1:000000000000:code-signing-config:csc-ffffffffffffffff0",
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusNotFound)
	var errBody map[string]string
	decodeJSON(t, resp, &errBody)
	if errBody["__type"] != "CodeSigningConfigNotFoundException" {
		t.Errorf("__type = %q, want CodeSigningConfigNotFoundException", errBody["__type"])
	}
}

func TestListFunctionsByCodeSigningConfig_andDeleteConflict(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := createCSC(t, srv).CodeSigningConfig.CodeSigningConfigArn

	// Given a function referencing the configuration
	create := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), map[string]any{
		"FunctionName":         "csc-user-fn",
		"Runtime":              "nodejs20.x",
		"Handler":              "index.handler",
		"Role":                 "arn:aws:iam::000000000000:role/lambda-role",
		"Code":                 map[string]any{},
		"CodeSigningConfigArn": arn,
	})
	helpers.AssertStatus(t, create, http.StatusCreated)
	create.Body.Close()

	// Then it is listed against that configuration
	list := doJSON(t, http.MethodGet, cscURL(srv, "/"+arn+"/functions"), nil)
	helpers.AssertStatus(t, list, http.StatusOK)
	var byCSC struct {
		FunctionArns []string `json:"FunctionArns"`
	}
	decodeJSON(t, list, &byCSC)
	list.Body.Close()
	if len(byCSC.FunctionArns) != 1 || !strings.HasSuffix(byCSC.FunctionArns[0], ":function:csc-user-fn") {
		t.Errorf("FunctionArns = %v, want the one referencing function", byCSC.FunctionArns)
	}

	// And the configuration cannot be deleted while it is in use
	del := doJSON(t, http.MethodDelete, cscURL(srv, "/"+arn), nil)
	helpers.AssertStatus(t, del, http.StatusConflict)
	del.Body.Close()

	// Once the association is removed, deletion succeeds
	rm := doJSON(t, http.MethodDelete, codeSigningConfigURL(srv, "csc-user-fn"), nil)
	helpers.AssertStatus(t, rm, http.StatusNoContent)
	rm.Body.Close()

	del2 := doJSON(t, http.MethodDelete, cscURL(srv, "/"+arn), nil)
	defer del2.Body.Close()
	helpers.AssertStatus(t, del2, http.StatusNoContent)
}
