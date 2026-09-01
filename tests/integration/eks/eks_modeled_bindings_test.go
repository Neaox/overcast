package eks_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// The six operations #858 records were implemented at hand-invented paths —
// four of them on a hand-invented HTTP method — so no AWS SDK could reach any
// of them. These tests drive each one at the binding the pinned model gives it
// (internal/awsapi/manifest.gen.go), and drive the invented sibling to prove it
// no longer answers. The second half matters as much as the first: a wrong
// method or a stray trailing "s" that still returns 200 is how this class of
// fault hides.

// expectAWSError asserts the response's status and AWS error code together.
// The code is what an SDK dispatches on, so asserting the status alone would
// pass for an error the model does not bind to the operation.
func expectAWSError(t *testing.T, resp *http.Response, status int, code string) map[string]any {
	t.Helper()
	body := expectJSONStatus(t, resp, status)
	if body["__type"] != code {
		t.Fatalf("expected %s for %d response, got %#v", code, status, body)
	}
	return body
}

// expectNoEKSAnswer asserts a request reached nothing that answers for EKS.
// The invented bindings are unregistered rather than kept as aliases, so what
// replies is the router's fallback, not this service — the assertion is that
// EKS's response envelope is absent, whatever the status.
func expectNoEKSAnswer(t *testing.T, resp *http.Response, envelopeKey string) {
	t.Helper()
	// The fallback may answer in XML (S3's) rather than JSON, so the body is
	// read raw and decoded leniently.
	raw := helpers.ReadBody(t, resp)
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("invented binding still answers 200; expected it to be unregistered. body=%s", raw)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		return
	}
	if _, found := body[envelopeKey]; found {
		t.Fatalf("invented binding answered with EKS's %q envelope: %s", envelopeKey, raw)
	}
}

// ─── DescribeAddonVersions — GET /addons/supported-versions ──────────────────

func TestEKSDescribeAddonVersions_modeledBinding(t *testing.T) {
	// Given: a running emulator (the add-on catalog is synthetic, not stored)
	srv := newEKSServer(t)

	// When: the add-on name is sent as the modeled httpQuery member
	body := expectJSONStatus(t, eksCall(t, http.MethodGet, srv.URL+"/addons/supported-versions?addonName=vpc-cni", nil), http.StatusOK)

	// Then: exactly that add-on comes back, with the AddonInfo members AWS binds
	addons, _ := body["addons"].([]any)
	if len(addons) != 1 {
		t.Fatalf("expected exactly the requested addon, got %#v", body["addons"])
	}
	entry, _ := addons[0].(map[string]any)
	if entry["addonName"] != "vpc-cni" {
		t.Fatalf("expected addonName vpc-cni, got %#v", entry)
	}
	for _, member := range []string{"type", "publisher", "owner", "addonVersions"} {
		if entry[member] == nil {
			t.Fatalf("expected AddonInfo member %q in the response, got %#v", member, entry)
		}
	}
}

func TestEKSDescribeAddonVersions_omittedAddonNameReturnsTheWholeCatalog(t *testing.T) {
	// Given: addonName is an optional query member in the model
	srv := newEKSServer(t)

	// When: it is omitted
	body := expectJSONStatus(t, eksCall(t, http.MethodGet, srv.URL+"/addons/supported-versions", nil), http.StatusOK)

	// Then: every catalogued add-on is listed rather than none
	addons, _ := body["addons"].([]any)
	if len(addons) < 2 {
		t.Fatalf("expected the whole catalog when addonName is omitted, got %#v", body["addons"])
	}
}

