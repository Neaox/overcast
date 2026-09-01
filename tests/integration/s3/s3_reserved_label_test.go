// Package s3_test — dotted bucket names, reserved host labels, addressability.
//
// AWS permits periods in general purpose bucket names (example.com and
// my.example.s3.bucket are AWS-documented valid examples). It discourages them,
// because they break the *.s3.<region>.amazonaws.com wildcard certificate for
// virtual-hosted-style addressing over HTTPS — but they are legal, so Overcast
// accepts them.
//
// That makes one collision reachable. Overcast host-routes three AWS data-plane
// labels (execute-api, lambda-url, appsync-api), so a bucket whose name carries
// one as a SECOND-OR-LATER dot segment cannot be reached by the bare
// virtual-hosted form: "my.execute-api.localhost" is an API Gateway invoke.
// A label at segment index 0 is unaffected, because a host-routed address
// always has a non-empty resource ID in front of the label.
//
// See docs/plans/host-routing-precedence.md §6.
package s3_test

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/http"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/overcast-sh/overcast/tests/helpers"
)

func TestCreateBucket_dottedNameIsAccepted(t *testing.T) {
	// Given: a running server
	srv := helpers.NewTestServer(t)

	// When: AWS-documented valid dotted names are created
	for _, bucket := range []string{"example.com", "www.example.com", "my.example.s3.bucket"} {
		t.Run(bucket, func(t *testing.T) {
			resp := putBucketRaw(t, srv, bucket)
			defer resp.Body.Close()

			// Then: they succeed, as they do on AWS
			helpers.AssertStatus(t, resp, http.StatusOK)
		})
	}
}

func TestDottedBucket_roundTripsPathStyleAndVirtualHosted(t *testing.T) {
	// Given: a dotted bucket holding an object
	srv := helpers.NewTestServer(t)
	const bucket = "my.dotted.bucket"
	createBucket(t, srv, bucket)
	putObject(t, srv, bucket, "hello.txt", []byte("hello"), "text/plain")

	cases := []struct{ name, host, path string }{
		{name: "path-style", path: "/" + bucket + "/hello.txt"},
		{name: "bare virtual-hosted", host: bucket + ".localhost:4566", path: "/hello.txt"},
		{name: "labelled virtual-hosted", host: bucket + ".s3.localhost:4566", path: "/hello.txt"},
		{name: "labelled with region", host: bucket + ".s3.us-east-1.localhost:4566", path: "/hello.txt"},
		{name: "wildcard DNS base", host: bucket + ".localhost.overcast.sh:4566", path: "/hello.txt"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// When: the object is fetched via that addressing form
			req, err := http.NewRequest(http.MethodGet, srv.URL+tc.path, nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			if tc.host != "" {
				req.Host = tc.host
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer resp.Body.Close()

			// Then: the object comes back — dotted names work in every form
			helpers.AssertStatus(t, resp, http.StatusOK)
			body, _ := io.ReadAll(resp.Body)
			if string(body) != "hello" {
				t.Errorf("body = %q, want %q", body, "hello")
			}
		})
	}
}

func TestCreateBucket_reservedHostLabelIsAcceptedAndWarned(t *testing.T) {
	// Given: a server whose warnings are observable
	core, logs := observer.New(zap.WarnLevel)
	srv := helpers.NewTestServer(t, helpers.WithLogger(zap.New(core)))

	// When: a bucket is created carrying a reserved host label as its second
	// segment
	resp := putBucketRaw(t, srv, "my.execute-api")
	defer resp.Body.Close()

	// Then: creation succeeds — AWS accepts the name, so Overcast must not
	// diverge by refusing it
	helpers.AssertStatus(t, resp, http.StatusOK)

	// ...and the addressing limitation is reported. The AWS wire format has no
	// field for a warning, so a log line is the only channel available.
	entries := logs.FilterMessageSnippet("reserved").All()
	if len(entries) == 0 {
		var got []string
		for _, e := range logs.All() {
			got = append(got, e.Message)
		}
		t.Fatalf("expected a warning naming the reserved host label; got warnings: %v", got)
	}
	found := false
	for _, e := range entries {
		for _, f := range e.Context {
			if f.String == "execute-api" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("warning did not name the offending label %q: %+v", "execute-api", entries[0].ContextMap())
	}
}

func TestCreateBucket_ordinaryDottedNameIsNotWarned(t *testing.T) {
	// Given: a server whose warnings are observable
	core, logs := observer.New(zap.WarnLevel)
	srv := helpers.NewTestServer(t, helpers.WithLogger(zap.New(core)))

	// When: a dotted name with no reserved segment is created
	resp := putBucketRaw(t, srv, "my.dotted.bucket")
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: nothing is warned — the diagnostic must not fire on every name
	// that merely contains a dot
	if n := logs.FilterMessageSnippet("reserved").Len(); n != 0 {
		t.Errorf("expected no reserved-label warning for %q, got %d", "my.dotted.bucket", n)
	}
}

func TestReservedLabelBucket_reachableExceptBareVirtualHost(t *testing.T) {
	// Given: a real bucket whose name carries a reserved host label
	srv := helpers.NewTestServer(t)
	const bucket = "my.execute-api"
	createBucket(t, srv, bucket)
	putObject(t, srv, bucket, "hello.txt", []byte("hello"), "text/plain")

	t.Run("path-style works", func(t *testing.T) {
		resp, err := http.DefaultClient.Get(srv.URL + "/" + bucket + "/hello.txt")
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusOK)
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "hello" {
			t.Errorf("body = %q, want %q", body, "hello")
		}
	})

	t.Run("labelled virtual-hosted works", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/hello.txt", nil)
		req.Host = bucket + ".s3.localhost:4566"
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusOK)
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "hello" {
			t.Errorf("body = %q, want %q", body, "hello")
		}
	})

	t.Run("bare virtual-hosted is shadowed by the service", func(t *testing.T) {
		// The documented limitation: the reserved label at segment index 1
		// makes this an API Gateway invoke for an apiId that does not exist.
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/hello.txt", nil)
		req.Host = bucket + ".localhost:4566"
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusForbidden)
	})
}

