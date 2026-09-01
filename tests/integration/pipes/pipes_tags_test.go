package pipes_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// pipesTagCall issues a request against the shared /tags/{resourceArn} route
// space the way the Pipes SDK does: the ARN is URL-escaped into a single path
// segment.
func pipesTagCall(t *testing.T, srv *helpers.TestServer, method, arn, query string, body map[string]any) *http.Response {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	u := srv.URL + "/tags/" + url.PathEscape(arn)
	if query != "" {
		u += "?" + query
	}
	req, err := http.NewRequest(method, u, rdr)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

func mustCreateTagTestPipe(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	streamARN := mustCreateStreamTable(t, srv, name+"-src")
	_, queueARN := mustCreateQueue(t, srv, name+"-dst")
	resp := createPipe(t, srv, name, streamARN, queueARN)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var p struct {
		Arn string `json:"Arn"`
	}
	helpers.DecodeJSON(t, resp, &p)
	if p.Arn == "" {
		t.Fatal("CreatePipe returned no Arn")
	}
	return p.Arn
}

// TestPipesTagResource_sdkRoundTrip drives the exact SDK wire shape: POST
// /tags/{escaped-arn} with a lowercase "tags" map, GET returning lowercase
// "tags", and DELETE with the tagKeys query parameter. Before the /tags route
// space had a single dispatching owner, these requests were silently answered
// by the EKS or API Gateway handlers and never reached the pipe.
func TestPipesTagResource_sdkRoundTrip(t *testing.T) {
	// Given: a pipe
	srv := helpers.NewTestServer(t)
	arn := mustCreateTagTestPipe(t, srv, "tag-pipe")

	// When: the pipe is tagged with the SDK's lowercase "tags" body
	resp := pipesTagCall(t, srv, http.MethodPost, arn, "", map[string]any{
		"tags": map[string]string{"env": "prod", "team": "events"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: ListTagsForResource returns them under lowercase "tags"
	get := pipesTagCall(t, srv, http.MethodGet, arn, "", nil)
	defer get.Body.Close()
	helpers.AssertStatus(t, get, http.StatusOK)
	var list struct {
		Tags map[string]string `json:"tags"`
	}
	helpers.DecodeJSON(t, get, &list)
	if list.Tags["env"] != "prod" || list.Tags["team"] != "events" {
		t.Errorf("tags: got %v, want env=prod team=events", list.Tags)
	}

	// When: one key is removed via the tagKeys query parameter
	del := pipesTagCall(t, srv, http.MethodDelete, arn, "tagKeys=env", nil)
	defer del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusOK)

	// Then: only the remaining tag is returned
	get2 := pipesTagCall(t, srv, http.MethodGet, arn, "", nil)
	defer get2.Body.Close()
	var list2 struct {
		Tags map[string]string `json:"tags"`
	}
	helpers.DecodeJSON(t, get2, &list2)
	if len(list2.Tags) != 1 || list2.Tags["team"] != "events" {
		t.Errorf("tags after untag: got %v, want only team=events", list2.Tags)
	}
}

// TestPipesTagResource_missingPipe: tagging a pipe that does not exist must
// return the Pipes NotFoundException — not silently store tags in another
// service's tag store.
func TestPipesTagResource_missingPipe(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := "arn:aws:pipes:us-east-1:000000000000:pipe/no-such-pipe"

	resp := pipesTagCall(t, srv, http.MethodPost, arn, "", map[string]any{
		"tags": map[string]string{"env": "prod"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertJSONError(t, resp, "NotFoundException")
}

// TestPipesTagResource_invalidTag: reserved aws: keys are rejected with the
// Pipes ValidationException.
func TestPipesTagResource_invalidTag(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := mustCreateTagTestPipe(t, srv, "badtag-pipe")

	resp := pipesTagCall(t, srv, http.MethodPost, arn, "", map[string]any{
		"tags": map[string]string{"aws:reserved": "x"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ValidationException")
}

// TestPipesTagResource_unescapedARN: clients that do not escape the ARN send
// a multi-segment path; the dispatcher must still route it to Pipes by the
// ARN's service prefix.
func TestPipesTagResource_unescapedARN(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := mustCreateTagTestPipe(t, srv, "rawpath-pipe")

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/tags/"+arn,
		bytes.NewReader([]byte(`{"tags":{"env":"dev"}}`)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	get := pipesTagCall(t, srv, http.MethodGet, arn, "", nil)
	defer get.Body.Close()
	var list struct {
		Tags map[string]string `json:"tags"`
	}
	helpers.DecodeJSON(t, get, &list)
	if list.Tags["env"] != "dev" {
		t.Errorf("tags: got %v, want env=dev", list.Tags)
	}
}
