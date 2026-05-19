package organizations_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func orgsCall(t *testing.T, method, url string, body any) *http.Response {
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
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "AWSOrganizationsV20161128.DescribeOrganization")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http request: %v", err)
	}
	return resp
}

func decodeBody(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body
}

func TestDescribeOrganization(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithServices("organizations"))

	resp := orgsCall(t, http.MethodPost, srv.URL+"/", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body := decodeBody(t, resp)
	org, ok := body["Organization"].(map[string]any)
	if !ok {
		t.Fatalf("expected Organization object, got %#v", body)
	}
	if org["Id"] != "o-overcast" {
		t.Fatalf("unexpected org id: %v", org["Id"])
	}
	if org["MasterAccountId"] != "000000000000" {
		t.Fatalf("unexpected master account id: %v", org["MasterAccountId"])
	}
	if org["Arn"] == "" {
		t.Fatalf("expected non-empty org ARN")
	}
}
