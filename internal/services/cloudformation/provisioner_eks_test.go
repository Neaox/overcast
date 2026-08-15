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
}

func (f *fakeEKS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	const arn = "arn:aws:eks:us-east-1:000000000000:cluster/app-eks"

	switch r.Header.Get("X-Amz-Target") {
	case "EKS.CreateCluster":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"cluster": map[string]any{"name": "app-eks", "arn": arn, "status": "CREATING"},
		})

	case "EKS.DescribeCluster":
		if f.notFound {
			http.Error(w, `{"__type":"ResourceNotFoundException"}`, http.StatusNotFound)
			return
		}
		cluster := map[string]any{"name": "app-eks", "arn": arn, "status": f.script.next("ACTIVE")}
		if len(f.issues) > 0 {
			cluster["health"] = map[string]any{"issues": f.issues}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"cluster": cluster})

	default:
		http.Error(w, "unexpected target "+r.Header.Get("X-Amz-Target"), http.StatusBadRequest)
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
	if !strings.HasSuffix(id, "cluster/app-eks") {
		t.Errorf("physical ID = %q, want the cluster ARN", id)
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
