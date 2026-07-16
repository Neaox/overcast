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
