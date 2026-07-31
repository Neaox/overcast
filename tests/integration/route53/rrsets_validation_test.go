// ChangeResourceRecordSets / ListResourceRecordSets tests for inert-level
// AWS fidelity: InvalidChangeBatch semantics, apex protections, routing
// metadata (SetIdentifier/Weight), DNS-order listing and pagination.
package route53_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func newZone(t *testing.T, srv *helpers.TestServer, name, ref string) (bareID string) {
	t.Helper()
	return strings.TrimPrefix(createZoneRef(t, srv, name, ref), "/hostedzone/")
}

const simpleARecord = `<ResourceRecordSet>` +
	`<Name>www.example.com.</Name><Type>A</Type><TTL>300</TTL>` +
	`<ResourceRecords><ResourceRecord><Value>1.2.3.4</Value></ResourceRecord></ResourceRecords>` +
	`</ResourceRecordSet>`

// ── InvalidChangeBatch semantics ─────────────────────────────────────────────

func TestChangeRRSets_createDuplicate(t *testing.T) {
	// Given: a record already exists
	srv := helpers.NewTestServer(t)
	zone := newZone(t, srv, "example.com.", "ref-ccb-dup")
	resp := changeRRSets(t, srv, zone, `<Change><Action>CREATE</Action>`+simpleARecord+`</Change>`)
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// When: CREATE is attempted again for the same name and type
	resp2 := changeRRSets(t, srv, zone, `<Change><Action>CREATE</Action>`+simpleARecord+`</Change>`)

	// Then: InvalidChangeBatch says the record already exists
	helpers.AssertStatus(t, resp2, http.StatusBadRequest)
	code, msg := errMessage(t, resp2)
	if code != "InvalidChangeBatch" {
		t.Errorf("expected InvalidChangeBatch, got %q (%s)", code, msg)
	}
	if !strings.Contains(msg, "but it already exists") {
		t.Errorf("expected 'but it already exists' in message, got %q", msg)
	}
}

func TestChangeRRSets_deleteMissing(t *testing.T) {
	// Given: a zone with no www record
	srv := helpers.NewTestServer(t)
	zone := newZone(t, srv, "example.com.", "ref-ccb-missing")

	// When: DELETE targets the non-existent record
	resp := changeRRSets(t, srv, zone, `<Change><Action>DELETE</Action>`+simpleARecord+`</Change>`)

	// Then: InvalidChangeBatch says the record was not found
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	code, msg := errMessage(t, resp)
	if code != "InvalidChangeBatch" {
		t.Errorf("expected InvalidChangeBatch, got %q (%s)", code, msg)
	}
	if !strings.Contains(msg, "but it was not found") {
		t.Errorf("expected 'but it was not found' in message, got %q", msg)
	}
}

func TestChangeRRSets_deleteValueMismatch(t *testing.T) {
	// Given: an A record with value 1.2.3.4
	srv := helpers.NewTestServer(t)
	zone := newZone(t, srv, "example.com.", "ref-ccb-mismatch")
	upsertRecord(t, srv, zone, "www.example.com.", "A", "1.2.3.4")

	// When: DELETE provides different values
	del := `<Change><Action>DELETE</Action><ResourceRecordSet>` +
		`<Name>www.example.com.</Name><Type>A</Type><TTL>300</TTL>` +
		`<ResourceRecords><ResourceRecord><Value>5.6.7.8</Value></ResourceRecord></ResourceRecords>` +
		`</ResourceRecordSet></Change>`
	resp := changeRRSets(t, srv, zone, del)

	// Then: InvalidChangeBatch reports the value mismatch and the record survives
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	code, msg := errMessage(t, resp)
	if code != "InvalidChangeBatch" {
		t.Errorf("expected InvalidChangeBatch, got %q (%s)", code, msg)
	}
	if !strings.Contains(msg, "do not match") {
		t.Errorf("expected value-mismatch message, got %q", msg)
	}
	out := listRRSets(t, srv, zone, "")
	if len(out.ResourceRecordSets) != 3 {
		t.Errorf("expected NS+SOA+A to survive, got %d records", len(out.ResourceRecordSets))
	}
}

func TestChangeRRSets_outOfZoneName(t *testing.T) {
	// Given: a zone for example.com
	srv := helpers.NewTestServer(t)
	zone := newZone(t, srv, "example.com.", "ref-ccb-oob")

	// When: a record outside the zone is created
	rec := `<Change><Action>CREATE</Action><ResourceRecordSet>` +
		`<Name>www.other.com.</Name><Type>A</Type><TTL>300</TTL>` +
		`<ResourceRecords><ResourceRecord><Value>1.2.3.4</Value></ResourceRecord></ResourceRecords>` +
		`</ResourceRecordSet></Change>`
	resp := changeRRSets(t, srv, zone, rec)

	// Then: InvalidChangeBatch says the name is not permitted in the zone
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	code, msg := errMessage(t, resp)
	if code != "InvalidChangeBatch" {
		t.Errorf("expected InvalidChangeBatch, got %q (%s)", code, msg)
	}
	if !strings.Contains(msg, "is not permitted in zone") {
		t.Errorf("expected 'is not permitted in zone' in message, got %q", msg)
	}
}

