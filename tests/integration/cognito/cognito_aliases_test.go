package cognito_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/tests/helpers"
)

func confirmationCodeForUser(t *testing.T, srv *helpers.TestServer, poolID, username string) string {
	t.Helper()
	kvs, err := srv.Store.Scan(context.Background(), "cognito:users:"+poolID, serviceutil.RegionKey(srv.Config.Region, poolID+"/"))
	if err != nil {
		t.Fatalf("scan cognito users: %v", err)
	}
	for _, kv := range kvs {
		var user struct {
			Username         string `json:"Username"`
			ConfirmationCode string `json:"ConfirmationCode"`
		}
		if err := json.Unmarshal([]byte(kv.Value), &user); err != nil {
			t.Fatalf("unmarshal stored cognito user: %v", err)
		}
		if user.Username == username {
			if user.ConfirmationCode == "" {
				t.Fatalf("user %q has empty confirmation code", username)
			}
			return user.ConfirmationCode
		}
	}
	t.Fatalf("stored user %q not found", username)
	return ""
}

func createPoolWithAliasAttributes(t *testing.T, srv *helpers.TestServer, name string, attrs []string) string {
	t.Helper()
	resp := cognitoCall(t, srv, "CreateUserPool", map[string]any{
		"PoolName":        name,
		"AliasAttributes": attrs,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		UserPool struct {
			Id string `json:"Id"`
		} `json:"UserPool"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.UserPool.Id == "" {
		t.Fatal("CreateUserPool returned empty Id")
	}
	return result.UserPool.Id
}

func TestAdminCreateUser_aliasPool_verifiedEmailSignIn(t *testing.T) {
	// Given: a pool where email is an alias and a user has a verified email alias
	srv := helpers.NewTestServer(t)
	poolID := createPoolWithAliasAttributes(t, srv, "alias-pool", []string{"email"})
	clientID := createClient(t, srv, poolID, "app")
	createResp := cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId":    poolID,
		"Username":      "alice",
		"MessageAction": "SUPPRESS",
		"UserAttributes": []map[string]string{
			{"Name": "email", "Value": "alice@example.com"},
			{"Name": "email_verified", "Value": "true"},
		},
	})
	createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	setResp := cognitoCall(t, srv, "AdminSetUserPassword", map[string]any{
		"UserPoolId": poolID,
		"Username":   "alice",
		"Password":   "AlicePass1!",
		"Permanent":  true,
	})
	setResp.Body.Close()
	helpers.AssertStatus(t, setResp, http.StatusOK)

	// When: the user signs in with the verified email alias
	resp := cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID,
		"AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME": "alice@example.com",
			"PASSWORD": "AlicePass1!",
		},
	})
	defer resp.Body.Close()

	// Then: Cognito resolves the alias to the user and returns tokens
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		AuthenticationResult struct {
			AccessToken string `json:"AccessToken"`
		} `json:"AuthenticationResult"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.AuthenticationResult.AccessToken == "" {
		t.Error("expected access token after alias sign-in")
	}
}

func TestAdminCreateUser_aliasPool_duplicateVerifiedEmail(t *testing.T) {
	// Given: a pool where email is an alias and one user owns a verified email alias
	srv := helpers.NewTestServer(t)
	poolID := createPoolWithAliasAttributes(t, srv, "alias-pool", []string{"email"})
	resp := cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId":    poolID,
		"Username":      "first",
		"MessageAction": "SUPPRESS",
		"UserAttributes": []map[string]string{
			{"Name": "email", "Value": "shared@example.com"},
			{"Name": "email_verified", "Value": "true"},
		},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// When: a second user is created with the same verified email alias without forcing migration
	resp = cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId":    poolID,
		"Username":      "second",
		"MessageAction": "SUPPRESS",
		"UserAttributes": []map[string]string{
			{"Name": "email", "Value": "shared@example.com"},
			{"Name": "email_verified", "Value": "true"},
		},
	})
	defer resp.Body.Close()

	// Then: AWS rejects the duplicate verified alias
	helpers.AssertJSONError(t, resp, "AliasExistsException")
}

