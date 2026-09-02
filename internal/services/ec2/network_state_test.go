package ec2

// network_state_test.go — what a per-VPC network is created *as*, and what
// happens when the one a VPC records is gone.
//
// Ownership — who may remove a network — is vpc_network_ownership_test.go's
// subject. This is the other half: a network Overcast keeps is only useful if
// it is in the state Overcast believes it is in, and a VPC whose network has
// vanished has to get one back rather than going on pointing at nothing.

import (
	"context"
	"testing"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/docker"
)

// Every VPC network carries the spec hash of the state it was created in, or
// the next start has nothing to verify it against and the drift that made
// #1564 invisible goes on being invisible.
func TestCreateDockerVPCNetwork_stampsTheSpecItWasCreatedTo(t *testing.T) {
	daemon := newNetworkDaemon(t)
	h := containerisedHandler(t, daemon)
	ctx := context.Background()

	if _, err := h.createDockerVPCNetwork(ctx, &VPC{VpcID: "vpc-1", CidrBlock: "10.9.0.0/16"}); err != nil {
		t.Fatalf("createDockerVPCNetwork: %v", err)
	}

	req := daemon.createRequest(h.cfg.VPCNetwork("vpc-1"))
	if req == nil {
		t.Fatalf("no create request for %s; calls: %v", h.cfg.VPCNetwork("vpc-1"), daemon.calls)
	}
	labels, _ := req["Labels"].(map[string]any)
	if labels[docker.LabelSpecHash] == nil {
		t.Errorf("labels = %v, want a spec hash so the next start can verify the network", labels)
	}
	if labels[docker.LabelEgressMode] == nil {
		t.Errorf("labels = %v, want the egress mode that chose this network's isolation", labels)
	}
}

// A VPC network's isolation follows OVERCAST_VPC_EGRESS and not the VPC's
// internet gateway. Reading the gateway alone delivered only the withholding
// half of AWS's model, in which a private-with-NAT subnet and an isolated one
// are the same network — so a stack that works on AWS failed here with no
// template change that helped.
func TestCreateDockerVPCNetwork_isolationFollowsTheEgressMode(t *testing.T) {
	for _, tc := range []struct {
		mode config.VPCEgressMode
		want bool
	}{{config.VPCEgressOpen, false}, {config.VPCEgressNone, true}} {
		t.Run(string(tc.mode), func(t *testing.T) {
			daemon := newNetworkDaemon(t)
			h := containerisedHandler(t, daemon)
			h.cfg.VPCEgress = tc.mode

			// No internet gateway is attached, which before egress modes was
			// the whole of the decision and made this network `--internal`.
			if _, err := h.createDockerVPCNetwork(context.Background(),
				&VPC{VpcID: "vpc-1", CidrBlock: "10.9.0.0/16"}); err != nil {
				t.Fatalf("createDockerVPCNetwork: %v", err)
			}

			req := daemon.createRequest(h.cfg.VPCNetwork("vpc-1"))
			if req == nil {
				t.Fatalf("no create request; calls: %v", daemon.calls)
			}
			got, _ := req["Internal"].(bool)
			if got != tc.want {
				t.Errorf("Internal = %v under %s, want %v", got, tc.mode, tc.want)
			}
		})
	}
}

// A VPC whose Docker network has been removed — by a neighbouring instance
// before #1568, by a `docker network prune`, by hand — must get one back on the
// next reconcile. Leaving the record pointing at a network id that no longer
// exists means every function placed in that VPC fails to start, with an error
// naming a network nobody can find.
func TestReconcileNetworks_recreatesAVPCNetworkThatVanished(t *testing.T) {
	daemon := newNetworkDaemon(t)
	h := containerisedHandler(t, daemon)
	ctx := context.Background()

	vpc := &VPC{VpcID: "vpc-live", CidrBlock: "10.42.0.0/16", DockerNetworkID: "net-vanished"}
	if aerr := h.store.putVPC(ctx, vpc); aerr != nil {
		t.Fatalf("putVPC: %v", aerr.Message)
	}

	// The daemon holds no network at all: the snapshot is empty, as it would be
	// after the record's network was removed.
	h.reconcileNetworks(ctx, nil)

	stored, aerr := h.store.getVPC(ctx, "vpc-live")
	if aerr != nil {
		t.Fatalf("getVPC: %v", aerr.Message)
	}
	if stored.DockerNetworkID == "net-vanished" || stored.DockerNetworkID == "" {
		t.Errorf("VPC network id = %q, want a freshly created network", stored.DockerNetworkID)
	}
	if stored.NetworkStatus != vpcNetworkStatusOK {
		t.Errorf("VPC network status = %q, want %q", stored.NetworkStatus, vpcNetworkStatusOK)
	}
}
