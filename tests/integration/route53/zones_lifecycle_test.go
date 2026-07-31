// Hosted-zone lifecycle tests for the Route 53 emulator at inert level:
// creation side resources (NS/SOA, delegation set), AWS-faithful validation
// and error codes, pagination, and comment updates.
package route53_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// ── Shared helpers for the inert-level suites ────────────────────────────────

// createZoneRef creates a hosted zone with an explicit caller reference and
// returns the full zone ID (/hostedzone/Z...).
func createZoneRef(t *testing.T, srv *helpers.TestServer, name, callerRef string) string {
	t.Helper()
	body := `<CreateHostedZoneRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">` +
		`<Name>` + name + `</Name>` +
		`<CallerReference>` + callerRef + `</CallerReference>` +
		`</CreateHostedZoneRequest>`
	resp := r53Call(t, srv, http.MethodPost, "/2013-04-01/hostedzone", body)
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var out struct {
		HostedZone struct {
			Id string `xml:"Id"`
		} `xml:"HostedZone"`
	}
	decodeXML(t, resp, &out)
	return out.HostedZone.Id
}

// errMessage decodes a Route 53 ErrorResponse envelope and returns code and message.
func errMessage(t *testing.T, resp *http.Response) (code, message string) {
	t.Helper()
	defer resp.Body.Close()
	var errResp struct {
		Error struct {
			Code    string `xml:"Code"`
			Message string `xml:"Message"`
		} `xml:"Error"`
	}
	helpers.DecodeXML(t, resp, &errResp)
	return errResp.Error.Code, errResp.Error.Message
}

// upsertRecord upserts a simple record and fails the test on a non-200.
func upsertRecord(t *testing.T, srv *helpers.TestServer, bareZoneID, name, rrType, value string) {
	t.Helper()
	resp := changeRRSets(t, srv, bareZoneID,
		`<Change><Action>UPSERT</Action><ResourceRecordSet>`+
			`<Name>`+name+`</Name><Type>`+rrType+`</Type><TTL>300</TTL>`+
			`<ResourceRecords><ResourceRecord><Value>`+value+`</Value></ResourceRecord></ResourceRecords>`+
			`</ResourceRecordSet></Change>`)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
}

// changeRRSets posts a ChangeResourceRecordSets batch built from raw <Change> elements.
func changeRRSets(t *testing.T, srv *helpers.TestServer, bareZoneID, changes string) *http.Response {
	t.Helper()
	body := `<ChangeResourceRecordSetsRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">` +
		`<ChangeBatch><Changes>` + changes + `</Changes></ChangeBatch>` +
		`</ChangeResourceRecordSetsRequest>`
	return r53Call(t, srv, http.MethodPost, "/2013-04-01/hostedzone/"+bareZoneID+"/rrset", body)
}

type listedRRSet struct {
	Name            string   `xml:"Name"`
	Type            string   `xml:"Type"`
	TTL             int64    `xml:"TTL"`
	SetIdentifier   string   `xml:"SetIdentifier"`
	Weight          int64    `xml:"Weight"`
	ResourceRecords []string `xml:"ResourceRecords>ResourceRecord>Value"`
}

type listRRSetsOut struct {
	ResourceRecordSets   []listedRRSet `xml:"ResourceRecordSets>ResourceRecordSet"`
	IsTruncated          bool          `xml:"IsTruncated"`
	NextRecordName       string        `xml:"NextRecordName"`
	NextRecordType       string        `xml:"NextRecordType"`
	NextRecordIdentifier string        `xml:"NextRecordIdentifier"`
	MaxItems             string        `xml:"MaxItems"`
}

func listRRSets(t *testing.T, srv *helpers.TestServer, bareZoneID, query string) listRRSetsOut {
	t.Helper()
	resp := r53Call(t, srv, http.MethodGet, "/2013-04-01/hostedzone/"+bareZoneID+"/rrset"+query, "")
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out listRRSetsOut
	decodeXML(t, resp, &out)
	return out
}

// ── CreateHostedZone: validation ─────────────────────────────────────────────

func TestCreateHostedZone_missingCallerReference(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: CreateHostedZone is called without a CallerReference
	body := `<CreateHostedZoneRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">` +
		`<Name>example.com.</Name>` +
		`</CreateHostedZoneRequest>`
	resp := r53Call(t, srv, http.MethodPost, "/2013-04-01/hostedzone", body)

	// Then: InvalidInput in the Route 53 ErrorResponse envelope
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertQueryXMLError(t, resp, "InvalidInput")
}

