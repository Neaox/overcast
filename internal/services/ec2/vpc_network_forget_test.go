package ec2

// vpc_network_forget_test.go — a deleted VPC takes its network out of
// /_overcast/health with it (#1583).
//
// The status entry and the problem entry are two separate records of the same
// network. forgetVPCNetwork already dropped the second; without the first, a
// deleted VPC stayed listed as a live network — carrying whatever drift it last
// had, and therefore whatever advisory that drift raised, for the life of the
// daemon.

import (
	"net/url"
	"testing"

	"github.com/overcast-sh/overcast/internal/docker"
)

func TestDeleteVpc_forgetsItsNetworkInHealth(t *testing.T) {
	for _, strategy := range []string{"shared", "strict", "remapped"} {
		t.Run(strategy, func(t *testing.T) {
			// Given: a VPC with a Docker network, reported to health.
			f := newFakeVPCDocker(t)
			h := vpcDockerHandler(t, f, strategy)
			tracker := docker.NewTracker()
			h.SetNetworkReporter(tracker)

			vpcID := createVPC(t, h, "10.30.0.0/16")
			name := h.cfg.VPCNetwork(vpcID)
			if !healthReports(tracker, name) {
				t.Fatalf("health does not report %q after CreateVpc; nothing to forget", name)
			}

			// When: the VPC is deleted.
			rec := ec2Call(t, h.DeleteVpc, url.Values{"VpcId": {vpcID}})
			if rec.Code != 200 {
				t.Fatalf("DeleteVpc: %d %s", rec.Code, rec.Body.String())
			}

			// Then: health has stopped talking about the network.
			if healthReports(tracker, name) {
				t.Errorf("health still reports %q after its VPC was deleted", name)
			}
		})
	}
}

// The shared strategy is the case that makes forgetting on the record alone
// wrong: two VPCs on one bridge, and deleting one of them removes a record, not
// a network. The check that the network is actually gone is what keeps a live
// network in the report.
func TestDeleteVpc_keepsANetworkASharerStillUses(t *testing.T) {
	f := newFakeVPCDocker(t)
	h := vpcDockerHandler(t, f, "shared")
	tracker := docker.NewTracker()
	h.SetNetworkReporter(tracker)

	// Given: two VPCs with the same CIDR, which `shared` puts on one bridge.
	owner := createVPC(t, h, "10.31.0.0/16")
	sharer := createVPC(t, h, "10.31.0.0/16")
	name := h.cfg.VPCNetwork(owner)
	if storedVPC(t, h, sharer).DockerNetworkID != storedVPC(t, h, owner).DockerNetworkID {
		t.Skip("this strategy did not share a network between the two VPCs")
	}
	if !healthReports(tracker, name) {
		t.Fatalf("health does not report %q", name)
	}

	// When: the sharer is deleted, leaving the network in place.
	rec := ec2Call(t, h.DeleteVpc, url.Values{"VpcId": {sharer}})
	if rec.Code != 200 {
		t.Fatalf("DeleteVpc: %d %s", rec.Code, rec.Body.String())
	}

	// Then: the network is still there, and health still says so.
	if f.network(name) == nil {
		t.Fatalf("the shared network %q was removed with the sharer", name)
	}
	if !healthReports(tracker, name) {
		t.Errorf("health forgot %q while the VPC that owns it is still there", name)
	}
}

// The default VPC sits on the shared data plane, which outlives every VPC on
// it. Forgetting that would take the plane out of health on a VPC deletion.
func TestForgetVPCNetworkStatus_neverForgetsTheSharedDataPlane(t *testing.T) {
	f := newFakeVPCDocker(t)
	h := vpcDockerHandler(t, f, "shared")
	tracker := docker.NewTracker()
	h.SetNetworkReporter(tracker)
	tracker.RecordNetworks([]docker.NetworkStatus{{Name: h.cfg.Network}})

	h.forgetVPCNetwork(t.Context(), &VPC{VpcID: "vpc-default", IsDefault: true,
		DockerNetworkName: h.cfg.Network, DockerNetworkID: "net-plane"})

	if !healthReports(tracker, h.cfg.Network) {
		t.Errorf("the shared data plane %q was forgotten when a VPC on it was deleted", h.cfg.Network)
	}
}
