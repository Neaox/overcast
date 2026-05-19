package eks_test

import (
	"net/http"
	"net/url"
	"testing"
)

func TestEKSDeleteClusterCleansAccessMetadata(t *testing.T) {
	srv := newEKSServer(t)

	_ = mustCreateCluster(t, srv.URL, "cleanup-cluster", nil)

	principal := "arn:aws:iam::000000000000:role/cleanup-principal"
	_ = mustCreateAccessEntry(t, srv.URL, "cleanup-cluster", principal, nil)

	encoded := url.PathEscape(principal)
	associate := eksCall(t, http.MethodPost, srv.URL+"/clusters/cleanup-cluster/access-entries/"+encoded+"/access-policies", map[string]any{
		"policyArn": "arn:aws:eks::aws:cluster-access-policy/AmazonEKSViewPolicy",
	})
	expectStatus(t, associate, http.StatusCreated)

	expectStatus(t, eksCall(t, http.MethodDelete, srv.URL+"/clusters/cleanup-cluster", nil), http.StatusOK)

	_ = mustCreateCluster(t, srv.URL, "cleanup-cluster", nil)

	listBody := expectJSONStatus(t, eksCall(t, http.MethodGet, srv.URL+"/clusters/cleanup-cluster/access-entries", nil), http.StatusOK)
	entries, _ := listBody["accessEntries"].([]any)
	if len(entries) != 0 {
		t.Fatalf("expected no retained access entries after cluster delete, got %v", entries)
	}
}

func TestEKSListAccessEntries(t *testing.T) {
	srv := newEKSServer(t)

	_ = mustCreateCluster(t, srv.URL, "access-entries-cluster", nil)

	listResp := eksCall(t, http.MethodGet, srv.URL+"/clusters/access-entries-cluster/access-entries", nil)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for list access entries, got %d", listResp.StatusCode)
	}
	body := decodeBody(t, listResp)
	entries, _ := body["accessEntries"].([]any)
	if len(entries) != 0 {
		t.Fatalf("expected empty accessEntries list, got %v", entries)
	}

	missing := eksCall(t, http.MethodGet, srv.URL+"/clusters/missing-cluster/access-entries", nil)
	_ = expectResourceNotFound(t, missing)
}

func TestEKSCreateAccessEntry(t *testing.T) {
	srv := newEKSServer(t)

	_ = mustCreateCluster(t, srv.URL, "access-entry-create-cluster", nil)

	entry := mustCreateAccessEntry(t, srv.URL, "access-entry-create-cluster", "arn:aws:iam::000000000000:role/app-team", map[string]any{
		"type":     "STANDARD",
		"username": "app-team-user",
	})
	if entry["principalArn"] != "arn:aws:iam::000000000000:role/app-team" {
		t.Fatalf("expected principalArn in create response, got %v", entry)
	}
	if entry["type"] != "STANDARD" {
		t.Fatalf("expected type STANDARD in create response, got %v", entry["type"])
	}

	listResp := eksCall(t, http.MethodGet, srv.URL+"/clusters/access-entry-create-cluster/access-entries", nil)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for list access entries, got %d", listResp.StatusCode)
	}
	listBody := decodeBody(t, listResp)
	entries, _ := listBody["accessEntries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("expected one access entry, got %v", entries)
	}
	if entries[0] != "arn:aws:iam::000000000000:role/app-team" {
		t.Fatalf("expected listed principalArn, got %v", entries[0])
	}

	dupResp := eksCall(t, http.MethodPost, srv.URL+"/clusters/access-entry-create-cluster/access-entries", map[string]any{
		"principalArn": "arn:aws:iam::000000000000:role/app-team",
	})
	if dupResp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for duplicate access entry, got %d", dupResp.StatusCode)
	}
	dupBody := decodeBody(t, dupResp)
	if dupBody["__type"] != "ResourceInUseException" {
		t.Fatalf("expected ResourceInUseException for duplicate access entry, got %#v", dupBody)
	}

	missingCluster := eksCall(t, http.MethodPost, srv.URL+"/clusters/missing-cluster/access-entries", map[string]any{
		"principalArn": "arn:aws:iam::000000000000:role/nope",
	})
	_ = expectResourceNotFound(t, missingCluster)

	missingPrincipal := eksCall(t, http.MethodPost, srv.URL+"/clusters/access-entry-create-cluster/access-entries", map[string]any{
		"username": "missing-principal",
	})
	if missingPrincipal.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for create access entry missing principalArn, got %d", missingPrincipal.StatusCode)
	}
	missingPrincipalBody := decodeBody(t, missingPrincipal)
	if missingPrincipalBody["__type"] != "MissingParameter" {
		t.Fatalf("expected MissingParameter for create access entry missing principalArn, got %#v", missingPrincipalBody)
	}
}

