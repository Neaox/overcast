// Package cognito_test: JWT, MFA, and Group management integration tests.
package cognito_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Neaox/overcast/tests/helpers"
)

// ─── JWT ──────────────────────────────────────────────────────────────────────

func TestInitiateAuth_returnsJWT(t *testing.T) {
	// Given: a confirmed user with permanent password
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	clientID := createClient(t, srv, poolID, "app")
	cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId": poolID, "Username": "jwtuser",
	}).Body.Close()
	cognitoCall(t, srv, "AdminSetUserPassword", map[string]any{
		"UserPoolId": poolID, "Username": "jwtuser",
		"Password": "JwtPass1!", "Permanent": true,
	}).Body.Close()

	// When: InitiateAuth is called
	resp := cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID, "AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{"USERNAME": "jwtuser", "PASSWORD": "JwtPass1!"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		AuthenticationResult struct {
			AccessToken  string `json:"AccessToken"`
			IdToken      string `json:"IdToken"`
			RefreshToken string `json:"RefreshToken"`
			TokenType    string `json:"TokenType"`
		} `json:"AuthenticationResult"`
	}
	helpers.DecodeJSON(t, resp, &result)

	// Then: tokens are JWT format (three dot-separated base64 segments).
	checkJWTFormat := func(name, tok string) {
		t.Helper()
		if tok == "" {
			t.Errorf("%s: token is empty", name)
			return
		}
		parts := bytes.Split([]byte(tok), []byte("."))
		if len(parts) != 3 {
			t.Errorf("%s: expected 3 JWT segments, got %d", name, len(parts))
		}
	}
	checkJWTFormat("AccessToken", result.AuthenticationResult.AccessToken)
	checkJWTFormat("IdToken", result.AuthenticationResult.IdToken)
	if result.AuthenticationResult.RefreshToken == "" {
		t.Error("RefreshToken is empty")
	}
	if result.AuthenticationResult.TokenType != "Bearer" {
		t.Errorf("TokenType: got %q, want Bearer", result.AuthenticationResult.TokenType)
	}
}

func TestJWKS_endpoint(t *testing.T) {
	// Given: a user pool with a signed-in user (triggers RSA key creation)
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	clientID := createClient(t, srv, poolID, "app")
	cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId": poolID, "Username": "jwksuser",
	}).Body.Close()
	cognitoCall(t, srv, "AdminSetUserPassword", map[string]any{
		"UserPoolId": poolID, "Username": "jwksuser",
		"Password": "JwksPass1!", "Permanent": true,
	}).Body.Close()
	cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID, "AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{"USERNAME": "jwksuser", "PASSWORD": "JwksPass1!"},
	}).Body.Close()

	// When: JWKS endpoint is fetched
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/us-east-1/"+poolID+"/.well-known/jwks.json", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("JWKS GET: %v", err)
	}
	defer resp.Body.Close()

	// Then: 200 with at least one RSA RS256 key
	helpers.AssertStatus(t, resp, http.StatusOK)
	var jwks struct {
		Keys []map[string]any `json:"keys"`
	}
	helpers.DecodeJSON(t, resp, &jwks)
	if len(jwks.Keys) == 0 {
		t.Fatal("JWKS: expected at least one key, got none")
	}
	k := jwks.Keys[0]
	if k["kty"] != "RSA" {
		t.Errorf("JWKS key kty: got %v, want RSA", k["kty"])
	}
	if k["alg"] != "RS256" {
		t.Errorf("JWKS key alg: got %v, want RS256", k["alg"])
	}
}

// ─── TOTP helper ─────────────────────────────────────────────────────────────

// testComputeTOTP computes a 6-digit RFC 6238 TOTP code for the given base32 secret.
// This mirrors the logic in totp.go; it is duplicated here so tests have no internal import.
func testComputeTOTP(t *testing.T, secret string) string {
	t.Helper()
	padded := secret
	if rem := len(padded) % 8; rem != 0 {
		padded += "========"[:8-rem]
	}
	key, err := base32.StdEncoding.DecodeString(padded)
	if err != nil {
		t.Fatalf("base32 decode TOTP secret: %v", err)
	}
	counter := uint64(time.Now().Unix()) / 30
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(buf)
	h := mac.Sum(nil)
	offset := h[len(h)-1] & 0x0f
	code := (uint32(h[offset]&0x7f)<<24 |
		uint32(h[offset+1])<<16 |
		uint32(h[offset+2])<<8 |
		uint32(h[offset+3])) % 1_000_000
	return fmt.Sprintf("%06d", code)
}

