package cloudformation

// provisioner_eks_test.go — when an AWS::EKS::Cluster resource is done.
//
// CreateCluster answers with the cluster in CREATING and the control plane
// comes up behind it. The handler returned there, so in live mode a stack
// reported CREATE_COMPLETE around a cluster whose API server was not up: the
// endpoint was still empty and a kubeconfig fetched off the back of that green
// deploy was refused with "Cluster is not ready yet".

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/config"
)

// fakeEKS is an EKS endpoint that walks one cluster through a scripted sequence
// of statuses — one per DescribeCluster, with the last repeating.
type fakeEKS struct {
	script statusScript
	// issues are the cluster health issues DescribeCluster reports, which is
	// where a control plane's own failure ends up on real EKS.
	issues []map[string]any
	// notFound answers DescribeCluster with a 404, as a cluster deleted from
	// under the stack does.
	notFound bool
	// deletedNames records every name DeleteCluster was called with, in
	// order — used to confirm Delete resolves an ARN-shaped physical ID back
	// to the plain name before dispatching (#1690).
	deletedNames []string
}

// ServeHTTP answers EKS's own REST bindings — POST /clusters,
// GET /clusters/{name} and DELETE /clusters/{name} — the surface the
// provisioner dispatches to since #1226 retired the invented "EKS.<Op>"
// X-Amz-Target prefix.
func (f *fakeEKS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	const arn = "arn:aws:eks:us-east-1:000000000000:cluster/app-eks"

	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/clusters":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"cluster": map[string]any{"name": "app-eks", "arn": arn, "status": "CREATING"},
		})

	case r.Method == http.MethodGet && r.URL.Path == "/clusters/app-eks":
		if f.notFound {
			http.Error(w, `{"__type":"ResourceNotFoundException"}`, http.StatusNotFound)
			return
		}
		cluster := map[string]any{"name": "app-eks", "arn": arn, "status": f.script.next("ACTIVE")}
		if len(f.issues) > 0 {
			cluster["health"] = map[string]any{"issues": f.issues}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"cluster": cluster})

	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/clusters/"):
		name := strings.TrimPrefix(r.URL.Path, "/clusters/")
		f.deletedNames = append(f.deletedNames, name)
		cluster := map[string]any{"name": name, "arn": arn, "status": "DELETING"}
		_ = json.NewEncoder(w).Encode(map[string]any{"cluster": cluster})

	default:
		http.Error(w, "unexpected request "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
	}
}

func eksClusterProps() map[string]any {
	return map[string]any{
		"Name":    "app-eks",
		"RoleArn": "arn:aws:iam::000000000000:role/eks",
		"Version": "1.29",
	}
}

// A created cluster is not done until its control plane is. The resource must
// keep asking rather than take CreateCluster's own CREATING as the end of it.
func TestEKSClusterCreate_waitsForTheClusterToBecomeActive(t *testing.T) {
	// Given: a control plane that is still coming up for its first two checks
	f := &fakeEKS{script: statusScript{statuses: []string{"CREATING", "CREATING", "ACTIVE"}}}
	p, rCtx := newTestProvisioner(t, f)

	// When: CloudFormation provisions it
	id, err := p.provisionResource(context.Background(), "Cluster",
		TemplateResource{Type: "AWS::EKS::Cluster"}, eksClusterProps(), rCtx)

	// Then: it completed only once the cluster was ACTIVE
	if err != nil {
		t.Fatalf("provisionResource: %v", err)
	}
	// Per AWS docs, Ref on AWS::EKS::Cluster returns the cluster name, not
	// its ARN — see eksClusterNameFromPhysicalID in
	// provisioner_query_rest_coverage.go (#1690). The ARN is still available
	// via Fn::GetAtt Arn.
	if id != "app-eks" {
		t.Errorf("physical ID = %q, want the cluster name %q", id, "app-eks")
	}
	if got := f.script.count(); got != 3 {
		t.Errorf("DescribeCluster calls = %d, want 3 — the resource completed before the control "+
			"plane was up", got)
	}
}