func TestCreateHostedZone_duplicateCallerReference(t *testing.T) {
	// Given: a zone created with a specific caller reference
	srv := helpers.NewTestServer(t)
	createZoneRef(t, srv, "example.com.", "ref-dup")

	// When: a second CreateHostedZone reuses the caller reference
	body := `<CreateHostedZoneRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">` +
		`<Name>other.com.</Name>` +
		`<CallerReference>ref-dup</CallerReference>` +
		`</CreateHostedZoneRequest>`
	resp := r53Call(t, srv, http.MethodPost, "/2013-04-01/hostedzone", body)

	// Then: HostedZoneAlreadyExists with HTTP 409, as on real AWS
	helpers.AssertStatus(t, resp, http.StatusConflict)
	helpers.AssertQueryXMLError(t, resp, "HostedZoneAlreadyExists")
}

func TestCreateHostedZone_invalidDomainName(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: CreateHostedZone is called with an invalid domain name
	body := `<CreateHostedZoneRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">` +
		`<Name>exa mple..com</Name>` +
		`<CallerReference>ref-bad-name</CallerReference>` +
		`</CreateHostedZoneRequest>`
	resp := r53Call(t, srv, http.MethodPost, "/2013-04-01/hostedzone", body)

	// Then: InvalidDomainName is returned
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertQueryXMLError(t, resp, "InvalidDomainName")
}

func TestCreateHostedZone_normalisesName(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: CreateHostedZone is called with a mixed-case name without trailing dot
	body := `<CreateHostedZoneRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">` +
		`<Name>MyZone.Example.COM</Name>` +
		`<CallerReference>ref-case</CallerReference>` +
		`</CreateHostedZoneRequest>`
	resp := r53Call(t, srv, http.MethodPost, "/2013-04-01/hostedzone", body)
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var out struct {
		HostedZone struct {
			Name string `xml:"Name"`
		} `xml:"HostedZone"`
	}
	decodeXML(t, resp, &out)

	// Then: the name comes back lowercased with a trailing dot, as on real AWS
	if out.HostedZone.Name != "myzone.example.com." {
		t.Errorf("expected normalised name myzone.example.com., got %q", out.HostedZone.Name)
	}
}

// ── CreateHostedZone: side resources ─────────────────────────────────────────

func TestCreateHostedZone_returnsDelegationSetAndLocation(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: CreateHostedZone succeeds
	body := `<CreateHostedZoneRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">` +
		`<Name>example.com.</Name>` +
		`<CallerReference>ref-ds</CallerReference>` +
		`</CreateHostedZoneRequest>`
	resp := r53Call(t, srv, http.MethodPost, "/2013-04-01/hostedzone", body)
	helpers.AssertStatus(t, resp, http.StatusCreated)

	// Then: the Location HTTP header points at the new zone
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "/2013-04-01/hostedzone/") {
		t.Errorf("expected Location header with /2013-04-01/hostedzone/, got %q", loc)
	}

	// And: the response carries a delegation set with four name servers
	var out struct {
		HostedZone struct {
			Id                     string `xml:"Id"`
			ResourceRecordSetCount int    `xml:"ResourceRecordSetCount"`
		} `xml:"HostedZone"`
		DelegationSet struct {
			NameServers []string `xml:"NameServers>NameServer"`
		} `xml:"DelegationSet"`
	}
	decodeXML(t, resp, &out)
	if len(out.DelegationSet.NameServers) != 4 {
		t.Fatalf("expected 4 name servers, got %d (%v)", len(out.DelegationSet.NameServers), out.DelegationSet.NameServers)
	}
	for _, ns := range out.DelegationSet.NameServers {
		if !strings.HasPrefix(ns, "ns-") || !strings.Contains(ns, ".awsdns-") {
			t.Errorf("name server %q does not look like an AWS name server", ns)
		}
	}
	// And: the new zone already counts its default NS + SOA records
	if out.HostedZone.ResourceRecordSetCount != 2 {
		t.Errorf("expected ResourceRecordSetCount=2 (NS+SOA), got %d", out.HostedZone.ResourceRecordSetCount)
	}
}

