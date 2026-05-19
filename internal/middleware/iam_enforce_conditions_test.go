package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/state"
)

// ─── End-to-end middleware tests with Condition blocks ────────────────────────

func TestIAMEnforce_condition_regionAllows(t *testing.T) {
	st := state.NewMemoryStore()
	// Policy allows SQS only in us-east-1.
	seedIAMUserWithPolicies(t, st, "test", []string{
		`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:CreateQueue","Resource":"*","Condition":{"StringEquals":{"aws:RequestedRegion":"us-east-1"}}}]}`,
	}, nil)

	called := false
	h := IAMEnforce(true, st, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueName":"demo"}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")
	// Credential scope has region us-east-1.
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260423/us-east-1/sqs/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	req.Header.Set("X-Amz-Date", "20260423T000000Z")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected next handler to be called")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestIAMEnforce_condition_regionBlocks(t *testing.T) {
	st := state.NewMemoryStore()
	// Policy allows SQS only in us-east-1, but request comes from eu-west-1.
	seedIAMUserWithPolicies(t, st, "test", []string{
		`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:CreateQueue","Resource":"*","Condition":{"StringEquals":{"aws:RequestedRegion":"us-east-1"}}}]}`,
	}, nil)

	h := IAMEnforce(true, st, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueName":"demo"}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")
	// Credential scope has region eu-west-1.
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260423/eu-west-1/sqs/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	req.Header.Set("X-Amz-Date", "20260423T000000Z")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	// Condition not met → Allow statement doesn't apply → NoMatch → deny.
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected %d, got %d", http.StatusForbidden, rec.Code)
	}
}

func TestIAMEnforce_condition_unknownOperator_denies(t *testing.T) {
	st := state.NewMemoryStore()
	// Policy has an unknown condition operator — must fail closed.
	seedIAMUserWithPolicies(t, st, "test", []string{
		`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:CreateQueue","Resource":"*","Condition":{"WeirdUnknownOp":{"aws:RequestedRegion":"us-east-1"}}}]}`,
	}, nil)

	h := IAMEnforce(true, st, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueName":"demo"}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260423/us-east-1/sqs/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	req.Header.Set("X-Amz-Date", "20260423T000000Z")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected %d (fail closed), got %d", http.StatusForbidden, rec.Code)
	}
}

func TestIAMEnforce_condition_denyWithRegion_blocksMatchingRegion(t *testing.T) {
	st := state.NewMemoryStore()
	// Allow everything, then deny SQS in eu-west-1.
	seedIAMUserWithPolicies(t, st, "test", []string{
		`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:*","Resource":"*"},{"Effect":"Deny","Action":"sqs:*","Resource":"*","Condition":{"StringEquals":{"aws:RequestedRegion":"eu-west-1"}}}]}`,
	}, nil)

	h := IAMEnforce(true, st, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueName":"demo"}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260423/eu-west-1/sqs/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	req.Header.Set("X-Amz-Date", "20260423T000000Z")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected %d, got %d", http.StatusForbidden, rec.Code)
	}
}

func TestIAMEnforce_condition_denyWithRegion_allowsOtherRegion(t *testing.T) {
	st := state.NewMemoryStore()
	// Allow everything, then deny SQS in eu-west-1 only.
	seedIAMUserWithPolicies(t, st, "test", []string{
		`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:*","Resource":"*"},{"Effect":"Deny","Action":"sqs:*","Resource":"*","Condition":{"StringEquals":{"aws:RequestedRegion":"eu-west-1"}}}]}`,
	}, nil)

	called := false
	h := IAMEnforce(true, st, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueName":"demo"}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")
	// us-east-1 is not blocked by the deny condition.
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260423/us-east-1/sqs/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	req.Header.Set("X-Amz-Date", "20260423T000000Z")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected next handler to be called")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestIAMEnforce_condition_principalArn_allowsMatchingUserArn(t *testing.T) {
	st := state.NewMemoryStore()

	user := map[string]any{
		"UserName": "test-user",
		"UserId":   "AIDAEXAMPLE123456",
		"Arn":      "arn:aws:iam::123456789012:user/test-user",
		"AccessKeys": []map[string]string{
			{"AccessKeyId": "test"},
		},
		"InlinePolicies": map[string]string{
			"inline-1": `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:CreateQueue","Resource":"*","Condition":{"StringEquals":{"aws:PrincipalArn":"arn:aws:iam::123456789012:user/test-user"}}}]}`,
		},
	}
	b, err := json.Marshal(user)
	if err != nil {
		t.Fatalf("marshal user: %v", err)
	}
	if err := st.Set(context.Background(), "iam:users", "test-user", string(b)); err != nil {
		t.Fatalf("seed user: %v", err)
	}

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
		t.Fatalf("expected %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestIAMEnforce_condition_principalAccount_blocksMismatch(t *testing.T) {
	st := state.NewMemoryStore()

	user := map[string]any{
		"UserName": "test-user",
		"Arn":      "arn:aws:iam::123456789012:user/test-user",
		"AccessKeys": []map[string]string{
			{"AccessKeyId": "test"},
		},
		"InlinePolicies": map[string]string{
			"inline-1": `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:CreateQueue","Resource":"*","Condition":{"StringEquals":{"aws:PrincipalAccount":"000000000000"}}}]}`,
		},
	}
	b, err := json.Marshal(user)
	if err != nil {
		t.Fatalf("marshal user: %v", err)
	}
	if err := st.Set(context.Background(), "iam:users", "test-user", string(b)); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	h := IAMEnforce(true, st, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueName":"demo"}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260423/us-east-1/sqs/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	req.Header.Set("X-Amz-Date", "20260423T000000Z")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected %d, got %d", http.StatusForbidden, rec.Code)
	}
}