// A cluster that reaches FAILED must fail the resource with what EKS recorded
// against it. DescribeCluster carries that as health.issues, which is where the
// control plane's own reason ends up.
func TestEKSClusterCreate_failsWithTheClustersOwnHealthIssue(t *testing.T) {
	// Given: a control plane that cannot start
	f := &fakeEKS{
		script: statusScript{statuses: []string{"CREATING", "FAILED"}},
		issues: []map[string]any{{
			"code":    "ConfigurationConflict",
			"message": "the k3s control-plane container exited before the API server bound",
		}},
	}
	p, rCtx := newTestProvisioner(t, f)

	// When: CloudFormation provisions it
	id, err := p.provisionResource(context.Background(), "Cluster",
		TemplateResource{Type: "AWS::EKS::Cluster"}, eksClusterProps(), rCtx)

	// Then: the resource fails, carrying the issue EKS recorded
	if err == nil {
		t.Fatal("expected the resource to fail for a cluster that went to FAILED")
	}
	if !strings.Contains(err.Error(), "before the API server bound") {
		t.Errorf("expected the cluster's own health issue, got %v", err)
	}
	if strings.Contains(err.Error(), "did not become ACTIVE within") {
		t.Errorf("a terminal status was reported as a timeout: %v", err)
	}
	if id == "" {
		t.Error("physical ID was dropped; a cluster that failed to come up still exists")
	}
}

// A cluster that never becomes ACTIVE must not leave the stack complete around
// it: the resource fails, saying what it was waiting for and how far it got.
func TestEKSClusterCreate_neverActiveFailsTheResource(t *testing.T) {
	f := &fakeEKS{script: statusScript{statuses: []string{"CREATING"}}}
	p, rCtx := newTestProvisioner(t, f, newPollDrivenClock())

	_, err := p.provisionResource(context.Background(), "Cluster",
		TemplateResource{Type: "AWS::EKS::Cluster"}, eksClusterProps(), rCtx)
	if err == nil {
		t.Fatal("expected the resource to fail for a cluster that never became ACTIVE")
	}
	for _, want := range []string{"app-eks", "become ACTIVE", "CREATING"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("failure reason %q does not mention %q", err.Error(), want)
		}
	}
}

// A cluster that is gone by the time the wait looks for it is not going to
// become ACTIVE.
func TestEKSClusterCreate_failsWhenTheClusterDisappears(t *testing.T) {
	f := &fakeEKS{notFound: true}
	p, rCtx := newTestProvisioner(t, f)

	if _, err := p.provisionResource(context.Background(), "Cluster",
		TemplateResource{Type: "AWS::EKS::Cluster"}, eksClusterProps(), rCtx); err == nil {
		t.Fatal("expected the resource to fail for a cluster that no longer exists")
	}
}

// eksClusterNameFromPhysicalID must resolve both the current physical-ID
// shape (the cluster name, per AWS docs — Ref returns the name, not the ARN)
// and the pre-#1690 shape (the ARN an older Overcast build recorded), so a
// stack whose state predates the fix still deletes and updates cleanly.
func TestEKSClusterNameFromPhysicalID(t *testing.T) {
	for _, tc := range []struct {
		name       string
		physicalID string
		want       string
	}{
		{name: "current: plain cluster name", physicalID: "app-eks", want: "app-eks"},
		{name: "pre-#1690: cluster ARN", physicalID: "arn:aws:eks:us-east-1:000000000000:cluster/app-eks", want: "app-eks"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := eksClusterNameFromPhysicalID(tc.physicalID); got != tc.want {
				t.Errorf("eksClusterNameFromPhysicalID(%q) = %q, want %q", tc.physicalID, got, tc.want)
			}
		})
	}
}

// A stack whose state predates #1690 has the cluster ARN recorded as the
// physical ID. Delete must resolve it to the plain name DeleteCluster
// expects rather than sending the ARN itself as a path segment (which would
// double-encode "/" and 404).
func TestEKSClusterDelete_toleratesARNShapedPhysicalID(t *testing.T) {
	// Given: a physical ID shaped the way a pre-fix Overcast build persisted it
	f := &fakeEKS{}
	h := &eksClusterHandler{}
	rCtx := &resolveContext{Region: "us-east-1", AccountID: "000000000000"}
	const preFixPhysicalID = "arn:aws:eks:us-east-1:000000000000:cluster/app-eks"

	// When: the stack is deleted
	err := h.Delete(context.Background(), f, &config.Config{}, preFixPhysicalID, rCtx)

	// Then: DeleteCluster was called with the plain name, and succeeded
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(f.deletedNames) != 1 || f.deletedNames[0] != "app-eks" {
		t.Errorf("DeleteCluster called with %v, want a single call for %q", f.deletedNames, "app-eks")
	}
}