func TestBucketNamedExactlyLikeReservedLabel_isFullyAddressable(t *testing.T) {
	// Given: a bucket named exactly like a host-route label. The label sits at
	// segment index 0, and a host-routed address always has a non-empty
	// resource ID in front of the label, so this does NOT collide.
	srv := helpers.NewTestServer(t)
	const bucket = "execute-api"
	createBucket(t, srv, bucket)
	putObject(t, srv, bucket, "hello.txt", []byte("hello"), "text/plain")

	for _, host := range []string{
		bucket + ".localhost:4566",
		bucket + ".s3.localhost:4566",
		bucket + ".s3.us-east-1.localhost:4566",
		bucket + ".localhost.overcast.sh:4566",
	} {
		t.Run(host, func(t *testing.T) {
			// When: the object is fetched virtual-hosted style
			req, _ := http.NewRequest(http.MethodGet, srv.URL+"/hello.txt", nil)
			req.Host = host
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer resp.Body.Close()

			// Then: S3 answers, in every form
			helpers.AssertStatus(t, resp, http.StatusOK)
			body, _ := io.ReadAll(resp.Body)
			if string(body) != "hello" {
				t.Errorf("body = %q, want %q", body, "hello")
			}
		})
	}
}

func TestReservedLabelHost_labelledFormNamesTheFullBucket(t *testing.T) {
	// Given: a server with no buckets
	srv := helpers.NewTestServer(t)

	// When: a dotted reserved-label name is addressed the explicit way.
	// PutObject is used rather than GetObject because GetObject resolves a
	// missing key before checking the bucket, so only PUT surfaces the bucket
	// name being asserted on.
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/hello.txt", bytes.NewReader([]byte("x")))
	req.Host = "my.execute-api.s3.localhost:4566"
	req.Header.Set("Content-Type", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	// Then: S3 handles it, naming the full dotted bucket — proving the split
	// landed on ".s3." rather than the first dot, and that the labelled form
	// is the escape hatch for a name the bare form cannot reach.
	helpers.AssertStatus(t, resp, http.StatusNotFound)
	body, _ := io.ReadAll(resp.Body)
	var errResp struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	if err := xml.Unmarshal(body, &errResp); err != nil {
		t.Fatalf("decode XML error %q: %v", body, err)
	}
	if errResp.Code != "NoSuchBucket" {
		t.Errorf("expected NoSuchBucket, got %q (%s)", errResp.Code, errResp.Message)
	}
	if want := "my.execute-api"; !strings.Contains(errResp.Message, want) {
		t.Errorf("expected error to name bucket %q, got %q", want, errResp.Message)
	}
}

// putBucketRaw issues CreateBucket and returns the raw response, so a test can
// assert on the status itself rather than failing inside createBucket.
func putBucketRaw(t *testing.T, srv *helpers.TestServer, bucket string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, srv.URL+"/"+bucket, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create bucket %q: %v", bucket, err)
	}
	return resp
}
