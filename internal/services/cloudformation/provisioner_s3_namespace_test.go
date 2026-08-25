package cloudformation

// provisioner_s3_namespace_test.go covers AWS::S3::Bucket's BucketNamespace
// and BucketNamePrefix properties (issue #1471): decode-time validation, the
// bucket-name construction and x-amz-bucket-namespace header pass-through in
// Create, and Replacement-on-update for both properties.
//
// End-to-end coverage (a real CreateStack, including the Fn::Sub
// pseudo-parameter path) lives in
// tests/integration/cloudformation/s3_bucket_namespace_test.go — these are
// the fast, router-free unit tests for the handler's own logic.

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestDecodeS3BucketProperties_bucketNamespaceAccepted(t *testing.T) {
	decoded, err := decodeS3BucketProperties(map[string]any{"BucketNamespace": "account-regional"})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.BucketNamespace == nil || *decoded.BucketNamespace != "account-regional" {
		t.Fatalf("BucketNamespace = %v, want account-regional", decoded.BucketNamespace)
	}
}

func TestDecodeS3BucketProperties_bucketNamePrefixAccepted(t *testing.T) {
	decoded, err := decodeS3BucketProperties(map[string]any{"BucketNamePrefix": "my-app"})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.BucketNamePrefix == nil || *decoded.BucketNamePrefix != "my-app" {
		t.Fatalf("BucketNamePrefix = %v, want my-app", decoded.BucketNamePrefix)
	}
}

func TestDecodeS3BucketProperties_bucketNamespaceInvalidValueRejected(t *testing.T) {
	if _, err := decodeS3BucketProperties(map[string]any{"BucketNamespace": "regional"}); err == nil {
		t.Fatal("expected error for an unrecognised BucketNamespace value")
	}
}

// Unverified against real AWS/CloudFormation: the exact validation message
// for BucketName + BucketNamePrefix both set — see issue #1471 "Needs AWS
// verification" item 4. Modeled as a decode-time rejection, matching this
// function's other property-shape conflict errors.
func TestDecodeS3BucketProperties_bucketNameAndPrefixMutuallyExclusive(t *testing.T) {
	_, err := decodeS3BucketProperties(map[string]any{
		"BucketName":       "explicit-name",
		"BucketNamePrefix": "prefix",
	})
	if err == nil {
		t.Fatal("expected error when BucketName and BucketNamePrefix are both set")
	}
}

func TestDecodeS3BucketProperties_bucketNamePrefixRejectsExplicitGlobalNamespace(t *testing.T) {
	_, err := decodeS3BucketProperties(map[string]any{
		"BucketNamePrefix": "my-app",
		"BucketNamespace":  "global",
	})
	if err == nil {
		t.Fatal("expected error when BucketNamePrefix is combined with an explicit non-account-regional BucketNamespace")
	}
}

func TestS3BucketHandlerCreate_bucketNamePrefixAppendsAccountRegionSuffix(t *testing.T) {
	var gotPath, gotNamespace string
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotNamespace = r.Header.Get("X-Amz-Bucket-Namespace")
		w.WriteHeader(http.StatusOK)
	})
	h := &s3BucketHandler{}
	props := map[string]any{"BucketNamePrefix": "my-app"}
	rCtx := &resolveContext{Region: "us-west-2", AccountID: "111122223333", LogicalID: "Bucket"}

	bucketName, attrs, err := h.Create(context.Background(), router, nil, props, rCtx)
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	const want = "my-app-111122223333-us-west-2-an"
	if bucketName != want {
		t.Errorf("bucket name = %q, want %q", bucketName, want)
	}
	if gotPath != "/"+want {
		t.Errorf("dispatched path = %q, want %q", gotPath, "/"+want)
	}
	if gotNamespace != "account-regional" {
		t.Errorf("X-Amz-Bucket-Namespace header = %q, want account-regional", gotNamespace)
	}
	if attrs["BucketName"] != want {
		t.Errorf("GetAtt BucketName = %q, want %q", attrs["BucketName"], want)
	}
}

