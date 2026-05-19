// Package route53_test contains integration tests for the Route 53 emulator.
//
// Run: go test ./tests/integration/route53/...
package route53_test

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

func r53Call(t *testing.T, srv *helpers.TestServer, method, path string, body string) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = bytes.NewBufferString(body)
	}
	req, err := http.NewRequest(method, srv.URL+path, bodyReader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/xml")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("r53Call %s %s: %v", method, path, err)
	}
	return resp
}

func decodeXML(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	defer resp.Body.Close()
	if err := xml.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode XML: %v", err)
	}
}

func createZone(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	body := `<CreateHostedZoneRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">` +
		`<Name>` + name + `</Name>` +
		`<CallerReference>ref-` + name + `</CallerReference>` +
		`</CreateHostedZoneRequest>`
	resp := r53Call(t, srv, http.MethodPost, "/2013-04-01/hostedzone", body)
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var out struct {
		HostedZone struct {
			Id   string `xml:"Id"`
			Name string `xml:"Name"`
		} `xml:"HostedZone"`
	}
	decodeXML(t, resp, &out)
	if out.HostedZone.Id == "" {
		t.Fatal("expected HostedZone.Id to be set")
	}
	return out.HostedZone.Id
}

// ── CreateHostedZone ──────────────────────────────────────────────────────────

func TestCreateHostedZone_success(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t, helpers.WithServices("route53"))

	// When: CreateHostedZone is called
	zoneID := createZone(t, srv, "example.com.")

	// Then: a valid zone ID is returned
	if !strings.HasPrefix(zoneID, "/hostedzone/") {
		t.Errorf("expected zone ID to start with /hostedzone/, got %q", zoneID)
	}
}

func TestCreateHostedZone_missingName(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t, helpers.WithServices("route53"))

	// When: CreateHostedZone is called without a Name
	body := `<CreateHostedZoneRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">` +
		`<CallerReference>ref-x</CallerReference>` +
		`</CreateHostedZoneRequest>`
	resp := r53Call(t, srv, http.MethodPost, "/2013-04-01/hostedzone", body)
	defer resp.Body.Close()

	// Then: 400 is returned
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

// ── ListHostedZones ───────────────────────────────────────────────────────────

func TestListHostedZones_empty(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t, helpers.WithServices("route53"))

	// When: ListHostedZones is called
	resp := r53Call(t, srv, http.MethodGet, "/2013-04-01/hostedzone", "")
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		HostedZones []struct {
			Id   string `xml:"Id"`
			Name string `xml:"Name"`
		} `xml:"HostedZones>HostedZone"`
	}
	decodeXML(t, resp, &out)

	// Then: no zones returned
	if len(out.HostedZones) != 0 {
		t.Errorf("expected 0 zones, got %d", len(out.HostedZones))
	}
}

func TestListHostedZones_afterCreate(t *testing.T) {
	// Given: two hosted zones exist
	srv := helpers.NewTestServer(t, helpers.WithServices("route53"))
	createZone(t, srv, "alpha.com.")
	createZone(t, srv, "beta.com.")

	// When: ListHostedZones is called
	resp := r53Call(t, srv, http.MethodGet, "/2013-04-01/hostedzone", "")
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		HostedZones []struct {
			Id   string `xml:"Id"`
			Name string `xml:"Name"`
		} `xml:"HostedZones>HostedZone"`
	}
	decodeXML(t, resp, &out)

	// Then: 2 zones are returned
	if len(out.HostedZones) != 2 {
		t.Errorf("expected 2 zones, got %d", len(out.HostedZones))
	}
}

// ── GetHostedZone ─────────────────────────────────────────────────────────────

func TestGetHostedZone_success(t *testing.T) {
	// Given: a hosted zone exists
	srv := helpers.NewTestServer(t, helpers.WithServices("route53"))
	zoneID := createZone(t, srv, "example.com.")
	// zoneID is like /hostedzone/Z123…; strip to get the bare ID
	bareID := strings.TrimPrefix(zoneID, "/hostedzone/")

	// When: GetHostedZone is called
	resp := r53Call(t, srv, http.MethodGet, "/2013-04-01/hostedzone/"+bareID, "")
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		HostedZone struct {
			Name string `xml:"Name"`
		} `xml:"HostedZone"`
	}
	decodeXML(t, resp, &out)

	// Then: the zone name matches
	if out.HostedZone.Name != "example.com." {
		t.Errorf("expected Name=example.com., got %q", out.HostedZone.Name)
	}
}

func TestGetHostedZone_notFound(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t, helpers.WithServices("route53"))

	// When: GetHostedZone is called with a non-existent ID
	resp := r53Call(t, srv, http.MethodGet, "/2013-04-01/hostedzone/ZNOTEXIST", "")
	defer resp.Body.Close()

	// Then: 404 is returned
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

// ── DeleteHostedZone ──────────────────────────────────────────────────────────

func TestDeleteHostedZone_success(t *testing.T) {
	// Given: a hosted zone exists
	srv := helpers.NewTestServer(t, helpers.WithServices("route53"))
	zoneID := createZone(t, srv, "example.com.")
	bareID := strings.TrimPrefix(zoneID, "/hostedzone/")

	// When: DeleteHostedZone is called
	resp := r53Call(t, srv, http.MethodDelete, "/2013-04-01/hostedzone/"+bareID, "")
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: zone is gone
	getResp := r53Call(t, srv, http.MethodGet, "/2013-04-01/hostedzone/"+bareID, "")
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusNotFound)
}

// ── ChangeResourceRecordSets ─────────────────────────────────────────────────

