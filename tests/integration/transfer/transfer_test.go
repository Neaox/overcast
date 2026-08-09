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
	srv := helpers.NewTestServer(t)

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

// ---- Resource tagging --------------------------------------------------------
//
// Transfer Family servers and users are taggable in real AWS through
// TagResource / UntagResource / ListTagsForResource, which address the resource
// by its `Arn` member (not `ResourceArn`), and CreateServer / CreateUser accept
// inline Tags.

func transferListTags(t *testing.T, srv *helpers.TestServer, arn string) map[string]string {
	t.Helper()
	resp := transferCall(t, srv, "ListTagsForResource", map[string]any{"Arn": arn})
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		t.Fatalf("ListTagsForResource status: got %d want 200", resp.StatusCode)
	}
	body := decodeMap(t, resp)
	raw, _ := body["Tags"].([]any)
	got := make(map[string]string, len(raw))
	for _, entry := range raw {
		tag, _ := entry.(map[string]any)
		key, _ := tag["Key"].(string)
		value, _ := tag["Value"].(string)
		got[key] = value
	}
	return got
}

// transferServerARN creates a server and returns its ARN.
func transferServerARN(t *testing.T, srv *helpers.TestServer, body map[string]any) (string, string) {
	t.Helper()
	resp := transferCall(t, srv, "CreateServer", body)
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		t.Fatalf("CreateServer status: got %d want 200", resp.StatusCode)
	}
	serverID, _ := decodeMap(t, resp)["ServerId"].(string)
	if serverID == "" {
		t.Fatal("CreateServer response missing ServerId")
	}
	describe := transferCall(t, srv, "DescribeServer", map[string]any{"ServerId": serverID})
	if describe.StatusCode != http.StatusOK {
		defer describe.Body.Close()
		t.Fatalf("DescribeServer status: got %d want 200", describe.StatusCode)
	}
	server, _ := decodeMap(t, describe)["Server"].(map[string]any)
	arn, _ := server["Arn"].(string)
	if arn == "" {
		t.Fatal("DescribeServer response missing Server.Arn")
	}
	return serverID, arn
}

func TestTransferTagResource_roundTripsOnAServer(t *testing.T) {
	srv := helpers.NewTestServer(t)
	_, arn := transferServerARN(t, srv, map[string]any{})

	resp := transferCall(t, srv, "TagResource", map[string]any{
		"Arn":  arn,
		"Tags": []map[string]string{{"Key": "env", "Value": "prod"}, {"Key": "team", "Value": "sftp"}},
	})
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		t.Fatalf("TagResource status: got %d want 200", resp.StatusCode)
	}
	resp.Body.Close()

	if got := transferListTags(t, srv, arn); got["env"] != "prod" || got["team"] != "sftp" {
		t.Fatalf("TagResource did not round-trip: got %v", got)
	}
}

func TestTransferUntagResource_removesNamedKeysOnly(t *testing.T) {
	srv := helpers.NewTestServer(t)
	_, arn := transferServerARN(t, srv, map[string]any{
		"Tags": []map[string]string{{"Key": "env", "Value": "prod"}, {"Key": "keep", "Value": "yes"}},
	})

	resp := transferCall(t, srv, "UntagResource", map[string]any{"Arn": arn, "TagKeys": []string{"env"}})
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		t.Fatalf("UntagResource status: got %d want 200", resp.StatusCode)
	}
	resp.Body.Close()

	got := transferListTags(t, srv, arn)
	if _, still := got["env"]; still {
		t.Errorf("UntagResource left env in place: %v", got)
	}
	if got["keep"] != "yes" {
		t.Errorf("UntagResource removed an unrelated tag: %v", got)
	}
}

func TestTransferCreateServer_appliesTagsAtCreation(t *testing.T) {
	srv := helpers.NewTestServer(t)
	_, arn := transferServerARN(t, srv, map[string]any{
		"Tags": []map[string]string{{"Key": "env", "Value": "staging"}},
	})
	if got := transferListTags(t, srv, arn); got["env"] != "staging" {
		t.Errorf("CreateServer tags not applied at creation: got %v", got)
	}
}

func TestTransferCreateUser_appliesTagsAtCreation(t *testing.T) {
	srv := helpers.NewTestServer(t)
	serverID, _ := transferServerARN(t, srv, map[string]any{})

	resp := transferCall(t, srv, "CreateUser", map[string]any{
		"ServerId": serverID,
		"UserName": "alice",
		"Role":     "arn:aws:iam::000000000000:role/transfer-user",
		"Tags":     []map[string]string{{"Key": "owner", "Value": "alice"}},
	})
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		t.Fatalf("CreateUser status: got %d want 200", resp.StatusCode)
	}
	resp.Body.Close()

	describe := transferCall(t, srv, "DescribeUser", map[string]any{"ServerId": serverID, "UserName": "alice"})
	if describe.StatusCode != http.StatusOK {
		defer describe.Body.Close()
		t.Fatalf("DescribeUser status: got %d want 200", describe.StatusCode)
	}
	user, _ := decodeMap(t, describe)["User"].(map[string]any)
	userARN, _ := user["Arn"].(string)

	if got := transferListTags(t, srv, userARN); got["owner"] != "alice" {
		t.Errorf("CreateUser tags not applied at creation: got %v", got)
	}
}

// Deleting the resource must take its tags with it, so a same-named
// replacement does not inherit them.
func TestTransferDeleteUser_dropsItsTags(t *testing.T) {
	srv := helpers.NewTestServer(t)
	serverID, _ := transferServerARN(t, srv, map[string]any{})
	create := func() string {
		resp := transferCall(t, srv, "CreateUser", map[string]any{
			"ServerId": serverID,
			"UserName": "bob",
			"Role":     "arn:aws:iam::000000000000:role/transfer-user",
			"Tags":     []map[string]string{{"Key": "env", "Value": "prod"}},
		})
		if resp.StatusCode != http.StatusOK {
			defer resp.Body.Close()
			t.Fatalf("CreateUser status: got %d want 200", resp.StatusCode)
		}
		resp.Body.Close()
		describe := transferCall(t, srv, "DescribeUser", map[string]any{"ServerId": serverID, "UserName": "bob"})
		user, _ := decodeMap(t, describe)["User"].(map[string]any)
		arn, _ := user["Arn"].(string)
		return arn
	}
	arn := create()
	if got := transferListTags(t, srv, arn); got["env"] != "prod" {
		t.Fatalf("setup: expected env=prod, got %v", got)
	}

	del := transferCall(t, srv, "DeleteUser", map[string]any{"ServerId": serverID, "UserName": "bob"})
	if del.StatusCode != http.StatusOK {
		defer del.Body.Close()
		t.Fatalf("DeleteUser status: got %d want 200", del.StatusCode)
	}
	del.Body.Close()

	resp := transferCall(t, srv, "CreateUser", map[string]any{
		"ServerId": serverID,
		"UserName": "bob",
		"Role":     "arn:aws:iam::000000000000:role/transfer-user",
	})
	resp.Body.Close()
	if got := transferListTags(t, srv, arn); len(got) != 0 {
		t.Errorf("recreated user inherited tags from the deleted one: %v", got)
	}
}

func TestTransferTagResource_unknownResource(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := transferCall(t, srv, "TagResource", map[string]any{
		"Arn":  "arn:aws:transfer:us-east-1:000000000000:server/s-99999999",
		"Tags": []map[string]string{{"Key": "env", "Value": "prod"}},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("TagResource on a missing server: got %d want 404", resp.StatusCode)
	}
}
