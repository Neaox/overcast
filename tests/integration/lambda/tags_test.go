package lambda_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func tagsURL(srv *helpers.TestServer, resource string) string {
	return srv.URL + "/2017-03-31/tags/" + url.PathEscape(resource)
}

func listResourceTags(t *testing.T, srv *helpers.TestServer, resource string) map[string]string {
	t.Helper()
	resp := doJSON(t, http.MethodGet, tagsURL(srv, resource), nil)
	helpers.AssertStatus(t, resp, http.StatusOK)
	var list struct {
		Tags map[string]string `json:"Tags"`
	}
	decodeJSON(t, resp, &list)
	return list.Tags
}

// TaggableResource in the pinned Lambda model is an ARN pattern. A bare
// function name, or an ARN belonging to another service, is a request-shape
// validation failure — not a resource lookup that happens to miss.
func TestLambdaTags_RejectsResourcesOutsideTheModeledPattern(t *testing.T) {
	resources := []struct {
		name     string
		resource string
	}{
		{name: "bare function name", resource: "my-function"},
		{name: "partial ARN", resource: "000000000000:function:my-function"},
		{name: "other service", resource: "arn:aws:s3:::my-bucket"},
		{name: "no resource segment", resource: "arn:aws:lambda:us-east-1:000000000000:"},
	}

	for _, tc := range resources {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a Lambda emulator with the function that the bare name would target.
			srv := helpers.NewTestServer(t)
			createFunction(t, srv, "my-function")

			// When/Then: every tag operation rejects the resource identically.
			for _, call := range []struct {
				method string
				body   any
				query  string
			}{
				{method: http.MethodPost, body: map[string]map[string]string{"Tags": {"env": "prod"}}},
				{method: http.MethodGet},
				{method: http.MethodDelete, query: "?tagKeys=env"},
			} {
				resp := doJSON(t, call.method, tagsURL(srv, tc.resource)+call.query, call.body)
				helpers.AssertStatus(t, resp, http.StatusBadRequest)
				helpers.AssertRequestID(t, resp)
				helpers.AssertJSONError(t, resp, "InvalidParameterValueException")
				resp.Body.Close()
			}
		})
	}
}

// AWS ListTags: "Lambda does not support adding tags to function aliases or
// versions." A qualified ARN satisfies TaggableResource's pattern, so this is a
// service-level refusal rather than a shape error — and it must not silently
// fall through to tagging the unqualified function.
func TestLambdaTags_RejectsQualifiedFunctionARNs(t *testing.T) {
	// Given: a function with a published version and an alias.
	srv := helpers.NewTestServer(t)
	fn := createFunction(t, srv, "qualified-tag-fn")
	version := publishLambdaVersion(t, srv, "qualified-tag-fn")
	createLambdaAlias(t, srv, "qualified-tag-fn", "live", version)

	for _, qualifier := range []string{version, "live", "$LATEST"} {
		t.Run(qualifier, func(t *testing.T) {
			// When: a qualified ARN is tagged.
			resp := doJSON(t, http.MethodPost, tagsURL(srv, fn.FunctionArn+":"+qualifier),
				map[string]map[string]string{"Tags": {"env": "prod"}})
			defer resp.Body.Close()

			// Then: the request is refused…
			helpers.AssertStatus(t, resp, http.StatusBadRequest)
			helpers.AssertRequestID(t, resp)
			helpers.AssertJSONError(t, resp, "InvalidParameterValueException")

			// …and no tag landed on the function itself.
			if tags := listResourceTags(t, srv, fn.FunctionArn); len(tags) != 0 {
				t.Fatalf("qualified TagResource leaked onto the function: %v", tags)
			}
		})
	}
}

