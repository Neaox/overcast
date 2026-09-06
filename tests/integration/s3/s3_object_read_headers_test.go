package s3_test

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// This file covers the two response-writing defects on the object read path:
//
//   - #1704 — HeadObject dropped the x-amz-meta-* user metadata GetObject
//     returns, so the two verbs disagreed about the same object.
//   - #1705 — an unsatisfiable Range answered 416 with the whole object's
//     Content-Length and no body, which is a malformed HTTP message.
//
// Both are asserted at the wire, because both are about bytes on the wire. The
// SDK-client half lives in s3_sdk_object_read_test.go.

// ---- #1704: GET and HEAD must agree ----------------------------------------

// volatileReadHeaders are the response headers that legitimately differ between
// two responses to the same object, so comparing GET against HEAD ignores them.
// Everything else is part of the contract RFC 9110 §9.3.2 states between the
// two verbs.
var volatileReadHeaders = map[string]bool{
	"X-Amz-Request-Id": true,
	"Date":             true,
}

func TestHeadObject_headersMatchGetObject(t *testing.T) {
	// Given: objects written by each of the three paths that produce one, each
	// carrying user metadata and the stored response headers
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "meta-bucket")
	putObjectWithHeaders(t, srv, "meta-bucket", "put.txt", []byte("hello"), map[string]string{
		"Content-Type":        "text/plain",
		"Content-Disposition": `attachment; filename="put.txt"`,
		"Content-Language":    "en-GB",
		"Cache-Control":       "max-age=60",
		"x-amz-meta-foo":      "bar",
		"x-amz-meta-Mixed":    "Case-Value",
	})
	copyObject(t, srv, "meta-bucket", "put.txt", "meta-bucket", "copied.txt")
	completeMultipart(t, srv, "meta-bucket", "multipart.txt", []byte("multipart body"), map[string]string{
		"x-amz-meta-foo": "bar",
	})

	for _, key := range []string{"put.txt", "copied.txt", "multipart.txt"} {
		t.Run(key, func(t *testing.T) {
			// When: we GET and HEAD the same object
			getResp, err := http.DefaultClient.Do(get(srv, "/meta-bucket/"+key))
			if err != nil {
				t.Fatal(err)
			}
			defer getResp.Body.Close()
			headResp, err := http.DefaultClient.Do(head(srv, "/meta-bucket/"+key))
			if err != nil {
				t.Fatal(err)
			}
			defer headResp.Body.Close()

			// Then: both succeed with byte-identical header sets
			helpers.AssertStatus(t, getResp, http.StatusOK)
			helpers.AssertStatus(t, headResp, http.StatusOK)
			helpers.AssertRequestID(t, headResp)
			assertHeadersEqual(t, getResp, headResp)

			// And: the user metadata really is there, rather than both being empty
			if got := headResp.Header.Get("x-amz-meta-foo"); got != "bar" {
				t.Errorf("HEAD x-amz-meta-foo: expected %q, got %q", "bar", got)
			}
		})
	}
}

func TestHeadObject_versionedReadCarriesMetadata(t *testing.T) {
	// Given: a versioned bucket holding two versions of a key with different
	// metadata
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "versioned-meta")
	enableVersioning(t, srv, "versioned-meta")
	putObjectWithHeaders(t, srv, "versioned-meta", "doc.txt", []byte("v1"), map[string]string{
		"Content-Type":   "text/plain",
		"x-amz-meta-gen": "one",
	})
	firstVersion := headVersionID(t, srv, "versioned-meta", "doc.txt")
	putObjectWithHeaders(t, srv, "versioned-meta", "doc.txt", []byte("v2"), map[string]string{
		"Content-Type":   "text/plain",
		"x-amz-meta-gen": "two",
	})

	// When: we read the older version by id, with both verbs
	path := "/versioned-meta/doc.txt?versionId=" + firstVersion
	getResp, err := http.DefaultClient.Do(get(srv, path))
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()
	headResp, err := http.DefaultClient.Do(head(srv, path))
	if err != nil {
		t.Fatal(err)
	}
	defer headResp.Body.Close()

	// Then: both carry that version's metadata, and agree on every header
	helpers.AssertStatus(t, getResp, http.StatusOK)
	helpers.AssertStatus(t, headResp, http.StatusOK)
	if got := headResp.Header.Get("x-amz-meta-gen"); got != "one" {
		t.Errorf("HEAD ?versionId= x-amz-meta-gen: expected %q, got %q", "one", got)
	}
	assertHeadersEqual(t, getResp, headResp)
}