func TestEKSDescribeAddonVersions_kubernetesVersionFiltersTheCatalog(t *testing.T) {
	// Given: the synthetic catalog declares compatibility with 1.30
	srv := newEKSServer(t)

	// When: a compatible and an incompatible cluster version are asked for
	compatible := expectJSONStatus(t, eksCall(t, http.MethodGet, srv.URL+"/addons/supported-versions?kubernetesVersion=1.30", nil), http.StatusOK)
	incompatible := expectJSONStatus(t, eksCall(t, http.MethodGet, srv.URL+"/addons/supported-versions?kubernetesVersion=1.99", nil), http.StatusOK)

	// Then: only the compatible one matches
	if got, _ := compatible["addons"].([]any); len(got) == 0 {
		t.Fatalf("expected addons compatible with 1.30, got %#v", compatible["addons"])
	}
	if got, _ := incompatible["addons"].([]any); len(got) != 0 {
		t.Fatalf("expected no addons compatible with 1.99, got %#v", incompatible["addons"])
	}
}

func TestEKSDescribeAddonVersions_paginatesWithMaxResultsAndNextToken(t *testing.T) {
	// Given: the operation is @paginated over addons with maxResults/nextToken
	srv := newEKSServer(t)

	// When: a page smaller than the catalog is requested
	first := expectJSONStatus(t, eksCall(t, http.MethodGet, srv.URL+"/addons/supported-versions?maxResults=1", nil), http.StatusOK)

	// Then: one item and a continuation token come back, and the token advances
	page, _ := first["addons"].([]any)
	if len(page) != 1 {
		t.Fatalf("expected one addon per page, got %#v", first["addons"])
	}
	token, _ := first["nextToken"].(string)
	if token == "" {
		t.Fatalf("expected a nextToken while the catalog is truncated, got %#v", first)
	}

	second := expectJSONStatus(t, eksCall(t, http.MethodGet, srv.URL+"/addons/supported-versions?maxResults=1&nextToken="+token, nil), http.StatusOK)
	nextPage, _ := second["addons"].([]any)
	if len(nextPage) != 1 {
		t.Fatalf("expected one addon on the second page, got %#v", second["addons"])
	}
	firstName, _ := page[0].(map[string]any)
	secondName, _ := nextPage[0].(map[string]any)
	if firstName["addonName"] == secondName["addonName"] {
		t.Fatalf("second page repeated the first: %v", firstName["addonName"])
	}
}

func TestEKSDescribeAddonVersions_rejectsAnUndecodableNextToken(t *testing.T) {
	// Given: a token that did not come from a previous page
	srv := newEKSServer(t)

	// When/Then: it is refused rather than silently restarting at page one
	expectAWSError(t, eksCall(t, http.MethodGet, srv.URL+"/addons/supported-versions?nextToken=not-a-token", nil),
		http.StatusBadRequest, "InvalidParameterException")
}

func TestEKSDescribeAddonVersions_inventedPathDoesNotAnswer(t *testing.T) {
	// Given: the path Overcast invented, with the add-on name as a path segment
	srv := newEKSServer(t)

	// When/Then: nothing serves it
	expectNoEKSAnswer(t, eksCall(t, http.MethodGet, srv.URL+"/addons/vpc-cni/versions", nil), "addons")
}

// ─── DescribeAddonConfiguration — GET /addons/configuration-schemas ──────────

func TestEKSDescribeAddonConfiguration_modeledBinding(t *testing.T) {
	// Given: addonName and addonVersion are both required httpQuery members
	srv := newEKSServer(t)

	// When: both are supplied
	body := expectJSONStatus(t, eksCall(t, http.MethodGet,
		srv.URL+"/addons/configuration-schemas?addonName=vpc-cni&addonVersion=v1.18.3-eksbuild.3", nil), http.StatusOK)

	// Then: the schema for the version that was asked for comes back
	if body["addonName"] != "vpc-cni" {
		t.Fatalf("expected addonName vpc-cni, got %#v", body)
	}
	if body["addonVersion"] != "v1.18.3-eksbuild.3" {
		t.Fatalf("expected the requested addonVersion to be echoed, got %#v", body["addonVersion"])
	}
	if schema, _ := body["configurationSchema"].(string); schema == "" {
		t.Fatalf("expected a configurationSchema, got %#v", body)
	}
}

