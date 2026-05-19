package eks

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

func TestLiveModeCreateClusterPersistsBootstrapRecord(t *testing.T) {
	cfg := &config.Config{
		Region:          "us-east-1",
		AccountID:       "000000000000",
		EKSMode:         config.EKSModeLive,
		EKSDockerSocket: "/var/run/docker.sock",
		EKSNetwork:      "overcast_eks",
	}
	// Use an unreachable TCP endpoint so Docker calls fail fast without side-effects.
	service := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())
	service.SetDocker(docker.NewClient("tcp://127.0.0.1:1", zap.NewNop()))

	r := chi.NewRouter()
	service.RegisterRoutes(r)

	payload, err := json.Marshal(map[string]any{
		"name":    "live-unit-cluster",
		"roleArn": "arn:aws:iam::000000000000:role/eks-role",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	createReq := httptest.NewRequest(http.MethodPost, "/clusters", bytes.NewReader(payload))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for live-mode create, got %d body=%s", createRec.Code, createRec.Body.String())
	}

	var createBody map[string]any
	if err := json.Unmarshal(createRec.Body.Bytes(), &createBody); err != nil {
		t.Fatalf("decode create body: %v", err)
	}
	cluster, _ := createBody["cluster"].(map[string]any)
	if cluster["status"] != "CREATING" {
		t.Fatalf("expected live-mode cluster status CREATING, got %v", cluster["status"])
	}
	if endpoint, _ := cluster["endpoint"].(string); endpoint != "" {
		t.Fatalf("expected live-mode cluster endpoint to be empty before bootstrap completes, got %q", endpoint)
	}

	describeReq := httptest.NewRequest(http.MethodGet, "/clusters/live-unit-cluster", nil)
	describeRec := httptest.NewRecorder()
	r.ServeHTTP(describeRec, describeReq)
	if describeRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for describe of live bootstrap record, got %d body=%s", describeRec.Code, describeRec.Body.String())
	}

	var describeBody map[string]any
	if err := json.Unmarshal(describeRec.Body.Bytes(), &describeBody); err != nil {
		t.Fatalf("decode describe body: %v", err)
	}
	described, _ := describeBody["cluster"].(map[string]any)
	if described["status"] != "CREATING" {
		t.Fatalf("expected described live-mode cluster status CREATING, got %v", described["status"])
	}
	if endpoint, _ := described["endpoint"].(string); endpoint != "" {
		t.Fatalf("expected described live-mode cluster endpoint to be empty before bootstrap completes, got %q", endpoint)
	}

	runtime, found := service.getLiveClusterRuntime("us-east-1", "live-unit-cluster")
	if !found {
		t.Fatal("expected live runtime registry entry after create")
	}
	if runtime.containerID != "" {
		t.Fatalf("expected bootstrap runtime to have no container id yet, got %q", runtime.containerID)
	}
}

func TestLiveModeDeleteClusterClearsRuntimeRegistry(t *testing.T) {
	cfg := &config.Config{
		Region:          "us-east-1",
		AccountID:       "000000000000",
		EKSMode:         config.EKSModeLive,
		EKSDockerSocket: "/var/run/docker.sock",
		EKSNetwork:      "overcast_eks",
	}
	// Use an unreachable TCP endpoint so Docker calls fail fast without side-effects.
	service := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())
	service.SetDocker(docker.NewClient("tcp://127.0.0.1:1", zap.NewNop()))

	r := chi.NewRouter()
	service.RegisterRoutes(r)

	payload, err := json.Marshal(map[string]any{
		"name":    "live-delete-cluster",
		"roleArn": "arn:aws:iam::000000000000:role/eks-role",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	createReq := httptest.NewRequest(http.MethodPost, "/clusters", bytes.NewReader(payload))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for live-mode create, got %d body=%s", createRec.Code, createRec.Body.String())
	}

	if _, found := service.getLiveClusterRuntime("us-east-1", "live-delete-cluster"); !found {
		t.Fatal("expected live runtime registry entry after create")
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/clusters/live-delete-cluster", nil)
	deleteRec := httptest.NewRecorder()
	r.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for live-mode delete, got %d body=%s", deleteRec.Code, deleteRec.Body.String())
	}

	if _, found := service.getLiveClusterRuntime("us-east-1", "live-delete-cluster"); found {
		t.Fatal("expected live runtime registry entry to be cleared on delete")
	}
}

