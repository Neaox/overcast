// Package cognito_test: Token claim fidelity tests — verifying that id/access/refresh
// tokens match real AWS Cognito claims exactly.
package cognito_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// decodeJWTPayload extracts the JSON payload (middle segment) of a JWT.
func decodeJWTPayload(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT segments, got %d", len(parts))
	}
	// JWT uses base64url (no padding)
	payload := parts[1]
	if rem := len(payload) % 4; rem != 0 {
		payload += strings.Repeat("=", 4-rem)
	}
	raw, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("base64-decode JWT payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatalf("unmarshal JWT payload: %v", err)
	}
	return claims
}

// isUUID returns true if s looks like a UUID v4 (8-4-4-4-12 hex with dashes).
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// authenticateUser is a test helper that creates a pool, client, user and authenticates.
// Returns poolID, clientID, and the AuthenticationResult tokens.
func authenticateUser(t *testing.T, srv *helpers.TestServer, username, email string) (poolID, clientID, accessToken, idToken, refreshToken string) {
	t.Helper()
	poolID = createPool(t, srv, "claim-pool")
	clientID = createClient(t, srv, poolID, "claim-app")
	cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId": poolID, "Username": username,
		"UserAttributes": []map[string]string{
			{"Name": "email", "Value": email},
		},
	}).Body.Close()
	cognitoCall(t, srv, "AdminSetUserPassword", map[string]any{
		"UserPoolId": poolID, "Username": username,
		"Password": "ClaimTest1!", "Permanent": true,
	}).Body.Close()
	resp := cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID, "AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{"USERNAME": username, "PASSWORD": "ClaimTest1!"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		AuthenticationResult struct {
			AccessToken  string `json:"AccessToken"`
			IdToken      string `json:"IdToken"`
			RefreshToken string `json:"RefreshToken"`
		} `json:"AuthenticationResult"`
	}
	helpers.DecodeJSON(t, resp, &result)
	return poolID, clientID, result.AuthenticationResult.AccessToken, result.AuthenticationResult.IdToken, result.AuthenticationResult.RefreshToken
}

// ─── SignUp returns UUID sub ──────────────────────────────────────────────────

func TestSignUp_returnsUUIDSub(t *testing.T) {
	// Given: a pool and client
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	clientID := createClient(t, srv, poolID, "app")

	// When: user signs up
	resp := cognitoCall(t, srv, "SignUp", map[string]any{
		"ClientId": clientID, "Username": "alice",
		"Password":       "Pass1234!",
		"UserAttributes": []map[string]string{{"Name": "email", "Value": "alice@example.com"}},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		UserSub string `json:"UserSub"`
	}
	helpers.DecodeJSON(t, resp, &result)

	// Then: UserSub is a UUID, NOT equal to the username
	if !isUUID(result.UserSub) {
		t.Errorf("UserSub should be a UUID, got %q", result.UserSub)
	}
	if result.UserSub == "alice" {
		t.Error("UserSub should NOT equal username")
	}
}

// ─── AdminGetUser returns sub in Attributes ───────────────────────────────────

func TestAdminGetUser_returnsSubInAttributes(t *testing.T) {
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")

	// Create user via AdminCreateUser
	cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId": poolID, "Username": "bob",
		"UserAttributes": []map[string]string{{"Name": "email", "Value": "bob@example.com"}},
	}).Body.Close()

	// AdminGetUser
	resp := cognitoCall(t, srv, "AdminGetUser", map[string]any{
		"UserPoolId": poolID, "Username": "bob",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		UserAttributes []struct {
			Name  string `json:"Name"`
			Value string `json:"Value"`
		} `json:"UserAttributes"`
	}
	helpers.DecodeJSON(t, resp, &result)

	var sub string
	for _, a := range result.UserAttributes {
		if a.Name == "sub" {
			sub = a.Value
			break
		}
	}
	if !isUUID(sub) {
		t.Errorf("expected sub attribute to be UUID, got %q", sub)
	}
}

// ─── Access token claims ──────────────────────────────────────────────────────

func TestAccessToken_claims(t *testing.T) {
	srv := helpers.NewTestServer(t)
	_, clientID, accessToken, _, _ := authenticateUser(t, srv, "claimuser", "claim@example.com")

	claims := decodeJWTPayload(t, accessToken)

	// sub must be a UUID
	sub, _ := claims["sub"].(string)
	if !isUUID(sub) {
		t.Errorf("access token sub should be UUID, got %q", sub)
	}
	if sub == "claimuser" {
		t.Error("access token sub should NOT equal username")
	}

	// scope must be aws.cognito.signin.user.admin
	scope, _ := claims["scope"].(string)
	if scope != "aws.cognito.signin.user.admin" {
		t.Errorf("access token scope: got %q, want %q", scope, "aws.cognito.signin.user.admin")
	}

	// jti must be UUID format
	jti, _ := claims["jti"].(string)
	if !isUUID(jti) {
		t.Errorf("access token jti should be UUID, got %q", jti)
	}

	// origin_jti must be present (UUID format)
	originJTI, _ := claims["origin_jti"].(string)
	if !isUUID(originJTI) {
		t.Errorf("access token origin_jti should be UUID, got %q", originJTI)
	}

	// event_id must be present (UUID format)
	eventID, _ := claims["event_id"].(string)
	if !isUUID(eventID) {
		t.Errorf("access token event_id should be UUID, got %q", eventID)
	}

	// client_id
	cid, _ := claims["client_id"].(string)
	if cid != clientID {
		t.Errorf("access token client_id: got %q, want %q", cid, clientID)
	}

	// username
	uname, _ := claims["username"].(string)
	if uname != "claimuser" {
		t.Errorf("access token username: got %q, want %q", uname, "claimuser")
	}
}

