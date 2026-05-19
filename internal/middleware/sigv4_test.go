package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
)

func TestSigV4_validationDisabled_passthrough(t *testing.T) {
	// Given: validation is disabled
	clk := clock.NewMock()
	called := false
	h := SigV4(false, nil, zap.NewNop(), clk)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	// When: a request passes through without any signature
	h.ServeHTTP(rec, req)

	// Then: the next handler runs untouched
	if !called {
		t.Fatal("expected next handler to be called")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestSigV4_validHeaderSignature_allowsRequest(t *testing.T) {
	// Given: a correctly signed JSON-protocol request
	clk := clock.NewMock()
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	clk.Set(now)
	called := false
	h := SigV4(true, nil, zap.NewNop(), clk)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueName":"demo"}`))
	req.Header.Set("Host", "localhost:4566")
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")
	signHeaderRequest(t, req, now, "us-east-1", "sqs", []string{"content-type", "host", "x-amz-target"})
	rec := httptest.NewRecorder()

	// When: the signed request is handled
	h.ServeHTTP(rec, req)

	// Then: signature validation succeeds and the request reaches the handler
	if !called {
		t.Fatal("expected next handler to be called")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestSigV4_invalidHeaderSignature_returnsJSONError(t *testing.T) {
	// Given: a signed JSON-protocol request with a corrupted signature
	clk := clock.NewMock()
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	clk.Set(now)
	h := SigV4(true, nil, zap.NewNop(), clk)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueName":"demo"}`))
	req.Header.Set("Host", "localhost:4566")
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")
	signHeaderRequest(t, req, now, "us-east-1", "sqs", []string{"content-type", "host", "x-amz-target"})
	req.Header.Set("Authorization", strings.Replace(req.Header.Get("Authorization"), "Signature=", "Signature=deadbeef", 1))
	rec := httptest.NewRecorder()

	// When: the request is handled
	h.ServeHTTP(rec, req)

	// Then: the middleware rejects it with an AWS JSON error
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "InvalidSignatureException") {
		t.Fatalf("expected InvalidSignatureException, got body %q", rec.Body.String())
	}
}

func TestSigV4_clockSkew_returnsQueryXMLError(t *testing.T) {
	// Given: a signed query-protocol request older than the allowed skew window
	clk := clock.NewMock()
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	clk.Set(now)
	h := SigV4(true, nil, zap.NewNop(), clk)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/?Action=ListTopics&Version=2010-03-31", nil)
	req.Header.Set("Host", "localhost:4566")
	signHeaderRequest(t, req, now.Add(-6*time.Minute), "us-east-1", "sns", []string{"host"})
	rec := httptest.NewRecorder()

	// When: the skewed request is handled
	h.ServeHTTP(rec, req)

	// Then: the middleware rejects it with an AWS Query XML error
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<Code>InvalidSignatureException</Code>") {
		t.Fatalf("expected query XML InvalidSignatureException, got body %q", rec.Body.String())
	}
}

func TestSigV4_validPresignedURL_allowsRequest(t *testing.T) {
	// Given: a correctly presigned S3-style GET request
	clk := clock.NewMock()
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	clk.Set(now)
	called := false
	h := SigV4(true, nil, zap.NewNop(), clk)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/my-bucket/test.txt", nil)
	req.Host = "localhost:4566"
	signPresignedRequest(t, req, now, "us-east-1", "s3", 300, []string{"host"})
	rec := httptest.NewRecorder()

	// When: the request is handled
	h.ServeHTTP(rec, req)

	// Then: presigned URL validation succeeds
	if !called {
		t.Fatal("expected next handler to be called")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func signHeaderRequest(t *testing.T, req *http.Request, now time.Time, region, service string, signedHeaders []string) {
	t.Helper()
	body := requestBodyBytes(t, req)
	payloadHash := testSHA256Hex(body)
	req.Body = io.NopCloser(strings.NewReader(string(body)))
	req.Header.Set("X-Amz-Date", now.UTC().Format("20060102T150405Z"))
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	canonicalRequest := canonicalRequestForTest(req, signedHeaders, payloadHash, false)
	date := now.UTC().Format("20060102")
	scope := date + "/" + region + "/" + service + "/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		req.Header.Get("X-Amz-Date"),
		scope,
		testSHA256Hex([]byte(canonicalRequest)),
	}, "\n")
	signature := testSignatureForRequest(date, region, service, stringToSign)
	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=test/%s, SignedHeaders=%s, Signature=%s",
		scope,
		strings.Join(signedHeaders, ";"),
		signature,
	))
}

