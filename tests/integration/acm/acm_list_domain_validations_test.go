// Package acm_test contains integration tests for the ACM emulator.
package acm_test

import (
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// ─── ListCertificateDomainValidations ──────────────────────────────────────

// domainValidationSummary mirrors the wire shape of one entry in
// DomainValidationSummaryList: DomainName plus its ActiveValidationConfiguration
// (ValidationMethod/ValidationStatus/ValidationChallenge).
type domainValidationSummary struct {
	DomainName                    string `json:"DomainName"`
	ActiveValidationConfiguration *struct {
		ValidationMethod string `json:"ValidationMethod"`
		ValidationStatus string `json:"ValidationStatus"`
	} `json:"ActiveValidationConfiguration"`
	RequestedValidationConfiguration *struct {
		ValidationMethod string `json:"ValidationMethod"`
		ValidationStatus string `json:"ValidationStatus"`
	} `json:"RequestedValidationConfiguration"`
}

type listCertificateDomainValidationsResult struct {
	DomainValidationSummaryList []domainValidationSummary `json:"DomainValidationSummaryList"`
	NextToken                   string                    `json:"NextToken"`
}

func TestListCertificateDomainValidations_withSANs(t *testing.T) {
	// Given: a certificate requested with a base domain and two SANs
	srv := helpers.NewTestServer(t)
	resp := acmCall(t, srv, "RequestCertificate", map[string]any{
		"DomainName":              "example.com",
		"SubjectAlternativeNames": []string{"example.com", "www.example.com", "api.example.com"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var reqResult struct {
		CertificateArn string `json:"CertificateArn"`
	}
	helpers.DecodeJSON(t, resp, &reqResult)

	// When: ListCertificateDomainValidations is called
	listResp := acmCall(t, srv, "ListCertificateDomainValidations", map[string]any{
		"CertificateArn": reqResult.CertificateArn,
	})
	defer listResp.Body.Close()

	// Then: one summary per domain (base + each SAN), each reporting success
	helpers.AssertStatus(t, listResp, http.StatusOK)
	helpers.AssertRequestID(t, listResp)
	var result listCertificateDomainValidationsResult
	helpers.DecodeJSON(t, listResp, &result)

	if len(result.DomainValidationSummaryList) != 3 {
		t.Fatalf("expected 3 domain validation summaries, got %d: %+v", len(result.DomainValidationSummaryList), result.DomainValidationSummaryList)
	}
	wantDomains := map[string]bool{"example.com": false, "www.example.com": false, "api.example.com": false}
	for _, sum := range result.DomainValidationSummaryList {
		if _, ok := wantDomains[sum.DomainName]; !ok {
			t.Errorf("unexpected domain %q in summary list", sum.DomainName)
			continue
		}
		wantDomains[sum.DomainName] = true
		if sum.ActiveValidationConfiguration == nil {
			t.Fatalf("domain %q: expected ActiveValidationConfiguration to be set", sum.DomainName)
		}
		if sum.ActiveValidationConfiguration.ValidationStatus != "SUCCESS" {
			t.Errorf("domain %q: expected ValidationStatus=SUCCESS, got %q", sum.DomainName, sum.ActiveValidationConfiguration.ValidationStatus)
		}
		if sum.RequestedValidationConfiguration != nil {
			t.Errorf("domain %q: expected no RequestedValidationConfiguration (no migration in progress), got %+v", sum.DomainName, sum.RequestedValidationConfiguration)
		}
	}
	for d, seen := range wantDomains {
		if !seen {
			t.Errorf("expected domain %q in summary list, not found", d)
		}
	}
}

func TestListCertificateDomainValidations_noSANs(t *testing.T) {
	// Given: a certificate requested with only a base domain name
	srv := helpers.NewTestServer(t)
	arn := requestCert(t, srv, "example.com")

	// When: ListCertificateDomainValidations is called
	resp := acmCall(t, srv, "ListCertificateDomainValidations", map[string]any{
		"CertificateArn": arn,
	})
	defer resp.Body.Close()

	// Then: exactly one summary, for the base domain, reporting success
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result listCertificateDomainValidationsResult
	helpers.DecodeJSON(t, resp, &result)

	if len(result.DomainValidationSummaryList) != 1 {
		t.Fatalf("expected exactly 1 domain validation summary, got %d: %+v", len(result.DomainValidationSummaryList), result.DomainValidationSummaryList)
	}
	sum := result.DomainValidationSummaryList[0]
	if sum.DomainName != "example.com" {
		t.Errorf("expected DomainName=example.com, got %q", sum.DomainName)
	}
	if sum.ActiveValidationConfiguration == nil || sum.ActiveValidationConfiguration.ValidationStatus != "SUCCESS" {
		t.Errorf("expected ActiveValidationConfiguration.ValidationStatus=SUCCESS, got %+v", sum.ActiveValidationConfiguration)
	}
}

func TestListCertificateDomainValidations_unknownArn(t *testing.T) {
	// Given: an empty store — no certificate with this ARN exists
	srv := helpers.NewTestServer(t)
	missing := "arn:aws:acm:us-east-1:000000000000:certificate/00000000-0000-0000-0000-000000000000"

	// When: ListCertificateDomainValidations is called with an unknown ARN
	resp := acmCall(t, srv, "ListCertificateDomainValidations", map[string]any{
		"CertificateArn": missing,
	})
	defer resp.Body.Close()

	// Then: the same ResourceNotFoundException requireCert produces elsewhere in ACM
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
	helpers.AssertRequestID(t, resp)
}

func TestListCertificateDomainValidations_pagination(t *testing.T) {
	// Given: a certificate with three domains (base + 2 SANs)
	srv := helpers.NewTestServer(t)
	reqResp := acmCall(t, srv, "RequestCertificate", map[string]any{
		"DomainName":              "example.com",
		"SubjectAlternativeNames": []string{"example.com", "www.example.com", "api.example.com"},
	})
	defer reqResp.Body.Close()
	var reqResult struct {
		CertificateArn string `json:"CertificateArn"`
	}
	helpers.DecodeJSON(t, reqResp, &reqResult)

	// When: the first page is requested with MaxItems=2
	page1Resp := acmCall(t, srv, "ListCertificateDomainValidations", map[string]any{
		"CertificateArn": reqResult.CertificateArn,
		"MaxItems":       2,
	})
	defer page1Resp.Body.Close()
	helpers.AssertStatus(t, page1Resp, http.StatusOK)
	var page1 listCertificateDomainValidationsResult
	helpers.DecodeJSON(t, page1Resp, &page1)

	// Then: exactly 2 summaries come back with a NextToken for the rest
	if len(page1.DomainValidationSummaryList) != 2 {
		t.Fatalf("expected 2 summaries on page 1, got %d", len(page1.DomainValidationSummaryList))
	}
	if page1.NextToken == "" {
		t.Fatal("expected a NextToken on a truncated page")
	}

	// When: the second page is fetched using that token
	page2Resp := acmCall(t, srv, "ListCertificateDomainValidations", map[string]any{
		"CertificateArn": reqResult.CertificateArn,
		"MaxItems":       2,
		"NextToken":      page1.NextToken,
	})
	defer page2Resp.Body.Close()
	helpers.AssertStatus(t, page2Resp, http.StatusOK)
	var page2 listCertificateDomainValidationsResult
	helpers.DecodeJSON(t, page2Resp, &page2)

	// Then: the remaining 1 summary comes back with no further NextToken
	if len(page2.DomainValidationSummaryList) != 1 {
		t.Fatalf("expected 1 summary on page 2, got %d", len(page2.DomainValidationSummaryList))
	}
	if page2.NextToken != "" {
		t.Errorf("expected no NextToken on the last page, got %q", page2.NextToken)
	}
}

func TestListCertificateDomainValidations_invalidNextToken(t *testing.T) {
	// Given: a certificate exists
	srv := helpers.NewTestServer(t)
	arn := requestCert(t, srv, "example.com")

	// When: ListCertificateDomainValidations is called with a garbage token
	resp := acmCall(t, srv, "ListCertificateDomainValidations", map[string]any{
		"CertificateArn": arn,
		"NextToken":      "not-a-real-token",
	})
	defer resp.Body.Close()

	// Then: the request is rejected rather than silently restarting from page 1
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidArgsException")
}