func TestIAMEnforce_condition_userID_allowsMatchingUserID(t *testing.T) {
	st := state.NewMemoryStore()

	user := map[string]any{
		"UserName": "test-user",
		"UserId":   "AIDAUSERIDMATCH",
		"AccessKeys": []map[string]string{
			{"AccessKeyId": "test"},
		},
		"InlinePolicies": map[string]string{
			"inline-1": `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:CreateQueue","Resource":"*","Condition":{"StringEquals":{"aws:userid":"AIDAUSERIDMATCH"}}}]}`,
		},
	}
	b, err := json.Marshal(user)
	if err != nil {
		t.Fatalf("marshal user: %v", err)
	}
	if err := st.Set(context.Background(), "iam:users", "test-user", string(b)); err != nil {
		t.Fatalf("seed user: %v", err)
	}

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
		t.Fatalf("expected %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestIAMEnforce_condition_currentTime_allowsMatchingSigV4Time(t *testing.T) {
	st := state.NewMemoryStore()
	seedIAMUserWithPolicies(t, st, "test", []string{
		`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:CreateQueue","Resource":"*","Condition":{"StringEquals":{"aws:CurrentTime":"2026-04-23T00:00:00Z"}}}]}`,
	}, nil)

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
		t.Fatalf("expected %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestIAMEnforce_condition_dateLessThan_allowsEarlierRequestTime(t *testing.T) {
	st := state.NewMemoryStore()
	seedIAMUserWithPolicies(t, st, "test", []string{
		`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:CreateQueue","Resource":"*","Condition":{"DateLessThan":{"aws:CurrentTime":"2026-04-24T00:00:00Z"}}}]}`,
	}, nil)

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
		t.Fatalf("expected %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestIAMEnforce_condition_dateGreaterThan_deniesWhenNotSatisfied(t *testing.T) {
	st := state.NewMemoryStore()
	seedIAMUserWithPolicies(t, st, "test", []string{
		`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:CreateQueue","Resource":"*","Condition":{"DateGreaterThan":{"aws:CurrentTime":"2026-04-24T00:00:00Z"}}}]}`,
	}, nil)

	h := IAMEnforce(true, st, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueName":"demo"}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260423/us-east-1/sqs/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	req.Header.Set("X-Amz-Date", "20260423T000000Z")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected %d, got %d", http.StatusForbidden, rec.Code)
	}
}

func TestIAMEnforce_condition_nullFalse_allowsWhenPrincipalArnPresent(t *testing.T) {
	st := state.NewMemoryStore()
	seedIAMUserWithPolicies(t, st, "test", []string{
		`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:CreateQueue","Resource":"*","Condition":{"Null":{"aws:PrincipalArn":"false"}}}]}`,
	}, nil)

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
		t.Fatalf("expected %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestIAMEnforce_condition_nullTrue_deniesWhenPrincipalArnPresent(t *testing.T) {
	st := state.NewMemoryStore()
	seedIAMUserWithPolicies(t, st, "test", []string{
		`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:CreateQueue","Resource":"*","Condition":{"Null":{"aws:PrincipalArn":"true"}}}]}`,
	}, nil)

	h := IAMEnforce(true, st, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueName":"demo"}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260423/us-east-1/sqs/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	req.Header.Set("X-Amz-Date", "20260423T000000Z")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected %d, got %d", http.StatusForbidden, rec.Code)
	}
}

func TestIAMEnforce_condition_stringEqualsIfExists_allowsWhenKeyMissing(t *testing.T) {
	st := state.NewMemoryStore()
	seedIAMUserWithPolicies(t, st, "test", []string{
		`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:CreateQueue","Resource":"*","Condition":{"StringEqualsIfExists":{"aws:PrincipalTag/team":"platform"}}}]}`,
	}, nil)

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
		t.Fatalf("expected %d, got %d", http.StatusNoContent, rec.Code)
	}
}

// ─── Numeric condition operator middleware tests ───────────────────────────────

// TestIAMEnforce_condition_numericLessThan_allowsSmallBody verifies that a
// policy with NumericLessThan on aws:RequestedContentLength allows requests
// whose Content-Length is below the threshold.
func TestIAMEnforce_condition_numericLessThan_allowsSmallBody(t *testing.T) {
	st := state.NewMemoryStore()
	// Allow only if request body is smaller than 1000 bytes.
	seedIAMUserWithPolicies(t, st, "test", []string{
		`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:CreateQueue","Resource":"*","Condition":{"NumericLessThan":{"aws:RequestedContentLength":"1000"}}}]}`,
	}, nil)

	called := false
	h := IAMEnforce(true, st, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	body := `{"QueueName":"demo"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.ContentLength = int64(len(body))
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
		t.Fatalf("expected %d, got %d", http.StatusNoContent, rec.Code)
	}
}

// TestIAMEnforce_condition_numericLessThan_deniesLargeBody verifies that the
// same NumericLessThan policy blocks a request whose Content-Length exceeds
// the threshold.
func TestIAMEnforce_condition_numericLessThan_deniesLargeBody(t *testing.T) {
	st := state.NewMemoryStore()
	seedIAMUserWithPolicies(t, st, "test", []string{
		`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:CreateQueue","Resource":"*","Condition":{"NumericLessThan":{"aws:RequestedContentLength":"10"}}}]}`,
	}, nil)

	h := IAMEnforce(true, st, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	body := `{"QueueName":"demo"}` // 20 bytes — exceeds threshold of 10
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260423/us-east-1/sqs/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	req.Header.Set("X-Amz-Date", "20260423T000000Z")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected %d, got %d", http.StatusForbidden, rec.Code)
	}
}