func TestEKSDescribeAddonConfiguration_requiresBothQueryMembers(t *testing.T) {
	// Given: the model marks addonName and addonVersion required
	srv := newEKSServer(t)

	// When/Then: omitting either is an InvalidParameterException
	expectAWSError(t, eksCall(t, http.MethodGet, srv.URL+"/addons/configuration-schemas?addonVersion=v1.18.3-eksbuild.3", nil),
		http.StatusBadRequest, "InvalidParameterException")
	expectAWSError(t, eksCall(t, http.MethodGet, srv.URL+"/addons/configuration-schemas?addonName=vpc-cni", nil),
		http.StatusBadRequest, "InvalidParameterException")
}

func TestEKSDescribeAddonConfiguration_unknownVersionIsNotFound(t *testing.T) {
	// Given: a catalogued add-on and a version it does not have
	srv := newEKSServer(t)

	// When/Then: the version is honoured rather than ignored
	expectResourceNotFound(t, eksCall(t, http.MethodGet,
		srv.URL+"/addons/configuration-schemas?addonName=vpc-cni&addonVersion=v0.0.0-eksbuild.0", nil))
}

func TestEKSDescribeAddonConfiguration_inventedPathDoesNotAnswer(t *testing.T) {
	// Given: the invented path, with the add-on name as a path segment
	srv := newEKSServer(t)

	// When/Then: nothing serves it
	expectNoEKSAnswer(t, eksCall(t, http.MethodGet, srv.URL+"/addons/vpc-cni/configuration", nil), "configurationSchema")
}

// ─── DescribeIdentityProviderConfig — POST .../identity-provider-configs/describe

func TestEKSDescribeIdentityProviderConfig_modeledBinding(t *testing.T) {
	// Given: a cluster with an associated OIDC identity provider config
	srv := newEKSServer(t)
	_ = mustCreateCluster(t, srv.URL, "idp-modeled-cluster", nil)
	_ = mustAssociateIdentityProviderConfig(t, srv.URL, "idp-modeled-cluster", "okta-modeled")

	// When: it is described through the modeled POST with a body member
	body := expectJSONStatus(t, eksCall(t, http.MethodPost, srv.URL+"/clusters/idp-modeled-cluster/identity-provider-configs/describe",
		map[string]any{"identityProviderConfig": map[string]any{"type": "oidc", "name": "okta-modeled"}}), http.StatusOK)

	// Then: the response is the modeled IdentityProviderConfigResponse envelope
	config, ok := body["identityProviderConfig"].(map[string]any)
	if !ok {
		t.Fatalf("expected an identityProviderConfig envelope, got %#v", body)
	}
	oidc, ok := config["oidc"].(map[string]any)
	if !ok {
		t.Fatalf("expected the config under an oidc member, got %#v", config)
	}
	if oidc["identityProviderConfigName"] != "okta-modeled" {
		t.Fatalf("expected identityProviderConfigName okta-modeled, got %#v", oidc)
	}
	if oidc["clusterName"] != "idp-modeled-cluster" {
		t.Fatalf("expected clusterName on the oidc config, got %#v", oidc)
	}
	if arn, _ := oidc["identityProviderConfigArn"].(string); arn == "" {
		t.Fatalf("expected identityProviderConfigArn on the oidc config, got %#v", oidc)
	}
	if oidc["issuerUrl"] != "https://idp.example.com" {
		t.Fatalf("expected the stored issuerUrl, got %#v", oidc)
	}
}

func TestEKSDescribeIdentityProviderConfig_requiresTypeAndName(t *testing.T) {
	// Given: identityProviderConfig, and its type and name, are all required
	srv := newEKSServer(t)
	_ = mustCreateCluster(t, srv.URL, "idp-required-cluster", nil)

	// When/Then: an empty body and a partial member are both refused
	expectAWSError(t, eksCall(t, http.MethodPost, srv.URL+"/clusters/idp-required-cluster/identity-provider-configs/describe",
		map[string]any{}), http.StatusBadRequest, "InvalidParameterException")
	expectAWSError(t, eksCall(t, http.MethodPost, srv.URL+"/clusters/idp-required-cluster/identity-provider-configs/describe",
		map[string]any{"identityProviderConfig": map[string]any{"type": "oidc"}}), http.StatusBadRequest, "InvalidParameterException")
}