func TestChangeRRSets_apexNSAndSOADeleteRejected(t *testing.T) {
	// Given: a fresh zone with its default apex records
	srv := helpers.NewTestServer(t)
	zone := newZone(t, srv, "example.com.", "ref-ccb-apex")

	for _, rrType := range []string{"NS", "SOA"} {
		// When: the apex default record is deleted
		del := `<Change><Action>DELETE</Action><ResourceRecordSet>` +
			`<Name>example.com.</Name><Type>` + rrType + `</Type><TTL>300</TTL>` +
			`<ResourceRecords><ResourceRecord><Value>x</Value></ResourceRecord></ResourceRecords>` +
			`</ResourceRecordSet></Change>`
		resp := changeRRSets(t, srv, zone, del)

		// Then: the delete is rejected
		helpers.AssertStatus(t, resp, http.StatusBadRequest)
		code, _ := errMessage(t, resp)
		if code != "InvalidChangeBatch" {
			t.Errorf("%s: expected InvalidChangeBatch, got %q", rrType, code)
		}
	}
}

func TestChangeRRSets_cnameAtApexRejected(t *testing.T) {
	// Given: a zone for example.com
	srv := helpers.NewTestServer(t)
	zone := newZone(t, srv, "example.com.", "ref-ccb-cname")

	// When: a CNAME is created at the zone apex
	rec := `<Change><Action>CREATE</Action><ResourceRecordSet>` +
		`<Name>example.com.</Name><Type>CNAME</Type><TTL>300</TTL>` +
		`<ResourceRecords><ResourceRecord><Value>target.example.net.</Value></ResourceRecord></ResourceRecords>` +
		`</ResourceRecordSet></Change>`
	resp := changeRRSets(t, srv, zone, rec)

	// Then: InvalidChangeBatch rejects a CNAME at the apex
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	code, msg := errMessage(t, resp)
	if code != "InvalidChangeBatch" {
		t.Errorf("expected InvalidChangeBatch, got %q (%s)", code, msg)
	}
	if !strings.Contains(msg, "not permitted at apex") {
		t.Errorf("expected apex-CNAME message, got %q", msg)
	}
}

func TestChangeRRSets_emptyBatch(t *testing.T) {
	// Given: a zone
	srv := helpers.NewTestServer(t)
	zone := newZone(t, srv, "example.com.", "ref-ccb-empty")

	// When: a change batch with no changes is submitted
	resp := changeRRSets(t, srv, zone, ``)

	// Then: InvalidChangeBatch is returned
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertQueryXMLError(t, resp, "InvalidChangeBatch")
}

func TestChangeRRSets_invalidAction(t *testing.T) {
	// Given: a zone
	srv := helpers.NewTestServer(t)
	zone := newZone(t, srv, "example.com.", "ref-ccb-action")

	// When: an unknown action is submitted
	resp := changeRRSets(t, srv, zone, `<Change><Action>REPLACE</Action>`+simpleARecord+`</Change>`)

	// Then: InvalidInput is returned
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertQueryXMLError(t, resp, "InvalidInput")
}

func TestChangeRRSets_aliasAndValuesMutuallyExclusive(t *testing.T) {
	// Given: a zone
	srv := helpers.NewTestServer(t)
	zone := newZone(t, srv, "example.com.", "ref-ccb-alias")

	// When: a change carries both an alias target and TTL + values
	rec := `<Change><Action>UPSERT</Action><ResourceRecordSet>` +
		`<Name>www.example.com.</Name><Type>A</Type><TTL>300</TTL>` +
		`<ResourceRecords><ResourceRecord><Value>1.2.3.4</Value></ResourceRecord></ResourceRecords>` +
		`<AliasTarget><HostedZoneId>Z2FDTNDATAQYW2</HostedZoneId><DNSName>d123.cloudfront.net.</DNSName><EvaluateTargetHealth>false</EvaluateTargetHealth></AliasTarget>` +
		`</ResourceRecordSet></Change>`
	resp := changeRRSets(t, srv, zone, rec)

	// Then: InvalidInput is returned, as on real AWS
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	code, msg := errMessage(t, resp)
	if code != "InvalidInput" {
		t.Errorf("expected InvalidInput, got %q (%s)", code, msg)
	}
	if !strings.Contains(msg, "Expected exactly one of") {
		t.Errorf("expected 'Expected exactly one of' in message, got %q", msg)
	}
}

func TestChangeRRSets_recordWithoutValuesOrAlias(t *testing.T) {
	// Given: a zone
	srv := helpers.NewTestServer(t)
	zone := newZone(t, srv, "example.com.", "ref-ccb-novalues")

	// When: a record has neither values nor an alias target
	rec := `<Change><Action>CREATE</Action><ResourceRecordSet>` +
		`<Name>www.example.com.</Name><Type>A</Type><TTL>300</TTL>` +
		`</ResourceRecordSet></Change>`
	resp := changeRRSets(t, srv, zone, rec)

	// Then: InvalidInput is returned
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertQueryXMLError(t, resp, "InvalidInput")
}