func TestCreateHostedZone_createsDefaultNSAndSOA(t *testing.T) {
	// Given: a freshly created hosted zone
	srv := helpers.NewTestServer(t)
	zoneID := createZoneRef(t, srv, "example.com.", "ref-defaults")
	bareID := strings.TrimPrefix(zoneID, "/hostedzone/")

	// When: the zone's record sets are listed
	out := listRRSets(t, srv, bareID, "")

	// Then: exactly the default NS and SOA records exist at the apex
	if len(out.ResourceRecordSets) != 2 {
		t.Fatalf("expected 2 default records, got %d", len(out.ResourceRecordSets))
	}
	ns, soa := out.ResourceRecordSets[0], out.ResourceRecordSets[1]
	if ns.Type != "NS" || ns.Name != "example.com." {
		t.Errorf("expected first record to be apex NS, got %s %s", ns.Name, ns.Type)
	}
	if len(ns.ResourceRecords) != 4 {
		t.Errorf("expected 4 NS values, got %d", len(ns.ResourceRecords))
	}
	if ns.TTL != 172800 {
		t.Errorf("expected NS TTL 172800, got %d", ns.TTL)
	}
	if soa.Type != "SOA" || soa.Name != "example.com." {
		t.Errorf("expected second record to be apex SOA, got %s %s", soa.Name, soa.Type)
	}
	if soa.TTL != 900 {
		t.Errorf("expected SOA TTL 900, got %d", soa.TTL)
	}
	if len(soa.ResourceRecords) != 1 || !strings.Contains(soa.ResourceRecords[0], "awsdns-hostmaster.amazon.com.") {
		t.Errorf("expected SOA value with awsdns-hostmaster.amazon.com., got %v", soa.ResourceRecords)
	}
}

// ── GetHostedZone ────────────────────────────────────────────────────────────

func TestGetHostedZone_includesDelegationSet(t *testing.T) {
	// Given: a hosted zone exists
	srv := helpers.NewTestServer(t)
	zoneID := createZoneRef(t, srv, "example.com.", "ref-get-ds")
	bareID := strings.TrimPrefix(zoneID, "/hostedzone/")

	// When: GetHostedZone is called
	resp := r53Call(t, srv, http.MethodGet, "/2013-04-01/hostedzone/"+bareID, "")
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		DelegationSet struct {
			NameServers []string `xml:"NameServers>NameServer"`
		} `xml:"DelegationSet"`
	}
	decodeXML(t, resp, &out)

	// Then: the same delegation set is returned
	if len(out.DelegationSet.NameServers) != 4 {
		t.Errorf("expected 4 name servers, got %d", len(out.DelegationSet.NameServers))
	}
}

// ── DeleteHostedZone ─────────────────────────────────────────────────────────

func TestDeleteHostedZone_notEmpty(t *testing.T) {
	// Given: a hosted zone with a non-default record
	srv := helpers.NewTestServer(t)
	zoneID := createZoneRef(t, srv, "example.com.", "ref-del-full")
	bareID := strings.TrimPrefix(zoneID, "/hostedzone/")
	upsertRecord(t, srv, bareID, "www.example.com.", "A", "1.2.3.4")

	// When: DeleteHostedZone is called
	resp := r53Call(t, srv, http.MethodDelete, "/2013-04-01/hostedzone/"+bareID, "")

	// Then: HostedZoneNotEmpty is returned and the zone survives
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertQueryXMLError(t, resp, "HostedZoneNotEmpty")

	getResp := r53Call(t, srv, http.MethodGet, "/2013-04-01/hostedzone/"+bareID, "")
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusOK)
}

