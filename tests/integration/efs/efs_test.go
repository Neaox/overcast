// Package efs_test contains integration tests for the EFS emulator.
//
// Run: go test ./tests/integration/efs/...
package efs_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

const apiPrefix = "/2015-02-01"

// efsCall sends a REST-JSON request the way the AWS SDK does for EFS.
func efsCall(t *testing.T, method, url string, body any) *http.Response {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http request: %v", err)
	}
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body
}

func expectJSONStatus(t *testing.T, resp *http.Response, expected int) map[string]any {
	t.Helper()
	if resp.StatusCode != expected {
		body := helpers.ReadBody(t, resp)
		t.Fatalf("expected %d, got %d; body=%s", expected, resp.StatusCode, body)
	}
	return decodeJSON(t, resp)
}

func expectStatus(t *testing.T, resp *http.Response, expected int) {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != expected {
		body := helpers.ReadBody(t, resp)
		t.Fatalf("expected %d, got %d; body=%s", expected, resp.StatusCode, body)
	}
}

func expectErrorCode(t *testing.T, resp *http.Response, status int, code string) {
	t.Helper()
	body := expectJSONStatus(t, resp, status)
	if body["__type"] != code {
		t.Fatalf("expected error %s, got %#v", code, body)
	}
}

func createFileSystem(t *testing.T, srv *helpers.TestServer, token string, extra map[string]any) map[string]any {
	t.Helper()
	body := map[string]any{"CreationToken": token}
	for k, v := range extra {
		body[k] = v
	}
	resp := efsCall(t, http.MethodPost, srv.URL+apiPrefix+"/file-systems", body)
	return expectJSONStatus(t, resp, http.StatusCreated)
}

func TestFileSystem_CRUDOverREST(t *testing.T) {
	srv := helpers.NewTestServer(t)

	// Create returns 201 with the "creating" state and defaults applied.
	created := createFileSystem(t, srv, "integration-1", map[string]any{
		"Tags": []map[string]string{{"Key": "Name", "Value": "it-fs"}},
	})
	fsID, _ := created["FileSystemId"].(string)
	if fsID == "" {
		t.Fatalf("expected FileSystemId, got %#v", created)
	}
	if created["LifeCycleState"] != "creating" {
		t.Fatalf("expected creating on create response, got %v", created["LifeCycleState"])
	}
	if created["PerformanceMode"] != "generalPurpose" || created["ThroughputMode"] != "bursting" {
		t.Fatalf("expected default modes, got %#v", created)
	}

	// With a real clock the lifecycle transition is inline: describe is available.
	desc := expectJSONStatus(t, efsCall(t, http.MethodGet, srv.URL+apiPrefix+"/file-systems?FileSystemId="+fsID, nil), http.StatusOK)
	fsList, _ := desc["FileSystems"].([]any)
	if len(fsList) != 1 {
		t.Fatalf("expected 1 file system, got %#v", desc)
	}
	fs := fsList[0].(map[string]any)
	if fs["LifeCycleState"] != "available" {
		t.Fatalf("expected available, got %v", fs["LifeCycleState"])
	}
	if fs["Name"] != "it-fs" {
		t.Fatalf("expected Name from tag, got %v", fs["Name"])
	}

	// Duplicate creation token → 409.
	resp := efsCall(t, http.MethodPost, srv.URL+apiPrefix+"/file-systems", map[string]any{"CreationToken": "integration-1"})
	expectErrorCode(t, resp, http.StatusConflict, "FileSystemAlreadyExists")

	// Update throughput mode → 202.
	updated := expectJSONStatus(t, efsCall(t, http.MethodPut, srv.URL+apiPrefix+"/file-systems/"+fsID, map[string]any{
		"ThroughputMode": "elastic",
	}), http.StatusAccepted)
	if updated["ThroughputMode"] != "elastic" {
		t.Fatalf("expected elastic, got %v", updated["ThroughputMode"])
	}

	// Delete → 204; then describes 404.
	expectStatus(t, efsCall(t, http.MethodDelete, srv.URL+apiPrefix+"/file-systems/"+fsID, nil), http.StatusNoContent)
	resp = efsCall(t, http.MethodGet, srv.URL+apiPrefix+"/file-systems?FileSystemId="+fsID, nil)
	expectErrorCode(t, resp, http.StatusNotFound, "FileSystemNotFound")
}

