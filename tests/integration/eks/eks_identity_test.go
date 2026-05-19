package eks_test

import (
	"net/http"
	"testing"
)

func TestEKSDeleteClusterClearsIdentityProviderConfigs(t *testing.T) {
	srv := newEKSServer(t)

	_ = mustCreateCluster(t, srv.URL, "idp-cleanup-cluster", nil)
	_ = mustAssociateIdentityProviderConfig(t, srv.URL, "idp-cleanup-cluster", "okta-main")

	beforeBody := expectJSONStatus(t, eksCall(t, http.MethodGet, srv.URL+"/clusters/idp-cleanup-cluster/identity-provider-configs", nil), http.StatusOK)
	beforeConfigs, _ := beforeBody["identityProviderConfigs"].([]any)
	if len(beforeConfigs) == 0 {
		t.Fatalf("expected at least one idp config before cluster delete, got %v", beforeConfigs)
	}

	expectStatus(t, eksCall(t, http.MethodDelete, srv.URL+"/clusters/idp-cleanup-cluster", nil), http.StatusOK)
	_ = mustCreateCluster(t, srv.URL, "idp-cleanup-cluster", nil)

	afterBody := expectJSONStatus(t, eksCall(t, http.MethodGet, srv.URL+"/clusters/idp-cleanup-cluster/identity-provider-configs", nil), http.StatusOK)
	afterConfigs, _ := afterBody["identityProviderConfigs"].([]any)
	if len(afterConfigs) != 0 {
		t.Fatalf("expected no retained idp configs after cluster delete, got %v", afterConfigs)
	}
}

func TestEKSListIdentityProviderConfigs(t *testing.T) {
	srv := newEKSServer(t)

	_ = mustCreateCluster(t, srv.URL, "idp-cluster", nil)

	resp := eksCall(t, http.MethodGet, srv.URL+"/clusters/idp-cluster/identity-provider-configs", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for list identity provider configs, got %d", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	items, _ := body["identityProviderConfigs"].([]any)
	if len(items) != 0 {
		t.Fatalf("expected empty identityProviderConfigs list, got %v", items)
	}

	missing := eksCall(t, http.MethodGet, srv.URL+"/clusters/missing-cluster/identity-provider-configs", nil)
	_ = expectResourceNotFound(t, missing)
}

func TestEKSAssociateIdentityProviderConfig(t *testing.T) {
	srv := newEKSServer(t)

	_ = mustCreateCluster(t, srv.URL, "idp-assoc-cluster", nil)

	update := mustAssociateIdentityProviderConfig(t, srv.URL, "idp-assoc-cluster", "okta-main")
	if update["type"] != "AssociateIdentityProviderConfig" {
		t.Fatalf("expected update type AssociateIdentityProviderConfig, got %v", update["type"])
	}
	updateID, _ := update["id"].(string)
	if updateID == "" {
		t.Fatalf("expected non-empty update id")
	}

	listResp := eksCall(t, http.MethodGet, srv.URL+"/clusters/idp-assoc-cluster/identity-provider-configs", nil)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for list identity provider configs, got %d", listResp.StatusCode)
	}
	listBody := decodeBody(t, listResp)
	items, _ := listBody["identityProviderConfigs"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected one identity provider config, got %v", items)
	}
	item, _ := items[0].(map[string]any)
	if item["type"] != "oidc" || item["name"] != "okta-main" {
		t.Fatalf("expected oidc/okta-main entry, got %v", item)
	}

	updateResp := eksCall(t, http.MethodGet, srv.URL+"/clusters/idp-assoc-cluster/updates/"+updateID, nil)
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for describe update, got %d", updateResp.StatusCode)
	}
}

