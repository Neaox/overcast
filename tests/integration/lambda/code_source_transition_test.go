package lambda_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

type codeSourceConfiguration struct {
	CodeSha256 string `json:"CodeSha256"`
	RevisionID string `json:"RevisionId"`
}

func TestUpdateFunctionCode_S3ToInlineDetachesS3Source(t *testing.T) {
	// Given: a Zip function initially uses an S3 object as its code source.
	srv := helpers.NewTestServer(t)
	putLambdaCodeObject(t, srv, "lambda-source-transitions", "function.zip", "original-s3-code")
	created := createS3CodeFunction(t, srv, "s3-to-inline", "lambda-source-transitions", "function.zip")

	// When: one function is updated to an inline deployment package.
	inlineCode := []byte("current-inline-code")
	updateResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/s3-to-inline/code"), map[string]any{
		"ZipFile": inlineCode,
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	var inlineConfig codeSourceConfiguration
	decodeJSON(t, updateResp, &inlineConfig)

	// Then: the inline deployment package and revision replace the S3-backed state.
	got := getCodeSourceConfiguration(t, srv, "s3-to-inline")
	if got.CodeSha256 != inlineConfig.CodeSha256 {
		t.Fatalf("CodeSha256 after inline update = %q, want %q", got.CodeSha256, inlineConfig.CodeSha256)
	}
	if got.RevisionID != inlineConfig.RevisionID {
		t.Fatalf("RevisionId after inline update = %q, want %q", got.RevisionID, inlineConfig.RevisionID)
	}
	if inlineConfig.RevisionID == created.RevisionID {
		t.Fatal("successful inline update did not change RevisionId")
	}
	if inlineConfig.CodeSha256 == created.CodeSha256 {
		t.Fatal("successful inline update did not change CodeSha256")
	}
}

func TestUpdateFunctionCode_S3FetchFailureDoesNotMutateFunction(t *testing.T) {
	// Given: a Zip function has an inline deployment package.
	srv := helpers.NewTestServer(t)
	createResp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "failed-s3-code-update",
		Runtime:      "nodejs20.x",
		Handler:      "index.handler",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		Code:         &lambdaCode{ZipFile: []byte("original-inline-code")},
	})
	helpers.AssertStatus(t, createResp, http.StatusCreated)
	var created codeSourceConfiguration
	decodeJSON(t, createResp, &created)
	putLambdaCodeObject(t, srv, "missing-code-bucket", "present.zip", "unused-code")

	// When: an update references an S3 object that cannot be fetched.
	updateResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/failed-s3-code-update/code"), map[string]any{
		"S3Bucket": "missing-code-bucket",
		"S3Key":    "missing.zip",
	})
	defer updateResp.Body.Close()

	// Then: Lambda returns an AWS-shaped error before mutating code or revision.
	helpers.AssertStatus(t, updateResp, http.StatusBadRequest)
	helpers.AssertRequestID(t, updateResp)
	helpers.AssertJSONError(t, updateResp, "InvalidParameterValueException")
	got := getCodeSourceConfiguration(t, srv, "failed-s3-code-update")
	if got != created {
		t.Fatalf("configuration mutated after failed S3 fetch: got %#v, want %#v", got, created)
	}
}

func TestUpdateFunctionCode_RejectsSourceForDifferentPackageTypeBeforeMutation(t *testing.T) {
	tests := []struct {
		name         string
		create       createFunctionReq
		update       map[string]any
		functionName string
		status       int
		errorCode    string
		unsupported  bool
	}{
		{
			name:         "Zip function with ImageUri",
			functionName: "zip-with-image",
			create: createFunctionReq{
				FunctionName: "zip-with-image",
				Runtime:      "nodejs20.x",
				Handler:      "index.handler",
				Role:         "arn:aws:iam::000000000000:role/lambda-role",
				Code:         &lambdaCode{ZipFile: []byte("zip-code")},
			},
			update:      map[string]any{"ImageUri": "000000000000.dkr.ecr.us-east-1.amazonaws.com/fn:latest"},
			status:      http.StatusNotImplemented,
			errorCode:   "NotImplemented",
			unsupported: true,
		},
		{
			name:         "Image function with ZipFile",
			functionName: "image-with-zip",
			create: createFunctionReq{
				FunctionName: "image-with-zip",
				Role:         "arn:aws:iam::000000000000:role/lambda-role",
				PackageType:  "Image",
				Code:         &lambdaCode{ImageUri: "000000000000.dkr.ecr.us-east-1.amazonaws.com/fn:original"},
			},
			update:    map[string]any{"ZipFile": []byte("zip-code")},
			status:    http.StatusBadRequest,
			errorCode: "InvalidParameterValueException",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a function with a fixed deployment package type.
			srv := helpers.NewTestServer(t)
			createResp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), tc.create)
			helpers.AssertStatus(t, createResp, http.StatusCreated)
			var created codeSourceConfiguration
			decodeJSON(t, createResp, &created)

			// When: UpdateFunctionCode supplies a source for the other package type.
			updateResp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/"+tc.functionName+"/code"), tc.update)
			defer updateResp.Body.Close()

			// Then: the request fails loudly without changing code or revision.
			helpers.AssertStatus(t, updateResp, tc.status)
			helpers.AssertRequestID(t, updateResp)
			if tc.unsupported {
				helpers.AssertHeader(t, updateResp, "x-emulator-unsupported", "true")
			}
			helpers.AssertJSONError(t, updateResp, tc.errorCode)
			got := getCodeSourceConfiguration(t, srv, tc.functionName)
			if got != created {
				t.Fatalf("configuration mutated after rejected source transition: got %#v, want %#v", got, created)
			}
		})
	}
}

