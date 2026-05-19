// Package ses_test contains integration tests for the SES service emulator.
//
// It covers both the SES v1 Query-protocol API (used by aws-sdk-go-v2/service/ses,
// boto3 ses, @aws-sdk/client-ses) and the SES v2 REST-JSON API (used by
// aws-sdk-go-v2/service/sesv2, boto3 sesv2, @aws-sdk/client-sesv2).
//
// Run: go test ./tests/integration/ses/...
package ses_test

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// sesCall performs an SES v1 Query-protocol POST / request.
func sesCall(t *testing.T, srv *helpers.TestServer, action string, params url.Values) *http.Response {
	t.Helper()
	params.Set("Action", action)
	params.Set("Version", "2010-12-01")
	body := strings.NewReader(params.Encode())
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", body)
	if err != nil {
		t.Fatalf("sesCall: new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Amz-Target", "") // no Target header — SES uses form body
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sesCall: do: %v", err)
	}
	return resp
}

// v2Call performs an SES v2 REST-JSON request.
func v2Call(t *testing.T, srv *helpers.TestServer, method, path string, body any) *http.Response {
	t.Helper()
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("v2Call: marshal: %v", err)
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, srv.URL+path, reqBody)
	if err != nil {
		t.Fatalf("v2Call: new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("v2Call: do: %v", err)
	}
	return resp
}

func decodeXML(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("decodeXML: read: %v", err)
	}
	if err := xml.Unmarshal(b, dst); err != nil {
		t.Fatalf("decodeXML: unmarshal: %v\nbody: %s", err, b)
	}
}

func decodeJSON(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("decodeJSON: read: %v", err)
	}
	if err := json.Unmarshal(b, dst); err != nil {
		t.Fatalf("decodeJSON: unmarshal: %v\nbody: %s", err, b)
	}
}

// ─── SES v1 — VerifyEmailIdentity ────────────────────────────────────────────

func TestSES_VerifyEmailIdentity_success(t *testing.T) {
	// Given a running server
	srv := helpers.NewTestServer(t)

	// When I verify an email address
	resp := sesCall(t, srv, "VerifyEmailIdentity", url.Values{
		"EmailAddress": {"alice@example.com"},
	})
	defer resp.Body.Close()

	// Then the response is 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestSES_VerifyEmailIdentity_missingParam(t *testing.T) {
	// Given a running server
	srv := helpers.NewTestServer(t)

	// When I call VerifyEmailIdentity without EmailAddress
	resp := sesCall(t, srv, "VerifyEmailIdentity", url.Values{})
	defer resp.Body.Close()

	// Then the response is 400 Bad Request
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

// ─── SES v1 — VerifyDomainIdentity ───────────────────────────────────────────

func TestSES_VerifyDomainIdentity_success(t *testing.T) {
	// Given a running server
	srv := helpers.NewTestServer(t)

	// When I verify a domain
	resp := sesCall(t, srv, "VerifyDomainIdentity", url.Values{
		"Domain": {"example.com"},
	})
	defer resp.Body.Close()

	// Then the response is 200 OK with a verification token
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		XMLName xml.Name `xml:"VerifyDomainIdentityResponse"`
		Result  struct {
			VerificationToken string `xml:"VerificationToken"`
		} `xml:"VerifyDomainIdentityResult"`
	}
	decodeXML(t, resp, &result)
	if result.Result.VerificationToken == "" {
		t.Error("expected VerificationToken to be set")
	}
}

// ─── SES v1 — ListIdentities ──────────────────────────────────────────────────

