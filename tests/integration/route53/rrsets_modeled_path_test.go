package route53_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// Route 53's record-set operations are the one place in this service where
// AWS's two published models disagree about the request URI, and the
// disagreement is a trailing slash:
//
//	botocore (boto3, the AWS CLI)  POST /2013-04-01/hostedzone/{Id}/rrset/
//	Smithy (Go v2, JS v3, Java v2) POST /2013-04-01/hostedzone/{HostedZoneId}/rrset
//
// chi treats the two as distinct patterns, so registering only the Smithy
// spelling 404s every boto3 and `aws route53 change-resource-record-sets`
// caller — #1413, wire-verified against alpha.36 and alpha.37. Real Route 53
// answers both, so Overcast registers both; these tests pin the spelling the
// rest of the suite does not already cover.
//
// ListResourceRecordSets carries no trailing slash in *either* model, so the
// slash form is not something a generated client sends. It is registered and
// tested anyway, because a caller that hand-builds one of these URLs
// hand-builds the other, and an emulator that answers POST .../rrset/ but
// 404s GET .../rrset/ is a trap real AWS does not set.

func TestChangeResourceRecordSets_trailingSlashPath(t *testing.T) {
	// Given: a hosted zone exists
	srv := helpers.NewTestServer(t)
	zoneID := createZone(t, srv, "example.com.")
	bareID := strings.TrimPrefix(zoneID, "/hostedzone/")

	// When: the change is posted to the URI botocore builds — with the
	// trailing slash — rather than the one the rest of the suite hand-signs
	body := `<ChangeResourceRecordSetsRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">` +
		`<ChangeBatch><Changes><Change>` +
		`<Action>UPSERT</Action>` +
		`<ResourceRecordSet>` +
		`<Name>www.example.com.</Name><Type>A</Type><TTL>300</TTL>` +
		`<ResourceRecords><ResourceRecord><Value>1.2.3.4</Value></ResourceRecord></ResourceRecords>` +
		`</ResourceRecordSet>` +
		`</Change></Changes></ChangeBatch>` +
		`</ChangeResourceRecordSetsRequest>`
	resp := r53Call(t, srv, http.MethodPost, "/2013-04-01/hostedzone/"+bareID+"/rrset/", body)

	// Then: the change applies, exactly as it does without the slash
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		ChangeInfo struct {
			Status string `xml:"Status"`
		} `xml:"ChangeInfo"`
	}
	decodeXML(t, resp, &out)
	if out.ChangeInfo.Status != "INSYNC" {
		t.Errorf("expected Status=INSYNC, got %q", out.ChangeInfo.Status)
	}

	// And: the record is really there, not merely acknowledged
	listed := listRRSets(t, srv, bareID, "")
	if !hasRecord(listed, "www.example.com.", "A") {
		t.Errorf("expected www.example.com. A in the zone, got %+v", listed.ResourceRecordSets)
	}
}

func TestListResourceRecordSets_trailingSlashPath(t *testing.T) {
	// Given: a hosted zone with one record
	srv := helpers.NewTestServer(t)
	zoneID := createZone(t, srv, "example.com.")
	bareID := strings.TrimPrefix(zoneID, "/hostedzone/")
	upsertRecord(t, srv, bareID, "www.example.com.", "A", "1.2.3.4")

	// When: the zone is listed at the trailing-slash spelling
	resp := r53Call(t, srv, http.MethodGet, "/2013-04-01/hostedzone/"+bareID+"/rrset/", "")

	// Then: the same record sets come back as without the slash
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out listRRSetsOut
	decodeXML(t, resp, &out)
	if !hasRecord(out, "www.example.com.", "A") {
		t.Errorf("expected www.example.com. A in the zone, got %+v", out.ResourceRecordSets)
	}
}

func hasRecord(out listRRSetsOut, name, rrType string) bool {
	for _, rr := range out.ResourceRecordSets {
		if rr.Name == name && rr.Type == rrType {
			return true
		}
	}
	return false
}