func TestEKSDisassociateIdentityProviderConfig(t *testing.T) {
	srv := newEKSServer(t)

	_ = mustCreateCluster(t, srv.URL, "idp-disassoc-cluster", nil)
	_ = mustAssociateIdentityProviderConfig(t, srv.URL, "idp-disassoc-cluster", "okta-main")

	disassocResp := eksCall(t, http.MethodPost, srv.URL+"/clusters/idp-disassoc-cluster/identity-provider-configs/disassociate", map[string]any{
		"identityProviderConfig": map[string]any{
			"type": "oidc",
			"name": "okta-main",
		},
	})
	if disassocResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for disassociate, got %d", disassocResp.StatusCode)
	}
	disassocBody := decodeBody(t, disassocResp)
	update, ok := disassocBody["update"].(map[string]any)
	if !ok {
		t.Fatalf("expected update in disassociate response, got %#v", disassocBody)
	}
	if update["type"] != "DisassociateIdentityProviderConfig" {
		t.Fatalf("expected update type DisassociateIdentityProviderConfig, got %v", update["type"])
	}

	listResp := eksCall(t, http.MethodGet, srv.URL+"/clusters/idp-disassoc-cluster/identity-provider-configs", nil)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for list identity provider configs, got %d", listResp.StatusCode)
	}
	listBody := decodeBody(t, listResp)
	items, _ := listBody["identityProviderConfigs"].([]any)
	if len(items) != 0 {
		t.Fatalf("expected no identity provider configs after disassociate, got %v", items)
	}
}

func TestEKSDescribeIdentityProviderConfig(t *testing.T) {
	srv := newEKSServer(t)

	_ = mustCreateCluster(t, srv.URL, "idp-describe-cluster", nil)
	_ = mustAssociateIdentityProviderConfig(t, srv.URL, "idp-describe-cluster", "okta-main")

	descResp := eksCall(t, http.MethodGet, srv.URL+"/clusters/idp-describe-cluster/identity-provider-configs/oidc/okta-main", nil)
	if descResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for describe identity provider config, got %d", descResp.StatusCode)
	}
	descBody := decodeBody(t, descResp)
	cfg, ok := descBody["identityProviderConfig"].(map[string]any)
	if !ok {
		t.Fatalf("expected identityProviderConfig in response, got %#v", descBody)
	}
	if cfg["type"] != "oidc" || cfg["name"] != "okta-main" {
		t.Fatalf("expected oidc/okta-main config, got %v", cfg)
	}
	oidc, _ := cfg["oidc"].(map[string]any)
	if oidc["issuerUrl"] != "https://idp.example.com" {
		t.Fatalf("expected issuerUrl in oidc config, got %v", oidc)
	}

	missing := eksCall(t, http.MethodGet, srv.URL+"/clusters/idp-describe-cluster/identity-provider-configs/oidc/missing", nil)
	_ = expectResourceNotFound(t, missing)
}

func TestEKSUpdateIdentityProviderConfig(t *testing.T) {
	srv := newEKSServer(t)

	_ = mustCreateCluster(t, srv.URL, "idp-update-cluster", nil)
	_ = mustAssociateIdentityProviderConfig(t, srv.URL, "idp-update-cluster", "okta-main")

	updateResp := eksCall(t, http.MethodPost, srv.URL+"/clusters/idp-update-cluster/identity-provider-configs/oidc/okta-main/update", map[string]any{
		"oidc": map[string]any{
			"issuerUrl":     "https://idp-v2.example.com",
			"usernameClaim": "email",
		},
	})
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for update identity provider config, got %d", updateResp.StatusCode)
	}
	body := decodeBody(t, updateResp)
	update, ok := body["update"].(map[string]any)
	if !ok {
		t.Fatalf("expected update in response, got %#v", body)
	}
	if update["type"] != "UpdateIdentityProviderConfig" {
		t.Fatalf("expected update type UpdateIdentityProviderConfig, got %v", update["type"])
	}

	descResp := eksCall(t, http.MethodGet, srv.URL+"/clusters/idp-update-cluster/identity-provider-configs/oidc/okta-main", nil)
	if descResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for describe identity provider config, got %d", descResp.StatusCode)
	}
	descBody := decodeBody(t, descResp)
	cfg, _ := descBody["identityProviderConfig"].(map[string]any)
	oidc, _ := cfg["oidc"].(map[string]any)
	if oidc["issuerUrl"] != "https://idp-v2.example.com" || oidc["usernameClaim"] != "email" {
		t.Fatalf("expected updated oidc fields, got %v", oidc)
	}
	if oidc["groupsClaim"] != "groups" {
		t.Fatalf("expected untouched groupsClaim to remain, got %v", oidc)
	}
}

