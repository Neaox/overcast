package lambda_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// codeVersions uploads two different bodies to the same key of a versioned
// bucket and returns their version ids, oldest first.
func codeVersions(t *testing.T, srv *helpers.TestServer, bucket, key, older, newer string) (string, string) {
	t.Helper()
	bucketResp := doJSON(t, http.MethodPut, srv.URL+"/"+bucket, nil)
	bucketResp.Body.Close()
	if bucketResp.StatusCode != http.StatusOK && bucketResp.StatusCode != http.StatusConflict {
		t.Fatalf("create bucket %s: status %d", bucket, bucketResp.StatusCode)
	}
	versioning := doPutRaw(t, srv, "/"+bucket+"?versioning",
		"<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>")
	versioning.Body.Close()
	helpers.AssertStatus(t, versioning, http.StatusOK)

	return putCodeVersion(t, srv, bucket, key, older), putCodeVersion(t, srv, bucket, key, newer)
}

func putCodeVersion(t *testing.T, srv *helpers.TestServer, bucket, key, body string) string {
	t.Helper()
	resp := doPutRaw(t, srv, "/"+bucket+"/"+key, body)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	version := resp.Header.Get("x-amz-version-id")
	if version == "" || version == "null" {
		t.Fatalf("PutObject %s/%s returned no version id (header %q)", bucket, key, version)
	}
	return version
}

func doPutRaw(t *testing.T, srv *helpers.TestServer, path, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build PUT %s: %v", path, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", path, err)
	}
	return resp
}

// inlineCodeSha256 is the CodeSha256 a function gets from an inline package
// with exactly these bytes — the reference an S3-sourced function's hash is
// compared against.
func inlineCodeSha256(t *testing.T, srv *helpers.TestServer, name, body string) string {
	t.Helper()
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: name,
		Runtime:      "nodejs20.x",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		Code:         &lambdaCode{ZipFile: []byte(body)},
	})
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var cfg codeSourceConfiguration
	decodeJSON(t, resp, &cfg)
	return cfg.CodeSha256
}

// CDK emits Code.S3ObjectVersion whenever the asset bucket has versioning
// enabled, so the deployment package a create must fetch is a *specific*
// version — not whatever happens to be current when Lambda reads the key.
func TestCreateFunction_S3ObjectVersionSelectsThatVersion(t *testing.T) {
	// Given: two deployment packages at the same key.
	srv := helpers.NewTestServer(t)
	oldVersion, _ := codeVersions(t, srv, "code-versions", "function.zip", "older-code", "newer-code")

	// When: CreateFunction names the older version.
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), map[string]any{
		"FunctionName": "pinned-old-version",
		"Runtime":      "nodejs20.x",
		"Handler":      "index.handler",
		"Role":         "arn:aws:iam::000000000000:role/lambda-role",
		"Code": map[string]any{
			"S3Bucket": "code-versions", "S3Key": "function.zip", "S3ObjectVersion": oldVersion,
		},
	})
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var created codeSourceConfiguration
	decodeJSON(t, resp, &created)

	// Then: the function carries the older bytes, not the current ones.
	wantOld := inlineCodeSha256(t, srv, "reference-older", "older-code")
	gotNew := inlineCodeSha256(t, srv, "reference-newer", "newer-code")
	if created.CodeSha256 == gotNew {
		t.Fatalf("S3ObjectVersion %q was ignored: the function holds the current version's bytes", oldVersion)
	}
	if created.CodeSha256 != wantOld {
		t.Fatalf("CodeSha256 = %q, want the older version's %q", created.CodeSha256, wantOld)
	}
}

