package cloudformation

// provisioner_msk_test.go — when an AWS::MSK::Cluster resource is done.
//
// CreateCluster POSTs /v1/clusters and answers with the cluster in "CREATING":
// the brokers come up behind it. The handler returned there, so a stack
// reported CREATE_COMPLETE around a cluster with no broker listening, and
// anything downstream — a consumer, a Lambda event source, a GetAtt on the
// ARN — ran against it.
//
// The wait is tested against a scripted MSK endpoint rather than the real
// service, because what is under test is the polling: that the resource keeps
// asking, believes only ACTIVE, and reports the cluster's own stateInfo when it
// fails. Against a metadata-only cluster the first check is already ACTIVE,
// which is exactly the case that cannot tell a wait apart from no wait at all.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// fakeMSK is an MSK REST endpoint that walks one cluster through a scripted
// sequence of states — one per DescribeCluster, with the last repeating.
type fakeMSK struct {
	script statusScript
	// stateInfo is what the cluster reports about an unusable state, which is
	// where the broker's own failure ends up on real MSK.
	stateInfo map[string]string
	// notFound answers DescribeCluster with a 404, as a cluster deleted from
	// under the stack does.
	notFound bool
}

func (f *fakeMSK) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	const clusterARN = "arn:aws:kafka:us-east-1:000000000000:cluster/app-msk/abcd"
	w.Header().Set("Content-Type", "application/json")

	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/clusters":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"clusterArn":  clusterARN,
			"clusterName": "app-msk",
			"state":       "CREATING",
		})

	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/clusters/"):
		if f.notFound {
			http.Error(w, `{"message":"cluster not found"}`, http.StatusNotFound)
			return
		}
		info := map[string]any{"state": f.script.next("ACTIVE")}
		if len(f.stateInfo) > 0 {
			info["stateInfo"] = f.stateInfo
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"clusterInfo": info})

	default:
		http.Error(w, fmt.Sprintf("unexpected %s %s", r.Method, r.URL.Path), http.StatusBadRequest)
	}
}

func mskClusterProps() map[string]any {
	return map[string]any{
		"ClusterName":         "app-msk",
		"KafkaVersion":        "3.5.1",
		"NumberOfBrokerNodes": 1,
	}
}

// A created cluster is not done until its brokers are. The resource must keep
// asking rather than take CreateCluster's own "CREATING" as the end of it.
func TestMSKClusterCreate_waitsForTheClusterToBecomeActive(t *testing.T) {
	// Given: a cluster that is still creating for its first two state checks
	f := &fakeMSK{script: statusScript{statuses: []string{"CREATING", "CREATING", "ACTIVE"}}}
	p, rCtx := newTestProvisioner(t, f)

	// When: CloudFormation provisions it
	id, err := p.provisionResource(context.Background(), "Kafka",
		TemplateResource{Type: "AWS::MSK::Cluster"}, mskClusterProps(), rCtx)

	// Then: it completed only once the cluster was ACTIVE
	if err != nil {
		t.Fatalf("provisionResource: %v", err)
	}
	if !strings.Contains(id, "cluster/app-msk") {
		t.Errorf("physical ID = %q, want the cluster ARN", id)
	}
	if got := f.script.count(); got != 3 {
		t.Errorf("DescribeCluster calls = %d, want 3 — the resource completed before the cluster "+
			"was ACTIVE", got)
	}
}