func TestEKSAssociateIdentityProviderConfigPersistsInlineTags(t *testing.T) {
	srv := newEKSServer(t)

	_ = mustCreateCluster(t, srv.URL, "idp-tags-cluster", nil)

	associateBody := expectJSONStatus(t, eksCall(t, http.MethodPost, srv.URL+"/clusters/idp-tags-cluster/identity-provider-configs/associate", map[string]any{
		"oidc": map[string]any{
			"identityProviderConfigName": "okta-tags",
			"issuerUrl":                  "https://idp.example.com",
			"clientId":                   "kubernetes",
			"usernameClaim":              "sub",
			"groupsClaim":                "groups",
		},
		"tags": map[string]string{
			"env":   "dev",
			"owner": "eks-team",
		},
	}), http.StatusOK)
	update, _ := associateBody["update"].(map[string]any)
	if update["type"] != "AssociateIdentityProviderConfig" {
		t.Fatalf("expected AssociateIdentityProviderConfig update type, got %#v", update["type"])
	}

	describeBody := expectJSONStatus(t, eksCall(t, http.MethodGet, srv.URL+"/clusters/idp-tags-cluster/identity-provider-configs/oidc/okta-tags", nil), http.StatusOK)
	cfg, _ := describeBody["identityProviderConfig"].(map[string]any)
	tags, _ := cfg["tags"].(map[string]any)
	if len(tags) != 2 || tags["env"] != "dev" || tags["owner"] != "eks-team" {
		t.Fatalf("expected inline tags in identity provider describe response, got %#v", cfg["tags"])
	}
}

func TestEKSIdentityProviderTagsDoNotLeakAcrossClusterDeleteRecreate(t *testing.T) {
	srv := newEKSServer(t)

	_ = mustCreateCluster(t, srv.URL, "idp-tags-cleanup-cluster", nil)

	expectJSONStatus(t, eksCall(t, http.MethodPost, srv.URL+"/clusters/idp-tags-cleanup-cluster/identity-provider-configs/associate", map[string]any{
		"oidc": map[string]any{
			"identityProviderConfigName": "okta-tags",
			"issuerUrl":                  "https://idp.example.com",
			"clientId":                   "kubernetes",
			"usernameClaim":              "sub",
			"groupsClaim":                "groups",
		},
		"tags": map[string]string{
			"env": "dev",
		},
	}), http.StatusOK)

	expectStatus(t, eksCall(t, http.MethodDelete, srv.URL+"/clusters/idp-tags-cleanup-cluster", nil), http.StatusOK)
	_ = mustCreateCluster(t, srv.URL, "idp-tags-cleanup-cluster", nil)

	expectJSONStatus(t, eksCall(t, http.MethodPost, srv.URL+"/clusters/idp-tags-cleanup-cluster/identity-provider-configs/associate", map[string]any{
		"oidc": map[string]any{
			"identityProviderConfigName": "okta-tags",
			"issuerUrl":                  "https://idp.example.com",
			"clientId":                   "kubernetes",
			"usernameClaim":              "sub",
			"groupsClaim":                "groups",
		},
	}), http.StatusOK)

	describeBody := expectJSONStatus(t, eksCall(t, http.MethodGet, srv.URL+"/clusters/idp-tags-cleanup-cluster/identity-provider-configs/oidc/okta-tags", nil), http.StatusOK)
	cfg, _ := describeBody["identityProviderConfig"].(map[string]any)
	tags, _ := cfg["tags"].(map[string]any)
	if len(tags) != 0 {
		t.Fatalf("expected no leaked inline tags after cluster delete/recreate, got %#v", cfg["tags"])
	}
}
