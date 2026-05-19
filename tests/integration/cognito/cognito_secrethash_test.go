// Package cognito_test: SECRET_HASH validation integration tests.
package cognito_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// createClientWithSecret creates a pool client with a generated secret and returns
// (clientID, clientSecret).
func createClientWithSecret(t *testing.T, srv *helpers.TestServer, poolID, name string) (string, string) {
	t.Helper()
	resp := cognitoCall(t, srv, "CreateUserPoolClient", map[string]any{
		"UserPoolId":     poolID,
		"ClientName":     name,
		"GenerateSecret": true,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		UserPoolClient struct {
			ClientId     string `json:"ClientId"`
			ClientSecret string `json:"ClientSecret"`
		} `json:"UserPoolClient"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.UserPoolClient.ClientId == "" {
		t.Fatal("CreateUserPoolClient returned empty ClientId")
	}
	if result.UserPoolClient.ClientSecret == "" {
		t.Fatal("CreateUserPoolClient returned empty ClientSecret")
	}
	return result.UserPoolClient.ClientId, result.UserPoolClient.ClientSecret
}

// secretHash computes the Cognito SECRET_HASH:
//
//	Base64( HMAC-SHA256( username + clientID , clientSecret ) )
func secretHash(username, clientID, clientSecret string) string {
	mac := hmac.New(sha256.New, []byte(clientSecret))
	mac.Write([]byte(username + clientID))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// ─── CreateUserPoolClient ─────────────────────────────────────────────────────

func TestCreateUserPoolClient_withSecret(t *testing.T) {
	// Given: a user pool
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")

	// When: CreateUserPoolClient with GenerateSecret=true
	clientID, secret := createClientWithSecret(t, srv, poolID, "app")
	if len(clientID) == 0 {
		t.Error("ClientId is empty")
	}
	if len(secret) < 10 {
		t.Errorf("ClientSecret looks too short: %q", secret)
	}
}

// ─── InitiateAuth SECRET_HASH ─────────────────────────────────────────────────

func TestInitiateAuth_secretHash_valid(t *testing.T) {
	// Given: a confirmed user and a client with a secret
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	clientID, clientSecret := createClientWithSecret(t, srv, poolID, "app")
	cognitoCall(t, srv, "AdminCreateUser", map[string]any{"UserPoolId": poolID, "Username": "alice"}).Body.Close()
	cognitoCall(t, srv, "AdminSetUserPassword", map[string]any{
		"UserPoolId": poolID, "Username": "alice", "Password": "AlicePass1!", "Permanent": true,
	}).Body.Close()

	// When: InitiateAuth with correct SECRET_HASH
	resp := cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID, "AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME":    "alice",
			"PASSWORD":    "AlicePass1!",
			"SECRET_HASH": secretHash("alice", clientID, clientSecret),
		},
	})
	defer resp.Body.Close()

	// Then: 200 with tokens
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		AuthenticationResult struct {
			AccessToken string `json:"AccessToken"`
		} `json:"AuthenticationResult"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.AuthenticationResult.AccessToken == "" {
		t.Error("expected AccessToken, got none")
	}
}

func TestInitiateAuth_secretHash_missing(t *testing.T) {
	// Given: a confirmed user and a client with a secret
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	clientID, _ := createClientWithSecret(t, srv, poolID, "app")
	cognitoCall(t, srv, "AdminCreateUser", map[string]any{"UserPoolId": poolID, "Username": "bob"}).Body.Close()
	cognitoCall(t, srv, "AdminSetUserPassword", map[string]any{
		"UserPoolId": poolID, "Username": "bob", "Password": "BobPass1!", "Permanent": true,
	}).Body.Close()

	// When: InitiateAuth without SECRET_HASH
	resp := cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID, "AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{"USERNAME": "bob", "PASSWORD": "BobPass1!"},
	})
	defer resp.Body.Close()

	// Then: InvalidParameterException
	helpers.AssertJSONError(t, resp, "InvalidParameterException")
}

func TestInitiateAuth_secretHash_wrong(t *testing.T) {
	// Given: a confirmed user and a client with a secret
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	clientID, _ := createClientWithSecret(t, srv, poolID, "app")
	cognitoCall(t, srv, "AdminCreateUser", map[string]any{"UserPoolId": poolID, "Username": "carol"}).Body.Close()
	cognitoCall(t, srv, "AdminSetUserPassword", map[string]any{
		"UserPoolId": poolID, "Username": "carol", "Password": "CarolPass1!", "Permanent": true,
	}).Body.Close()

	// When: InitiateAuth with incorrect SECRET_HASH
	resp := cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID, "AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME":    "carol",
			"PASSWORD":    "CarolPass1!",
			"SECRET_HASH": "nottherealhash",
		},
	})
	defer resp.Body.Close()

	// Then: NotAuthorizedException
	helpers.AssertJSONError(t, resp, "NotAuthorizedException")
}

