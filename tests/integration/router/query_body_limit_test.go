package router_test

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/tests/helpers"
)

type queryXMLError struct {
	XMLName xml.Name `xml:"ErrorResponse"`
	Error   struct {
		Type    string `xml:"Type"`
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	} `xml:"Error"`
}

// postQueryForm sends a Query-protocol POST whose body is padded to at least
// size bytes, without ever holding the encoded form in memory twice.
func postQueryForm(t *testing.T, endpoint string, params url.Values, padTo int) *http.Response {
	t.Helper()
	encoded := params.Encode()
	if pad := padTo - len(encoded) - len("&Padding="); pad > 0 {
		encoded += "&Padding=" + strings.Repeat("x", pad)
	}
	req, err := http.NewRequest(http.MethodPost, endpoint+"/", strings.NewReader(encoded))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post query form: %v", err)
	}
	return resp
}

// TestQueryRequestOverBodyLimit_isNotReportedAsUnimplemented covers the answer
// a Query-protocol request gets when its body is too large for the server to
// parse as a form at all.
//
// The operation is implemented; only the body is unacceptable. Answering 501
// NotImplemented — with the x-emulator-unsupported header that tells tooling
// Overcast lacks the operation — sends a caller looking for a workaround to a
// feature gap that does not exist. See internal/protocol.ErrMethodNotAllowed
// for the same distinction drawn for a method AWS itself refuses.
func TestQueryRequestOverBodyLimit_isNotReportedAsUnimplemented(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := postQueryForm(t, srv.URL, url.Values{
		"Action":    {"CreateStack"},
		"Version":   {"2010-05-15"},
		"StackName": {"too-large"},
	}, protocol.MaxQueryRequestBody+1)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusNotImplemented {
		t.Fatalf("status = 501: an oversized body was reported as an unimplemented operation; body = %s", body)
	}
	if got := resp.Header.Get("x-emulator-unsupported"); got != "" {
		t.Errorf("x-emulator-unsupported = %q, want it absent", got)
	}
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body = %s", resp.StatusCode, body)
	}

	var parsed queryXMLError
	if err := xml.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal error response: %v; body = %s", err, body)
	}
	if parsed.Error.Code != "RequestEntityTooLarge" {
		t.Errorf("error code = %q, want RequestEntityTooLarge", parsed.Error.Code)
	}
}

// TestQueryRequestOverBodyLimit_answersEveryQueryService is the general half of
// the same claim: the fallthrough being fixed lives in the shared POST /
// dispatcher, so the answer must not depend on which Query service was
// addressed — including one whose action nothing implements.
func TestQueryRequestOverBodyLimit_answersEveryQueryService(t *testing.T) {
	srv := helpers.NewTestServer(t)

	for _, tc := range []struct {
		name    string
		action  string
		version string
	}{
		{"sns", "Publish", "2010-03-31"},
		{"iam", "CreateUser", "2010-05-08"},
		{"ec2", "RunInstances", "2016-11-15"},
		{"unknown action", "NoSuchActionAnywhere", "2010-05-15"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := postQueryForm(t, srv.URL, url.Values{
				"Action":  {tc.action},
				"Version": {tc.version},
			}, protocol.MaxQueryRequestBody+1)
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)

			if resp.StatusCode != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d, want 413; body = %s", resp.StatusCode, body)
			}
			if got := resp.Header.Get("x-emulator-unsupported"); got != "" {
				t.Errorf("x-emulator-unsupported = %q, want it absent", got)
			}
		})
	}
}

// TestQueryRequestWithUnparseableBody_isNotReportedAsUnimplemented is the other
// way the same parse fails. Size is not the only reason a form body does not
// decode, and neither reason says anything about whether the operation exists.
//
// The padding is what makes this reach the branch. Below
// protocol.MaxQueryFormBody the pre-routing parse has already cached whatever
// it could decode on r.PostForm, which makes the dispatcher's own ParseForm a
// no-op that reports no error — so a small malformed body routes on its
// partial fields and gets its service's own answer. Only past that limit is the
// dispatcher the first to look at the body.
func TestQueryRequestWithUnparseableBody_isNotReportedAsUnimplemented(t *testing.T) {
	srv := helpers.NewTestServer(t)

	// %ZZ is not a valid percent escape, so ParseForm rejects the body.
	malformed := "Action=CreateStack&Version=2010-05-15&Bad=%ZZ&Padding=" +
		strings.Repeat("x", protocol.MaxQueryFormBody)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(malformed))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("x-emulator-unsupported"); got != "" {
		t.Errorf("x-emulator-unsupported = %q, want it absent", got)
	}
	var parsed queryXMLError
	if err := xml.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal error response: %v; body = %s", err, body)
	}
	if parsed.Error.Code != "MalformedQueryString" {
		t.Errorf("error code = %q, want MalformedQueryString", parsed.Error.Code)
	}
}

// TestQueryRequestUnderBodyLimit_isStillDispatched pins the other side of the
// boundary: the limit only decides what an already-refused request is told, and
// must not start refusing a request that works today. A Query form larger than
// protocol.MaxQueryFormBody is legitimate — SQS accepts a 1 MiB MessageBody,
// which URL-encodes to more than that — and must still reach its service.
func TestQueryRequestUnderBodyLimit_isStillDispatched(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := postQueryForm(t, srv.URL, url.Values{
		"Action":  {"CreateStack"},
		"Version": {"2010-05-15"},
		// No StackName: CloudFormation's own validation is the proof that the
		// request was decoded and dispatched rather than refused wholesale.
	}, protocol.MaxQueryFormBody*2)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 from CloudFormation's own validation; body = %s", resp.StatusCode, body)
	}
	var parsed queryXMLError
	if err := xml.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal error response: %v; body = %s", err, body)
	}
	if parsed.Error.Code != "ValidationError" {
		t.Errorf("error code = %q, want ValidationError from CloudFormation", parsed.Error.Code)
	}
}