// ─── MFA ─────────────────────────────────────────────────────────────────────

func TestMFA_associateAndVerify(t *testing.T) {
	// Given: a signed-in user
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	clientID := createClient(t, srv, poolID, "app")
	cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId": poolID, "Username": "mfauser",
	}).Body.Close()
	cognitoCall(t, srv, "AdminSetUserPassword", map[string]any{
		"UserPoolId": poolID, "Username": "mfauser",
		"Password": "MfaPass1!", "Permanent": true,
	}).Body.Close()
	authResp := cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID, "AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{"USERNAME": "mfauser", "PASSWORD": "MfaPass1!"},
	})
	defer authResp.Body.Close()
	helpers.AssertStatus(t, authResp, http.StatusOK)
	var authResult struct {
		AuthenticationResult struct {
			AccessToken string `json:"AccessToken"`
		} `json:"AuthenticationResult"`
	}
	helpers.DecodeJSON(t, authResp, &authResult)
	accessToken := authResult.AuthenticationResult.AccessToken

	// When: AssociateSoftwareToken
	assocResp := cognitoCall(t, srv, "AssociateSoftwareToken", map[string]any{
		"AccessToken": accessToken,
	})
	defer assocResp.Body.Close()
	helpers.AssertStatus(t, assocResp, http.StatusOK)
	var assocResult struct {
		SecretCode string `json:"SecretCode"`
	}
	helpers.DecodeJSON(t, assocResp, &assocResult)
	if assocResult.SecretCode == "" {
		t.Fatal("AssociateSoftwareToken: empty SecretCode")
	}

	// When: VerifySoftwareToken with valid TOTP code
	code := testComputeTOTP(t, assocResult.SecretCode)
	verifyResp := cognitoCall(t, srv, "VerifySoftwareToken", map[string]any{
		"AccessToken": accessToken,
		"UserCode":    code,
	})
	defer verifyResp.Body.Close()

	// Then: 200 with Status="SUCCESS"
	helpers.AssertStatus(t, verifyResp, http.StatusOK)
	var verifyResult struct {
		Status string `json:"Status"`
	}
	helpers.DecodeJSON(t, verifyResp, &verifyResult)
	if verifyResult.Status != "SUCCESS" {
		t.Errorf("VerifySoftwareToken: Status=%q, want SUCCESS", verifyResult.Status)
	}
}

