package router

// advisories_unbacked_test.go — vpc-network-unbacked: a VPC whose Docker
// network the daemon refused to create is reported the moment it happens, and
// separately from a network that exists in the wrong state.

import (
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/dataplane"
)

func TestCheckVPCNetworkUnbacked_firesCriticallyPerUnbackedVPC(t *testing.T) {
	// Given: EC2 recorded one VPC the daemon refused a network for, beside
	// one whose network exists but carries the wrong isolation.
	problems := []dataplane.VPCNetworkProblem{
		{VpcID: "vpc-a", Unbacked: true, Detail: "the Docker network could not be created: 403: invalid pool request: Pool overlaps with other one on this address space"},
		{VpcID: "vpc-b", NetworkID: "net-b", Detail: "the Docker network should be external (an internet gateway is attached) but could not be recreated: boom"},
	}

	// When: both rules evaluate the same set.
	unbacked := checkVPCNetworkUnbacked(problems)
	isolation := checkVPCNetworkIsolation(problems)

	// Then: the unbacked VPC is a critical advisory of its own, naming the
	// VPC and Docker's reason, and the isolation advisory does not repeat it.
	if unbacked == nil {
		t.Fatal("expected an unbacked advisory, got nil")
	}
	if unbacked.Severity != advisorySeverityCritical {
		t.Errorf("severity = %q, want %q", unbacked.Severity, advisorySeverityCritical)
	}
	if unbacked.Code != advisoryCodeVPCNetworkUnbacked {
		t.Errorf("code = %q, want %q", unbacked.Code, advisoryCodeVPCNetworkUnbacked)
	}
	if !strings.HasPrefix(unbacked.Title, "A VPC has no Docker network") {
		t.Errorf("title = %q, want it to say the VPC has no network", unbacked.Title)
	}
	for _, want := range []string{"vpc-a: ", "Pool overlaps", "OVERCAST_EC2_VPC_STRATEGY=remapped"} {
		if !strings.Contains(unbacked.Detail, want) {
			t.Errorf("detail %q does not mention %q", unbacked.Detail, want)
		}
	}
	if strings.Contains(unbacked.Detail, "vpc-b") {
		t.Errorf("detail %q lists the isolation problem as an unbacked one", unbacked.Detail)
	}
	if unbacked.DocsPath != vpcNetworkUnbackedDocsPath {
		t.Errorf("docs path = %q, want %q", unbacked.DocsPath, vpcNetworkUnbackedDocsPath)
	}

	if isolation == nil {
		t.Fatal("expected the isolation advisory for vpc-b, got nil")
	}
	if strings.Contains(isolation.Detail, "vpc-a") || !strings.HasPrefix(isolation.Title, "A VPC's network") {
		t.Errorf("isolation advisory %q / %q counts the unbacked VPC", isolation.Title, isolation.Detail)
	}
}

func TestCheckVPCNetworkUnbacked_absentWhenNothingIsUnbacked(t *testing.T) {
	// Given/When/Then: no problems, or only isolation problems, means no
	// advisory — the common case must stay silent.
	if a := checkVPCNetworkUnbacked(nil); a != nil {
		t.Fatalf("expected no advisory, got %+v", a)
	}
	only := []dataplane.VPCNetworkProblem{{VpcID: "vpc-b", NetworkID: "net-b", Detail: "wrong isolation"}}
	if a := checkVPCNetworkUnbacked(only); a != nil {
		t.Fatalf("expected no advisory for isolation problems alone, got %+v", a)
	}
}

func TestCheckVPCNetworkUnbacked_countsSeveral(t *testing.T) {
	problems := make([]dataplane.VPCNetworkProblem, 0, vpcNetworkAdvisoryMaxListed+2)
	for i := 0; i < vpcNetworkAdvisoryMaxListed+2; i++ {
		problems = append(problems, dataplane.VPCNetworkProblem{
			VpcID: "vpc-" + string(rune('a'+i)), Unbacked: true, Detail: "refused",
		})
	}
	a := checkVPCNetworkUnbacked(problems)
	if a == nil {
		t.Fatal("expected an advisory, got nil")
	}
	if !strings.HasPrefix(a.Title, "7 VPCs have no Docker network") {
		t.Errorf("title = %q, want it to count every VPC", a.Title)
	}
	if !strings.Contains(a.Detail, "; and 2 more") {
		t.Errorf("detail %q does not cap the list at %d", a.Detail, vpcNetworkAdvisoryMaxListed)
	}
}
