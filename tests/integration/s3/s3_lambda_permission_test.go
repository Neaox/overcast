package s3_test

// s3_lambda_permission_test.go — S3 → Lambda delivery under
// OVERCAST_ENFORCE_LAMBDA_RESOURCE_POLICY.
//
// Real S3 checks the function's resource-based policy twice over: once when the
// notification configuration is put ("In the case of AWS Lambda destinations,
// Amazon S3 verifies that the Lambda function permissions grant Amazon S3
// permission to invoke the function from the Amazon S3 bucket" — and the PUT is
// atomic, so an unverifiable destination refuses the whole configuration), and
// again at delivery time, where a permission removed afterwards makes the
// notification fail silently.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// seedLambdaFunction writes a function record straight into the store, the way
// the existing notification tests do: these assert on delivery reaching Lambda,
// not on the function running, so no Docker is involved.
func seedLambdaFunction(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	arn := "arn:aws:lambda:us-east-1:000000000000:function:" + name
	fn := map[string]any{
		"name": name, "arn": arn, "runtime": "nodejs20.x", "handler": "index.handler",
		"state": "Active", "timeout": 30, "memory_size": 128,
	}
	encoded, err := json.Marshal(fn)
	if err != nil {
		t.Fatalf("marshalling the seeded function: %v", err)
	}
	if err := srv.Store.Set(context.Background(), "lambda:functions", "us-east-1/"+name, string(encoded)); err != nil {
		t.Fatalf("seeding the function: %v", err)
	}
	return arn
}

// addLambdaPermission calls AddPermission over HTTP, exactly as
// `aws lambda add-permission` does.
func addLambdaPermission(t *testing.T, srv *helpers.TestServer, functionName, qualifier string, body map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshalling the permission: %v", err)
	}
	url := srv.URL + "/2015-03-31/functions/" + functionName + "/policy"
	if qualifier != "" {
		url += "?Qualifier=" + qualifier
	}
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("AddPermission: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusCreated)
}

