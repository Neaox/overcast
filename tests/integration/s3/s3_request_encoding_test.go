// Raw-HTTP request-shape tests for the S3 object path.
//
// Every AWS SDK sets a sensible Content-Type and percent-encodes keys the way
// Go's own URL canonicalisation would, which is exactly why the bugs covered
// here survived both the SDK-driven tests in s3_test.go and the compat suites.
// These tests speak raw HTTP on purpose.
package s3_test

import (
	"crypto/md5" //nolint:gosec // S3 ETags are MD5 by specification.
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// ---- Content-Type must not change how the body is read ----------------------

// TestPutObject_formURLEncodedContentTypeKeepsBody covers the content type many
// minimal HTTP clients send when none is set — `curl --data-binary` and browser
// form posts both default to it. The AWS Query protocol's identifier reads
// Action out of a form-encoded body, and r.ParseForm drains r.Body, so an S3
// object write wearing that header used to reach the handler with nothing left
// to store: 200 OK, the empty-string ETag, and a zero-byte object. On AWS the
// header is metadata, not a parsing instruction.
func TestPutObject_formURLEncodedContentTypeKeepsBody(t *testing.T) {
	// Given: a bucket, and a body that is not form-encoded at all
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "bodycheck")
	body := []byte("hello-body-check")

	// When: it is PUT with the default content type of a bare HTTP client
	resp, err := http.DefaultClient.Do(put(srv, "/bodycheck/k1", body, map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: the object is stored whole, and the ETag is the body's MD5
	helpers.AssertStatus(t, resp, http.StatusOK)
	wantETag := `"` + md5Hex(body) + `"`
	if got := resp.Header.Get("ETag"); got != wantETag {
		t.Errorf("ETag = %q, want %q", got, wantETag)
	}
	assertObjectBytes(t, srv, "/bodycheck/k1", body)
}

// TestPutObject_formURLEncodedBodyThatParsesAsAForm is the harder half of the
// same bug: a body that really is valid form encoding. Nothing about the bytes
// distinguishes it from a Query-protocol request, so a fix that only rewinds
// r.Body after a failed parse would still lose this one.
func TestPutObject_formURLEncodedBodyThatParsesAsAForm(t *testing.T) {
	// Given: a bucket, and a body that is itself a well-formed query string
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "bodycheck")
	body := []byte("Action=CreateQueue&Version=2012-11-05&QueueName=not-a-queue")

	// When: it is PUT as an object
	resp, err := http.DefaultClient.Do(put(srv, "/bodycheck/form.txt", body, map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: S3 stores the bytes verbatim — it never was a Query request
	helpers.AssertStatus(t, resp, http.StatusOK)
	assertObjectBytes(t, srv, "/bodycheck/form.txt", body)
}

// TestPutObject_bodySurvivesEveryContentType answers "is this specific to one
// content type?". It is: net/http only reads the body for
// application/x-www-form-urlencoded, so that was the one header that triggered
// the drain. The rest of this table is a regression net, not a list of things
// that were ever broken — a future sniffer that reaches for the body on some
// other header fails here instead of in a user's data.
func TestPutObject_bodySurvivesEveryContentType(t *testing.T) {
	contentTypes := []string{
		"application/x-www-form-urlencoded",
		"application/x-www-form-urlencoded; charset=utf-8",
		"APPLICATION/X-WWW-FORM-URLENCODED",
		"multipart/form-data; boundary=xyz",
		"application/x-amz-json-1.0",
		"application/x-amz-json-1.1",
		"application/json",
		"application/xml",
		"text/plain",
		"application/octet-stream",
		"",
	}

	for _, ct := range contentTypes {
		name := ct
		if name == "" {
			name = "absent"
		}
		t.Run(name, func(t *testing.T) {
			// Given: a bucket
			srv := helpers.NewTestServer(t)
			createBucket(t, srv, "ctcheck")
			body := []byte("Action=Publish&Version=2010-03-31&Message=payload")

			// When: an object is PUT under this content type
			headers := map[string]string{}
			if ct != "" {
				headers["Content-Type"] = ct
			}
			resp, err := http.DefaultClient.Do(put(srv, "/ctcheck/obj", body, headers))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			// Then: the body is stored whole, whatever the label said
			helpers.AssertStatus(t, resp, http.StatusOK)
			if got, want := resp.Header.Get("ETag"), `"`+md5Hex(body)+`"`; got != want {
				t.Errorf("ETag = %q, want %q", got, want)
			}
			assertObjectBytes(t, srv, "/ctcheck/obj", body)
		})
	}
}

// TestPutObject_formURLEncodedContentTypeIsStored checks the header survives as
// object metadata, since AWS echoes it back on GET.
func TestPutObject_formURLEncodedContentTypeIsStored(t *testing.T) {
	// Given: a bucket
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "bodycheck")

	// When: an object is written with the form content type
	resp, err := http.DefaultClient.Do(put(srv, "/bodycheck/k2", []byte("x"), map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
	}))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: GET reports it back
	got, err := http.DefaultClient.Do(get(srv, "/bodycheck/k2"))
	if err != nil {
		t.Fatal(err)
	}
	defer got.Body.Close()
	helpers.AssertStatus(t, got, http.StatusOK)
	if ct := got.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q, want application/x-www-form-urlencoded", ct)
	}
}

