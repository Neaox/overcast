package cloudtrail_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

const cloudTrailTargetPrefix = "com.amazonaws.cloudtrail.v20131101.CloudTrail_20131101."

func ctCall(t *testing.T, srv *helpers.TestServer, action string, body any) *http.Response {
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
	req.Header.Set("X-Amz-Target", cloudTrailTargetPrefix+action)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request %s: %v", action, err)
	}
	return resp
}

func decodeMap(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()

	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return out
}

func TestCreateDescribeDeleteTrail_roundTrip(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithServices("cloudtrail"))

	createResp := ctCall(t, srv, "CreateTrail", map[string]any{
		"Name":                       "trail-a",
		"S3BucketName":               "logs-bucket",
		"IncludeGlobalServiceEvents": true,
	})
	if createResp.StatusCode != http.StatusOK {
		t.Fatalf("CreateTrail status: got %d want 200", createResp.StatusCode)
	}
	createBody := decodeMap(t, createResp)
	if got := createBody["Name"]; got != "trail-a" {
		t.Fatalf("CreateTrail Name: got %v want trail-a", got)
	}

	descResp := ctCall(t, srv, "DescribeTrails", map[string]any{})
	if descResp.StatusCode != http.StatusOK {
		t.Fatalf("DescribeTrails status: got %d want 200", descResp.StatusCode)
	}
	descBody := decodeMap(t, descResp)
	trailsRaw, ok := descBody["trailList"].([]any)
	if !ok {
		t.Fatalf("DescribeTrails trailList missing or wrong type: %#v", descBody)
	}
	if len(trailsRaw) != 1 {
		t.Fatalf("DescribeTrails trail count: got %d want 1", len(trailsRaw))
	}

	delResp := ctCall(t, srv, "DeleteTrail", map[string]any{"Name": "trail-a"})
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("DeleteTrail status: got %d want 200", delResp.StatusCode)
	}
	_ = decodeMap(t, delResp)

	descAfterResp := ctCall(t, srv, "DescribeTrails", map[string]any{})
	if descAfterResp.StatusCode != http.StatusOK {
		t.Fatalf("DescribeTrails after delete status: got %d want 200", descAfterResp.StatusCode)
	}
	descAfterBody := decodeMap(t, descAfterResp)
	trailsAfterRaw, ok := descAfterBody["trailList"].([]any)
	if !ok {
		t.Fatalf("DescribeTrails after delete trailList missing or wrong type: %#v", descAfterBody)
	}
	if len(trailsAfterRaw) != 0 {
		t.Fatalf("DescribeTrails after delete trail count: got %d want 0", len(trailsAfterRaw))
	}
}

func TestLookupEvents_inertEmptyResult(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithServices("cloudtrail"))

	resp := ctCall(t, srv, "LookupEvents", map[string]any{
		"MaxResults": 20,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("LookupEvents status: got %d want 200", resp.StatusCode)
	}
	body := decodeMap(t, resp)

	events, ok := body["Events"].([]any)
	if !ok {
		t.Fatalf("LookupEvents Events missing or wrong type: %#v", body)
	}
	if len(events) != 0 {
		t.Fatalf("LookupEvents event count: got %d want 0", len(events))
	}
}