func TestEKSDescribeIdentityProviderConfig_unknownConfigIsNotFound(t *testing.T) {
	// Given: a cluster with no identity provider configs
	srv := newEKSServer(t)
	_ = mustCreateCluster(t, srv.URL, "idp-missing-cluster", nil)

	// When/Then: describing one is a ResourceNotFoundException
	expectResourceNotFound(t, eksCall(t, http.MethodPost, srv.URL+"/clusters/idp-missing-cluster/identity-provider-configs/describe",
		map[string]any{"identityProviderConfig": map[string]any{"type": "oidc", "name": "absent"}}))
}

func TestEKSDescribeIdentityProviderConfig_inventedGetPathDoesNotAnswer(t *testing.T) {
	// Given: the invented GET binding with type and name as path segments
	srv := newEKSServer(t)
	_ = mustCreateCluster(t, srv.URL, "idp-invented-cluster", nil)
	_ = mustAssociateIdentityProviderConfig(t, srv.URL, "idp-invented-cluster", "okta-invented")

	// When/Then: nothing serves it, even though the config exists
	expectNoEKSAnswer(t, eksCall(t, http.MethodGet,
		srv.URL+"/clusters/idp-invented-cluster/identity-provider-configs/oidc/okta-invented", nil), "identityProviderConfig")
}

// ─── ListInsights — POST /clusters/{clusterName}/insights ────────────────────

func TestEKSListInsights_modeledBinding(t *testing.T) {
	// Given: a cluster
	srv := newEKSServer(t)
	_ = mustCreateCluster(t, srv.URL, "insights-modeled-cluster", nil)

	// When: insights are listed through the modeled POST
	body := expectJSONStatus(t, eksCall(t, http.MethodPost, srv.URL+"/clusters/insights-modeled-cluster/insights",
		map[string]any{}), http.StatusOK)

	// Then: the synthetic insight summaries come back
	insights, _ := body["insights"].([]any)
	if len(insights) == 0 {
		t.Fatalf("expected synthetic insights, got %#v", body)
	}
}

func TestEKSListInsights_honoursTheModeledFilter(t *testing.T) {
	// Given: a cluster whose synthetic insight is a PASSING UPGRADE_READINESS
	srv := newEKSServer(t)
	_ = mustCreateCluster(t, srv.URL, "insights-filter-cluster", nil)
	base := srv.URL + "/clusters/insights-filter-cluster/insights"

	// When: each filter member is applied, matching and not matching
	matching := expectJSONStatus(t, eksCall(t, http.MethodPost, base,
		map[string]any{"filter": map[string]any{"categories": []string{"UPGRADE_READINESS"}}}), http.StatusOK)
	otherCategory := expectJSONStatus(t, eksCall(t, http.MethodPost, base,
		map[string]any{"filter": map[string]any{"categories": []string{"MISCONFIGURATION"}}}), http.StatusOK)
	otherStatus := expectJSONStatus(t, eksCall(t, http.MethodPost, base,
		map[string]any{"filter": map[string]any{"statuses": []string{"ERROR"}}}), http.StatusOK)

	// Then: only the matching filter returns the insight
	if got, _ := matching["insights"].([]any); len(got) == 0 {
		t.Fatalf("expected the UPGRADE_READINESS insight, got %#v", matching["insights"])
	}
	if got, _ := otherCategory["insights"].([]any); len(got) != 0 {
		t.Fatalf("expected no MISCONFIGURATION insights, got %#v", otherCategory["insights"])
	}
	if got, _ := otherStatus["insights"].([]any); len(got) != 0 {
		t.Fatalf("expected no ERROR insights, got %#v", otherStatus["insights"])
	}
}