func TestSES_ListIdentities_empty(t *testing.T) {
	// Given a running server with no identities
	srv := helpers.NewTestServer(t)

	// When I list identities
	resp := sesCall(t, srv, "ListIdentities", url.Values{})
	defer resp.Body.Close()

	// Then the response is 200 OK with an empty list
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestSES_ListIdentities_returnsVerified(t *testing.T) {
	// Given a server with a verified email
	srv := helpers.NewTestServer(t)
	r := sesCall(t, srv, "VerifyEmailIdentity", url.Values{"EmailAddress": {"bob@example.com"}})
	r.Body.Close()

	// When I list identities
	resp := sesCall(t, srv, "ListIdentities", url.Values{})
	defer resp.Body.Close()

	// Then the email appears in the list
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		XMLName xml.Name `xml:"ListIdentitiesResponse"`
		Result  struct {
			Identities struct {
				Members []string `xml:"member"`
			} `xml:"Identities"`
		} `xml:"ListIdentitiesResult"`
	}
	decodeXML(t, resp, &result)
	found := false
	for _, id := range result.Result.Identities.Members {
		if id == "bob@example.com" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected bob@example.com in identities, got %v", result.Result.Identities.Members)
	}
}

// ─── SES v1 — GetIdentityVerificationAttributes ───────────────────────────────

func TestSES_GetIdentityVerificationAttributes_verified(t *testing.T) {
	// Given a server with a verified email
	srv := helpers.NewTestServer(t)
	r := sesCall(t, srv, "VerifyEmailIdentity", url.Values{"EmailAddress": {"carol@example.com"}})
	r.Body.Close()

	// When I get verification attributes
	resp := sesCall(t, srv, "GetIdentityVerificationAttributes", url.Values{
		"Identities.member.1": {"carol@example.com"},
	})
	defer resp.Body.Close()

	// Then the identity shows as Success
	helpers.AssertStatus(t, resp, http.StatusOK)
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "Success") {
		t.Errorf("expected VerificationStatus=Success in response, got: %s", b)
	}
}

// ─── SES v1 — DeleteIdentity ──────────────────────────────────────────────────

func TestSES_DeleteIdentity_success(t *testing.T) {
	// Given a verified email
	srv := helpers.NewTestServer(t)
	r := sesCall(t, srv, "VerifyEmailIdentity", url.Values{"EmailAddress": {"dave@example.com"}})
	r.Body.Close()

	// When I delete it
	resp := sesCall(t, srv, "DeleteIdentity", url.Values{"Identity": {"dave@example.com"}})
	defer resp.Body.Close()

	// Then 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And it no longer appears in the list
	listResp := sesCall(t, srv, "ListIdentities", url.Values{})
	defer listResp.Body.Close()
	b, _ := io.ReadAll(listResp.Body)
	if strings.Contains(string(b), "dave@example.com") {
		t.Error("expected dave@example.com to be deleted, but still in list")
	}
}

// ─── SES v1 — SendEmail (no mailer) ──────────────────────────────────────────

func TestSES_SendEmail_noMailer(t *testing.T) {
	// Given a server with no SMTP configured
	srv := helpers.NewTestServer(t)

	// When I send an email
	resp := sesCall(t, srv, "SendEmail", url.Values{
		"Source":                           {"sender@example.com"},
		"Destination.ToAddresses.member.1": {"recipient@example.com"},
		"Message.Subject.Data":             {"Hello"},
		"Message.Body.Text.Data":           {"Hello world"},
	})
	defer resp.Body.Close()

	// Then it succeeds with a MessageId (delivery is no-op without mailer)
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		XMLName xml.Name `xml:"SendEmailResponse"`
		Result  struct {
			MessageId string `xml:"MessageId"`
		} `xml:"SendEmailResult"`
	}
	decodeXML(t, resp, &result)
	if result.Result.MessageId == "" {
		t.Error("expected MessageId to be set")
	}
}

func TestSES_SendEmail_missingSource(t *testing.T) {
	// Given a server
	srv := helpers.NewTestServer(t)

	// When I send without a Source
	resp := sesCall(t, srv, "SendEmail", url.Values{
		"Destination.ToAddresses.member.1": {"recipient@example.com"},
		"Message.Subject.Data":             {"Hello"},
		"Message.Body.Text.Data":           {"Hello world"},
	})
	defer resp.Body.Close()

	// Then 400
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

// ─── SES v1 — GetSendQuota ────────────────────────────────────────────────────

func TestSES_GetSendQuota(t *testing.T) {
	// Given a server
	srv := helpers.NewTestServer(t)

	// When I get the send quota
	resp := sesCall(t, srv, "GetSendQuota", url.Values{})
	defer resp.Body.Close()

	// Then 200 OK with quota fields
	helpers.AssertStatus(t, resp, http.StatusOK)
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "Max24HourSend") {
		t.Errorf("expected Max24HourSend in response, got: %s", b)
	}
}