// ---- #1705: an unsatisfiable Range ----------------------------------------

func TestGetObject_unsatisfiableRangeIsAWellFramedInvalidRange(t *testing.T) {
	// Given: a ten-byte object
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "range-bucket")
	putObject(t, srv, "range-bucket", "r.txt", []byte("0123456789"), "text/plain")

	// When: we ask for a range entirely beyond it
	req := get(srv, "/range-bucket/r.txt")
	req.Header.Set("Range", "bytes=500-600")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: 416 with the InvalidRange document AWS sends, and framing that
	// matches the bytes actually written
	helpers.AssertStatus(t, resp, http.StatusRequestedRangeNotSatisfiable)
	helpers.AssertRequestID(t, resp)
	if cr := resp.Header.Get("Content-Range"); cr != "bytes */10" {
		t.Errorf("expected Content-Range %q, got %q", "bytes */10", cr)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the 416 body failed, which is the bug: %v", err)
	}
	assertContentLengthMatchesBody(t, resp, body)

	var errResp struct {
		XMLName xml.Name `xml:"Error"`
		Code    string   `xml:"Code"`
		Message string   `xml:"Message"`
	}
	if err := xml.Unmarshal(body, &errResp); err != nil {
		t.Fatalf("416 body is not an <Error> document: %v (body: %q)", err, body)
	}
	if errResp.Code != "InvalidRange" {
		t.Errorf("expected error code %q, got %q", "InvalidRange", errResp.Code)
	}
}

func TestGetObject_rangeOutcomes(t *testing.T) {
	// Given: a ten-byte object and an empty one
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "range-cases")
	putObject(t, srv, "range-cases", "ten.txt", []byte("0123456789"), "text/plain")
	putObject(t, srv, "range-cases", "empty.txt", nil, "text/plain")

	cases := []struct {
		name       string
		key        string
		rangeValue string
		status     int
		body       string
	}{
		// A range that starts inside the object truncates to its end.
		{name: "partially satisfiable", key: "ten.txt", rangeValue: "bytes=5-100", status: http.StatusPartialContent, body: "56789"},
		{name: "start beyond end", key: "ten.txt", rangeValue: "bytes=500-600", status: http.StatusRequestedRangeNotSatisfiable},
		{name: "first byte is the object length", key: "ten.txt", rangeValue: "bytes=10-", status: http.StatusRequestedRangeNotSatisfiable},
		// A suffix longer than the object is the whole object, not an error.
		{name: "suffix longer than object", key: "ten.txt", rangeValue: "bytes=-500", status: http.StatusPartialContent, body: "0123456789"},
		{name: "zero-length suffix", key: "ten.txt", rangeValue: "bytes=-0", status: http.StatusRequestedRangeNotSatisfiable},
		// AWS ignores a Range it cannot parse and answers with the whole
		// object — verified against real S3 in localstack/localstack#9076,
		// where `bytes=1-0` and `bytes=15-1` both return the full body.
		{name: "unparseable", key: "ten.txt", rangeValue: "bytes=abc", status: http.StatusOK, body: "0123456789"},
		{name: "inverted", key: "ten.txt", rangeValue: "bytes=5-1", status: http.StatusOK, body: "0123456789"},
		{name: "unknown range unit", key: "ten.txt", rangeValue: "items=0-1", status: http.StatusOK, body: "0123456789"},
		// Nothing overlaps a zero-length representation.
		{name: "range on an empty object", key: "empty.txt", rangeValue: "bytes=0-1", status: http.StatusRequestedRangeNotSatisfiable},
		{name: "suffix on an empty object", key: "empty.txt", rangeValue: "bytes=-5", status: http.StatusRequestedRangeNotSatisfiable},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// When: we GET with that Range header
			req := get(srv, "/range-cases/"+tc.key)
			req.Header.Set("Range", tc.rangeValue)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			// Then: the status and body are AWS's, and the framing is honest
			helpers.AssertStatus(t, resp, tc.status)
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("reading the body failed: %v", err)
			}
			assertContentLengthMatchesBody(t, resp, body)
			if tc.status != http.StatusRequestedRangeNotSatisfiable && string(body) != tc.body {
				t.Errorf("expected body %q, got %q", tc.body, body)
			}
		})
	}
}

