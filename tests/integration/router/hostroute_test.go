// Package router_test — host-based routing fallthrough.
//
// middleware.HostDispatch (internal/middleware/hostroute.go) only claims
// Host headers matching its grammar for a fixed set of labels
// (execute-api, lambda-url, appsync-api). Every other Host — including ones
// that merely look similar — must fall through untouched, per AGENTS.md
// "Routing fallthrough is S3": any request not claimed by a more specific
// route lands on the S3 handler. This file proves that fallthrough still
// produces a well-formed S3 response (not a panic, not a 500) end-to-end
// through the real middleware stack.
package router_test

import (
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

func TestHostDispatch_unrecognisedLabelFallsThroughToS3(t *testing.T) {
	// Given a running server and no buckets created
	srv := helpers.NewTestServer(t)

	// When a request carries a Host that resembles the host-route grammar
	// (id.label.region.base) but uses a label not in the table
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/some-bucket/some-key", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Host = "abc123.not-a-real-service.us-east-1.amazonaws.com"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	// Then it reaches S3 (not a panic, not a misrouted 200) and returns a
	// well-formed S3 XML error — NoSuchBucket, since neither "abc123..." nor
	// "some-bucket" was ever created (#1635: GetObject checks the bucket
	// first and only then the key).
	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertXMLError(t, resp, "NoSuchBucket")
}

func TestHostDispatch_plainHostFallsThroughToS3(t *testing.T) {
	// Given a running server
	srv := helpers.NewTestServer(t)

	// When a request carries an ordinary Host with no host-route grammar at all
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	// Then it reaches S3's ListBuckets — 200, not a panic or misroute
	helpers.AssertStatus(t, resp, http.StatusOK)
}
