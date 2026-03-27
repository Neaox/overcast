package helpers

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"testing"
)

// AssertStatus fails the test if the response status code doesn't match expected.
func AssertStatus(t *testing.T, resp *http.Response, expected int) {
	t.Helper()
	if resp.StatusCode != expected {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("expected status %d, got %d\nbody: %s", expected, resp.StatusCode, string(body))
	}
}

// AssertHeader fails the test if the named header is not present or doesn't match value.
// Pass an empty value string to just assert presence.
func AssertHeader(t *testing.T, resp *http.Response, header, value string) {
	t.Helper()
	got := resp.Header.Get(header)
	if value == "" && got == "" {
		t.Errorf("expected header %q to be present, but it was missing", header)
		return
	}
	if value != "" && got != value {
		t.Errorf("header %q: expected %q, got %q", header, value, got)
	}
}

// AssertRequestID fails the test if x-amzn-requestid or x-amz-request-id is missing.
func AssertRequestID(t *testing.T, resp *http.Response) {
	t.Helper()
	if resp.Header.Get("x-amzn-requestid") == "" && resp.Header.Get("x-amz-request-id") == "" {
		t.Error("response is missing request ID header (x-amzn-requestid or x-amz-request-id)")
	}
}

// DecodeJSON decodes the response body into v and fails the test on error.
func DecodeJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("failed to decode JSON response: %v\nbody: %s", err, string(body))
	}
}

// DecodeXML decodes the response body into v and fails the test on error.
func DecodeXML(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := xml.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("failed to decode XML response: %v", err)
	}
}

// ReadBody reads and returns the response body as a string.
func ReadBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}
	return string(body)
}

// AssertJSONError decodes the response as a JSON error and checks the error code.
func AssertJSONError(t *testing.T, resp *http.Response, expectedCode string) {
	t.Helper()
	var errResp struct {
		Type    string `json:"__type"`
		Message string `json:"message"`
	}
	DecodeJSON(t, resp, &errResp)
	if errResp.Type != expectedCode {
		t.Errorf("expected error code %q, got %q (message: %s)", expectedCode, errResp.Type, errResp.Message)
	}
}

// AssertXMLError decodes the response as an XML error and checks the error code.
func AssertXMLError(t *testing.T, resp *http.Response, expectedCode string) {
	t.Helper()
	var errResp struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	DecodeXML(t, resp, &errResp)
	if errResp.Code != expectedCode {
		t.Errorf("expected error code %q, got %q (message: %s)", expectedCode, errResp.Code, errResp.Message)
	}
}
