package cloudtrail_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

const cloudTrailTargetPrefix = "com.amazonaws.cloudtrail.v20131101.CloudTrail_20131101."

func ctCall(t *testing.T, srv *helpers.TestServer, action string, body any) *http.Response {
	t.Helper()

	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s body: %v", action, err)
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new request %s: %v", action, err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", cloudTrailTargetPrefix+action)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request %s: %v", action, err)
	}
	return resp
}

func decodeMap(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()

	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return out
}

func TestCreateDescribeDeleteTrail_roundTrip(t *testing.T) {
	srv := helpers.NewTestServer(t)

	createResp := ctCall(t, srv, "CreateTrail", map[string]any{
		"Name":                       "trail-a",
		"S3BucketName":               "logs-bucket",
		"IncludeGlobalServiceEvents": true,
	})
	if createResp.StatusCode != http.StatusOK {
		t.Fatalf("CreateTrail status: got %d want 200", createResp.StatusCode)
	}
	createBody := decodeMap(t, createResp)
	if got := createBody["Name"]; got != "trail-a" {
		t.Fatalf("CreateTrail Name: got %v want trail-a", got)
	}

	descResp := ctCall(t, srv, "DescribeTrails", map[string]any{})
	if descResp.StatusCode != http.StatusOK {
		t.Fatalf("DescribeTrails status: got %d want 200", descResp.StatusCode)
	}
	descBody := decodeMap(t, descResp)
	trailsRaw, ok := descBody["trailList"].([]any)
	if !ok {
		t.Fatalf("DescribeTrails trailList missing or wrong type: %#v", descBody)
	}
	if len(trailsRaw) != 1 {
		t.Fatalf("DescribeTrails trail count: got %d want 1", len(trailsRaw))
	}

	delResp := ctCall(t, srv, "DeleteTrail", map[string]any{"Name": "trail-a"})
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("DeleteTrail status: got %d want 200", delResp.StatusCode)
	}
	_ = decodeMap(t, delResp)

	descAfterResp := ctCall(t, srv, "DescribeTrails", map[string]any{})
	if descAfterResp.StatusCode != http.StatusOK {
		t.Fatalf("DescribeTrails after delete status: got %d want 200", descAfterResp.StatusCode)
	}
	descAfterBody := decodeMap(t, descAfterResp)
	trailsAfterRaw, ok := descAfterBody["trailList"].([]any)
	if !ok {
		t.Fatalf("DescribeTrails after delete trailList missing or wrong type: %#v", descAfterBody)
	}
	if len(trailsAfterRaw) != 0 {
		t.Fatalf("DescribeTrails after delete trail count: got %d want 0", len(trailsAfterRaw))
	}
}

func TestLookupEvents_inertEmptyResult(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ctCall(t, srv, "LookupEvents", map[string]any{
		"MaxResults": 20,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("LookupEvents status: got %d want 200", resp.StatusCode)
	}
	body := decodeMap(t, resp)

	events, ok := body["Events"].([]any)
	if !ok {
		t.Fatalf("LookupEvents Events missing or wrong type: %#v", body)
	}
	if len(events) != 0 {
		t.Fatalf("LookupEvents event count: got %d want 0", len(events))
	}
}

// ---- Resource tagging --------------------------------------------------------
//
// CloudTrail spells its tag operations AddTags / RemoveTags / ListTags, and
// they are shaped unlike the usual trio: the resource is a `ResourceId`, the
// tags are a `TagsList`, RemoveTags takes tags rather than keys, and ListTags
// takes a *list* of resource IDs and answers with a `ResourceTagList`.

// ctCreateTrail creates a trail and returns its ARN.
func ctCreateTrail(t *testing.T, srv *helpers.TestServer, name string, extra map[string]any) string {
	t.Helper()
	body := map[string]any{"Name": name, "S3BucketName": "audit-bucket"}
	for k, v := range extra {
		body[k] = v
	}
	resp := ctCall(t, srv, "CreateTrail", body)
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		t.Fatalf("CreateTrail status: got %d want 200", resp.StatusCode)
	}
	arn, _ := decodeMap(t, resp)["TrailARN"].(string)
	if arn == "" {
		t.Fatal("CreateTrail response missing TrailARN")
	}
	return arn
}

// ctListTags reads one resource's tags back through ListTags.
func ctListTags(t *testing.T, srv *helpers.TestServer, arn string) map[string]string {
	t.Helper()
	resp := ctCall(t, srv, "ListTags", map[string]any{"ResourceIdList": []string{arn}})
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		t.Fatalf("ListTags status: got %d want 200", resp.StatusCode)
	}
	body := decodeMap(t, resp)
	entries, _ := body["ResourceTagList"].([]any)
	got := map[string]string{}
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		if id, _ := entry["ResourceId"].(string); id != arn {
			continue
		}
		list, _ := entry["TagsList"].([]any)
		for _, rawTag := range list {
			tag, _ := rawTag.(map[string]any)
			key, _ := tag["Key"].(string)
			value, _ := tag["Value"].(string)
			got[key] = value
		}
	}
	return got
}

