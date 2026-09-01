// Tag operation tests for the Route 53 emulator: ChangeTagsForResource,
// ListTagsForResource, ListTagsForResources for hosted zones and health checks.
package route53_test

import (
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

type tagXML struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

func changeTags(t *testing.T, srv *helpers.TestServer, resourceType, resourceID, body string) *http.Response {
	t.Helper()
	return r53Call(t, srv, http.MethodPost, "/2013-04-01/tags/"+resourceType+"/"+resourceID,
		`<ChangeTagsForResourceRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">`+body+`</ChangeTagsForResourceRequest>`)
}

func listTags(t *testing.T, srv *helpers.TestServer, resourceType, resourceID string) []tagXML {
	t.Helper()
	resp := r53Call(t, srv, http.MethodGet, "/2013-04-01/tags/"+resourceType+"/"+resourceID, "")
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		ResourceTagSet struct {
			ResourceType string   `xml:"ResourceType"`
			ResourceId   string   `xml:"ResourceId"`
			Tags         []tagXML `xml:"Tags>Tag"`
		} `xml:"ResourceTagSet"`
	}
	decodeXML(t, resp, &out)
	if out.ResourceTagSet.ResourceType != resourceType || out.ResourceTagSet.ResourceId != resourceID {
		t.Errorf("expected ResourceTagSet for %s/%s, got %s/%s",
			resourceType, resourceID, out.ResourceTagSet.ResourceType, out.ResourceTagSet.ResourceId)
	}
	return out.ResourceTagSet.Tags
}

func TestChangeTags_addAndList(t *testing.T) {
	// Given: a hosted zone
	srv := helpers.NewTestServer(t)
	zone := newZone(t, srv, "example.com.", "ref-tags-add")

	// When: two tags are added
	resp := changeTags(t, srv, "hostedzone", zone,
		`<AddTags><Tag><Key>env</Key><Value>dev</Value></Tag><Tag><Key>team</Key><Value>infra</Value></Tag></AddTags>`)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: ListTagsForResource returns both tags
	tags := listTags(t, srv, "hostedzone", zone)
	if len(tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(tags))
	}
}

func TestChangeTags_upsertAndRemove(t *testing.T) {
	// Given: a zone with tags env=dev and team=infra
	srv := helpers.NewTestServer(t)
	zone := newZone(t, srv, "example.com.", "ref-tags-rm")
	resp := changeTags(t, srv, "hostedzone", zone,
		`<AddTags><Tag><Key>env</Key><Value>dev</Value></Tag><Tag><Key>team</Key><Value>infra</Value></Tag></AddTags>`)
	resp.Body.Close()

	// When: env is overwritten and team removed in one call
	resp2 := changeTags(t, srv, "hostedzone", zone,
		`<AddTags><Tag><Key>env</Key><Value>prod</Value></Tag></AddTags><RemoveTagKeys><Key>team</Key></RemoveTagKeys>`)
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)

	// Then: only env=prod remains
	tags := listTags(t, srv, "hostedzone", zone)
	if len(tags) != 1 || tags[0].Key != "env" || tags[0].Value != "prod" {
		t.Errorf("expected single tag env=prod, got %+v", tags)
	}
}

func TestChangeTags_missingZone(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: tags are changed on a non-existent hosted zone
	resp := changeTags(t, srv, "hostedzone", "ZMISSING",
		`<AddTags><Tag><Key>env</Key><Value>dev</Value></Tag></AddTags>`)

	// Then: NoSuchHostedZone is returned
	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertQueryXMLError(t, resp, "NoSuchHostedZone")
}

func TestChangeTags_invalidResourceType(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: an unknown resource type is used
	resp := changeTags(t, srv, "loadbalancer", "X",
		`<AddTags><Tag><Key>env</Key><Value>dev</Value></Tag></AddTags>`)

	// Then: InvalidInput is returned
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertQueryXMLError(t, resp, "InvalidInput")
}

func TestListTagsForResources_batch(t *testing.T) {
	// Given: two tagged zones
	srv := helpers.NewTestServer(t)
	zoneA := newZone(t, srv, "a.com.", "ref-tags-batch-a")
	zoneB := newZone(t, srv, "b.com.", "ref-tags-batch-b")
	for _, z := range []string{zoneA, zoneB} {
		resp := changeTags(t, srv, "hostedzone", z,
			`<AddTags><Tag><Key>env</Key><Value>dev</Value></Tag></AddTags>`)
		resp.Body.Close()
	}

	// When: ListTagsForResources is called for both
	body := `<ListTagsForResourcesRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">` +
		`<ResourceIds><ResourceId>` + zoneA + `</ResourceId><ResourceId>` + zoneB + `</ResourceId></ResourceIds>` +
		`</ListTagsForResourcesRequest>`
	resp := r53Call(t, srv, http.MethodPost, "/2013-04-01/tags/hostedzone", body)
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		ResourceTagSets []struct {
			ResourceId string   `xml:"ResourceId"`
			Tags       []tagXML `xml:"Tags>Tag"`
		} `xml:"ResourceTagSets>ResourceTagSet"`
	}
	decodeXML(t, resp, &out)

	// Then: a tag set is returned per zone
	if len(out.ResourceTagSets) != 2 {
		t.Fatalf("expected 2 tag sets, got %d", len(out.ResourceTagSets))
	}
	for _, set := range out.ResourceTagSets {
		if len(set.Tags) != 1 || set.Tags[0].Key != "env" {
			t.Errorf("expected env tag on %s, got %+v", set.ResourceId, set.Tags)
		}
	}
}

func TestDeleteHostedZone_removesTags(t *testing.T) {
	// Given: a tagged zone with no extra records
	srv := helpers.NewTestServer(t)
	zone := newZone(t, srv, "example.com.", "ref-tags-cascade")
	resp := changeTags(t, srv, "hostedzone", zone,
		`<AddTags><Tag><Key>env</Key><Value>dev</Value></Tag></AddTags>`)
	resp.Body.Close()

	// When: the zone is deleted
	delResp := r53Call(t, srv, http.MethodDelete, "/2013-04-01/hostedzone/"+zone, "")
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("expected delete 200, got %d", delResp.StatusCode)
	}

	// Then: listing tags for the deleted zone reports NoSuchHostedZone
	listResp := r53Call(t, srv, http.MethodGet, "/2013-04-01/tags/hostedzone/"+zone, "")
	helpers.AssertStatus(t, listResp, http.StatusNotFound)
	helpers.AssertQueryXMLError(t, listResp, "NoSuchHostedZone")
}