func TestMFA_fullflow(t *testing.T) {
	// Given: a user whose TOTP MFA has been enrolled and enabled
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	clientID := createClient(t, srv, poolID, "app")
	cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId": poolID, "Username": "mfa2",
	}).Body.Close()
	cognitoCall(t, srv, "AdminSetUserPassword", map[string]any{
		"UserPoolId": poolID, "Username": "mfa2",
		"Password": "Mfa2Pass1!", "Permanent": true,
	}).Body.Close()

	// Sign in to get first access token
	authResp := cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID, "AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{"USERNAME": "mfa2", "PASSWORD": "Mfa2Pass1!"},
	})
	defer authResp.Body.Close()
	var authResult struct {
		AuthenticationResult struct {
			AccessToken string `json:"AccessToken"`
		} `json:"AuthenticationResult"`
	}
	helpers.DecodeJSON(t, authResp, &authResult)
	accessToken := authResult.AuthenticationResult.AccessToken

	// Enroll TOTP
	assocResp := cognitoCall(t, srv, "AssociateSoftwareToken", map[string]any{"AccessToken": accessToken})
	defer assocResp.Body.Close()
	var assocResult struct {
		SecretCode string `json:"SecretCode"`
	}
	helpers.DecodeJSON(t, assocResp, &assocResult)

	code1 := testComputeTOTP(t, assocResult.SecretCode)
	cognitoCall(t, srv, "VerifySoftwareToken", map[string]any{
		"AccessToken": accessToken, "UserCode": code1,
	}).Body.Close()

	// Enable MFA
	mfaPrefResp := cognitoCall(t, srv, "SetUserMFAPreference", map[string]any{
		"AccessToken":              accessToken,
		"SoftwareTokenMfaSettings": map[string]any{"Enabled": true, "PreferredMfa": true},
	})
	defer mfaPrefResp.Body.Close()
	helpers.AssertStatus(t, mfaPrefResp, http.StatusOK)

	// When: InitiateAuth — should return SOFTWARE_TOKEN_MFA challenge
	initResp := cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID, "AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{"USERNAME": "mfa2", "PASSWORD": "Mfa2Pass1!"},
	})
	defer initResp.Body.Close()
	helpers.AssertStatus(t, initResp, http.StatusOK)
	var challengeResult struct {
		ChallengeName string `json:"ChallengeName"`
		Session       string `json:"Session"`
	}
	helpers.DecodeJSON(t, initResp, &challengeResult)
	if challengeResult.ChallengeName != "SOFTWARE_TOKEN_MFA" {
		t.Fatalf("expected SOFTWARE_TOKEN_MFA challenge, got %q", challengeResult.ChallengeName)
	}

	// When: RespondToAuthChallenge with TOTP code
	code2 := testComputeTOTP(t, assocResult.SecretCode)
	challengeResp := cognitoCall(t, srv, "RespondToAuthChallenge", map[string]any{
		"ClientId":      clientID,
		"ChallengeName": "SOFTWARE_TOKEN_MFA",
		"Session":       challengeResult.Session,
		"ChallengeResponses": map[string]string{
			"USERNAME":                "mfa2",
			"SOFTWARE_TOKEN_MFA_CODE": code2,
		},
	})
	defer challengeResp.Body.Close()

	// Then: 200 with tokens
	helpers.AssertStatus(t, challengeResp, http.StatusOK)
	var tokResult struct {
		AuthenticationResult struct {
			AccessToken string `json:"AccessToken"`
		} `json:"AuthenticationResult"`
	}
	helpers.DecodeJSON(t, challengeResp, &tokResult)
	if tokResult.AuthenticationResult.AccessToken == "" {
		t.Error("RespondToAuthChallenge(MFA): empty AccessToken")
	}
}

// ─── Group management ─────────────────────────────────────────────────────────

