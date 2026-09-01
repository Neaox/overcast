package cloudformation_test

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// maxInlineTemplateBytes mirrors the AWS cap on an inline TemplateBody. A
// template larger than this must be passed by TemplateURL instead.
const maxInlineTemplateBytes = 51_200

// templateOfSize returns a valid template whose serialized length is exactly
// size bytes, padded through the Description field.
func templateOfSize(size int) string {
	const head = `{"AWSTemplateFormatVersion":"2010-09-09","Description":"`
	const tail = `","Resources":{"Q":{"Type":"AWS::SQS::Queue","Properties":{}}}}`
	pad := size - len(head) - len(tail)
	if pad < 0 {
		pad = 0
	}
	return head + strings.Repeat("x", pad) + tail
}

type cfnErrorResponse struct {
	XMLName xml.Name `xml:"ErrorResponse"`
	Error   struct {
		Type    string `xml:"Type"`
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	} `xml:"Error"`
}

// assertTemplateTooLarge reads a response and asserts it is the AWS-shaped
// refusal of an oversized inline template: ValidationError, HTTP 400, and a
// message naming the byte limit so the caller learns what to do about it.
func assertTemplateTooLarge(t *testing.T, resp *http.Response) {
	t.Helper()
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusNotImplemented {
		t.Fatalf("status = 501: an oversized template was reported as an unimplemented operation; body = %s", clip(body))
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", resp.StatusCode, clip(body))
	}
	var parsed cfnErrorResponse
	if err := xml.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal error response: %v; body = %s", err, clip(body))
	}
	if parsed.Error.Code != "ValidationError" {
		t.Errorf("error code = %q, want ValidationError", parsed.Error.Code)
	}
	if !strings.Contains(parsed.Error.Message, "51200") {
		t.Errorf("message = %q, want it to name the 51200 byte limit", parsed.Error.Message)
	}
	if !strings.Contains(parsed.Error.Message, "templateBody") {
		t.Errorf("message = %q, want it to name the templateBody member", parsed.Error.Message)
	}
}

// TestCreateStack_InlineTemplateBodyOverLimit is the case behind the bug: an
// inline template past the AWS cap must be refused as a validation error rather
// than accepted.
func TestCreateStack_InlineTemplateBodyOverLimit(t *testing.T) {
	srv := helpers.NewTestServer(t)

	assertTemplateTooLarge(t, cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"oversize-inline"},
		"TemplateBody": {templateOfSize(maxInlineTemplateBytes + 1)},
	}))
}

// TestCreateStack_InlineTemplateBodyAtLimit pins the boundary from the other
// side: exactly 51,200 bytes is within the cap and must still deploy.
func TestCreateStack_InlineTemplateBodyAtLimit(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"at-limit-inline"},
		"TemplateBody": {templateOfSize(maxInlineTemplateBytes)},
	})
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 at exactly the limit; body = %s", resp.StatusCode, clip(body))
	}
}

// TestCreateStack_InlineTemplateBodyOverQueryFormBuffer sends a template large
// enough that the Query form parser stops buffering the body speculatively, so
// the request takes a different dispatch path inside the service. The size
// verdict must not depend on that: it is a property of the template, not of how
// the request happened to be decoded.
func TestCreateStack_InlineTemplateBodyOverQueryFormBuffer(t *testing.T) {
	srv := helpers.NewTestServer(t)

	assertTemplateTooLarge(t, cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"oversize-inline-huge"},
		"TemplateBody": {templateOfSize(2 << 20)},
	}))
}

// TestUpdateStack_InlineTemplateBodyOverLimit and the two below extend the cap
// to every operation that accepts an inline template. AWS applies the limit at
// the parameter, not at CreateStack.
func TestUpdateStack_InlineTemplateBodyOverLimit(t *testing.T) {
	srv := helpers.NewTestServer(t)

	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"update-oversize"},
		"TemplateBody": {templateOfSize(1024)},
	})
	create.Body.Close()

	assertTemplateTooLarge(t, cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {"update-oversize"},
		"TemplateBody": {templateOfSize(maxInlineTemplateBytes + 1)},
	}))
}

func TestValidateTemplate_InlineTemplateBodyOverLimit(t *testing.T) {
	srv := helpers.NewTestServer(t)

	assertTemplateTooLarge(t, cfnQuery(t, srv, "ValidateTemplate", url.Values{
		"TemplateBody": {templateOfSize(maxInlineTemplateBytes + 1)},
	}))
}

func TestCreateChangeSet_InlineTemplateBodyOverLimit(t *testing.T) {
	srv := helpers.NewTestServer(t)

	assertTemplateTooLarge(t, cfnQuery(t, srv, "CreateChangeSet", url.Values{
		"StackName":     {"changeset-oversize"},
		"ChangeSetName": {"cs1"},
		"ChangeSetType": {"CREATE"},
		"TemplateBody":  {templateOfSize(maxInlineTemplateBytes + 1)},
	}))
}

func TestGetTemplateSummary_InlineTemplateBodyOverLimit(t *testing.T) {
	srv := helpers.NewTestServer(t)

	assertTemplateTooLarge(t, cfnQuery(t, srv, "GetTemplateSummary", url.Values{
		"TemplateBody": {templateOfSize(maxInlineTemplateBytes + 1)},
	}))
}