// ─── SES v2 — POST /v2/email/identities ──────────────────────────────────────

func TestSESV2_CreateEmailIdentity_email(t *testing.T) {
	// Given a running server
	srv := helpers.NewTestServer(t)

	// When I create an email identity via v2
	resp := v2Call(t, srv, http.MethodPut, "/v2/email/identities", map[string]string{
		"EmailIdentity": "test@example.com",
	})
	defer resp.Body.Close()

	// Then 200 OK with identity type
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result map[string]any
	decodeJSON(t, resp, &result)
	if result["IdentityType"] != "EMAIL_ADDRESS" {
		t.Errorf("expected IdentityType=EMAIL_ADDRESS, got %v", result["IdentityType"])
	}
}

func TestSESV2_CreateEmailIdentity_domain(t *testing.T) {
	// Given a running server
	srv := helpers.NewTestServer(t)

	// When I create a domain identity via v2
	resp := v2Call(t, srv, http.MethodPut, "/v2/email/identities", map[string]string{
		"EmailIdentity": "example.com",
	})
	defer resp.Body.Close()

	// Then 200 OK with domain type
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result map[string]any
	decodeJSON(t, resp, &result)
	if result["IdentityType"] != "DOMAIN" {
		t.Errorf("expected IdentityType=DOMAIN, got %v", result["IdentityType"])
	}
}

// ─── SES v2 — GET /v2/email/identities ───────────────────────────────────────

func TestSESV2_ListEmailIdentities(t *testing.T) {
	// Given a server with an identity
	srv := helpers.NewTestServer(t)
	r := v2Call(t, srv, http.MethodPut, "/v2/email/identities", map[string]string{"EmailIdentity": "list@example.com"})
	r.Body.Close()

	// When I list identities
	resp := v2Call(t, srv, http.MethodGet, "/v2/email/identities", nil)
	defer resp.Body.Close()

	// Then the identity appears
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		EmailIdentities []struct {
			IdentityName string `json:"IdentityName"`
		} `json:"EmailIdentities"`
	}
	decodeJSON(t, resp, &result)
	found := false
	for _, id := range result.EmailIdentities {
		if id.IdentityName == "list@example.com" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected list@example.com in identities")
	}
}

// ─── SES v2 — GET /v2/email/identities/{EmailIdentity} ────────────────────────

func TestSESV2_GetEmailIdentity(t *testing.T) {
	// Given a server with an identity
	srv := helpers.NewTestServer(t)
	r := v2Call(t, srv, http.MethodPut, "/v2/email/identities", map[string]string{"EmailIdentity": "get@example.com"})
	r.Body.Close()

	// When I get the identity
	resp := v2Call(t, srv, http.MethodGet, "/v2/email/identities/get@example.com", nil)
	defer resp.Body.Close()

	// Then 200 OK with identity details
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result map[string]any
	decodeJSON(t, resp, &result)
	if result["IdentityType"] != "EMAIL_ADDRESS" {
		t.Errorf("expected IdentityType=EMAIL_ADDRESS, got %v", result["IdentityType"])
	}
}

// ─── SES v2 — DELETE /v2/email/identities/{EmailIdentity} ─────────────────────