func TestHeadObject_unsatisfiableRangeSendsNoBody(t *testing.T) {
	// Given: a ten-byte object
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "head-range")
	putObject(t, srv, "head-range", "r.txt", []byte("0123456789"), "text/plain")

	// When: we HEAD it with a range beyond its end
	req := head(srv, "/head-range/r.txt")
	req.Header.Set("Range", "bytes=500-600")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: the same 416 answer as GET, with no body at all
	helpers.AssertStatus(t, resp, http.StatusRequestedRangeNotSatisfiable)
	helpers.AssertRequestID(t, resp)
	if cr := resp.Header.Get("Content-Range"); cr != "bytes */10" {
		t.Errorf("expected Content-Range %q, got %q", "bytes */10", cr)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the HEAD 416 body failed: %v", err)
	}
	if len(body) != 0 {
		t.Errorf("HEAD response must have no body, got %d bytes", len(body))
	}
}

func TestHeadObject_satisfiableRangeIsPartialContent(t *testing.T) {
	// Given: a ten-byte object
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "head-range-ok")
	putObject(t, srv, "head-range-ok", "r.txt", []byte("0123456789"), "text/plain")

	// When: we HEAD it with a satisfiable range
	req := head(srv, "/head-range-ok/r.txt")
	req.Header.Set("Range", "bytes=2-5")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: 206 describing the range, and still no body
	helpers.AssertStatus(t, resp, http.StatusPartialContent)
	if cr := resp.Header.Get("Content-Range"); cr != "bytes 2-5/10" {
		t.Errorf("expected Content-Range %q, got %q", "bytes 2-5/10", cr)
	}
	if cl := resp.Header.Get("Content-Length"); cl != "4" {
		t.Errorf("expected Content-Length %q, got %q", "4", cl)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) != 0 {
		t.Errorf("HEAD response must have no body, got %d bytes", len(body))
	}
}

func TestGetObject_keepAliveSurvivesA416(t *testing.T) {
	// Given: a ten-byte object, and one warmed connection in the pool
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "keepalive")
	putObject(t, srv, "keepalive", "r.txt", []byte("0123456789"), "text/plain")
	warm, err := http.DefaultClient.Do(get(srv, "/keepalive/r.txt"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, warm.Body)
	warm.Body.Close()

	// When: an unsatisfiable range is answered on that connection
	req := get(srv, "/keepalive/r.txt")
	req.Header.Set("Range", "bytes=500-600")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("draining the 416 desynchronised the connection: %v", err)
	}
	resp.Body.Close()

	// Then: the next request reuses that same connection and succeeds
	var reused bool
	next := get(srv, "/keepalive/r.txt")
	next = next.WithContext(httptrace.WithClientTrace(next.Context(), &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused },
	}))
	nextResp, err := http.DefaultClient.Do(next)
	if err != nil {
		t.Fatalf("the request after a 416 failed: %v", err)
	}
	defer nextResp.Body.Close()
	helpers.AssertStatus(t, nextResp, http.StatusOK)
	body, err := io.ReadAll(nextResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "0123456789" {
		t.Errorf("expected the whole object back, got %q", body)
	}
	if !reused {
		t.Error("the request after a 416 opened a new connection, so keep-alive was not proven safe")
	}
}

// ---- Test helpers ----------------------------------------------------------

// assertHeadersEqual compares two responses' header sets, ignoring the ones
// that legitimately differ per response.
func assertHeadersEqual(t *testing.T, want, got *http.Response) {
	t.Helper()
	names := map[string]bool{}
	for name := range want.Header {
		names[name] = true
	}
	for name := range got.Header {
		names[name] = true
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		if !volatileReadHeaders[name] {
			sorted = append(sorted, name)
		}
	}
	sort.Strings(sorted)
	for _, name := range sorted {
		wantValue := strings.Join(want.Header.Values(name), ", ")
		gotValue := strings.Join(got.Header.Values(name), ", ")
		if wantValue != gotValue {
			t.Errorf("header %q: GET has %q, HEAD has %q", name, wantValue, gotValue)
		}
	}
}

