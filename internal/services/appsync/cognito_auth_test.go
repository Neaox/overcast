package appsync

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

// stubCognitoValidator stands in for the Cognito service's
// events.CognitoTokenValidator, recording whether it was consulted.
type stubCognitoValidator struct {
	calls  int
	claims map[string]any
	err    error
}

func (s *stubCognitoValidator) ValidateCognitoToken(context.Context, string) (map[string]any, error) {
	s.calls++
	return s.claims, s.err
}

// strictCognitoHandler builds a Handler with strict Cognito enforcement on and
// the given validator wired.
func strictCognitoHandler(v *stubCognitoValidator) *Handler {
	return &Handler{
		cfg:              &config.Config{EnforceAppSyncCognitoAuth: true},
		log:              serviceutil.NewServiceLogger(zap.NewNop(), serviceName),
		cognitoValidator: v,
	}
}

// cognitoRequest is a GraphQL request carrying the given bearer token.
func cognitoRequest(token string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/graphql", nil)
	req.RemoteAddr = "10.0.0.9:12345"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

func TestAuthenticateCognito_relaxedNeverVerifies(t *testing.T) {
	// Given: the default posture and a validator that would reject everything
	validator := &stubCognitoValidator{err: errors.New("must not be called")}
	h := &Handler{cfg: &config.Config{}, cognitoValidator: validator}
	token := unsignedJWT(t, map[string]any{
		"sub": "user-1",
		"iss": "https://cognito-idp.us-east-1.amazonaws.com/us-east-1_other",
	})

	// When: a token from an unrelated issuer is presented
	identity, authErr := h.authenticateCognito(cognitoRequest(token),
		json.RawMessage(`{"userPoolId":"us-east-1_mine"}`))

	// Then: it is accepted without the validator ever being consulted
	if authErr != nil {
		t.Fatalf("unexpected auth error: %v", authErr)
	}
	assertIdentityField(t, identity, "sub", "user-1")
	if validator.calls != 0 {
		t.Errorf("validator consulted %d times in relaxed mode, want 0", validator.calls)
	}
}

func TestAuthenticateCognito_strictReturnsVerifiedClaims(t *testing.T) {
	// Given: strict enforcement and a validator that verifies the token
	validator := &stubCognitoValidator{claims: map[string]any{
		"sub":              "verified-sub",
		"iss":              "http://localhost:4566/us-east-1_mine",
		"cognito:username": "alice",
		"cognito:groups":   []any{"admins"},
		"token_use":        "id",
	}}
	h := strictCognitoHandler(validator)
	token := unsignedJWT(t, map[string]any{
		"sub":       "unverified-sub",
		"iss":       "http://localhost:4566/us-east-1_mine",
		"token_use": "id",
	})

	// When: the request is authorized
	identity, authErr := h.authenticateCognito(cognitoRequest(token),
		json.RawMessage(`{"userPoolId":"us-east-1_mine"}`))

	// Then: the identity is built from the verified claims, not the decoded ones
	if authErr != nil {
		t.Fatalf("unexpected auth error: %v", authErr)
	}
	if validator.calls != 1 {
		t.Fatalf("validator consulted %d times, want 1", validator.calls)
	}
	assertIdentityField(t, identity, "sub", "verified-sub")
	assertIdentityField(t, identity, "username", "alice")
	assertIdentityField(t, identity, "issuer", "http://localhost:4566/us-east-1_mine")
	assertIdentityField(t, identity, "defaultAuthStrategy", "ALLOW")
	claims, ok := identity["claims"].(map[string]any)
	if !ok {
		t.Fatalf("expected claims map, got %#v", identity["claims"])
	}
	if groups, ok := claims["cognito:groups"].([]any); !ok || len(groups) != 1 || groups[0] != "admins" {
		t.Errorf("claims[cognito:groups] = %#v, want [admins]", claims["cognito:groups"])
	}
}

func TestAuthenticateCognito_strictRejections(t *testing.T) {
	// Given: strict enforcement over a pool the API is configured with
	const poolCfg = `{"userPoolId":"us-east-1_mine"}`
	goodClaims := map[string]any{"sub": "s", "iss": "http://localhost:4566/us-east-1_mine", "token_use": "id"}

	tests := []struct {
		name      string
		cfg       string
		token     string
		validator *stubCognitoValidator
	}{
		{
			name:      "no user pool configured",
			cfg:       `{"defaultAction":"ALLOW"}`,
			token:     unsignedJWT(t, goodClaims),
			validator: &stubCognitoValidator{claims: goodClaims},
		},
		{
			name:      "malformed token",
			cfg:       poolCfg,
			token:     "not-a-jwt",
			validator: &stubCognitoValidator{claims: goodClaims},
		},
		{
			name:      "issuer names another pool",
			cfg:       poolCfg,
			token:     unsignedJWT(t, map[string]any{"iss": "http://localhost:4566/us-east-1_theirs", "token_use": "id"}),
			validator: &stubCognitoValidator{claims: goodClaims},
		},
		{
			name:      "issuer merely contains the pool ID",
			cfg:       poolCfg,
			token:     unsignedJWT(t, map[string]any{"iss": "https://evil.example.com/us-east-1_mine-not", "token_use": "id"}),
			validator: &stubCognitoValidator{claims: goodClaims},
		},
		{
			name:      "token_use is neither id nor access",
			cfg:       poolCfg,
			token:     unsignedJWT(t, map[string]any{"iss": "http://localhost:4566/us-east-1_mine", "token_use": "refresh"}),
			validator: &stubCognitoValidator{claims: goodClaims},
		},
		{
			name:      "client ID does not match appIdClientRegex",
			cfg:       `{"userPoolId":"us-east-1_mine","appIdClientRegex":"^allowed$"}`,
			token:     unsignedJWT(t, map[string]any{"iss": "http://localhost:4566/us-east-1_mine", "token_use": "id", "aud": "denied"}),
			validator: &stubCognitoValidator{claims: goodClaims},
		},
		{
			name:      "signature or expiry rejected by Cognito",
			cfg:       poolCfg,
			token:     unsignedJWT(t, goodClaims),
			validator: &stubCognitoValidator{err: errors.New("cognito: invalid signature")},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := strictCognitoHandler(tc.validator)

			// When: the request is authorized
			_, authErr := h.authenticateCognito(cognitoRequest(tc.token), json.RawMessage(tc.cfg))

			// Then: it is refused with AppSync's UnauthorizedException
			if authErr == nil {
				t.Fatal("expected an auth error")
			}
			if authErr.Code != "UnauthorizedException" || authErr.HTTPStatus != http.StatusUnauthorized {
				t.Errorf("got %s/%d, want UnauthorizedException/401", authErr.Code, authErr.HTTPStatus)
			}
		})
	}
}

