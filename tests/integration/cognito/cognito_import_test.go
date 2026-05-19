package cognito_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Neaox/overcast/tests/helpers"
)

func importUsersCall(t *testing.T, srv *helpers.TestServer, poolID string, body map[string]any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal import body: %v", err)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/_overcast/cognito/user-pools/"+poolID+"/import-users", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("importUsersCall: %v", err)
	}
	return resp
}

func TestImportUsers_success(t *testing.T) {
	// Given: a server with an empty user pool
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "import-pool")

	// When: importing a single confirmed user
	now := time.Now().UTC().Truncate(time.Second)
	resp := importUsersCall(t, srv, poolID, map[string]any{
		"users": []map[string]any{
			{
				"username":   "jdoe",
				"sub":        "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
				"enabled":    true,
				"status":     "CONFIRMED",
				"createdAt":  now,
				"modifiedAt": now,
				"attributes": []map[string]string{
					{"name": "email", "value": "jdoe@example.com"},
					{"name": "given_name", "value": "John"},
				},
			},
		},
	})
	defer resp.Body.Close()

	// Then: the import succeeds with 1 imported, 0 skipped
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Imported int              `json:"imported"`
		Skipped  int              `json:"skipped"`
		Errors   []map[string]any `json:"errors"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Imported != 1 {
		t.Fatalf("expected 1 imported, got %d", result.Imported)
	}
	if result.Skipped != 0 {
		t.Fatalf("expected 0 skipped, got %d", result.Skipped)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("expected 0 errors, got %v", result.Errors)
	}
}

func TestImportUsers_confirmedMapsToForceChangePassword(t *testing.T) {
	// Given: a server with an empty user pool
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "status-pool")

	// When: importing a CONFIRMED user
	resp := importUsersCall(t, srv, poolID, map[string]any{
		"users": []map[string]any{
			{
				"username": "confirmed-user",
				"sub":      "11111111-1111-1111-1111-111111111111",
				"enabled":  true,
				"status":   "CONFIRMED",
			},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the user is in FORCE_CHANGE_PASSWORD status
	getResp := cognitoCall(t, srv, "AdminGetUser", map[string]any{
		"UserPoolId": poolID,
		"Username":   "confirmed-user",
	})
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusOK)
	var user struct {
		UserStatus string `json:"UserStatus"`
	}
	helpers.DecodeJSON(t, getResp, &user)
	if user.UserStatus != "FORCE_CHANGE_PASSWORD" {
		t.Fatalf("expected FORCE_CHANGE_PASSWORD, got %s", user.UserStatus)
	}
}

func TestImportUsers_forceChangePasswordStaysSame(t *testing.T) {
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "fcp-pool")

	resp := importUsersCall(t, srv, poolID, map[string]any{
		"users": []map[string]any{
			{
				"username": "fcp-user",
				"sub":      "22222222-2222-2222-2222-222222222222",
				"enabled":  true,
				"status":   "FORCE_CHANGE_PASSWORD",
			},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	getResp := cognitoCall(t, srv, "AdminGetUser", map[string]any{
		"UserPoolId": poolID,
		"Username":   "fcp-user",
	})
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusOK)
	var user struct {
		UserStatus string `json:"UserStatus"`
	}
	helpers.DecodeJSON(t, getResp, &user)
	if user.UserStatus != "FORCE_CHANGE_PASSWORD" {
		t.Fatalf("expected FORCE_CHANGE_PASSWORD, got %s", user.UserStatus)
	}
}

func TestImportUsers_multipleUsers(t *testing.T) {
	// Given: a server with an empty user pool
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "multi-pool")

	// When: importing 3 users
	resp := importUsersCall(t, srv, poolID, map[string]any{
		"users": []map[string]any{
			{"username": "user-a", "sub": "aaa-aaa", "enabled": true, "status": "CONFIRMED"},
			{"username": "user-b", "sub": "bbb-bbb", "enabled": false, "status": "UNCONFIRMED"},
			{"username": "user-c", "sub": "ccc-ccc", "enabled": true, "status": "FORCE_CHANGE_PASSWORD"},
		},
	})
	defer resp.Body.Close()

	// Then: all 3 are imported
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Imported int `json:"imported"`
		Skipped  int `json:"skipped"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Imported != 3 {
		t.Fatalf("expected 3 imported, got %d", result.Imported)
	}

	// And: they appear in ListUsers
	listResp := cognitoCall(t, srv, "ListUsers", map[string]any{
		"UserPoolId": poolID,
	})
	defer listResp.Body.Close()
	helpers.AssertStatus(t, listResp, http.StatusOK)
	var listResult struct {
		Users []struct {
			Username string `json:"Username"`
		} `json:"Users"`
	}
	helpers.DecodeJSON(t, listResp, &listResult)
	if len(listResult.Users) != 3 {
		t.Fatalf("expected 3 users in ListUsers, got %d", len(listResult.Users))
	}
}

