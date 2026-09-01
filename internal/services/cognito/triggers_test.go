package cognito

// triggers_test.go — failing-first coverage for issue #1171: LambdaConfig
// triggers were stored and round-tripped since #536 but never invoked.
//
// These are white-box package tests (package cognito, not cognito_test),
// following the same pattern secretsmanager's rotation_test.go and
// apigateway's handler_execution_test.go use for a fake
// events.FunctionSyncInvoker: construct the Service directly, wire a fake
// invoker via InitLambdaInvoker, and drive requests through s.Dispatch with
// httptest — no real Lambda container, no full router.
//
// They deliberately do NOT live under tests/integration/cognito: that
// package's helpers.TestServer builds every service through router.New with
// no seam to inject a fake invoker, and host-mode Lambda container invoke is
// broken on this box (see the environment notes in the issue). CI's
// Docker-gated Lambda integration suite is what proves the real container
// invocation path; these tests prove the dispatch, event-shape, and
// response-merge logic that sits in front of it.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/state"
)

// ─── test scaffolding ──────────────────────────────────────────────────────

func newTriggerTestService(t *testing.T) *Service {
	t.Helper()
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	return New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())
}

// dispatch drives one Cognito API call directly through s.Dispatch — the
// same X-Amz-Target JSON 1.1 protocol every real Cognito SDK client uses,
// and the one tests/integration/cognito exercises.
func dispatch(t *testing.T, s *Service, op string, body map[string]any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s body: %v", op, err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", targetPrefix+op)
	rec := httptest.NewRecorder()
	s.Dispatch(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: unmarshal response %q: %v", op, rec.Body.String(), err)
		}
	}
	return rec, out
}

// invocation is one call the fake invoker recorded.
type invocation struct {
	FunctionName string
	Event        map[string]any // the decoded trigger envelope
}

// fakeTriggerInvoker is a minimal events.FunctionSyncInvoker: it records
// every call and, for a configured function name, runs a caller-supplied
// handler against the decoded envelope — exactly the "receive event, mutate
// event.response, return event" shape every real Lambda trigger blueprint
// AWS documents follows. A function name with no registered handler behaves
// like Invoke() does for a function that does not exist: (nil, nil).
type fakeTriggerInvoker struct {
	mu       sync.Mutex
	calls    []invocation
	handlers map[string]func(event map[string]any) (*events.InvokeOutcome, error)
}

func newFakeTriggerInvoker() *fakeTriggerInvoker {
	return &fakeTriggerInvoker{handlers: map[string]func(map[string]any) (*events.InvokeOutcome, error){}}
}

// on registers a handler for functionName. respond is merged into the
// event's "response" object before it is echoed back, exactly as a real
// trigger function's `event.response = {...}; return event;` would.
func (f *fakeTriggerInvoker) on(functionName string, respond map[string]any) {
	f.handlers[functionName] = func(event map[string]any) (*events.InvokeOutcome, error) {
		event["response"] = respond
		out, err := json.Marshal(event)
		if err != nil {
			return nil, err
		}
		return &events.InvokeOutcome{Payload: out}, nil
	}
}

// onError registers functionName to fail every invocation the way a
// function that threw an error does: FunctionError set, Payload carrying the
// error message a real Lambda runtime would report.
func (f *fakeTriggerInvoker) onError(functionName, message string) {
	f.handlers[functionName] = func(map[string]any) (*events.InvokeOutcome, error) {
		msg, _ := json.Marshal(message)
		return &events.InvokeOutcome{FunctionError: "Unhandled", Payload: msg}, nil
	}
}

func (f *fakeTriggerInvoker) Invoke(_ context.Context, functionName string, payload []byte) (*events.InvokeOutcome, error) {
	var event map[string]any
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.calls = append(f.calls, invocation{FunctionName: functionName, Event: event})
	handler := f.handlers[functionName]
	f.mu.Unlock()
	if handler == nil {
		return nil, nil
	}
	return handler(event)
}