// Tags and TagKeys are `required` members of TagResourceRequest and
// UntagResourceRequest respectively.
func TestLambdaTags_RequiresItsRequiredMembers(t *testing.T) {
	// Given: a tagged function.
	srv := helpers.NewTestServer(t)
	fn := createFunction(t, srv, "required-members-fn")
	tagResp := doJSON(t, http.MethodPost, tagsURL(srv, fn.FunctionArn),
		map[string]map[string]string{"Tags": {"env": "prod"}})
	tagResp.Body.Close()
	helpers.AssertStatus(t, tagResp, http.StatusNoContent)

	// When: TagResource omits Tags.
	missingTags := doJSON(t, http.MethodPost, tagsURL(srv, fn.FunctionArn), map[string]any{})
	defer missingTags.Body.Close()

	// Then: it is rejected.
	helpers.AssertStatus(t, missingTags, http.StatusBadRequest)
	helpers.AssertJSONError(t, missingTags, "InvalidParameterValueException")

	// When: UntagResource omits tagKeys.
	missingKeys := doJSON(t, http.MethodDelete, tagsURL(srv, fn.FunctionArn), nil)
	defer missingKeys.Body.Close()

	// Then: it is rejected rather than silently succeeding.
	helpers.AssertStatus(t, missingKeys, http.StatusBadRequest)
	helpers.AssertJSONError(t, missingKeys, "InvalidParameterValueException")

	// And: the existing tags are untouched by either refusal.
	if tags := listResourceTags(t, srv, fn.FunctionArn); tags["env"] != "prod" {
		t.Fatalf("tags mutated by refused requests: %v", tags)
	}
}

// CreateEventSourceMapping has a Tags member, and CloudFormation merges the
// stack's tags into it on every tagged deploy. The tags land in the mapping's
// own tag store, reachable through the same three tag operations.
func TestCreateEventSourceMapping_StoresTags(t *testing.T) {
	// Given: a function to attach a mapping to.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "esm-create-tag-fn")

	// When: the mapping is created with Tags.
	esm := createESM(t, srv, map[string]any{
		"FunctionName":   "esm-create-tag-fn",
		"EventSourceArn": sqsARN("esm-create-tag-queue"),
		"Tags":           map[string]string{"team": "platform", "env": "dev"},
	})
	esmARN, _ := esm["EventSourceMappingArn"].(string)
	if esmARN == "" {
		t.Fatalf("event source mapping has no ARN: %v", esm)
	}

	// Then: ListTags reports them…
	tags := listResourceTags(t, srv, esmARN)
	if tags["team"] != "platform" || tags["env"] != "dev" {
		t.Fatalf("tags after create = %v, want team=platform env=dev", tags)
	}

	// …TagResource still merges onto them…
	add := doJSON(t, http.MethodPost, tagsURL(srv, esmARN),
		map[string]map[string]string{"Tags": {"env": "prod"}})
	add.Body.Close()
	helpers.AssertStatus(t, add, http.StatusNoContent)
	tags = listResourceTags(t, srv, esmARN)
	if tags["team"] != "platform" || tags["env"] != "prod" {
		t.Fatalf("tags after TagResource = %v", tags)
	}

	// …UntagResource removes them…
	remove := doJSON(t, http.MethodDelete, tagsURL(srv, esmARN)+"?tagKeys=team", nil)
	remove.Body.Close()
	helpers.AssertStatus(t, remove, http.StatusNoContent)
	tags = listResourceTags(t, srv, esmARN)
	if _, present := tags["team"]; present {
		t.Fatalf("tags after UntagResource = %v", tags)
	}

	// …and the create response keeps AWS's shape: EventSourceMappingConfiguration
	// has no Tags member, so the tags are readable only through ListTags.
	if _, present := esm["Tags"]; present {
		t.Errorf("CreateEventSourceMapping echoed Tags; AWS's response shape has no such member")
	}
}

// TestCreateEventSourceMapping_TagsSurviveRestart lives in tags_restart_test.go:
// it needs a durable store, which a -tags nosqlite build does not have.

