package eks_test

import (
	"net/http"
	"testing"
)

func TestEKSListPodIdentityAssociations(t *testing.T) {
	srv := newEKSServer(t)

	_ = mustCreateCluster(t, srv.URL, "pod-identity-cluster", nil)

	listBody := expectJSONStatus(t, eksCall(t, http.MethodGet, srv.URL+"/clusters/pod-identity-cluster/pod-identity-associations", nil), http.StatusOK)
	items, _ := listBody["associations"].([]any)
	if len(items) != 0 {
		t.Fatalf("expected empty associations list, got %v", items)
	}

	_ = expectResourceNotFound(t, eksCall(t, http.MethodGet, srv.URL+"/clusters/no-cluster/pod-identity-associations", nil))
}

func TestEKSPodIdentityAssociationLifecycle(t *testing.T) {
	srv := newEKSServer(t)

	_ = mustCreateCluster(t, srv.URL, "pod-identity-lifecycle-cluster", nil)

	assoc := mustCreatePodIdentityAssociation(t, srv.URL, "pod-identity-lifecycle-cluster", map[string]any{
		"namespace":      "default",
		"serviceAccount": "app-sa",
		"roleArn":        "arn:aws:iam::000000000000:role/pod-identity-role",
	})
	associationID, _ := assoc["associationId"].(string)
	if associationID == "" {
		t.Fatalf("expected non-empty associationId in create response, got %v", assoc)
	}

	listBody := expectJSONStatus(t, eksCall(t, http.MethodGet, srv.URL+"/clusters/pod-identity-lifecycle-cluster/pod-identity-associations", nil), http.StatusOK)
	items, _ := listBody["associations"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected one pod identity association after create, got %v", items)
	}

	describeBody := expectJSONStatus(t, eksCall(t, http.MethodGet, srv.URL+"/clusters/pod-identity-lifecycle-cluster/pod-identity-associations/"+associationID, nil), http.StatusOK)
	described, _ := describeBody["association"].(map[string]any)
	if described["associationId"] != associationID {
		t.Fatalf("expected associationId %q in describe response, got %v", associationID, described["associationId"])
	}

	expectStatus(t, eksCall(t, http.MethodDelete, srv.URL+"/clusters/pod-identity-lifecycle-cluster/pod-identity-associations/"+associationID, nil), http.StatusOK)

	afterBody := expectJSONStatus(t, eksCall(t, http.MethodGet, srv.URL+"/clusters/pod-identity-lifecycle-cluster/pod-identity-associations", nil), http.StatusOK)
	afterItems, _ := afterBody["associations"].([]any)
	if len(afterItems) != 0 {
		t.Fatalf("expected no pod identity associations after delete, got %v", afterItems)
	}

	_ = expectResourceNotFound(t, eksCall(t, http.MethodGet, srv.URL+"/clusters/pod-identity-lifecycle-cluster/pod-identity-associations/"+associationID, nil))
}

func TestEKSUpdatePodIdentityAssociation(t *testing.T) {
	srv := newEKSServer(t)

	_ = mustCreateCluster(t, srv.URL, "pod-identity-update-cluster", nil)

	assoc := mustCreatePodIdentityAssociation(t, srv.URL, "pod-identity-update-cluster", map[string]any{
		"namespace":      "default",
		"serviceAccount": "app-sa",
		"roleArn":        "arn:aws:iam::000000000000:role/pod-identity-role-a",
	})
	associationID, _ := assoc["associationId"].(string)
	if associationID == "" {
		t.Fatalf("expected non-empty associationId in create response, got %v", assoc)
	}

	updateBody := expectJSONStatus(t, eksCall(t, http.MethodPost, srv.URL+"/clusters/pod-identity-update-cluster/pod-identity-associations/"+associationID, map[string]any{
		"roleArn": "arn:aws:iam::000000000000:role/pod-identity-role-b",
	}), http.StatusOK)
	updated, _ := updateBody["association"].(map[string]any)
	if updated["roleArn"] != "arn:aws:iam::000000000000:role/pod-identity-role-b" {
		t.Fatalf("expected updated roleArn in update response, got %v", updated["roleArn"])
	}

	describeBody := expectJSONStatus(t, eksCall(t, http.MethodGet, srv.URL+"/clusters/pod-identity-update-cluster/pod-identity-associations/"+associationID, nil), http.StatusOK)
	described, _ := describeBody["association"].(map[string]any)
	if described["roleArn"] != "arn:aws:iam::000000000000:role/pod-identity-role-b" {
		t.Fatalf("expected persisted updated roleArn, got %v", described["roleArn"])
	}

	_ = expectResourceNotFound(t, eksCall(t, http.MethodPost, srv.URL+"/clusters/pod-identity-update-cluster/pod-identity-associations/missing-id", map[string]any{
		"roleArn": "arn:aws:iam::000000000000:role/nope",
	}))
}

