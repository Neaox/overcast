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
	"strings"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/containerendpoint"
	"github.com/overcast-sh/overcast/internal/docker"
)

// connector is the slice of *docker.Client this package needs, so callers can
// substitute a fake and services can keep depending on an interface rather
// than the concrete client.
type connector interface {
	ConnectNetworkWithAliases(ctx context.Context, networkID, containerID string, aliases []string) error
}

// reconnector is a connector that can also take a container off a network,
// which is what changing an alias set requires. Kept separate from connector so
// the many callers that only ever attach are not made to grow a method.
type reconnector interface {
	connector
	DisconnectNetwork(ctx context.Context, networkID, containerID string) error
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

// nativeLinuxDaemon is containerendpoint.NativeLinuxDaemon, indirected so
// ControlPlaneInternal is testable without a Docker daemon.
var nativeLinuxDaemon = containerendpoint.NativeLinuxDaemon

// Placement describes where one managed container belongs.
//
// The zero value places a container on the default data plane, which is what
// every resource that named no VPC wants.
type Placement struct {
	// VPCNetwork is the Docker network backing the resource's VPC. Empty when
	// the resource named no VPC, which puts it on the default data plane.
	VPCNetwork string

	// Public keeps a VPC-placed resource on the default plane as well as its
	// VPC network, so callers outside the VPC can still reach it.
	//
	// This is the escape hatch from placement, and it is deliberately spelled
	// with AWS's own fields rather than an Overcast-specific one: RDS's
	// `PubliclyAccessible`, ECS's `assignPublicIp: ENABLED`. Someone who needs
	// it locally needs the same field on AWS, so the fix they learn here is the
	// fix that works there.
	//
	// Ignored when VPCNetwork is empty — a resource already on the default
	// plane cannot be made more reachable.
	Public bool

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

// ControlPlaneInternal returns the decision function that says whether the
// control plane is created `--internal` — cut off from everything Docker did
// not put on it — and why.
//
// It should be, in principle: a container in a VPC with no internet gateway is
// on an `--internal` VPC network, and if the control plane it also sits on has
// egress then the VPC's isolation is decoration. Making it internal is what
// makes a private subnet actually private.
//
// OVERCAST_CONTROL_PLANE_INTERNAL settles it when it is `true` or `false`, and
// that is the point of the variable: the isolation of this one network decides
// whether a Lambda in a gateway-less VPC reaches the internet, and a team that
// pinned an Overcast version is entitled to the same answer on every machine.
// `auto`, the default, is autoControlPlaneInternal's host probe — right for
// most machines, but a property of the *host*, so two engineers on one image
// can legitimately differ (#1564).
//
// The decision runs once, from inside Probe, after the client is dialled and
// confirmed available but before any network is created — see
// docker.NetworkSpec.InternalMode — and Probe logs it, reason included, at
// info on every startup.
func ControlPlaneInternal(cfg *config.Config) func(ctx context.Context, dc *docker.Client) docker.InternalDecision {
	return func(ctx context.Context, dc *docker.Client) docker.InternalDecision {
		switch cfg.ControlPlaneInternal {
		case config.ControlPlaneInternalTrue:
			return withEgressWarning(docker.InternalDecision{
				Internal: true,
				Reason:   "OVERCAST_CONTROL_PLANE_INTERNAL=true",
			})
		case config.ControlPlaneInternalFalse:
			return docker.InternalDecision{
				Internal: false,
				Reason:   "OVERCAST_CONTROL_PLANE_INTERNAL=false",
			}
		case config.ControlPlaneInternalAuto:
			return withEgressWarning(autoControlPlaneInternal(ctx, dc))
		default:
			// Unreachable: config.Load rejects every other value, and a Config
			// built directly leaves the field empty. Both land on auto.
			return withEgressWarning(autoControlPlaneInternal(ctx, dc))
		}
	}
}

// ControlPlaneEgressWarning is what isolating this plane actually costs, and
// it is stated in full because the cost lands a long way from its cause: a
// function that cannot reach an external API fails minutes later, inside
// somebody's application code, as ENETUNREACH.
//
// Isolating the plane is what makes a VPC with no internet gateway genuinely
// withhold the internet, which is good fidelity. What Overcast does not yet do
// is the half of AWS's model that *grants* egress. Route tables, their
// `0.0.0.0/0` entries and NAT gateways are stored and returned as metadata and
// consulted nowhere (docs/services/ec2/limitations.md); the only thing that
// takes a VPC's own network out of `--internal` is an attached internet
// gateway, and that toggle needs to remove and recreate the network, which
// Docker refuses while containers are on it. A VPC network created before its
// gateway was attached — the ordering every CDK and CloudFormation deploy
// uses — can therefore stay `--internal` for the life of the run.
//
// When it does, this plane is the only egress path its containers have. That
// is measurable, not theoretical: a container on an `--internal` VPC network
// plus a routable control plane takes its default route from the control
// plane and reaches the internet; make the control plane internal too and it
// reaches nothing.
const ControlPlaneEgressWarning = "control plane network: internal=true — a container in a VPC whose own " +
	"network is also internal then has NO egress at all, including to real AWS endpoints, and fails with " +
	"ENETUNREACH. Overcast does not emulate NAT gateway or internet gateway routing: route tables are " +
	"metadata only, so a private-with-egress subnet is not distinguished from an isolated one " +
	"(docs/services/ec2/limitations.md). Set OVERCAST_CONTROL_PLANE_INTERNAL=false to restore egress"

// withEgressWarning attaches ControlPlaneEgressWarning to a decision that came
// out internal, whichever value produced it. Under `auto` the isolation is
// still inferred from the host, so this is the one line that tells an operator
// their machine opted them in.
func withEgressWarning(d docker.InternalDecision) docker.InternalDecision {
	if d.Internal {
		d.Warnings = append(d.Warnings, ControlPlaneEgressWarning)
	}
	return d
}

// autoControlPlaneInternal is OVERCAST_CONTROL_PLANE_INTERNAL=auto: the host
// probe, which is what alpha.37 shipped and what the default still is.
//
// Decided by the same three-row table containerendpoint.ResolveListen uses to
// pick the Runtime API's bind address (docs/plans/container-network-topology.md
// § 5), because it is the same fact asked from the other side — whether the
// address a container uses to reach Overcast is on-link on an `--internal`
// bridge, or reachable only through the route `--internal` would cut:
//
//   - Overcast containerised: always yes. Overcast is *on* the plane, so its
//     own address there is on-link regardless of platform.
//   - Overcast on a native Linux host: yes. Containers dial the control
//     network's own gateway, and an `--internal` bridge still has one — only
//     routing *beyond* the bridge is cut, and the gateway is on it.
//   - Overcast on a Docker Desktop host: no. Containers dial the host's own
//     routable address, which sits beyond the bridge — `--internal` would
//     sever exactly that path, and every invocation would strand at INIT.
//   - Anything undetermined (no Docker client yet, no default network to
//     probe): no. Getting this wrong does not degrade gracefully — the Runtime
//     API rides this plane — so an inconclusive probe keeps the safe answer
//     rather than guessing.
//
// It cannot inspect the control plane itself, which may not exist yet on a
// first run, so it asks the same question of Docker's own always-present
// default network instead (containerendpoint.NativeLinuxDaemon).
//
// Every branch names itself in the reason. The last two rows are the same
// answer for opposite causes — one probed and declined, one never got to
// probe — and telling them apart is most of what makes a surprising `auto`
// diagnosable.
func autoControlPlaneInternal(ctx context.Context, dc *docker.Client) docker.InternalDecision {
	if runningInContainer() {
		return docker.InternalDecision{
			Internal: true,
			Reason:   "auto: Overcast is containerised",
		}
	}
	if nativeLinuxDaemon(ctx, dc) {
		return docker.InternalDecision{
			Internal: true,
			Reason:   "auto: native Linux Docker daemon",
		}
	}
	return docker.InternalDecision{
		Internal: false,
		Reason:   "auto: Docker Desktop, or a daemon this host could not probe",
	}
}

// PlaneSpecs is the set of networks the Docker supervisor ensures at startup:
// the control plane, with its isolation decided by ControlPlaneInternal, and
// the default data plane.
//
// Per-VPC networks are absent on purpose — EC2 creates those on demand, one per
// VPC, and marks them internal or not according to whether the VPC has an
// internet gateway.
//
// One definition, used by both the supervisor and Lambda's independent probe,
// so the two cannot disagree about what exists or how it is isolated.
func PlaneSpecs(cfg *config.Config) []docker.NetworkSpec {
	return []docker.NetworkSpec{
		{Name: cfg.Network},
		{Name: Primary(cfg), InternalMode: ControlPlaneInternal(cfg)},
	}
}

// PrimaryEndpoints is Primary as a NetworkingConfig, for container create.
func PrimaryEndpoints(cfg *config.Config) *docker.NetworkingConfig {
	return &docker.NetworkingConfig{
		EndpointsConfig: map[string]*docker.EndpointSettings{
			Primary(cfg): {},
		},
	}
}

// DataNetwork returns the plane p's resource is addressed on — its VPC's
// network when it named a VPC, the default plane otherwise.
//
// This is the *primary* plane, not the only one it can be reached on today;
// see DataNetworks.
func DataNetwork(cfg *config.Config, p Placement) string {
	if p.VPCNetwork != "" {
		return p.VPCNetwork
	}
	return cfg.Network
}

// DataNetworks returns every data plane a container placed by p joins.
//
// **A resource that named a VPC gets that VPC's network and nothing else.**
// That is the point of naming one: on AWS, placement subtracts. A function with
// a VpcConfig gives up the internet it had and reaches only what its ENIs route
// to; it cannot reach another VPC without peering, and it cannot reach the AWS
// APIs at all without a NAT gateway or a VPC endpoint. Somebody who wrote a VPC
// into their stack meant it, and an emulator that quietly ignores the
// declaration withholds the most common AWS networking mistake there is until
// the code reaches somewhere the failure costs a five-minute timeout with no
// explanation attached.
//
// Two things make that restriction safe to impose here, and neither was true
// before phase 5:
//
//   - A connection this forbids fails by *name*, not by hanging. Overcast's
//     resolver refuses a data-plane name the caller cannot reach and names both
//     sides (internal/dns, internal/dataplane/guard.go). Without that, the same
//     restriction produced a client waiting on a port nothing speaks.
//   - The way out is an AWS field rather than an Overcast one — see Public.
//
// The control plane is unaffected and is not returned here: Overcast's own API
// and the Lambda Runtime API stay reachable from inside any VPC, which is the
// one divergence from AWS this model keeps on purpose. Read it as every VPC
// having an interface endpoint for every service; the alternative is a function
// that cannot finish INIT.
func DataNetworks(cfg *config.Config, p Placement) []string {
	if p.VPCNetwork == "" || p.VPCNetwork == cfg.Network {
		return []string{cfg.Network}
	}
	if p.Public || !enforceable(cfg) {
		return []string{p.VPCNetwork, cfg.Network}
	}
	return []string{p.VPCNetwork}
}

// enforceable reports whether a forbidden connection would fail in a way the
// user can act on. Placement only restricts where it does.
//
// The whole argument for restricting is that the guard turns a connection a VPC
// forbids into a named refusal. That guard lives behind Overcast's resolver, and
// the resolver does not always run: it needs upstream servers to forward to,
// which it reads from /etc/resolv.conf, which does not exist on a native Windows
// or macOS host. There it declines to start, containers are never pointed at it,
// and the guard never sees a query — see internal/router/container_dns.go.
//
// Restricting anyway on those hosts would deliver exactly the failure phase 5
// existed to remove: a name resolving to Overcast's own address, and a client
// hanging on a port nothing speaks. So enforcement follows the resolver rather
// than the calendar, and a host that cannot diagnose the failure does not get
// the restriction. Run Overcast in a container to have both.
func enforceable(cfg *config.Config) bool {
	return cfg.DNSListening
}

// AttachAdopted is Attach for a container Overcast did not create in this run —
// one reused after a restart.
//
// It joins the control plane too. A container created by this version was
// created there, but one adopted from an earlier version was created on a
// per-service network that no longer exists in the model, and without this it
// would never be on the plane ContainerAddr inspects — leaving a containerised
// Overcast dialling a published host port it cannot reach, so health checks
// never pass and the resource never leaves "creating".
func AttachAdopted(ctx context.Context, dc connector, cfg *config.Config, containerID string, p Placement) error {
	if dc == nil || containerID == "" {
		return nil
	}
	if err := dc.ConnectNetworkWithAliases(ctx, Primary(cfg), containerID, nil); err != nil {
		return fmt.Errorf("attach adopted container to the control plane: %w", err)
	}
	return Attach(ctx, dc, cfg, containerID, p)
}

// Attach connects a container to its data planes, advertising p.Aliases on
// each.
//
// Call it after CreateContainer and before StartContainer: a container that
// starts before it is attached can race its own first outbound connection —
// application code that opens a database connection on the first line runs
// before the name it dials resolves. The one caller that cannot obey this is
// ECS's awsvpc path, which reads back the address Docker assigned and so needs
// the container running.
//
// Safe to repeat: a container already on a plane is left as it is, so reconcile
// and container-reuse paths may call this freely.
func Attach(ctx context.Context, dc connector, cfg *config.Config, containerID string, p Placement) error {
	if dc == nil || containerID == "" {
		return nil
	}
	for _, network := range DataNetworks(cfg, p) {
		if network == "" {
			continue
		}
		if err := dc.ConnectNetworkWithAliases(ctx, network, containerID, p.Aliases); err != nil {
			return fmt.Errorf("attach container to data plane %s: %w", network, err)
		}
	}
	return nil
}

// Reattach replaces the set of DNS names a running container answers to on its
// data planes.
//
// Attach cannot do this, and cannot be made to. Docker fixes a container's
// aliases when it joins a network and rejects a second connect for one that is
// already there — which Attach deliberately swallows as success, so calling it
// again with a different set silently keeps the old one. Leaving the network
// and rejoining it is the only way to change them.
//
// The container keeps running throughout, but it is off the plane for the
// moment between the two calls, and Docker drops its existing connections on
// that plane when it leaves. That is deliberate rather than tolerated: this is
// for moving a name from one container to another — an Aurora writer promotion
// — and on AWS a failover breaks open connections in exactly the same way. A
// caller that only needs to add a container to a plane wants Attach.
//
// A container that is not on a plane it was expected to be on is not an error.
// There is nothing to leave, and the rejoin is what the caller actually wants;
// refusing here would turn a promotion that raced a container restart into a
// name that resolves nowhere.
func Reattach(ctx context.Context, dc reconnector, cfg *config.Config, containerID string, p Placement) error {
	if dc == nil || containerID == "" {
		return nil
	}
	for _, network := range DataNetworks(cfg, p) {
		if network == "" {
			continue
		}
		if err := dc.DisconnectNetwork(ctx, network, containerID); err != nil && !isNotConnected(err) {
			return fmt.Errorf("detach container from data plane %s: %w", network, err)
		}
	}
	return Attach(ctx, dc, cfg, containerID, p)
}

// isNotConnected reports whether err is Docker refusing a disconnect because
// the container was not on that network — the mirror of the client's tolerance
// for a connect that finds it already there.
func isNotConnected(err error) bool {
	return strings.Contains(err.Error(), "is not connected to network")
}

// Networks returns every plane a container placed by p sits on, control first.
// Useful for diagnostics and for callers that need the full set rather than
// the create-time/attach-time split Primary and Attach express.
func Networks(cfg *config.Config, p Placement) []string {
	return append([]string{Primary(cfg)}, DataNetworks(cfg, p)...)
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
