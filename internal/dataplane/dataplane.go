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
// The egress mode decides it. `none` isolates every network Overcast creates,
// this one included; `open`, the default, isolates none of them. That is a
// change of authority rather than of mechanism: until #1564 this network's
// isolation was inferred from the *host* — is the daemon native Linux, is
// Overcast containerised — which made the same pinned version behave
// differently on two machines, and which answered a question about egress with
// a fact about the Runtime API. Those are now two separate questions with two
// separate answers; see runtimeAPIReachableOnInternalPlane.
//
// OVERCAST_CONTROL_PLANE_INTERNAL still wins where it is pinned, so a
// configuration written against #1566 keeps its answer, and setting it earns a
// deprecation notice naming the mode that means the same thing.
//
// The decision runs once, from inside Probe, after the client is dialled and
// confirmed available but before any network is created — see
// docker.NetworkSpec.InternalMode — and Probe logs it, reason included, at info
// on every startup.
func ControlPlaneInternal(cfg *config.Config) func(ctx context.Context, dc *docker.Client) docker.InternalDecision {
	return func(ctx context.Context, dc *docker.Client) docker.InternalDecision {
		d := docker.InternalDecision{
			Internal: egressMode(cfg) == config.VPCEgressNone,
			Reason:   "OVERCAST_VPC_EGRESS=" + string(egressMode(cfg)),
		}

		// The deprecated pin, applied on top. It names one network, which is
		// why it is deprecated: egress is a property of the topology and this
		// only ever settled a third of it.
		switch cfg.ControlPlaneInternal {
		case config.ControlPlaneInternalTrue:
			d.Internal, d.Reason = true, "OVERCAST_CONTROL_PLANE_INTERNAL=true"
		case config.ControlPlaneInternalFalse:
			d.Internal, d.Reason = false, "OVERCAST_CONTROL_PLANE_INTERNAL=false"
		case config.ControlPlaneInternalAuto:
		}
		if cfg.ControlPlaneInternalSet {
			d.Warnings = append(d.Warnings, deprecationNotice(cfg))
		}

		if !d.Internal {
			return d
		}

		// The one thing that can still overrule an isolated control plane: a
		// host where isolating it would strand every invocation at INIT. This
		// is the only place the host probe is consulted, and it decides nothing
		// about egress — see runtimeAPIReachableOnInternalPlane.
		if !runtimeAPIReachableOnInternalPlane(ctx, dc) {
			return docker.InternalDecision{
				Internal: false,
				Reason:   d.Reason + ", overridden: an internal control plane would sever the Runtime API on this host",
				Warnings: append(d.Warnings, ControlPlaneMustStayRoutableWarning),
			}
		}

		// Isolation asked for by the mode is the mode working as documented and
		// needs no warning. Isolation arrived at by the deprecated pin while the
		// mode says `open` is a contradiction the operator should see.
		if egressMode(cfg) != config.VPCEgressNone {
			d.Warnings = append(d.Warnings, ControlPlaneEgressWarning)
		}
		return d
	}
}

// DataPlaneInternal returns the decision function for the default data plane —
// the network every container that named no VPC joins.
//
// It exists at all because `none` has to mean none. Before egress modes this
// plane was never `--internal`, so a machine could isolate its control plane
// and every VPC network and still have every non-VPC function reach the
// internet through this one: "hermetic" that leaked on the most common
// placement there is.
func DataPlaneInternal(cfg *config.Config) func(ctx context.Context, dc *docker.Client) docker.InternalDecision {
	return func(_ context.Context, _ *docker.Client) docker.InternalDecision {
		return docker.InternalDecision{
			Internal: egressMode(cfg) == config.VPCEgressNone,
			Reason:   "OVERCAST_VPC_EGRESS=" + string(egressMode(cfg)),
		}
	}
}