func TestCloudTrailAddTags_roundTrips(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := ctCreateTrail(t, srv, "tagged-trail", nil)

	resp := ctCall(t, srv, "AddTags", map[string]any{
		"ResourceId": arn,
		"TagsList":   []map[string]string{{"Key": "env", "Value": "prod"}, {"Key": "team", "Value": "sec"}},
	})
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		t.Fatalf("AddTags status: got %d want 200", resp.StatusCode)
	}
	resp.Body.Close()

	if got := ctListTags(t, srv, arn); got["env"] != "prod" || got["team"] != "sec" {
		t.Fatalf("AddTags did not round-trip: got %v", got)
	}
}

// RemoveTags takes a TagsList, not a list of keys — CloudTrail matches on the
// Key of each entry and ignores the Value.
func TestCloudTrailRemoveTags_matchesOnKey(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := ctCreateTrail(t, srv, "untagged-trail", map[string]any{
		"TagsList": []map[string]string{{"Key": "env", "Value": "prod"}, {"Key": "keep", "Value": "yes"}},
	})

	resp := ctCall(t, srv, "RemoveTags", map[string]any{
		"ResourceId": arn,
		"TagsList":   []map[string]string{{"Key": "env", "Value": "a-different-value"}},
	})
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		t.Fatalf("RemoveTags status: got %d want 200", resp.StatusCode)
	}
	resp.Body.Close()

	got := ctListTags(t, srv, arn)
	if _, still := got["env"]; still {
		t.Errorf("RemoveTags left env in place: %v", got)
	}
	if got["keep"] != "yes" {
		t.Errorf("RemoveTags removed an unrelated tag: %v", got)
	}
}

func TestCloudTrailCreateTrail_appliesTagsAtCreation(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := ctCreateTrail(t, srv, "tag-on-create-trail", map[string]any{
		"TagsList": []map[string]string{{"Key": "env", "Value": "staging"}},
	})
	if got := ctListTags(t, srv, arn); got["env"] != "staging" {
		t.Errorf("CreateTrail tags not applied at creation: got %v", got)
	}
}

// ListTags takes a list of resource IDs and answers one entry per resource.
func TestCloudTrailListTags_returnsAnEntryPerResource(t *testing.T) {
	srv := helpers.NewTestServer(t)
	first := ctCreateTrail(t, srv, "trail-one", map[string]any{
		"TagsList": []map[string]string{{"Key": "which", "Value": "one"}},
	})
	second := ctCreateTrail(t, srv, "trail-two", map[string]any{
		"TagsList": []map[string]string{{"Key": "which", "Value": "two"}},
	})

	resp := ctCall(t, srv, "ListTags", map[string]any{"ResourceIdList": []string{first, second}})
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		t.Fatalf("ListTags status: got %d want 200", resp.StatusCode)
	}
	entries, _ := decodeMap(t, resp)["ResourceTagList"].([]any)
	if len(entries) != 2 {
		t.Fatalf("ResourceTagList has %d entries, want 2", len(entries))
	}
	byID := map[string]string{}
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		id, _ := entry["ResourceId"].(string)
		list, _ := entry["TagsList"].([]any)
		for _, rawTag := range list {
			tag, _ := rawTag.(map[string]any)
			if key, _ := tag["Key"].(string); key == "which" {
				byID[id], _ = tag["Value"].(string)
			}
		}
	}
	if byID[first] != "one" || byID[second] != "two" {
		t.Errorf("ListTags mixed up the resources: %v", byID)
	}
}

// Deleting the trail takes its tags with it.
func TestCloudTrailDeleteTrail_dropsItsTags(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := ctCreateTrail(t, srv, "recycled-trail", map[string]any{
		"TagsList": []map[string]string{{"Key": "env", "Value": "prod"}},
	})

	resp := ctCall(t, srv, "DeleteTrail", map[string]any{"Name": "recycled-trail"})
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		t.Fatalf("DeleteTrail status: got %d want 200", resp.StatusCode)
	}
	resp.Body.Close()

	ctCreateTrail(t, srv, "recycled-trail", nil)
	if got := ctListTags(t, srv, arn); len(got) != 0 {
		t.Errorf("recreated trail inherited tags from the deleted one: %v", got)
	}
}

func TestCloudTrailAddTags_unknownTrail(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := ctCall(t, srv, "AddTags", map[string]any{
		"ResourceId": "arn:aws:cloudtrail:us-east-1:000000000000:trail/nope",
		"TagsList":   []map[string]string{{"Key": "env", "Value": "prod"}},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("AddTags on a missing trail: got %d want 404", resp.StatusCode)
	}
}