func TestSESV2_DeleteEmailIdentity(t *testing.T) {
	// Given a server with an identity
	srv := helpers.NewTestServer(t)
	r := v2Call(t, srv, http.MethodPut, "/v2/email/identities", map[string]string{"EmailIdentity": "delete@example.com"})
	r.Body.Close()

	// When I delete it
	resp := v2Call(t, srv, http.MethodDelete, "/v2/email/identities/delete@example.com", nil)
	defer resp.Body.Close()

	// Then 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And it no longer appears in the list
	listResp := v2Call(t, srv, http.MethodGet, "/v2/email/identities", nil)
	defer listResp.Body.Close()
	b, _ := io.ReadAll(listResp.Body)
	if strings.Contains(string(b), "delete@example.com") {
		t.Error("expected delete@example.com to be deleted from list")
	}
}

// ─── SES v2 — POST /v2/email/outbound-emails ─────────────────────────────────

func TestSESV2_SendEmail_simple(t *testing.T) {
	// Given a server with no SMTP configured
	srv := helpers.NewTestServer(t)

	// When I send a simple email via v2
	resp := v2Call(t, srv, http.MethodPost, "/v2/email/outbound-emails", map[string]any{
		"FromEmailAddress": "sender@example.com",
		"Destination": map[string]any{
			"ToAddresses": []string{"recipient@example.com"},
		},
		"Content": map[string]any{
			"Simple": map[string]any{
				"Subject": map[string]string{"Data": "Test Subject"},
				"Body": map[string]any{
					"Text": map[string]string{"Data": "Hello world"},
				},
			},
		},
	})
	defer resp.Body.Close()

	// Then it succeeds with a MessageId
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result map[string]any
	decodeJSON(t, resp, &result)
	if result["MessageId"] == "" {
		t.Error("expected MessageId to be set")
	}
}