// AWS tags event source mappings through the same three operations.
func TestLambdaTags_EventSourceMappingLifecycle(t *testing.T) {
	// Given: an event source mapping.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "esm-tag-fn")
	esm := createESM(t, srv, map[string]any{
		"FunctionName":   "esm-tag-fn",
		"EventSourceArn": sqsARN("esm-tag-queue"),
	})
	esmARN, _ := esm["EventSourceMappingArn"].(string)
	if esmARN == "" {
		t.Fatalf("event source mapping has no ARN: %v", esm)
	}

	// When: tags are added, replaced and removed.
	add := doJSON(t, http.MethodPost, tagsURL(srv, esmARN),
		map[string]map[string]string{"Tags": {"env": "dev", "team": "platform"}})
	add.Body.Close()
	helpers.AssertStatus(t, add, http.StatusNoContent)
	if tags := listResourceTags(t, srv, esmARN); tags["env"] != "dev" || tags["team"] != "platform" {
		t.Fatalf("tags after add = %v", tags)
	}

	replace := doJSON(t, http.MethodPost, tagsURL(srv, esmARN),
		map[string]map[string]string{"Tags": {"env": "prod"}})
	replace.Body.Close()
	helpers.AssertStatus(t, replace, http.StatusNoContent)
	tags := listResourceTags(t, srv, esmARN)
	if tags["env"] != "prod" || tags["team"] != "platform" {
		t.Fatalf("TagResource must merge, not replace the whole set: %v", tags)
	}

	remove := doJSON(t, http.MethodDelete, tagsURL(srv, esmARN)+"?tagKeys=team&tagKeys=absent", nil)
	remove.Body.Close()
	helpers.AssertStatus(t, remove, http.StatusNoContent)
	tags = listResourceTags(t, srv, esmARN)
	if _, present := tags["team"]; present {
		t.Fatalf("tags after remove = %v", tags)
	}
	if tags["env"] != "prod" {
		t.Fatalf("unrelated tag removed: %v", tags)
	}

	// Then: an event source mapping's tags are not leaked into its own wire
	// shape — EventSourceMappingConfiguration has no Tags member.
	getResp := doJSON(t, http.MethodGet, lambdaURL(srv, "/event-source-mappings/"+esm["UUID"].(string)), nil)
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusOK)
	var config map[string]any
	decodeJSON(t, getResp, &config)
	if _, present := config["Tags"]; present {
		t.Errorf("EventSourceMappingConfiguration exposed Tags; AWS's shape has no such member")
	}
}

func TestLambdaTags_NotFound(t *testing.T) {
	tests := []struct {
		name     string
		resource string
	}{
		{
			name:     "unknown function",
			resource: "arn:aws:lambda:us-east-1:000000000000:function:absent-fn",
		},
		{
			name:     "unknown event source mapping",
			resource: "arn:aws:lambda:us-east-1:000000000000:event-source-mapping:11111111-2222-3333-4444-555555555555",
		},
		{
			name:     "function in another region",
			resource: "arn:aws:lambda:eu-west-1:000000000000:function:tags-not-found-fn",
		},
		{
			name:     "function in another account",
			resource: "arn:aws:lambda:us-east-1:111111111111:function:tags-not-found-fn",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a function that exists only in the request's own region and account.
			srv := helpers.NewTestServer(t)
			createFunction(t, srv, "tags-not-found-fn")

			// When/Then: every tag operation reports the resource as absent.
			for _, call := range []struct {
				method string
				body   any
				query  string
			}{
				{method: http.MethodPost, body: map[string]map[string]string{"Tags": {"env": "prod"}}},
				{method: http.MethodGet},
				{method: http.MethodDelete, query: "?tagKeys=env"},
			} {
				resp := doJSON(t, call.method, tagsURL(srv, tc.resource)+call.query, call.body)
				helpers.AssertStatus(t, resp, http.StatusNotFound)
				helpers.AssertRequestID(t, resp)
				helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
				resp.Body.Close()
			}
		})
	}
}

// Deleting the resource must take its tags with it, so a later resource that
// reuses the identifier does not inherit them.
func TestLambdaTags_DeletedEventSourceMappingDropsItsTags(t *testing.T) {
	// Given: a tagged event source mapping.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "esm-tag-delete-fn")
	esm := createESM(t, srv, map[string]any{
		"FunctionName":   "esm-tag-delete-fn",
		"EventSourceArn": sqsARN("esm-tag-delete-queue"),
	})
	uuid := esm["UUID"].(string)
	esmARN := esm["EventSourceMappingArn"].(string)
	add := doJSON(t, http.MethodPost, tagsURL(srv, esmARN),
		map[string]map[string]string{"Tags": {"env": "prod"}})
	add.Body.Close()
	helpers.AssertStatus(t, add, http.StatusNoContent)

	// When: the mapping is deleted.
	del := doJSON(t, http.MethodDelete, lambdaURL(srv, "/event-source-mappings/"+uuid), nil)
	del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusOK)

	// Then: its tags are gone with it.
	resp := doJSON(t, http.MethodGet, tagsURL(srv, esmARN), nil)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
}