func signPresignedRequest(t *testing.T, req *http.Request, now time.Time, region, service string, expires int, signedHeaders []string) {
	t.Helper()
	date := now.UTC().Format("20060102")
	amzDate := now.UTC().Format("20060102T150405Z")
	scope := date + "/" + region + "/" + service + "/aws4_request"
	q := req.URL.Query()
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", "test/"+scope)
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", fmt.Sprintf("%d", expires))
	q.Set("X-Amz-SignedHeaders", strings.Join(signedHeaders, ";"))
	req.URL.RawQuery = q.Encode()
	canonicalRequest := canonicalRequestForTest(req, signedHeaders, "UNSIGNED-PAYLOAD", true)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		testSHA256Hex([]byte(canonicalRequest)),
	}, "\n")
	signature := testSignatureForRequest(date, region, service, stringToSign)
	q = req.URL.Query()
	q.Set("X-Amz-Signature", signature)
	req.URL.RawQuery = q.Encode()
}

func requestBodyBytes(t *testing.T, req *http.Request) []byte {
	t.Helper()
	if req.Body == nil {
		return nil
	}
	b, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return b
}

func canonicalRequestForTest(req *http.Request, signedHeaders []string, payloadHash string, presigned bool) string {
	canonicalHeaders := canonicalHeadersForTest(req, signedHeaders)
	return strings.Join([]string{
		req.Method,
		canonicalPathForTest(req.URL),
		canonicalQueryForTest(req.URL.Query(), presigned),
		canonicalHeaders,
		strings.Join(signedHeaders, ";"),
		payloadHash,
	}, "\n")
}

func canonicalPathForTest(u *url.URL) string {
	if u == nil || u.EscapedPath() == "" {
		return "/"
	}
	return u.EscapedPath()
}

func canonicalQueryForTest(values url.Values, presigned bool) string {
	type pair struct{ k, v string }
	pairs := make([]pair, 0)
	for k, vs := range values {
		if presigned && strings.EqualFold(k, "X-Amz-Signature") {
			continue
		}
		sort.Strings(vs)
		for _, v := range vs {
			pairs = append(pairs, pair{testAWSURLEscape(k), testAWSURLEscape(v)})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].k == pairs[j].k {
			return pairs[i].v < pairs[j].v
		}
		return pairs[i].k < pairs[j].k
	})
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, p.k+"="+p.v)
	}
	return strings.Join(parts, "&")
}

func canonicalHeadersForTest(req *http.Request, signedHeaders []string) string {
	parts := make([]string, 0, len(signedHeaders))
	for _, h := range signedHeaders {
		name := strings.ToLower(h)
		value := headerValueForTest(req, name)
		parts = append(parts, name+":"+normalizeHeaderValueForTest(value)+"\n")
	}
	return strings.Join(parts, "")
}

func headerValueForTest(req *http.Request, name string) string {
	if name == "host" {
		if req.Host != "" {
			return req.Host
		}
		return req.URL.Host
	}
	return req.Header.Get(name)
}

func normalizeHeaderValueForTest(v string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(v)), " ")
}

func testSignatureForRequest(date, region, service, stringToSign string) string {
	kDate := testHMACSHA256([]byte("AWS4test"), date)
	kRegion := testHMACSHA256(kDate, region)
	kService := testHMACSHA256(kRegion, service)
	kSigning := testHMACSHA256(kService, "aws4_request")
	return hex.EncodeToString(testHMACSHA256(kSigning, stringToSign))
}

func testHMACSHA256(key []byte, msg string) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(msg))
	return h.Sum(nil)
}

func testSHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func testAWSURLEscape(s string) string {
	encoded := url.QueryEscape(s)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	encoded = strings.ReplaceAll(encoded, "*", "%2A")
	encoded = strings.ReplaceAll(encoded, "%7E", "~")
	return encoded
}

func TestSigV4_customSecretResolver_usesIAMSecret(t *testing.T) {
	clk := clock.NewMock()
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	clk.Set(now)

	resolver := &stubSecretResolver{secrets: map[string]string{"AKIAIOSFODNN7EXAMPLE": "custom-secret-key"}}

	called := false
	h := SigV4(true, resolver, zap.NewNop(), clk)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/my-bucket/test.txt", nil)
	req.Host = "localhost:4566"
	signPresignedRequestWithSecret(t, req, now, "us-east-1", "s3", 300, []string{"host"}, "AKIAIOSFODNN7EXAMPLE", "custom-secret-key")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected next handler to be called with custom secret")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestSigV4_customSecretResolver_wrongSecret_returns403(t *testing.T) {
	clk := clock.NewMock()
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	clk.Set(now)

	// Resolver knows key AKIAIOSFODNN7EXAMPLE but returns a DIFFERENT secret
	// than the one used to sign the request.
	resolver := &stubSecretResolver{secrets: map[string]string{"AKIAIOSFODNN7EXAMPLE": "correct-secret"}}

	h := SigV4(true, resolver, zap.NewNop(), clk)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	// Sign with a different secret than the one the resolver returns
	req := httptest.NewRequest(http.MethodGet, "/my-bucket/test.txt", nil)
	req.Host = "localhost:4566"
	signPresignedRequestWithSecret(t, req, now, "us-east-1", "s3", 300, []string{"host"}, "AKIAIOSFODNN7EXAMPLE", "wrong-secret")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, rec.Code)
	}
}

func TestSigV4_customSecretResolver_unknownKey_fallsBackToDefault(t *testing.T) {
	clk := clock.NewMock()
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	clk.Set(now)

	// resolver doesn't know "test" → should fall back to DefaultSigV4Secret
	resolver := &stubSecretResolver{secrets: map[string]string{}}

	called := false
	h := SigV4(true, resolver, zap.NewNop(), clk)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/my-bucket/test.txt", nil)
	req.Host = "localhost:4566"
	signPresignedRequest(t, req, now, "us-east-1", "s3", 300, []string{"host"})
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected next handler to be called (fell back to default secret)")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rec.Code)
	}
}

// stubSecretResolver implements SecretResolver with an in-memory map.
type stubSecretResolver struct {
	secrets map[string]string
}

func (s *stubSecretResolver) ResolveSecret(_ context.Context, accessKeyID string) (string, bool, error) {
	secret, ok := s.secrets[accessKeyID]
	return secret, ok, nil
}

func signPresignedRequestWithSecret(t *testing.T, req *http.Request, now time.Time, region, service string, expires int, signedHeaders []string, accessKey, secret string) {
	t.Helper()
	date := now.UTC().Format("20060102")
	amzDate := now.UTC().Format("20060102T150405Z")
	scope := date + "/" + region + "/" + service + "/aws4_request"
	q := req.URL.Query()
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", accessKey+"/"+scope)
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", fmt.Sprintf("%d", expires))
	q.Set("X-Amz-SignedHeaders", strings.Join(signedHeaders, ";"))
	req.URL.RawQuery = q.Encode()
	canonicalRequest := canonicalRequestForTest(req, signedHeaders, "UNSIGNED-PAYLOAD", true)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		testSHA256Hex([]byte(canonicalRequest)),
	}, "\n")
	signature := testSignatureForKey(date, region, service, stringToSign, secret)
	q = req.URL.Query()
	q.Set("X-Amz-Signature", signature)
	req.URL.RawQuery = q.Encode()
}

func testSignatureForKey(date, region, service, stringToSign, secret string) string {
	kDate := testHMACSHA256([]byte("AWS4"+secret), date)
	kRegion := testHMACSHA256(kDate, region)
	kService := testHMACSHA256(kRegion, service)
	kSigning := testHMACSHA256(kService, "aws4_request")
	return hex.EncodeToString(testHMACSHA256(kSigning, stringToSign))
}