// The contract for an unfetchable S3Bucket/S3Key applies unchanged to an
// unfetchable version: the create fails and persists nothing.
func TestCreateFunction_UnknownS3ObjectVersionCreatesNothing(t *testing.T) {
	tests := []struct {
		name    string
		version func(deleteMarker, absent string) string
	}{
		{name: "absent version", version: func(_, absent string) string { return absent }},
		{name: "delete marker", version: func(deleteMarker, _ string) string { return deleteMarker }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a versioned key whose newest version is a delete marker.
			srv := helpers.NewTestServer(t)
			_, _ = codeVersions(t, srv, "code-bad-version", "function.zip", "older-code", "newer-code")
			del := doJSON(t, http.MethodDelete, srv.URL+"/code-bad-version/function.zip", nil)
			del.Body.Close()
			helpers.AssertStatus(t, del, http.StatusNoContent)
			deleteMarker := del.Header.Get("x-amz-version-id")
			if deleteMarker == "" {
				t.Fatalf("DeleteObject on a versioned bucket returned no delete-marker version id")
			}

			// When: CreateFunction names a version it cannot read.
			resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), map[string]any{
				"FunctionName": "bad-version-fn",
				"Runtime":      "nodejs20.x",
				"Handler":      "index.handler",
				"Role":         "arn:aws:iam::000000000000:role/lambda-role",
				"Code": map[string]any{
					"S3Bucket": "code-bad-version", "S3Key": "function.zip",
					"S3ObjectVersion": tc.version(deleteMarker, "absent-version-id"),
				},
			})
			defer resp.Body.Close()

			// Then: the same GetObject translation as any other fetch failure…
			helpers.AssertStatus(t, resp, http.StatusBadRequest)
			helpers.AssertRequestID(t, resp)
			helpers.AssertJSONError(t, resp, "InvalidParameterValueException")

			// …and nothing was persisted.
			get := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/bad-version-fn"), nil)
			defer get.Body.Close()
			helpers.AssertStatus(t, get, http.StatusNotFound)
		})
	}
}

// UpdateFunctionCode takes the same member and must discriminate the same way.
func TestUpdateFunctionCode_S3ObjectVersionSelectsThatVersion(t *testing.T) {
	// Given: a function currently running the newest package.
	srv := helpers.NewTestServer(t)
	oldVersion, _ := codeVersions(t, srv, "update-code-versions", "function.zip", "older-code", "newer-code")
	createS3CodeFunction(t, srv, "rollback-fn", "update-code-versions", "function.zip")

	// When: UpdateFunctionCode rolls it back to the older version.
	resp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/rollback-fn/code"), map[string]any{
		"S3Bucket": "update-code-versions", "S3Key": "function.zip", "S3ObjectVersion": oldVersion,
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var updated codeSourceConfiguration
	decodeJSON(t, resp, &updated)

	// Then: the function holds the older bytes.
	wantOld := inlineCodeSha256(t, srv, "update-reference-older", "older-code")
	if updated.CodeSha256 != wantOld {
		t.Fatalf("CodeSha256 = %q after rollback, want the older version's %q", updated.CodeSha256, wantOld)
	}
}

// A failed version fetch must leave the function exactly as it was — the same
// guarantee UpdateFunctionCode already gives for an unreadable bucket or key.
func TestUpdateFunctionCode_UnknownS3ObjectVersionLeavesFunctionUnchanged(t *testing.T) {
	// Given: a function created from a versioned package.
	srv := helpers.NewTestServer(t)
	_, _ = codeVersions(t, srv, "update-bad-version", "function.zip", "older-code", "newer-code")
	before := createS3CodeFunction(t, srv, "unchanged-fn", "update-bad-version", "function.zip")

	// When: UpdateFunctionCode names a version that does not exist.
	resp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/unchanged-fn/code"), map[string]any{
		"S3Bucket": "update-bad-version", "S3Key": "function.zip", "S3ObjectVersion": "absent-version-id",
	})
	defer resp.Body.Close()

	// Then: the update is rejected…
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidParameterValueException")

	// …and neither the package nor the revision moved.
	after := getCodeSourceConfiguration(t, srv, "unchanged-fn")
	if after.CodeSha256 != before.CodeSha256 || after.RevisionID != before.RevisionID {
		t.Fatalf("rejected update mutated the function: %+v, want %+v", after, before)
	}
}