func TestDeleteHostedZone_succeedsWithOnlyDefaultRecords(t *testing.T) {
	// Given: a hosted zone whose only records are the default NS + SOA
	srv := helpers.NewTestServer(t)
	zoneID := createZoneRef(t, srv, "example.com.", "ref-del-defaults")
	bareID := strings.TrimPrefix(zoneID, "/hostedzone/")

	// When: DeleteHostedZone is called
	resp := r53Call(t, srv, http.MethodDelete, "/2013-04-01/hostedzone/"+bareID, "")
	defer resp.Body.Close()

	// Then: the delete succeeds even though NS + SOA still exist
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ── ListHostedZones pagination ───────────────────────────────────────────────

func TestListHostedZones_pagination(t *testing.T) {
	// Given: three hosted zones
	srv := helpers.NewTestServer(t)
	createZoneRef(t, srv, "alpha.com.", "ref-a")
	createZoneRef(t, srv, "beta.com.", "ref-b")
	createZoneRef(t, srv, "gamma.com.", "ref-c")

	// When: the first page of two is requested
	resp := r53Call(t, srv, http.MethodGet, "/2013-04-01/hostedzone?maxitems=2", "")
	helpers.AssertStatus(t, resp, http.StatusOK)
	var page1 struct {
		HostedZones []struct {
			Id string `xml:"Id"`
		} `xml:"HostedZones>HostedZone"`
		IsTruncated bool   `xml:"IsTruncated"`
		NextMarker  string `xml:"NextMarker"`
		MaxItems    string `xml:"MaxItems"`
	}
	decodeXML(t, resp, &page1)

	// Then: the page is truncated with a marker for the next zone
	if len(page1.HostedZones) != 2 {
		t.Fatalf("expected 2 zones on page 1, got %d", len(page1.HostedZones))
	}
	if !page1.IsTruncated || page1.NextMarker == "" {
		t.Fatalf("expected IsTruncated with NextMarker, got truncated=%v marker=%q", page1.IsTruncated, page1.NextMarker)
	}
	if page1.MaxItems != "2" {
		t.Errorf("expected MaxItems=2 echoed, got %q", page1.MaxItems)
	}

	// And: the second page returns the remaining zone
	resp2 := r53Call(t, srv, http.MethodGet, "/2013-04-01/hostedzone?maxitems=2&marker="+page1.NextMarker, "")
	helpers.AssertStatus(t, resp2, http.StatusOK)
	var page2 struct {
		HostedZones []struct {
			Id string `xml:"Id"`
		} `xml:"HostedZones>HostedZone"`
		IsTruncated bool `xml:"IsTruncated"`
	}
	decodeXML(t, resp2, &page2)
	if len(page2.HostedZones) != 1 || page2.IsTruncated {
		t.Errorf("expected final page with 1 zone, got %d (truncated=%v)", len(page2.HostedZones), page2.IsTruncated)
	}
}

// ── ListHostedZonesByName ────────────────────────────────────────────────────

func TestListHostedZonesByName_ordersAndFilters(t *testing.T) {
	// Given: zones whose DNS-order differs from creation order
	srv := helpers.NewTestServer(t)
	createZoneRef(t, srv, "www.example.com.", "ref-www")
	createZoneRef(t, srv, "example.com.", "ref-apex")
	createZoneRef(t, srv, "abc.example.com.", "ref-abc")

	// When: ListHostedZonesByName is called
	resp := r53Call(t, srv, http.MethodGet, "/2013-04-01/hostedzonesbyname", "")
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		HostedZones []struct {
			Name string `xml:"Name"`
		} `xml:"HostedZones>HostedZone"`
		IsTruncated bool `xml:"IsTruncated"`
	}
	decodeXML(t, resp, &out)

	// Then: zones come back in DNS order (labels reversed)
	got := make([]string, 0, len(out.HostedZones))
	for _, z := range out.HostedZones {
		got = append(got, z.Name)
	}
	want := []string{"example.com.", "abc.example.com.", "www.example.com."}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("expected DNS order %v, got %v", want, got)
	}

	// And: dnsname= starts listing at the given name
	resp2 := r53Call(t, srv, http.MethodGet, "/2013-04-01/hostedzonesbyname?dnsname=abc.example.com.", "")
	helpers.AssertStatus(t, resp2, http.StatusOK)
	var out2 struct {
		HostedZones []struct {
			Name string `xml:"Name"`
		} `xml:"HostedZones>HostedZone"`
	}
	decodeXML(t, resp2, &out2)
	if len(out2.HostedZones) != 2 || out2.HostedZones[0].Name != "abc.example.com." {
		t.Errorf("expected listing to start at abc.example.com., got %+v", out2.HostedZones)
	}
}

// ── GetHostedZoneCount ───────────────────────────────────────────────────────

func TestGetHostedZoneCount(t *testing.T) {
	// Given: two hosted zones
	srv := helpers.NewTestServer(t)
	createZoneRef(t, srv, "one.com.", "ref-1")
	createZoneRef(t, srv, "two.com.", "ref-2")

	// When: GetHostedZoneCount is called
	resp := r53Call(t, srv, http.MethodGet, "/2013-04-01/hostedzonecount", "")
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		HostedZoneCount int `xml:"HostedZoneCount"`
	}
	decodeXML(t, resp, &out)

	// Then: the count is 2
	if out.HostedZoneCount != 2 {
		t.Errorf("expected HostedZoneCount=2, got %d", out.HostedZoneCount)
	}
}