func TestS3BucketHandlerCreate_explicitBucketNameWithNamespaceHeaderPassthrough(t *testing.T) {
	var gotPath, gotNamespace string
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotNamespace = r.Header.Get("X-Amz-Bucket-Namespace")
		w.WriteHeader(http.StatusOK)
	})
	h := &s3BucketHandler{}
	const bucketName = "amzn-app-000000000000-us-east-1-an"
	props := map[string]any{"BucketName": bucketName, "BucketNamespace": "account-regional"}
	rCtx := &resolveContext{Region: "us-east-1", AccountID: "000000000000"}

	got, _, err := h.Create(context.Background(), router, nil, props, rCtx)
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if got != bucketName {
		t.Errorf("bucket name = %q, want %q", got, bucketName)
	}
	if gotPath != "/"+bucketName {
		t.Errorf("dispatched path = %q, want %q", gotPath, "/"+bucketName)
	}
	if gotNamespace != "account-regional" {
		t.Errorf("X-Amz-Bucket-Namespace header = %q, want account-regional", gotNamespace)
	}
}

func TestS3BucketHandlerCreate_globalNamespaceSendsNoHeader(t *testing.T) {
	var sawHeader bool
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawHeader = r.Header.Get("X-Amz-Bucket-Namespace") != ""
		w.WriteHeader(http.StatusOK)
	})
	h := &s3BucketHandler{}
	props := map[string]any{"BucketName": "plain-bucket"}
	rCtx := &resolveContext{Region: "us-east-1", AccountID: "000000000000"}

	if _, _, err := h.Create(context.Background(), router, nil, props, rCtx); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if sawHeader {
		t.Error("expected no X-Amz-Bucket-Namespace header for a bucket with no namespace property set — the global-namespace default should stay a silent no-op against every existing template")
	}
}

func TestS3BucketHandlerUpdate_bucketNamespaceChangeRequiresReplacement(t *testing.T) {
	h := &s3BucketHandler{}
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	const name = "amzn-app-000000000000-us-east-1-an"
	oldProps := map[string]any{"BucketName": name}
	newProps := map[string]any{"BucketName": name, "BucketNamespace": "account-regional"}
	rCtx := &resolveContext{Region: "us-east-1", AccountID: "000000000000"}

	_, _, err := h.Update(context.Background(), router, nil, name, newProps, oldProps, rCtx)
	if !errors.Is(err, errReplacementRequired) {
		t.Fatalf("Update err = %v, want errReplacementRequired", err)
	}
}

func TestS3BucketHandlerUpdate_bucketNamePrefixChangeRequiresReplacement(t *testing.T) {
	h := &s3BucketHandler{}
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	oldProps := map[string]any{"BucketNamePrefix": "old-prefix"}
	newProps := map[string]any{"BucketNamePrefix": "new-prefix"}
	rCtx := &resolveContext{Region: "us-east-1", AccountID: "000000000000"}

	_, _, err := h.Update(context.Background(), router, nil, "old-prefix-000000000000-us-east-1-an", newProps, oldProps, rCtx)
	if !errors.Is(err, errReplacementRequired) {
		t.Fatalf("Update err = %v, want errReplacementRequired", err)
	}
}

func TestS3BucketHandlerUpdate_unchangedNamespacePropertiesAreNotReplacement(t *testing.T) {
	h := &s3BucketHandler{}
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	const name = "amzn-app-000000000000-us-east-1-an"
	props := map[string]any{"BucketName": name, "BucketNamespace": "account-regional"}
	rCtx := &resolveContext{Region: "us-east-1", AccountID: "000000000000"}

	if _, _, err := h.Update(context.Background(), router, nil, name, props, props, rCtx); err != nil {
		t.Fatalf("Update returned error for an unchanged bucket: %v", err)
	}
}