// TestCreateStack_TemplateURLIsNotCappedAtTheInlineLimit proves the escape
// hatch the error message points at actually works. The 51,200-byte cap is on
// the inline parameter alone; a template fetched from S3 is allowed to be
// larger, which is the entire reason TemplateURL exists.
func TestCreateStack_TemplateURLIsNotCappedAtTheInlineLimit(t *testing.T) {
	srv := helpers.NewTestServer(t)

	putS3Object(t, srv, "cfn-templates", "big.json", templateOfSize(maxInlineTemplateBytes+1))

	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":   {"from-url"},
		"TemplateURL": {srv.URL + "/cfn-templates/big.json"},
	})
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 for an oversized template passed by URL; body = %s", resp.StatusCode, clip(body))
	}
}

// maxResolvedTemplateBytes mirrors AWS's cap on a template passed as an S3
// object. It is a decimal megabyte, not a mebibyte: the error AWS returns names
// 1000000 exactly.
const maxResolvedTemplateBytes = 1_000_000

// TestCreateStack_TemplateURLOverTheResolvedLimit closes the other end of the
// escape hatch. TemplateURL lifts the 51,200-byte cap but does not remove every
// cap, and without this the divergence the inline check removed would simply
// have moved to the path the error message sends people down.
func TestCreateStack_TemplateURLOverTheResolvedLimit(t *testing.T) {
	srv := helpers.NewTestServer(t)

	putS3Object(t, srv, "cfn-templates", "huge.json", templateOfSize(maxResolvedTemplateBytes+1))

	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":   {"from-url-too-big"},
		"TemplateURL": {srv.URL + "/cfn-templates/huge.json"},
	})
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", resp.StatusCode, clip(body))
	}
	var parsed cfnErrorResponse
	if err := xml.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal error response: %v; body = %s", err, clip(body))
	}
	if parsed.Error.Code != "ValidationError" {
		t.Errorf("error code = %q, want ValidationError", parsed.Error.Code)
	}
	// AWS's wording, verbatim.
	if parsed.Error.Message != "Template may not exceed 1000000 bytes in size." {
		t.Errorf("message = %q, want AWS's \"Template may not exceed 1000000 bytes in size.\"", parsed.Error.Message)
	}
}

// TestCreateStack_TemplateURLAtTheResolvedLimit pins the boundary. A decimal
// megabyte is not a mebibyte, and picking the wrong one would reject 48,576
// bytes' worth of templates AWS accepts.
func TestCreateStack_TemplateURLAtTheResolvedLimit(t *testing.T) {
	srv := helpers.NewTestServer(t)

	putS3Object(t, srv, "cfn-templates", "atlimit.json", templateOfSize(maxResolvedTemplateBytes))

	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":   {"from-url-at-limit"},
		"TemplateURL": {srv.URL + "/cfn-templates/atlimit.json"},
	})
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 at exactly the limit; body = %s", resp.StatusCode, clip(body))
	}
}

// TestNestedStack_TemplateURLOverTheResolvedLimit applies the same cap where a
// nested stack fetches its child template. On AWS the child comes through the
// same TemplateURL parameter and is bound by the same limit, so a parent that
// deploys here with an oversized child would be the original bug in miniature.
func TestNestedStack_TemplateURLOverTheResolvedLimit(t *testing.T) {
	srv := helpers.NewTestServer(t)

	putS3Object(t, srv, "cfn-templates", "child-huge.json", templateOfSize(maxResolvedTemplateBytes+1))

	parent := `{"AWSTemplateFormatVersion":"2010-09-09","Resources":{"Child":{` +
		`"Type":"AWS::CloudFormation::Stack","Properties":{"TemplateURL":"` +
		srv.URL + `/cfn-templates/child-huge.json"}}}}`

	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"nested-too-big"},
		"TemplateBody": {parent},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CreateStack status = %d, want 200 — the child fails during provisioning, not at the API", resp.StatusCode)
	}

	// The child's size is what must fail it. Reaching CREATE_COMPLETE would mean
	// a parent deployed here that AWS would have refused.
	waitForStackStatusIn(t, srv, "nested-too-big",
		"CREATE_FAILED", "ROLLBACK_IN_PROGRESS", "ROLLBACK_COMPLETE")
}

// putS3Object stores an object through the emulator's own S3 service so the
// CloudFormation TemplateURL fetch resolves it.
func putS3Object(t *testing.T, srv *helpers.TestServer, bucket, key, content string) {
	t.Helper()
	mk, err := http.NewRequest(http.MethodPut, srv.URL+"/"+bucket, nil)
	if err != nil {
		t.Fatalf("new bucket request: %v", err)
	}
	resp, err := http.DefaultClient.Do(mk)
	if err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	resp.Body.Close()

	put, err := http.NewRequest(http.MethodPut, srv.URL+"/"+bucket+"/"+key, strings.NewReader(content))
	if err != nil {
		t.Fatalf("new object request: %v", err)
	}
	put.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(put)
	if err != nil {
		t.Fatalf("put object: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("put object: status = %d; body = %s", resp.StatusCode, clip(body))
	}
}

func clip(b []byte) string {
	if len(b) > 400 {
		return string(b[:400]) + "..."
	}
	return string(b)
}