// ─── ID token claims ──────────────────────────────────────────────────────────

func TestIDToken_claims(t *testing.T) {
	srv := helpers.NewTestServer(t)
	_, clientID, _, idToken, _ := authenticateUser(t, srv, "iduser", "iduser@example.com")

	claims := decodeJWTPayload(t, idToken)

	// sub must be a UUID
	sub, _ := claims["sub"].(string)
	if !isUUID(sub) {
		t.Errorf("id token sub should be UUID, got %q", sub)
	}
	if sub == "iduser" {
		t.Error("id token sub should NOT equal username")
	}

	// event_id must be UUID
	eventID, _ := claims["event_id"].(string)
	if !isUUID(eventID) {
		t.Errorf("id token event_id should be UUID, got %q", eventID)
	}

	// jti must be UUID
	jti, _ := claims["jti"].(string)
	if !isUUID(jti) {
		t.Errorf("id token jti should be UUID, got %q", jti)
	}

	// aud must be client ID
	aud, _ := claims["aud"].(string)
	if aud != clientID {
		t.Errorf("id token aud: got %q, want %q", aud, clientID)
	}

	// cognito:username
	cogUser, _ := claims["cognito:username"].(string)
	if cogUser != "iduser" {
		t.Errorf("id token cognito:username: got %q, want %q", cogUser, "iduser")
	}

	// email attribute must be present
	email, _ := claims["email"].(string)
	if email != "iduser@example.com" {
		t.Errorf("id token email: got %q, want %q", email, "iduser@example.com")
	}
}

// ─── ID token email_verified is boolean ───────────────────────────────────────

func TestIDToken_emailVerifiedIsBoolean(t *testing.T) {
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	clientID := createClient(t, srv, poolID, "app")

	// Create user with email_verified attribute
	cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId": poolID, "Username": "booluser",
		"UserAttributes": []map[string]string{
			{"Name": "email", "Value": "bool@example.com"},
			{"Name": "email_verified", "Value": "true"},
		},
	}).Body.Close()
	cognitoCall(t, srv, "AdminSetUserPassword", map[string]any{
		"UserPoolId": poolID, "Username": "booluser",
		"Password": "BoolPass1!", "Permanent": true,
	}).Body.Close()

	resp := cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID, "AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{"USERNAME": "booluser", "PASSWORD": "BoolPass1!"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		AuthenticationResult struct {
			IdToken string `json:"IdToken"`
		} `json:"AuthenticationResult"`
	}
	helpers.DecodeJSON(t, resp, &result)

	claims := decodeJWTPayload(t, result.AuthenticationResult.IdToken)

	// email_verified must be boolean true, not string "true"
	ev, ok := claims["email_verified"]
	if !ok {
		t.Fatal("email_verified claim missing from ID token")
	}
	if _, isString := ev.(string); isString {
		t.Errorf("email_verified should be boolean, got string %q", ev)
	}
	if ev != true {
		t.Errorf("email_verified: got %v (%T), want true (bool)", ev, ev)
	}
}

// ─── Access token cognito:groups ──────────────────────────────────────────────

func TestAccessToken_cognitoGroups(t *testing.T) {
	srv := helpers.NewTestServer(t)
	poolID, clientID, _, _, _ := authenticateUser(t, srv, "groupuser", "group@example.com")

	// Create a group and add user
	cognitoCall(t, srv, "CreateGroup", map[string]any{
		"UserPoolId": poolID, "GroupName": "admins",
	}).Body.Close()
	cognitoCall(t, srv, "AdminAddUserToGroup", map[string]any{
		"UserPoolId": poolID, "Username": "groupuser", "GroupName": "admins",
	}).Body.Close()

	// Re-authenticate to get token with groups
	resp := cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID, "AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{"USERNAME": "groupuser", "PASSWORD": "ClaimTest1!"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		AuthenticationResult struct {
			AccessToken string `json:"AccessToken"`
		} `json:"AuthenticationResult"`
	}
	helpers.DecodeJSON(t, resp, &result)

	claims := decodeJWTPayload(t, result.AuthenticationResult.AccessToken)

	// cognito:groups must be present on access token
	groups, ok := claims["cognito:groups"]
	if !ok {
		t.Fatal("cognito:groups claim missing from access token")
	}
	arr, ok := groups.([]any)
	if !ok {
		t.Fatalf("cognito:groups should be array, got %T", groups)
	}
	found := false
	for _, g := range arr {
		if g == "admins" {
			found = true
		}
	}
	if !found {
		t.Errorf("cognito:groups should contain 'admins', got %v", arr)
	}
}