func TestMountTargetsAndAccessPoints_OverREST(t *testing.T) {
	srv := helpers.NewTestServer(t)
	created := createFileSystem(t, srv, "mt-ap", nil)
	fsID := created["FileSystemId"].(string)

	// Mount target: create, list, security groups, delete.
	mt := expectJSONStatus(t, efsCall(t, http.MethodPost, srv.URL+apiPrefix+"/mount-targets", map[string]any{
		"FileSystemId":   fsID,
		"SubnetId":       "subnet-0123",
		"SecurityGroups": []string{"sg-1"},
	}), http.StatusOK)
	mtID := mt["MountTargetId"].(string)
	if mt["IpAddress"] == "" || mt["AvailabilityZoneName"] == "" {
		t.Fatalf("expected synthesized network fields, got %#v", mt)
	}

	// File system deletion is blocked while the mount target exists.
	resp := efsCall(t, http.MethodDelete, srv.URL+apiPrefix+"/file-systems/"+fsID, nil)
	expectErrorCode(t, resp, http.StatusConflict, "FileSystemInUse")

	// Modify + describe security groups.
	expectStatus(t, efsCall(t, http.MethodPut, srv.URL+apiPrefix+"/mount-targets/"+mtID+"/security-groups", map[string]any{
		"SecurityGroups": []string{"sg-2", "sg-3"},
	}), http.StatusNoContent)
	groups := expectJSONStatus(t, efsCall(t, http.MethodGet, srv.URL+apiPrefix+"/mount-targets/"+mtID+"/security-groups", nil), http.StatusOK)
	if list, _ := groups["SecurityGroups"].([]any); len(list) != 2 {
		t.Fatalf("expected 2 security groups, got %#v", groups)
	}

	// Access point with client token + posix user.
	ap := expectJSONStatus(t, efsCall(t, http.MethodPost, srv.URL+apiPrefix+"/access-points", map[string]any{
		"ClientToken":  "ap-token",
		"FileSystemId": fsID,
		"PosixUser":    map[string]any{"Uid": 1000, "Gid": 1000},
		"Tags":         []map[string]string{{"Key": "Name", "Value": "it-ap"}},
	}), http.StatusOK)
	apID := ap["AccessPointId"].(string)
	if ap["Name"] != "it-ap" || ap["AccessPointArn"] == "" {
		t.Fatalf("unexpected access point: %#v", ap)
	}

	list := expectJSONStatus(t, efsCall(t, http.MethodGet, srv.URL+apiPrefix+"/access-points?FileSystemId="+fsID, nil), http.StatusOK)
	if aps, _ := list["AccessPoints"].([]any); len(aps) != 1 {
		t.Fatalf("expected 1 access point, got %#v", list)
	}

	// Tag operations via resource-tags routes.
	expectStatus(t, efsCall(t, http.MethodPost, srv.URL+apiPrefix+"/resource-tags/"+apID, map[string]any{
		"Tags": []map[string]string{{"Key": "team", "Value": "storage"}},
	}), http.StatusOK)
	tags := expectJSONStatus(t, efsCall(t, http.MethodGet, srv.URL+apiPrefix+"/resource-tags/"+apID, nil), http.StatusOK)
	if tagList, _ := tags["Tags"].([]any); len(tagList) != 2 {
		t.Fatalf("expected 2 tags, got %#v", tags)
	}
	expectStatus(t, efsCall(t, http.MethodDelete, srv.URL+apiPrefix+"/resource-tags/"+apID+"?"+url.Values{"tagKeys": {"team"}}.Encode(), nil), http.StatusOK)

	// Cleanup order: access point, mount target, file system.
	expectStatus(t, efsCall(t, http.MethodDelete, srv.URL+apiPrefix+"/access-points/"+apID, nil), http.StatusNoContent)
	expectStatus(t, efsCall(t, http.MethodDelete, srv.URL+apiPrefix+"/mount-targets/"+mtID, nil), http.StatusNoContent)
	expectStatus(t, efsCall(t, http.MethodDelete, srv.URL+apiPrefix+"/file-systems/"+fsID, nil), http.StatusNoContent)
}