func TestEKSListInsights_rejectsAnUndecodableNextToken(t *testing.T) {
	// Given: a cluster
	srv := newEKSServer(t)
	_ = mustCreateCluster(t, srv.URL, "insights-token-cluster", nil)

	// When/Then: a token that did not come from a previous page is refused
	expectAWSError(t, eksCall(t, http.MethodPost, srv.URL+"/clusters/insights-token-cluster/insights",
		map[string]any{"nextToken": "not-a-token"}), http.StatusBadRequest, "InvalidParameterException")
}

func TestEKSListInsights_inventedGetMethodDoesNotAnswer(t *testing.T) {
	// Given: a cluster, and the GET the emulator used to serve
	srv := newEKSServer(t)
	_ = mustCreateCluster(t, srv.URL, "insights-get-cluster", nil)

	// When/Then: the method AWS does not model answers nothing
	expectNoEKSAnswer(t, eksCall(t, http.MethodGet, srv.URL+"/clusters/insights-get-cluster/insights", nil), "insights")
}

// ─── UpdateAddon — POST /clusters/{clusterName}/addons/{addonName}/update ────

func TestEKSUpdateAddon_modeledBinding(t *testing.T) {
	// Given: a cluster with an add-on
	srv := newEKSServer(t)
	_ = mustCreateCluster(t, srv.URL, "addon-update-modeled-cluster", nil)
	_ = mustCreateAddon(t, srv.URL, "addon-update-modeled-cluster", "vpc-cni", "v1.17.1-eksbuild.1")

	// When: it is updated at the modeled path — "update", not "updates"
	body := expectJSONStatus(t, eksCall(t, http.MethodPost,
		srv.URL+"/clusters/addon-update-modeled-cluster/addons/vpc-cni/update",
		map[string]any{"addonVersion": "v1.18.3-eksbuild.3"}), http.StatusOK)

	// Then: an Update is returned and the stored add-on moved
	update, ok := body["update"].(map[string]any)
	if !ok || update["status"] != "Successful" {
		t.Fatalf("expected a successful update, got %#v", body)
	}
	describe := expectJSONStatus(t, eksCall(t, http.MethodGet,
		srv.URL+"/clusters/addon-update-modeled-cluster/addons/vpc-cni", nil), http.StatusOK)
	addon, _ := describe["addon"].(map[string]any)
	if addon["addonVersion"] != "v1.18.3-eksbuild.3" {
		t.Fatalf("expected the updated addonVersion, got %#v", addon)
	}
}

func TestEKSUpdateAddon_inventedPluralPathDoesNotAnswer(t *testing.T) {
	// Given: a cluster with an add-on, and the one-character-different path
	srv := newEKSServer(t)
	_ = mustCreateCluster(t, srv.URL, "addon-update-invented-cluster", nil)
	_ = mustCreateAddon(t, srv.URL, "addon-update-invented-cluster", "vpc-cni", "v1.17.1-eksbuild.1")

	// When/Then: ".../updates" answers nothing
	expectNoEKSAnswer(t, eksCall(t, http.MethodPost,
		srv.URL+"/clusters/addon-update-invented-cluster/addons/vpc-cni/updates",
		map[string]any{"addonVersion": "v1.18.3-eksbuild.3"}), "update")
}

// ─── UpdateNodegroupVersion — POST .../node-groups/{name}/update-version ─────

