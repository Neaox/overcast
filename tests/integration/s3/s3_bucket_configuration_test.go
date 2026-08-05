package s3_test

import (
	"encoding/xml"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func TestDeleteBucketCors_removesConfiguration(t *testing.T) {
	// Given: a bucket with a CORS configuration
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "delete-cors-bucket")
	corsXML := `<CORSConfiguration><CORSRule><AllowedMethod>GET</AllowedMethod><AllowedOrigin>*</AllowedOrigin></CORSRule></CORSConfiguration>`
	putResp, err := http.DefaultClient.Do(put(srv, "/delete-cors-bucket?cors", []byte(corsXML), map[string]string{"Content-Type": "application/xml"}))
	if err != nil {
		t.Fatalf("PutBucketCors: %v", err)
	}
	putResp.Body.Close()
	helpers.AssertStatus(t, putResp, http.StatusOK)

	// When: DeleteBucketCors is called
	deleteResp, err := http.DefaultClient.Do(bucketConfigurationDeleteRequest(t, srv, "/delete-cors-bucket?cors"))
	if err != nil {
		t.Fatalf("DeleteBucketCors: %v", err)
	}
	defer deleteResp.Body.Close()

	// Then: S3 returns 204 and GetBucketCors reports the AWS not-found error
	helpers.AssertStatus(t, deleteResp, http.StatusNoContent)
	getResp, err := http.DefaultClient.Do(get(srv, "/delete-cors-bucket?cors"))
	if err != nil {
		t.Fatalf("GetBucketCors: %v", err)
	}
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusNotFound)
	assertS3ErrorCode(t, getResp, "NoSuchCORSConfiguration")
}

func TestDeleteBucketWebsite_removesConfiguration(t *testing.T) {
	// Given: a bucket with a website configuration
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "delete-website-bucket")
	websiteXML := `<WebsiteConfiguration><IndexDocument><Suffix>index.html</Suffix></IndexDocument></WebsiteConfiguration>`
	putResp, err := http.DefaultClient.Do(put(srv, "/delete-website-bucket?website", []byte(websiteXML), map[string]string{"Content-Type": "application/xml"}))
	if err != nil {
		t.Fatalf("PutBucketWebsite: %v", err)
	}
	putResp.Body.Close()
	helpers.AssertStatus(t, putResp, http.StatusOK)

	// When: DeleteBucketWebsite is called
	deleteResp, err := http.DefaultClient.Do(bucketConfigurationDeleteRequest(t, srv, "/delete-website-bucket?website"))
	if err != nil {
		t.Fatalf("DeleteBucketWebsite: %v", err)
	}
	defer deleteResp.Body.Close()

	// Then: S3 returns 204 and GetBucketWebsite reports the AWS not-found error
	helpers.AssertStatus(t, deleteResp, http.StatusNoContent)
	getResp, err := http.DefaultClient.Do(get(srv, "/delete-website-bucket?website"))
	if err != nil {
		t.Fatalf("GetBucketWebsite: %v", err)
	}
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusNotFound)
	assertS3ErrorCode(t, getResp, "NoSuchWebsiteConfiguration")
}

func TestPutBucketVersioning_invalidStatusReturnsMalformedXML(t *testing.T) {
	// Given: a bucket and a status outside the S3 API enum
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "invalid-versioning-bucket")
	body := `<VersioningConfiguration><Status>Invalid</Status></VersioningConfiguration>`

	// When: PutBucketVersioning is called
	resp, err := http.DefaultClient.Do(put(srv, "/invalid-versioning-bucket?versioning", []byte(body), map[string]string{"Content-Type": "application/xml"}))
	if err != nil {
		t.Fatalf("PutBucketVersioning: %v", err)
	}
	defer resp.Body.Close()

	// Then: S3 rejects it with its schema-validation error and does not persist it
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	errorBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read error body: %v", err)
	}
	if !strings.Contains(string(errorBody), "<Code>MalformedXML</Code>") ||
		!strings.Contains(string(errorBody), "did not validate against our published schema") {
		t.Fatalf("unexpected error body: %s", errorBody)
	}

	getResp, err := http.DefaultClient.Do(get(srv, "/invalid-versioning-bucket?versioning"))
	if err != nil {
		t.Fatalf("GetBucketVersioning: %v", err)
	}
	defer getResp.Body.Close()
	var result struct {
		Status string `xml:"Status"`
	}
	if err := xml.NewDecoder(getResp.Body).Decode(&result); err != nil {
		t.Fatalf("decode versioning response: %v", err)
	}
	if result.Status != "" {
		t.Fatalf("versioning status persisted after rejected request: %q", result.Status)
	}
}

func TestDeleteBucket_removesNotificationConfiguration(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "notification-recreate")
	notificationXML := `<NotificationConfiguration><QueueConfiguration><Queue>arn:aws:sqs:us-east-1:000000000000:events</Queue><Event>s3:ObjectCreated:*</Event></QueueConfiguration></NotificationConfiguration>`
	putResp, err := http.DefaultClient.Do(put(srv, "/notification-recreate?notification", []byte(notificationXML), map[string]string{"Content-Type": "application/xml"}))
	if err != nil {
		t.Fatalf("PutBucketNotificationConfiguration: %v", err)
	}
	putResp.Body.Close()
	helpers.AssertStatus(t, putResp, http.StatusOK)
	deleteResp, err := http.DefaultClient.Do(bucketConfigurationDeleteRequest(t, srv, "/notification-recreate"))
	if err != nil {
		t.Fatalf("DeleteBucket: %v", err)
	}
	deleteResp.Body.Close()
	helpers.AssertStatus(t, deleteResp, http.StatusNoContent)
	createBucket(t, srv, "notification-recreate")
	getResp, err := http.DefaultClient.Do(get(srv, "/notification-recreate?notification"))
	if err != nil {
		t.Fatalf("GetBucketNotificationConfiguration: %v", err)
	}
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusOK)
	body, err := io.ReadAll(getResp.Body)
	if err != nil {
		t.Fatalf("read notification response: %v", err)
	}
	if strings.Contains(string(body), "QueueConfiguration") {
		t.Fatalf("notification configuration survived bucket recreation: %s", body)
	}
}

func assertS3ErrorCode(t *testing.T, resp *http.Response, want string) {
	t.Helper()
	var result struct {
		Code string `xml:"Code"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode S3 error: %v", err)
	}
	if result.Code != want {
		t.Fatalf("S3 error code: got %q, want %q", result.Code, want)
	}
}

func bucketConfigurationDeleteRequest(t *testing.T, srv *helpers.TestServer, path string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("build DELETE %s: %v", path, err)
	}
	return req
}
