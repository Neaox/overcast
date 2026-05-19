package eks

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestLiveModeListNodegroupsBlocksMockRecord(t *testing.T) {
	svc := newLiveModeTestService()
	putLegacyMockCluster(t, svc)

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/clusters/"+legacyMockClusterName+"/node-groups", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeCreateNodegroupBlocksMockRecord(t *testing.T) {
	svc := newLiveModeTestService()
	putLegacyMockCluster(t, svc)

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	payload, _ := json.Marshal(map[string]any{
		"nodegroupName": "ng1",
		"nodeRole":      "arn:aws:iam::000000000000:role/ng-role",
		"version":       "1.31",
	})
	req := httptest.NewRequest(http.MethodPost, "/clusters/"+legacyMockClusterName+"/node-groups", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeListAccessEntriesBlocksMockRecord(t *testing.T) {
	svc := newLiveModeTestService()
	putLegacyMockCluster(t, svc)

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/clusters/"+legacyMockClusterName+"/access-entries", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeCreateAccessEntryBlocksMockRecord(t *testing.T) {
	svc := newLiveModeTestService()
	putLegacyMockCluster(t, svc)

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	payload, _ := json.Marshal(map[string]any{
		"principalArn": "arn:aws:iam::000000000000:user/dev",
	})
	req := httptest.NewRequest(http.MethodPost, "/clusters/"+legacyMockClusterName+"/access-entries", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeListIdentityProviderConfigsBlocksMockRecord(t *testing.T) {
	svc := newLiveModeTestService()
	putLegacyMockCluster(t, svc)

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/clusters/"+legacyMockClusterName+"/identity-provider-configs", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeCreatePodIdentityAssociationBlocksMockRecord(t *testing.T) {
	svc := newLiveModeTestService()
	putLegacyMockCluster(t, svc)

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	payload, _ := json.Marshal(map[string]any{
		"namespace":      "default",
		"serviceAccount": "sa",
		"roleArn":        "arn:aws:iam::000000000000:role/pod-role",
	})
	req := httptest.NewRequest(http.MethodPost, "/clusters/"+legacyMockClusterName+"/pod-identity-associations", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeListFargateProfilesBlocksMockRecord(t *testing.T) {
	svc := newLiveModeTestService()
	putLegacyMockCluster(t, svc)

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/clusters/"+legacyMockClusterName+"/fargate-profiles", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeCreateAddonBlocksMockRecord(t *testing.T) {
	svc := newLiveModeTestService()
	putLegacyMockCluster(t, svc)

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	payload, _ := json.Marshal(map[string]any{"addonName": "coredns"})
	req := httptest.NewRequest(http.MethodPost, "/clusters/"+legacyMockClusterName+"/addons", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeNodegroupLifecycleAllowsLiveRecord(t *testing.T) {
	svc := newLiveModeTestService()
	putLiveClusterRecord(t, svc, "live-nodegroups")

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	createPayload, _ := json.Marshal(map[string]any{
		"nodegroupName": "ng-a",
		"nodeRole":      "arn:aws:iam::000000000000:role/ng-role",
		"subnets":       []string{"subnet-1"},
		"instanceTypes": []string{"m5.large"},
	})
	createReq := httptest.NewRequest(http.MethodPost, "/clusters/live-nodegroups/node-groups", bytes.NewReader(createPayload))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating nodegroup on live record, got %d body=%s", createRec.Code, createRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/clusters/live-nodegroups/node-groups", nil)
	listRec := httptest.NewRecorder()
	r.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200 listing nodegroups on live record, got %d body=%s", listRec.Code, listRec.Body.String())
	}
	var listBody map[string]any
	if err := json.Unmarshal(listRec.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode list nodegroups response: %v", err)
	}
	namesRaw, _ := listBody["nodegroups"].([]any)
	names := make([]string, 0, len(namesRaw))
	for _, n := range namesRaw {
		if s, ok := n.(string); ok {
			names = append(names, s)
		}
	}
	sort.Strings(names)
	if len(names) != 1 || names[0] != "ng-a" {
		t.Fatalf("unexpected nodegroups list: %#v", listBody["nodegroups"])
	}

	describeReq := httptest.NewRequest(http.MethodGet, "/clusters/live-nodegroups/node-groups/ng-a", nil)
	describeRec := httptest.NewRecorder()
	r.ServeHTTP(describeRec, describeReq)
	if describeRec.Code != http.StatusOK {
		t.Fatalf("expected 200 describing nodegroup on live record, got %d body=%s", describeRec.Code, describeRec.Body.String())
	}

	updateCfgPayload, _ := json.Marshal(map[string]any{
		"labels": map[string]string{"env": "dev"},
	})
	updateCfgReq := httptest.NewRequest(http.MethodPost, "/clusters/live-nodegroups/node-groups/ng-a/update-config", bytes.NewReader(updateCfgPayload))
	updateCfgReq.Header.Set("Content-Type", "application/json")
	updateCfgRec := httptest.NewRecorder()
	r.ServeHTTP(updateCfgRec, updateCfgReq)
	if updateCfgRec.Code != http.StatusOK {
		t.Fatalf("expected 200 updating nodegroup config on live record, got %d body=%s", updateCfgRec.Code, updateCfgRec.Body.String())
	}

	updateVerPayload, _ := json.Marshal(map[string]any{"version": "1.32"})
	updateVerReq := httptest.NewRequest(http.MethodPost, "/clusters/live-nodegroups/node-groups/ng-a/updates", bytes.NewReader(updateVerPayload))
	updateVerReq.Header.Set("Content-Type", "application/json")
	updateVerRec := httptest.NewRecorder()
	r.ServeHTTP(updateVerRec, updateVerReq)
	if updateVerRec.Code != http.StatusOK {
		t.Fatalf("expected 200 updating nodegroup version on live record, got %d body=%s", updateVerRec.Code, updateVerRec.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/clusters/live-nodegroups/node-groups/ng-a", nil)
	deleteRec := httptest.NewRecorder()
	r.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("expected 200 deleting nodegroup on live record, got %d body=%s", deleteRec.Code, deleteRec.Body.String())
	}
}

func TestLiveModeAddonLifecycleAllowsLiveRecord(t *testing.T) {
	svc := newLiveModeTestService()
	putLiveClusterRecord(t, svc, "live-addons")

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	createPayload, _ := json.Marshal(map[string]any{
		"addonName":           "coredns",
		"addonVersion":        "v1.11.1-eksbuild.9",
		"configurationValues": "{\"replicaCount\":2}",
	})
	createReq := httptest.NewRequest(http.MethodPost, "/clusters/live-addons/addons", bytes.NewReader(createPayload))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating addon on live record, got %d body=%s", createRec.Code, createRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/clusters/live-addons/addons", nil)
	listRec := httptest.NewRecorder()
	r.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200 listing addons on live record, got %d body=%s", listRec.Code, listRec.Body.String())
	}
	var listBody map[string]any
	if err := json.Unmarshal(listRec.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode list addons response: %v", err)
	}
	addonsRaw, _ := listBody["addons"].([]any)
	addons := make([]string, 0, len(addonsRaw))
	for _, a := range addonsRaw {
		if s, ok := a.(string); ok {
			addons = append(addons, s)
		}
	}
	sort.Strings(addons)
	if len(addons) != 1 || addons[0] != "coredns" {
		t.Fatalf("unexpected addons list: %#v", listBody["addons"])
	}

	describeReq := httptest.NewRequest(http.MethodGet, "/clusters/live-addons/addons/coredns", nil)
	describeRec := httptest.NewRecorder()
	r.ServeHTTP(describeRec, describeReq)
	if describeRec.Code != http.StatusOK {
		t.Fatalf("expected 200 describing addon on live record, got %d body=%s", describeRec.Code, describeRec.Body.String())
	}

	updatePayload, _ := json.Marshal(map[string]any{
		"addonVersion": "v1.11.2-eksbuild.1",
	})
	updateReq := httptest.NewRequest(http.MethodPost, "/clusters/live-addons/addons/coredns/updates", bytes.NewReader(updatePayload))
	updateReq.Header.Set("Content-Type", "application/json")
	updateRec := httptest.NewRecorder()
	r.ServeHTTP(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("expected 200 updating addon on live record, got %d body=%s", updateRec.Code, updateRec.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/clusters/live-addons/addons/coredns", nil)
	deleteRec := httptest.NewRecorder()
	r.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("expected 200 deleting addon on live record, got %d body=%s", deleteRec.Code, deleteRec.Body.String())
	}
}

func TestLiveModeFargateProfileLifecycleAllowsLiveRecord(t *testing.T) {
	svc := newLiveModeTestService()
	putLiveClusterRecord(t, svc, "live-fargate")

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	createPayload, _ := json.Marshal(map[string]any{
		"fargateProfileName":  "workloads",
		"podExecutionRoleArn": "arn:aws:iam::000000000000:role/eks-fargate-role",
		"subnets":             []string{"subnet-1", "subnet-2"},
		"selectors": []map[string]any{
			{"namespace": "apps"},
		},
	})
	createReq := httptest.NewRequest(http.MethodPost, "/clusters/live-fargate/fargate-profiles", bytes.NewReader(createPayload))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating fargate profile on live record, got %d body=%s", createRec.Code, createRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/clusters/live-fargate/fargate-profiles", nil)
	listRec := httptest.NewRecorder()
	r.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200 listing fargate profiles on live record, got %d body=%s", listRec.Code, listRec.Body.String())
	}
	var listBody map[string]any
	if err := json.Unmarshal(listRec.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode list fargate profiles response: %v", err)
	}
	namesRaw, _ := listBody["fargateProfileNames"].([]any)
	names := make([]string, 0, len(namesRaw))
	for _, n := range namesRaw {
		if s, ok := n.(string); ok {
			names = append(names, s)
		}
	}
	sort.Strings(names)
	if len(names) != 2 || names[0] != "default" || names[1] != "workloads" {
		t.Fatalf("unexpected fargate profile names: %#v", listBody["fargateProfileNames"])
	}

	describeReq := httptest.NewRequest(http.MethodGet, "/clusters/live-fargate/fargate-profiles/workloads", nil)
	describeRec := httptest.NewRecorder()
	r.ServeHTTP(describeRec, describeReq)
	if describeRec.Code != http.StatusOK {
		t.Fatalf("expected 200 describing fargate profile on live record, got %d body=%s", describeRec.Code, describeRec.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/clusters/live-fargate/fargate-profiles/workloads", nil)
	deleteRec := httptest.NewRecorder()
	r.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("expected 200 deleting fargate profile on live record, got %d body=%s", deleteRec.Code, deleteRec.Body.String())
	}
}

func TestLiveModeAccessEntryPolicyLifecycleAllowsLiveRecord(t *testing.T) {
	svc := newLiveModeTestService()
	putLiveClusterRecord(t, svc, "live-access")

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	principalArn := "arn:aws:iam::000000000000:role/dev-admin"
	escapedPrincipal := url.PathEscape(principalArn)

	createPayload, _ := json.Marshal(map[string]any{
		"principalArn":     principalArn,
		"type":             "STANDARD",
		"username":         "dev-admin",
		"kubernetesGroups": []string{"system:masters"},
	})
	createReq := httptest.NewRequest(http.MethodPost, "/clusters/live-access/access-entries", bytes.NewReader(createPayload))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating access entry on live record, got %d body=%s", createRec.Code, createRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/clusters/live-access/access-entries", nil)
	listRec := httptest.NewRecorder()
	r.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200 listing access entries on live record, got %d body=%s", listRec.Code, listRec.Body.String())
	}
	var listBody map[string]any
	if err := json.Unmarshal(listRec.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode list access entries response: %v", err)
	}
	entriesRaw, _ := listBody["accessEntries"].([]any)
	if len(entriesRaw) != 1 || entriesRaw[0] != principalArn {
		t.Fatalf("unexpected access entries list: %#v", listBody["accessEntries"])
	}

	describeReq := httptest.NewRequest(http.MethodGet, "/clusters/live-access/access-entries/"+escapedPrincipal, nil)
	describeRec := httptest.NewRecorder()
	r.ServeHTTP(describeRec, describeReq)
	if describeRec.Code != http.StatusOK {
		t.Fatalf("expected 200 describing access entry on live record, got %d body=%s", describeRec.Code, describeRec.Body.String())
	}

	updatePayload, _ := json.Marshal(map[string]any{
		"username": "platform-admin",
	})
	updateReq := httptest.NewRequest(http.MethodPost, "/clusters/live-access/access-entries/"+escapedPrincipal, bytes.NewReader(updatePayload))
	updateReq.Header.Set("Content-Type", "application/json")
	updateRec := httptest.NewRecorder()
	r.ServeHTTP(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("expected 200 updating access entry on live record, got %d body=%s", updateRec.Code, updateRec.Body.String())
	}

	policyArn := "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy"
	escapedPolicy := url.PathEscape(policyArn)
	associatePayload, _ := json.Marshal(map[string]any{
		"policyArn": policyArn,
	})
	associateReq := httptest.NewRequest(http.MethodPost, "/clusters/live-access/access-entries/"+escapedPrincipal+"/access-policies", bytes.NewReader(associatePayload))
	associateReq.Header.Set("Content-Type", "application/json")
	associateRec := httptest.NewRecorder()
	r.ServeHTTP(associateRec, associateReq)
	if associateRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 associating access policy on live record, got %d body=%s", associateRec.Code, associateRec.Body.String())
	}

	listPoliciesReq := httptest.NewRequest(http.MethodGet, "/clusters/live-access/access-entries/"+escapedPrincipal+"/access-policies", nil)
	listPoliciesRec := httptest.NewRecorder()
	r.ServeHTTP(listPoliciesRec, listPoliciesReq)
	if listPoliciesRec.Code != http.StatusOK {
		t.Fatalf("expected 200 listing associated access policies on live record, got %d body=%s", listPoliciesRec.Code, listPoliciesRec.Body.String())
	}

	disassociateReq := httptest.NewRequest(http.MethodDelete, "/clusters/live-access/access-entries/"+escapedPrincipal+"/access-policies/"+escapedPolicy, nil)
	disassociateRec := httptest.NewRecorder()
	r.ServeHTTP(disassociateRec, disassociateReq)
	if disassociateRec.Code != http.StatusOK {
		t.Fatalf("expected 200 disassociating access policy on live record, got %d body=%s", disassociateRec.Code, disassociateRec.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/clusters/live-access/access-entries/"+escapedPrincipal, nil)
	deleteRec := httptest.NewRecorder()
	r.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("expected 200 deleting access entry on live record, got %d body=%s", deleteRec.Code, deleteRec.Body.String())
	}
}

func TestLiveModeIdentityProviderLifecycleAllowsLiveRecord(t *testing.T) {
	svc := newLiveModeTestService()
	putLiveClusterRecord(t, svc, "live-idp")

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	listReq := httptest.NewRequest(http.MethodGet, "/clusters/live-idp/identity-provider-configs", nil)
	listRec := httptest.NewRecorder()
	r.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200 listing identity provider configs on live record, got %d body=%s", listRec.Code, listRec.Body.String())
	}

	associatePayload, _ := json.Marshal(map[string]any{
		"oidc": map[string]any{
			"identityProviderConfigName": "corp-oidc",
			"issuerUrl":                  "https://idp.example.com",
			"clientId":                   "kube-client",
		},
	})
	associateReq := httptest.NewRequest(http.MethodPost, "/clusters/live-idp/identity-provider-configs/associate", bytes.NewReader(associatePayload))
	associateReq.Header.Set("Content-Type", "application/json")
	associateRec := httptest.NewRecorder()
	r.ServeHTTP(associateRec, associateReq)
	if associateRec.Code != http.StatusOK {
		t.Fatalf("expected 200 associating identity provider config on live record, got %d body=%s", associateRec.Code, associateRec.Body.String())
	}

	listAfterReq := httptest.NewRequest(http.MethodGet, "/clusters/live-idp/identity-provider-configs", nil)
	listAfterRec := httptest.NewRecorder()
	r.ServeHTTP(listAfterRec, listAfterReq)
	if listAfterRec.Code != http.StatusOK {
		t.Fatalf("expected 200 listing identity provider configs after associate, got %d body=%s", listAfterRec.Code, listAfterRec.Body.String())
	}
	var listBody map[string]any
	if err := json.Unmarshal(listAfterRec.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode list identity provider configs response: %v", err)
	}
	items, _ := listBody["identityProviderConfigs"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected one identity provider config after associate, got %#v", listBody["identityProviderConfigs"])
	}

	describeReq := httptest.NewRequest(http.MethodGet, "/clusters/live-idp/identity-provider-configs/oidc/corp-oidc", nil)
	describeRec := httptest.NewRecorder()
	r.ServeHTTP(describeRec, describeReq)
	if describeRec.Code != http.StatusOK {
		t.Fatalf("expected 200 describing identity provider config on live record, got %d body=%s", describeRec.Code, describeRec.Body.String())
	}

	updatePayload, _ := json.Marshal(map[string]any{
		"oidc": map[string]any{
			"groupsClaim": "groups",
		},
	})
	updateReq := httptest.NewRequest(http.MethodPost, "/clusters/live-idp/identity-provider-configs/oidc/corp-oidc/update", bytes.NewReader(updatePayload))
	updateReq.Header.Set("Content-Type", "application/json")
	updateRec := httptest.NewRecorder()
	r.ServeHTTP(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("expected 200 updating identity provider config on live record, got %d body=%s", updateRec.Code, updateRec.Body.String())
	}

	disassociatePayload, _ := json.Marshal(map[string]any{
		"identityProviderConfig": map[string]any{
			"type": "oidc",
			"name": "corp-oidc",
		},
	})
	disassociateReq := httptest.NewRequest(http.MethodPost, "/clusters/live-idp/identity-provider-configs/disassociate", bytes.NewReader(disassociatePayload))
	disassociateReq.Header.Set("Content-Type", "application/json")
	disassociateRec := httptest.NewRecorder()
	r.ServeHTTP(disassociateRec, disassociateReq)
	if disassociateRec.Code != http.StatusOK {
		t.Fatalf("expected 200 disassociating identity provider config on live record, got %d body=%s", disassociateRec.Code, disassociateRec.Body.String())
	}
}

func TestLiveModePodIdentityAssociationLifecycleAllowsLiveRecord(t *testing.T) {
	svc := newLiveModeTestService()
	putLiveClusterRecord(t, svc, "live-pod-identity")

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	listReq := httptest.NewRequest(http.MethodGet, "/clusters/live-pod-identity/pod-identity-associations", nil)
	listRec := httptest.NewRecorder()
	r.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200 listing pod identity associations on live record, got %d body=%s", listRec.Code, listRec.Body.String())
	}

	createPayload, _ := json.Marshal(map[string]any{
		"namespace":      "apps",
		"serviceAccount": "backend",
		"roleArn":        "arn:aws:iam::000000000000:role/pod-role",
	})
	createReq := httptest.NewRequest(http.MethodPost, "/clusters/live-pod-identity/pod-identity-associations", bytes.NewReader(createPayload))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating pod identity association on live record, got %d body=%s", createRec.Code, createRec.Body.String())
	}

	var createBody map[string]any
	if err := json.Unmarshal(createRec.Body.Bytes(), &createBody); err != nil {
		t.Fatalf("decode create pod identity response: %v", err)
	}
	assoc, _ := createBody["association"].(map[string]any)
	associationID, _ := assoc["associationId"].(string)
	if associationID == "" {
		t.Fatalf("expected associationId in create response, got %#v", createBody)
	}

	describeReq := httptest.NewRequest(http.MethodGet, "/clusters/live-pod-identity/pod-identity-associations/"+associationID, nil)
	describeRec := httptest.NewRecorder()
	r.ServeHTTP(describeRec, describeReq)
	if describeRec.Code != http.StatusOK {
		t.Fatalf("expected 200 describing pod identity association on live record, got %d body=%s", describeRec.Code, describeRec.Body.String())
	}

	updatePayload, _ := json.Marshal(map[string]any{
		"roleArn": "arn:aws:iam::000000000000:role/pod-role-updated",
	})
	updateReq := httptest.NewRequest(http.MethodPost, "/clusters/live-pod-identity/pod-identity-associations/"+associationID, bytes.NewReader(updatePayload))
	updateReq.Header.Set("Content-Type", "application/json")
	updateRec := httptest.NewRecorder()
	r.ServeHTTP(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("expected 200 updating pod identity association on live record, got %d body=%s", updateRec.Code, updateRec.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/clusters/live-pod-identity/pod-identity-associations/"+associationID, nil)
	deleteRec := httptest.NewRecorder()
	r.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("expected 200 deleting pod identity association on live record, got %d body=%s", deleteRec.Code, deleteRec.Body.String())
	}
}