func TestAuthenticateCognito_strictWithoutValidatorFailsClosed(t *testing.T) {
	// Given: strict enforcement on a server where Cognito is not wired
	h := strictCognitoHandler(nil)
	h.cognitoValidator = nil
	token := unsignedJWT(t, map[string]any{"iss": "http://localhost:4566/us-east-1_mine", "token_use": "id"})

	// When: a request is authorized
	_, authErr := h.authenticateCognito(cognitoRequest(token), json.RawMessage(`{"userPoolId":"us-east-1_mine"}`))

	// Then: it is refused rather than silently falling back to relaxed
	if authErr == nil || authErr.Code != "UnauthorizedException" {
		t.Fatalf("expected UnauthorizedException, got %#v", authErr)
	}
}

func TestAuthenticateCognito_strictAcceptsAccessToken(t *testing.T) {
	// Given: strict enforcement and an appIdClientRegex matching the client
	claims := map[string]any{
		"iss":       "http://localhost:4566/us-east-1_mine",
		"token_use": "access",
		"client_id": "abc123",
		"username":  "alice",
	}
	h := strictCognitoHandler(&stubCognitoValidator{claims: claims})

	// When: an access token whose client_id matches is presented
	identity, authErr := h.authenticateCognito(cognitoRequest(unsignedJWT(t, claims)),
		json.RawMessage(`{"userPoolId":"us-east-1_mine","appIdClientRegex":"^abc123$"}`))

	// Then: it is accepted, with username read from the access token claim
	if authErr != nil {
		t.Fatalf("unexpected auth error: %v", authErr)
	}
	assertIdentityField(t, identity, "username", "alice")
}