// TestPostObject_formURLEncodedContentTypeIsNotAQueryRequest guards the sibling
// method. A POST to an object path is S3's, whatever its content type; only a
// POST to the service root is Query traffic.
func TestPostObject_formURLEncodedContentTypeIsNotAQueryRequest(t *testing.T) {
	// Given: a bucket
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "bodycheck")

	// When: a form-encoded POST is sent to an object path
	req := mustReq(http.MethodPost, srv.URL+"/bodycheck/k3", strings.NewReader("Action=ListQueues"), map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
	})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: S3 answers it, with S3's own error for a bare object POST — not
	// the Query dispatcher's "unknown action" XML.
	helpers.AssertStatus(t, resp, http.StatusMethodNotAllowed)
	helpers.AssertXMLError(t, resp, "MethodNotAllowed")
}

// ---- Percent-encoded keys ---------------------------------------------------

// TestPutObject_percentEncodedPlusInKey covers `%2B`. chi routes on
// r.URL.RawPath when Go's canonical escaping of the decoded path differs from
// what the client sent, which is true for `+` and false for `%20` or a
// multi-byte UTF-8 sequence — so `+` alone used to be stored and listed as the
// literal three characters `%2B`.
func TestPutObject_percentEncodedPlusInKey(t *testing.T) {
	// Given: a bucket
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "pluscheck")
	body := []byte("plus-body")

	// When: an object is PUT at a key whose only encoded character is `+`
	resp, err := http.DefaultClient.Do(put(srv, "/pluscheck/plusonly/a%2Bb.txt", body, nil))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the key decoded to `+`, and reads at the same encoded path work
	assertBucketKeys(t, srv, "pluscheck", []string{"plusonly/a+b.txt"})
	assertObjectBytes(t, srv, "/pluscheck/plusonly/a%2Bb.txt", body)
}

// TestPutObject_percentEncodedKeysDecodeConsistently pins the whole class, so a
// fix for `+` cannot regress the encodings that already worked.
func TestPutObject_percentEncodedKeysDecodeConsistently(t *testing.T) {
	cases := []struct {
		name        string
		encodedPath string
		wantKey     string
	}{
		{name: "plus", encodedPath: "a%2Bb.txt", wantKey: "a+b.txt"},
		{name: "space", encodedPath: "a%20b.txt", wantKey: "a b.txt"},
		{name: "utf8", encodedPath: "caf%C3%A9.txt", wantKey: "café.txt"},
		{name: "equals", encodedPath: "a%3Db.txt", wantKey: "a=b.txt"},
		{name: "ampersand", encodedPath: "a%26b.txt", wantKey: "a&b.txt"},
		{name: "question mark", encodedPath: "a%3Fb.txt", wantKey: "a?b.txt"},
		{name: "hash", encodedPath: "a%23b.txt", wantKey: "a#b.txt"},
		{name: "literal percent-encoding", encodedPath: "a%2520b.txt", wantKey: "a%20b.txt"},
		{name: "nested with plus", encodedPath: "logs/2024%2B/a%2Bb.txt", wantKey: "logs/2024+/a+b.txt"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a bucket
			srv := helpers.NewTestServer(t)
			createBucket(t, srv, "keycheck")
			body := []byte("body-" + tc.name)

			// When: the object is written at the encoded path
			resp, err := http.DefaultClient.Do(put(srv, "/keycheck/"+tc.encodedPath, body, nil))
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			helpers.AssertStatus(t, resp, http.StatusOK)

			// Then: the stored key is the decoded form, and it reads back
			assertBucketKeys(t, srv, "keycheck", []string{tc.wantKey})
			assertObjectBytes(t, srv, "/keycheck/"+tc.encodedPath, body)
		})
	}
}

// ---- Test helpers ----------------------------------------------------------

func md5Hex(b []byte) string {
	sum := md5.Sum(b) //nolint:gosec // S3 ETags are MD5 by specification.
	return hex.EncodeToString(sum[:])
}

// assertObjectBytes checks both the streamed body and the length HEAD reports,
// because a dropped body shows up in each independently.
func assertObjectBytes(t *testing.T, srv *helpers.TestServer, path string, want []byte) {
	t.Helper()

	resp, err := http.DefaultClient.Do(get(srv, path))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("GET %s body = %q, want %q", path, got, want)
	}

	head, err := http.DefaultClient.Do(mustReq(http.MethodHead, srv.URL+path, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	head.Body.Close()
	helpers.AssertStatus(t, head, http.StatusOK)
	if cl := head.Header.Get("Content-Length"); cl != strconv.Itoa(len(want)) {
		t.Errorf("HEAD %s Content-Length = %q, want %d", path, cl, len(want))
	}
}

func assertBucketKeys(t *testing.T, srv *helpers.TestServer, bucket string, want []string) {
	t.Helper()

	resp, err := http.DefaultClient.Do(get(srv, "/"+bucket+"?list-type=2"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		Contents []struct {
			Key string `xml:"Key"`
		} `xml:"Contents"`
	}
	helpers.DecodeXML(t, resp, &result)

	got := make([]string, 0, len(result.Contents))
	for _, c := range result.Contents {
		got = append(got, c.Key)
	}
	if len(got) != len(want) {
		t.Fatalf("keys = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("keys = %q, want %q", got, want)
			return
		}
	}
}
