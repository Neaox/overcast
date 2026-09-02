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

// `none` has to mean none: a VPC network is `--internal` whatever the template
// says, because a routable VPC bridge would be a hole in the mode's promise.
//
// `open` leaves the gateway deciding, which costs it nothing — the container is
// also on the routable control plane and takes its default route from there, so
// it has egress either way (measured end-to-end: a Lambda in an isolated subnet
// on an `Internal=true` network reached checkip.amazonaws.com and got a 403
// from real sts.us-east-1). Flattening the flag would only make it lie about
// the template and leave `routed` nothing to inherit.
func TestCreateDockerVPCNetwork_isolationUnderEachEgressMode(t *testing.T) {
	for _, tc := range []struct {
		mode config.VPCEgressMode
		want bool
	}{
		{config.VPCEgressOpen, true}, // no gateway attached, so still internal
		{config.VPCEgressNone, true},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			daemon := newNetworkDaemon(t)
			h := containerisedHandler(t, daemon)
			h.cfg.VPCEgress = tc.mode

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

// The one difference the mode makes to a VPC network today: a VPC *with* an
// internet gateway is routable under `open` and still isolated under `none`.
func TestVPCNetworkInternal_noneIgnoresTheGateway(t *testing.T) {
	daemon := newNetworkDaemon(t)
	h := containerisedHandler(t, daemon)
	ctx := context.Background()

	vpc := &VPC{VpcID: "vpc-gw", CidrBlock: "10.9.0.0/16"}
	igw := &InternetGateway{
		InternetGatewayID: "igw-1",
		Attachments:       []IGWAttachment{{VpcID: "vpc-gw", State: "attached"}},
	}
	if aerr := h.store.putInternetGateway(ctx, igw); aerr != nil {
		t.Fatal(aerr.Message)
	}

	for _, tc := range []struct {
		mode config.VPCEgressMode
		want bool
	}{{config.VPCEgressOpen, false}, {config.VPCEgressNone, true}} {
		t.Run(string(tc.mode), func(t *testing.T) {
			d := newNetworkDaemon(t)
			hh := containerisedHandler(t, d)
			hh.cfg.VPCEgress = tc.mode
			if aerr := hh.store.putInternetGateway(ctx, igw); aerr != nil {
				t.Fatal(aerr.Message)
			}
			if _, err := hh.createDockerVPCNetwork(ctx, vpc); err != nil {
				t.Fatalf("createDockerVPCNetwork: %v", err)
			}
			req := d.createRequest(hh.cfg.VPCNetwork("vpc-gw"))
			if req == nil {
				t.Fatalf("no create request; calls: %v", d.calls)
			}
			if got, _ := req["Internal"].(bool); got != tc.want {
				t.Errorf("Internal = %v under %s with a gateway attached, want %v", got, tc.mode, tc.want)
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