func TestEKSUpdateNodegroupVersion_modeledBinding(t *testing.T) {
	// Given: a cluster with a nodegroup
	srv := newEKSServer(t)
	_ = mustCreateCluster(t, srv.URL, "ng-update-modeled-cluster", nil)
	_ = mustCreateNodegroup(t, srv.URL, "ng-update-modeled-cluster", "workers", []string{"subnet-1"})

	// When: the version is updated at the modeled path
	body := expectJSONStatus(t, eksCall(t, http.MethodPost,
		srv.URL+"/clusters/ng-update-modeled-cluster/node-groups/workers/update-version",
		map[string]any{"version": "1.32"}), http.StatusOK)

	// Then: an Update is returned and the stored nodegroup moved
	update, ok := body["update"].(map[string]any)
	if !ok || update["status"] != "Successful" {
		t.Fatalf("expected a successful update, got %#v", body)
	}
	describe := expectJSONStatus(t, eksCall(t, http.MethodGet,
		srv.URL+"/clusters/ng-update-modeled-cluster/node-groups/workers", nil), http.StatusOK)
	nodegroup, _ := describe["nodegroup"].(map[string]any)
	if nodegroup["version"] != "1.32" {
		t.Fatalf("expected the updated version, got %#v", nodegroup)
	}
}

func TestEKSUpdateNodegroupVersion_acceptsReleaseVersionWithoutVersion(t *testing.T) {
	// Given: a nodegroup, and a model in which no body member is required
	srv := newEKSServer(t)
	_ = mustCreateCluster(t, srv.URL, "ng-release-cluster", nil)
	_ = mustCreateNodegroup(t, srv.URL, "ng-release-cluster", "workers", []string{"subnet-1"})

	// When: only releaseVersion is sent, as `aws eks update-nodegroup-version
	// --release-version` does
	body := expectJSONStatus(t, eksCall(t, http.MethodPost,
		srv.URL+"/clusters/ng-release-cluster/node-groups/workers/update-version",
		map[string]any{"releaseVersion": "1.31.0-20250101"}), http.StatusOK)

	// Then: it is accepted and applied, not rejected for a missing version
	if _, ok := body["update"].(map[string]any); !ok {
		t.Fatalf("expected an update, got %#v", body)
	}
	describe := expectJSONStatus(t, eksCall(t, http.MethodGet,
		srv.URL+"/clusters/ng-release-cluster/node-groups/workers", nil), http.StatusOK)
	nodegroup, _ := describe["nodegroup"].(map[string]any)
	if nodegroup["releaseVersion"] != "1.31.0-20250101" {
		t.Fatalf("expected the updated releaseVersion, got %#v", nodegroup)
	}
}

func TestEKSUpdateNodegroupVersion_inventedPluralPathDoesNotAnswer(t *testing.T) {
	// Given: a nodegroup, and the invented ".../updates" path
	srv := newEKSServer(t)
	_ = mustCreateCluster(t, srv.URL, "ng-update-invented-cluster", nil)
	_ = mustCreateNodegroup(t, srv.URL, "ng-update-invented-cluster", "workers", []string{"subnet-1"})

	// When/Then: it answers nothing
	expectNoEKSAnswer(t, eksCall(t, http.MethodPost,
		srv.URL+"/clusters/ng-update-invented-cluster/node-groups/workers/updates",
		map[string]any{"version": "1.32"}), "update")
}

// ─── Operations AWS does not model at all ───────────────────────────────────

func TestEKSUnmodeledOperations_areNotServed(t *testing.T) {
	// Given: a cluster with an identity provider config. #861 recorded three
	// capability rows naming operations AWS has no model for; two of them were
	// reachable emulator inventions and are gone.
	srv := newEKSServer(t)
	_ = mustCreateCluster(t, srv.URL, "unmodeled-cluster", nil)
	_ = mustAssociateIdentityProviderConfig(t, srv.URL, "unmodeled-cluster", "okta-unmodeled")

	// When/Then: neither invented route answers
	expectNoEKSAnswer(t, eksCall(t, http.MethodGet, srv.URL+"/access-policies/AmazonEKSViewPolicy", nil), "accessPolicy")
	expectNoEKSAnswer(t, eksCall(t, http.MethodPost,
		srv.URL+"/clusters/unmodeled-cluster/identity-provider-configs/oidc/okta-unmodeled/update",
		map[string]any{"oidc": map[string]any{"usernameClaim": "email"}}), "update")
}
