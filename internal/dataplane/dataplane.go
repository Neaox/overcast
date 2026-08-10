// Package dataplane places the containers Overcast starts onto its Docker
// networks.
//
// Overcast runs two kinds of plane, and the split is the whole point of this
// package (docs/dev/container-networking.md, docs/plans/container-network-topology.md):
//
//   - The **control plane** carries Overcast's own channel to every container
//     it starts — the Lambda Runtime API, and the AWS_ENDPOINT_URL calls
//     function and task code makes back into the emulator. On AWS the Runtime
//     API lives inside the execution sandbox and the emulator's API stands in
//     for a VPC endpoint per service; both are reachable whatever VPC a
//     resource joins, so this plane is never withheld.
//   - A **data plane** carries traffic between resources: a task reaching a
//     cache node, a function reaching a database. A container has exactly one —
//     its VPC's network when it named a VPC, the default plane otherwise.
//
// Keeping them apart is what allows a VPC to restrict the second without
// severing the first, which would strand every invocation at INIT.
//
// Before this package existed each service open-coded its own answer, and the
// answers disagreed: RDS attached to two compute networks, ElastiCache to one,
// MSK to none. That divergence is the bug class this package exists to close —
// see #872.
package dataplane

import (
	"context"
	"fmt"
	"os"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/containerendpoint"
	"github.com/Neaox/overcast/internal/docker"
)

// connector is the slice of *docker.Client this package needs, so callers can
// substitute a fake and services can keep depending on an interface rather
// than the concrete client.
type connector interface {
	ConnectNetworkWithAliases(ctx context.Context, networkID, containerID string, aliases []string) error
}

// inspector is the slice of *docker.Client ContainerAddr needs.
type inspector interface {
	ConnectNetwork(ctx context.Context, networkID, containerID string) error
	InspectContainer(ctx context.Context, id string) (*docker.ContainerInspect, error)
}

// runningInContainer reports whether this process is itself containerised.
// Indirected so tests can exercise both sides on one machine.
var runningInContainer = func() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// Placement describes where one managed container belongs.
//
// The zero value places a container on the default data plane, which is what
// every resource that named no VPC wants.
type Placement struct {
	// VPCNetwork is the Docker network backing the resource's VPC. Empty when
	// the resource named no VPC, which puts it on the default data plane.
	VPCNetwork string

	// Aliases are the DNS names the container answers to on its data plane —
	// every hostname the API could hand a caller for this resource. Compute
	// (a Lambda container, an ECS task) leaves this empty: nothing resolves a
	// function by name.
	//
	// Register every name, not the canonical one. Docker aliases are
	// exact-match, and an unregistered name under a split-horizon domain does
	// not fail cleanly — it reaches Overcast's own resolver, which answers any
	// such subdomain with Overcast's address, so the caller connects to the
	// emulator on the engine's port and hangs.
	Aliases []string
}

// Primary returns the network a managed container is *created* on
// (HostConfig.NetworkMode).
//
// It is always the control plane. Docker only accepts one network at container
// create, and this is the one that must be in place from the container's first
// packet: a Lambda container that cannot reach the Runtime API never finishes
// INIT, whereas a data-plane peer it cannot reach yet is merely unreachable
// until Attach runs a moment later.
func Primary(cfg *config.Config) string {
	return cfg.ControlNetwork()
}

// PrimaryEndpoints is Primary as a NetworkingConfig, for container create.
func PrimaryEndpoints(cfg *config.Config) *docker.NetworkingConfig {
	return &docker.NetworkingConfig{
		EndpointsConfig: map[string]*docker.EndpointSettings{
			Primary(cfg): {},
		},
	}
}

// DataNetwork returns the one data plane p belongs on.
func DataNetwork(cfg *config.Config, p Placement) string {
	if p.VPCNetwork != "" {
		return p.VPCNetwork
	}
	return cfg.Network
}

// Attach connects a container to its data plane, advertising p.Aliases there.
//
// Call it after CreateContainer and before StartContainer: a container that
// starts before it is attached can race its own first outbound connection.
// Connecting is idempotent, so reconcile paths may repeat it freely.
func Attach(ctx context.Context, dc connector, cfg *config.Config, containerID string, p Placement) error {
	network := DataNetwork(cfg, p)
	if network == "" || containerID == "" {
		return nil
	}
	if err := dc.ConnectNetworkWithAliases(ctx, network, containerID, p.Aliases); err != nil {
		return fmt.Errorf("attach container to data plane %s: %w", network, err)
	}
	return nil
}

