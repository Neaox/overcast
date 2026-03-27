// Package s3_test contains integration tests for the S3 service emulator.
//
// TDD contract: every test in this file must pass before the corresponding
// handler code in internal/services/s3/ is considered complete.
// New handlers MUST have a failing test written here first.
//
// Run: go test ./tests/integration/s3/...
// Run with race detector: go test -race ./tests/integration/s3/...
package s3_test

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/your-org/overcast/tests/helpers"
)

// ---- CreateBucket ----------------------------------------------------------

func TestCreateBucket_success(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp, err := http.DefaultClient.Do(put(srv, "/test-bucket", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertRequestID(t, resp)

	if loc := resp.Header.Get("Location"); loc != "/test-bucket" {
		t.Errorf("expected Location: /test-bucket, got %q", loc)
	}
}

func TestCreateBucket_alreadyExists(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "my-bucket")

	resp, err := http.DefaultClient.Do(put(srv, "/my-bucket", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusConflict)
	helpers.AssertXMLError(t, resp, "BucketAlreadyOwnedByYou")
}

func TestCreateBucket_nameTooShort(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp, err := http.DefaultClient.Do(put(srv, "/ab", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertXMLError(t, resp, "InvalidArgument")
}

// ---- HeadBucket ------------------------------------------------------------

func TestHeadBucket_exists(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "my-bucket")

	resp, err := http.DefaultClient.Do(head(srv, "/my-bucket"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestHeadBucket_notFound(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp, err := http.DefaultClient.Do(head(srv, "/no-such-bucket"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

// ---- DeleteBucket ----------------------------------------------------------

func TestDeleteBucket_empty(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "my-bucket")

	resp, err := http.DefaultClient.Do(del(srv, "/my-bucket"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusNoContent)
}

func TestDeleteBucket_notEmpty(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "my-bucket")
	putObject(t, srv, "my-bucket", "key1", []byte("data"), "text/plain")

	resp, err := http.DefaultClient.Do(del(srv, "/my-bucket"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusConflict)
	helpers.AssertXMLError(t, resp, "BucketNotEmpty")
}

func TestDeleteBucket_notFound(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp, err := http.DefaultClient.Do(del(srv, "/no-such-bucket"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertXMLError(t, resp, "NoSuchBucket")
}

// ---- PutObject -------------------------------------------------------------

func TestPutObject_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "my-bucket")

	body := []byte("hello world")
	resp, err := http.DefaultClient.Do(put(srv, "/my-bucket/hello.txt", body, map[string]string{
		"Content-Type": "text/plain",
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	if etag := resp.Header.Get("ETag"); etag == "" {
		t.Error("expected ETag header to be set")
	}
}

func TestPutObject_noBucket(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp, err := http.DefaultClient.Do(put(srv, "/no-bucket/key.txt", []byte("data"), nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertXMLError(t, resp, "NoSuchBucket")
}

func TestPutObject_withMetadata(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "my-bucket")

	resp, err := http.DefaultClient.Do(put(srv, "/my-bucket/obj.txt", []byte("data"), map[string]string{
		"Content-Type":       "text/plain",
		"X-Amz-Meta-Author":  "test",
		"X-Amz-Meta-Version": "1.0",
	}))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Verify metadata round-trips via GetObject.
	got, err := http.DefaultClient.Do(get(srv, "/my-bucket/obj.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer got.Body.Close()
	helpers.AssertStatus(t, got, http.StatusOK)

	if got.Header.Get("X-Amz-Meta-Author") != "test" {
		t.Errorf("expected X-Amz-Meta-Author: test, got %q", got.Header.Get("X-Amz-Meta-Author"))
	}
}

// ---- GetObject -------------------------------------------------------------

func TestGetObject_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "my-bucket")

	content := []byte("the quick brown fox")
	putObject(t, srv, "my-bucket", "fox.txt", content, "text/plain")

	resp, err := http.DefaultClient.Do(get(srv, "/my-bucket/fox.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	if ct := resp.Header.Get("Content-Type"); ct != "text/plain" {
		t.Errorf("expected Content-Type text/plain, got %q", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(body, content) {
		t.Errorf("expected body %q, got %q", content, body)
	}
}

func TestGetObject_notFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "my-bucket")

	resp, err := http.DefaultClient.Do(get(srv, "/my-bucket/no-such-key.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertXMLError(t, resp, "NoSuchKey")
}

// ---- HeadObject ------------------------------------------------------------

func TestHeadObject_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "my-bucket")
	putObject(t, srv, "my-bucket", "doc.pdf", []byte("pdf-data"), "application/pdf")

	resp, err := http.DefaultClient.Do(head(srv, "/my-bucket/doc.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	if ct := resp.Header.Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("expected Content-Type application/pdf, got %q", ct)
	}
	// HEAD must not return a body.
	body, _ := io.ReadAll(resp.Body)
	if len(body) > 0 {
		t.Errorf("HEAD response must have no body, got %d bytes", len(body))
	}
}

// ---- DeleteObject ----------------------------------------------------------

func TestDeleteObject_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "my-bucket")
	putObject(t, srv, "my-bucket", "gone.txt", []byte("bye"), "text/plain")

	resp, err := http.DefaultClient.Do(del(srv, "/my-bucket/gone.txt"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNoContent)

	// Verify object is gone.
	got, err := http.DefaultClient.Do(get(srv, "/my-bucket/gone.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer got.Body.Close()
	helpers.AssertStatus(t, got, http.StatusNotFound)
}

func TestDeleteObject_nonExistentIsIdempotent(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "my-bucket")

	// AWS DeleteObject is idempotent — deleting a non-existent key returns 204.
	resp, err := http.DefaultClient.Do(del(srv, "/my-bucket/does-not-exist.txt"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNoContent)
}

// ---- DeleteObjects (batch) -------------------------------------------------

func TestDeleteObjects_batchDelete(t *testing.T) {
	// Given three objects in a bucket
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "batch-bucket")
	putObject(t, srv, "batch-bucket", "a.txt", []byte("a"), "text/plain")
	putObject(t, srv, "batch-bucket", "b.txt", []byte("b"), "text/plain")
	putObject(t, srv, "batch-bucket", "c.txt", []byte("c"), "text/plain")

	// When we batch-delete a.txt and b.txt
	body := `<Delete><Object><Key>a.txt</Key></Object><Object><Key>b.txt</Key></Object></Delete>`
	req := mustReq(http.MethodPost, srv.URL+"/batch-bucket?delete", strings.NewReader(body), map[string]string{
		"Content-Type": "application/xml",
	})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then we get 200 with deleted keys in response
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And a.txt and b.txt are gone
	for _, key := range []string{"a.txt", "b.txt"} {
		got, err := http.DefaultClient.Do(get(srv, "/batch-bucket/"+key))
		if err != nil {
			t.Fatal(err)
		}
		got.Body.Close()
		helpers.AssertStatus(t, got, http.StatusNotFound)
	}

	// But c.txt still exists
	got, err := http.DefaultClient.Do(get(srv, "/batch-bucket/c.txt"))
	if err != nil {
		t.Fatal(err)
	}
	got.Body.Close()
	helpers.AssertStatus(t, got, http.StatusOK)
}

func TestDeleteObjects_quiet(t *testing.T) {
	// Given an object in a bucket
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "quiet-bucket")
	putObject(t, srv, "quiet-bucket", "x.txt", []byte("x"), "text/plain")

	// When we batch-delete with Quiet=true
	body := `<Delete><Quiet>true</Quiet><Object><Key>x.txt</Key></Object></Delete>`
	req := mustReq(http.MethodPost, srv.URL+"/quiet-bucket?delete", strings.NewReader(body), map[string]string{
		"Content-Type": "application/xml",
	})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then we get 200 and the object is gone
	helpers.AssertStatus(t, resp, http.StatusOK)

	got, err := http.DefaultClient.Do(get(srv, "/quiet-bucket/x.txt"))
	if err != nil {
		t.Fatal(err)
	}
	got.Body.Close()
	helpers.AssertStatus(t, got, http.StatusNotFound)
}

func TestDeleteObjects_bucketNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)

	body := `<Delete><Object><Key>x.txt</Key></Object></Delete>`
	req := mustReq(http.MethodPost, srv.URL+"/no-such-bucket?delete", strings.NewReader(body), map[string]string{
		"Content-Type": "application/xml",
	})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

// ---- ListObjectsV2 ---------------------------------------------------------

func TestListObjectsV2_empty(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "my-bucket")

	resp, err := http.DefaultClient.Do(get(srv, "/my-bucket?list-type=2"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		KeyCount int `xml:"KeyCount"`
	}
	helpers.DecodeXML(t, resp, &result)
	if result.KeyCount != 0 {
		t.Errorf("expected KeyCount 0, got %d", result.KeyCount)
	}
}

func TestListObjectsV2_withObjects(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "my-bucket")

	keys := []string{"a.txt", "b.txt", "c.txt"}
	for _, k := range keys {
		putObject(t, srv, "my-bucket", k, []byte("data"), "text/plain")
	}

	resp, err := http.DefaultClient.Do(get(srv, "/my-bucket?list-type=2"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		KeyCount int `xml:"KeyCount"`
		Contents []struct {
			Key string `xml:"Key"`
		} `xml:"Contents"`
	}
	helpers.DecodeXML(t, resp, &result)

	if result.KeyCount != len(keys) {
		t.Errorf("expected KeyCount %d, got %d", len(keys), result.KeyCount)
	}
}

func TestListObjectsV2_withPrefix(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "my-bucket")

	putObject(t, srv, "my-bucket", "logs/2024/jan.log", []byte("data"), "text/plain")
	putObject(t, srv, "my-bucket", "logs/2024/feb.log", []byte("data"), "text/plain")
	putObject(t, srv, "my-bucket", "images/photo.jpg", []byte("data"), "image/jpeg")

	resp, err := http.DefaultClient.Do(get(srv, "/my-bucket?list-type=2&prefix=logs/"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		KeyCount int `xml:"KeyCount"`
	}
	helpers.DecodeXML(t, resp, &result)

	if result.KeyCount != 2 {
		t.Errorf("expected 2 objects with prefix 'logs/', got %d", result.KeyCount)
	}
}

// ---- CopyObject ------------------------------------------------------------

func TestCopyObject_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "src-bucket")
	createBucket(t, srv, "dst-bucket")
	putObject(t, srv, "src-bucket", "original.txt", []byte("original content"), "text/plain")

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/dst-bucket/copy.txt", nil)
	req.Header.Set("x-amz-copy-source", "/src-bucket/original.txt")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Verify the copy is readable.
	got, err := http.DefaultClient.Do(get(srv, "/dst-bucket/copy.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer got.Body.Close()
	helpers.AssertStatus(t, got, http.StatusOK)
	body, _ := io.ReadAll(got.Body)
	if string(body) != "original content" {
		t.Errorf("expected 'original content', got %q", string(body))
	}
}

// ---- GetBucketLocation -----------------------------------------------------

func TestGetBucketLocation_success(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithRegion("eu-west-1"))
	createBucket(t, srv, "my-bucket")

	resp, err := http.DefaultClient.Do(get(srv, "/my-bucket?location"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		LocationConstraint string `xml:",chardata"`
	}
	helpers.DecodeXML(t, resp, &result)
	if result.LocationConstraint != "eu-west-1" {
		t.Errorf("expected location eu-west-1, got %q", result.LocationConstraint)
	}
}

func TestGetBucketLocation_notFound(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp, err := http.DefaultClient.Do(get(srv, "/no-such-bucket?location"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertXMLError(t, resp, "NoSuchBucket")
}

// ---- CopyObject (additional error paths) -----------------------------------

func TestCopyObject_missingSourceKey(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "src-bucket")
	createBucket(t, srv, "dst-bucket")

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/dst-bucket/copy.txt", nil)
	req.Header.Set("x-amz-copy-source", "/src-bucket/no-such-key")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertXMLError(t, resp, "NoSuchKey")
}

func TestCopyObject_invalidCopySource(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "dst-bucket")

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/dst-bucket/copy.txt", nil)
	req.Header.Set("x-amz-copy-source", "no-slash-at-all") // no bucket/key separator

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertXMLError(t, resp, "InvalidArgument")
}

// ---- HeadObject (bucket not found path) ------------------------------------

func TestHeadObject_bucketNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp, err := http.DefaultClient.Do(head(srv, "/no-such-bucket/key.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

// ---- DeleteObject (bucket not found path) ----------------------------------

func TestDeleteObject_bucketNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp, err := http.DefaultClient.Do(del(srv, "/no-such-bucket/key.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertXMLError(t, resp, "NoSuchBucket")
}

// ---- ListObjectsV2 with delimiter (folder simulation) ----------------------

func TestListObjectsV2_withDelimiter_collapsesPrefixes(t *testing.T) {
	// Given: a bucket with objects at different "folder" depths
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "my-bucket")
	putObject(t, srv, "my-bucket", "photos/2024/jan.jpg", []byte("data"), "image/jpeg")
	putObject(t, srv, "my-bucket", "photos/2024/feb.jpg", []byte("data"), "image/jpeg")
	putObject(t, srv, "my-bucket", "photos/2025/mar.jpg", []byte("data"), "image/jpeg")
	putObject(t, srv, "my-bucket", "docs/readme.txt", []byte("data"), "text/plain")
	putObject(t, srv, "my-bucket", "root.txt", []byte("data"), "text/plain")

	// When: we list with delimiter=/ and no prefix (root level)
	resp, err := http.DefaultClient.Do(get(srv, "/my-bucket?list-type=2&delimiter=%2F"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: we get root.txt as a Content, and photos/ + docs/ as CommonPrefixes
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Contents []struct {
			Key string `xml:"Key"`
		} `xml:"Contents"`
		CommonPrefixes []struct {
			Prefix string `xml:"Prefix"`
		} `xml:"CommonPrefixes"`
		Delimiter string `xml:"Delimiter"`
	}
	helpers.DecodeXML(t, resp, &result)

	if len(result.Contents) != 1 {
		t.Errorf("expected 1 Content at root, got %d: %v", len(result.Contents), result.Contents)
	}
	if result.Contents[0].Key != "root.txt" {
		t.Errorf("expected root.txt, got %q", result.Contents[0].Key)
	}

	if len(result.CommonPrefixes) != 2 {
		t.Errorf("expected 2 CommonPrefixes (docs/, photos/), got %d: %v", len(result.CommonPrefixes), result.CommonPrefixes)
	}

	prefixes := map[string]bool{}
	for _, cp := range result.CommonPrefixes {
		prefixes[cp.Prefix] = true
	}
	if !prefixes["docs/"] {
		t.Error("expected CommonPrefix docs/")
	}
	if !prefixes["photos/"] {
		t.Error("expected CommonPrefix photos/")
	}

	if result.Delimiter != "/" {
		t.Errorf("expected Delimiter '/', got %q", result.Delimiter)
	}
}

func TestListObjectsV2_withDelimiterAndPrefix_drillsIntoFolder(t *testing.T) {
	// Given: a bucket with objects nested under photos/
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "my-bucket")
	putObject(t, srv, "my-bucket", "photos/2024/jan.jpg", []byte("data"), "image/jpeg")
	putObject(t, srv, "my-bucket", "photos/2024/feb.jpg", []byte("data"), "image/jpeg")
	putObject(t, srv, "my-bucket", "photos/2025/mar.jpg", []byte("data"), "image/jpeg")
	putObject(t, srv, "my-bucket", "docs/readme.txt", []byte("data"), "text/plain")

	// When: we list with prefix=photos/ and delimiter=/
	resp, err := http.DefaultClient.Do(get(srv, "/my-bucket?list-type=2&prefix=photos%2F&delimiter=%2F"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: we get photos/2024/ and photos/2025/ as CommonPrefixes, no Contents
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Contents []struct {
			Key string `xml:"Key"`
		} `xml:"Contents"`
		CommonPrefixes []struct {
			Prefix string `xml:"Prefix"`
		} `xml:"CommonPrefixes"`
		Prefix string `xml:"Prefix"`
	}
	helpers.DecodeXML(t, resp, &result)

	if len(result.Contents) != 0 {
		t.Errorf("expected 0 Contents, got %d", len(result.Contents))
	}
	if len(result.CommonPrefixes) != 2 {
		t.Errorf("expected 2 CommonPrefixes (photos/2024/, photos/2025/), got %d: %v",
			len(result.CommonPrefixes), result.CommonPrefixes)
	}
	if result.Prefix != "photos/" {
		t.Errorf("expected Prefix 'photos/', got %q", result.Prefix)
	}
}

func TestListObjectsV2_withDelimiterAndPrefix_listsLeafObjects(t *testing.T) {
	// Given: objects inside photos/2024/
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "my-bucket")
	putObject(t, srv, "my-bucket", "photos/2024/jan.jpg", []byte("data"), "image/jpeg")
	putObject(t, srv, "my-bucket", "photos/2024/feb.jpg", []byte("data"), "image/jpeg")
	putObject(t, srv, "my-bucket", "photos/2025/mar.jpg", []byte("data"), "image/jpeg")

	// When: we list with prefix=photos/2024/ and delimiter=/
	resp, err := http.DefaultClient.Do(get(srv, "/my-bucket?list-type=2&prefix=photos%2F2024%2F&delimiter=%2F"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: we get jan.jpg and feb.jpg as Contents, no CommonPrefixes
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Contents []struct {
			Key string `xml:"Key"`
		} `xml:"Contents"`
		CommonPrefixes []struct {
			Prefix string `xml:"Prefix"`
		} `xml:"CommonPrefixes"`
		KeyCount int `xml:"KeyCount"`
	}
	helpers.DecodeXML(t, resp, &result)

	if len(result.Contents) != 2 {
		t.Errorf("expected 2 Contents (jan.jpg, feb.jpg), got %d", len(result.Contents))
	}
	if len(result.CommonPrefixes) != 0 {
		t.Errorf("expected 0 CommonPrefixes, got %d", len(result.CommonPrefixes))
	}
	if result.KeyCount != 2 {
		t.Errorf("expected KeyCount 2, got %d", result.KeyCount)
	}
}

// ---- ListObjectsV2 bucket not found ----------------------------------------

func TestListObjectsV2_bucketNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp, err := http.DefaultClient.Do(get(srv, "/no-such-bucket?list-type=2"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertXMLError(t, resp, "NoSuchBucket")
}

// ---- ListObjectsV2 pagination with continuation tokens ---------------------

func TestListObjectsV2_maxKeys_truncatesResult(t *testing.T) {
	// Given: a bucket with 5 objects
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "paging-bucket")
	for _, key := range []string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt"} {
		putObject(t, srv, "paging-bucket", key, []byte("x"), "text/plain")
	}

	// When: we list with max-keys=2
	resp, err := http.DefaultClient.Do(get(srv, "/paging-bucket?list-type=2&max-keys=2"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: we get exactly 2 objects, IsTruncated=true, and a NextContinuationToken
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Contents []struct {
			Key string `xml:"Key"`
		} `xml:"Contents"`
		IsTruncated           bool   `xml:"IsTruncated"`
		NextContinuationToken string `xml:"NextContinuationToken"`
		KeyCount              int    `xml:"KeyCount"`
	}
	helpers.DecodeXML(t, resp, &result)

	if len(result.Contents) != 2 {
		t.Errorf("expected 2 Contents, got %d: %v", len(result.Contents), result.Contents)
	}
	if result.Contents[0].Key != "a.txt" {
		t.Errorf("expected first key a.txt, got %q", result.Contents[0].Key)
	}
	if result.Contents[1].Key != "b.txt" {
		t.Errorf("expected second key b.txt, got %q", result.Contents[1].Key)
	}
	if !result.IsTruncated {
		t.Error("expected IsTruncated=true")
	}
	if result.NextContinuationToken == "" {
		t.Error("expected non-empty NextContinuationToken")
	}
	if result.KeyCount != 2 {
		t.Errorf("expected KeyCount=2, got %d", result.KeyCount)
	}
}

func TestListObjectsV2_continuationToken_resumesFromCorrectKey(t *testing.T) {
	// Given: a bucket with 5 objects
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "paging-bucket")
	for _, key := range []string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt"} {
		putObject(t, srv, "paging-bucket", key, []byte("x"), "text/plain")
	}

	// When: we fetch page 1 (max-keys=2),  then page 2 using the returned token
	resp1, err := http.DefaultClient.Do(get(srv, "/paging-bucket?list-type=2&max-keys=2"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp1.Body.Close()

	var page1 struct {
		Contents []struct {
			Key string `xml:"Key"`
		} `xml:"Contents"`
		NextContinuationToken string `xml:"NextContinuationToken"`
	}
	helpers.DecodeXML(t, resp1, &page1)

	token := page1.NextContinuationToken
	if token == "" {
		t.Fatal("expected a NextContinuationToken from page 1")
	}

	resp2, err := http.DefaultClient.Do(get(srv, "/paging-bucket?list-type=2&max-keys=2&continuation-token="+token))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	// Then: page 2 starts from c.txt
	helpers.AssertStatus(t, resp2, http.StatusOK)
	var page2 struct {
		Contents []struct {
			Key string `xml:"Key"`
		} `xml:"Contents"`
		IsTruncated           bool   `xml:"IsTruncated"`
		NextContinuationToken string `xml:"NextContinuationToken"`
	}
	helpers.DecodeXML(t, resp2, &page2)

	if len(page2.Contents) != 2 {
		t.Errorf("expected 2 Contents on page 2, got %d: %v", len(page2.Contents), page2.Contents)
	}
	if page2.Contents[0].Key != "c.txt" {
		t.Errorf("expected first key c.txt on page 2, got %q", page2.Contents[0].Key)
	}
	if page2.Contents[1].Key != "d.txt" {
		t.Errorf("expected second key d.txt on page 2, got %q", page2.Contents[1].Key)
	}
	if !page2.IsTruncated {
		t.Error("expected IsTruncated=true for page 2 (e.txt remains)")
	}
}

func TestListObjectsV2_continuationToken_lastPage_isNotTruncated(t *testing.T) {
	// Given: a bucket with 3 objects
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "paging-bucket")
	for _, key := range []string{"a.txt", "b.txt", "c.txt"} {
		putObject(t, srv, "paging-bucket", key, []byte("x"), "text/plain")
	}

	// When: we page through with max-keys=2 until the last page
	resp1, err := http.DefaultClient.Do(get(srv, "/paging-bucket?list-type=2&max-keys=2"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp1.Body.Close()
	var page1 struct {
		NextContinuationToken string `xml:"NextContinuationToken"`
	}
	helpers.DecodeXML(t, resp1, &page1)

	resp2, err := http.DefaultClient.Do(get(srv, "/paging-bucket?list-type=2&max-keys=2&continuation-token="+page1.NextContinuationToken))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	// Then: last page has 1 item, IsTruncated=false, no NextContinuationToken
	helpers.AssertStatus(t, resp2, http.StatusOK)
	var page2 struct {
		Contents []struct {
			Key string `xml:"Key"`
		} `xml:"Contents"`
		IsTruncated           bool   `xml:"IsTruncated"`
		NextContinuationToken string `xml:"NextContinuationToken"`
	}
	helpers.DecodeXML(t, resp2, &page2)

	if len(page2.Contents) != 1 {
		t.Errorf("expected 1 Content on last page, got %d: %v", len(page2.Contents), page2.Contents)
	}
	if page2.Contents[0].Key != "c.txt" {
		t.Errorf("expected c.txt on last page, got %q", page2.Contents[0].Key)
	}
	if page2.IsTruncated {
		t.Error("expected IsTruncated=false on last page")
	}
	if page2.NextContinuationToken != "" {
		t.Errorf("expected no NextContinuationToken on last page, got %q", page2.NextContinuationToken)
	}
}

// ---- Unimplemented operations return 501, not 404 --------------------------

func TestMultipartUpload_returns501(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "my-bucket")

	// POST to bucket initiates a multipart upload in real S3.
	resp, err := http.Post(srv.URL+"/my-bucket?uploads", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusNotImplemented)
	if got := resp.Header.Get("x-emulator-unsupported"); got != "true" {
		t.Errorf("expected x-emulator-unsupported: true header")
	}
}

// TestS3_UnimplementedOperations_return501 verifies that every S3 route that
// is not yet implemented returns HTTP 501 with x-emulator-unsupported: true.
// Each sub-test name identifies the AWS S3 operation being tested.
//
// The test pre-creates bucket "stub-bucket" and object "stub-bucket/stub-key"
// so that routes under /{bucket} and /{bucket}/{key} are not rejected with a
// 404 before reaching the stub handler.
func TestS3_UnimplementedOperations_return501(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "stub-bucket")
	putObject(t, srv, "stub-bucket", "stub-key", []byte("data"), "text/plain")

	type tc struct {
		name   string
		method string
		path   string // path + query string, no host
	}

	cases := []tc{
		// ---- Root -------------------------------------------------------
		{"ListDirectoryBuckets", http.MethodGet, "/?directory-buckets"},

		// ---- Bucket GET sub-resources -----------------------------------
		{"GetBucketAcl", http.MethodGet, "/stub-bucket?acl"},
		{"GetBucketCors", http.MethodGet, "/stub-bucket?cors"},
		{"GetBucketPolicy", http.MethodGet, "/stub-bucket?policy"},
		{"GetBucketPolicyStatus", http.MethodGet, "/stub-bucket?policyStatus"},
		{"GetBucketLifecycleConfiguration", http.MethodGet, "/stub-bucket?lifecycle"},
		{"GetBucketVersioning", http.MethodGet, "/stub-bucket?versioning"},
		{"GetBucketTagging", http.MethodGet, "/stub-bucket?tagging"},
		{"GetBucketWebsite", http.MethodGet, "/stub-bucket?website"},
		{"GetBucketLogging", http.MethodGet, "/stub-bucket?logging"},
		{"GetBucketReplication", http.MethodGet, "/stub-bucket?replication"},
		{"GetBucketEncryption", http.MethodGet, "/stub-bucket?encryption"},
		{"GetBucketAccelerateConfiguration", http.MethodGet, "/stub-bucket?accelerate"},
		{"GetBucketRequestPayment", http.MethodGet, "/stub-bucket?requestPayment"},
		{"GetBucketOwnershipControls", http.MethodGet, "/stub-bucket?ownershipControls"},
		{"GetPublicAccessBlock", http.MethodGet, "/stub-bucket?publicAccessBlock"},
		{"ListMultipartUploads", http.MethodGet, "/stub-bucket?uploads"},
		{"ListObjectVersions", http.MethodGet, "/stub-bucket?versions"},
		{"ListBucketAnalyticsConfigurations", http.MethodGet, "/stub-bucket?analytics"},
		{"ListBucketIntelligentTieringConfigurations", http.MethodGet, "/stub-bucket?intelligent-tiering"},
		{"ListBucketInventoryConfigurations", http.MethodGet, "/stub-bucket?inventory"},
		{"ListBucketMetricsConfigurations", http.MethodGet, "/stub-bucket?metrics"},
		{"GetObjectLockConfiguration", http.MethodGet, "/stub-bucket?object-lock"},
		{"GetBucketAbac", http.MethodGet, "/stub-bucket?abac"},
		{"GetBucketMetadataConfiguration", http.MethodGet, "/stub-bucket?metadata"},
		{"GetBucketMetadataTableConfiguration", http.MethodGet, "/stub-bucket?metadataTable"},
		{"CreateSession", http.MethodGet, "/stub-bucket?session"},

		// ---- Bucket PUT sub-resources -----------------------------------
		{"PutBucketAcl", http.MethodPut, "/stub-bucket?acl"},
		{"PutBucketCors", http.MethodPut, "/stub-bucket?cors"},
		{"PutBucketPolicy", http.MethodPut, "/stub-bucket?policy"},
		{"PutBucketLifecycleConfiguration", http.MethodPut, "/stub-bucket?lifecycle"},
		{"PutBucketVersioning", http.MethodPut, "/stub-bucket?versioning"},
		{"PutBucketTagging", http.MethodPut, "/stub-bucket?tagging"},
		{"PutBucketWebsite", http.MethodPut, "/stub-bucket?website"},
		{"PutBucketLogging", http.MethodPut, "/stub-bucket?logging"},
		{"PutBucketReplication", http.MethodPut, "/stub-bucket?replication"},
		{"PutBucketEncryption", http.MethodPut, "/stub-bucket?encryption"},
		{"PutBucketAccelerateConfiguration", http.MethodPut, "/stub-bucket?accelerate"},
		{"PutBucketRequestPayment", http.MethodPut, "/stub-bucket?requestPayment"},
		{"PutBucketOwnershipControls", http.MethodPut, "/stub-bucket?ownershipControls"},
		{"PutPublicAccessBlock", http.MethodPut, "/stub-bucket?publicAccessBlock"},
		{"PutBucketAnalyticsConfiguration", http.MethodPut, "/stub-bucket?analytics"},
		{"PutBucketIntelligentTieringConfiguration", http.MethodPut, "/stub-bucket?intelligent-tiering"},
		{"PutBucketInventoryConfiguration", http.MethodPut, "/stub-bucket?inventory"},
		{"PutBucketMetricsConfiguration", http.MethodPut, "/stub-bucket?metrics"},
		{"PutObjectLockConfiguration", http.MethodPut, "/stub-bucket?object-lock"},
		{"PutBucketAbac", http.MethodPut, "/stub-bucket?abac"},
		{"CreateBucketMetadataConfiguration", http.MethodPut, "/stub-bucket?metadata"},
		{"UpdateBucketMetadataTableConfiguration", http.MethodPut, "/stub-bucket?metadataTable"},

		// ---- Bucket DELETE sub-resources --------------------------------
		{"DeleteBucketCors", http.MethodDelete, "/stub-bucket?cors"},
		{"DeleteBucketPolicy", http.MethodDelete, "/stub-bucket?policy"},
		{"DeleteBucketLifecycle", http.MethodDelete, "/stub-bucket?lifecycle"},
		{"DeleteBucketTagging", http.MethodDelete, "/stub-bucket?tagging"},
		{"DeleteBucketWebsite", http.MethodDelete, "/stub-bucket?website"},
		{"DeleteBucketReplication", http.MethodDelete, "/stub-bucket?replication"},
		{"DeleteBucketEncryption", http.MethodDelete, "/stub-bucket?encryption"},
		{"DeleteBucketAnalyticsConfiguration", http.MethodDelete, "/stub-bucket?analytics"},
		{"DeleteBucketIntelligentTieringConfiguration", http.MethodDelete, "/stub-bucket?intelligent-tiering"},
		{"DeleteBucketInventoryConfiguration", http.MethodDelete, "/stub-bucket?inventory"},
		{"DeleteBucketMetricsConfiguration", http.MethodDelete, "/stub-bucket?metrics"},
		{"DeleteBucketOwnershipControls", http.MethodDelete, "/stub-bucket?ownershipControls"},
		{"DeletePublicAccessBlock", http.MethodDelete, "/stub-bucket?publicAccessBlock"},
		{"DeleteBucketMetadataConfiguration", http.MethodDelete, "/stub-bucket?metadata"},
		{"DeleteBucketMetadataTableConfiguration", http.MethodDelete, "/stub-bucket?metadataTable"},

		// ---- Bucket POST sub-resources ----------------------------------
		{"CreateBucketMetadataTableConfiguration", http.MethodPost, "/stub-bucket?metadataTable"},

		// ---- Object GET sub-resources -----------------------------------
		{"GetObjectAcl", http.MethodGet, "/stub-bucket/stub-key?acl"},
		{"GetObjectTagging", http.MethodGet, "/stub-bucket/stub-key?tagging"},
		{"GetObjectAttributes", http.MethodGet, "/stub-bucket/stub-key?attributes"},
		{"GetObjectLegalHold", http.MethodGet, "/stub-bucket/stub-key?legal-hold"},
		{"GetObjectRetention", http.MethodGet, "/stub-bucket/stub-key?retention"},
		{"GetObjectTorrent", http.MethodGet, "/stub-bucket/stub-key?torrent"},
		{"ListParts", http.MethodGet, "/stub-bucket/stub-key?uploadId=test-upload-id"},

		// ---- Object PUT sub-resources -----------------------------------
		{"PutObjectAcl", http.MethodPut, "/stub-bucket/stub-key?acl"},
		{"PutObjectTagging", http.MethodPut, "/stub-bucket/stub-key?tagging"},
		{"PutObjectLegalHold", http.MethodPut, "/stub-bucket/stub-key?legal-hold"},
		{"PutObjectRetention", http.MethodPut, "/stub-bucket/stub-key?retention"},
		{"RenameObject", http.MethodPut, "/stub-bucket/stub-key?rename"},
		{"UpdateObjectEncryption", http.MethodPut, "/stub-bucket/stub-key?encryption"},
		{"UploadPart", http.MethodPut, "/stub-bucket/stub-key?partNumber=1&uploadId=test-upload-id"},
		{"UploadPartCopy", http.MethodPut, "/stub-bucket/stub-key?partNumber=1"},

		// ---- Object DELETE sub-resources --------------------------------
		{"DeleteObjectTagging", http.MethodDelete, "/stub-bucket/stub-key?tagging"},
		{"AbortMultipartUpload", http.MethodDelete, "/stub-bucket/stub-key?uploadId=test-upload-id"},

		// ---- Object POST sub-resources ----------------------------------
		{"CreateMultipartUpload", http.MethodPost, "/stub-bucket/stub-key?uploads"},
		{"CompleteMultipartUpload", http.MethodPost, "/stub-bucket/stub-key?uploadId=test-upload-id"},
		{"RestoreObject", http.MethodPost, "/stub-bucket/stub-key?restore"},
		{"SelectObjectContent", http.MethodPost, "/stub-bucket/stub-key?select"},
		{"WriteGetObjectResponse", http.MethodPost, "/stub-bucket/stub-key?writeGetObjectResponse"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req, err := http.NewRequest(c.method, srv.URL+c.path, nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			// UploadPartCopy needs the copy-source header to route correctly.
			if c.name == "UploadPartCopy" {
				req.Header.Set("x-amz-copy-source", "/stub-bucket/stub-key")
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("do request: %v", err)
			}
			resp.Body.Close()

			helpers.AssertStatus(t, resp, http.StatusNotImplemented)
			if got := resp.Header.Get("x-emulator-unsupported"); got != "true" {
				t.Errorf("expected x-emulator-unsupported: true, got %q", got)
			}
		})
	}
}

// ---- ListBuckets -----------------------------------------------------------

func TestListBuckets_empty(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp, err := http.DefaultClient.Do(get(srv, "/"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertRequestID(t, resp)

	var result struct {
		Buckets struct {
			Bucket []struct{ Name string }
		}
	}
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode ListBucketsResult: %v", err)
	}
	if len(result.Buckets.Bucket) != 0 {
		t.Errorf("expected 0 buckets, got %d", len(result.Buckets.Bucket))
	}
}

func TestListBuckets_withBuckets(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "alpha")
	createBucket(t, srv, "beta")
	createBucket(t, srv, "gamma")

	resp, err := http.DefaultClient.Do(get(srv, "/"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		Buckets struct {
			Bucket []struct{ Name string }
		}
	}
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode ListBucketsResult: %v", err)
	}
	if got := len(result.Buckets.Bucket); got != 3 {
		t.Errorf("expected 3 buckets, got %d", got)
	}
	names := make(map[string]bool, 3)
	for _, b := range result.Buckets.Bucket {
		names[b.Name] = true
	}
	for _, want := range []string{"alpha", "beta", "gamma"} {
		if !names[want] {
			t.Errorf("bucket %q missing from ListBuckets response", want)
		}
	}
}

// ---- Request ID is present on every response --------------------------------

func TestEveryResponse_hasRequestID(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "my-bucket")

	cases := []*http.Request{
		mustReq(http.MethodPut, srv.URL+"/test-bucket2", nil, nil),
		mustReq(http.MethodHead, srv.URL+"/my-bucket", nil, nil),
		mustReq(http.MethodGet, srv.URL+"/my-bucket?list-type=2", nil, nil),
		mustReq(http.MethodGet, srv.URL+"/my-bucket/missing", nil, nil), // 404
	}

	for _, req := range cases {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		helpers.AssertRequestID(t, resp)
	}
}

// ---- Disk-backed body storage ----------------------------------------------

func TestPutObject_bodyStoredOnDisk(t *testing.T) {
	// Given a test server with a known data directory
	dataDir := t.TempDir()
	srv := helpers.NewTestServer(t, helpers.WithDataDir(dataDir))
	createBucket(t, srv, "my-bucket")

	// When we put an object with a known body
	content := []byte("disk-backed-body-content")
	putObject(t, srv, "my-bucket", "disk-test.txt", content, "text/plain")

	// Then GetObject returns the correct body
	resp, err := http.DefaultClient.Do(get(srv, "/my-bucket/disk-test.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	body, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(body, content) {
		t.Errorf("expected body %q, got %q", content, body)
	}

	// And a body file exists on disk under the data directory
	bodyFiles := findFiles(t, dataDir)
	if len(bodyFiles) == 0 {
		t.Fatal("expected at least one body file on disk under DataDir, found none")
	}
}

func TestDeleteObject_removesBodyFromDisk(t *testing.T) {
	// Given a test server with a known data directory
	dataDir := t.TempDir()
	srv := helpers.NewTestServer(t, helpers.WithDataDir(dataDir))
	createBucket(t, srv, "my-bucket")
	putObject(t, srv, "my-bucket", "to-delete.txt", []byte("ephemeral"), "text/plain")

	// Verify body file exists
	before := findFiles(t, dataDir)
	if len(before) == 0 {
		t.Fatal("expected body file on disk after put")
	}

	// When we delete the object
	resp, err := http.DefaultClient.Do(del(srv, "/my-bucket/to-delete.txt"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNoContent)

	// Then the body file is removed from disk
	after := findFiles(t, dataDir)
	if len(after) != 0 {
		t.Errorf("expected 0 body files after delete, found %d: %v", len(after), after)
	}
}

// findFiles returns all regular files under dir (recursively).
func findFiles(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("findFiles: %v", err)
	}
	return files
}

// ---- Test helpers ----------------------------------------------------------
// These are local to this package, not exported to other test packages.

func createBucket(t *testing.T, srv *helpers.TestServer, name string) {
	t.Helper()
	resp, err := http.DefaultClient.Do(put(srv, "/"+name, nil, nil))
	if err != nil {
		t.Fatalf("createBucket %q: %v", name, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("createBucket %q: unexpected status %d", name, resp.StatusCode)
	}
}

func putObject(t *testing.T, srv *helpers.TestServer, bucket, key string, body []byte, contentType string) {
	t.Helper()
	resp, err := http.DefaultClient.Do(put(srv, "/"+bucket+"/"+key, body, map[string]string{
		"Content-Type": contentType,
	}))
	if err != nil {
		t.Fatalf("putObject %q/%q: %v", bucket, key, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("putObject %q/%q: unexpected status %d", bucket, key, resp.StatusCode)
	}
}

// ---- BucketNotificationConfiguration --------------------------------------

// notifXML is the AWS-compatible XML body for PutBucketNotificationConfiguration.
// It registers one SQS queue for all ObjectCreated events on the "uploads/" prefix.
const notifXML = `<?xml version="1.0" encoding="UTF-8"?>
<NotificationConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <QueueConfiguration>
    <Id>test-queue-config</Id>
    <Queue>arn:aws:sqs:us-east-1:000000000000:my-queue</Queue>
    <Event>s3:ObjectCreated:*</Event>
    <Filter>
      <S3Key>
        <FilterRule>
          <Name>prefix</Name>
          <Value>uploads/</Value>
        </FilterRule>
      </S3Key>
    </Filter>
  </QueueConfiguration>
</NotificationConfiguration>`

func TestPutBucketNotificationConfiguration_success(t *testing.T) {
	// Given a bucket exists
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "notify-bucket")

	// When we configure notifications
	resp, err := http.DefaultClient.Do(
		put(srv, "/notify-bucket?notification", []byte(notifXML), map[string]string{
			"Content-Type": "application/xml",
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then the response is 200 with no body
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertRequestID(t, resp)
}

func TestPutBucketNotificationConfiguration_bucketNotFound(t *testing.T) {
	// Given no bucket exists
	srv := helpers.NewTestServer(t)

	// When we configure notifications on a missing bucket
	resp, err := http.DefaultClient.Do(
		put(srv, "/missing-bucket?notification", []byte(notifXML), map[string]string{
			"Content-Type": "application/xml",
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then we get 404 NoSuchBucket
	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertXMLError(t, resp, "NoSuchBucket")
}

func TestGetBucketNotificationConfiguration_empty(t *testing.T) {
	// Given a bucket with no notification config
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "quiet-bucket")

	// When we get the notification config
	resp, err := http.DefaultClient.Do(get(srv, "/quiet-bucket?notification"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then we get an empty NotificationConfiguration
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertRequestID(t, resp)

	var cfg struct {
		XMLName xml.Name `xml:"NotificationConfiguration"`
	}
	body, _ := io.ReadAll(resp.Body)
	if err := xml.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("expected valid XML, got %q: %v", body, err)
	}
}

func TestGetBucketNotificationConfiguration_roundtrip(t *testing.T) {
	// Given a bucket with a saved notification config
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "roundtrip-bucket")

	putResp, err := http.DefaultClient.Do(
		put(srv, "/roundtrip-bucket?notification", []byte(notifXML), map[string]string{
			"Content-Type": "application/xml",
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	putResp.Body.Close()
	helpers.AssertStatus(t, putResp, http.StatusOK)

	// When we get the config back
	resp, err := http.DefaultClient.Do(get(srv, "/roundtrip-bucket?notification"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then the queue ARN and event type are preserved
	helpers.AssertStatus(t, resp, http.StatusOK)

	type FilterRule struct {
		Name  string `xml:"Name"`
		Value string `xml:"Value"`
	}
	type QueueConfig struct {
		ID     string       `xml:"Id"`
		Queue  string       `xml:"Queue"`
		Events []string     `xml:"Event"`
		Rules  []FilterRule `xml:"Filter>S3Key>FilterRule"`
	}
	var cfg struct {
		XMLName      xml.Name      `xml:"NotificationConfiguration"`
		QueueConfigs []QueueConfig `xml:"QueueConfiguration"`
	}
	body, _ := io.ReadAll(resp.Body)
	if err := xml.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("expected valid XML, got %q: %v", body, err)
	}

	if len(cfg.QueueConfigs) != 1 {
		t.Fatalf("expected 1 QueueConfiguration, got %d", len(cfg.QueueConfigs))
	}
	qc := cfg.QueueConfigs[0]
	if qc.ID != "test-queue-config" {
		t.Errorf("expected Id %q, got %q", "test-queue-config", qc.ID)
	}
	if qc.Queue != "arn:aws:sqs:us-east-1:000000000000:my-queue" {
		t.Errorf("unexpected Queue ARN: %q", qc.Queue)
	}
	if len(qc.Events) != 1 || qc.Events[0] != "s3:ObjectCreated:*" {
		t.Errorf("unexpected Events: %v", qc.Events)
	}
	if len(qc.Rules) != 1 || qc.Rules[0].Name != "prefix" || qc.Rules[0].Value != "uploads/" {
		t.Errorf("unexpected filter rules: %v", qc.Rules)
	}
}

func TestGetBucketNotificationConfiguration_bucketNotFound(t *testing.T) {
	// Given no bucket exists
	srv := helpers.NewTestServer(t)

	// When we get notifications for a missing bucket
	resp, err := http.DefaultClient.Do(get(srv, "/missing-bucket?notification"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then we get 404 NoSuchBucket
	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertXMLError(t, resp, "NoSuchBucket")
}

func TestPutBucketNotificationConfiguration_replace(t *testing.T) {
	// Given a bucket with an existing notification config
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "replace-bucket")

	firstPut, _ := http.DefaultClient.Do(
		put(srv, "/replace-bucket?notification", []byte(notifXML), map[string]string{
			"Content-Type": "application/xml",
		}),
	)
	firstPut.Body.Close()

	// When we PUT an empty NotificationConfiguration (clear all)
	emptyXML := `<NotificationConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"></NotificationConfiguration>`
	clearResp, err := http.DefaultClient.Do(
		put(srv, "/replace-bucket?notification", []byte(emptyXML), map[string]string{
			"Content-Type": "application/xml",
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	clearResp.Body.Close()
	helpers.AssertStatus(t, clearResp, http.StatusOK)

	// Then getting the config returns empty
	getResp, err := http.DefaultClient.Do(get(srv, "/replace-bucket?notification"))
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()

	var cfg struct {
		XMLName      xml.Name `xml:"NotificationConfiguration"`
		QueueConfigs []struct {
			ID string `xml:"Id"`
		} `xml:"QueueConfiguration"`
	}
	body, _ := io.ReadAll(getResp.Body)
	if err := xml.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("expected valid XML after clear, got %q: %v", body, err)
	}
	if len(cfg.QueueConfigs) != 0 {
		t.Errorf("expected 0 QueueConfigurations after clear, got %d", len(cfg.QueueConfigs))
	}
}

func put(srv *helpers.TestServer, path string, body []byte, headers map[string]string) *http.Request {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, _ := http.NewRequest(http.MethodPut, srv.URL+path, bodyReader)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

func get(srv *helpers.TestServer, path string) *http.Request {
	req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	return req
}

func head(srv *helpers.TestServer, path string) *http.Request {
	req, _ := http.NewRequest(http.MethodHead, srv.URL+path, nil)
	return req
}

func del(srv *helpers.TestServer, path string) *http.Request {
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+path, nil)
	return req
}

func mustReq(method, url string, body io.Reader, headers map[string]string) *http.Request {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		panic(fmt.Sprintf("mustReq: %v", err))
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

// Ensure xml package is used (for helpers.DecodeXML result types above).
var _ = xml.Unmarshal
var _ = strings.Contains

// ---- S3 Event Notifications → SQS ------------------------------------------

// sqsCall is a minimal SQS JSON helper for cross-service notification tests.
func sqsCall(t *testing.T, srv *helpers.TestServer, action string, body map[string]interface{}) *http.Response {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS."+action)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sqsCall %s: %v", action, err)
	}
	return resp
}

func TestPutObject_sendsNotificationToSQS(t *testing.T) {
	// Given an SQS queue and an S3 bucket with notifications pointing to it
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "event-bucket")

	// Create the SQS queue
	createResp := sqsCall(t, srv, "CreateQueue", map[string]interface{}{
		"QueueName": "event-queue",
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	var qResult struct {
		QueueUrl string `json:"QueueUrl"`
	}
	helpers.DecodeJSON(t, createResp, &qResult)

	// Configure notifications: ObjectCreated → SQS queue
	notifXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<NotificationConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <QueueConfiguration>
    <Id>put-to-queue</Id>
    <Queue>arn:aws:sqs:us-east-1:000000000000:event-queue</Queue>
    <Event>s3:ObjectCreated:*</Event>
  </QueueConfiguration>
</NotificationConfiguration>`)

	putNCResp, _ := http.DefaultClient.Do(
		put(srv, "/event-bucket?notification", []byte(notifXML), map[string]string{"Content-Type": "application/xml"}),
	)
	putNCResp.Body.Close()
	helpers.AssertStatus(t, putNCResp, http.StatusOK)

	// When we upload an object
	putObjResp, _ := http.DefaultClient.Do(
		put(srv, "/event-bucket/docs/hello.txt", []byte("hello world"), map[string]string{"Content-Type": "text/plain"}),
	)
	putObjResp.Body.Close()
	helpers.AssertStatus(t, putObjResp, http.StatusOK)

	// Then a notification message appears in the SQS queue (async — small retry window)
	var msgBody string
	for i := 0; i < 20; i++ {
		recvResp := sqsCall(t, srv, "ReceiveMessage", map[string]interface{}{
			"QueueUrl":            qResult.QueueUrl,
			"MaxNumberOfMessages": 1,
		})
		var recvResult struct {
			Messages []struct {
				Body string `json:"Body"`
			} `json:"Messages"`
		}
		helpers.DecodeJSON(t, recvResp, &recvResult)
		recvResp.Body.Close()
		if len(recvResult.Messages) > 0 {
			msgBody = recvResult.Messages[0].Body
			break
		}
		// Bus is async — give it a moment
		time.Sleep(5 * time.Millisecond)
	}

	if msgBody == "" {
		t.Fatal("expected a notification message in SQS, got none after retries")
	}

	// The message body should be S3 notification event JSON
	var eventEnvelope struct {
		Records []struct {
			EventName string `json:"eventName"`
			S3        struct {
				Bucket struct {
					Name string `json:"name"`
				} `json:"bucket"`
				Object struct {
					Key  string `json:"key"`
					Size int64  `json:"size"`
				} `json:"object"`
			} `json:"s3"`
		} `json:"Records"`
	}
	if err := json.Unmarshal([]byte(msgBody), &eventEnvelope); err != nil {
		t.Fatalf("expected valid JSON event, got %q: %v", msgBody, err)
	}
	if len(eventEnvelope.Records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(eventEnvelope.Records))
	}
	rec := eventEnvelope.Records[0]
	if rec.EventName != "ObjectCreated:Put" {
		t.Errorf("expected eventName ObjectCreated:Put, got %q", rec.EventName)
	}
	if rec.S3.Bucket.Name != "event-bucket" {
		t.Errorf("expected bucket event-bucket, got %q", rec.S3.Bucket.Name)
	}
	if rec.S3.Object.Key != "docs/hello.txt" {
		t.Errorf("expected key docs/hello.txt, got %q", rec.S3.Object.Key)
	}
	if rec.S3.Object.Size != 11 {
		t.Errorf("expected size 11, got %d", rec.S3.Object.Size)
	}
}

func TestDeleteObject_sendsNotificationToSQS(t *testing.T) {
	// Given an SQS queue and an S3 bucket with ObjectRemoved notifications
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "del-bucket")

	createResp := sqsCall(t, srv, "CreateQueue", map[string]interface{}{
		"QueueName": "del-queue",
	})
	defer createResp.Body.Close()
	var qResult struct {
		QueueUrl string `json:"QueueUrl"`
	}
	helpers.DecodeJSON(t, createResp, &qResult)

	notifXML := `<?xml version="1.0" encoding="UTF-8"?>
<NotificationConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <QueueConfiguration>
    <Id>del-config</Id>
    <Queue>arn:aws:sqs:us-east-1:000000000000:del-queue</Queue>
    <Event>s3:ObjectRemoved:*</Event>
  </QueueConfiguration>
</NotificationConfiguration>`

	putNCResp, _ := http.DefaultClient.Do(
		put(srv, "/del-bucket?notification", []byte(notifXML), map[string]string{"Content-Type": "application/xml"}),
	)
	putNCResp.Body.Close()

	// Put then delete an object
	putObjResp, _ := http.DefaultClient.Do(
		put(srv, "/del-bucket/remove-me.txt", []byte("bye"), map[string]string{"Content-Type": "text/plain"}),
	)
	putObjResp.Body.Close()

	// Drain any ObjectCreated messages (not subscribed but just in case)
	time.Sleep(10 * time.Millisecond)

	// When we delete the object
	delResp, _ := http.DefaultClient.Do(del(srv, "/del-bucket/remove-me.txt"))
	delResp.Body.Close()
	helpers.AssertStatus(t, delResp, http.StatusNoContent)

	// Then a notification appears
	var msgBody string
	for i := 0; i < 20; i++ {
		recvResp := sqsCall(t, srv, "ReceiveMessage", map[string]interface{}{
			"QueueUrl":            qResult.QueueUrl,
			"MaxNumberOfMessages": 1,
		})
		var recvResult struct {
			Messages []struct {
				Body string `json:"Body"`
			} `json:"Messages"`
		}
		helpers.DecodeJSON(t, recvResp, &recvResult)
		recvResp.Body.Close()
		if len(recvResult.Messages) > 0 {
			msgBody = recvResult.Messages[0].Body
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if msgBody == "" {
		t.Fatal("expected a delete notification message in SQS, got none")
	}

	var eventEnvelope struct {
		Records []struct {
			EventName string `json:"eventName"`
			S3        struct {
				Object struct {
					Key string `json:"key"`
				} `json:"object"`
			} `json:"s3"`
		} `json:"Records"`
	}
	if err := json.Unmarshal([]byte(msgBody), &eventEnvelope); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(eventEnvelope.Records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(eventEnvelope.Records))
	}
	if eventEnvelope.Records[0].EventName != "ObjectRemoved:Delete" {
		t.Errorf("expected ObjectRemoved:Delete, got %q", eventEnvelope.Records[0].EventName)
	}
}

func TestPutObject_notificationFilteredByPrefix(t *testing.T) {
	// Given a notification config with prefix filter "uploads/"
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "filter-bucket")

	createResp := sqsCall(t, srv, "CreateQueue", map[string]interface{}{
		"QueueName": "filter-queue",
	})
	defer createResp.Body.Close()
	var qResult struct {
		QueueUrl string `json:"QueueUrl"`
	}
	helpers.DecodeJSON(t, createResp, &qResult)

	notifXML := `<?xml version="1.0" encoding="UTF-8"?>
<NotificationConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <QueueConfiguration>
    <Id>filter-config</Id>
    <Queue>arn:aws:sqs:us-east-1:000000000000:filter-queue</Queue>
    <Event>s3:ObjectCreated:*</Event>
    <Filter>
      <S3Key>
        <FilterRule><Name>prefix</Name><Value>uploads/</Value></FilterRule>
      </S3Key>
    </Filter>
  </QueueConfiguration>
</NotificationConfiguration>`

	putNCResp, _ := http.DefaultClient.Do(
		put(srv, "/filter-bucket?notification", []byte(notifXML), map[string]string{"Content-Type": "application/xml"}),
	)
	putNCResp.Body.Close()

	// When we upload an object that does NOT match the prefix
	notMatchResp, _ := http.DefaultClient.Do(
		put(srv, "/filter-bucket/other/file.txt", []byte("nope"), map[string]string{"Content-Type": "text/plain"}),
	)
	notMatchResp.Body.Close()

	time.Sleep(20 * time.Millisecond)

	// Then no message in queue
	recvResp := sqsCall(t, srv, "ReceiveMessage", map[string]interface{}{
		"QueueUrl":            qResult.QueueUrl,
		"MaxNumberOfMessages": 1,
	})
	var recvResult struct {
		Messages []struct{ Body string } `json:"Messages"`
	}
	helpers.DecodeJSON(t, recvResp, &recvResult)
	recvResp.Body.Close()
	if len(recvResult.Messages) != 0 {
		t.Errorf("expected no messages for non-matching prefix, got %d", len(recvResult.Messages))
	}

	// When we upload an object that DOES match the prefix
	matchResp, _ := http.DefaultClient.Do(
		put(srv, "/filter-bucket/uploads/doc.txt", []byte("yes"), map[string]string{"Content-Type": "text/plain"}),
	)
	matchResp.Body.Close()

	var msgBody string
	for i := 0; i < 20; i++ {
		recvResp := sqsCall(t, srv, "ReceiveMessage", map[string]interface{}{
			"QueueUrl":            qResult.QueueUrl,
			"MaxNumberOfMessages": 1,
		})
		var r2 struct {
			Messages []struct{ Body string } `json:"Messages"`
		}
		helpers.DecodeJSON(t, recvResp, &r2)
		recvResp.Body.Close()
		if len(r2.Messages) > 0 {
			msgBody = r2.Messages[0].Body
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if msgBody == "" {
		t.Fatal("expected notification for uploads/doc.txt, got none")
	}
}