func TestDeleteClusterClearsClusterScopedArtifacts(t *testing.T) {
	const (
		region      = "us-east-1"
		clusterName = "cleanup-cluster"
		otherName   = "other-cluster"
	)

	svc := New(
		&config.Config{Region: region, AccountID: "000000000000", EKSMode: config.EKSModeMock},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	ctx := context.Background()
	clusterArn := svc.clusterARN(region, clusterName)
	if err := svc.putCluster(ctx, region, &Cluster{
		Name:      clusterName,
		Arn:       clusterArn,
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://cleanup-cluster.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("put cleanup cluster: %v", err)
	}
	if err := svc.putCluster(ctx, region, &Cluster{
		Name:      otherName,
		Arn:       svc.clusterARN(region, otherName),
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://other-cluster.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("put other cluster: %v", err)
	}

	seed := func(ns, key string) {
		t.Helper()
		if err := svc.store.Set(ctx, ns, key, "{}"); err != nil {
			t.Fatalf("seed %s/%s: %v", ns, key, err)
		}
	}

	principalArn := "arn:aws:iam::000000000000:user/dev"
	policyArn := "arn:aws:eks::aws:cluster-access-policy/AmazonEKSAdminPolicy"
	nodeARN := svc.nodegroupARN(region, clusterName, "ng1")
	fargateARN := svc.fargateProfileARN(region, clusterName, "fp1")
	addonARN := svc.addonARN(region, clusterName, "coredns")

	seed(nsNodegroups, nodegroupKey(region, clusterName, "ng1"))
	seed(nsUpdates, updateKey(region, clusterName, "upd-1"))
	seed(nsFargate, fargateProfileKey(region, clusterName, "fp1"))
	seed(nsAddons, addonKey(region, clusterName, "coredns"))
	seed(nsIDPConfigs, idpConfigKey(region, clusterName, "oidc", "cfg-1"))
	seed(nsAccess, accessEntryKey(region, clusterName, principalArn))
	seed(nsAccessPol, associatedAccessPolicyKey(region, clusterName, principalArn, policyArn))
	seed(nsPodIDAssoc, podIdentityAssociationKey(region, clusterName, "assoc-1"))

	seed(nsTags, tagKey(clusterArn))
	seed(nsTags, tagKey(nodeARN))
	seed(nsTags, tagKey(fargateARN))
	seed(nsTags, tagKey(addonARN))

	otherNodegroupKey := nodegroupKey(region, otherName, "ng-other")
	seed(nsNodegroups, otherNodegroupKey)

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodDelete, "/clusters/"+clusterName, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 delete, got %d body=%s", rec.Code, rec.Body.String())
	}

	assertMissing := func(ns, key string) {
		t.Helper()
		_, found, err := svc.store.Get(ctx, ns, key)
		if err != nil {
			t.Fatalf("get %s/%s: %v", ns, key, err)
		}
		if found {
			t.Fatalf("expected %s/%s to be deleted", ns, key)
		}
	}

	assertMissing(nsClusters, clusterKey(region, clusterName))
	assertMissing(nsNodegroups, nodegroupKey(region, clusterName, "ng1"))
	assertMissing(nsUpdates, updateKey(region, clusterName, "upd-1"))
	assertMissing(nsFargate, fargateProfileKey(region, clusterName, "fp1"))
	assertMissing(nsAddons, addonKey(region, clusterName, "coredns"))
	assertMissing(nsIDPConfigs, idpConfigKey(region, clusterName, "oidc", "cfg-1"))
	assertMissing(nsAccess, accessEntryKey(region, clusterName, principalArn))
	assertMissing(nsAccessPol, associatedAccessPolicyKey(region, clusterName, principalArn, policyArn))
	assertMissing(nsPodIDAssoc, podIdentityAssociationKey(region, clusterName, "assoc-1"))

	assertMissing(nsTags, tagKey(clusterArn))
	assertMissing(nsTags, tagKey(nodeARN))
	assertMissing(nsTags, tagKey(fargateARN))
	assertMissing(nsTags, tagKey(addonARN))

	if _, found, err := svc.store.Get(ctx, nsNodegroups, otherNodegroupKey); err != nil {
		t.Fatalf("get other-cluster nodegroup: %v", err)
	} else if !found {
		t.Fatal("expected other cluster nodegroup to remain after deleting cleanup cluster")
	}

	if _, found, err := svc.store.Get(ctx, nsClusters, clusterKey(region, otherName)); err != nil {
		t.Fatalf("get other cluster: %v", err)
	} else if !found {
		t.Fatal("expected other cluster to remain after deleting cleanup cluster")
	}

	// Ensure the scan prefix used by cleanup does not over-delete across clusters.
	pairs, err := svc.store.Scan(ctx, nsNodegroups, serviceutil.RegionKey(region, otherName+"/"))
	if err != nil {
		t.Fatalf("scan other cluster nodegroups: %v", err)
	}
	if len(pairs) != 1 {
		t.Fatalf("expected exactly 1 other-cluster nodegroup after cleanup, got %d", len(pairs))
	}
}