func TestImportUsers_withGroups(t *testing.T) {
	// Given: a server with an empty user pool (no groups exist yet)
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "group-pool")

	// When: importing a user that belongs to groups that don't exist
	resp := importUsersCall(t, srv, poolID, map[string]any{
		"users": []map[string]any{
			{
				"username": "group-user",
				"sub":      "33333333-3333-3333-3333-333333333333",
				"enabled":  true,
				"status":   "CONFIRMED",
				"groups":   []string{"Admins", "Editors"},
			},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the groups are auto-created
	listGroupsResp := cognitoCall(t, srv, "ListGroups", map[string]any{
		"UserPoolId": poolID,
	})
	defer listGroupsResp.Body.Close()
	helpers.AssertStatus(t, listGroupsResp, http.StatusOK)
	var groupsResult struct {
		Groups []struct {
			GroupName string `json:"GroupName"`
		} `json:"Groups"`
	}
	helpers.DecodeJSON(t, listGroupsResp, &groupsResult)
	if len(groupsResult.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groupsResult.Groups))
	}

	// And: the user is a member of those groups
	groupsForUserResp := cognitoCall(t, srv, "AdminListGroupsForUser", map[string]any{
		"UserPoolId": poolID,
		"Username":   "group-user",
	})
	defer groupsForUserResp.Body.Close()
	helpers.AssertStatus(t, groupsForUserResp, http.StatusOK)
	var userGroupsResult struct {
		Groups []struct {
			GroupName string `json:"GroupName"`
		} `json:"Groups"`
	}
	helpers.DecodeJSON(t, groupsForUserResp, &userGroupsResult)
	if len(userGroupsResult.Groups) != 2 {
		t.Fatalf("expected 2 user groups, got %d", len(userGroupsResult.Groups))
	}
}

func TestImportUsers_externalProviderSkipped(t *testing.T) {
	// Given: a server with an empty user pool
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "ext-pool")

	// When: importing an EXTERNAL_PROVIDER user
	resp := importUsersCall(t, srv, poolID, map[string]any{
		"users": []map[string]any{
			{
				"username": "federated-user",
				"sub":      "44444444-4444-4444-4444-444444444444",
				"enabled":  true,
				"status":   "EXTERNAL_PROVIDER",
			},
		},
	})
	defer resp.Body.Close()

	// Then: it is skipped, not imported
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Imported int              `json:"imported"`
		Skipped  int              `json:"skipped"`
		Errors   []map[string]any `json:"errors"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Imported != 0 {
		t.Fatalf("expected 0 imported, got %d", result.Imported)
	}
	if result.Skipped != 1 {
		t.Fatalf("expected 1 skipped, got %d", result.Skipped)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(result.Errors))
	}
}

func TestImportUsers_missingUsername(t *testing.T) {
	// Given: a server with an empty user pool
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "missing-username-pool")

	// When: importing a user with missing Username
	resp := importUsersCall(t, srv, poolID, map[string]any{
		"users": []map[string]any{
			{
				"sub":    "55555555-5555-5555-5555-555555555555",
				"status": "CONFIRMED",
			},
		},
	})
	defer resp.Body.Close()

	// Then: it is skipped with an error
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Imported int              `json:"imported"`
		Skipped  int              `json:"skipped"`
		Errors   []map[string]any `json:"errors"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Skipped != 1 {
		t.Fatalf("expected 1 skipped, got %d", result.Skipped)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(result.Errors))
	}
}

