package ec2

// vpc_egress_docker_test.go — OVERCAST_VPC_EGRESS=routed against a real Docker
// daemon. The fake in vpc_egress_test.go pins the sequence; this pins the two
// facts about the daemon the sequence relies on: that a container whose every
// network is `--internal` has no default route at all, and that joining the
// VPC's egress network — routable, ranked with a gateway priority — gives it
// one, through that network's gateway, and leaving it takes it away again.

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/dataplane"
	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/docker/dockertest"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/state"
)

func TestRoutedEgress_realDaemonDefaultRouteFollowsTheEgressNetwork(t *testing.T) {
	// Opt-in, for the reason handler_igw_network_docker_test.go gives: this
	// creates real bridge networks and a real container.
	if os.Getenv("OVERCAST_DOCKER_NETWORK_TESTS") == "" {
		t.Skip("set OVERCAST_DOCKER_NETWORK_TESTS=1 to run the real-daemon routed-egress test " +
			"(it creates bridge networks and consumes Docker address-pool space)")
	}
	dc := docker.NewClient(config.DefaultDockerSocket(), zap.NewNop())
	if !dc.Available(5 * time.Second) {
		t.Skip("Docker not available, skipping the real-daemon routed-egress test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	const image = "alpine:3"
	if err := docker.NewImagePuller(dc).Ensure(ctx, image); err != nil {
		t.Skipf("cannot fetch %s: %v", image, err)
	}

	origInContainer := runningInContainer
	t.Cleanup(func() { runningInContainer = origInContainer })
	runningInContainer = func() bool { return false }

	// Everything this run creates is named and addressed from its own suffix,
	// so it cannot collide with another run's, another agent's instance, or
	// the developer's own: its own OVERCAST_NETWORK prefix, its own /16 for
	// the VPC, and its own /24 pool for the egress network.
	suffix := strings.ReplaceAll(protocol.NewRequestID(), "-", "")[:12]
	planeOctet := 200 + int(suffix[0]%40)
	poolOctet := int(suffix[1])
	cfg := &config.Config{
		Region: "us-east-1", AccountID: "000000000000",
		Network:       "overcast_vpc_egress_test_" + suffix,
		VPCEgress:     config.VPCEgressRouted,
		VPCEgressPool: "198.19." + itoa(poolOctet) + ".0/24",
	}
	h := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.NewMock()).handler
	h.docker = dc
	h.dockerReady.Store(true)

	vpc := &VPC{VpcID: "vpc-egr" + suffix[:8], CidrBlock: cidrFor(planeOctet)}
	planeName := cfg.VPCNetwork(vpc.VpcID)
	egressName := cfg.VPCEgressNetwork(vpc.VpcID)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		dockertest.RemoveOwned(cleanupCtx, dc,
			[]string{egressName, planeName, cfg.ControlNetwork(), cfg.Network}, t.Logf)
	})

	// Given: the VPC's plane, --internal as every plane is under routed; a
	// subnet on its main route table, which has no default route; and a
	// container in that subnet, on the plane and nothing else — the shape a
	// function in an isolated subnet has when the control plane is internal.
	planeID, err := h.createDockerVPCNetworkInternal(ctx, vpc, true, false)
	if err != nil {
		t.Fatalf("create the VPC plane: %v", err)
	}
	vpc.DockerNetworkID, vpc.NetworkStatus = planeID, vpcNetworkStatusOK
	if aerr := h.store.putVPC(ctx, vpc); aerr != nil {
		t.Fatal(aerr.Message)
	}
	subnet := &Subnet{SubnetID: "subnet-egr" + suffix[:8], VpcID: vpc.VpcID, CidrBlock: "10." + itoa(planeOctet) + ".1.0/24", AvailabilityZone: "us-east-1a", State: "available"}
	if aerr := h.store.putSubnet(ctx, subnet); aerr != nil {
		t.Fatal(aerr.Message)
	}
	rt := newMainRouteTable(vpc.VpcID, vpc.CidrBlock)
	if aerr := h.store.putRouteTable(ctx, rt); aerr != nil {
		t.Fatal(aerr.Message)
	}

	containerID, err := dc.CreateContainer(ctx, "overcast-vpc-egress-test-"+suffix, &docker.CreateContainerRequest{
		ContainerConfig: &docker.ContainerConfig{Image: image, Cmd: []string{"sleep", "300"}},
		NetworkingConfig: &docker.NetworkingConfig{EndpointsConfig: map[string]*docker.EndpointSettings{
			planeName: {IPAMConfig: &docker.EndpointIPAMConfig{IPv4Address: addressFor(planeOctet, 10)}},
		}},
	})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	t.Cleanup(func() { _ = dc.RemoveContainerForce(containerID) })
	if err := dc.StartContainer(ctx, containerID); err != nil {
		t.Fatalf("start container: %v", err)
	}
	h.recordPlacement(ctx, containerID, dataplane.Placement{VPCNetwork: planeID, VPCID: vpc.VpcID, SubnetIDs: []string{subnet.SubnetID}})

	// The container's default route, or "" for none. busybox's `ip route
	// show default` ignores the filter and prints the link routes too, so the
	// table is read whole and the default picked out here.
	defaultRoute := func() string {
		t.Helper()
		res, err := dc.Exec(ctx, containerID, []string{"ip", "route"}, nil)
		if err != nil {
			t.Fatalf("ip route: %v", err)
		}
		for _, line := range strings.Split(res.Output, "\n") {
			if line = strings.TrimSpace(line); strings.HasPrefix(line, "default ") {
				return line
			}
		}
		return ""
	}

	// Then: with every network --internal there is no default route — the
	// ENETUNREACH a missing NAT gateway produces, rather than a hang.
	if got := defaultRoute(); got != "" {
		t.Fatalf("a container on nothing but an --internal plane has a default route: %q", got)
	}

	// When: the subnet's route table gains a default route to an attached
	// internet gateway, and the placements are revisited.
	igw := &InternetGateway{InternetGatewayID: "igw-egr" + suffix[:8], Attachments: []IGWAttachment{{VpcID: vpc.VpcID, State: "attached"}}}
	if aerr := h.store.putInternetGateway(ctx, igw); aerr != nil {
		t.Fatal(aerr.Message)
	}
	rt.Routes = append(rt.Routes, Route{DestinationCidrBlock: "0.0.0.0/0", GatewayID: igw.InternetGatewayID, Origin: "CreateRoute"})
	if aerr := h.store.putRouteTable(ctx, rt); aerr != nil {
		t.Fatal(aerr.Message)
	}
	h.reconcileVPCEgress(ctx, vpc.VpcID)

	// Then: the egress network exists as specified, the container is on it
	// beside its unchanged plane attachment, and its default route now runs
	// through that network's gateway.
	if got := h.networkProblems(); len(got) != 0 {
		t.Fatalf("problems after the route: %+v", got)
	}
	egress, err := dc.InspectNetwork(ctx, egressName)
	if err != nil {
		t.Fatalf("inspect egress network: %v", err)
	}
	if egress.Internal {
		t.Errorf("the egress network is --internal")
	}
	if egress.Options[docker.OptionIPMasquerade] != "true" {
		t.Errorf("egress options = %v, want masquerade on", egress.Options)
	}
	if egress.Subnet() != cfg.VPCEgressPool {
		t.Errorf("egress subnet = %q, want the pool's one /24 %q", egress.Subnet(), cfg.VPCEgressPool)
	}
	if _, ok := egress.Containers[containerID]; !ok {
		t.Fatalf("container is not on the egress network: %v", egress.Containers)
	}
	c, err := dc.InspectContainer(ctx, containerID)
	if err != nil {
		t.Fatal(err)
	}
	if ep := c.NetworkSettings.Networks[planeName]; ep.IPAddress != addressFor(planeOctet, 10) {
		t.Errorf("plane address after the move = %q, want it untouched", ep.IPAddress)
	}
	if got := defaultRoute(); !strings.HasPrefix(got, "default via "+egress.Gateway()) {
		t.Errorf("default route = %q, want it via the egress network's gateway %s", got, egress.Gateway())
	}

	// When: the route is removed again.
	rt.Routes = rt.Routes[:1]
	if aerr := h.store.putRouteTable(ctx, rt); aerr != nil {
		t.Fatal(aerr.Message)
	}
	h.reconcileVPCEgress(ctx, vpc.VpcID)

	// Then: the route out is gone, and nothing else changed.
	if got := defaultRoute(); got != "" {
		t.Errorf("default route after the route was deleted = %q, want none", got)
	}
	if egress, err := dc.InspectNetwork(ctx, egressName); err != nil || len(egress.Containers) != 0 {
		t.Errorf("egress network after the withdrawal = %+v, %v; want it empty", egress, err)
	}
}