func TestEKSDescribeAccessEntry(t *testing.T) {
	srv := newEKSServer(t)

	_ = mustCreateCluster(t, srv.URL, "access-entry-describe-cluster", nil)

	principal := "arn:aws:iam::000000000000:role/dev/platform-team"
	_ = mustCreateAccessEntry(t, srv.URL, "access-entry-describe-cluster", principal, map[string]any{
		"username":         "platform-team",
		"kubernetesGroups": []string{"system:masters"},
	})

	encoded := url.PathEscape(principal)
	describeResp := eksCall(t, http.MethodGet, srv.URL+"/clusters/access-entry-describe-cluster/access-entries/"+encoded, nil)
	if describeResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for describe access entry, got %d", describeResp.StatusCode)
	}
	body := decodeBody(t, describeResp)
	entry, _ := body["accessEntry"].(map[string]any)
	if entry["principalArn"] != principal {
		t.Fatalf("expected principalArn %q, got %v", principal, entry["principalArn"])
	}
	if entry["username"] != "platform-team" {
		t.Fatalf("expected username platform-team, got %v", entry["username"])
	}
	if entry["type"] != "STANDARD" {
		t.Fatalf("expected default type STANDARD, got %v", entry["type"])
	}

	missingEntry := eksCall(t, http.MethodGet, srv.URL+"/clusters/access-entry-describe-cluster/access-entries/"+url.PathEscape("arn:aws:iam::000000000000:role/missing"), nil)
	_ = expectResourceNotFound(t, missingEntry)

	missingCluster := eksCall(t, http.MethodGet, srv.URL+"/clusters/no-cluster/access-entries/"+encoded, nil)
	_ = expectResourceNotFound(t, missingCluster)
}

func TestEKSDeleteAccessEntry(t *testing.T) {
	srv := newEKSServer(t)

	_ = mustCreateCluster(t, srv.URL, "access-entry-delete-cluster", nil)

	principal := "arn:aws:iam::000000000000:role/delete/me"
	_ = mustCreateAccessEntry(t, srv.URL, "access-entry-delete-cluster", principal, nil)

	encoded := url.PathEscape(principal)
	deleteResp := eksCall(t, http.MethodDelete, srv.URL+"/clusters/access-entry-delete-cluster/access-entries/"+encoded, nil)
	if deleteResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for delete access entry, got %d", deleteResp.StatusCode)
	}

	listResp := eksCall(t, http.MethodGet, srv.URL+"/clusters/access-entry-delete-cluster/access-entries", nil)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for list after delete, got %d", listResp.StatusCode)
	}
	listBody := decodeBody(t, listResp)
	entries, _ := listBody["accessEntries"].([]any)
	if len(entries) != 0 {
		t.Fatalf("expected zero access entries after delete, got %v", entries)
	}

	describeDeleted := eksCall(t, http.MethodGet, srv.URL+"/clusters/access-entry-delete-cluster/access-entries/"+encoded, nil)
	_ = expectResourceNotFound(t, describeDeleted)

	deleteMissing := eksCall(t, http.MethodDelete, srv.URL+"/clusters/access-entry-delete-cluster/access-entries/"+encoded, nil)
	_ = expectResourceNotFound(t, deleteMissing)

	deleteMissingCluster := eksCall(t, http.MethodDelete, srv.URL+"/clusters/no-cluster/access-entries/"+encoded, nil)
	_ = expectResourceNotFound(t, deleteMissingCluster)

	_ = mustCreateAccessEntry(t, srv.URL, "access-entry-delete-cluster", principal, nil)
	_ = mustAssociateAccessPolicy(t, srv.URL, "access-entry-delete-cluster", principal, map[string]any{
		"policyArn": "arn:aws:eks::aws:cluster-access-policy/AmazonEKSViewPolicy",
	})

	deleteAgain := eksCall(t, http.MethodDelete, srv.URL+"/clusters/access-entry-delete-cluster/access-entries/"+encoded, nil)
	if deleteAgain.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for deleting recreated access entry, got %d", deleteAgain.StatusCode)
	}

	_ = mustCreateAccessEntry(t, srv.URL, "access-entry-delete-cluster", principal, nil)

	listAssociated := eksCall(t, http.MethodGet, srv.URL+"/clusters/access-entry-delete-cluster/access-entries/"+encoded+"/access-policies", nil)
	if listAssociated.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for list associated policies on recreated entry, got %d", listAssociated.StatusCode)
	}
	associatedBody := decodeBody(t, listAssociated)
	associated, _ := associatedBody["associatedAccessPolicies"].([]any)
	if len(associated) != 0 {
		t.Fatalf("expected no retained associated policies after access entry deletion, got %v", associated)
	}
}