// A cluster that reaches FAILED must fail the resource with the cluster's own
// account of it. MSK records that in stateInfo, which the API reference
// describes as the code and message for a cluster "in an unusable state" —
// exactly what an operator needs and exactly what a timeout does not give them.
func TestMSKClusterCreate_failsWithTheClustersOwnStateInfo(t *testing.T) {
	// Given: a cluster whose broker dies while it is being created
	f := &fakeMSK{
		script:    statusScript{statuses: []string{"CREATING", "FAILED"}},
		stateInfo: map[string]string{"code": "BROKER_START_FAILED", "message": "the broker container exited with code 1"},
	}
	p, rCtx := newTestProvisioner(t, f)

	// When: CloudFormation provisions it
	id, err := p.provisionResource(context.Background(), "Kafka",
		TemplateResource{Type: "AWS::MSK::Cluster"}, mskClusterProps(), rCtx)

	// Then: the resource fails, carrying what MSK recorded against it
	if err == nil {
		t.Fatal("expected the resource to fail for a cluster that went to FAILED")
	}
	if !strings.Contains(err.Error(), "exited with code 1") {
		t.Errorf("expected the cluster's own stateInfo message, got %v", err)
	}
	if strings.Contains(err.Error(), "did not become ACTIVE within") {
		t.Errorf("a terminal state was reported as a timeout: %v", err)
	}
	// The cluster exists — create named it before it failed — and rollback
	// deletes it by that name.
	if id == "" {
		t.Error("physical ID was dropped; a cluster that failed to come up still exists")
	}
	if got := f.script.count(); got != 2 {
		t.Errorf("DescribeCluster calls = %d, want 2 — the wait kept polling past a terminal state", got)
	}
}

// A cluster that never becomes ACTIVE must not leave the stack complete around
// it: the stack fails, and the reason says what it was waiting for.
func TestMSKClusterCreate_neverActiveFailsTheResource(t *testing.T) {
	// Given: a cluster stuck in CREATING for as long as anyone asks
	f := &fakeMSK{script: statusScript{statuses: []string{"CREATING"}}}
	p, rCtx := newTestProvisioner(t, f, newPollDrivenClock())

	// When: CloudFormation provisions it
	_, err := p.provisionResource(context.Background(), "Kafka",
		TemplateResource{Type: "AWS::MSK::Cluster"}, mskClusterProps(), rCtx)

	// Then: it fails, saying where the cluster got to and what was expected
	if err == nil {
		t.Fatal("expected the resource to fail for a cluster that never became ACTIVE")
	}
	for _, want := range []string{"app-msk", "become ACTIVE", "CREATING"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("failure reason %q does not mention %q", err.Error(), want)
		}
	}
}

// A deployment with no MSK container runtime is not a special case. The wait
// used to be skipped there, because MSK left a cluster with no broker coming in
// CREATING and waiting for it would have held every MSK stack open for three
// quarters of an hour before rolling it back. MSK now marks such a cluster
// ACTIVE at once — there is no broker being claimed — so the wait runs
// everywhere and gets its answer on the first poll.
func TestMSKClusterCreate_withoutAContainerRuntimeStillWaitsAndCompletes(t *testing.T) {
	// Given: a deployment with no MSK container runtime, where the service
	// answers ACTIVE because nothing is coming that could change it
	f := &fakeMSK{script: statusScript{statuses: []string{"ACTIVE"}}}
	p, rCtx := newTestProvisioner(t, f, newPollDrivenClock())
	p.cfg.MSKDockerSocket = ""

	// When: CloudFormation provisions a cluster
	_, err := p.provisionResource(context.Background(), "Kafka",
		TemplateResource{Type: "AWS::MSK::Cluster"}, mskClusterProps(), rCtx)

	// Then: it completes, having actually asked
	if err != nil {
		t.Fatalf("provisionResource: %v", err)
	}
	if got := f.script.count(); got != 1 {
		t.Errorf("DescribeCluster calls = %d, want 1 — the wait no longer skips a socketless deployment", got)
	}
}

// A cluster that is gone by the time the wait looks for it is not going to
// become ACTIVE.
func TestMSKClusterCreate_failsWhenTheClusterDisappears(t *testing.T) {
	f := &fakeMSK{notFound: true}
	p, rCtx := newTestProvisioner(t, f)

	if _, err := p.provisionResource(context.Background(), "Kafka",
		TemplateResource{Type: "AWS::MSK::Cluster"}, mskClusterProps(), rCtx); err == nil {
		t.Fatal("expected the resource to fail for a cluster that no longer exists")
	}
}