func TestAdminCreateUser_aliasPool_forceAliasCreation(t *testing.T) {
	// Given: a verified email alias is owned by an existing user
	srv := helpers.NewTestServer(t)
	poolID := createPoolWithAliasAttributes(t, srv, "alias-pool", []string{"email"})
	clientID := createClient(t, srv, poolID, "app")
	resp := cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId":    poolID,
		"Username":      "first",
		"MessageAction": "SUPPRESS",
		"UserAttributes": []map[string]string{
			{"Name": "email", "Value": "move@example.com"},
			{"Name": "email_verified", "Value": "true"},
		},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp = cognitoCall(t, srv, "AdminSetUserPassword", map[string]any{
		"UserPoolId": poolID,
		"Username":   "first",
		"Password":   "FirstPass1!",
		"Permanent":  true,
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// When: a second user is created with ForceAliasCreation=true for the same verified email
	resp = cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId":             poolID,
		"Username":               "second",
		"MessageAction":          "SUPPRESS",
		"ForceAliasCreation":     true,
		"TemporaryPassword":      "SecondTemp1!",
		"DesiredDeliveryMediums": []string{"EMAIL"},
		"UserAttributes": []map[string]string{
			{"Name": "email", "Value": "move@example.com"},
			{"Name": "email_verified", "Value": "true"},
		},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp = cognitoCall(t, srv, "AdminSetUserPassword", map[string]any{
		"UserPoolId": poolID,
		"Username":   "second",
		"Password":   "SecondPass1!",
		"Permanent":  true,
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the alias signs in to the second user, not the first user
	resp = cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID,
		"AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME": "move@example.com",
			"PASSWORD": "SecondPass1!",
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	oldResp := cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID,
		"AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME": "move@example.com",
			"PASSWORD": "FirstPass1!",
		},
	})
	defer oldResp.Body.Close()
	helpers.AssertJSONError(t, oldResp, "NotAuthorizedException")
}

func TestConfirmSignUp_aliasPool_duplicateVerifiedEmail(t *testing.T) {
	// Given: a verified email alias is owned by an existing user and another user signs up with the same email
	srv := helpers.NewTestServer(t)
	poolID := createPoolWithAliasAttributes(t, srv, "alias-pool", []string{"email"})
	clientID := createClient(t, srv, poolID, "app")
	resp := cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId":    poolID,
		"Username":      "first",
		"MessageAction": "SUPPRESS",
		"UserAttributes": []map[string]string{
			{"Name": "email", "Value": "confirm@example.com"},
			{"Name": "email_verified", "Value": "true"},
		},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp = cognitoCall(t, srv, "SignUp", map[string]any{
		"ClientId": clientID,
		"Username": "second",
		"Password": "SecondPass1!",
		"UserAttributes": []map[string]string{
			{"Name": "email", "Value": "confirm@example.com"},
		},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	code := confirmationCodeForUser(t, srv, poolID, "second")

	// When: ConfirmSignUp confirms the duplicate alias without ForceAliasCreation
	resp = cognitoCall(t, srv, "ConfirmSignUp", map[string]any{
		"ClientId":         clientID,
		"Username":         "second",
		"ConfirmationCode": code,
	})
	defer resp.Body.Close()

	// Then: AWS rejects the duplicate alias
	helpers.AssertJSONError(t, resp, "AliasExistsException")
}

func TestConfirmSignUp_aliasPool_forceAliasCreation(t *testing.T) {
	// Given: a verified email alias is owned by one user and a second user has the same unconfirmed email
	srv := helpers.NewTestServer(t)
	poolID := createPoolWithAliasAttributes(t, srv, "alias-pool", []string{"email"})
	clientID := createClient(t, srv, poolID, "app")
	resp := cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId":    poolID,
		"Username":      "first",
		"MessageAction": "SUPPRESS",
		"UserAttributes": []map[string]string{
			{"Name": "email", "Value": "migrate-confirm@example.com"},
			{"Name": "email_verified", "Value": "true"},
		},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp = cognitoCall(t, srv, "AdminSetUserPassword", map[string]any{
		"UserPoolId": poolID,
		"Username":   "first",
		"Password":   "FirstPass1!",
		"Permanent":  true,
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp = cognitoCall(t, srv, "SignUp", map[string]any{
		"ClientId": clientID,
		"Username": "second",
		"Password": "SecondPass1!",
		"UserAttributes": []map[string]string{
			{"Name": "email", "Value": "migrate-confirm@example.com"},
		},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	code := confirmationCodeForUser(t, srv, poolID, "second")

	// When: ConfirmSignUp uses ForceAliasCreation for the duplicate alias
	resp = cognitoCall(t, srv, "ConfirmSignUp", map[string]any{
		"ClientId":           clientID,
		"Username":           "second",
		"ConfirmationCode":   code,
		"ForceAliasCreation": true,
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the alias signs in to the second user and no longer authenticates the first user
	resp = cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID,
		"AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME": "migrate-confirm@example.com",
			"PASSWORD": "SecondPass1!",
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	oldResp := cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID,
		"AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME": "migrate-confirm@example.com",
			"PASSWORD": "FirstPass1!",
		},
	})
	defer oldResp.Body.Close()
	helpers.AssertJSONError(t, oldResp, "NotAuthorizedException")
}

func TestAdminCreateUser_aliasPool_verifiedPhoneSignIn(t *testing.T) {
	// Given: a pool where phone_number is an alias and a user has a verified phone alias
	srv := helpers.NewTestServer(t)
	poolID := createPoolWithAliasAttributes(t, srv, "alias-pool", []string{"phone_number"})
	clientID := createClient(t, srv, poolID, "app")
	resp := cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId":    poolID,
		"Username":      "phoneuser",
		"MessageAction": "SUPPRESS",
		"UserAttributes": []map[string]string{
			{"Name": "phone_number", "Value": "+12065550100"},
			{"Name": "phone_number_verified", "Value": "true"},
		},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp = cognitoCall(t, srv, "AdminSetUserPassword", map[string]any{
		"UserPoolId": poolID,
		"Username":   "phoneuser",
		"Password":   "PhonePass1!",
		"Permanent":  true,
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// When: the user signs in with the verified phone alias
	resp = cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID,
		"AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME": "+12065550100",
			"PASSWORD": "PhonePass1!",
		},
	})
	defer resp.Body.Close()

	// Then: Cognito resolves the alias to the user and returns tokens
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestAdminCreateUser_aliasPool_duplicateVerifiedPhone(t *testing.T) {
	// Given: a pool where phone_number is an alias and one user owns a verified phone alias
	srv := helpers.NewTestServer(t)
	poolID := createPoolWithAliasAttributes(t, srv, "alias-pool", []string{"phone_number"})
	resp := cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId":    poolID,
		"Username":      "first",
		"MessageAction": "SUPPRESS",
		"UserAttributes": []map[string]string{
			{"Name": "phone_number", "Value": "+12065550101"},
			{"Name": "phone_number_verified", "Value": "true"},
		},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// When: a second user is created with the same verified phone alias
	resp = cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId":    poolID,
		"Username":      "second",
		"MessageAction": "SUPPRESS",
		"UserAttributes": []map[string]string{
			{"Name": "phone_number", "Value": "+12065550101"},
			{"Name": "phone_number_verified", "Value": "true"},
		},
	})
	defer resp.Body.Close()

	// Then: AWS rejects the duplicate verified alias
	helpers.AssertJSONError(t, resp, "AliasExistsException")
}

func TestConfirmSignUp_aliasPool_forcePhoneAliasCreation(t *testing.T) {
	// Given: a verified phone alias is owned by one user and a second user has the same unconfirmed phone number
	srv := helpers.NewTestServer(t)
	poolID := createPoolWithAliasAttributes(t, srv, "alias-pool", []string{"phone_number"})
	clientID := createClient(t, srv, poolID, "app")
	resp := cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId":    poolID,
		"Username":      "first",
		"MessageAction": "SUPPRESS",
		"UserAttributes": []map[string]string{
			{"Name": "phone_number", "Value": "+12065550102"},
			{"Name": "phone_number_verified", "Value": "true"},
		},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp = cognitoCall(t, srv, "AdminSetUserPassword", map[string]any{
		"UserPoolId": poolID,
		"Username":   "first",
		"Password":   "FirstPass1!",
		"Permanent":  true,
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp = cognitoCall(t, srv, "SignUp", map[string]any{
		"ClientId": clientID,
		"Username": "second",
		"Password": "SecondPass1!",
		"UserAttributes": []map[string]string{
			{"Name": "phone_number", "Value": "+12065550102"},
		},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	code := confirmationCodeForUser(t, srv, poolID, "second")

	// When: ConfirmSignUp uses ForceAliasCreation for the duplicate phone alias
	resp = cognitoCall(t, srv, "ConfirmSignUp", map[string]any{
		"ClientId":           clientID,
		"Username":           "second",
		"ConfirmationCode":   code,
		"ForceAliasCreation": true,
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the phone alias signs in to the second user
	resp = cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID,
		"AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME": "+12065550102",
			"PASSWORD": "SecondPass1!",
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestConfirmSignUp_sessionResponse(t *testing.T) {
	// Given: a self-service user with a valid confirmation code
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	clientID := createClientWithExplicitAuthFlows(t, srv, poolID, "app", []string{"ALLOW_USER_AUTH", "ALLOW_REFRESH_TOKEN_AUTH"})
	resp := cognitoCall(t, srv, "SignUp", map[string]any{
		"ClientId": clientID,
		"Username": "session-user",
		"Password": "SessionPass1!",
		"UserAttributes": []map[string]string{
			{"Name": "email", "Value": "session@example.com"},
		},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	code := confirmationCodeForUser(t, srv, poolID, "session-user")

	// When: ConfirmSignUp succeeds
	resp = cognitoCall(t, srv, "ConfirmSignUp", map[string]any{
		"ClientId":         clientID,
		"Username":         "session-user",
		"ConfirmationCode": code,
	})
	defer resp.Body.Close()

	// Then: the response includes a Session value per AWS wire shape
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Session string `json:"Session"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Session == "" {
		t.Fatal("expected non-empty Session")
	}
}

func TestInitiateAuth_userAuthWithConfirmSignUpSession(t *testing.T) {
	// Given: a newly confirmed self-service user and the ConfirmSignUp session
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	clientID := createClientWithExplicitAuthFlows(t, srv, poolID, "app", []string{"ALLOW_USER_AUTH", "ALLOW_REFRESH_TOKEN_AUTH"})
	resp := cognitoCall(t, srv, "SignUp", map[string]any{
		"ClientId": clientID,
		"Username": "session-auth-user",
		"Password": "SessionPass1!",
		"UserAttributes": []map[string]string{
			{"Name": "email", "Value": "session-auth@example.com"},
		},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	code := confirmationCodeForUser(t, srv, poolID, "session-auth-user")
	resp = cognitoCall(t, srv, "ConfirmSignUp", map[string]any{
		"ClientId":         clientID,
		"Username":         "session-auth-user",
		"ConfirmationCode": code,
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var confirmResult struct {
		Session string `json:"Session"`
	}
	helpers.DecodeJSON(t, resp, &confirmResult)
	resp.Body.Close()

	// When: InitiateAuth uses USER_AUTH with the ConfirmSignUp session
	resp = cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"AuthFlow": "USER_AUTH",
		"ClientId": clientID,
		"AuthParameters": map[string]string{
			"USERNAME": "session-auth-user",
		},
		"Session": confirmResult.Session,
	})
	defer resp.Body.Close()

	// Then: Cognito signs the user in without asking for the confirmation code again
	helpers.AssertStatus(t, resp, http.StatusOK)
	var authResult struct {
		AuthenticationResult struct {
			AccessToken  string `json:"AccessToken"`
			IdToken      string `json:"IdToken"`
			RefreshToken string `json:"RefreshToken"`
			TokenType    string `json:"TokenType"`
		} `json:"AuthenticationResult"`
	}
	helpers.DecodeJSON(t, resp, &authResult)
	if authResult.AuthenticationResult.AccessToken == "" || authResult.AuthenticationResult.IdToken == "" || authResult.AuthenticationResult.RefreshToken == "" {
		t.Fatal("expected authentication tokens")
	}
	if authResult.AuthenticationResult.TokenType != "Bearer" {
		t.Fatalf("expected Bearer token type, got %q", authResult.AuthenticationResult.TokenType)
	}
}

func TestAdminInitiateAuth_userAuthWithConfirmSignUpSession(t *testing.T) {
	// Given: a newly confirmed self-service user and the ConfirmSignUp session
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	clientID := createClientWithExplicitAuthFlows(t, srv, poolID, "app", []string{"ALLOW_USER_AUTH", "ALLOW_REFRESH_TOKEN_AUTH"})
	resp := cognitoCall(t, srv, "SignUp", map[string]any{
		"ClientId": clientID,
		"Username": "admin-session-auth-user",
		"Password": "SessionPass1!",
		"UserAttributes": []map[string]string{
			{"Name": "email", "Value": "admin-session-auth@example.com"},
		},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	code := confirmationCodeForUser(t, srv, poolID, "admin-session-auth-user")
	resp = cognitoCall(t, srv, "ConfirmSignUp", map[string]any{
		"ClientId":         clientID,
		"Username":         "admin-session-auth-user",
		"ConfirmationCode": code,
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var confirmResult struct {
		Session string `json:"Session"`
	}
	helpers.DecodeJSON(t, resp, &confirmResult)
	resp.Body.Close()

	// When: AdminInitiateAuth uses USER_AUTH with the ConfirmSignUp session
	resp = cognitoCall(t, srv, "AdminInitiateAuth", map[string]any{
		"AuthFlow":   "USER_AUTH",
		"UserPoolId": poolID,
		"ClientId":   clientID,
		"AuthParameters": map[string]string{
			"USERNAME": "admin-session-auth-user",
		},
		"Session": confirmResult.Session,
	})
	defer resp.Body.Close()

	// Then: Cognito signs the user in without asking for the confirmation code again
	helpers.AssertStatus(t, resp, http.StatusOK)
	var authResult struct {
		AuthenticationResult struct {
			AccessToken  string `json:"AccessToken"`
			IdToken      string `json:"IdToken"`
			RefreshToken string `json:"RefreshToken"`
			TokenType    string `json:"TokenType"`
		} `json:"AuthenticationResult"`
	}
	helpers.DecodeJSON(t, resp, &authResult)
	if authResult.AuthenticationResult.AccessToken == "" || authResult.AuthenticationResult.IdToken == "" || authResult.AuthenticationResult.RefreshToken == "" {
		t.Fatal("expected authentication tokens")
	}
	if authResult.AuthenticationResult.TokenType != "Bearer" {
		t.Fatalf("expected Bearer token type, got %q", authResult.AuthenticationResult.TokenType)
	}
}

func TestAdminUpdateUserAttributes_aliasPool_duplicateVerifiedEmail(t *testing.T) {
	// Given: a pool where email is an alias and one user owns a verified email alias
	srv := helpers.NewTestServer(t)
	poolID := createPoolWithAliasAttributes(t, srv, "alias-pool", []string{"email"})
	resp := cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId":    poolID,
		"Username":      "first",
		"MessageAction": "SUPPRESS",
		"UserAttributes": []map[string]string{
			{"Name": "email", "Value": "update@example.com"},
			{"Name": "email_verified", "Value": "true"},
		},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp = cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId":    poolID,
		"Username":      "second",
		"MessageAction": "SUPPRESS",
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// When: another user attempts to claim the same verified email alias via AdminUpdateUserAttributes
	resp = cognitoCall(t, srv, "AdminUpdateUserAttributes", map[string]any{
		"UserPoolId": poolID,
		"Username":   "second",
		"UserAttributes": []map[string]string{
			{"Name": "email", "Value": "update@example.com"},
			{"Name": "email_verified", "Value": "true"},
		},
	})
	defer resp.Body.Close()

	// Then: AWS rejects the duplicate verified alias
	helpers.AssertJSONError(t, resp, "AliasExistsException")
}

func TestAdminUpdateUserAttributes_aliasPool_duplicateVerifiedPhone(t *testing.T) {
	// Given: a pool where phone_number is an alias and one user owns a verified phone alias
	srv := helpers.NewTestServer(t)
	poolID := createPoolWithAliasAttributes(t, srv, "alias-pool", []string{"phone_number"})
	resp := cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId":    poolID,
		"Username":      "first",
		"MessageAction": "SUPPRESS",
		"UserAttributes": []map[string]string{
			{"Name": "phone_number", "Value": "+12065550999"},
			{"Name": "phone_number_verified", "Value": "true"},
		},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp = cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId":    poolID,
		"Username":      "second",
		"MessageAction": "SUPPRESS",
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// When: another user attempts to claim the same verified phone alias via AdminUpdateUserAttributes
	resp = cognitoCall(t, srv, "AdminUpdateUserAttributes", map[string]any{
		"UserPoolId": poolID,
		"Username":   "second",
		"UserAttributes": []map[string]string{
			{"Name": "phone_number", "Value": "+12065550999"},
			{"Name": "phone_number_verified", "Value": "true"},
		},
	})
	defer resp.Body.Close()

	// Then: AWS rejects the duplicate verified alias
	helpers.AssertJSONError(t, resp, "AliasExistsException")
}