func TestEKSUpdateAccessEntry(t *testing.T) {
	srv := newEKSServer(t)

	_ = mustCreateCluster(t, srv.URL, "access-entry-update-cluster", nil)

	principal := "arn:aws:iam::000000000000:role/update/me"
	_ = mustCreateAccessEntry(t, srv.URL, "access-entry-update-cluster", principal, map[string]any{
		"username":         "bootstrap-user",
		"kubernetesGroups": []string{"devs"},
	})

	encoded := url.PathEscape(principal)
	updateResp := eksCall(t, http.MethodPost, srv.URL+"/clusters/access-entry-update-cluster/access-entries/"+encoded, map[string]any{
		"username":         "updated-user",
		"kubernetesGroups": []string{"system:masters", "platform"},
	})
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for update access entry, got %d", updateResp.StatusCode)
	}
	body := decodeBody(t, updateResp)
	entry, _ := body["accessEntry"].(map[string]any)
	if entry["username"] != "updated-user" {
		t.Fatalf("expected updated username, got %v", entry["username"])
	}
	groups, _ := entry["kubernetesGroups"].([]any)
	if len(groups) != 2 || groups[0] != "system:masters" || groups[1] != "platform" {
		t.Fatalf("expected updated kubernetesGroups, got %v", entry["kubernetesGroups"])
	}

	describeResp := eksCall(t, http.MethodGet, srv.URL+"/clusters/access-entry-update-cluster/access-entries/"+encoded, nil)
	if describeResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for describe after update, got %d", describeResp.StatusCode)
	}
	describeBody := decodeBody(t, describeResp)
	describeEntry, _ := describeBody["accessEntry"].(map[string]any)
	if describeEntry["username"] != "updated-user" {
		t.Fatalf("expected persisted updated username, got %v", describeEntry["username"])
	}

	missingEntry := eksCall(t, http.MethodPost, srv.URL+"/clusters/access-entry-update-cluster/access-entries/"+url.PathEscape("arn:aws:iam::000000000000:role/missing"), map[string]any{
		"username": "noop",
	})
	_ = expectResourceNotFound(t, missingEntry)

	missingCluster := eksCall(t, http.MethodPost, srv.URL+"/clusters/no-cluster/access-entries/"+encoded, map[string]any{
		"username": "noop",
	})
	_ = expectResourceNotFound(t, missingCluster)
}

