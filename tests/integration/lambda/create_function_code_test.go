package lambda_test

import (
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// AWS rejects CreateFunction when the deployment package cannot be read from
// S3 (API_CreateFunction: InvalidParameterValueException), and creates nothing.
// Overcast used to log a warning and create a function with no usable code,
// which only surfaced as a confusing failure at invoke time.
func TestCreateFunction_S3FetchFailureCreatesNothing(t *testing.T) {
	tests := []struct {
		name   string
		bucket string
		key    string
		setup  func(t *testing.T, srv *helpers.TestServer)
	}{
		{
			name:   "missing bucket",
			bucket: "create-code-absent-bucket",
			key:    "function.zip",
		},
		{
			name:   "missing key",
			bucket: "create-code-present-bucket",
			key:    "absent.zip",
			setup: func(t *testing.T, srv *helpers.TestServer) {
				putLambdaCodeObject(t, srv, "create-code-present-bucket", "present.zip", "unused-code")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Given: an S3 deployment package location that cannot be fetched.
			srv := helpers.NewTestServer(t)
			if tc.setup != nil {
				tc.setup(t, srv)
			}

			// When: CreateFunction references it.
			resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), map[string]any{
				"FunctionName": "s3-fetch-failure",
				"Runtime":      "nodejs20.x",
				"Handler":      "index.handler",
				"Role":         "arn:aws:iam::000000000000:role/lambda-role",
				"Code":         map[string]any{"S3Bucket": tc.bucket, "S3Key": tc.key},
			})
			defer resp.Body.Close()

			// Then: the create fails with the AWS GetObject translation.
			helpers.AssertStatus(t, resp, http.StatusBadRequest)
			helpers.AssertRequestID(t, resp)
			helpers.AssertJSONError(t, resp, "InvalidParameterValueException")

			// And: no function was persisted.
			getResp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/s3-fetch-failure"), nil)
			defer getResp.Body.Close()
			helpers.AssertStatus(t, getResp, http.StatusNotFound)

			listResp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions"), nil)
			defer listResp.Body.Close()
			helpers.AssertStatus(t, listResp, http.StatusOK)
			var list struct {
				Functions []struct {
					FunctionName string `json:"FunctionName"`
				} `json:"Functions"`
			}
			decodeJSON(t, listResp, &list)
			for _, fn := range list.Functions {
				if fn.FunctionName == "s3-fetch-failure" {
					t.Fatal("ListFunctions still reports the function after a failed create")
				}
			}
		})
	}
}

// A reachable S3 package still creates a function whose code is the object's
// bytes — the failure path must not cost the working path.
func TestCreateFunction_S3PackageIsFetchedIntoTheFunction(t *testing.T) {
	// Given: a deployment package uploaded to S3.
	srv := helpers.NewTestServer(t)
	putLambdaCodeObject(t, srv, "create-code-ok", "function.zip", "real-s3-code")

	// When: CreateFunction references it.
	created := createS3CodeFunction(t, srv, "s3-code-ok", "create-code-ok", "function.zip")

	// Then: the stored package is the S3 object, not an empty archive.
	if created.CodeSha256 == "" {
		t.Fatal("CodeSha256 is empty after an S3-sourced create")
	}
	inline := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "s3-code-ok-reference",
		Runtime:      "nodejs20.x",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		Code:         &lambdaCode{ZipFile: []byte("real-s3-code")},
	})
	defer inline.Body.Close()
	helpers.AssertStatus(t, inline, http.StatusCreated)
	var reference codeSourceConfiguration
	decodeJSON(t, inline, &reference)
	if created.CodeSha256 != reference.CodeSha256 {
		t.Fatalf("CodeSha256 from S3 = %q, want the hash of the object's bytes %q", created.CodeSha256, reference.CodeSha256)
	}
}