func TestCreateGroup_and_GetGroup(t *testing.T) {
	// Given: a user pool
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")

	// When: CreateGroup
	resp := cognitoCall(t, srv, "CreateGroup", map[string]any{
		"UserPoolId":  poolID,
		"GroupName":   "admins",
		"Description": "Administrators",
		"Precedence":  1,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var createResult struct {
		Group struct {
			GroupName   string `json:"GroupName"`
			Description string `json:"Description"`
		} `json:"Group"`
	}
	helpers.DecodeJSON(t, resp, &createResult)
	if createResult.Group.GroupName != "admins" {
		t.Errorf("CreateGroup: GroupName=%q, want admins", createResult.Group.GroupName)
	}

	// When: GetGroup
	getResp := cognitoCall(t, srv, "GetGroup", map[string]any{
		"UserPoolId": poolID, "GroupName": "admins",
	})
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusOK)
	var getResult struct {
		Group struct {
			GroupName string `json:"GroupName"`
		} `json:"Group"`
	}
	helpers.DecodeJSON(t, getResp, &getResult)
	if getResult.Group.GroupName != "admins" {
		t.Errorf("GetGroup: GroupName=%q, want admins", getResult.Group.GroupName)
	}
}

func TestListGroups_success(t *testing.T) {
	// Given: two groups in a pool
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	cognitoCall(t, srv, "CreateGroup", map[string]any{"UserPoolId": poolID, "GroupName": "g1"}).Body.Close()
	cognitoCall(t, srv, "CreateGroup", map[string]any{"UserPoolId": poolID, "GroupName": "g2"}).Body.Close()

	// When: ListGroups
	resp := cognitoCall(t, srv, "ListGroups", map[string]any{"UserPoolId": poolID})
	defer resp.Body.Close()

	// Then: both groups returned
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Groups []struct {
			GroupName string `json:"GroupName"`
		} `json:"Groups"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Groups) != 2 {
		t.Errorf("ListGroups: got %d groups, want 2", len(result.Groups))
	}
}

func TestListGroups_pagination(t *testing.T) {
	// Given: three groups in a pool
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	cognitoCall(t, srv, "CreateGroup", map[string]any{"UserPoolId": poolID, "GroupName": "g1"}).Body.Close()
	cognitoCall(t, srv, "CreateGroup", map[string]any{"UserPoolId": poolID, "GroupName": "g2"}).Body.Close()
	cognitoCall(t, srv, "CreateGroup", map[string]any{"UserPoolId": poolID, "GroupName": "g3"}).Body.Close()

	// When: ListGroups requests the first page
	resp := cognitoCall(t, srv, "ListGroups", map[string]any{"UserPoolId": poolID, "Limit": 2})
	defer resp.Body.Close()

	// Then: it returns two groups and a token
	helpers.AssertStatus(t, resp, http.StatusOK)
	var first struct {
		NextToken string `json:"NextToken"`
		Groups    []struct {
			GroupName string `json:"GroupName"`
		} `json:"Groups"`
	}
	helpers.DecodeJSON(t, resp, &first)
	if len(first.Groups) != 2 || first.NextToken == "" {
		t.Fatalf("expected first page with 2 groups and token, got %#v", first)
	}

	// When: ListGroups requests the next page
	resp = cognitoCall(t, srv, "ListGroups", map[string]any{"UserPoolId": poolID, "Limit": 2, "NextToken": first.NextToken})
	defer resp.Body.Close()

	// Then: it returns the final group without another token
	helpers.AssertStatus(t, resp, http.StatusOK)
	var second struct {
		NextToken string `json:"NextToken"`
		Groups    []struct {
			GroupName string `json:"GroupName"`
		} `json:"Groups"`
	}
	helpers.DecodeJSON(t, resp, &second)
	if len(second.Groups) != 1 || second.NextToken != "" {
		t.Fatalf("expected final page with 1 group and no token, got %#v", second)
	}
}

func TestDeleteGroup_success(t *testing.T) {
	// Given: an existing group
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	cognitoCall(t, srv, "CreateGroup", map[string]any{"UserPoolId": poolID, "GroupName": "tbd"}).Body.Close()

	// When: DeleteGroup
	resp := cognitoCall(t, srv, "DeleteGroup", map[string]any{
		"UserPoolId": poolID, "GroupName": "tbd",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: GetGroup returns error
	getResp := cognitoCall(t, srv, "GetGroup", map[string]any{"UserPoolId": poolID, "GroupName": "tbd"})
	defer getResp.Body.Close()
	helpers.AssertJSONError(t, getResp, "ResourceNotFoundException")
}

func TestAdminAddUserToGroup_and_ListUsersInGroup(t *testing.T) {
	// Given: a pool, a group, and a user
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	cognitoCall(t, srv, "CreateGroup", map[string]any{"UserPoolId": poolID, "GroupName": "staff"}).Body.Close()
	cognitoCall(t, srv, "AdminCreateUser", map[string]any{"UserPoolId": poolID, "Username": "alice"}).Body.Close()

	// When: AdminAddUserToGroup
	addResp := cognitoCall(t, srv, "AdminAddUserToGroup", map[string]any{
		"UserPoolId": poolID, "Username": "alice", "GroupName": "staff",
	})
	defer addResp.Body.Close()
	helpers.AssertStatus(t, addResp, http.StatusOK)

	// Then: ListUsersInGroup includes alice
	listResp := cognitoCall(t, srv, "ListUsersInGroup", map[string]any{
		"UserPoolId": poolID, "GroupName": "staff",
	})
	defer listResp.Body.Close()
	helpers.AssertStatus(t, listResp, http.StatusOK)
	var listResult struct {
		Users []struct {
			Username string `json:"Username"`
		} `json:"Users"`
	}
	helpers.DecodeJSON(t, listResp, &listResult)
	if len(listResult.Users) != 1 || listResult.Users[0].Username != "alice" {
		t.Errorf("ListUsersInGroup: got %+v, want [{alice}]", listResult.Users)
	}
}

func TestListUsersInGroup_pagination(t *testing.T) {
	// Given: three users in one group
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	cognitoCall(t, srv, "CreateGroup", map[string]any{"UserPoolId": poolID, "GroupName": "staff"}).Body.Close()
	for _, username := range []string{"alice", "bob", "carol"} {
		cognitoCall(t, srv, "AdminCreateUser", map[string]any{"UserPoolId": poolID, "Username": username}).Body.Close()
		cognitoCall(t, srv, "AdminAddUserToGroup", map[string]any{"UserPoolId": poolID, "Username": username, "GroupName": "staff"}).Body.Close()
	}

	// When: ListUsersInGroup requests the first page
	resp := cognitoCall(t, srv, "ListUsersInGroup", map[string]any{"UserPoolId": poolID, "GroupName": "staff", "Limit": 2})
	defer resp.Body.Close()

	// Then: it returns two users and a token
	helpers.AssertStatus(t, resp, http.StatusOK)
	var first struct {
		NextToken string `json:"NextToken"`
		Users     []struct {
			Username string `json:"Username"`
		} `json:"Users"`
	}
	helpers.DecodeJSON(t, resp, &first)
	if len(first.Users) != 2 || first.NextToken == "" {
		t.Fatalf("expected first page with 2 users and token, got %#v", first)
	}

	// When: ListUsersInGroup requests the next page
	resp = cognitoCall(t, srv, "ListUsersInGroup", map[string]any{"UserPoolId": poolID, "GroupName": "staff", "Limit": 2, "NextToken": first.NextToken})
	defer resp.Body.Close()

	// Then: it returns the remaining user without another token
	helpers.AssertStatus(t, resp, http.StatusOK)
	var second struct {
		NextToken string `json:"NextToken"`
		Users     []struct {
			Username string `json:"Username"`
		} `json:"Users"`
	}
	helpers.DecodeJSON(t, resp, &second)
	if len(second.Users) != 1 || second.NextToken != "" {
		t.Fatalf("expected final page with 1 user and no token, got %#v", second)
	}
}

func TestAdminRemoveUserFromGroup(t *testing.T) {
	// Given: alice is in the group
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	cognitoCall(t, srv, "CreateGroup", map[string]any{"UserPoolId": poolID, "GroupName": "staff"}).Body.Close()
	cognitoCall(t, srv, "AdminCreateUser", map[string]any{"UserPoolId": poolID, "Username": "alice"}).Body.Close()
	cognitoCall(t, srv, "AdminAddUserToGroup", map[string]any{
		"UserPoolId": poolID, "Username": "alice", "GroupName": "staff",
	}).Body.Close()

	// When: AdminRemoveUserFromGroup
	resp := cognitoCall(t, srv, "AdminRemoveUserFromGroup", map[string]any{
		"UserPoolId": poolID, "Username": "alice", "GroupName": "staff",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: ListUsersInGroup is empty
	listResp := cognitoCall(t, srv, "ListUsersInGroup", map[string]any{
		"UserPoolId": poolID, "GroupName": "staff",
	})
	defer listResp.Body.Close()
	var listResult struct {
		Users []struct{ Username string } `json:"Users"`
	}
	helpers.DecodeJSON(t, listResp, &listResult)
	if len(listResult.Users) != 0 {
		t.Errorf("ListUsersInGroup after remove: got %d users, want 0", len(listResult.Users))
	}
}

func TestAdminListGroupsForUser(t *testing.T) {
	// Given: alice is in two groups
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	cognitoCall(t, srv, "CreateGroup", map[string]any{"UserPoolId": poolID, "GroupName": "grp1"}).Body.Close()
	cognitoCall(t, srv, "CreateGroup", map[string]any{"UserPoolId": poolID, "GroupName": "grp2"}).Body.Close()
	cognitoCall(t, srv, "AdminCreateUser", map[string]any{"UserPoolId": poolID, "Username": "alice"}).Body.Close()
	cognitoCall(t, srv, "AdminAddUserToGroup", map[string]any{
		"UserPoolId": poolID, "Username": "alice", "GroupName": "grp1",
	}).Body.Close()
	cognitoCall(t, srv, "AdminAddUserToGroup", map[string]any{
		"UserPoolId": poolID, "Username": "alice", "GroupName": "grp2",
	}).Body.Close()

	// When: AdminListGroupsForUser
	resp := cognitoCall(t, srv, "AdminListGroupsForUser", map[string]any{
		"UserPoolId": poolID, "Username": "alice",
	})
	defer resp.Body.Close()

	// Then: both groups returned
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Groups []struct {
			GroupName string `json:"GroupName"`
		} `json:"Groups"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Groups) != 2 {
		t.Errorf("AdminListGroupsForUser: got %d groups, want 2", len(result.Groups))
	}
}