// VPCNetworkInternal reports whether the Docker network backing one VPC is
// created `--internal`.
//
// hasInternetGateway is what the VPC's own template says, and under the modes
// that exist today it changes nothing — which is the honest position rather
// than an oversight. `open` grants egress everywhere and `none` withholds it
// everywhere; the mode that would consult the template is `routed`, and
// `routed` needs route tables, which Overcast does not read
// (config.VPCEgressRouted). Deriving isolation from the gateway alone, as
// Overcast did before, delivers only the withholding half of AWS's model: a
// private subnet behind a NAT gateway becomes indistinguishable from an
// isolated one, and a stack that works on AWS fails here with no template
// change that helps.
//
// The parameter stays because `routed` will need it, and because a caller that
// has the fact should go on passing it — this is the seam that mode reads it at.
func VPCNetworkInternal(cfg *config.Config, hasInternetGateway bool) bool {
	_ = hasInternetGateway
	return egressMode(cfg) == config.VPCEgressNone
}

// VPCNetworkSpec is the full desired state of the Docker network backing one
// VPC, so a per-VPC network is verified against the same field-by-field
// comparison the planes are (docker.EnsureNetwork) rather than being the one
// network class nobody checks.
//
// vpcID names the VPC, subnet is the CIDR the strategy picked (empty leaves
// IPAM to Docker), hasInternetGateway is what the template says — see
// VPCNetworkInternal for why that currently changes nothing — and owner is the
// EC2 service's instance identity (serviceutil.InstanceDomain).
//
// owner is passed in rather than derived from cfg because it has to be the
// same store-scoped identity every other Docker resource is stamped with
// (docker.LabelInstance). A VPC network is the one Overcast resource whose name
// comes from an emulated resource id rather than from configuration, so two
// instances on one daemon can mint the same name from unrelated state — and it
// is the label, not the name, that decides who may remove it. An instance whose
// identity cannot be established stamps nothing and, by the same rule, removes
// nothing.
func VPCNetworkSpec(cfg *config.Config, vpcID, subnet, owner string, hasInternetGateway bool) docker.NetworkSpec {
	labels := docker.ManagedLabels("ec2", vpcID)
	labels["overcast.vpc-id"] = vpcID
	if owner != "" {
		labels[docker.LabelInstance] = owner
	}
	return docker.NetworkSpec{
		Name:       cfg.VPCNetwork(vpcID),
		Internal:   VPCNetworkInternal(cfg, hasInternetGateway),
		Subnet:     subnet,
		Labels:     labels,
		Owner:      owner,
		Version:    cfg.Version,
		EgressMode: string(egressMode(cfg)),
	}
}

// ControlPlaneEgressWarning is what isolating this plane costs when the egress
// mode did not ask for it, stated in full because the cost lands a long way
// from its cause: a function that cannot reach an external API fails minutes
// later, inside somebody's application code, as ENETUNREACH.
const ControlPlaneEgressWarning = "control plane network: internal=true while OVERCAST_VPC_EGRESS is not " +
	"`none` — every container Overcast starts loses its route out through this plane, and a container in " +
	"a VPC whose own network is also internal then has NO egress at all, including to real AWS endpoints. " +
	"OVERCAST_CONTROL_PLANE_INTERNAL is deprecated: set OVERCAST_VPC_EGRESS=none to mean this deliberately, " +
	"or unset it to restore egress"

// ControlPlaneMustStayRoutableWarning is the shortfall on a host where an
// isolated control plane cannot be delivered.
//
// It is a warning rather than a refusal to start because the alternatives are
// worse in both directions: isolating anyway strands every invocation at INIT,
// and refusing to start turns a partly-achievable hermetic mode into no mode at
// all on the most common developer platform. What `none` still delivers here is
// every data plane isolated; what it cannot deliver is this one network, and
// containers keep a route out through it. Run Overcast containerised, or
// against a native Linux daemon, to get the whole of it.
const ControlPlaneMustStayRoutableWarning = "OVERCAST_VPC_EGRESS=none asked for an isolated control " +
	"plane, but on this host containers reach the Lambda Runtime API at the host's own routable address, " +
	"which `--internal` would sever — every invocation would strand at INIT. The control plane was left " +
	"routable, so containers still have a route out through it and this stack is NOT hermetic. Every data " +
	"plane is isolated as asked. Run Overcast containerised, or against a native Linux Docker daemon, for " +
	"the whole of `none`"