func TestSESV2_SendEmail_missingContent(t *testing.T) {
	// Given a server
	srv := helpers.NewTestServer(t)

	// When I send without Content
	resp := v2Call(t, srv, http.MethodPost, "/v2/email/outbound-emails", map[string]any{
		"FromEmailAddress": "sender@example.com",
	})
	defer resp.Body.Close()

	// Then 400
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

// ─── Template tests ──────────────────────────────────────────────────────────

func TestCreateTemplate_success(t *testing.T) {
	// Given: a fresh server
	srv := helpers.NewTestServer(t)

	// When: CreateTemplate is called
	resp := sesCall(t, srv, "CreateTemplate", url.Values{
		"Template.TemplateName": {"my-template"},
		"Template.SubjectPart":  {"Hello {{name}}"},
		"Template.TextPart":     {"Hi {{name}}"},
		"Template.HtmlPart":     {"<p>Hi {{name}}</p>"},
	})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestCreateTemplate_duplicate(t *testing.T) {
	// Given: a template already exists
	srv := helpers.NewTestServer(t)
	r := sesCall(t, srv, "CreateTemplate", url.Values{
		"Template.TemplateName": {"dup-template"},
		"Template.SubjectPart":  {"Subject"},
	})
	r.Body.Close()

	// When: CreateTemplate is called again with the same name
	resp := sesCall(t, srv, "CreateTemplate", url.Values{
		"Template.TemplateName": {"dup-template"},
		"Template.SubjectPart":  {"Subject2"},
	})
	defer resp.Body.Close()

	// Then: 400 AlreadyExists error
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestGetTemplate_success(t *testing.T) {
	// Given: a template exists
	srv := helpers.NewTestServer(t)
	r := sesCall(t, srv, "CreateTemplate", url.Values{
		"Template.TemplateName": {"get-template"},
		"Template.SubjectPart":  {"Hello {{name}}"},
		"Template.HtmlPart":     {"<p>Hello</p>"},
	})
	r.Body.Close()

	// When: GetTemplate is called
	resp := sesCall(t, srv, "GetTemplate", url.Values{
		"TemplateName": {"get-template"},
	})
	defer resp.Body.Close()

	// Then: 200 with TemplateName in body
	helpers.AssertStatus(t, resp, http.StatusOK)
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "get-template") {
		t.Errorf("expected TemplateName in response, got: %s", body)
	}
}

func TestGetTemplate_notFound(t *testing.T) {
	// Given: no templates
	srv := helpers.NewTestServer(t)

	// When: GetTemplate is called for a non-existent template
	resp := sesCall(t, srv, "GetTemplate", url.Values{
		"TemplateName": {"no-such-template"},
	})
	defer resp.Body.Close()

	// Then: 400 TemplateDoesNotExist
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestUpdateTemplate_success(t *testing.T) {
	// Given: a template exists
	srv := helpers.NewTestServer(t)
	r := sesCall(t, srv, "CreateTemplate", url.Values{
		"Template.TemplateName": {"update-template"},
		"Template.SubjectPart":  {"Old subject"},
	})
	r.Body.Close()

	// When: UpdateTemplate is called with a new subject
	resp := sesCall(t, srv, "UpdateTemplate", url.Values{
		"Template.TemplateName": {"update-template"},
		"Template.SubjectPart":  {"New subject"},
	})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: GetTemplate reflects the new subject
	getResp := sesCall(t, srv, "GetTemplate", url.Values{"TemplateName": {"update-template"}})
	defer getResp.Body.Close()
	body, _ := io.ReadAll(getResp.Body)
	if !strings.Contains(string(body), "New subject") {
		t.Errorf("expected updated subject in get response, got: %s", body)
	}
}

func TestListTemplates_success(t *testing.T) {
	// Given: two templates exist
	srv := helpers.NewTestServer(t)
	for _, name := range []string{"list-tmpl-1", "list-tmpl-2"} {
		r := sesCall(t, srv, "CreateTemplate", url.Values{
			"Template.TemplateName": {name},
			"Template.SubjectPart":  {"Subject"},
		})
		r.Body.Close()
	}

	// When: ListTemplates is called
	resp := sesCall(t, srv, "ListTemplates", url.Values{})
	defer resp.Body.Close()

	// Then: both templates appear in the response
	helpers.AssertStatus(t, resp, http.StatusOK)
	body, _ := io.ReadAll(resp.Body)
	bs := string(body)
	for _, name := range []string{"list-tmpl-1", "list-tmpl-2"} {
		if !strings.Contains(bs, name) {
			t.Errorf("expected %q in ListTemplates response, got: %s", name, bs)
		}
	}
}

func TestDeleteTemplate_success(t *testing.T) {
	// Given: a template exists
	srv := helpers.NewTestServer(t)
	r := sesCall(t, srv, "CreateTemplate", url.Values{
		"Template.TemplateName": {"del-template"},
		"Template.SubjectPart":  {"Subject"},
	})
	r.Body.Close()

	// When: DeleteTemplate is called
	resp := sesCall(t, srv, "DeleteTemplate", url.Values{
		"TemplateName": {"del-template"},
	})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: GetTemplate returns not found
	getResp := sesCall(t, srv, "GetTemplate", url.Values{"TemplateName": {"del-template"}})
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusBadRequest)
}

func TestSendTemplatedEmail_success(t *testing.T) {
	// Given: a template and verified identity exist
	srv := helpers.NewTestServer(t)
	c := sesCall(t, srv, "CreateTemplate", url.Values{
		"Template.TemplateName": {"send-template"},
		"Template.SubjectPart":  {"Hello {{name}}"},
		"Template.TextPart":     {"Hi {{name}}"},
		"Template.HtmlPart":     {"<p>Hi {{name}}</p>"},
	})
	c.Body.Close()
	v := sesCall(t, srv, "VerifyEmailIdentity", url.Values{"EmailAddress": {"sender@example.com"}})
	v.Body.Close()

	// When: SendTemplatedEmail is called
	resp := sesCall(t, srv, "SendTemplatedEmail", url.Values{
		"Source":                           {"sender@example.com"},
		"Destination.ToAddresses.member.1": {"recipient@example.com"},
		"Template":                         {"send-template"},
		"TemplateData":                     {`{"name":"World"}`},
	})
	defer resp.Body.Close()

	// Then: 200 with a MessageId
	helpers.AssertStatus(t, resp, http.StatusOK)
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "MessageId") {
		t.Errorf("expected MessageId in response, got: %s", body)
	}
}
