package cognito_test

import (
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func createUserAndReturnSub(t *testing.T, srv *helpers.TestServer, poolID, username string) string {
	t.Helper()
	resp := cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId":    poolID,
		"Username":      username,
		"MessageAction": "SUPPRESS",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		User struct {
			Attributes []struct {
				Name  string `json:"Name"`
				Value string `json:"Value"`
			} `json:"Attributes"`
		} `json:"User"`
	}
	helpers.DecodeJSON(t, resp, &result)
	for _, attr := range result.User.Attributes {
		if attr.Name == "sub" && attr.Value != "" {
			return attr.Value
		}
	}
	t.Fatalf("AdminCreateUser did not return sub attribute: %#v", result.User.Attributes)
	return ""
}

func TestAdminGetUser_plainPool_subUsername(t *testing.T) {
	// Given: a plain username pool user with a sub attribute
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	sub := createUserAndReturnSub(t, srv, poolID, "alice")

	// When: AdminGetUser is called with sub as Username
	resp := cognitoCall(t, srv, "AdminGetUser", map[string]any{
		"UserPoolId": poolID,
		"Username":   sub,
	})
	defer resp.Body.Close()

	// Then: the user is resolved by sub and the stored username is returned
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Username string `json:"Username"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Username != "alice" {
		t.Fatalf("expected username alice, got %q", result.Username)
	}
}

func TestAdminSetUserPassword_plainPool_subUsername(t *testing.T) {
	// Given: a plain username pool user with a sub attribute
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	clientID := createClient(t, srv, poolID, "app")
	sub := createUserAndReturnSub(t, srv, poolID, "bob")

	// When: AdminSetUserPassword is called with sub as Username
	resp := cognitoCall(t, srv, "AdminSetUserPassword", map[string]any{
		"UserPoolId": poolID,
		"Username":   sub,
		"Password":   "BobPass1!",
		"Permanent":  true,
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the user can authenticate with the password set through sub lookup
	resp = cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID,
		"AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME": "bob",
			"PASSWORD": "BobPass1!",
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestAdminUpdateUserAttributes_plainPool_subUsername(t *testing.T) {
	// Given: a plain username pool user with a sub attribute.
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	sub := createUserAndReturnSub(t, srv, poolID, "carol")

	// When: AdminUpdateUserAttributes is called with sub as Username.
	resp := cognitoCall(t, srv, "AdminUpdateUserAttributes", map[string]any{
		"UserPoolId": poolID,
		"Username":   sub,
		"UserAttributes": []map[string]string{
			{"Name": "custom:role", "Value": "admin"},
		},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the stored user has the updated attribute.
	resp = cognitoCall(t, srv, "AdminGetUser", map[string]any{
		"UserPoolId": poolID,
		"Username":   "carol",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		UserAttributes []map[string]string `json:"UserAttributes"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if !hasAttr(result.UserAttributes, "custom:role", "admin") {
		t.Fatalf("expected custom:role=admin, got %v", result.UserAttributes)
	}
}