// Networks returns every plane a container placed by p sits on, control first.
// Useful for diagnostics and for callers that need the full set rather than
// the create-time/attach-time split Primary and Attach express.
func Networks(cfg *config.Config, p Placement) []string {
	return []string{Primary(cfg), DataNetwork(cfg, p)}
}

// Hostnames builds the complete alias set for one container-backed resource:
// name applied to every hostname base Overcast could mint an endpoint under,
// plus any address the stored record already advertises.
//
// The set, not the canonical name. An endpoint address is minted on the host
// the *calling client* reached Overcast on (docs/networking.md § Data-plane
// endpoints), so the same node is `x.us-east-1.cfg.localhost.overcast.sh` to
// one caller and `x.us-east-1.cfg.localhost` to another — and whichever
// process resolves the name later is often not the one that received it.
// Docker aliases are exact-match, so a name that was not registered does not
// resolve; under a split-horizon domain it does not even fail cleanly. See the
// Aliases field on Placement.
//
// advertised covers a record minted under a hostname the current configuration
// no longer lists. IP literals and loopback names are dropped — they are not
// usable as aliases and mean the wrong thing inside a container.
func Hostnames(cfg *config.Config, name func(base string) string, advertised ...string) []string {
	bases := containerendpoint.ResourceHostnames(cfg)
	names := make([]string, 0, len(bases)+len(advertised))
	for _, base := range bases {
		if n := name(base); n != "" {
			names = append(names, n)
		}
	}
	names = append(names, advertised...)
	return docker.EndpointAliases(names...)
}

// ContainerAddr returns the address *Overcast itself* uses to reach one of the
// containers it started, or "" when it should fall back to a published host
// port on loopback.
//
// This is deliberately not the address a client is handed. Overcast may be on
// the host, where only the published port is bound, or in a container beside
// the engine, where only the engine's own port is — so health checks and
// client responses need different answers, and conflating them is how an ECS
// task ends up dialling 127.0.0.1 and reaching itself.
//
// The control plane is the network used: every container Overcast starts is on
// it, whatever VPC it later joins, so this one lookup works for every service.
func ContainerAddr(ctx context.Context, dc inspector, cfg *config.Config, containerID string) string {
	if dc == nil || containerID == "" || !runningInContainer() {
		return ""
	}
	network := Primary(cfg)

	// Idempotent, and needed at all because Overcast only joins a plane when
	// it first has a reason to.
	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		_ = dc.ConnectNetwork(ctx, network, hostname)
	}

	info, err := dc.InspectContainer(ctx, containerID)
	if err != nil || info == nil {
		return ""
	}
	if ep, ok := info.NetworkSettings.Networks[network]; ok && ep.IPAddress != "" {
		return ep.IPAddress
	}
	return ""
}

// VPCResolver is the part of the EC2 service a placement decision needs.
//
// Lambda, ECS and RDS each declare a wider resolver interface of their own;
// every one of them is a superset of this, so they satisfy it structurally and
// need no adapter.
type VPCResolver interface {
	VPCNetworkStatus(ctx context.Context, vpcID string) string
	DockerNetworkForVpc(ctx context.Context, vpcID string) string
}

// Launchable reports whether a VPC in this network status can take containers.
//
// An empty status is launchable: it predates the field, and VPCs stored before
// it existed are ordinary working VPCs. The unlaunchable states are "conflict"
// (a strict-strategy CIDR collision) and "unbacked" (no Docker network), and
// anything unrecognised is treated as unlaunchable — placing into a VPC whose
// state we cannot read starts something unreachable, which is the failure this
// whole package exists to prevent.
func Launchable(status string) bool {
	switch status {
	case "", "ok", "shared", "remapped":
		return true
	default:
		return false
	}
}

// PlaceInVPC resolves vpcID to the Placement a container in it should take.
//
// A resource with no VPC, or no resolver to ask, lands on the default data
// plane — the zero Placement. A VPC that cannot take containers is an error
// rather than a silent fallback: quietly placing a resource somewhere other
// than the VPC it asked for is how an unreachable endpoint gets minted.
func PlaceInVPC(ctx context.Context, r VPCResolver, vpcID string) (Placement, error) {
	if r == nil || vpcID == "" {
		return Placement{}, nil
	}
	if status := r.VPCNetworkStatus(ctx, vpcID); !Launchable(status) {
		return Placement{}, fmt.Errorf("VPC %s is not launchable (network status=%s)", vpcID, status)
	}
	return Placement{VPCNetwork: r.DockerNetworkForVpc(ctx, vpcID)}, nil
}