func TestEKSCreatePodIdentityAssociationRejectsDuplicateServiceAccountBinding(t *testing.T) {
	srv := newEKSServer(t)

	_ = mustCreateCluster(t, srv.URL, "pod-identity-dup-cluster", nil)

	_ = mustCreatePodIdentityAssociation(t, srv.URL, "pod-identity-dup-cluster", map[string]any{
		"namespace":      "default",
		"serviceAccount": "shared-sa",
		"roleArn":        "arn:aws:iam::000000000000:role/pod-identity-role-a",
	})

	dupBody := expectJSONStatus(t, eksCall(t, http.MethodPost, srv.URL+"/clusters/pod-identity-dup-cluster/pod-identity-associations", map[string]any{
		"namespace":      "default",
		"serviceAccount": "shared-sa",
		"roleArn":        "arn:aws:iam::000000000000:role/pod-identity-role-b",
	}), http.StatusConflict)
	if dupBody["__type"] != "ResourceInUseException" {
		t.Fatalf("expected ResourceInUseException for duplicate pod identity association, got %#v", dupBody)
	}
}

func TestEKSDeleteClusterClearsPodIdentityAssociations(t *testing.T) {
	srv := newEKSServer(t)

	_ = mustCreateCluster(t, srv.URL, "pod-identity-cleanup-cluster", nil)

	_ = mustCreatePodIdentityAssociation(t, srv.URL, "pod-identity-cleanup-cluster", map[string]any{
		"namespace":      "default",
		"serviceAccount": "cleanup-sa",
		"roleArn":        "arn:aws:iam::000000000000:role/pod-identity-cleanup-role",
	})

	beforeBody := expectJSONStatus(t, eksCall(t, http.MethodGet, srv.URL+"/clusters/pod-identity-cleanup-cluster/pod-identity-associations", nil), http.StatusOK)
	beforeItems, _ := beforeBody["associations"].([]any)
	if len(beforeItems) != 1 {
		t.Fatalf("expected one association before cluster delete, got %v", beforeItems)
	}

	expectStatus(t, eksCall(t, http.MethodDelete, srv.URL+"/clusters/pod-identity-cleanup-cluster", nil), http.StatusOK)
	_ = mustCreateCluster(t, srv.URL, "pod-identity-cleanup-cluster", nil)

	afterBody := expectJSONStatus(t, eksCall(t, http.MethodGet, srv.URL+"/clusters/pod-identity-cleanup-cluster/pod-identity-associations", nil), http.StatusOK)
	afterItems, _ := afterBody["associations"].([]any)
	if len(afterItems) != 0 {
		t.Fatalf("expected no retained pod identity associations after cluster delete, got %v", afterItems)
	}
}

func TestEKSCreatePodIdentityAssociationPersistsInlineTags(t *testing.T) {
	srv := newEKSServer(t)

	_ = mustCreateCluster(t, srv.URL, "pod-identity-tags-cluster", nil)

	association := mustCreatePodIdentityAssociation(t, srv.URL, "pod-identity-tags-cluster", map[string]any{
		"namespace":      "default",
		"serviceAccount": "tags-sa",
		"roleArn":        "arn:aws:iam::000000000000:role/pod-identity-tags-role",
		"tags": map[string]string{
			"env":   "dev",
			"owner": "eks-team",
		},
	})
	associationID, _ := association["associationId"].(string)
	if associationID == "" {
		t.Fatalf("expected non-empty associationId in create response, got %v", association)
	}
	createdTags, _ := association["tags"].(map[string]any)
	if len(createdTags) != 2 || createdTags["env"] != "dev" || createdTags["owner"] != "eks-team" {
		t.Fatalf("expected inline tags in create response, got %#v", association["tags"])
	}

	describeBody := expectJSONStatus(t, eksCall(t, http.MethodGet, srv.URL+"/clusters/pod-identity-tags-cluster/pod-identity-associations/"+associationID, nil), http.StatusOK)
	described, _ := describeBody["association"].(map[string]any)
	describedTags, _ := described["tags"].(map[string]any)
	if len(describedTags) != 2 || describedTags["env"] != "dev" || describedTags["owner"] != "eks-team" {
		t.Fatalf("expected inline tags in describe response, got %#v", described["tags"])
	}
}