// ── UpdateHostedZoneComment ──────────────────────────────────────────────────

func TestUpdateHostedZoneComment(t *testing.T) {
	// Given: a hosted zone exists
	srv := helpers.NewTestServer(t)
	zoneID := createZoneRef(t, srv, "example.com.", "ref-comment")
	bareID := strings.TrimPrefix(zoneID, "/hostedzone/")

	// When: the comment is updated
	body := `<UpdateHostedZoneCommentRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">` +
		`<Comment>internal zone</Comment>` +
		`</UpdateHostedZoneCommentRequest>`
	resp := r53Call(t, srv, http.MethodPost, "/2013-04-01/hostedzone/"+bareID, body)
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		HostedZone struct {
			Config struct {
				Comment string `xml:"Comment"`
			} `xml:"Config"`
		} `xml:"HostedZone"`
	}
	decodeXML(t, resp, &out)

	// Then: the response and a subsequent GetHostedZone both carry the comment
	if out.HostedZone.Config.Comment != "internal zone" {
		t.Errorf("expected updated comment, got %q", out.HostedZone.Config.Comment)
	}
	getResp := r53Call(t, srv, http.MethodGet, "/2013-04-01/hostedzone/"+bareID, "")
	helpers.AssertStatus(t, getResp, http.StatusOK)
	var got struct {
		HostedZone struct {
			Config struct {
				Comment string `xml:"Comment"`
			} `xml:"Config"`
		} `xml:"HostedZone"`
	}
	decodeXML(t, getResp, &got)
	if got.HostedZone.Config.Comment != "internal zone" {
		t.Errorf("expected persisted comment, got %q", got.HostedZone.Config.Comment)
	}
}

func TestUpdateHostedZoneComment_notFound(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: UpdateHostedZoneComment targets a missing zone
	body := `<UpdateHostedZoneCommentRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">` +
		`<Comment>x</Comment>` +
		`</UpdateHostedZoneCommentRequest>`
	resp := r53Call(t, srv, http.MethodPost, "/2013-04-01/hostedzone/ZMISSING", body)

	// Then: NoSuchHostedZone is returned
	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertQueryXMLError(t, resp, "NoSuchHostedZone")
}

// ── Private zones ────────────────────────────────────────────────────────────

func TestCreateHostedZone_privateZoneStoresVPC(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: a private hosted zone is created with a VPC
	body := `<CreateHostedZoneRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">` +
		`<Name>internal.example.com.</Name>` +
		`<CallerReference>ref-private</CallerReference>` +
		`<HostedZoneConfig><PrivateZone>true</PrivateZone></HostedZoneConfig>` +
		`<VPC><VPCRegion>us-east-1</VPCRegion><VPCId>vpc-12345678</VPCId></VPC>` +
		`</CreateHostedZoneRequest>`
	resp := r53Call(t, srv, http.MethodPost, "/2013-04-01/hostedzone", body)
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var out struct {
		HostedZone struct {
			Id     string `xml:"Id"`
			Config struct {
				PrivateZone bool `xml:"PrivateZone"`
			} `xml:"Config"`
		} `xml:"HostedZone"`
		VPC struct {
			VPCRegion string `xml:"VPCRegion"`
			VPCId     string `xml:"VPCId"`
		} `xml:"VPC"`
	}
	decodeXML(t, resp, &out)

	// Then: the create response echoes the VPC
	if !out.HostedZone.Config.PrivateZone {
		t.Error("expected PrivateZone=true")
	}
	if out.VPC.VPCId != "vpc-12345678" || out.VPC.VPCRegion != "us-east-1" {
		t.Errorf("expected VPC echoed, got %+v", out.VPC)
	}

	// And: GetHostedZone returns the associated VPC
	bareID := strings.TrimPrefix(out.HostedZone.Id, "/hostedzone/")
	getResp := r53Call(t, srv, http.MethodGet, "/2013-04-01/hostedzone/"+bareID, "")
	helpers.AssertStatus(t, getResp, http.StatusOK)
	var got struct {
		VPCs []struct {
			VPCId string `xml:"VPCId"`
		} `xml:"VPCs>VPC"`
	}
	decodeXML(t, getResp, &got)
	if len(got.VPCs) != 1 || got.VPCs[0].VPCId != "vpc-12345678" {
		t.Errorf("expected one associated VPC, got %+v", got.VPCs)
	}
}