func TestAdminListGroupsForUser_pagination(t *testing.T) {
	// Given: alice is in three groups
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	cognitoCall(t, srv, "AdminCreateUser", map[string]any{"UserPoolId": poolID, "Username": "alice"}).Body.Close()
	for _, groupName := range []string{"grp1", "grp2", "grp3"} {
		cognitoCall(t, srv, "CreateGroup", map[string]any{"UserPoolId": poolID, "GroupName": groupName}).Body.Close()
		cognitoCall(t, srv, "AdminAddUserToGroup", map[string]any{"UserPoolId": poolID, "Username": "alice", "GroupName": groupName}).Body.Close()
	}

	// When: AdminListGroupsForUser requests the first page
	resp := cognitoCall(t, srv, "AdminListGroupsForUser", map[string]any{"UserPoolId": poolID, "Username": "alice", "Limit": 2})
	defer resp.Body.Close()

	// Then: it returns two groups and a token
	helpers.AssertStatus(t, resp, http.StatusOK)
	var first struct {
		NextToken string `json:"NextToken"`
		Groups    []struct {
			GroupName string `json:"GroupName"`
		} `json:"Groups"`
	}
	helpers.DecodeJSON(t, resp, &first)
	if len(first.Groups) != 2 || first.NextToken == "" {
		t.Fatalf("expected first page with 2 groups and token, got %#v", first)
	}

	// When: AdminListGroupsForUser requests the next page
	resp = cognitoCall(t, srv, "AdminListGroupsForUser", map[string]any{"UserPoolId": poolID, "Username": "alice", "Limit": 2, "NextToken": first.NextToken})
	defer resp.Body.Close()

	// Then: it returns the remaining group without another token
	helpers.AssertStatus(t, resp, http.StatusOK)
	var second struct {
		NextToken string `json:"NextToken"`
		Groups    []struct {
			GroupName string `json:"GroupName"`
		} `json:"Groups"`
	}
	helpers.DecodeJSON(t, resp, &second)
	if len(second.Groups) != 1 || second.NextToken != "" {
		t.Fatalf("expected final page with 1 group and no token, got %#v", second)
	}
}

func TestUpdateGroup_success(t *testing.T) {
	// Given: a group
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	cognitoCall(t, srv, "CreateGroup", map[string]any{
		"UserPoolId": poolID, "GroupName": "team", "Description": "old",
	}).Body.Close()

	// When: UpdateGroup
	resp := cognitoCall(t, srv, "UpdateGroup", map[string]any{
		"UserPoolId": poolID, "GroupName": "team", "Description": "new",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: GetGroup shows updated description
	getResp := cognitoCall(t, srv, "GetGroup", map[string]any{"UserPoolId": poolID, "GroupName": "team"})
	defer getResp.Body.Close()
	var getResult struct {
		Group struct {
			Description string `json:"Description"`
		} `json:"Group"`
	}
	helpers.DecodeJSON(t, getResp, &getResult)
	if getResult.Group.Description != "new" {
		t.Errorf("UpdateGroup: Description=%q, want new", getResult.Group.Description)
	}
}
