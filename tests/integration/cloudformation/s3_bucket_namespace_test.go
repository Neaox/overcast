// s3_bucket_namespace_test.go covers AWS::S3::Bucket's BucketNamespace and
// BucketNamePrefix properties end to end through a real CreateStack/
// UpdateStack, including the Fn::Sub pseudo-parameter path the issue calls
// out (issue #1471). Router-free unit coverage of the handler's own decode
// and replacement logic lives in
// internal/services/cloudformation/provisioner_s3_namespace_test.go.
package cloudformation_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// TestS3BucketNamespace_bucketNamePrefixCreatesAccountRegionalBucket covers
// BucketNamePrefix: CloudFormation appends
// "-<AWS::AccountId>-<AWS::Region>-an" itself, the console-style path the
// issue's implementation plan describes.
func TestS3BucketNamespace_bucketNamePrefixCreatesAccountRegionalBucket(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "cfn-s3-ns-prefix"
	template := `{"Resources":{"Bucket":{"Type":"AWS::S3::Bucket","Properties":{"BucketNamePrefix":"cfn-ns-prefix"}}}}`

	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {template},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// Default test-server account (000000000000) and region (us-east-1) —
	// see tests/helpers/server.go defaultTestConfig.
	const wantBucket = "cfn-ns-prefix-000000000000-us-east-1-an"
	physicalID := stackResourcePhysicalID(t, srv, stackName, "Bucket")
	if physicalID != wantBucket {
		t.Errorf("physical resource ID = %q, want %q", physicalID, wantBucket)
	}
	assertBucketExists(t, srv, wantBucket, true)
}

// TestS3BucketNamespace_subPseudoParameterCreatesAccountRegionalBucket covers
// the other documented path: an explicit BucketName built from
// Fn::Sub "${AWS::AccountId}"/"${AWS::Region}", paired with
// BucketNamespace: account-regional. Pseudo-parameter resolution itself
// (template.go) already worked before this issue — what this test pins is
// that CreateBucket accepts the result once the account-regional header
// travels with it.
func TestS3BucketNamespace_subPseudoParameterCreatesAccountRegionalBucket(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "cfn-s3-ns-sub"
	template := `{"Resources":{"Bucket":{"Type":"AWS::S3::Bucket","Properties":{` +
		`"BucketName":{"Fn::Sub":"cfn-ns-sub-${AWS::AccountId}-${AWS::Region}-an"},` +
		`"BucketNamespace":"account-regional"}}}}`

	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {template},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	const wantBucket = "cfn-ns-sub-000000000000-us-east-1-an"
	physicalID := stackResourcePhysicalID(t, srv, stackName, "Bucket")
	if physicalID != wantBucket {
		t.Errorf("physical resource ID = %q, want %q", physicalID, wantBucket)
	}
	assertBucketExists(t, srv, wantBucket, true)
}

// TestS3BucketNamespace_bucketNamePrefixChangeReplaces exercises Replacement
// on update through a real stack update, mirroring the pattern
// TestUpdateStack_successfulReplacementDeletesTheOriginal already uses for
// SQS: the old bucket must be gone and the new one present, not just the
// stack's own bookkeeping updated.
func TestS3BucketNamespace_bucketNamePrefixChangeReplaces(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "cfn-s3-ns-replace"
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {`{"Resources":{"Bucket":{"Type":"AWS::S3::Bucket","Properties":{"BucketNamePrefix":"cfn-ns-replace-v1"}}}}`},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	const original = "cfn-ns-replace-v1-000000000000-us-east-1-an"
	assertBucketExists(t, srv, original, true)

	update := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {`{"Resources":{"Bucket":{"Type":"AWS::S3::Bucket","Properties":{"BucketNamePrefix":"cfn-ns-replace-v2"}}}}`},
	})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	const replacement = "cfn-ns-replace-v2-000000000000-us-east-1-an"
	assertBucketExists(t, srv, replacement, true)
	assertBucketExists(t, srv, original, false)
}

func assertBucketExists(t *testing.T, srv *helpers.TestServer, bucket string, want bool) {
	t.Helper()
	resp, err := http.Head(srv.URL + "/" + bucket)
	if err != nil {
		t.Fatalf("HEAD /%s: %v", bucket, err)
	}
	defer resp.Body.Close()
	got := resp.StatusCode == http.StatusOK
	if got != want {
		verb := "not to exist"
		if want {
			verb = "to exist"
		}
		t.Errorf("expected bucket %q %s; HEAD status = %d", bucket, verb, resp.StatusCode)
	}
}
