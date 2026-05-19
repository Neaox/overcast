// Package cognito_test: Token validity configuration integration tests.
package cognito_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// ─── CreateUserPoolClient with token validity ─────────────────────────────────

func TestCreateUserPoolClient_withTokenValidity(t *testing.T) {
	// Given: a user pool
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "tv-pool")

	// When: CreateUserPoolClient is called with token validity settings
	resp := cognitoCall(t, srv, "CreateUserPoolClient", map[string]any{
		"UserPoolId":           poolID,
		"ClientName":           "tv-client",
		"IdTokenValidity":      30,
		"AccessTokenValidity":  15,
		"RefreshTokenValidity": 7,
		"TokenValidityUnits": map[string]string{
			"IdToken":      "minutes",
			"AccessToken":  "minutes",
			"RefreshToken": "days",
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the response includes the token validity settings
	var result struct {
		UserPoolClient struct {
			ClientId             string `json:"ClientId"`
			ClientName           string `json:"ClientName"`
			IdTokenValidity      int    `json:"IdTokenValidity"`
			AccessTokenValidity  int    `json:"AccessTokenValidity"`
			RefreshTokenValidity int    `json:"RefreshTokenValidity"`
			TokenValidityUnits   struct {
				IdToken      string `json:"IdToken"`
				AccessToken  string `json:"AccessToken"`
				RefreshToken string `json:"RefreshToken"`
			} `json:"TokenValidityUnits"`
		} `json:"UserPoolClient"`
	}
	helpers.DecodeJSON(t, resp, &result)
	c := result.UserPoolClient
	if c.ClientId == "" {
		t.Fatal("expected ClientId to be set")
	}
	if c.IdTokenValidity != 30 {
		t.Errorf("IdTokenValidity: got %d, want 30", c.IdTokenValidity)
	}
	if c.AccessTokenValidity != 15 {
		t.Errorf("AccessTokenValidity: got %d, want 15", c.AccessTokenValidity)
	}
	if c.RefreshTokenValidity != 7 {
		t.Errorf("RefreshTokenValidity: got %d, want 7", c.RefreshTokenValidity)
	}
	if c.TokenValidityUnits.IdToken != "minutes" {
		t.Errorf("TokenValidityUnits.IdToken: got %q, want %q", c.TokenValidityUnits.IdToken, "minutes")
	}
	if c.TokenValidityUnits.AccessToken != "minutes" {
		t.Errorf("TokenValidityUnits.AccessToken: got %q, want %q", c.TokenValidityUnits.AccessToken, "minutes")
	}
	if c.TokenValidityUnits.RefreshToken != "days" {
		t.Errorf("TokenValidityUnits.RefreshToken: got %q, want %q", c.TokenValidityUnits.RefreshToken, "days")
	}
}

func TestCreateUserPoolClient_defaultTokenValidity(t *testing.T) {
	// Given: a user pool
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "def-pool")

	// When: CreateUserPoolClient is called WITHOUT token validity settings
	resp := cognitoCall(t, srv, "CreateUserPoolClient", map[string]any{
		"UserPoolId": poolID,
		"ClientName": "def-client",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: DescribeUserPoolClient returns AWS defaults
	var createResult struct {
		UserPoolClient struct {
			ClientId string `json:"ClientId"`
		} `json:"UserPoolClient"`
	}
	helpers.DecodeJSON(t, resp, &createResult)

	descResp := cognitoCall(t, srv, "DescribeUserPoolClient", map[string]any{
		"UserPoolId": poolID,
		"ClientId":   createResult.UserPoolClient.ClientId,
	})
	defer descResp.Body.Close()
	helpers.AssertStatus(t, descResp, http.StatusOK)

	var result struct {
		UserPoolClient struct {
			IdTokenValidity      int `json:"IdTokenValidity"`
			AccessTokenValidity  int `json:"AccessTokenValidity"`
			RefreshTokenValidity int `json:"RefreshTokenValidity"`
			TokenValidityUnits   struct {
				IdToken      string `json:"IdToken"`
				AccessToken  string `json:"AccessToken"`
				RefreshToken string `json:"RefreshToken"`
			} `json:"TokenValidityUnits"`
		} `json:"UserPoolClient"`
	}
	helpers.DecodeJSON(t, descResp, &result)
	c := result.UserPoolClient
	if c.IdTokenValidity != 1 {
		t.Errorf("default IdTokenValidity: got %d, want 1", c.IdTokenValidity)
	}
	if c.AccessTokenValidity != 1 {
		t.Errorf("default AccessTokenValidity: got %d, want 1", c.AccessTokenValidity)
	}
	if c.RefreshTokenValidity != 30 {
		t.Errorf("default RefreshTokenValidity: got %d, want 30", c.RefreshTokenValidity)
	}
	if c.TokenValidityUnits.IdToken != "hours" {
		t.Errorf("default TokenValidityUnits.IdToken: got %q, want %q", c.TokenValidityUnits.IdToken, "hours")
	}
	if c.TokenValidityUnits.AccessToken != "hours" {
		t.Errorf("default TokenValidityUnits.AccessToken: got %q, want %q", c.TokenValidityUnits.AccessToken, "hours")
	}
	if c.TokenValidityUnits.RefreshToken != "days" {
		t.Errorf("default TokenValidityUnits.RefreshToken: got %q, want %q", c.TokenValidityUnits.RefreshToken, "days")
	}
}

// ─── UpdateUserPoolClient with token validity ─────────────────────────────────

func TestUpdateUserPoolClient_tokenValidity(t *testing.T) {
	// Given: a client with default token validity
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "upd-pool")
	clientID := createClient(t, srv, poolID, "upd-client")

	// When: UpdateUserPoolClient is called with new token validity
	resp := cognitoCall(t, srv, "UpdateUserPoolClient", map[string]any{
		"UserPoolId":           poolID,
		"ClientId":             clientID,
		"IdTokenValidity":      45,
		"AccessTokenValidity":  20,
		"RefreshTokenValidity": 14,
		"TokenValidityUnits": map[string]string{
			"IdToken":      "minutes",
			"AccessToken":  "minutes",
			"RefreshToken": "days",
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: DescribeUserPoolClient reflects the updated values
	descResp := cognitoCall(t, srv, "DescribeUserPoolClient", map[string]any{
		"UserPoolId": poolID,
		"ClientId":   clientID,
	})
	defer descResp.Body.Close()
	helpers.AssertStatus(t, descResp, http.StatusOK)

	var result struct {
		UserPoolClient struct {
			IdTokenValidity      int `json:"IdTokenValidity"`
			AccessTokenValidity  int `json:"AccessTokenValidity"`
			RefreshTokenValidity int `json:"RefreshTokenValidity"`
			TokenValidityUnits   struct {
				IdToken      string `json:"IdToken"`
				AccessToken  string `json:"AccessToken"`
				RefreshToken string `json:"RefreshToken"`
			} `json:"TokenValidityUnits"`
		} `json:"UserPoolClient"`
	}
	helpers.DecodeJSON(t, descResp, &result)
	c := result.UserPoolClient
	if c.IdTokenValidity != 45 {
		t.Errorf("IdTokenValidity: got %d, want 45", c.IdTokenValidity)
	}
	if c.AccessTokenValidity != 20 {
		t.Errorf("AccessTokenValidity: got %d, want 20", c.AccessTokenValidity)
	}
	if c.RefreshTokenValidity != 14 {
		t.Errorf("RefreshTokenValidity: got %d, want 14", c.RefreshTokenValidity)
	}
	if c.TokenValidityUnits.IdToken != "minutes" {
		t.Errorf("TokenValidityUnits.IdToken: got %q, want %q", c.TokenValidityUnits.IdToken, "minutes")
	}
	if c.TokenValidityUnits.AccessToken != "minutes" {
		t.Errorf("TokenValidityUnits.AccessToken: got %q, want %q", c.TokenValidityUnits.AccessToken, "minutes")
	}
	if c.TokenValidityUnits.RefreshToken != "days" {
		t.Errorf("TokenValidityUnits.RefreshToken: got %q, want %q", c.TokenValidityUnits.RefreshToken, "days")
	}
}

// ─── Auth with custom token validity ──────────────────────────────────────────

func TestInitiateAuth_respectsCustomTokenValidity(t *testing.T) {
	// Given: a client with 5-minute access/id tokens
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "auth-tv-pool")

	resp := cognitoCall(t, srv, "CreateUserPoolClient", map[string]any{
		"UserPoolId":           poolID,
		"ClientName":           "short-lived",
		"AccessTokenValidity":  5,
		"IdTokenValidity":      10,
		"RefreshTokenValidity": 1,
		"TokenValidityUnits": map[string]string{
			"AccessToken":  "minutes",
			"IdToken":      "minutes",
			"RefreshToken": "days",
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var createResult struct {
		UserPoolClient struct {
			ClientId string `json:"ClientId"`
		} `json:"UserPoolClient"`
	}
	helpers.DecodeJSON(t, resp, &createResult)
	clientID := createResult.UserPoolClient.ClientId

	// Create and confirm a user
	cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId": poolID, "Username": "tvuser",
	}).Body.Close()
	cognitoCall(t, srv, "AdminSetUserPassword", map[string]any{
		"UserPoolId": poolID, "Username": "tvuser",
		"Password": "TvPass1!", "Permanent": true,
	}).Body.Close()

	// When: the user authenticates
	authResp := cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID, "AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{"USERNAME": "tvuser", "PASSWORD": "TvPass1!"},
	})
	defer authResp.Body.Close()
	helpers.AssertStatus(t, authResp, http.StatusOK)

	var authResult struct {
		AuthenticationResult struct {
			AccessToken string `json:"AccessToken"`
			IdToken     string `json:"IdToken"`
			ExpiresIn   int    `json:"ExpiresIn"`
		} `json:"AuthenticationResult"`
	}
	helpers.DecodeJSON(t, authResp, &authResult)

	// Then: ExpiresIn reflects the custom access token validity (5 minutes = 300 seconds)
	if authResult.AuthenticationResult.ExpiresIn != 300 {
		t.Errorf("ExpiresIn: got %d, want 300", authResult.AuthenticationResult.ExpiresIn)
	}

	// And: the access token JWT exp claim is ~5 minutes from iat
	accessClaims := decodeJWTClaims(t, authResult.AuthenticationResult.AccessToken)
	iat, _ := accessClaims["iat"].(float64)
	exp, _ := accessClaims["exp"].(float64)
	accessDelta := int(exp - iat)
	if accessDelta != 300 {
		t.Errorf("access token exp-iat: got %d, want 300", accessDelta)
	}

	// And: the ID token JWT exp claim is ~10 minutes from iat
	idClaims := decodeJWTClaims(t, authResult.AuthenticationResult.IdToken)
	idIat, _ := idClaims["iat"].(float64)
	idExp, _ := idClaims["exp"].(float64)
	idDelta := int(idExp - idIat)
	if idDelta != 600 {
		t.Errorf("id token exp-iat: got %d, want 600", idDelta)
	}
}

// decodeJWTClaims extracts the payload claims from a JWT without signature verification.
func decodeJWTClaims(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT segments, got %d", len(parts))
	}
	// JWT uses raw base64url (no padding)
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