func TestAdminDeleteUser_plainPool_subUsername(t *testing.T) {
	// Given: a plain username pool user with a sub attribute
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	sub := createUserAndReturnSub(t, srv, poolID, "carol")

	// When: AdminDeleteUser is called with sub as Username
	resp := cognitoCall(t, srv, "AdminDeleteUser", map[string]any{
		"UserPoolId": poolID,
		"Username":   sub,
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the original username no longer resolves
	resp = cognitoCall(t, srv, "AdminGetUser", map[string]any{
		"UserPoolId": poolID,
		"Username":   "carol",
	})
	defer resp.Body.Close()
	helpers.AssertJSONError(t, resp, "UserNotFoundException")
}

func TestAdminDisableEnableUser_plainPool_subUsername(t *testing.T) {
	// Given: a plain username pool user with a sub attribute and a password.
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	clientID := createClient(t, srv, poolID, "app")
	sub := createUserAndReturnSub(t, srv, poolID, "dave")
	resp := cognitoCall(t, srv, "AdminSetUserPassword", map[string]any{
		"UserPoolId": poolID,
		"Username":   "dave",
		"Password":   "DavePass1!",
		"Permanent":  true,
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// When: AdminDisableUser is called with sub as Username.
	resp = cognitoCall(t, srv, "AdminDisableUser", map[string]any{
		"UserPoolId": poolID,
		"Username":   sub,
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the stored user is disabled.
	resp = cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID,
		"AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME": "dave",
			"PASSWORD": "DavePass1!",
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "NotAuthorizedException")

	// When: AdminEnableUser is called with sub as Username.
	resp = cognitoCall(t, srv, "AdminEnableUser", map[string]any{
		"UserPoolId": poolID,
		"Username":   sub,
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the stored user is enabled again.
	resp = cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID,
		"AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME": "dave",
			"PASSWORD": "DavePass1!",
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestAdminDeleteUserAttributes_plainPool_subUsername(t *testing.T) {
	// Given: a plain username pool user with a sub attribute and a custom attribute.
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	sub := createUserAndReturnSub(t, srv, poolID, "dave")
	resp := cognitoCall(t, srv, "AdminUpdateUserAttributes", map[string]any{
		"UserPoolId": poolID,
		"Username":   "dave",
		"UserAttributes": []map[string]string{
			{"Name": "custom:color", "Value": "green"},
		},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// When: AdminDeleteUserAttributes is called with sub as Username.
	resp = cognitoCall(t, srv, "AdminDeleteUserAttributes", map[string]any{
		"UserPoolId":         poolID,
		"Username":           sub,
		"UserAttributeNames": []string{"custom:color"},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the stored user no longer has the attribute.
	resp = cognitoCall(t, srv, "AdminGetUser", map[string]any{
		"UserPoolId": poolID,
		"Username":   "dave",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		UserAttributes []map[string]string `json:"UserAttributes"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if hasAttr(result.UserAttributes, "custom:color", "green") {
		t.Fatalf("expected custom:color to be deleted, got %v", result.UserAttributes)
	}
}

func TestAdminConfirmSignUp_plainPool_subUsername(t *testing.T) {
	// Given: a plain username pool user signed up with a sub attribute.
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	clientID := createClient(t, srv, poolID, "app")
	resp := cognitoCall(t, srv, "SignUp", map[string]any{
		"ClientId": clientID,
		"Username": "dave",
		"Password": "DavePass1!",
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var signUp struct {
		UserSub string `json:"UserSub"`
	}
	helpers.DecodeJSON(t, resp, &signUp)
	resp.Body.Close()

	// When: AdminConfirmSignUp is called with sub as Username.
	resp = cognitoCall(t, srv, "AdminConfirmSignUp", map[string]any{
		"UserPoolId": poolID,
		"Username":   signUp.UserSub,
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the stored user is confirmed and can authenticate.
	resp = cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID,
		"AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME": "dave",
			"PASSWORD": "DavePass1!",
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestAdminGroupMembership_plainPool_subUsername(t *testing.T) {
	// Given: a plain username pool user with a sub attribute and a group.
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "p")
	sub := createUserAndReturnSub(t, srv, poolID, "dave")
	resp := cognitoCall(t, srv, "CreateGroup", map[string]any{"UserPoolId": poolID, "GroupName": "staff"})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// When: AdminAddUserToGroup is called with sub as Username.
	resp = cognitoCall(t, srv, "AdminAddUserToGroup", map[string]any{
		"UserPoolId": poolID,
		"Username":   sub,
		"GroupName":  "staff",
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: AdminListGroupsForUser resolves the same sub Username and returns the group.
	resp = cognitoCall(t, srv, "AdminListGroupsForUser", map[string]any{
		"UserPoolId": poolID,
		"Username":   sub,
	})
	var result struct {
		Groups []struct{ GroupName string } `json:"Groups"`
	}
	helpers.DecodeJSON(t, resp, &result)
	resp.Body.Close()
	if len(result.Groups) != 1 || result.Groups[0].GroupName != "staff" {
		t.Fatalf("expected staff group for sub Username, got %#v", result.Groups)
	}

	// When: AdminRemoveUserFromGroup is called with sub as Username.
	resp = cognitoCall(t, srv, "AdminRemoveUserFromGroup", map[string]any{
		"UserPoolId": poolID,
		"Username":   sub,
		"GroupName":  "staff",
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the resolved user has no group memberships.
	resp = cognitoCall(t, srv, "AdminListGroupsForUser", map[string]any{
		"UserPoolId": poolID,
		"Username":   sub,
	})
	defer resp.Body.Close()
	result.Groups = nil
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Groups) != 0 {
		t.Fatalf("expected no groups after removal, got %#v", result.Groups)
	}
}