func TestEKSAssociateAndListAssociatedAccessPolicies(t *testing.T) {
	srv := newEKSServer(t)

	_ = mustCreateCluster(t, srv.URL, "access-policy-cluster", nil)

	principal := "arn:aws:iam::000000000000:role/access-policy-principal"
	_ = mustCreateAccessEntry(t, srv.URL, "access-policy-cluster", principal, nil)

	encodedPrincipal := url.PathEscape(principal)
	assoc := mustAssociateAccessPolicy(t, srv.URL, "access-policy-cluster", principal, map[string]any{
		"policyArn": "arn:aws:eks::aws:cluster-access-policy/AmazonEKSViewPolicy",
		"accessScope": map[string]any{
			"type":       "namespace",
			"namespaces": []string{"team-a", "team-b"},
		},
	})
	if assoc["policyArn"] != "arn:aws:eks::aws:cluster-access-policy/AmazonEKSViewPolicy" {
		t.Fatalf("expected policyArn in association response, got %v", assoc["policyArn"])
	}

	listResp := eksCall(t, http.MethodGet, srv.URL+"/clusters/access-policy-cluster/access-entries/"+encodedPrincipal+"/access-policies", nil)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for list associated access policies, got %d", listResp.StatusCode)
	}
	listBody := decodeBody(t, listResp)
	items, _ := listBody["associatedAccessPolicies"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected one associated access policy, got %v", items)
	}
	item, _ := items[0].(map[string]any)
	if item["policyArn"] != "arn:aws:eks::aws:cluster-access-policy/AmazonEKSViewPolicy" {
		t.Fatalf("expected listed policy ARN, got %v", item["policyArn"])
	}

	dupResp := eksCall(t, http.MethodPost, srv.URL+"/clusters/access-policy-cluster/access-entries/"+encodedPrincipal+"/access-policies", map[string]any{
		"policyArn": "arn:aws:eks::aws:cluster-access-policy/AmazonEKSViewPolicy",
	})
	if dupResp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for duplicate associated access policy, got %d", dupResp.StatusCode)
	}
	dupBody := decodeBody(t, dupResp)
	if dupBody["__type"] != "ResourceInUseException" {
		t.Fatalf("expected ResourceInUseException for duplicate associated access policy, got %#v", dupBody)
	}

	missingEntryResp := eksCall(t, http.MethodGet, srv.URL+"/clusters/access-policy-cluster/access-entries/"+url.PathEscape("arn:aws:iam::000000000000:role/missing")+"/access-policies", nil)
	_ = expectResourceNotFound(t, missingEntryResp)

	missingPolicy := eksCall(t, http.MethodPost, srv.URL+"/clusters/access-policy-cluster/access-entries/"+encodedPrincipal+"/access-policies", map[string]any{
		"accessScope": map[string]any{"type": "cluster"},
	})
	if missingPolicy.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for associate access policy missing policyArn, got %d", missingPolicy.StatusCode)
	}
	missingPolicyBody := decodeBody(t, missingPolicy)
	if missingPolicyBody["__type"] != "MissingParameter" {
		t.Fatalf("expected MissingParameter for associate access policy missing policyArn, got %#v", missingPolicyBody)
	}
}

func TestEKSDisassociateAccessPolicy(t *testing.T) {
	srv := newEKSServer(t)

	_ = mustCreateCluster(t, srv.URL, "access-policy-disassociate-cluster", nil)

	principal := "arn:aws:iam::000000000000:role/disassociate-principal"
	_ = mustCreateAccessEntry(t, srv.URL, "access-policy-disassociate-cluster", principal, nil)

	encodedPrincipal := url.PathEscape(principal)
	policyARN := "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy"
	_ = mustAssociateAccessPolicy(t, srv.URL, "access-policy-disassociate-cluster", principal, map[string]any{
		"policyArn": policyARN,
	})

	disassociateResp := eksCall(t, http.MethodDelete, srv.URL+"/clusters/access-policy-disassociate-cluster/access-entries/"+encodedPrincipal+"/access-policies/"+url.PathEscape(policyARN), nil)
	if disassociateResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for disassociate access policy, got %d", disassociateResp.StatusCode)
	}

	listResp := eksCall(t, http.MethodGet, srv.URL+"/clusters/access-policy-disassociate-cluster/access-entries/"+encodedPrincipal+"/access-policies", nil)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for list associated access policies, got %d", listResp.StatusCode)
	}
	listBody := decodeBody(t, listResp)
	items, _ := listBody["associatedAccessPolicies"].([]any)
	if len(items) != 0 {
		t.Fatalf("expected zero associated access policies after disassociate, got %v", items)
	}

	missingPolicyResp := eksCall(t, http.MethodDelete, srv.URL+"/clusters/access-policy-disassociate-cluster/access-entries/"+encodedPrincipal+"/access-policies/"+url.PathEscape(policyARN), nil)
	_ = expectResourceNotFound(t, missingPolicyResp)

	missingClusterResp := eksCall(t, http.MethodDelete, srv.URL+"/clusters/no-cluster/access-entries/"+encodedPrincipal+"/access-policies/"+url.PathEscape(policyARN), nil)
	_ = expectResourceNotFound(t, missingClusterResp)
}