func TestInitiateAuth_noSecret_unexpectedHash(t *testing.T) {
	// Given: a client without a secret
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	clientID := createClient(t, srv, poolID, "app")
	cognitoCall(t, srv, "AdminCreateUser", map[string]any{"UserPoolId": poolID, "Username": "dave"}).Body.Close()
	cognitoCall(t, srv, "AdminSetUserPassword", map[string]any{
		"UserPoolId": poolID, "Username": "dave", "Password": "DavePass1!", "Permanent": true,
	}).Body.Close()

	// When: InitiateAuth with a SECRET_HASH on a secret-less client
	resp := cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID, "AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME":    "dave",
			"PASSWORD":    "DavePass1!",
			"SECRET_HASH": "shouldnotbehere",
		},
	})
	defer resp.Body.Close()

	// Then: InvalidParameterException
	helpers.AssertJSONError(t, resp, "InvalidParameterException")
}

// ─── AdminInitiateAuth SECRET_HASH ────────────────────────────────────────────

func TestAdminInitiateAuth_secretHash_missing(t *testing.T) {
	// Given: a confirmed user and a client with a secret
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	clientID, _ := createClientWithSecret(t, srv, poolID, "app")
	cognitoCall(t, srv, "AdminCreateUser", map[string]any{"UserPoolId": poolID, "Username": "adminhash"}).Body.Close()
	cognitoCall(t, srv, "AdminSetUserPassword", map[string]any{
		"UserPoolId": poolID, "Username": "adminhash", "Password": "AdminHash1!", "Permanent": true,
	}).Body.Close()

	// When: AdminInitiateAuth omits SECRET_HASH for the secret client
	resp := cognitoCall(t, srv, "AdminInitiateAuth", map[string]any{
		"UserPoolId": poolID,
		"ClientId":   clientID,
		"AuthFlow":   "ADMIN_USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME": "adminhash",
			"PASSWORD": "AdminHash1!",
		},
	})
	defer resp.Body.Close()

	// Then: InvalidParameterException
	helpers.AssertJSONError(t, resp, "InvalidParameterException")
}

// ─── Challenge SECRET_HASH ────────────────────────────────────────────────────

func TestRespondToAuthChallenge_secretHash_missing(t *testing.T) {
	// Given: a secret client and a NEW_PASSWORD_REQUIRED challenge session
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	clientID, clientSecret := createClientWithSecret(t, srv, poolID, "app")
	cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId": poolID, "Username": "challengehash", "TemporaryPassword": "TempHash1!",
	}).Body.Close()
	initResp := cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID,
		"AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME":    "challengehash",
			"PASSWORD":    "TempHash1!",
			"SECRET_HASH": secretHash("challengehash", clientID, clientSecret),
		},
	})
	defer initResp.Body.Close()
	helpers.AssertStatus(t, initResp, http.StatusOK)
	var challenge struct {
		Session string `json:"Session"`
	}
	helpers.DecodeJSON(t, initResp, &challenge)

	// When: RespondToAuthChallenge omits SECRET_HASH for the secret client
	resp := cognitoCall(t, srv, "RespondToAuthChallenge", map[string]any{
		"ClientId":      clientID,
		"ChallengeName": "NEW_PASSWORD_REQUIRED",
		"Session":       challenge.Session,
		"ChallengeResponses": map[string]string{
			"USERNAME":     "challengehash",
			"NEW_PASSWORD": "FinalHash1!",
		},
	})
	defer resp.Body.Close()

	// Then: InvalidParameterException
	helpers.AssertJSONError(t, resp, "InvalidParameterException")
}

func TestAdminRespondToAuthChallenge_secretHash_missing(t *testing.T) {
	// Given: a secret client and a NEW_PASSWORD_REQUIRED admin challenge session
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	clientID, clientSecret := createClientWithSecret(t, srv, poolID, "app")
	cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId": poolID, "Username": "adminchallenge", "TemporaryPassword": "TempHash1!",
	}).Body.Close()
	initResp := cognitoCall(t, srv, "AdminInitiateAuth", map[string]any{
		"UserPoolId": poolID,
		"ClientId":   clientID,
		"AuthFlow":   "ADMIN_USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME":    "adminchallenge",
			"PASSWORD":    "TempHash1!",
			"SECRET_HASH": secretHash("adminchallenge", clientID, clientSecret),
		},
	})
	defer initResp.Body.Close()
	helpers.AssertStatus(t, initResp, http.StatusOK)
	var challenge struct {
		Session string `json:"Session"`
	}
	helpers.DecodeJSON(t, initResp, &challenge)

	// When: AdminRespondToAuthChallenge omits SECRET_HASH for the secret client
	resp := cognitoCall(t, srv, "AdminRespondToAuthChallenge", map[string]any{
		"UserPoolId":    poolID,
		"ClientId":      clientID,
		"ChallengeName": "NEW_PASSWORD_REQUIRED",
		"Session":       challenge.Session,
		"ChallengeResponses": map[string]string{
			"USERNAME":     "adminchallenge",
			"NEW_PASSWORD": "FinalHash1!",
		},
	})
	defer resp.Body.Close()

	// Then: InvalidParameterException
	helpers.AssertJSONError(t, resp, "InvalidParameterException")
}

