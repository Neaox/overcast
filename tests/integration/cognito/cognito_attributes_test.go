package cognito_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Neaox/overcast/tests/helpers"
)

// ─── GetUser should return full user profile ──────────────────────────────────

func TestGetUser_returnsFullProfile(t *testing.T) {
	// Given: an authenticated user with an email attribute
	srv := helpers.NewTestServer(t)
	_, _, accessToken, _, _ := authenticateUser(t, srv, "profileuser", "profile@example.com")

	// When: GetUser is called with the access token
	resp := cognitoCall(t, srv, "GetUser", map[string]any{
		"AccessToken": accessToken,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		Username             string              `json:"Username"`
		UserAttributes       []map[string]string `json:"UserAttributes"`
		UserCreateDate       float64             `json:"UserCreateDate"`
		UserLastModifiedDate float64             `json:"UserLastModifiedDate"`
	}
	helpers.DecodeJSON(t, resp, &result)

	// Then: all profile fields are present
	if result.Username != "profileuser" {
		t.Errorf("Username: got %q, want %q", result.Username, "profileuser")
	}
	if result.UserCreateDate == 0 {
		t.Error("UserCreateDate should be non-zero")
	}
	if result.UserLastModifiedDate == 0 {
		t.Error("UserLastModifiedDate should be non-zero")
	}
	if len(result.UserAttributes) == 0 {
		t.Error("UserAttributes should not be empty")
	}
}

// ─── UpdateUserAttributes (self-service) ──────────────────────────────────────

func TestUpdateUserAttributes_setsAttribute(t *testing.T) {
	// Given: an authenticated user
	srv := helpers.NewTestServer(t)
	poolID, _, accessToken, _, _ := authenticateUser(t, srv, "updateself", "self@example.com")

	// When: the user updates their own custom attribute
	resp := cognitoCall(t, srv, "UpdateUserAttributes", map[string]any{
		"AccessToken": accessToken,
		"UserAttributes": []map[string]string{
			{"Name": "custom:nickname", "Value": "Buddy"},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: AdminGetUser reflects the new attribute
	resp2 := cognitoCall(t, srv, "AdminGetUser", map[string]any{
		"UserPoolId": poolID, "Username": "updateself",
	})
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)

	var user struct {
		UserAttributes []map[string]string `json:"UserAttributes"`
	}
	helpers.DecodeJSON(t, resp2, &user)

	found := false
	for _, attr := range user.UserAttributes {
		if attr["Name"] == "custom:nickname" && attr["Value"] == "Buddy" {
			found = true
		}
	}
	if !found {
		t.Error("expected custom:nickname=Buddy in user attributes after UpdateUserAttributes")
	}
}

func TestUpdateUserAttributes_verificationBeforeUpdate(t *testing.T) {
	// Given: a pool that requires email verification before attribute updates
	srv := helpers.NewTestServer(t)
	poolID := createPoolWithAttributeUpdateSettings(t, srv, "verify-before-update", []string{"email"})
	clientID := createClient(t, srv, poolID, "app")
	createConfirmedUser(t, srv, poolID, "verifyupdate", "Secure123!")
	setUserEmail(t, srv, poolID, "verifyupdate", "old@example.com", true)
	accessToken := initiatePasswordAuth(t, srv, clientID, "verifyupdate", "Secure123!")

	// When: the user changes email through self-service UpdateUserAttributes
	resp := cognitoCall(t, srv, "UpdateUserAttributes", map[string]any{
		"AccessToken": accessToken,
		"UserAttributes": []map[string]string{
			{"Name": "email", "Value": "new@example.com"},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var updated struct {
		CodeDeliveryDetailsList []map[string]string `json:"CodeDeliveryDetailsList"`
	}
	helpers.DecodeJSON(t, resp, &updated)
	if len(updated.CodeDeliveryDetailsList) != 1 || updated.CodeDeliveryDetailsList[0]["AttributeName"] != "email" {
		t.Fatalf("expected email CodeDeliveryDetailsList, got %#v", updated.CodeDeliveryDetailsList)
	}

	// Then: the current email remains unchanged until the verification code is accepted
	assertUserAttribute(t, srv, poolID, "verifyupdate", "email", "old@example.com")
	code := pendingAttributeCode(t, srv, poolID, "verifyupdate", "email")

	verifyResp := cognitoCall(t, srv, "VerifyUserAttribute", map[string]any{
		"AccessToken":   accessToken,
		"AttributeName": "email",
		"Code":          code,
	})
	defer verifyResp.Body.Close()
	helpers.AssertStatus(t, verifyResp, http.StatusOK)

	assertUserAttribute(t, srv, poolID, "verifyupdate", "email", "new@example.com")
	assertUserAttribute(t, srv, poolID, "verifyupdate", "email_verified", "true")
}

func TestAdminUpdateUserAttributes_verificationBeforeUpdate(t *testing.T) {
	// Given: a pool that requires phone verification before attribute updates
	srv := helpers.NewTestServer(t)
	poolID := createPoolWithAttributeUpdateSettings(t, srv, "admin-verify-before-update", []string{"phone_number"})
	createConfirmedUser(t, srv, poolID, "adminverifyupdate", "Secure123!")
	cognitoCall(t, srv, "AdminUpdateUserAttributes", map[string]any{
		"UserPoolId": poolID,
		"Username":   "adminverifyupdate",
		"UserAttributes": []map[string]string{
			{"Name": "phone_number", "Value": "+15555550100"},
			{"Name": "phone_number_verified", "Value": "true"},
		},
	}).Body.Close()

	// When: admin updates the phone number without also setting phone_number_verified=true
	resp := cognitoCall(t, srv, "AdminUpdateUserAttributes", map[string]any{
		"UserPoolId": poolID,
		"Username":   "adminverifyupdate",
		"UserAttributes": []map[string]string{
			{"Name": "phone_number", "Value": "+15555550101"},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the stored phone number remains unchanged and a pending update is created
	assertUserAttribute(t, srv, poolID, "adminverifyupdate", "phone_number", "+15555550100")
	if code := pendingAttributeCode(t, srv, poolID, "adminverifyupdate", "phone_number"); code == "" {
		t.Fatal("expected pending phone_number verification code")
	}

	// When: admin includes phone_number_verified=true in the same request
	resp2 := cognitoCall(t, srv, "AdminUpdateUserAttributes", map[string]any{
		"UserPoolId": poolID,
		"Username":   "adminverifyupdate",
		"UserAttributes": []map[string]string{
			{"Name": "phone_number", "Value": "+15555550102"},
			{"Name": "phone_number_verified", "Value": "true"},
		},
	})
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)

	assertUserAttribute(t, srv, poolID, "adminverifyupdate", "phone_number", "+15555550102")
	assertUserAttribute(t, srv, poolID, "adminverifyupdate", "phone_number_verified", "true")
}

func TestUserPoolAttributeUpdateSettings_createAndUpdate(t *testing.T) {
	// Given: a pool created with email verification-before-update enabled
	srv := helpers.NewTestServer(t)
	poolID := createPoolWithAttributeUpdateSettings(t, srv, "attribute-update-settings", []string{"email"})

	// When: the pool is described
	descResp := cognitoCall(t, srv, "DescribeUserPool", map[string]any{"UserPoolId": poolID})
	defer descResp.Body.Close()
	helpers.AssertStatus(t, descResp, http.StatusOK)

	var desc struct {
		UserPool struct {
			UserAttributeUpdateSettings struct {
				AttributesRequireVerificationBeforeUpdate []string `json:"AttributesRequireVerificationBeforeUpdate"`
			} `json:"UserAttributeUpdateSettings"`
		} `json:"UserPool"`
	}
	helpers.DecodeJSON(t, descResp, &desc)
	if len(desc.UserPool.UserAttributeUpdateSettings.AttributesRequireVerificationBeforeUpdate) != 1 || desc.UserPool.UserAttributeUpdateSettings.AttributesRequireVerificationBeforeUpdate[0] != "email" {
		t.Fatalf("expected email update setting, got %#v", desc.UserPool.UserAttributeUpdateSettings.AttributesRequireVerificationBeforeUpdate)
	}

	// When: UpdateUserPool changes the setting to phone_number
	updateResp := cognitoCall(t, srv, "UpdateUserPool", map[string]any{
		"UserPoolId": poolID,
		"UserAttributeUpdateSettings": map[string]any{
			"AttributesRequireVerificationBeforeUpdate": []string{"phone_number"},
		},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)

	// Then: DescribeUserPool reflects the updated setting
	descResp2 := cognitoCall(t, srv, "DescribeUserPool", map[string]any{"UserPoolId": poolID})
	defer descResp2.Body.Close()
	helpers.AssertStatus(t, descResp2, http.StatusOK)
	helpers.DecodeJSON(t, descResp2, &desc)
	if len(desc.UserPool.UserAttributeUpdateSettings.AttributesRequireVerificationBeforeUpdate) != 1 || desc.UserPool.UserAttributeUpdateSettings.AttributesRequireVerificationBeforeUpdate[0] != "phone_number" {
		t.Fatalf("expected phone_number update setting, got %#v", desc.UserPool.UserAttributeUpdateSettings.AttributesRequireVerificationBeforeUpdate)
	}
}

func TestGetUserAttributeVerificationCode_resendsPendingCode(t *testing.T) {
	// Given: a signed-in user with a pending email update
	srv := helpers.NewTestServer(t)
	poolID := createPoolWithAttributeUpdateSettings(t, srv, "resend-attribute-code", []string{"email"})
	clientID := createClient(t, srv, poolID, "app")
	createConfirmedUser(t, srv, poolID, "resendattr", "Secure123!")
	setUserEmail(t, srv, poolID, "resendattr", "old@example.com", true)
	accessToken := initiatePasswordAuth(t, srv, clientID, "resendattr", "Secure123!")

	resp := cognitoCall(t, srv, "UpdateUserAttributes", map[string]any{
		"AccessToken": accessToken,
		"UserAttributes": []map[string]string{
			{"Name": "email", "Value": "resend@example.com"},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	oldCode := pendingAttributeCode(t, srv, poolID, "resendattr", "email")

	// When: the user requests another verification code for the same attribute
	resendResp := cognitoCall(t, srv, "GetUserAttributeVerificationCode", map[string]any{
		"AccessToken":   accessToken,
		"AttributeName": "email",
	})
	defer resendResp.Body.Close()
	helpers.AssertStatus(t, resendResp, http.StatusOK)
	var delivery struct {
		CodeDeliveryDetails map[string]string `json:"CodeDeliveryDetails"`
	}
	helpers.DecodeJSON(t, resendResp, &delivery)
	if delivery.CodeDeliveryDetails["AttributeName"] != "email" || delivery.CodeDeliveryDetails["DeliveryMedium"] != "EMAIL" {
		t.Fatalf("unexpected CodeDeliveryDetails: %#v", delivery.CodeDeliveryDetails)
	}
	newCode := pendingAttributeCode(t, srv, poolID, "resendattr", "email")
	if newCode == oldCode {
		t.Fatal("expected a new verification code after resend")
	}

	// Then: the old code no longer works, but the new code applies the pending value
	wrongResp := cognitoCall(t, srv, "VerifyUserAttribute", map[string]any{
		"AccessToken":   accessToken,
		"AttributeName": "email",
		"Code":          oldCode,
	})
	defer wrongResp.Body.Close()
	helpers.AssertJSONError(t, wrongResp, "CodeMismatchException")

	verifyResp := cognitoCall(t, srv, "VerifyUserAttribute", map[string]any{
		"AccessToken":   accessToken,
		"AttributeName": "email",
		"Code":          newCode,
	})
	defer verifyResp.Body.Close()
	helpers.AssertStatus(t, verifyResp, http.StatusOK)
	assertUserAttribute(t, srv, poolID, "resendattr", "email", "resend@example.com")
}

func TestVerifyUserAttribute_expiredCode(t *testing.T) {
	// Given: a signed-in user with a pending email update and a mock clock
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	poolID := createPoolWithAttributeUpdateSettings(t, srv, "expired-attribute-code", []string{"email"})
	clientID := createClient(t, srv, poolID, "app")
	createConfirmedUser(t, srv, poolID, "expireattr", "Secure123!")
	setUserEmail(t, srv, poolID, "expireattr", "old@example.com", true)
	accessToken := initiatePasswordAuth(t, srv, clientID, "expireattr", "Secure123!")

	resp := cognitoCall(t, srv, "UpdateUserAttributes", map[string]any{
		"AccessToken": accessToken,
		"UserAttributes": []map[string]string{
			{"Name": "email", "Value": "expired@example.com"},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	code := pendingAttributeCode(t, srv, poolID, "expireattr", "email")

	// When: the code is submitted after expiry
	srv.Clock.Add(20 * time.Minute)
	verifyResp := cognitoCall(t, srv, "VerifyUserAttribute", map[string]any{
		"AccessToken":   accessToken,
		"AttributeName": "email",
		"Code":          code,
	})
	defer verifyResp.Body.Close()

	// Then: Cognito returns ExpiredCodeException and leaves the current value intact
	helpers.AssertJSONError(t, verifyResp, "ExpiredCodeException")
	assertUserAttribute(t, srv, poolID, "expireattr", "email", "old@example.com")
}

func TestUpdateUserAttributes_invalidToken(t *testing.T) {
	// Given: a server
	srv := helpers.NewTestServer(t)

	// When: UpdateUserAttributes is called with an invalid token
	resp := cognitoCall(t, srv, "UpdateUserAttributes", map[string]any{
		"AccessToken": "invalid-token",
		"UserAttributes": []map[string]string{
			{"Name": "custom:foo", "Value": "bar"},
		},
	})
	defer resp.Body.Close()

	// Then: returns 400 NotAuthorizedException
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

// ─── AdminDeleteUserAttributes ────────────────────────────────────────────────

func TestAdminDeleteUserAttributes_removesAttributes(t *testing.T) {
	// Given: a user with custom attributes
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "del-attrs-pool")
	cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId": poolID, "Username": "delattrs",
		"UserAttributes": []map[string]string{
			{"Name": "email", "Value": "del@example.com"},
			{"Name": "custom:color", "Value": "blue"},
		},
	}).Body.Close()

	// When: AdminDeleteUserAttributes removes the custom attribute
	resp := cognitoCall(t, srv, "AdminDeleteUserAttributes", map[string]any{
		"UserPoolId":         poolID,
		"Username":           "delattrs",
		"UserAttributeNames": []string{"custom:color"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the attribute is gone
	resp2 := cognitoCall(t, srv, "AdminGetUser", map[string]any{
		"UserPoolId": poolID, "Username": "delattrs",
	})
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)

	var user struct {
		UserAttributes []map[string]string `json:"UserAttributes"`
	}
	helpers.DecodeJSON(t, resp2, &user)

	for _, attr := range user.UserAttributes {
		if attr["Name"] == "custom:color" {
			t.Error("custom:color should have been deleted")
		}
	}
}

func TestAdminDeleteUserAttributes_userNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "delattr-nouser")

	resp := cognitoCall(t, srv, "AdminDeleteUserAttributes", map[string]any{
		"UserPoolId":         poolID,
		"Username":           "ghost",
		"UserAttributeNames": []string{"email"},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

// ─── DeleteUserAttributes (self-service) ──────────────────────────────────────

func TestDeleteUserAttributes_removesAttributes(t *testing.T) {
	// Given: an authenticated user with a custom attribute
	srv := helpers.NewTestServer(t)
	poolID, _, accessToken, _, _ := authenticateUser(t, srv, "delselfattr", "delself@example.com")

	// First add a custom attribute
	cognitoCall(t, srv, "AdminUpdateUserAttributes", map[string]any{
		"UserPoolId": poolID, "Username": "delselfattr",
		"UserAttributes": []map[string]string{
			{"Name": "custom:role", "Value": "admin"},
		},
	}).Body.Close()

	// When: the user deletes the attribute via self-service
	resp := cognitoCall(t, srv, "DeleteUserAttributes", map[string]any{
		"AccessToken":        accessToken,
		"UserAttributeNames": []string{"custom:role"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the attribute is gone
	resp2 := cognitoCall(t, srv, "AdminGetUser", map[string]any{
		"UserPoolId": poolID, "Username": "delselfattr",
	})
	defer resp2.Body.Close()

	var user struct {
		UserAttributes []map[string]string `json:"UserAttributes"`
	}
	helpers.DecodeJSON(t, resp2, &user)

	for _, attr := range user.UserAttributes {
		if attr["Name"] == "custom:role" {
			t.Error("custom:role should have been deleted")
		}
	}
}

func TestDeleteUserAttributes_invalidToken(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := cognitoCall(t, srv, "DeleteUserAttributes", map[string]any{
		"AccessToken":        "bad-token",
		"UserAttributeNames": []string{"custom:foo"},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func createPoolWithAttributeUpdateSettings(t *testing.T, srv *helpers.TestServer, name string, attrs []string) string {
	t.Helper()
	resp := cognitoCall(t, srv, "CreateUserPool", map[string]any{
		"PoolName": name,
		"UserAttributeUpdateSettings": map[string]any{
			"AttributesRequireVerificationBeforeUpdate": attrs,
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		UserPool struct {
			Id string `json:"Id"`
		} `json:"UserPool"`
	}
	helpers.DecodeJSON(t, resp, &result)
	return result.UserPool.Id
}

func setUserEmail(t *testing.T, srv *helpers.TestServer, poolID, username, email string, verified bool) {
	t.Helper()
	attrs := []map[string]string{{"Name": "email", "Value": email}}
	if verified {
		attrs = append(attrs, map[string]string{"Name": "email_verified", "Value": "true"})
	}
	resp := cognitoCall(t, srv, "AdminUpdateUserAttributes", map[string]any{
		"UserPoolId":     poolID,
		"Username":       username,
		"UserAttributes": attrs,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func initiatePasswordAuth(t *testing.T, srv *helpers.TestServer, clientID, username, password string) string {
	t.Helper()
	resp := cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID,
		"AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME": username,
			"PASSWORD": password,
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		AuthenticationResult struct {
			AccessToken string `json:"AccessToken"`
		} `json:"AuthenticationResult"`
	}
	helpers.DecodeJSON(t, resp, &result)
	return result.AuthenticationResult.AccessToken
}

func assertUserAttribute(t *testing.T, srv *helpers.TestServer, poolID, username, name, want string) {
	t.Helper()
	resp := cognitoCall(t, srv, "AdminGetUser", map[string]any{
		"UserPoolId": poolID,
		"Username":   username,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var user struct {
		UserAttributes []map[string]string `json:"UserAttributes"`
	}
	helpers.DecodeJSON(t, resp, &user)
	for _, attr := range user.UserAttributes {
		if attr["Name"] == name {
			if attr["Value"] != want {
				t.Fatalf("%s: got %q, want %q", name, attr["Value"], want)
			}
			return
		}
	}
	t.Fatalf("missing attribute %s in %#v", name, user.UserAttributes)
}

func pendingAttributeCode(t *testing.T, srv *helpers.TestServer, poolID, username, attrName string) string {
	t.Helper()
	raw, ok, err := srv.Store.Get(context.Background(), "cognito:users:"+poolID, "us-east-1/"+poolID+"/"+username)
	if err != nil {
		t.Fatalf("get user state: %v", err)
	}
	if !ok {
		t.Fatalf("missing user state for %s", username)
	}
	var user struct {
		PendingAttributeUpdates []struct {
			Name string `json:"Name"`
			Code string `json:"Code"`
		} `json:"PendingAttributeUpdates"`
	}
	if err := json.Unmarshal([]byte(raw), &user); err != nil {
		t.Fatalf("unmarshal user state: %v", err)
	}
	for _, pending := range user.PendingAttributeUpdates {
		if pending.Name == attrName {
			return pending.Code
		}
	}
	t.Fatalf("missing pending update for %s in %#v", attrName, user.PendingAttributeUpdates)
	return ""
}