// ─── Refresh token preserves origin_jti ───────────────────────────────────────

func TestRefreshToken_preservesOriginJTI(t *testing.T) {
	srv := helpers.NewTestServer(t)
	_, clientID, accessToken, _, refreshToken := authenticateUser(t, srv, "refreshuser", "refresh@example.com")

	// Get the original access token's jti
	origClaims := decodeJWTPayload(t, accessToken)
	origJTI, _ := origClaims["jti"].(string)
	origOriginJTI, _ := origClaims["origin_jti"].(string)

	// For direct auth, origin_jti should equal jti
	if origOriginJTI != origJTI {
		t.Errorf("on direct auth, origin_jti (%q) should equal jti (%q)", origOriginJTI, origJTI)
	}

	// Refresh the token
	resp := cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID, "AuthFlow": "REFRESH_TOKEN_AUTH",
		"AuthParameters": map[string]string{"REFRESH_TOKEN": refreshToken},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		AuthenticationResult struct {
			AccessToken string `json:"AccessToken"`
		} `json:"AuthenticationResult"`
	}
	helpers.DecodeJSON(t, resp, &result)

	newClaims := decodeJWTPayload(t, result.AuthenticationResult.AccessToken)
	newOriginJTI, _ := newClaims["origin_jti"].(string)
	newJTI, _ := newClaims["jti"].(string)

	// After refresh, origin_jti must still reference the ORIGINAL auth's jti
	if newOriginJTI != origJTI {
		t.Errorf("after refresh, origin_jti (%q) should equal original jti (%q)", newOriginJTI, origJTI)
	}
	// But jti must be different (new token)
	if newJTI == origJTI {
		t.Error("after refresh, jti should be different from original jti")
	}
}

// ─── Access and ID tokens share the same event_id ─────────────────────────────

func TestTokens_shareEventID(t *testing.T) {
	srv := helpers.NewTestServer(t)
	_, _, accessToken, idToken, _ := authenticateUser(t, srv, "eventuser", "event@example.com")

	accessClaims := decodeJWTPayload(t, accessToken)
	idClaims := decodeJWTPayload(t, idToken)

	accessEventID, _ := accessClaims["event_id"].(string)
	idEventID, _ := idClaims["event_id"].(string)

	if accessEventID != idEventID {
		t.Errorf("access and id tokens should share event_id; access=%q id=%q", accessEventID, idEventID)
	}
}

// ─── Access and ID tokens share the same sub ──────────────────────────────────

func TestTokens_shareSubUUID(t *testing.T) {
	srv := helpers.NewTestServer(t)
	_, _, accessToken, idToken, _ := authenticateUser(t, srv, "subuser", "sub@example.com")

	accessClaims := decodeJWTPayload(t, accessToken)
	idClaims := decodeJWTPayload(t, idToken)

	accessSub, _ := accessClaims["sub"].(string)
	idSub, _ := idClaims["sub"].(string)

	if accessSub != idSub {
		t.Errorf("access and id tokens should share sub; access=%q id=%q", accessSub, idSub)
	}
	if !isUUID(accessSub) {
		t.Errorf("sub should be UUID, got %q", accessSub)
	}
}

// ─── ConfirmSignUp sets email_verified ────────────────────────────────────────

func TestConfirmSignUp_setsEmailVerified(t *testing.T) {
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	clientID := createClient(t, srv, poolID, "app")

	// SignUp
	cognitoCall(t, srv, "SignUp", map[string]any{
		"ClientId": clientID, "Username": "verifyuser",
		"Password":       "Verify1!",
		"UserAttributes": []map[string]string{{"Name": "email", "Value": "verify@example.com"}},
	}).Body.Close()

	// AdminConfirmSignUp (shortcut)
	cognitoCall(t, srv, "AdminConfirmSignUp", map[string]any{
		"UserPoolId": poolID, "Username": "verifyuser",
	}).Body.Close()

	// Check user attributes
	resp := cognitoCall(t, srv, "AdminGetUser", map[string]any{
		"UserPoolId": poolID, "Username": "verifyuser",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		UserAttributes []struct {
			Name  string `json:"Name"`
			Value string `json:"Value"`
		} `json:"UserAttributes"`
	}
	helpers.DecodeJSON(t, resp, &result)

	var emailVerified string
	for _, a := range result.UserAttributes {
		if a.Name == "email_verified" {
			emailVerified = a.Value
			break
		}
	}
	if emailVerified != "true" {
		t.Errorf("email_verified should be 'true' after ConfirmSignUp, got %q", emailVerified)
	}
}