func (f *fakeTriggerInvoker) only(t *testing.T, functionName string) invocation {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c.FunctionName == functionName {
			return c
		}
	}
	t.Fatalf("fake invoker never received a call for function %q (calls: %+v)", functionName, f.calls)
	return invocation{}
}

func (f *fakeTriggerInvoker) count(functionName string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.FunctionName == functionName {
			n++
		}
	}
	return n
}

// fakeMailer captures every email sent, so a CustomMessage override can be
// asserted against the message that actually went out.
type fakeMailer struct {
	mu   sync.Mutex
	sent []sentMail
}

type sentMail struct {
	To      []string
	Subject string
	Body    string
}

func (m *fakeMailer) Send(_ context.Context, _ string, to []string, subject, body, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, sentMail{To: to, Subject: subject, Body: body})
	return nil
}
func (m *fakeMailer) SendRaw(context.Context, string, []string, []byte) error { return nil }

func (m *fakeMailer) last(t *testing.T) sentMail {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sent) == 0 {
		t.Fatal("fakeMailer: no mail sent")
	}
	return m.sent[len(m.sent)-1]
}

// createPool creates a user pool with the given LambdaConfig and returns its ID.
func createTriggerPool(t *testing.T, s *Service, lambdaConfig map[string]any) string {
	t.Helper()
	body := map[string]any{"PoolName": "trigger-pool"}
	if lambdaConfig != nil {
		body["LambdaConfig"] = lambdaConfig
	}
	rec, out := dispatch(t, s, "CreateUserPool", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("CreateUserPool: status %d body %s", rec.Code, rec.Body.String())
	}
	pool, _ := out["UserPool"].(map[string]any)
	id, _ := pool["Id"].(string)
	if id == "" {
		t.Fatal("CreateUserPool: empty pool Id")
	}
	return id
}

func createTriggerClient(t *testing.T, s *Service, poolID string) string {
	t.Helper()
	rec, out := dispatch(t, s, "CreateUserPoolClient", map[string]any{
		"UserPoolId": poolID, "ClientName": "trigger-client",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("CreateUserPoolClient: status %d body %s", rec.Code, rec.Body.String())
	}
	client, _ := out["UserPoolClient"].(map[string]any)
	id, _ := client["ClientId"].(string)
	if id == "" {
		t.Fatal("CreateUserPoolClient: empty ClientId")
	}
	return id
}

// jwtClaims decodes (without verifying — these are test assertions on our
// own emulator's output, not a security boundary) the payload of a JWT.
func jwtClaims(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("malformed JWT: %q", token)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode JWT payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("unmarshal JWT claims: %v", err)
	}
	return claims
}

// ─── PreSignUp ─────────────────────────────────────────────────────────────

