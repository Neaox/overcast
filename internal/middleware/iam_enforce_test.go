package middleware

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/state"
)

func TestIAMEnforce_disabled_passthroughUnsigned(t *testing.T) {
	called := false
	h := IAMEnforce(false, nil, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueName":"demo"}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected next handler to be called")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestIAMEnforce_enabled_deniesUnsignedJSON(t *testing.T) {
	h := IAMEnforce(true, nil, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueName":"demo"}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "AccessDeniedException") {
		t.Fatalf("expected AccessDeniedException, got body %q", rec.Body.String())
	}
}

func TestIAMEnforce_enabled_deniesUnsignedQueryXML(t *testing.T) {
	h := IAMEnforce(true, nil, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/?Action=GetCallerIdentity&Version=2011-06-15", nil)
	req.Header.Set("Host", "localhost:4566")
	req.Header.Set("Authorization", "")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<Code>AccessDenied</Code>") {
		t.Fatalf("expected Query XML AccessDenied, got body %q", rec.Body.String())
	}
}

func TestIAMEnforce_enabled_deniesSignedRequestWithoutPrincipal(t *testing.T) {
	called := false
	h := IAMEnforce(true, state.NewMemoryStore(), zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueName":"demo"}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260423/us-east-1/sqs/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if called {
		t.Fatal("expected next handler not to be called")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, rec.Code)
	}
}

