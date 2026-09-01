package msk

import (
	"context"
	"slices"
	"testing"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/state"
)

// On AWS there is no such thing as a non-VPC MSK cluster: a provisioned cluster
// names client subnets and its brokers live in that VPC. Overcast placed the
// Redpanda container on the default plane regardless, so a function or task
// created *in* the VPC and a broker outside it only ever agreed by accident —
// once a VPC-placed resource stops keeping the default plane too, the bootstrap
// name stops resolving. These tests pin the subnet → VPC → network resolution
// that closes that.

// fakePlacementResolver answers from fixed tables, so a subnet or VPC absent
// from them stands in for one EC2 has no record of.
type fakePlacementResolver struct {
	subnetVPC map[string]string
	status    map[string]string
	network   map[string]string
}

func (f fakePlacementResolver) VpcIDForSubnet(_ context.Context, subnetID string) string {
	return f.subnetVPC[subnetID]
}

func (f fakePlacementResolver) VPCNetworkStatus(_ context.Context, vpcID string) string {
	return f.status[vpcID]
}

func (f fakePlacementResolver) DockerNetworkForVpc(_ context.Context, vpcID string) string {
	return f.network[vpcID]
}

func newPlacementHandler(t *testing.T, resolver VPCNetworkResolver) *Handler {
	t.Helper()
	return &Handler{
		cfg:         &config.Config{Region: "us-east-1", AccountID: "000000000000", Network: "overcast"},
		store:       newMSKStore(state.NewMemoryStore(), "us-east-1"),
		vpcResolver: resolver,
	}
}

const placementARN = "arn:aws:kafka:us-east-1:000000000000:cluster/events/abc-123"

func seedCluster(t *testing.T, h *Handler, subnets ...string) {
	t.Helper()
	if aerr := h.store.putCluster(context.Background(), &Cluster{
		ClusterArn:          placementARN,
		ClusterName:         "events",
		State:               "CREATING",
		BrokerNodeGroupInfo: BrokerNodeGroupInfo{ClientSubnets: subnets},
	}); aerr != nil {
		t.Fatalf("seed cluster: %v", aerr)
	}
}

func TestVPCForCluster_resolvesTheVPCTheClientSubnetsNamed(t *testing.T) {
	// Given: a cluster created with subnets EC2 owns.
	h := newPlacementHandler(t, fakePlacementResolver{
		subnetVPC: map[string]string{"subnet-a": "vpc-abc", "subnet-b": "vpc-abc"},
	})
	seedCluster(t, h, "subnet-a", "subnet-b")

	// When/Then: the brokers belong in that VPC.
	if got := h.vpcForCluster(context.Background(), placementARN); got != "vpc-abc" {
		t.Fatalf("vpcForCluster = %q, want %q", got, "vpc-abc")
	}
}

func TestVPCForCluster_fallsBackToTheDefaultPlane(t *testing.T) {
	cases := []struct {
		name     string
		subnets  []string
		resolver VPCNetworkResolver
	}{
		{
			// A v1 CreateCluster that omitted brokerNodeGroupInfo entirely.
			name:     "no subnets at all",
			resolver: fakePlacementResolver{subnetVPC: map[string]string{"subnet-a": "vpc-abc"}},
		},
		{
			// Synthetic IDs a test or a hand-written call invented; EC2 has no
			// record of them, and inventing a VPC for them would be worse.
			name:     "subnets EC2 does not know",
			subnets:  []string{"subnet-synthetic"},
			resolver: fakePlacementResolver{subnetVPC: map[string]string{"subnet-a": "vpc-abc"}},
		},
		{
			// EC2 disabled: there is nobody to ask.
			name:    "no resolver wired",
			subnets: []string{"subnet-a"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a cluster Overcast cannot place in any VPC.
			h := newPlacementHandler(t, tc.resolver)
			seedCluster(t, h, tc.subnets...)

			// When/Then: it keeps landing on the default plane rather than
			// erroring — "no VPC" has always been a working cluster here.
			if got := h.vpcForCluster(context.Background(), placementARN); got != "" {
				t.Fatalf("vpcForCluster = %q, want the default plane", got)
			}
		})
	}
}

