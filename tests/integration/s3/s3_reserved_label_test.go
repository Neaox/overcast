// Package s3_test — reserved host labels and bucket addressability.
//
// Overcast host-routes three AWS data-plane labels (execute-api, lambda-url,
// appsync-api). A Host whose bucket portion carries one of those as a
// second-or-later dot segment is claimed by that service rather than by S3:
// "my.execute-api.localhost" parses as an API Gateway invoke.
//
// Two things make this narrow in practice:
//
//   - A label at segment index 0 does NOT collide, because {id} must be
//     non-empty in every real AWS host-routed shape. A bucket named exactly
//     "execute-api" is fully addressable in every form.
//   - Overcast's CreateBucket validation rejects dots outright
//     (serviceutil.BucketName), so a bucket that could collide cannot be
//     created here at all today. That is a deliberate divergence from real
//     AWS, which permits dots — see the note on
//     TestS3VirtualHostedStyle_DottedBucketNameHost_SplitsOnFirstDotS3 and
//     docs/plans/host-routing-precedence.md §6.
//
// These tests therefore pin the routing contract directly, the same way that
// existing test does, rather than round-tripping a bucket that cannot exist.
package s3_test

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func TestCreateBucket_dottedNameIsRejected(t *testing.T) {
	// Given: a running server
	srv := helpers.NewTestServer(t)

	// When: a dotted bucket name is created — legal on real AWS, refused here
	resp := putBucketRaw(t, srv, "my.dotted.bucket")
	defer resp.Body.Close()

	// Then: 400 InvalidArgument. This is the guard that keeps the
	// reserved-label collision below theoretical: no bucket that could
	// collide can be created through Overcast's own API.
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertXMLError(t, resp, "InvalidArgument")
}

func TestBucketNamedExactlyLikeReservedLabel_isFullyAddressable(t *testing.T) {
	// Given: a bucket named exactly like a host-route label. The label sits at
	// segment index 0, and {id} must be non-empty in every real AWS
	// host-routed shape, so this does NOT collide.
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
			req, err := http.NewRequest(http.MethodGet, srv.URL+"/hello.txt", nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
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

func TestReservedLabelHost_bareFormIsClaimedByTheService(t *testing.T) {
	// Given: a server with no APIs and no buckets
	srv := helpers.NewTestServer(t)

	// When: a Host arrives whose bucket portion would carry a reserved label
	// at segment index >= 1 — the shape a dotted bucket "my.execute-api"
	// would produce in the bare virtual-hosted form
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/hello.txt", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Host = "my.execute-api.localhost:4566"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	// Then: API Gateway claims it and returns its own 403 for an unknown
	// apiId — not an S3 error. This is the documented limitation: that Host
	// cannot address a bucket, only the service.
	helpers.AssertStatus(t, resp, http.StatusForbidden)
}

func TestReservedLabelHost_labelledFormStillReachesS3(t *testing.T) {
	// Given: a server with no buckets
	srv := helpers.NewTestServer(t)

	// When: the same name is addressed the explicit way, with the ".s3."
	// separator in front of the base. PutObject is used rather than GetObject
	// because GetObject resolves a missing key before checking the bucket, so
	// only PUT surfaces the bucket name we want to assert on.
	req, err := http.NewRequest(http.MethodPut, srv.URL+"/hello.txt", bytes.NewReader([]byte("x")))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Host = "my.execute-api.s3.localhost:4566"
	req.Header.Set("Content-Type", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	// Then: S3 handles it, naming the full dotted bucket — proving the
	// labelled form is the escape hatch for a name the bare form cannot
	// reach, and that the split landed on ".s3." rather than the first dot.
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