func TestIAMEnforce_enabled_bypassesInternalRoutes(t *testing.T) {
	called := false
	h := IAMEnforce(true, state.NewMemoryStore(), zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/_health", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected internal route to bypass IAM enforcement")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestIAMEnforce_enabled_allowsSignedRequestWithMatchingPolicy(t *testing.T) {
	st := state.NewMemoryStore()
	seedIAMUserWithPolicies(t, st, "test", []string{`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:CreateQueue","Resource":"*"}]}`}, nil)

	called := false
	h := IAMEnforce(true, st, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueName":"demo"}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260423/us-east-1/sqs/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	req.Header.Set("X-Amz-Date", "20260423T000000Z")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected next handler to be called")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestIAMEnforce_enabled_explicitDenyOverridesAllow(t *testing.T) {
	st := state.NewMemoryStore()
	seedIAMUserWithPolicies(t, st, "test", []string{`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:*","Resource":"*"},{"Effect":"Deny","Action":"sqs:DeleteQueue","Resource":"*"}]}`}, nil)

	h := IAMEnforce(true, st, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueUrl":"https://localhost/000000000000/demo"}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.DeleteQueue")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260423/us-east-1/sqs/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	req.Header.Set("X-Amz-Date", "20260423T000000Z")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, rec.Code)
	}
}

func TestIAMEnforce_enabled_allowsSignedRequestWithGroupInlinePolicy(t *testing.T) {
	st := state.NewMemoryStore()
	seedIAMUserWithPolicies(t, st, "test", nil, nil)
	seedIAMGroupWithPolicies(t, st, "devs", []string{"test"}, []string{`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:CreateQueue","Resource":"*"}]}`}, nil)

	called := false
	h := IAMEnforce(true, st, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueName":"demo"}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260423/us-east-1/sqs/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	req.Header.Set("X-Amz-Date", "20260423T000000Z")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected next handler to be called")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestIAMEnforce_enabled_groupExplicitDenyOverridesUserAllow(t *testing.T) {
	st := state.NewMemoryStore()
	seedIAMUserWithPolicies(t, st, "test", []string{`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:DeleteQueue","Resource":"*"}]}`}, nil)
	seedIAMGroupWithPolicies(t, st, "security", []string{"test"}, nil, map[string]string{
		"arn:aws:iam::000000000000:policy/deny-delete": `{"Version":"2012-10-17","Statement":[{"Effect":"Deny","Action":"sqs:DeleteQueue","Resource":"*"}]}`,
	})

	h := IAMEnforce(true, st, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueUrl":"https://localhost/000000000000/demo"}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.DeleteQueue")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260423/us-east-1/sqs/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	req.Header.Set("X-Amz-Date", "20260423T000000Z")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, rec.Code)
	}
}

func TestIAMEnforce_enabled_restFallbackS3ExplicitDenyBlocks(t *testing.T) {
	st := state.NewMemoryStore()
	seedIAMUserWithPolicies(t, st, "test", []string{`{"Version":"2012-10-17","Statement":[{"Effect":"Deny","Action":"s3:ListBuckets","Resource":"*"}]}`}, nil)

	h := IAMEnforce(true, st, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260423/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	req.Header.Set("X-Amz-Date", "20260423T000000Z")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<Code>AccessDenied</Code>") {
		t.Fatalf("expected XML AccessDenied body, got %q", rec.Body.String())
	}
}

func TestIAMEnforce_enabled_restFallbackS3AllowPasses(t *testing.T) {
	st := state.NewMemoryStore()
	seedIAMUserWithPolicies(t, st, "test", []string{`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:ListBuckets","Resource":"*"}]}`}, nil)

	called := false
	h := IAMEnforce(true, st, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260423/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	req.Header.Set("X-Amz-Date", "20260423T000000Z")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected next handler to be called")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestIAMEnforce_enabled_notActionAllow_allowsNonExcludedAction(t *testing.T) {
	st := state.NewMemoryStore()
	seedIAMUserWithPolicies(t, st, "test", []string{`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","NotAction":"sqs:DeleteQueue","Resource":"*"}]}`}, nil)

	called := false
	h := IAMEnforce(true, st, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueName":"demo"}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260423/us-east-1/sqs/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	req.Header.Set("X-Amz-Date", "20260423T000000Z")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected next handler to be called")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestIAMEnforce_enabled_notActionAllow_deniesExcludedAction(t *testing.T) {
	st := state.NewMemoryStore()
	seedIAMUserWithPolicies(t, st, "test", []string{`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","NotAction":"sqs:DeleteQueue","Resource":"*"}]}`}, nil)

	h := IAMEnforce(true, st, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueUrl":"https://localhost/000000000000/demo"}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.DeleteQueue")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260423/us-east-1/sqs/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	req.Header.Set("X-Amz-Date", "20260423T000000Z")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, rec.Code)
	}
}

func TestIAMEnforce_enabled_notResourceAllow_deniesExcludedResource(t *testing.T) {
	st := state.NewMemoryStore()
	seedIAMUserWithPolicies(t, st, "test", []string{`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:DeleteQueue","NotResource":"arn:aws:sqs:us-east-1:000000000000:blocked-queue"}]}`}, nil)

	h := IAMEnforce(true, st, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueUrl":"https://localhost/000000000000/blocked-queue"}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.DeleteQueue")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260423/us-east-1/sqs/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	req.Header.Set("X-Amz-Date", "20260423T000000Z")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, rec.Code)
	}
}

func TestIAMEnforce_enabled_notResourceAllow_allowsOtherResource(t *testing.T) {
	st := state.NewMemoryStore()
	seedIAMUserWithPolicies(t, st, "test", []string{`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:DeleteQueue","NotResource":"arn:aws:sqs:us-east-1:000000000000:blocked-queue"}]}`}, nil)

	called := false
	h := IAMEnforce(true, st, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueUrl":"https://localhost/000000000000/other-queue"}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.DeleteQueue")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260423/us-east-1/sqs/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	req.Header.Set("X-Amz-Date", "20260423T000000Z")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected next handler to be called")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestIAMEnforce_enabled_roleSessionInlinePolicy_allows(t *testing.T) {
	st := state.NewMemoryStore()
	// No user record — the access key belongs to a role session.
	seedIAMRoleSession(t, st, "ASIA-role-key", "arn:aws:iam::123456789012:role/my-role", "my-role")
	seedIAMRoleWithPolicies(t, st, "my-role",
		[]string{`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:CreateQueue","Resource":"*"}]}`},
		nil,
	)

	called := false
	h := IAMEnforce(true, st, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueName":"demo"}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=ASIA-role-key/20260423/us-east-1/sqs/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	req.Header.Set("X-Amz-Date", "20260423T000000Z")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected next handler to be called")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestIAMEnforce_enabled_roleSessionInlinePolicy_denies(t *testing.T) {
	st := state.NewMemoryStore()
	seedIAMRoleSession(t, st, "ASIA-role-key", "arn:aws:iam::123456789012:role/my-role", "my-role")
	seedIAMRoleWithPolicies(t, st, "my-role",
		[]string{`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:CreateQueue","Resource":"*"}]}`},
		nil,
	)

	h := IAMEnforce(true, st, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueUrl":"https://localhost/000000000000/demo"}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.DeleteQueue")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=ASIA-role-key/20260423/us-east-1/sqs/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	req.Header.Set("X-Amz-Date", "20260423T000000Z")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, rec.Code)
	}
}

func TestIAMEnforce_enabled_roleSessionExplicitDeny_overridesAllow(t *testing.T) {
	st := state.NewMemoryStore()
	seedIAMRoleSession(t, st, "ASIA-role-key", "arn:aws:iam::123456789012:role/my-role", "my-role")
	seedIAMRoleWithPolicies(t, st, "my-role",
		[]string{`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:*","Resource":"*"},{"Effect":"Deny","Action":"sqs:DeleteQueue","Resource":"*"}]}`},
		nil,
	)

	h := IAMEnforce(true, st, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueUrl":"https://localhost/000000000000/demo"}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.DeleteQueue")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=ASIA-role-key/20260423/us-east-1/sqs/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	req.Header.Set("X-Amz-Date", "20260423T000000Z")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, rec.Code)
	}
}

func TestIAMEnforce_enabled_roleSessionManagedPolicy_allows(t *testing.T) {
	st := state.NewMemoryStore()
	seedIAMRoleSession(t, st, "ASIA-role-key", "arn:aws:iam::123456789012:role/my-role", "my-role")
	seedIAMRoleWithPolicies(t, st, "my-role", nil, map[string]string{
		"arn:aws:iam::aws:policy/SQSFullAccess": `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:*","Resource":"*"}]}`,
	})

	called := false
	h := IAMEnforce(true, st, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueName":"demo"}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=ASIA-role-key/20260423/us-east-1/sqs/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	req.Header.Set("X-Amz-Date", "20260423T000000Z")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected next handler to be called")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestIAMRequestFieldResolver_jsonFieldsAndArray_doNotConsumeBody(t *testing.T) {
	body := `{"TableName":"demo-table","SecretId":"demo-secret","clusters":["cluster-a"]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")

	fields := newIAMRequestFieldResolver()

	if got := fields.field(req, "TableName"); got != "demo-table" {
		t.Fatalf("unexpected table name: %q", got)
	}
	if got := fields.field(req, "SecretId"); got != "demo-secret" {
		t.Fatalf("unexpected secret id: %q", got)
	}
	if got := fields.firstJSONArrayStringField(req, "clusters"); got != "cluster-a" {
		t.Fatalf("unexpected first clusters value: %q", got)
	}

	raw, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	if got := string(raw); got != body {
		t.Fatalf("expected request body to be preserved, got %q", got)
	}
}

func TestIAMRegionOrDefault_usesSigV4RegionWhenPresent(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260423/eu-west-1/sqs/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")

	if got := iamRegionOrDefault(req); got != "eu-west-1" {
		t.Fatalf("expected eu-west-1, got %q", got)
	}
}

func TestIAMRegionOrDefault_fallsBackToUSEast1(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)

	if got := iamRegionOrDefault(req); got != "us-east-1" {
		t.Fatalf("expected us-east-1 fallback, got %q", got)
	}
}

func TestIAMRequestFieldResolver_precedence(t *testing.T) {
	t.Run("query over json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/?Name=query-value", strings.NewReader(`{"Name":"json-value"}`))
		req.Header.Set("Content-Type", "application/x-amz-json-1.1")

		fields := newIAMRequestFieldResolver()
		if got := fields.field(req, "Name"); got != "query-value" {
			t.Fatalf("expected query value, got %q", got)
		}
	})

	t.Run("form over missing query", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("Name=form-value"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		fields := newIAMRequestFieldResolver()
		if got := fields.field(req, "Name"); got != "form-value" {
			t.Fatalf("expected form value, got %q", got)
		}
	})

	t.Run("json fallback", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"Name":"json-value"}`))
		req.Header.Set("Content-Type", "application/x-amz-json-1.1")

		fields := newIAMRequestFieldResolver()
		if got := fields.field(req, "Name"); got != "json-value" {
			t.Fatalf("expected json value, got %q", got)
		}
	})
}

func seedIAMUserWithPolicies(t *testing.T, st state.Store, accessKey string, inlinePolicies []string, managedPolicies map[string]string) {
	t.Helper()

	ctx := context.Background()
	inline := map[string]string{}
	for i, doc := range inlinePolicies {
		inline["inline-"+strconv.Itoa(i+1)] = doc
	}

	attached := make([]map[string]string, 0, len(managedPolicies))
	for arn, doc := range managedPolicies {
		attached = append(attached, map[string]string{
			"PolicyArn": arn,
		})
		body, err := json.Marshal(map[string]any{"Document": doc})
		if err != nil {
			t.Fatalf("marshal managed policy: %v", err)
		}
		if err := st.Set(ctx, "iam:policies", arn, string(body)); err != nil {
			t.Fatalf("seed managed policy: %v", err)
		}
	}

	user := map[string]any{
		"UserName":         accessKey,
		"AccessKeys":       []map[string]string{{"AccessKeyId": accessKey}},
		"InlinePolicies":   inline,
		"AttachedPolicies": attached,
	}
	b, err := json.Marshal(user)
	if err != nil {
		t.Fatalf("marshal user seed: %v", err)
	}
	if err := st.Set(ctx, "iam:users", accessKey, string(b)); err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

func seedIAMGroupWithPolicies(
	t *testing.T,
	st state.Store,
	groupName string,
	members []string,
	inlinePolicies []string,
	managedPolicies map[string]string,
) {
	t.Helper()

	ctx := context.Background()
	inline := map[string]string{}
	for i, doc := range inlinePolicies {
		inline["inline-"+strconv.Itoa(i+1)] = doc
	}

	attached := make([]map[string]string, 0, len(managedPolicies))
	for arn, doc := range managedPolicies {
		attached = append(attached, map[string]string{
			"PolicyArn": arn,
		})
		body, err := json.Marshal(map[string]any{"Document": doc})
		if err != nil {
			t.Fatalf("marshal managed policy: %v", err)
		}
		if err := st.Set(ctx, "iam:policies", arn, string(body)); err != nil {
			t.Fatalf("seed managed policy: %v", err)
		}
	}

	group := map[string]any{
		"Members":          members,
		"InlinePolicies":   inline,
		"AttachedPolicies": attached,
	}
	b, err := json.Marshal(group)
	if err != nil {
		t.Fatalf("marshal group seed: %v", err)
	}
	if err := st.Set(ctx, "iam:groups", groupName, string(b)); err != nil {
		t.Fatalf("seed group: %v", err)
	}
}

func seedIAMRoleSession(t *testing.T, st state.Store, accessKeyID, roleArn, roleName string) {
	t.Helper()
	session := map[string]string{
		"RoleArn":  roleArn,
		"RoleName": roleName,
	}
	b, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("marshal role session: %v", err)
	}
	if err := st.Set(context.Background(), "iam:sessions", accessKeyID, string(b)); err != nil {
		t.Fatalf("seed role session: %v", err)
	}
}

func seedIAMRoleWithPolicies(t *testing.T, st state.Store, roleName string, inlinePolicies []string, managedPolicies map[string]string) {
	t.Helper()

	ctx := context.Background()
	inline := map[string]string{}
	for i, doc := range inlinePolicies {
		inline["inline-"+strconv.Itoa(i+1)] = doc
	}

	attached := make([]map[string]string, 0, len(managedPolicies))
	for arn, doc := range managedPolicies {
		attached = append(attached, map[string]string{
			"PolicyArn": arn,
		})
		body, err := json.Marshal(map[string]any{"Document": doc})
		if err != nil {
			t.Fatalf("marshal managed policy: %v", err)
		}
		if err := st.Set(ctx, "iam:policies", arn, string(body)); err != nil {
			t.Fatalf("seed managed policy: %v", err)
		}
	}

	role := map[string]any{
		"RoleName":         roleName,
		"InlinePolicies":   inline,
		"AttachedPolicies": attached,
	}
	b, err := json.Marshal(role)
	if err != nil {
		t.Fatalf("marshal role seed: %v", err)
	}
	if err := st.Set(ctx, "iam:roles", roleName, string(b)); err != nil {
		t.Fatalf("seed role: %v", err)
	}
}

// ─── Condition evaluation unit tests ─────────────────────────────────────────

func TestEvaluateConditions_noCondition_alwaysMet(t *testing.T) {
	met, unknown := evaluateConditions(nil, map[string]string{})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_stringEquals_match(t *testing.T) {
	cond := map[string]map[string][]string{
		"StringEquals": {"aws:requestedregion": {"us-east-1"}},
	}
	met, unknown := evaluateConditions(cond, map[string]string{"aws:requestedregion": "us-east-1"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_stringEquals_noMatch(t *testing.T) {
	cond := map[string]map[string][]string{
		"StringEquals": {"aws:requestedregion": {"us-east-1"}},
	}
	met, unknown := evaluateConditions(cond, map[string]string{"aws:requestedregion": "eu-west-1"})
	if met || unknown {
		t.Fatalf("expected (false, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_stringEquals_multiValueOR(t *testing.T) {
	cond := map[string]map[string][]string{
		"StringEquals": {"aws:requestedregion": {"us-east-1", "eu-west-1"}},
	}
	met, unknown := evaluateConditions(cond, map[string]string{"aws:requestedregion": "eu-west-1"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_stringNotEquals_match(t *testing.T) {
	cond := map[string]map[string][]string{
		"StringNotEquals": {"aws:requestedregion": {"us-east-1"}},
	}
	met, unknown := evaluateConditions(cond, map[string]string{"aws:requestedregion": "eu-west-1"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_stringLike_wildcardMatch(t *testing.T) {
	cond := map[string]map[string][]string{
		"StringLike": {"aws:requestedregion": {"us-*"}},
	}
	met, unknown := evaluateConditions(cond, map[string]string{"aws:requestedregion": "us-east-1"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_stringLike_noMatch(t *testing.T) {
	cond := map[string]map[string][]string{
		"StringLike": {"aws:requestedregion": {"us-*"}},
	}
	met, unknown := evaluateConditions(cond, map[string]string{"aws:requestedregion": "eu-west-1"})
	if met || unknown {
		t.Fatalf("expected (false, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_arnLike_wildcardMatch(t *testing.T) {
	cond := map[string]map[string][]string{
		"ArnLike": {"aws:requestedregion": {"arn:aws:s3:::my-bucket*"}},
	}
	met, unknown := evaluateConditions(cond, map[string]string{"aws:requestedregion": "arn:aws:s3:::my-bucket-prod"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_boolEquals_match(t *testing.T) {
	cond := map[string]map[string][]string{
		"Bool": {"aws:requestedregion": {"true"}},
	}
	met, unknown := evaluateConditions(cond, map[string]string{"aws:requestedregion": "true"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_ipAddress_exactMatch(t *testing.T) {
	cond := map[string]map[string][]string{
		"IpAddress": {"aws:sourceip": {"192.168.1.1"}},
	}
	met, unknown := evaluateConditions(cond, map[string]string{"aws:sourceip": "192.168.1.1"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_ipAddress_cidrMatch(t *testing.T) {
	cond := map[string]map[string][]string{
		"IpAddress": {"aws:sourceip": {"10.0.0.0/8"}},
	}
	met, unknown := evaluateConditions(cond, map[string]string{"aws:sourceip": "10.1.2.3"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_ipAddress_cidrNoMatch(t *testing.T) {
	cond := map[string]map[string][]string{
		"IpAddress": {"aws:sourceip": {"10.0.0.0/8"}},
	}
	met, unknown := evaluateConditions(cond, map[string]string{"aws:sourceip": "192.168.1.1"})
	if met || unknown {
		t.Fatalf("expected (false, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_unknownOperator_signalsUnknown(t *testing.T) {
	cond := map[string]map[string][]string{
		"SomeUnknownOp": {"aws:requestedregion": {"us-east-1"}},
	}
	met, unknown := evaluateConditions(cond, map[string]string{"aws:requestedregion": "us-east-1"})
	if met || !unknown {
		t.Fatalf("expected (false, true), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_missingContextKey_notMet(t *testing.T) {
	cond := map[string]map[string][]string{
		"StringEquals": {"aws:principalarn": {"arn:aws:iam::123:user/admin"}},
	}
	// aws:principalarn is not in the context
	met, unknown := evaluateConditions(cond, map[string]string{"aws:requestedregion": "us-east-1"})
	if met || unknown {
		t.Fatalf("expected (false, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_dateLessThan_match(t *testing.T) {
	cond := map[string]map[string][]string{
		"DateLessThan": {"aws:currenttime": {"2026-04-24T00:00:00Z"}},
	}
	met, unknown := evaluateConditions(cond, map[string]string{"aws:currenttime": "2026-04-23T00:00:00Z"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_dateGreaterThan_noMatch(t *testing.T) {
	cond := map[string]map[string][]string{
		"DateGreaterThan": {"aws:currenttime": {"2026-04-24T00:00:00Z"}},
	}
	met, unknown := evaluateConditions(cond, map[string]string{"aws:currenttime": "2026-04-23T00:00:00Z"})
	if met || unknown {
		t.Fatalf("expected (false, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_dateNotEquals_match(t *testing.T) {
	cond := map[string]map[string][]string{
		"DateNotEquals": {"aws:currenttime": {"2026-04-24T00:00:00Z", "2026-04-25T00:00:00Z"}},
	}
	met, unknown := evaluateConditions(cond, map[string]string{"aws:currenttime": "2026-04-23T00:00:00Z"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_null_true_matchesMissingKey(t *testing.T) {
	cond := map[string]map[string][]string{
		"Null": {"aws:principalarn": {"true"}},
	}
	met, unknown := evaluateConditions(cond, map[string]string{"aws:requestedregion": "us-east-1"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_null_false_matchesPresentKey(t *testing.T) {
	cond := map[string]map[string][]string{
		"Null": {"aws:principalarn": {"false"}},
	}
	met, unknown := evaluateConditions(cond, map[string]string{"aws:principalarn": "arn:aws:iam::123456789012:user/test"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_null_false_failsMissingKey(t *testing.T) {
	cond := map[string]map[string][]string{
		"Null": {"aws:principalarn": {"false"}},
	}
	met, unknown := evaluateConditions(cond, map[string]string{"aws:requestedregion": "us-east-1"})
	if met || unknown {
		t.Fatalf("expected (false, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_stringEqualsIfExists_missingKey_met(t *testing.T) {
	cond := map[string]map[string][]string{
		"StringEqualsIfExists": {"aws:principaltag/team": {"platform"}},
	}
	met, unknown := evaluateConditions(cond, map[string]string{"aws:requestedregion": "us-east-1"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_stringEqualsIfExists_presentKeyMatch(t *testing.T) {
	cond := map[string]map[string][]string{
		"StringEqualsIfExists": {"aws:requestedregion": {"us-east-1"}},
	}
	met, unknown := evaluateConditions(cond, map[string]string{"aws:requestedregion": "us-east-1"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_stringEqualsIfExists_presentKeyNoMatch(t *testing.T) {
	cond := map[string]map[string][]string{
		"StringEqualsIfExists": {"aws:requestedregion": {"us-east-1"}},
	}
	met, unknown := evaluateConditions(cond, map[string]string{"aws:requestedregion": "eu-west-1"})
	if met || unknown {
		t.Fatalf("expected (false, false), got (%v, %v)", met, unknown)
	}
}

// ─── Numeric condition operator unit tests ────────────────────────────────────

func TestEvaluateConditions_numericEquals_match(t *testing.T) {
	cond := map[string]map[string][]string{
		"NumericEquals": {"aws:requestedcontentlength": {"100"}},
	}
	met, unknown := evaluateConditions(cond, map[string]string{"aws:requestedcontentlength": "100"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_numericEquals_noMatch(t *testing.T) {
	cond := map[string]map[string][]string{
		"NumericEquals": {"aws:requestedcontentlength": {"100"}},
	}
	met, unknown := evaluateConditions(cond, map[string]string{"aws:requestedcontentlength": "99"})
	if met || unknown {
		t.Fatalf("expected (false, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_numericLessThan_match(t *testing.T) {
	cond := map[string]map[string][]string{
		"NumericLessThan": {"aws:requestedcontentlength": {"1000"}},
	}
	met, unknown := evaluateConditions(cond, map[string]string{"aws:requestedcontentlength": "50"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_numericLessThan_noMatch(t *testing.T) {
	cond := map[string]map[string][]string{
		"NumericLessThan": {"aws:requestedcontentlength": {"1000"}},
	}
	met, unknown := evaluateConditions(cond, map[string]string{"aws:requestedcontentlength": "5000"})
	if met || unknown {
		t.Fatalf("expected (false, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_numericGreaterThan_match(t *testing.T) {
	cond := map[string]map[string][]string{
		"NumericGreaterThan": {"aws:requestedcontentlength": {"10"}},
	}
	met, unknown := evaluateConditions(cond, map[string]string{"aws:requestedcontentlength": "42"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_numericNotEquals_match(t *testing.T) {
	cond := map[string]map[string][]string{
		"NumericNotEquals": {"aws:requestedcontentlength": {"0"}},
	}
	met, unknown := evaluateConditions(cond, map[string]string{"aws:requestedcontentlength": "512"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_numericNotEquals_noMatch(t *testing.T) {
	cond := map[string]map[string][]string{
		"NumericNotEquals": {"aws:requestedcontentlength": {"512"}},
	}
	met, unknown := evaluateConditions(cond, map[string]string{"aws:requestedcontentlength": "512"})
	if met || unknown {
		t.Fatalf("expected (false, false), got (%v, %v)", met, unknown)
	}
}

// ─── Policy variable expansion unit tests ─────────────────────────────────────

func TestExpandPolicyVariables_substitutesUsername(t *testing.T) {
	reqCtx := map[string]string{"aws:username": "alice"}
	got := expandPolicyVariables("arn:aws:s3:::home/${aws:username}/*", reqCtx)
	want := "arn:aws:s3:::home/alice/*"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestExpandPolicyVariables_substitutesUserid(t *testing.T) {
	reqCtx := map[string]string{"aws:userid": "AIDAEXAMPLE"}
	got := expandPolicyVariables("arn:aws:iam::000000000000:user/${aws:userid}", reqCtx)
	want := "arn:aws:iam::000000000000:user/AIDAEXAMPLE"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestExpandPolicyVariables_unknownVariableLeftLiteral(t *testing.T) {
	reqCtx := map[string]string{}
	got := expandPolicyVariables("arn:aws:s3:::home/${aws:unknown}/*", reqCtx)
	want := "arn:aws:s3:::home/${aws:unknown}/*"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestExpandPolicyVariables_noVariables(t *testing.T) {
	reqCtx := map[string]string{"aws:username": "alice"}
	got := expandPolicyVariables("arn:aws:s3:::my-bucket/*", reqCtx)
	want := "arn:aws:s3:::my-bucket/*"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestExpandPolicyVariables_caseInsensitiveVariable(t *testing.T) {
	reqCtx := map[string]string{"aws:username": "bob"}
	// Variable name in different case — should still resolve.
	got := expandPolicyVariables("arn:aws:s3:::home/${AWS:Username}/*", reqCtx)
	want := "arn:aws:s3:::home/bob/*"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestStatementMatchesResource_expandsUsernameVariable(t *testing.T) {
	stmt := iamPolicyStatement{
		Resource: []string{"arn:aws:s3:::home/${aws:username}/*"},
	}
	reqCtx := map[string]string{"aws:username": "carol"}
	if !statementMatchesResource("arn:aws:s3:::home/carol/file.txt", stmt, reqCtx) {
		t.Fatal("expected resource to match after variable expansion")
	}
}

func TestStatementMatchesResource_expandsUsername_noMatch(t *testing.T) {
	stmt := iamPolicyStatement{
		Resource: []string{"arn:aws:s3:::home/${aws:username}/*"},
	}
	reqCtx := map[string]string{"aws:username": "carol"}
	if statementMatchesResource("arn:aws:s3:::home/mallory/file.txt", stmt, reqCtx) {
		t.Fatal("expected resource to NOT match after variable expansion")
	}
}
