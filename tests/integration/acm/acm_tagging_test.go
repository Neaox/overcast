// Package acm_test — certificate tagging.
//
// ACM has two spellings for the same tag set: the original
// AddTagsToCertificate / RemoveTagsFromCertificate / ListTagsForCertificate,
// and the modern TagResource / UntagResource / ListTagsForResource aliases.
// RequestCertificate also carries inline Tags.
//
// Run: go test ./tests/integration/acm/...
package acm_test

import (
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// requestCertificate issues a certificate, optionally with inline tags, and
// returns its ARN.
func requestCertificate(t *testing.T, srv *helpers.TestServer, domain string, tags []map[string]string) string {
	t.Helper()
	body := map[string]any{"DomainName": domain}
	if tags != nil {
		body["Tags"] = tags
	}
	resp := acmCall(t, srv, "RequestCertificate", body)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		CertificateArn string `json:"CertificateArn"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.CertificateArn == "" {
		t.Fatal("expected CertificateArn to be set")
	}
	return result.CertificateArn
}

// acmTagMap reads a certificate's tags back through ListTagsForResource.
func acmTagMap(t *testing.T, srv *helpers.TestServer, arn string) map[string]string {
	t.Helper()
	resp := acmCall(t, srv, "ListTagsForResource", map[string]any{"ResourceArn": arn})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Tags []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"Tags"`
	}
	helpers.DecodeJSON(t, resp, &out)
	got := make(map[string]string, len(out.Tags))
	for _, tag := range out.Tags {
		got[tag.Key] = tag.Value
	}
	return got
}

func TestACMTagResource_roundTrips(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := requestCertificate(t, srv, "tagged.example.com", nil)

	resp := acmCall(t, srv, "TagResource", map[string]any{
		"ResourceArn": arn,
		"Tags":        []map[string]string{{"Key": "env", "Value": "prod"}},
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	if got := acmTagMap(t, srv, arn); got["env"] != "prod" {
		t.Fatalf("TagResource did not round-trip: got %v", got)
	}
}

// The two spellings address one tag set, not two.
func TestACMTagResource_sharesTagsWithTheCertificateSpelling(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := requestCertificate(t, srv, "shared.example.com", nil)

	add := acmCall(t, srv, "AddTagsToCertificate", map[string]any{
		"CertificateArn": arn,
		"Tags":           []map[string]string{{"Key": "viaCertificate", "Value": "yes"}},
	})
	helpers.AssertStatus(t, add, http.StatusOK)
	add.Body.Close()

	tag := acmCall(t, srv, "TagResource", map[string]any{
		"ResourceArn": arn,
		"Tags":        []map[string]string{{"Key": "viaResource", "Value": "yes"}},
	})
	helpers.AssertStatus(t, tag, http.StatusOK)
	tag.Body.Close()

	got := acmTagMap(t, srv, arn)
	if got["viaCertificate"] != "yes" || got["viaResource"] != "yes" {
		t.Fatalf("the two spellings do not share a tag set: got %v", got)
	}
}

// UntagResource takes TagKeys, unlike RemoveTagsFromCertificate's Tags list.
func TestACMUntagResource_removesNamedKeysOnly(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := requestCertificate(t, srv, "untagged.example.com", []map[string]string{
		{"Key": "env", "Value": "prod"},
		{"Key": "keep", "Value": "yes"},
	})

	resp := acmCall(t, srv, "UntagResource", map[string]any{
		"ResourceArn": arn,
		"TagKeys":     []string{"env"},
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	got := acmTagMap(t, srv, arn)
	if _, still := got["env"]; still {
		t.Errorf("UntagResource left env in place: %v", got)
	}
	if got["keep"] != "yes" {
		t.Errorf("UntagResource removed an unrelated tag: %v", got)
	}
}

func TestACMRequestCertificate_appliesTagsAtCreation(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := requestCertificate(t, srv, "tagatcreate.example.com", []map[string]string{
		{"Key": "env", "Value": "staging"},
	})
	if got := acmTagMap(t, srv, arn); got["env"] != "staging" {
		t.Errorf("RequestCertificate tags not applied at creation: got %v", got)
	}
}

// Tagging a certificate that does not exist is an error, not a silent write
// into a namespace no DeleteCertificate will ever clean up.
func TestACMTagging_unknownCertificate(t *testing.T) {
	srv := helpers.NewTestServer(t)
	missing := "arn:aws:acm:us-east-1:000000000000:certificate/00000000-0000-0000-0000-000000000000"

	for _, tc := range []struct {
		op   string
		body map[string]any
	}{
		{"TagResource", map[string]any{"ResourceArn": missing, "Tags": []map[string]string{{"Key": "env", "Value": "prod"}}}},
		{"UntagResource", map[string]any{"ResourceArn": missing, "TagKeys": []string{"env"}}},
		{"ListTagsForResource", map[string]any{"ResourceArn": missing}},
		{"AddTagsToCertificate", map[string]any{"CertificateArn": missing, "Tags": []map[string]string{{"Key": "env", "Value": "prod"}}}},
		{"RemoveTagsFromCertificate", map[string]any{"CertificateArn": missing, "Tags": []map[string]string{{"Key": "env"}}}},
		{"ListTagsForCertificate", map[string]any{"CertificateArn": missing}},
	} {
		resp := acmCall(t, srv, tc.op, tc.body)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s on a missing certificate: got %d want 404", tc.op, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

// Deleting the certificate takes its tags with it.
func TestACMDeleteCertificate_dropsItsTags(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := requestCertificate(t, srv, "recycled.example.com", []map[string]string{
		{"Key": "env", "Value": "prod"},
	})
	if got := acmTagMap(t, srv, arn); got["env"] != "prod" {
		t.Fatalf("setup: expected env=prod, got %v", got)
	}

	del := acmCall(t, srv, "DeleteCertificate", map[string]any{"CertificateArn": arn})
	helpers.AssertStatus(t, del, http.StatusOK)
	del.Body.Close()

	resp := acmCall(t, srv, "ListTagsForResource", map[string]any{"ResourceArn": arn})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("ListTagsForResource after delete: got %d want 404", resp.StatusCode)
	}
}
