package ec2

// handler_igw_network_docker_test.go — the gateway flip against a real Docker
// daemon. The fake in handler_igw_network_test.go pins the sequence; this pins
// the two facts about the daemon the sequence relies on: that it refuses to
// remove a network with endpoints (the reason the containers are moved at
// all), and that it accepts a container back on the recreated network at the
// address and aliases it had.

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/docker/dockertest"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/state"
)

func TestFlipDockerVPCNetworkInternal_realDaemonMovesContainers(t *testing.T) {
	// Opt-in. This one creates real bridge networks and a real container, so on
	// a host that already holds enough networks it fails with "Pool overlaps
	// with other one on this address space" — a fact about the machine rather
	// than about the code, and one that made `-tags slim` runs fail for
	// reviewers. Docker being *reachable* is not consent to consume its address
	// pool.
	if os.Getenv("OVERCAST_DOCKER_NETWORK_TESTS") == "" {
		t.Skip("set OVERCAST_DOCKER_NETWORK_TESTS=1 to run the real-daemon gateway flip test " +
			"(it creates bridge networks and consumes Docker address-pool space)")
	}
	dc := docker.NewClient(config.DefaultDockerSocket(), zap.NewNop())
	if !dc.Available(5 * time.Second) {
		t.Skip("Docker not available, skipping the real-daemon gateway flip test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	const image = "alpine:3"
	if err := docker.NewImagePuller(dc).Ensure(ctx, image); err != nil {
		t.Skipf("cannot fetch %s: %v", image, err)
	}

	// Given: a VPC backed by a real --internal network, with a container on
	// it at a pinned address and under a DNS alias — the shape an RDS
	// instance or an ECS task has.
	origInContainer := runningInContainer
	t.Cleanup(func() { runningInContainer = origInContainer })
	runningInContainer = func() bool { return false }

	suffix := strings.ReplaceAll(protocol.NewRequestID(), "-", "")[:12]
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000", Network: "overcast_vpc_igw_test_" + suffix}
	h := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.NewMock()).handler
	h.docker = dc
	h.dockerReady.Store(true)

	// A /16 nothing else on the daemon is likely to hold: the second octet
	// comes from the run's own suffix.
	octet := 200 + int(suffix[0]%40)
	vpc := &VPC{VpcID: "vpc-igw" + suffix[:8], CidrBlock: cidrFor(octet)}
	address := addressFor(octet, 10)
	name := cfg.VPCNetwork(vpc.VpcID)
	// Exact names through the shared helper, as the ECS and RDS data-plane
	// tests do (#1567): it evicts whatever is left on each network before
	// removing it, so a container the test failed to clean up does not leave
	// the network behind too. The planes are listed as well as the VPC network
	// — this handler creates all three under a per-run OVERCAST_NETWORK.
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		dockertest.RemoveOwned(cleanupCtx, dc,
			[]string{name, cfg.ControlNetwork(), cfg.Network}, t.Logf)
	})
	netID, err := h.createDockerVPCNetworkInternal(ctx, vpc, true, false)
	if err != nil {
		t.Fatalf("create internal VPC network: %v", err)
	}
	vpc.DockerNetworkID = netID
	vpc.NetworkStatus = vpcNetworkStatusOK
	if aerr := h.store.putVPC(ctx, vpc); aerr != nil {
		t.Fatal(aerr.Message)
	}

	containerID, err := dc.CreateContainer(ctx, "overcast-vpc-igw-test-"+suffix, &docker.CreateContainerRequest{
		ContainerConfig: &docker.ContainerConfig{Image: image, Cmd: []string{"sleep", "300"}},
		NetworkingConfig: &docker.NetworkingConfig{EndpointsConfig: map[string]*docker.EndpointSettings{
			name: {Aliases: []string{"mydb.vpc.test"}, IPAMConfig: &docker.EndpointIPAMConfig{IPv4Address: address}},
		}},
	})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	t.Cleanup(func() { _ = dc.RemoveContainerForce(containerID) })
	if err := dc.StartContainer(ctx, containerID); err != nil {
		t.Fatalf("start container: %v", err)
	}

	// The daemon really does refuse the shortcut — otherwise moving the
	// containers would be ceremony.
	if err := dc.RemoveNetwork(ctx, netID); err == nil || !strings.Contains(err.Error(), "active endpoints") {
		t.Fatalf("expected Docker to refuse removing a network with an endpoint, got %v", err)
	}

	// When: the network is flipped to external.
	newID, err := h.flipDockerVPCNetworkInternal(ctx, vpc, false, true)

	// Then: a new, external network backs the VPC; the old one is gone; and
	// the container is on the new one at the same address, still answering
	// to its alias.
	if err != nil {
		t.Fatalf("flip: %v", err)
	}
	if newID == "" || newID == netID {
		t.Fatalf("expected a recreated network, got %q (old %q)", newID, netID)
	}
	info, err := dc.InspectNetwork(ctx, newID)
	if err != nil {
		t.Fatalf("inspect recreated network: %v", err)
	}
	if info.Internal {
		t.Error("recreated network is still --internal")
	}
	if _, ok := info.Containers[containerID]; !ok {
		t.Errorf("container %s is not on the recreated network: %v", containerID, info.Containers)
	}
	if _, err := dc.InspectNetwork(ctx, netID); !docker.IsNotFound(err) {
		t.Errorf("old network %s still exists (inspect err = %v)", netID, err)
	}
	c, err := dc.InspectContainer(ctx, containerID)
	if err != nil {
		t.Fatalf("inspect container: %v", err)
	}
	ep, ok := c.NetworkSettings.Networks[name]
	if !ok || ep.NetworkID != newID {
		t.Fatalf("container's attachment = %+v, want one on %s", c.NetworkSettings.Networks, newID)
	}
	if ep.IPAddress != address {
		t.Errorf("container address after the flip = %s, want %s", ep.IPAddress, address)
	}
	if !containsString(ep.Aliases, "mydb.vpc.test") {
		t.Errorf("container aliases after the flip = %v, want mydb.vpc.test kept", ep.Aliases)
	}
}

func cidrFor(secondOctet int) string {
	return "10." + itoa(secondOctet) + ".0.0/16"
}

func addressFor(secondOctet, host int) string {
	return "10." + itoa(secondOctet) + ".0." + itoa(host)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