func TestVPCForCluster_clusterRecordAlreadyGone(t *testing.T) {
	// Given: the record was deleted while the container was still starting.
	h := newPlacementHandler(t, fakePlacementResolver{
		subnetVPC: map[string]string{"subnet-a": "vpc-abc"},
	})

	// When/Then: nothing to resolve, and nothing to fail over.
	if got := h.vpcForCluster(context.Background(), placementARN); got != "" {
		t.Fatalf("vpcForCluster = %q, want the default plane", got)
	}
}

func TestPlacementFor_usesTheVPCNetwork(t *testing.T) {
	// Given: a launchable VPC with a Docker network behind it.
	h := newPlacementHandler(t, fakePlacementResolver{
		status:  map[string]string{"vpc-abc": "ok"},
		network: map[string]string{"vpc-abc": "overcast-vpc-vpc-abc"},
	})
	aliases := []string{"events.us-east-1.kafka.localhost"}

	// When: the broker container is placed.
	placement, err := h.placementFor(context.Background(), "vpc-abc", aliases)
	if err != nil {
		t.Fatalf("placementFor: %v", err)
	}

	// Then: it joins the VPC's network, still advertising its bootstrap names.
	if placement.VPCNetwork != "overcast-vpc-vpc-abc" {
		t.Fatalf("VPCNetwork = %q, want the VPC's network", placement.VPCNetwork)
	}
	if !slices.Equal(placement.Aliases, aliases) {
		t.Fatalf("Aliases = %#v, want %#v", placement.Aliases, aliases)
	}
}

func TestPlacementFor_unlaunchableVPCIsAnError(t *testing.T) {
	// Given: a VPC whose Docker network could not be created — the usual cause
	// is a CIDR collision with a leftover network from an earlier run.
	h := newPlacementHandler(t, fakePlacementResolver{
		status: map[string]string{"vpc-abc": "unbacked"},
	})

	// When/Then: placing into it fails rather than quietly landing the brokers
	// on the default plane, where nothing in the VPC could reach them anyway.
	if _, err := h.placementFor(context.Background(), "vpc-abc", nil); err == nil {
		t.Fatal("placementFor into an unbacked VPC: want an error, got nil")
	}
}

func TestPlacementFor_noVPCKeepsTheDefaultPlane(t *testing.T) {
	// Given: a cluster with no VPC to resolve.
	h := newPlacementHandler(t, fakePlacementResolver{})
	aliases := []string{"events.us-east-1.kafka.localhost"}

	// When: it is placed.
	placement, err := h.placementFor(context.Background(), "", aliases)
	if err != nil {
		t.Fatalf("placementFor: %v", err)
	}

	// Then: the zero placement, which is the default plane, plus its aliases.
	if placement.VPCNetwork != "" {
		t.Fatalf("VPCNetwork = %q, want the default plane", placement.VPCNetwork)
	}
	if !slices.Equal(placement.Aliases, aliases) {
		t.Fatalf("Aliases = %#v, want %#v", placement.Aliases, aliases)
	}
}

// A nil resolver stored in the interface field must not be mistaken for a wired
// one: dataplane.PlaceInVPC only skips resolution when the interface itself is
// nil, and a typed nil would panic on the first call.
func TestPlacementFor_withoutAResolver(t *testing.T) {
	h := newPlacementHandler(t, nil)

	placement, err := h.placementFor(context.Background(), "vpc-abc", nil)
	if err != nil {
		t.Fatalf("placementFor: %v", err)
	}
	if placement.VPCNetwork != "" {
		t.Fatalf("VPCNetwork = %q, want the default plane", placement.VPCNetwork)
	}
}