// ─── SignUp SECRET_HASH ────────────────────────────────────────────────────────

func TestSignUp_secretHash_valid(t *testing.T) {
	// Given: a pool client with a secret
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	clientID, clientSecret := createClientWithSecret(t, srv, poolID, "app")

	// When: SignUp with correct SECRET_HASH
	resp := cognitoCall(t, srv, "SignUp", map[string]any{
		"ClientId":   clientID,
		"Username":   "newuser",
		"Password":   "NewUser1!",
		"SecretHash": secretHash("newuser", clientID, clientSecret),
	})
	defer resp.Body.Close()

	// Then: 200
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestSignUp_secretHash_missing(t *testing.T) {
	// Given: a pool client with a secret
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	clientID, _ := createClientWithSecret(t, srv, poolID, "app")

	// When: SignUp without SECRET_HASH
	resp := cognitoCall(t, srv, "SignUp", map[string]any{
		"ClientId": clientID, "Username": "newuser2", "Password": "NewUser1!",
	})
	defer resp.Body.Close()

	// Then: InvalidParameterException
	helpers.AssertJSONError(t, resp, "InvalidParameterException")
}

// ─── RefreshToken SECRET_HASH ─────────────────────────────────────────────────

func TestRefreshToken_secretHash_valid(t *testing.T) {
	// Given: a user already signed in with a client that has a secret
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	clientID, clientSecret := createClientWithSecret(t, srv, poolID, "app")
	cognitoCall(t, srv, "AdminCreateUser", map[string]any{"UserPoolId": poolID, "Username": "eve"}).Body.Close()
	cognitoCall(t, srv, "AdminSetUserPassword", map[string]any{
		"UserPoolId": poolID, "Username": "eve", "Password": "EvePass1!", "Permanent": true,
	}).Body.Close()
	authResp := cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID, "AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME":    "eve",
			"PASSWORD":    "EvePass1!",
			"SECRET_HASH": secretHash("eve", clientID, clientSecret),
		},
	})
	defer authResp.Body.Close()
	var authResult struct {
		AuthenticationResult struct {
			RefreshToken string `json:"RefreshToken"`
		} `json:"AuthenticationResult"`
	}
	helpers.DecodeJSON(t, authResp, &authResult)
	refreshToken := authResult.AuthenticationResult.RefreshToken

	// When: REFRESH_TOKEN_AUTH with correct SECRET_HASH
	resp := cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID, "AuthFlow": "REFRESH_TOKEN_AUTH",
		"AuthParameters": map[string]string{
			"REFRESH_TOKEN": refreshToken,
			"SECRET_HASH":   secretHash("eve", clientID, clientSecret),
		},
	})
	defer resp.Body.Close()

	// Then: 200 with new tokens
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		AuthenticationResult struct {
			AccessToken string `json:"AccessToken"`
		} `json:"AuthenticationResult"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.AuthenticationResult.AccessToken == "" {
		t.Error("expected AccessToken in refresh response")
	}
}

func TestRefreshToken_secretHash_missing(t *testing.T) {
	// Given: user signed in with a secret client
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	clientID, clientSecret := createClientWithSecret(t, srv, poolID, "app")
	cognitoCall(t, srv, "AdminCreateUser", map[string]any{"UserPoolId": poolID, "Username": "frank"}).Body.Close()
	cognitoCall(t, srv, "AdminSetUserPassword", map[string]any{
		"UserPoolId": poolID, "Username": "frank", "Password": "FrankPass1!", "Permanent": true,
	}).Body.Close()
	authResp := cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID, "AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME":    "frank",
			"PASSWORD":    "FrankPass1!",
			"SECRET_HASH": secretHash("frank", clientID, clientSecret),
		},
	})
	defer authResp.Body.Close()
	var authResult struct {
		AuthenticationResult struct {
			RefreshToken string `json:"RefreshToken"`
		} `json:"AuthenticationResult"`
	}
	helpers.DecodeJSON(t, authResp, &authResult)

	// When: REFRESH_TOKEN_AUTH without SECRET_HASH
	resp := cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID, "AuthFlow": "REFRESH_TOKEN_AUTH",
		"AuthParameters": map[string]string{
			"REFRESH_TOKEN": authResult.AuthenticationResult.RefreshToken,
		},
	})
	defer resp.Body.Close()

	// Then: InvalidParameterException
	helpers.AssertJSONError(t, resp, "InvalidParameterException")
}
