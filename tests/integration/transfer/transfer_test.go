package transfer_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

const transferTargetPrefix = "TransferService."

func transferCall(t *testing.T, srv *helpers.TestServer, action string, body any) *http.Response {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s body: %v", action, err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new request %s: %v", action, err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", transferTargetPrefix+action)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request %s: %v", action, err)
	}
	return resp
}

func decodeMap(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	out := map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return out
}

func TestTransferServerAndUserLifecycle(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithServices("transfer"))

	createServerResp := transferCall(t, srv, "CreateServer", map[string]any{})
	if createServerResp.StatusCode != http.StatusOK {
		t.Fatalf("CreateServer status: got %d want 200", createServerResp.StatusCode)
	}
	serverBody := decodeMap(t, createServerResp)
	serverID, _ := serverBody["ServerId"].(string)
	if serverID == "" {
		t.Fatalf("CreateServer response missing ServerId: %#v", serverBody)
	}

	createUserResp := transferCall(t, srv, "CreateUser", map[string]any{
		"ServerId":      serverID,
		"UserName":      "alice",
		"Role":          "arn:aws:iam::000000000000:role/transfer-user",
		"HomeDirectory": "/home/alice",
	})
	if createUserResp.StatusCode != http.StatusOK {
		t.Fatalf("CreateUser status: got %d want 200", createUserResp.StatusCode)
	}
	_ = decodeMap(t, createUserResp)

	listUsersResp := transferCall(t, srv, "ListUsers", map[string]any{"ServerId": serverID})
	if listUsersResp.StatusCode != http.StatusOK {
		t.Fatalf("ListUsers status: got %d want 200", listUsersResp.StatusCode)
	}
	usersBody := decodeMap(t, listUsersResp)
	users, ok := usersBody["Users"].([]any)
	if !ok || len(users) != 1 {
		t.Fatalf("expected one user in list, got %#v", usersBody)
	}

	delUserResp := transferCall(t, srv, "DeleteUser", map[string]any{"ServerId": serverID, "UserName": "alice"})
	if delUserResp.StatusCode != http.StatusOK {
		t.Fatalf("DeleteUser status: got %d want 200", delUserResp.StatusCode)
	}
	_ = decodeMap(t, delUserResp)

	delServerResp := transferCall(t, srv, "DeleteServer", map[string]any{"ServerId": serverID})
	if delServerResp.StatusCode != http.StatusOK {
		t.Fatalf("DeleteServer status: got %d want 200", delServerResp.StatusCode)
	}
	_ = decodeMap(t, delServerResp)
}