func TestEKSListAccessPolicies(t *testing.T) {
	srv := newEKSServer(t)

	resp := eksCall(t, http.MethodGet, srv.URL+"/access-policies", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for list access policies, got %d", resp.StatusCode)
	}

	body := decodeBody(t, resp)
	items, _ := body["accessPolicies"].([]any)
	if len(items) < 2 {
		t.Fatalf("expected at least two synthetic access policies, got %v", items)
	}

	first, _ := items[0].(map[string]any)
	if first["name"] == "" {
		t.Fatalf("expected policy name in first item, got %v", first)
	}
	if first["arn"] == "" {
		t.Fatalf("expected policy arn in first item, got %v", first)
	}
}

func TestEKSDescribeAccessPolicy(t *testing.T) {
	srv := newEKSServer(t)

	resp := eksCall(t, http.MethodGet, srv.URL+"/access-policies/AmazonEKSViewPolicy", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for describe access policy, got %d", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	policy, _ := body["accessPolicy"].(map[string]any)
	if policy["name"] != "AmazonEKSViewPolicy" {
		t.Fatalf("expected policy name AmazonEKSViewPolicy, got %v", policy["name"])
	}
	if policy["arn"] != "arn:aws:eks::aws:cluster-access-policy/AmazonEKSViewPolicy" {
		t.Fatalf("expected policy ARN for view policy, got %v", policy["arn"])
	}

	missing := eksCall(t, http.MethodGet, srv.URL+"/access-policies/UnknownPolicy", nil)
	_ = expectResourceNotFound(t, missing)
}

func TestEKSCreateAccessEntryPersistsInlineTags(t *testing.T) {
	srv := newEKSServer(t)

	_ = mustCreateCluster(t, srv.URL, "access-entry-tags-cluster", nil)

	principal := "arn:aws:iam::000000000000:role/access-entry-tags"
	entry := mustCreateAccessEntry(t, srv.URL, "access-entry-tags-cluster", principal, map[string]any{
		"tags": map[string]string{
			"env":   "dev",
			"owner": "eks-team",
		},
	})
	createTags, _ := entry["tags"].(map[string]any)
	if len(createTags) != 2 || createTags["env"] != "dev" || createTags["owner"] != "eks-team" {
		t.Fatalf("expected inline tags in create access entry response, got %#v", entry["tags"])
	}

	describeResp := eksCall(t, http.MethodGet, srv.URL+"/clusters/access-entry-tags-cluster/access-entries/"+url.PathEscape(principal), nil)
	if describeResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for describe access entry, got %d", describeResp.StatusCode)
	}
	describeBody := decodeBody(t, describeResp)
	described, _ := describeBody["accessEntry"].(map[string]any)
	describeTags, _ := described["tags"].(map[string]any)
	if len(describeTags) != 2 || describeTags["env"] != "dev" || describeTags["owner"] != "eks-team" {
		t.Fatalf("expected inline tags in describe access entry response, got %#v", described["tags"])
	}
}

func TestEKSAccessEntryTagsDoNotLeakAcrossClusterDeleteRecreate(t *testing.T) {
	srv := newEKSServer(t)

	_ = mustCreateCluster(t, srv.URL, "access-entry-tags-cleanup-cluster", nil)

	principal := "arn:aws:iam::000000000000:role/access-entry-tags"
	_ = mustCreateAccessEntry(t, srv.URL, "access-entry-tags-cleanup-cluster", principal, map[string]any{
		"tags": map[string]string{
			"env": "dev",
		},
	})

	expectStatus(t, eksCall(t, http.MethodDelete, srv.URL+"/clusters/access-entry-tags-cleanup-cluster", nil), http.StatusOK)
	_ = mustCreateCluster(t, srv.URL, "access-entry-tags-cleanup-cluster", nil)

	recreated := mustCreateAccessEntry(t, srv.URL, "access-entry-tags-cleanup-cluster", principal, nil)
	tags, _ := recreated["tags"].(map[string]any)
	if len(tags) != 0 {
		t.Fatalf("expected no leaked inline tags after cluster delete/recreate, got %#v", recreated["tags"])
	}
}