func TestImportUsers_missingSub(t *testing.T) {
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "missing-sub-pool")

	resp := importUsersCall(t, srv, poolID, map[string]any{
		"users": []map[string]any{
			{
				"username": "nosub",
				"status":   "CONFIRMED",
			},
		},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Imported int              `json:"imported"`
		Skipped  int              `json:"skipped"`
		Errors   []map[string]any `json:"errors"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Skipped != 1 {
		t.Fatalf("expected 1 skipped, got %d", result.Skipped)
	}
}

func TestImportUsers_duplicateUser(t *testing.T) {
	// Given: a pool with an existing user
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "dup-pool")

	createResp := cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId": poolID,
		"Username":   "existing",
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)

	// When: importing a user with the same username
	resp := importUsersCall(t, srv, poolID, map[string]any{
		"users": []map[string]any{
			{
				"username": "existing",
				"sub":      "66666666-6666-6666-6666-666666666666",
				"enabled":  true,
				"status":   "CONFIRMED",
			},
		},
	})
	defer resp.Body.Close()

	// Then: it is skipped
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Imported int              `json:"imported"`
		Skipped  int              `json:"skipped"`
		Errors   []map[string]any `json:"errors"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Skipped != 1 {
		t.Fatalf("expected 1 skipped, got %d", result.Skipped)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(result.Errors))
	}
}

func TestImportUsers_poolNotFound(t *testing.T) {
	// Given: a server with no pools
	srv := helpers.NewTestServer(t)

	// When: importing into a non-existent pool
	resp := importUsersCall(t, srv, "us-east-1_nonexistent", map[string]any{
		"users": []map[string]any{
			{"username": "anyone", "sub": "any", "enabled": true, "status": "CONFIRMED"},
		},
	})
	defer resp.Body.Close()

	// Then: we get a 400 error
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
}

func TestImportUsers_archivedMapsToDisabled(t *testing.T) {
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "archived-pool")

	resp := importUsersCall(t, srv, poolID, map[string]any{
		"users": []map[string]any{
			{
				"username": "archived-user",
				"sub":      "77777777-7777-7777-7777-777777777777",
				"enabled":  true,
				"status":   "ARCHIVED",
			},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	getResp := cognitoCall(t, srv, "AdminGetUser", map[string]any{
		"UserPoolId": poolID,
		"Username":   "archived-user",
	})
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusOK)
	var user struct {
		UserStatus string `json:"UserStatus"`
	}
	helpers.DecodeJSON(t, getResp, &user)
	if user.UserStatus != "DISABLED" {
		t.Fatalf("expected DISABLED, got %s", user.UserStatus)
	}
}

func TestImportUsers_preservesSub(t *testing.T) {
	// Given: a server with an empty user pool
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "sub-pool")
	originalSub := "88888888-8888-8888-8888-888888888888"

	// When: importing a user with an explicit sub
	resp := importUsersCall(t, srv, poolID, map[string]any{
		"users": []map[string]any{
			{
				"username": "sub-user",
				"sub":      originalSub,
				"enabled":  true,
				"status":   "CONFIRMED",
				"attributes": []map[string]string{
					{"name": "sub", "value": "should-be-overwritten"},
				},
			},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the sub attribute is set to the imported value, not overwritten
	getResp := cognitoCall(t, srv, "AdminGetUser", map[string]any{
		"UserPoolId": poolID,
		"Username":   "sub-user",
	})
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusOK)
	var user struct {
		UserAttributes []struct {
			Name  string `json:"Name"`
			Value string `json:"Value"`
		} `json:"UserAttributes"`
	}
	helpers.DecodeJSON(t, getResp, &user)

	var foundSub string
	for _, attr := range user.UserAttributes {
		if attr.Name == "sub" {
			foundSub = attr.Value
			break
		}
	}
	if foundSub != originalSub {
		t.Fatalf("expected sub %s, got %s", originalSub, foundSub)
	}
}

func TestImportUsers_disabledStatus(t *testing.T) {
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "disabled-pool")

	resp := importUsersCall(t, srv, poolID, map[string]any{
		"users": []map[string]any{
			{
				"username": "disabled-user",
				"sub":      "99999999-9999-9999-9999-999999999999",
				"enabled":  true,
				"status":   "DISABLED",
			},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	getResp := cognitoCall(t, srv, "AdminGetUser", map[string]any{
		"UserPoolId": poolID,
		"Username":   "disabled-user",
	})
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusOK)
	var user struct {
		UserStatus string `json:"UserStatus"`
	}
	helpers.DecodeJSON(t, getResp, &user)
	if user.UserStatus != "DISABLED" {
		t.Fatalf("expected DISABLED, got %s", user.UserStatus)
	}
}