// ── Normalisation and routing metadata ───────────────────────────────────────

func TestChangeRRSets_normalisesRecordName(t *testing.T) {
	// Given: a zone
	srv := helpers.NewTestServer(t)
	zone := newZone(t, srv, "example.com.", "ref-ccb-norm")

	// When: a record is upserted with mixed case and no trailing dot
	rec := `<Change><Action>UPSERT</Action><ResourceRecordSet>` +
		`<Name>WWW.Example.COM</Name><Type>A</Type><TTL>300</TTL>` +
		`<ResourceRecords><ResourceRecord><Value>1.2.3.4</Value></ResourceRecord></ResourceRecords>` +
		`</ResourceRecordSet></Change>`
	resp := changeRRSets(t, srv, zone, rec)
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Then: the listed record name is lowercase with a trailing dot
	out := listRRSets(t, srv, zone, "")
	var found bool
	for _, rr := range out.ResourceRecordSets {
		if rr.Type == "A" && rr.Name == "www.example.com." {
			found = true
		}
	}
	if !found {
		t.Errorf("expected normalised record www.example.com. A, got %+v", out.ResourceRecordSets)
	}
}

func TestChangeRRSets_weightedRecordsWithSetIdentifier(t *testing.T) {
	// Given: a zone
	srv := helpers.NewTestServer(t)
	zone := newZone(t, srv, "example.com.", "ref-ccb-weighted")

	// When: two weighted records share a name and type with distinct identifiers
	for _, w := range []struct{ id, weight, value string }{
		{"blue", "10", "1.1.1.1"},
		{"green", "20", "2.2.2.2"},
	} {
		rec := `<Change><Action>CREATE</Action><ResourceRecordSet>` +
			`<Name>api.example.com.</Name><Type>A</Type>` +
			`<SetIdentifier>` + w.id + `</SetIdentifier><Weight>` + w.weight + `</Weight>` +
			`<TTL>60</TTL>` +
			`<ResourceRecords><ResourceRecord><Value>` + w.value + `</Value></ResourceRecord></ResourceRecords>` +
			`</ResourceRecordSet></Change>`
		resp := changeRRSets(t, srv, zone, rec)
		helpers.AssertStatus(t, resp, http.StatusOK)
		resp.Body.Close()
	}

	// Then: both variants are listed with their routing metadata
	out := listRRSets(t, srv, zone, "")
	var ids []string
	for _, rr := range out.ResourceRecordSets {
		if rr.Name == "api.example.com." && rr.Type == "A" {
			ids = append(ids, rr.SetIdentifier)
			if rr.Weight != 10 && rr.Weight != 20 {
				t.Errorf("expected Weight 10 or 20, got %d", rr.Weight)
			}
		}
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 weighted variants, got %d (%v)", len(ids), ids)
	}
}

// ── Listing: order and pagination ────────────────────────────────────────────

func TestListRRSets_dnsOrderAndPagination(t *testing.T) {
	// Given: a zone with two extra records (apex NS + SOA exist already)
	srv := helpers.NewTestServer(t)
	zone := newZone(t, srv, "example.com.", "ref-list-page")
	upsertRecord(t, srv, zone, "beta.example.com.", "A", "2.2.2.2")
	upsertRecord(t, srv, zone, "alpha.example.com.", "A", "1.1.1.1")

	// When: the first page of two is requested
	page1 := listRRSets(t, srv, zone, "?maxitems=2")

	// Then: the apex records come first (DNS order) and the page is truncated
	if len(page1.ResourceRecordSets) != 2 {
		t.Fatalf("expected 2 records on page 1, got %d", len(page1.ResourceRecordSets))
	}
	if page1.ResourceRecordSets[0].Type != "NS" || page1.ResourceRecordSets[1].Type != "SOA" {
		t.Errorf("expected apex NS then SOA first, got %s then %s",
			page1.ResourceRecordSets[0].Type, page1.ResourceRecordSets[1].Type)
	}
	if !page1.IsTruncated || page1.NextRecordName != "alpha.example.com." || page1.NextRecordType != "A" {
		t.Fatalf("expected truncation pointing at alpha.example.com. A, got truncated=%v name=%q type=%q",
			page1.IsTruncated, page1.NextRecordName, page1.NextRecordType)
	}

	// And: resuming from the pointer returns the remaining records in order
	page2 := listRRSets(t, srv, zone, "?maxitems=2&name="+page1.NextRecordName+"&type="+page1.NextRecordType)
	if len(page2.ResourceRecordSets) != 2 || page2.IsTruncated {
		t.Fatalf("expected final page with 2 records, got %d (truncated=%v)",
			len(page2.ResourceRecordSets), page2.IsTruncated)
	}
	if page2.ResourceRecordSets[0].Name != "alpha.example.com." || page2.ResourceRecordSets[1].Name != "beta.example.com." {
		t.Errorf("expected alpha then beta, got %q then %q",
			page2.ResourceRecordSets[0].Name, page2.ResourceRecordSets[1].Name)
	}
}
