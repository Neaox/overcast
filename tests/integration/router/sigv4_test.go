// Package router_test — end-to-end SigV4 validation pinning.
//
// OVERCAST_SIGV4_VALIDATE once shipped with a startup warning claiming
// validation was "not yet implemented — all requests are accepted" while the
// middleware was in fact rejecting bad signatures with 403 (issue #735). The
// warning and the implementation could only drift that far apart because no
// test pinned the flag's observable behaviour through the real server stack.
// These tests are that pin: a header-signed request with a corrupted
// signature is rejected with 403 InvalidSignatureException when the flag is
// on, and accepted when it is off.
package router_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// sqsListQueuesRequest builds an SQS ListQueues request against srv signed
// with the default local-dev secret ("test"), mirroring what an AWS SDK
// configured with test credentials sends.
func sqsListQueuesRequest(t *testing.T, srv *helpers.TestServer) *http.Request {
	t.Helper()
	const body = `{}`
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.ListQueues")
	signSigV4HeaderRequest(t, req, []byte(body), time.Now().UTC(), "us-east-1", "sqs", "test", "test")
	return req
}

func TestSigV4Validate_on_validSignature_accepted(t *testing.T) {
	// Given: a server with SigV4 validation enabled
	srv := helpers.NewTestServer(t, helpers.WithSigV4Validate(true))

	// When: a correctly signed request is sent
	resp, err := http.DefaultClient.Do(sqsListQueuesRequest(t, srv))
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	// Then: it is accepted. This also proves the signing helper below is
	// correct, so the 403 in the corrupted-signature test can only mean the
	// signature check itself fired.
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestSigV4Validate_on_corruptedSignature_returns403(t *testing.T) {
	// Given: a server with SigV4 validation enabled
	srv := helpers.NewTestServer(t, helpers.WithSigV4Validate(true))

	// When: the request's signature is corrupted after signing
	req := sqsListQueuesRequest(t, srv)
	req.Header.Set("Authorization", strings.Replace(
		req.Header.Get("Authorization"), "Signature=", "Signature=deadbeef", 1))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	// Then: the server rejects it with 403 InvalidSignatureException
	helpers.AssertStatus(t, resp, http.StatusForbidden)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "InvalidSignatureException") {
		t.Fatalf("expected InvalidSignatureException in body, got %q", body)
	}
}

func TestSigV4Validate_off_corruptedSignature_accepted(t *testing.T) {
	// Given: a server with SigV4 validation disabled (the default)
	srv := helpers.NewTestServer(t)

	// When: the same corrupted-signature request is sent
	req := sqsListQueuesRequest(t, srv)
	req.Header.Set("Authorization", strings.Replace(
		req.Header.Get("Authorization"), "Signature=", "Signature=deadbeef", 1))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	// Then: it is accepted — with the flag off, signatures are not checked
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ---- Signing helper ---------------------------------------------------------

// signSigV4HeaderRequest signs req with an AWS SigV4 Authorization header the
// way an SDK would: X-Amz-Date and X-Amz-Content-Sha256 headers plus the
// four-stage HMAC over the canonical request. Only what these tests need is
// implemented (no query string, no multi-value headers).
func signSigV4HeaderRequest(t *testing.T, req *http.Request, body []byte, now time.Time, region, service, accessKey, secret string) {
	t.Helper()
	amzDate := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	payloadHash := sigv4TestSHA256Hex(body)
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	signedHeaders := []string{"content-type", "host", "x-amz-content-sha256", "x-amz-date", "x-amz-target"}
	canonicalHeaders := make([]string, 0, len(signedHeaders))
	for _, h := range signedHeaders {
		value := req.Header.Get(h)
		if h == "host" {
			value = req.URL.Host // the client sends Host from the URL
		}
		canonicalHeaders = append(canonicalHeaders, h+":"+strings.Join(strings.Fields(value), " ")+"\n")
	}
	canonicalRequest := strings.Join([]string{
		req.Method,
		"/",
		"", // no query string
		strings.Join(canonicalHeaders, ""),
		strings.Join(signedHeaders, ";"),
		payloadHash,
	}, "\n")

	scope := date + "/" + region + "/" + service + "/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sigv4TestSHA256Hex([]byte(canonicalRequest)),
	}, "\n")

	kDate := sigv4TestHMAC([]byte("AWS4"+secret), date)
	kRegion := sigv4TestHMAC(kDate, region)
	kService := sigv4TestHMAC(kRegion, service)
	kSigning := sigv4TestHMAC(kService, "aws4_request")
	signature := hex.EncodeToString(sigv4TestHMAC(kSigning, stringToSign))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, scope, strings.Join(signedHeaders, ";"), signature,
	))
}

func sigv4TestHMAC(key []byte, msg string) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(msg))
	return h.Sum(nil)
}

func sigv4TestSHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