func TestTrigger_PreSignUp_AutoConfirmAndVerify(t *testing.T) {
	s := newTriggerTestService(t)
	inv := newFakeTriggerInvoker()
	s.InitLambdaInvoker(inv)

	poolID := createTriggerPool(t, s, map[string]any{"PreSignUp": "arn:aws:lambda:us-east-1:123456789012:function:pre-signup"})
	clientID := createTriggerClient(t, s, poolID)
	inv.on("pre-signup", map[string]any{"autoConfirmUser": true, "autoVerifyEmail": true, "autoVerifyPhone": false})

	rec, out := dispatch(t, s, "SignUp", map[string]any{
		"ClientId": clientID, "Username": "alice", "Password": "P@ssw0rd1",
		"UserAttributes": []map[string]any{{"Name": "email", "Value": "alice@example.com"}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("SignUp: status %d body %s", rec.Code, rec.Body.String())
	}
	if confirmed, _ := out["UserConfirmed"].(bool); !confirmed {
		t.Errorf("UserConfirmed = %v, want true (PreSignUp set autoConfirmUser)", out["UserConfirmed"])
	}

	call := inv.only(t, "pre-signup")
	if call.Event["triggerSource"] != "PreSignUp_SignUp" {
		t.Errorf("triggerSource = %v, want PreSignUp_SignUp", call.Event["triggerSource"])
	}
	if call.Event["userPoolId"] != poolID {
		t.Errorf("userPoolId = %v, want %s", call.Event["userPoolId"], poolID)
	}
	if call.Event["userName"] != "alice" {
		t.Errorf("userName = %v, want alice", call.Event["userName"])
	}
	req, _ := call.Event["request"].(map[string]any)
	attrs, _ := req["userAttributes"].(map[string]any)
	if attrs["email"] != "alice@example.com" {
		t.Errorf("request.userAttributes.email = %v, want alice@example.com", attrs["email"])
	}
	callerCtx, _ := call.Event["callerContext"].(map[string]any)
	if callerCtx["clientId"] != clientID {
		t.Errorf("callerContext.clientId = %v, want %s", callerCtx["clientId"], clientID)
	}

	// AWS fires PostConfirmation_ConfirmSignUp within the SignUp call itself
	// once PreSignUp auto-confirms — there is no separate ConfirmSignUp call
	// to hang a Lambda-driven auto-confirm off of.
	rec2, out2 := dispatch(t, s, "AdminGetUser", map[string]any{"UserPoolId": poolID, "Username": "alice"})
	if rec2.Code != http.StatusOK {
		t.Fatalf("AdminGetUser: status %d body %s", rec2.Code, rec2.Body.String())
	}
	if out2["UserStatus"] != "CONFIRMED" {
		t.Errorf("UserStatus = %v, want CONFIRMED", out2["UserStatus"])
	}
}

func TestTrigger_PreSignUp_FunctionErrorFailsSignUp(t *testing.T) {
	s := newTriggerTestService(t)
	inv := newFakeTriggerInvoker()
	s.InitLambdaInvoker(inv)

	poolID := createTriggerPool(t, s, map[string]any{"PreSignUp": "arn:aws:lambda:us-east-1:123456789012:function:pre-signup"})
	clientID := createTriggerClient(t, s, poolID)
	inv.onError("pre-signup", "custom domain not allowed")

	rec, out := dispatch(t, s, "SignUp", map[string]any{
		"ClientId": clientID, "Username": "bob", "Password": "P@ssw0rd1",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("SignUp: status %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if out["__type"] != "UserLambdaValidationException" {
		t.Errorf("error type = %v, want UserLambdaValidationException", out["__type"])
	}
	if msg, _ := out["message"].(string); !strings.Contains(msg, "custom domain not allowed") {
		t.Errorf("error message %q does not surface the trigger's own error text", msg)
	}

	// The user must not have been created — SignUp failed before persisting.
	rec2, _ := dispatch(t, s, "AdminGetUser", map[string]any{"UserPoolId": poolID, "Username": "bob"})
	if rec2.Code == http.StatusOK {
		t.Error("AdminGetUser succeeded for a user whose SignUp should have failed at PreSignUp")
	}
}

func TestTrigger_PreSignUp_ConfiguredButNoInvokerWired_Fails(t *testing.T) {
	// A pool with a configured trigger but a Service that never had
	// InitLambdaInvoker called (a wiring bug, or — before this issue —
	// every production instance) must fail loudly, not silently proceed as
	// if no trigger were configured. This is the §2.1 shape-2 failure the
	// issue exists to remove.
	s := newTriggerTestService(t)
	poolID := createTriggerPool(t, s, map[string]any{"PreSignUp": "arn:aws:lambda:us-east-1:123456789012:function:pre-signup"})
	clientID := createTriggerClient(t, s, poolID)

	rec, out := dispatch(t, s, "SignUp", map[string]any{
		"ClientId": clientID, "Username": "carol", "Password": "P@ssw0rd1",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("SignUp: status %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if out["__type"] != "UserLambdaValidationException" {
		t.Errorf("error type = %v, want UserLambdaValidationException", out["__type"])
	}
}

func TestTrigger_PreSignUp_NotConfigured_SignUpUnaffected(t *testing.T) {
	// Regression: a pool with no LambdaConfig at all must behave exactly as
	// it did before this issue.
	s := newTriggerTestService(t)
	poolID := createTriggerPool(t, s, nil)
	clientID := createTriggerClient(t, s, poolID)

	rec, out := dispatch(t, s, "SignUp", map[string]any{
		"ClientId": clientID, "Username": "dave", "Password": "P@ssw0rd1",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("SignUp: status %d body %s", rec.Code, rec.Body.String())
	}
	if confirmed, _ := out["UserConfirmed"].(bool); confirmed {
		t.Error("UserConfirmed = true with no PreSignUp trigger configured")
	}
}

// ─── PostConfirmation ──────────────────────────────────────────────────────

func TestTrigger_PostConfirmation_FireAndForget(t *testing.T) {
	s := newTriggerTestService(t)
	inv := newFakeTriggerInvoker()
	s.InitLambdaInvoker(inv)
	inv.onError("post-confirm", "downstream provisioning failed")

	poolID := createTriggerPool(t, s, map[string]any{"PostConfirmation": "arn:aws:lambda:us-east-1:123456789012:function:post-confirm"})
	clientID := createTriggerClient(t, s, poolID)

	dispatch(t, s, "SignUp", map[string]any{
		"ClientId": clientID, "Username": "erin", "Password": "P@ssw0rd1",
		"UserAttributes": []map[string]any{{"Name": "email", "Value": "erin@example.com"}},
	})

	rec, _ := dispatch(t, s, "ConfirmSignUp", map[string]any{
		"ClientId": clientID, "Username": "erin", "ConfirmationCode": mustConfirmationCode(t, s, poolID, "erin"),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("ConfirmSignUp: status %d body %s (PostConfirmation errors must not fail ConfirmSignUp — AWS does not roll back confirmation on this trigger's error)", rec.Code, rec.Body.String())
	}

	call := inv.only(t, "post-confirm")
	if call.Event["triggerSource"] != "PostConfirmation_ConfirmSignUp" {
		t.Errorf("triggerSource = %v, want PostConfirmation_ConfirmSignUp", call.Event["triggerSource"])
	}
	req, _ := call.Event["request"].(map[string]any)
	attrs, _ := req["userAttributes"].(map[string]any)
	if attrs["email"] != "erin@example.com" {
		t.Errorf("request.userAttributes.email = %v, want erin@example.com", attrs["email"])
	}
}

// mustConfirmationCode reaches into the store to read a freshly-created
// user's confirmation code — there is no API that returns it (the code is
// delivered out of band), so tests that need to complete ConfirmSignUp
// without a mailer configured have to read it directly.
func mustConfirmationCode(t *testing.T, s *Service, poolID, username string) string {
	t.Helper()
	u, err := s.loadUser(context.Background(), poolID, username)
	if err != nil || u == nil {
		t.Fatalf("loadUser(%s): %v", username, err)
	}
	if u.ConfirmationCode == "" {
		t.Fatalf("user %s has no confirmation code", username)
	}
	return u.ConfirmationCode
}

// ─── PreTokenGeneration ────────────────────────────────────────────────────

func TestTrigger_PreTokenGeneration_ClaimsOverride(t *testing.T) {
	s := newTriggerTestService(t)
	inv := newFakeTriggerInvoker()
	s.InitLambdaInvoker(inv)

	poolID := createTriggerPool(t, s, map[string]any{"PreTokenGeneration": "arn:aws:lambda:us-east-1:123456789012:function:pre-token"})
	clientID := createTriggerClient(t, s, poolID)
	inv.on("pre-token", map[string]any{
		"claimsOverrideDetails": map[string]any{
			"claimsToAddOrOverride": map[string]any{"custom:tenant": "acme"},
			"claimsToSuppress":      []string{"email"},
		},
	})

	dispatch(t, s, "SignUp", map[string]any{
		"ClientId": clientID, "Username": "frank", "Password": "P@ssw0rd1",
		"UserAttributes": []map[string]any{{"Name": "email", "Value": "frank@example.com"}},
	})
	dispatch(t, s, "ConfirmSignUp", map[string]any{
		"ClientId": clientID, "Username": "frank", "ConfirmationCode": mustConfirmationCode(t, s, poolID, "frank"),
	})

	rec, out := dispatch(t, s, "InitiateAuth", map[string]any{
		"ClientId": clientID, "AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]any{"USERNAME": "frank", "PASSWORD": "P@ssw0rd1"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("InitiateAuth: status %d body %s", rec.Code, rec.Body.String())
	}
	result, _ := out["AuthenticationResult"].(map[string]any)
	idToken, _ := result["IdToken"].(string)
	if idToken == "" {
		t.Fatal("no IdToken in AuthenticationResult")
	}
	claims := jwtClaims(t, idToken)
	if claims["custom:tenant"] != "acme" {
		t.Errorf("ID token custom:tenant = %v, want acme (claimsToAddOrOverride)", claims["custom:tenant"])
	}
	if _, present := claims["email"]; present {
		t.Errorf("ID token still carries email claim %v, want it suppressed", claims["email"])
	}

	call := inv.only(t, "pre-token")
	if call.Event["triggerSource"] != triggerSourceTokenGenAuthentication {
		t.Errorf("triggerSource = %v, want %s", call.Event["triggerSource"], triggerSourceTokenGenAuthentication)
	}
}

func TestTrigger_PreTokenGeneration_FunctionErrorFailsAuth(t *testing.T) {
	s := newTriggerTestService(t)
	inv := newFakeTriggerInvoker()
	s.InitLambdaInvoker(inv)
	inv.onError("pre-token", "token issuance blocked")

	poolID := createTriggerPool(t, s, map[string]any{"PreTokenGeneration": "arn:aws:lambda:us-east-1:123456789012:function:pre-token"})
	clientID := createTriggerClient(t, s, poolID)

	dispatch(t, s, "SignUp", map[string]any{"ClientId": clientID, "Username": "gina", "Password": "P@ssw0rd1"})
	dispatch(t, s, "ConfirmSignUp", map[string]any{
		"ClientId": clientID, "Username": "gina", "ConfirmationCode": mustConfirmationCode(t, s, poolID, "gina"),
	})

	rec, out := dispatch(t, s, "InitiateAuth", map[string]any{
		"ClientId": clientID, "AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]any{"USERNAME": "gina", "PASSWORD": "P@ssw0rd1"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("InitiateAuth: status %d, want 400 (body %s) — AWS: \"doesn't generate a token\" on a PreTokenGeneration error", rec.Code, rec.Body.String())
	}
	if out["__type"] != "UserLambdaValidationException" {
		t.Errorf("error type = %v, want UserLambdaValidationException", out["__type"])
	}
}

// ─── PostAuthentication ────────────────────────────────────────────────────

func TestTrigger_PostAuthentication_FunctionErrorFailsAuth(t *testing.T) {
	s := newTriggerTestService(t)
	inv := newFakeTriggerInvoker()
	s.InitLambdaInvoker(inv)
	inv.onError("post-auth", "risk engine unavailable")

	poolID := createTriggerPool(t, s, map[string]any{"PostAuthentication": "arn:aws:lambda:us-east-1:123456789012:function:post-auth"})
	clientID := createTriggerClient(t, s, poolID)

	dispatch(t, s, "SignUp", map[string]any{"ClientId": clientID, "Username": "hank", "Password": "P@ssw0rd1"})
	dispatch(t, s, "ConfirmSignUp", map[string]any{
		"ClientId": clientID, "Username": "hank", "ConfirmationCode": mustConfirmationCode(t, s, poolID, "hank"),
	})

	rec, out := dispatch(t, s, "InitiateAuth", map[string]any{
		"ClientId": clientID, "AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]any{"USERNAME": "hank", "PASSWORD": "P@ssw0rd1"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("InitiateAuth: status %d, want 400 (body %s) — a PostAuthentication error must abort token issuance", rec.Code, rec.Body.String())
	}
	if out["__type"] != "UserLambdaValidationException" {
		t.Errorf("error type = %v, want UserLambdaValidationException", out["__type"])
	}

	call := inv.only(t, "post-auth")
	if call.Event["triggerSource"] != triggerSourcePostAuthentication {
		t.Errorf("triggerSource = %v, want %s", call.Event["triggerSource"], triggerSourcePostAuthentication)
	}
}

func TestTrigger_PostAuthentication_SuccessAllowsAuth(t *testing.T) {
	s := newTriggerTestService(t)
	inv := newFakeTriggerInvoker()
	s.InitLambdaInvoker(inv)
	inv.on("post-auth", map[string]any{})

	poolID := createTriggerPool(t, s, map[string]any{"PostAuthentication": "arn:aws:lambda:us-east-1:123456789012:function:post-auth"})
	clientID := createTriggerClient(t, s, poolID)

	dispatch(t, s, "SignUp", map[string]any{"ClientId": clientID, "Username": "ivan", "Password": "P@ssw0rd1"})
	dispatch(t, s, "ConfirmSignUp", map[string]any{
		"ClientId": clientID, "Username": "ivan", "ConfirmationCode": mustConfirmationCode(t, s, poolID, "ivan"),
	})

	rec, out := dispatch(t, s, "InitiateAuth", map[string]any{
		"ClientId": clientID, "AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]any{"USERNAME": "ivan", "PASSWORD": "P@ssw0rd1"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("InitiateAuth: status %d body %s", rec.Code, rec.Body.String())
	}
	result, _ := out["AuthenticationResult"].(map[string]any)
	if result["AccessToken"] == "" || result["AccessToken"] == nil {
		t.Error("no AccessToken issued despite a successful PostAuthentication trigger")
	}
	if inv.count("post-auth") != 1 {
		t.Errorf("PostAuthentication invoked %d times, want 1", inv.count("post-auth"))
	}
}

// ─── CustomMessage ─────────────────────────────────────────────────────────

func TestTrigger_CustomMessage_OverridesVerificationEmail(t *testing.T) {
	s := newTriggerTestService(t)
	inv := newFakeTriggerInvoker()
	s.InitLambdaInvoker(inv)
	mailer := &fakeMailer{}
	s.InitEmailDelivery(mailer)

	poolID := createTriggerPool(t, s, map[string]any{"CustomMessage": "arn:aws:lambda:us-east-1:123456789012:function:custom-msg"})
	clientID := createTriggerClient(t, s, poolID)
	inv.on("custom-msg", map[string]any{
		"emailSubject": "Welcome to Acme",
		"emailMessage": "Hi there, your code is {####} — thanks for joining Acme.",
	})

	rec, _ := dispatch(t, s, "SignUp", map[string]any{
		"ClientId": clientID, "Username": "jill", "Password": "P@ssw0rd1",
		"UserAttributes": []map[string]any{{"Name": "email", "Value": "jill@example.com"}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("SignUp: status %d body %s", rec.Code, rec.Body.String())
	}
	s.Shutdown() // verification email is sent async — wait for it before asserting

	sent := mailer.last(t)
	if sent.Subject != "Welcome to Acme" {
		t.Errorf("email subject = %q, want the CustomMessage override", sent.Subject)
	}
	code := mustConfirmationCode(t, s, poolID, "jill")
	want := "Hi there, your code is " + code + " — thanks for joining Acme."
	if sent.Body != want {
		t.Errorf("email body = %q, want %q (codeParameter {####} substituted with the real code)", sent.Body, want)
	}

	call := inv.only(t, "custom-msg")
	if call.Event["triggerSource"] != triggerSourceCustomMessageSignUp {
		t.Errorf("triggerSource = %v, want %s", call.Event["triggerSource"], triggerSourceCustomMessageSignUp)
	}
	req, _ := call.Event["request"].(map[string]any)
	if req["codeParameter"] != "{####}" {
		t.Errorf("request.codeParameter = %v, want the {####} placeholder", req["codeParameter"])
	}
}

func TestTrigger_CustomMessage_FunctionErrorFailsSignUp(t *testing.T) {
	s := newTriggerTestService(t)
	inv := newFakeTriggerInvoker()
	s.InitLambdaInvoker(inv)
	s.InitEmailDelivery(&fakeMailer{})
	inv.onError("custom-msg", "template render failed")

	poolID := createTriggerPool(t, s, map[string]any{"CustomMessage": "arn:aws:lambda:us-east-1:123456789012:function:custom-msg"})
	clientID := createTriggerClient(t, s, poolID)

	rec, out := dispatch(t, s, "SignUp", map[string]any{
		"ClientId": clientID, "Username": "kate", "Password": "P@ssw0rd1",
		"UserAttributes": []map[string]any{{"Name": "email", "Value": "kate@example.com"}},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("SignUp: status %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if out["__type"] != "UserLambdaValidationException" {
		t.Errorf("error type = %v, want UserLambdaValidationException", out["__type"])
	}
}

// ─── AdminCreateUser (PreSignUp_AdminCreateUser + CustomMessage_AdminCreateUser) ──

func TestTrigger_AdminCreateUser_PreSignUpIgnoresAutoConfirm(t *testing.T) {
	// AWS documents that autoConfirmUser/autoVerifyEmail/autoVerifyPhone are
	// ignored when the pre sign-up trigger fires for AdminCreateUser — only
	// a function error is honored.
	s := newTriggerTestService(t)
	inv := newFakeTriggerInvoker()
	s.InitLambdaInvoker(inv)
	inv.on("pre-signup", map[string]any{"autoConfirmUser": true})

	poolID := createTriggerPool(t, s, map[string]any{"PreSignUp": "arn:aws:lambda:us-east-1:123456789012:function:pre-signup"})

	rec, out := dispatch(t, s, "AdminCreateUser", map[string]any{
		"UserPoolId": poolID, "Username": "leo", "MessageAction": "SUPPRESS",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("AdminCreateUser: status %d body %s", rec.Code, rec.Body.String())
	}
	user, _ := out["User"].(map[string]any)
	if user["UserStatus"] != "FORCE_CHANGE_PASSWORD" {
		t.Errorf("UserStatus = %v, want FORCE_CHANGE_PASSWORD (AdminCreateUser ignores PreSignUp's autoConfirmUser)", user["UserStatus"])
	}
	call := inv.only(t, "pre-signup")
	if call.Event["triggerSource"] != "PreSignUp_AdminCreateUser" {
		t.Errorf("triggerSource = %v, want PreSignUp_AdminCreateUser", call.Event["triggerSource"])
	}
}

func TestTrigger_AdminCreateUser_CustomMessageOverridesInvite(t *testing.T) {
	s := newTriggerTestService(t)
	inv := newFakeTriggerInvoker()
	s.InitLambdaInvoker(inv)
	mailer := &fakeMailer{}
	s.InitEmailDelivery(mailer)
	inv.on("custom-msg", map[string]any{
		"emailSubject": "Your Acme account",
		"emailMessage": "Username: {username} Temp password: {####}",
	})

	poolID := createTriggerPool(t, s, map[string]any{"CustomMessage": "arn:aws:lambda:us-east-1:123456789012:function:custom-msg"})

	rec, _ := dispatch(t, s, "AdminCreateUser", map[string]any{
		"UserPoolId": poolID, "Username": "mia",
		"UserAttributes": []map[string]any{{"Name": "email", "Value": "mia@example.com"}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("AdminCreateUser: status %d body %s", rec.Code, rec.Body.String())
	}
	s.Shutdown() // invite email is sent async — wait for it before asserting

	sent := mailer.last(t)
	if sent.Subject != "Your Acme account" {
		t.Errorf("email subject = %q, want the CustomMessage override", sent.Subject)
	}
	if !strings.Contains(sent.Body, "Username: mia") {
		t.Errorf("email body = %q, want the {username} placeholder substituted", sent.Body)
	}

	call := inv.only(t, "custom-msg")
	if call.Event["triggerSource"] != "CustomMessage_AdminCreateUser" {
		t.Errorf("triggerSource = %v, want CustomMessage_AdminCreateUser", call.Event["triggerSource"])
	}
	req, _ := call.Event["request"].(map[string]any)
	if req["usernameParameter"] != "mia" {
		t.Errorf("request.usernameParameter = %v, want mia", req["usernameParameter"])
	}
}