// deprecationNotice names the mode that expresses what the deprecated variable
// was set to mean, so the operator is told what to write instead rather than
// only that they are wrong.
func deprecationNotice(cfg *config.Config) string {
	replacement := "OVERCAST_VPC_EGRESS=open (the default)"
	if cfg.ControlPlaneInternal == config.ControlPlaneInternalTrue {
		replacement = "OVERCAST_VPC_EGRESS=none"
	}
	return "OVERCAST_CONTROL_PLANE_INTERNAL is deprecated and will be removed: it pins one network's " +
		"isolation, and egress is a property of the whole topology — a container takes its default route " +
		"from whichever of its networks is routable, so isolating one of them settles nothing. Set " +
		replacement + " instead. The setting is still honoured for now"
}

// egressMode is cfg.VPCEgress with the zero value read as the default, so a
// Config built directly in a test reports the mode Load would have given it.
func egressMode(cfg *config.Config) config.VPCEgressMode {
	if cfg == nil || cfg.VPCEgress == "" {
		return config.VPCEgressOpen
	}
	return cfg.VPCEgress
}

// runtimeAPIReachableOnInternalPlane reports whether containers could still
// reach Overcast — the Lambda Runtime API above all — if the control plane were
// `--internal`.
//
// This is the probe alpha.37 used to decide egress, and decoupling it is most of
// what #1564 was really about. It is a fact about the *host*: which address a
// container dials to reach a server on this machine, and whether that address
// survives an isolated bridge. It is not a fact about the template, the VPC, or
// what the operator wants, and it never should have decided any of them. Egress
// is decided by OVERCAST_VPC_EGRESS; this decides only whether the isolation
// that mode asks for can be applied to the one network the Runtime API rides.
//
// It is the same three-row table containerendpoint.ResolveListen uses to pick
// the Runtime API's bind address, asked from the other side:
//
//   - Overcast containerised: yes. Overcast is *on* the plane, so its own
//     address there is on-link regardless of platform.
//   - Overcast on a native Linux host: yes. Containers dial the network's own
//     gateway, and an `--internal` bridge still has one — only routing *beyond*
//     the bridge is cut, and the gateway is on it.
//   - Overcast on a Docker Desktop host: no. Containers dial the host's own
//     routable address, which sits beyond the bridge; `--internal` severs
//     exactly that path.
//   - Undetermined (no client, no default network to probe): no. Getting this
//     wrong does not degrade gracefully — the Runtime API rides this plane — so
//     an inconclusive probe keeps the answer that cannot strand anything.
//
// It cannot inspect the control plane itself, which may not exist yet on a
// first run, so it asks the same question of Docker's own always-present
// default network (containerendpoint.NativeLinuxDaemon).
func runtimeAPIReachableOnInternalPlane(ctx context.Context, dc *docker.Client) bool {
	return runningInContainer() || nativeLinuxDaemon(ctx, dc)
}

// PlaneSpecs is the set of networks the Docker supervisor ensures at startup:
// the default data plane and the control plane, each with its full desired
// state rather than only its name.
//
// The specs are complete on purpose. Docker's create-network call returns an
// existing network unchanged, so a plane created by an older version, a
// different mode, or by hand keeps every setting it was born with while looking
// present and correct — see docker.EnsureNetwork, which compares each of these
// fields against the live network on every startup.
//
// Per-VPC networks are absent here on purpose: EC2 creates those on demand, one
// per VPC, from VPCNetworkSpec.
//
// One definition, used by both the supervisor and Lambda's independent probe,
// so the two cannot disagree about what exists or how it is isolated.
func PlaneSpecs(cfg *config.Config) []docker.NetworkSpec {
	return []docker.NetworkSpec{
		{
			Name:         cfg.Network,
			InternalMode: DataPlaneInternal(cfg),
			Labels:       docker.ManagedLabels(docker.ServiceCore, cfg.Network),
			Owner:        cfg.Network,
			Version:      cfg.Version,
			EgressMode:   string(egressMode(cfg)),
		},
		{
			Name:         Primary(cfg),
			InternalMode: ControlPlaneInternal(cfg),
			Labels:       docker.ManagedLabels(docker.ServiceCore, Primary(cfg)),
			Owner:        cfg.Network,
			Version:      cfg.Version,
			EgressMode:   string(egressMode(cfg)),
		},
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