// assertContentLengthMatchesBody is the framing check: a declared length that
// exceeds the bytes actually sent is a malformed HTTP message, and is what
// breaks an SDK client's connection.
func assertContentLengthMatchesBody(t *testing.T, resp *http.Response, body []byte) {
	t.Helper()
	declared := resp.Header.Get("Content-Length")
	if declared == "" {
		t.Error("response has no Content-Length")
		return
	}
	n, err := strconv.Atoi(declared)
	if err != nil {
		t.Errorf("Content-Length %q is not a number: %v", declared, err)
		return
	}
	if n != len(body) {
		t.Errorf("Content-Length is %d but the body is %d bytes", n, len(body))
	}
}

// putObjectWithHeaders writes an object with an arbitrary request header set,
// which is how the metadata and stored-header cases are arranged.
func putObjectWithHeaders(t *testing.T, srv *helpers.TestServer, bucket, key string, body []byte, headers map[string]string) {
	t.Helper()
	resp, err := http.DefaultClient.Do(put(srv, "/"+bucket+"/"+key, body, headers))
	if err != nil {
		t.Fatalf("putObjectWithHeaders %q/%q: %v", bucket, key, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("putObjectWithHeaders %q/%q: unexpected status %d", bucket, key, resp.StatusCode)
	}
}

// copyObject performs a server-side copy, whose destination inherits the
// source's metadata.
func copyObject(t *testing.T, srv *helpers.TestServer, srcBucket, srcKey, destBucket, destKey string) {
	t.Helper()
	resp, err := http.DefaultClient.Do(put(srv, "/"+destBucket+"/"+destKey, nil, map[string]string{
		"x-amz-copy-source": "/" + srcBucket + "/" + srcKey,
	}))
	if err != nil {
		t.Fatalf("copyObject %q/%q: %v", destBucket, destKey, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("copyObject %q/%q: unexpected status %d", destBucket, destKey, resp.StatusCode)
	}
}

// completeMultipart runs a one-part multipart upload, whose finished object
// carries the metadata supplied at CreateMultipartUpload.
func completeMultipart(t *testing.T, srv *helpers.TestServer, bucket, key string, body []byte, headers map[string]string) {
	t.Helper()

	createResp, err := http.DefaultClient.Do(mustReq(http.MethodPost, srv.URL+"/"+bucket+"/"+key+"?uploads", nil, headers))
	if err != nil {
		t.Fatalf("createMultipartUpload %q/%q: %v", bucket, key, err)
	}
	var created struct {
		UploadId string `xml:"UploadId"`
	}
	helpers.DecodeXML(t, createResp, &created)
	if created.UploadId == "" {
		t.Fatalf("createMultipartUpload %q/%q returned no UploadId", bucket, key)
	}

	partResp, err := http.DefaultClient.Do(put(srv,
		"/"+bucket+"/"+key+"?partNumber=1&uploadId="+created.UploadId, body, nil))
	if err != nil {
		t.Fatalf("uploadPart %q/%q: %v", bucket, key, err)
	}
	etag := partResp.Header.Get("ETag")
	partResp.Body.Close()
	if etag == "" {
		t.Fatalf("uploadPart %q/%q returned no ETag", bucket, key)
	}

	completeBody := fmt.Sprintf(
		`<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>%s</ETag></Part></CompleteMultipartUpload>`, etag)
	completeResp, err := http.DefaultClient.Do(mustReq(http.MethodPost,
		srv.URL+"/"+bucket+"/"+key+"?uploadId="+created.UploadId,
		bytes.NewReader([]byte(completeBody)), nil))
	if err != nil {
		t.Fatalf("completeMultipartUpload %q/%q: %v", bucket, key, err)
	}
	defer completeResp.Body.Close()
	if completeResp.StatusCode != http.StatusOK {
		t.Fatalf("completeMultipartUpload %q/%q: unexpected status %d", bucket, key, completeResp.StatusCode)
	}
}

// headVersionID reads the current version id of a key.
func headVersionID(t *testing.T, srv *helpers.TestServer, bucket, key string) string {
	t.Helper()
	resp, err := http.DefaultClient.Do(head(srv, "/"+bucket+"/"+key))
	if err != nil {
		t.Fatalf("headVersionID %q/%q: %v", bucket, key, err)
	}
	defer resp.Body.Close()
	id := resp.Header.Get("x-amz-version-id")
	if id == "" {
		t.Fatalf("headVersionID %q/%q: no x-amz-version-id header", bucket, key)
	}
	return id
}