func TestPoliciesAndConfiguration_OverREST(t *testing.T) {
	srv := helpers.NewTestServer(t)
	created := createFileSystem(t, srv, "policies", map[string]any{"Backup": true})
	fsID := created["FileSystemId"].(string)

	// Backup policy reflects the Backup create flag.
	backup := expectJSONStatus(t, efsCall(t, http.MethodGet, srv.URL+apiPrefix+"/file-systems/"+fsID+"/backup-policy", nil), http.StatusOK)
	if bp, _ := backup["BackupPolicy"].(map[string]any); bp["Status"] != "ENABLED" {
		t.Fatalf("expected ENABLED, got %#v", backup)
	}

	// File-system policy: missing → 404, then put/get/delete.
	resp := efsCall(t, http.MethodGet, srv.URL+apiPrefix+"/file-systems/"+fsID+"/policy", nil)
	expectErrorCode(t, resp, http.StatusNotFound, "PolicyNotFound")
	policyDoc := `{"Version":"2012-10-17","Statement":[]}`
	put := expectJSONStatus(t, efsCall(t, http.MethodPut, srv.URL+apiPrefix+"/file-systems/"+fsID+"/policy", map[string]any{
		"Policy": policyDoc,
	}), http.StatusOK)
	if put["Policy"] != policyDoc {
		t.Fatalf("policy not echoed: %#v", put)
	}
	expectStatus(t, efsCall(t, http.MethodDelete, srv.URL+apiPrefix+"/file-systems/"+fsID+"/policy", nil), http.StatusOK)

	// Lifecycle configuration round-trip.
	lc := expectJSONStatus(t, efsCall(t, http.MethodPut, srv.URL+apiPrefix+"/file-systems/"+fsID+"/lifecycle-configuration", map[string]any{
		"LifecyclePolicies": []map[string]string{{"TransitionToIA": "AFTER_30_DAYS"}},
	}), http.StatusOK)
	if policies, _ := lc["LifecyclePolicies"].([]any); len(policies) != 1 {
		t.Fatalf("expected 1 lifecycle policy, got %#v", lc)
	}

	// Legacy tag API round-trip.
	expectStatus(t, efsCall(t, http.MethodPost, srv.URL+apiPrefix+"/create-tags/"+fsID, map[string]any{
		"Tags": []map[string]string{{"Key": "legacy", "Value": "yes"}},
	}), http.StatusNoContent)
	legacy := expectJSONStatus(t, efsCall(t, http.MethodGet, srv.URL+apiPrefix+"/tags/"+fsID+"/", nil), http.StatusOK)
	if tagList, _ := legacy["Tags"].([]any); len(tagList) != 1 {
		t.Fatalf("expected 1 legacy tag, got %#v", legacy)
	}
	expectStatus(t, efsCall(t, http.MethodPost, srv.URL+apiPrefix+"/delete-tags/"+fsID, map[string]any{
		"TagKeys": []string{"legacy"},
	}), http.StatusNoContent)

	// Account preferences.
	prefs := expectJSONStatus(t, efsCall(t, http.MethodGet, srv.URL+apiPrefix+"/account-preferences", nil), http.StatusOK)
	if p, _ := prefs["ResourceIdPreference"].(map[string]any); p["ResourceIdType"] != "LONG_ID" {
		t.Fatalf("expected LONG_ID, got %#v", prefs)
	}
}

// TestXAmzTarget_noLongerDispatches pins #1226: EFS is restJson1 and the
// pinned models give it no X-Amz-Target prefix, so a request shaped like the
// invented "EFS.<Op>" wire CloudFormation's provisioner used to speak
// internally must get the same honest "unknown operation" answer any
// nonsense target gets — never a 200, and never silently proxied elsewhere.
func TestXAmzTarget_noLongerDispatches(t *testing.T) {
	srv := helpers.NewTestServer(t)
	created := createFileSystem(t, srv, "typed", nil)
	fsID := created["FileSystemId"].(string)

	payload, _ := json.Marshal(map[string]any{"FileSystemId": fsID})
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "EFS.DescribeFileSystems")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		t.Fatalf("X-Amz-Target: EFS.DescribeFileSystems answered 200 — the invented wire is still reachable")
	}
	helpers.AssertJSONError(t, resp, "UnknownOperationException")
}

func TestUnimplementedOperation_Returns501(t *testing.T) {
	srv := helpers.NewTestServer(t)
	created := createFileSystem(t, srv, "repl", nil)
	fsID := created["FileSystemId"].(string)

	// CreateReplicationConfiguration is modeled but not implemented: the
	// generated operation registry must claim it with a protocol-correct 501.
	// The request must carry the elasticfilesystem SigV4 scope (as real SDKs
	// do) — unsigned unmatched paths belong to S3's fallback by design.
	payload, _ := json.Marshal(map[string]any{"Destinations": []map[string]string{{"Region": "us-west-2"}}})
	req, err := http.NewRequest(http.MethodPost, srv.URL+apiPrefix+"/file-systems/"+fsID+"/replication-configuration", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260731/us-east-1/elasticfilesystem/aws4_request, SignedHeaders=host, Signature=deadbeef")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		body := helpers.ReadBody(t, resp)
		t.Fatalf("expected 501, got %d; body=%s", resp.StatusCode, body)
	}
	if resp.Header.Get("x-emulator-unsupported") != "true" {
		t.Fatal("expected x-emulator-unsupported header on 501")
	}
}