func TestUpdateFunctionCode_RejectsMixedZipSourcesBeforeMutation(t *testing.T) {
	// Given: a Zip function exists.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "mixed-zip-sources")
	created := getCodeSourceConfiguration(t, srv, "mixed-zip-sources")

	// When: both mutually exclusive Zip source forms are supplied.
	resp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/mixed-zip-sources/code"), map[string]any{
		"ZipFile":  []byte("inline"),
		"S3Bucket": "lambda-source-transitions",
		"S3Key":    "function.zip",
	})
	defer resp.Body.Close()

	// Then: the uncertain validation-message edge fails loudly without mutation.
	helpers.AssertStatus(t, resp, http.StatusNotImplemented)
	helpers.AssertRequestID(t, resp)
	helpers.AssertHeader(t, resp, "x-emulator-unsupported", "true")
	helpers.AssertJSONError(t, resp, "NotImplemented")
	got := getCodeSourceConfiguration(t, srv, "mixed-zip-sources")
	if got != created {
		t.Fatalf("configuration mutated after unsupported mixed source: got %#v, want %#v", got, created)
	}
}

func TestUpdateFunctionCode_MixedImageSourcesFailLoudlyBeforeMutation(t *testing.T) {
	// Given: an image function exists.
	srv := helpers.NewTestServer(t)
	createResp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), createFunctionReq{
		FunctionName: "mixed-image-sources",
		Role:         "arn:aws:iam::000000000000:role/lambda-role",
		PackageType:  "Image",
		Code:         &lambdaCode{ImageUri: "000000000000.dkr.ecr.us-east-1.amazonaws.com/fn:original"},
	})
	helpers.AssertStatus(t, createResp, http.StatusCreated)
	var created codeSourceConfiguration
	decodeJSON(t, createResp, &created)

	// When: ImageUri is combined with an incompatible Zip source.
	resp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/mixed-image-sources/code"), map[string]any{
		"ImageUri": "000000000000.dkr.ecr.us-east-1.amazonaws.com/fn:next",
		"ZipFile":  []byte("zip-code"),
	})
	defer resp.Body.Close()

	// Then: Overcast does not emit a factually wrong missing-ImageUri message.
	helpers.AssertStatus(t, resp, http.StatusNotImplemented)
	helpers.AssertRequestID(t, resp)
	helpers.AssertHeader(t, resp, "x-emulator-unsupported", "true")
	helpers.AssertJSONError(t, resp, "NotImplemented")
	got := getCodeSourceConfiguration(t, srv, "mixed-image-sources")
	if got != created {
		t.Fatalf("configuration mutated after unsupported mixed source: got %#v, want %#v", got, created)
	}
}

func createS3CodeFunction(t *testing.T, srv *helpers.TestServer, name, bucket, key string) codeSourceConfiguration {
	t.Helper()
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), map[string]any{
		"FunctionName": name,
		"Runtime":      "nodejs20.x",
		"Handler":      "index.handler",
		"Role":         "arn:aws:iam::000000000000:role/lambda-role",
		"Code": map[string]any{
			"S3Bucket": bucket,
			"S3Key":    key,
		},
	})
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var config codeSourceConfiguration
	decodeJSON(t, resp, &config)
	return config
}

func getCodeSourceConfiguration(t *testing.T, srv *helpers.TestServer, name string) codeSourceConfiguration {
	t.Helper()
	resp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/"+name+"/configuration"), nil)
	helpers.AssertStatus(t, resp, http.StatusOK)
	var config codeSourceConfiguration
	decodeJSON(t, resp, &config)
	return config
}

func putLambdaCodeObject(t *testing.T, srv *helpers.TestServer, bucket, key, body string) {
	t.Helper()
	bucketResp := doJSON(t, http.MethodPut, srv.URL+"/"+bucket, nil)
	defer bucketResp.Body.Close()
	if bucketResp.StatusCode != http.StatusOK && bucketResp.StatusCode != http.StatusConflict {
		t.Fatalf("create bucket %s: status %d", bucket, bucketResp.StatusCode)
	}

	objectReq, err := http.NewRequest(http.MethodPut, srv.URL+"/"+bucket+"/"+key, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build put object request: %v", err)
	}
	objectResp, err := http.DefaultClient.Do(objectReq)
	if err != nil {
		t.Fatalf("put object %s/%s: %v", bucket, key, err)
	}
	defer objectResp.Body.Close()
	helpers.AssertStatus(t, objectResp, http.StatusOK)
}