// ─── Policy variable substitution middleware tests ────────────────────────────

// TestIAMEnforce_policyVariable_username_allowsMatchingUser verifies that
// ${aws:username} in a Resource pattern is expanded to the calling user's name
// before matching, allowing requests that match the expanded ARN.
func TestIAMEnforce_policyVariable_username_allowsMatchingUser(t *testing.T) {
	st := state.NewMemoryStore()
	// Policy allows queues whose name starts with the caller's username.
	seedIAMUserWithPolicies(t, st, "alice", []string{
		`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:CreateQueue","Resource":"arn:aws:sqs:us-east-1:000000000000:${aws:username}-*"}]}`,
	}, nil)

	called := false
	h := IAMEnforce(true, st, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	// Queue name "alice-myqueue" should match arn:aws:sqs:...:alice-*
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueName":"alice-myqueue"}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=alice/20260423/us-east-1/sqs/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	req.Header.Set("X-Amz-Date", "20260423T000000Z")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected next handler to be called")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected %d, got %d", http.StatusNoContent, rec.Code)
	}
}

// TestIAMEnforce_policyVariable_username_deniesOtherUser verifies that
// ${aws:username} expansion causes a deny when the resource belongs to a
// different user than the caller.
func TestIAMEnforce_policyVariable_username_deniesOtherUser(t *testing.T) {
	st := state.NewMemoryStore()
	seedIAMUserWithPolicies(t, st, "alice", []string{
		`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:CreateQueue","Resource":"arn:aws:sqs:us-east-1:000000000000:${aws:username}-*"}]}`,
	}, nil)

	h := IAMEnforce(true, st, zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	// Queue name "bob-myqueue" should NOT match arn:aws:sqs:...:alice-*
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueName":"bob-myqueue"}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=alice/20260423/us-east-1/sqs/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	req.Header.Set("X-Amz-Date", "20260423T000000Z")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected %d, got %d", http.StatusForbidden, rec.Code)
	}
}