func TestChangeResourceRecordSets_upsert(t *testing.T) {
	// Given: a hosted zone exists
	srv := helpers.NewTestServer(t, helpers.WithServices("route53"))
	zoneID := createZone(t, srv, "example.com.")
	bareID := strings.TrimPrefix(zoneID, "/hostedzone/")

	// When: ChangeResourceRecordSets is called with an UPSERT
	body := `<ChangeResourceRecordSetsRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">` +
		`<ChangeBatch>` +
		`<Changes>` +
		`<Change>` +
		`<Action>UPSERT</Action>` +
		`<ResourceRecordSet>` +
		`<Name>www.example.com.</Name>` +
		`<Type>A</Type>` +
		`<TTL>300</TTL>` +
		`<ResourceRecords>` +
		`<ResourceRecord><Value>1.2.3.4</Value></ResourceRecord>` +
		`</ResourceRecords>` +
		`</ResourceRecordSet>` +
		`</Change>` +
		`</Changes>` +
		`</ChangeBatch>` +
		`</ChangeResourceRecordSetsRequest>`
	resp := r53Call(t, srv, http.MethodPost, "/2013-04-01/hostedzone/"+bareID+"/rrset", body)
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		ChangeInfo struct {
			Id     string `xml:"Id"`
			Status string `xml:"Status"`
		} `xml:"ChangeInfo"`
	}
	decodeXML(t, resp, &out)

	// Then: ChangeInfo with Status=INSYNC is returned
	if out.ChangeInfo.Status != "INSYNC" {
		t.Errorf("expected Status=INSYNC, got %q", out.ChangeInfo.Status)
	}
	if !strings.HasPrefix(out.ChangeInfo.Id, "/change/") {
		t.Errorf("expected ChangeInfo.Id to start with /change/, got %q", out.ChangeInfo.Id)
	}
}

// ── ListResourceRecordSets ───────────────────────────────────────────────────

func TestListResourceRecordSets_afterChange(t *testing.T) {
	// Given: a hosted zone exists with one record
	srv := helpers.NewTestServer(t, helpers.WithServices("route53"))
	zoneID := createZone(t, srv, "example.com.")
	bareID := strings.TrimPrefix(zoneID, "/hostedzone/")

	body := `<ChangeResourceRecordSetsRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">` +
		`<ChangeBatch><Changes><Change>` +
		`<Action>UPSERT</Action>` +
		`<ResourceRecordSet>` +
		`<Name>www.example.com.</Name><Type>A</Type><TTL>300</TTL>` +
		`<ResourceRecords><ResourceRecord><Value>1.2.3.4</Value></ResourceRecord></ResourceRecords>` +
		`</ResourceRecordSet>` +
		`</Change></Changes></ChangeBatch>` +
		`</ChangeResourceRecordSetsRequest>`
	changeResp := r53Call(t, srv, http.MethodPost, "/2013-04-01/hostedzone/"+bareID+"/rrset", body)
	defer changeResp.Body.Close()
	io.Copy(io.Discard, changeResp.Body)

	// When: ListResourceRecordSets is called
	resp := r53Call(t, srv, http.MethodGet, "/2013-04-01/hostedzone/"+bareID+"/rrset", "")
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		ResourceRecordSets []struct {
			Name string `xml:"Name"`
			Type string `xml:"Type"`
		} `xml:"ResourceRecordSets>ResourceRecordSet"`
	}
	decodeXML(t, resp, &out)

	// Then: one record is returned
	if len(out.ResourceRecordSets) != 1 {
		t.Errorf("expected 1 record set, got %d", len(out.ResourceRecordSets))
	}
	if out.ResourceRecordSets[0].Name != "www.example.com." {
		t.Errorf("expected Name=www.example.com., got %q", out.ResourceRecordSets[0].Name)
	}
}

// ── GetChange ────────────────────────────────────────────────────────────────

func TestGetChange_insync(t *testing.T) {
	// Given: a change was submitted
	srv := helpers.NewTestServer(t, helpers.WithServices("route53"))
	zoneID := createZone(t, srv, "example.com.")
	bareID := strings.TrimPrefix(zoneID, "/hostedzone/")

	body := `<ChangeResourceRecordSetsRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">` +
		`<ChangeBatch><Changes><Change>` +
		`<Action>UPSERT</Action>` +
		`<ResourceRecordSet>` +
		`<Name>test.example.com.</Name><Type>A</Type><TTL>60</TTL>` +
		`<ResourceRecords><ResourceRecord><Value>9.9.9.9</Value></ResourceRecord></ResourceRecords>` +
		`</ResourceRecordSet>` +
		`</Change></Changes></ChangeBatch>` +
		`</ChangeResourceRecordSetsRequest>`
	changeResp := r53Call(t, srv, http.MethodPost, "/2013-04-01/hostedzone/"+bareID+"/rrset", body)
	var changeOut struct {
		ChangeInfo struct {
			Id string `xml:"Id"`
		} `xml:"ChangeInfo"`
	}
	decodeXML(t, changeResp, &changeOut)
	changeID := strings.TrimPrefix(changeOut.ChangeInfo.Id, "/change/")

	// When: GetChange is called
	resp := r53Call(t, srv, http.MethodGet, "/2013-04-01/change/"+changeID, "")
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		ChangeInfo struct {
			Status string `xml:"Status"`
		} `xml:"ChangeInfo"`
	}
	decodeXML(t, resp, &out)

	// Then: Status is INSYNC
	if out.ChangeInfo.Status != "INSYNC" {
		t.Errorf("expected INSYNC, got %q", out.ChangeInfo.Status)
	}
}
