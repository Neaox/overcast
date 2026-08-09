package cloudformation_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// CDK emits Code.S3ObjectVersion for every asset once the bootstrap bucket has
// versioning enabled, so a stack that names one is the ordinary case, not an
// exotic template. The provisioner forwards it and Lambda must fetch that
// version's bytes.
func TestCreateStack_LambdaS3ObjectVersionSelectsThatVersion(t *testing.T) {
	// Given: two deployment packages at the same asset key.
	srv := helpers.NewTestServer(t)
	oldVersion, newVersion := s3PutTwoVersions(t, srv, "cfn-versioned-code", "function.zip", "older-code", "newer-code")
	if oldVersion == newVersion {
		t.Fatalf("bucket is not versioned: both puts reported %q", oldVersion)
	}
	template := fmt.Sprintf(`{"Resources":{"Function":{"Type":"AWS::Lambda::Function","Properties":{"FunctionName":"cfn-versioned-code-fn","Runtime":"python3.11","Handler":"index.handler","Role":"arn:aws:iam::000000000000:role/lambda-role","Code":{"S3Bucket":"cfn-versioned-code","S3Key":"function.zip","S3ObjectVersion":%q}}}}}`, oldVersion)

	// When: the stack names the older version.
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"lambda-versioned-code-stack"},
		"TemplateBody": {template},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "lambda-versioned-code-stack", "CREATE_COMPLETE")

	// Then: the function holds the older version's bytes, not the current ones.
	if got, want := lambdaCodeSha256(t, srv, "cfn-versioned-code-fn"), inlineCodeSha256ViaLambda(t, srv, "cfn-version-reference-old", "older-code"); got != want {
		t.Fatalf("CodeSha256 = %q, want the pinned version's %q", got, want)
	}
}

// A version the template names but S3 cannot serve is an ordinary resource
// failure — the same shape as an absent key — not a silent CREATE_COMPLETE over
// a function with the wrong code.
func TestCreateStack_LambdaUnknownS3ObjectVersionFailsStack(t *testing.T) {
	// Given: a versioned asset bucket and a template naming a version that is
	// not in it.
	srv := helpers.NewTestServer(t)
	s3PutTwoVersions(t, srv, "cfn-bad-version", "function.zip", "older-code", "newer-code")
	template := `{"Resources":{"Function":{"Type":"AWS::Lambda::Function","Properties":{"FunctionName":"cfn-bad-version-fn","Runtime":"python3.11","Handler":"index.handler","Role":"arn:aws:iam::000000000000:role/lambda-role","Code":{"S3Bucket":"cfn-bad-version","S3Key":"function.zip","S3ObjectVersion":"absent-version-id"}}}}}`

	// When: the stack is created.
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":       {"lambda-bad-version-stack"},
		"TemplateBody":    {template},
		"DisableRollback": {"true"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "lambda-bad-version-stack", "CREATE_FAILED")

	// Then: the Lambda failure reaches the stack events…
	events := cfnQuery(t, srv, "DescribeStackEvents", url.Values{
		"StackName": {"lambda-bad-version-stack"},
	})
	defer events.Body.Close()
	helpers.AssertStatus(t, events, http.StatusOK)
	if body := string(readBody(t, events)); !strings.Contains(body, "CreateFunction") {
		t.Fatalf("stack events do not mention the failing Lambda call: %s", body)
	}

	// …and no partially configured function remains behind.
	configuration := lambdaRequest(t, srv, http.MethodGet,
		"/2015-03-31/functions/cfn-bad-version-fn/configuration", nil)
	defer configuration.Body.Close()
	helpers.AssertStatus(t, configuration, http.StatusNotFound)
}

// s3PutTwoVersions creates a versioned bucket and writes two bodies to the same
// key, returning their version ids oldest first.
func s3PutTwoVersions(t *testing.T, srv *helpers.TestServer, bucket, key, older, newer string) (string, string) {
	t.Helper()
	s3PutObject(t, srv, bucket, key+".bootstrap", "bootstrap")

	versioning, err := http.NewRequest(http.MethodPut, srv.URL+"/"+bucket+"?versioning",
		strings.NewReader("<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>"))
	if err != nil {
		t.Fatalf("build PutBucketVersioning: %v", err)
	}
	versioningResp, err := http.DefaultClient.Do(versioning)
	if err != nil {
		t.Fatalf("PutBucketVersioning %s: %v", bucket, err)
	}
	versioningResp.Body.Close()
	helpers.AssertStatus(t, versioningResp, http.StatusOK)

	return s3PutVersion(t, srv, bucket, key, older), s3PutVersion(t, srv, bucket, key, newer)
}

func s3PutVersion(t *testing.T, srv *helpers.TestServer, bucket, key, body string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, srv.URL+"/"+bucket+"/"+key, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build PutObject: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PutObject %s/%s: %v", bucket, key, err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	version := resp.Header.Get("x-amz-version-id")
	if version == "" || version == "null" {
		t.Fatalf("PutObject %s/%s returned no version id (header %q)", bucket, key, version)
	}
	return version
}

func lambdaCodeSha256(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	resp := lambdaRequest(t, srv, http.MethodGet, "/2015-03-31/functions/"+name+"/configuration", nil)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var config struct {
		CodeSha256 string `json:"CodeSha256"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		t.Fatalf("decode GetFunctionConfiguration: %v", err)
	}
	return config.CodeSha256
}

// inlineCodeSha256ViaLambda is the CodeSha256 Lambda derives from exactly these
// bytes, used as the reference an S3-sourced function is compared against.
func inlineCodeSha256ViaLambda(t *testing.T, srv *helpers.TestServer, name, body string) string {
	t.Helper()
	resp := lambdaRequest(t, srv, http.MethodPost, "/2015-03-31/functions", map[string]any{
		"FunctionName": name, "Runtime": "python3.11", "Handler": "index.handler",
		"Role": "arn:aws:iam::000000000000:role/lambda-role",
		"Code": map[string]any{"ZipFile": []byte(body)},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var config struct {
		CodeSha256 string `json:"CodeSha256"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		t.Fatalf("decode CreateFunction: %v", err)
	}
	return config.CodeSha256
}