// lambdaNotificationXML is a notification configuration with one Lambda
// destination on object creation.
func lambdaNotificationXML(functionARN string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<NotificationConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <CloudFunctionConfiguration>
    <Id>invoke-on-put</Id>
    <CloudFunction>` + functionARN + `</CloudFunction>
    <Event>s3:ObjectCreated:*</Event>
  </CloudFunctionConfiguration>
</NotificationConfiguration>`
}

func putLambdaNotification(t *testing.T, srv *helpers.TestServer, bucket, functionARN string) *http.Response {
	t.Helper()
	resp, err := http.DefaultClient.Do(put(srv, "/"+bucket+"?notification",
		[]byte(lambdaNotificationXML(functionARN)), map[string]string{"Content-Type": "application/xml"}))
	if err != nil {
		t.Fatalf("PutBucketNotificationConfiguration: %v", err)
	}
	return resp
}

// waitForInvocation reports whether an invocation record for name appeared
// within a short window. Delivery is asynchronous, so both the positive and the
// negative case have to wait; the negative one is bounded by the same budget.
func waitForInvocation(t *testing.T, srv *helpers.TestServer, name string) bool {
	t.Helper()
	for i := 0; i < 40; i++ {
		kvs, err := srv.Store.Scan(context.Background(), "lambda:invocations", "us-east-1/"+name+":")
		if err == nil && len(kvs) > 0 {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func TestPutBucketNotificationConfiguration_lambdaPermissionMissing(t *testing.T) {
	// Given: enforcement on, a function, and a bucket — but no AddPermission
	srv := helpers.NewTestServer(t, helpers.WithEnforceLambdaResourcePolicy(true))
	createBucket(t, srv, "no-permission-bucket")
	arn := seedLambdaFunction(t, srv, "unpermitted-handler")

	// When: a Lambda notification destination is configured
	resp := putLambdaNotification(t, srv, "no-permission-bucket", arn)
	defer resp.Body.Close()

	// Then: S3 refuses the whole configuration, as it does on AWS
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "InvalidArgument") {
		t.Errorf("error code: want InvalidArgument, got %s", body)
	}
	if !strings.Contains(body, "Unable to validate the following destination configurations") {
		t.Errorf("error message: want the destination-validation wording, got %s", body)
	}

	// And: nothing was stored — the PUT is atomic
	getResp, err := http.DefaultClient.Do(get(srv, "/no-permission-bucket?notification"))
	if err != nil {
		t.Fatalf("GetBucketNotificationConfiguration: %v", err)
	}
	defer getResp.Body.Close()
	if stored := helpers.ReadBody(t, getResp); strings.Contains(stored, "CloudFunctionConfiguration") {
		t.Errorf("the refused configuration was stored anyway: %s", stored)
	}
}

func TestPutBucketNotificationConfiguration_lambdaPermissionGranted(t *testing.T) {
	// Given: enforcement on and the permission `aws lambda add-permission` writes
	srv := helpers.NewTestServer(t, helpers.WithEnforceLambdaResourcePolicy(true))
	createBucket(t, srv, "permitted-bucket")
	arn := seedLambdaFunction(t, srv, "permitted-handler")
	addLambdaPermission(t, srv, "permitted-handler", "", map[string]any{
		"StatementId": "s3invoke", "Action": "lambda:InvokeFunction",
		"Principal": "s3.amazonaws.com", "SourceArn": "arn:aws:s3:::permitted-bucket",
		"SourceAccount": "000000000000",
	})

	// When: the notification is configured and an object is written
	resp := putLambdaNotification(t, srv, "permitted-bucket", arn)
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	putResp, err := http.DefaultClient.Do(put(srv, "/permitted-bucket/trigger.txt", []byte("hello"), nil))
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	putResp.Body.Close()
	helpers.AssertStatus(t, putResp, http.StatusOK)

	// Then: the notification reaches the function
	if !waitForInvocation(t, srv, "permitted-handler") {
		t.Fatal("expected an invocation record, got none")
	}
}

func TestPutBucketNotificationConfiguration_lambdaPermissionOtherBucket(t *testing.T) {
	// Given: a permission scoped to a different bucket
	srv := helpers.NewTestServer(t, helpers.WithEnforceLambdaResourcePolicy(true))
	createBucket(t, srv, "wrong-source-bucket")
	arn := seedLambdaFunction(t, srv, "other-bucket-handler")
	addLambdaPermission(t, srv, "other-bucket-handler", "", map[string]any{
		"StatementId": "s3invoke", "Action": "lambda:InvokeFunction",
		"Principal": "s3.amazonaws.com", "SourceArn": "arn:aws:s3:::some-other-bucket",
	})

	// When: this bucket configures the destination
	resp := putLambdaNotification(t, srv, "wrong-source-bucket", arn)
	defer resp.Body.Close()

	// Then: the SourceArn condition does not match, so validation fails
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestPutBucketNotificationConfiguration_lambdaPermissionWrongSourceAccount(t *testing.T) {
	// Given: a permission requiring the bucket to belong to another account
	srv := helpers.NewTestServer(t, helpers.WithEnforceLambdaResourcePolicy(true))
	createBucket(t, srv, "wrong-account-bucket")
	arn := seedLambdaFunction(t, srv, "wrong-account-handler")
	addLambdaPermission(t, srv, "wrong-account-handler", "", map[string]any{
		"StatementId": "s3invoke", "Action": "lambda:InvokeFunction",
		"Principal": "s3.amazonaws.com", "SourceArn": "arn:aws:s3:::wrong-account-bucket",
		"SourceAccount": "111122223333",
	})

	// When: the bucket in this account configures the destination
	resp := putLambdaNotification(t, srv, "wrong-account-bucket", arn)
	defer resp.Body.Close()

	// Then: the SourceAccount condition fails
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestPutBucketNotificationConfiguration_lambdaPermissionWildcardSourceARN(t *testing.T) {
	// Given: a permission admitting every bucket whose name starts with "logs-"
	srv := helpers.NewTestServer(t, helpers.WithEnforceLambdaResourcePolicy(true))
	createBucket(t, srv, "logs-2026")
	arn := seedLambdaFunction(t, srv, "wildcard-handler")
	addLambdaPermission(t, srv, "wildcard-handler", "", map[string]any{
		"StatementId": "s3invoke", "Action": "lambda:InvokeFunction",
		"Principal": "s3.amazonaws.com", "SourceArn": "arn:aws:s3:::logs-*",
	})

	// When: one of those buckets configures the destination
	resp := putLambdaNotification(t, srv, "logs-2026", arn)
	defer resp.Body.Close()

	// Then: the wildcard matches
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestPutBucketNotificationConfiguration_lambdaPermissionAliasDoesNotAuthoriseLatest(t *testing.T) {
	// Given: the permission is on the "live" alias rather than the function
	srv := helpers.NewTestServer(t, helpers.WithEnforceLambdaResourcePolicy(true))
	createBucket(t, srv, "alias-bucket")
	arn := seedLambdaFunction(t, srv, "alias-handler")
	seedLambdaAlias(t, srv, "alias-handler", "live")
	addLambdaPermission(t, srv, "alias-handler", "live", map[string]any{
		"StatementId": "s3invoke", "Action": "lambda:InvokeFunction",
		"Principal": "s3.amazonaws.com", "SourceArn": "arn:aws:s3:::alias-bucket",
	})

	// When: the unqualified function ARN is configured as the destination
	resp := putLambdaNotification(t, srv, "alias-bucket", arn)
	defer resp.Body.Close()

	// Then: a qualified grant does not authorise $LATEST
	helpers.AssertStatus(t, resp, http.StatusBadRequest)

	// And: the alias ARN itself is accepted
	aliasResp := putLambdaNotification(t, srv, "alias-bucket", arn+":live")
	defer aliasResp.Body.Close()
	helpers.AssertStatus(t, aliasResp, http.StatusOK)
}

func TestPutBucketNotificationConfiguration_skipDestinationValidation(t *testing.T) {
	// Given: enforcement on and no permission at all
	srv := helpers.NewTestServer(t, helpers.WithEnforceLambdaResourcePolicy(true))
	createBucket(t, srv, "skip-validation-bucket")
	arn := seedLambdaFunction(t, srv, "skipped-handler")

	// When: the caller asks S3 to skip destination validation
	resp, err := http.DefaultClient.Do(put(srv, "/skip-validation-bucket?notification",
		[]byte(lambdaNotificationXML(arn)), map[string]string{
			"Content-Type":                      "application/xml",
			"x-amz-skip-destination-validation": "true",
		}))
	if err != nil {
		t.Fatalf("PutBucketNotificationConfiguration: %v", err)
	}
	defer resp.Body.Close()

	// Then: the configuration is stored unchecked
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestPutObject_lambdaPermissionRemovedAfterConfiguration(t *testing.T) {
	// Given: a configured, permitted destination whose permission is then removed
	srv := helpers.NewTestServer(t, helpers.WithEnforceLambdaResourcePolicy(true))
	createBucket(t, srv, "revoked-bucket")
	arn := seedLambdaFunction(t, srv, "revoked-handler")
	addLambdaPermission(t, srv, "revoked-handler", "", map[string]any{
		"StatementId": "s3invoke", "Action": "lambda:InvokeFunction",
		"Principal": "s3.amazonaws.com", "SourceArn": "arn:aws:s3:::revoked-bucket",
	})
	resp := putLambdaNotification(t, srv, "revoked-bucket", arn)
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	removeReq, _ := http.NewRequest(http.MethodDelete,
		srv.URL+"/2015-03-31/functions/revoked-handler/policy/s3invoke", nil)
	removeResp, err := http.DefaultClient.Do(removeReq)
	if err != nil {
		t.Fatalf("RemovePermission: %v", err)
	}
	removeResp.Body.Close()
	helpers.AssertStatus(t, removeResp, http.StatusNoContent)

	// When: an object is written
	putResp, err := http.DefaultClient.Do(put(srv, "/revoked-bucket/trigger.txt", []byte("hello"), nil))
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	putResp.Body.Close()
	helpers.AssertStatus(t, putResp, http.StatusOK)

	// Then: the write still succeeds and the notification is silently dropped
	if waitForInvocation(t, srv, "revoked-handler") {
		t.Fatal("expected no invocation once the permission was removed")
	}
}

func TestPutBucketNotificationConfiguration_enforcementOffNeedsNoPermission(t *testing.T) {
	// Given: the default server, with enforcement off
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "unenforced-bucket")
	arn := seedLambdaFunction(t, srv, "unenforced-handler")

	// When: a destination with no permission at all is configured and fired
	resp := putLambdaNotification(t, srv, "unenforced-bucket", arn)
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	putResp, err := http.DefaultClient.Do(put(srv, "/unenforced-bucket/trigger.txt", []byte("hello"), nil))
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	putResp.Body.Close()

	// Then: delivery is unchanged — the knob is what turns the check on
	if !waitForInvocation(t, srv, "unenforced-handler") {
		t.Fatal("expected an invocation with enforcement off, got none")
	}
}

// seedLambdaAlias writes an alias record so AddPermission accepts a qualifier.
func seedLambdaAlias(t *testing.T, srv *helpers.TestServer, functionName, alias string) {
	t.Helper()
	record := map[string]any{
		"name":             alias,
		"function_name":    functionName,
		"function_version": "$LATEST",
		"alias_arn":        fmt.Sprintf("arn:aws:lambda:us-east-1:000000000000:function:%s:%s", functionName, alias),
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshalling the seeded alias: %v", err)
	}
	if err := srv.Store.Set(context.Background(), "lambda:aliases",
		"us-east-1/"+functionName+":"+alias, string(encoded)); err != nil {
		t.Fatalf("seeding the alias: %v", err)
	}
}
