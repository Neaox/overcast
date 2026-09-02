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
	"strconv"
	"strings"
	"sync"

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

// egressConnector is a connector that can rank a network as the container's
// default-route source (docker.EndpointSettings.GwPriority), which joining an
// egress network needs. Optional: a connector without it — a test fake — joins
// the egress network unranked, and Docker's name-order tie-break decides.
type egressConnector interface {
	ConnectNetworkWithConfig(ctx context.Context, networkID, containerID string, cfg *docker.EndpointSettings) error
}

// EgressGatewayPriority is the docker.EndpointSettings.GwPriority a container
// joins its VPC's egress network with. Any positive value beats the zero every
// other attachment carries, so a container that is also on a routable plane —
// the default plane, when its resource is Public; the control plane, on a host
// where that could not be isolated — still takes its default route from the
// egress network its subnet's route table chose. See Placement.EgressNetwork.
const EgressGatewayPriority = 10

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

	// EgressNetwork is the routable bridge that gives this container its
	// default route, joined in addition to VPCNetwork. Set only under
	// OVERCAST_VPC_EGRESS=routed, and only when the resource's subnet has a
	// `0.0.0.0/0` route to an internet or NAT gateway; empty means the
	// container has whatever route out its other networks give it — none, in
	// `routed`, which is the mode's point.
	//
	// It is a second network rather than a routable VPCNetwork because the
	// VPC's plane has to stay one network for every container in the VPC,
	// whatever their subnets route to: an isolated database and a NAT-routed
	// function in one VPC reach each other on AWS, and they could not if each
	// egress class were its own bridge. So VPCNetwork carries the names and
	// the intra-VPC traffic, and this carries only the route out. No aliases
	// are registered on it.
	EgressNetwork string

	// VPCID and SubnetIDs are where the resource asked to be, kept on the
	// placement so Attach can record it (see PlacementRecorder) and the EC2
	// service can revisit the egress decision when a route table changes.
	VPCID     string
	SubnetIDs []string

	// recorder is told which container this placement was applied to, when
	// the resolver that made it wants to know. Set by PlaceInSubnets.
	recorder PlacementRecorder
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
		// `none` and `routed` both isolate it. `routed` has to: a container
		// takes its default route from whichever of its networks is routable,
		// so a routable control plane would hand every container a route out
		// whatever its route table says — measured end to end in #1571, where
		// a function in an isolated subnet reached real AWS through exactly
		// this network. Egress under `routed` comes from the per-VPC egress
		// network instead (Placement.EgressNetwork).
		d := docker.InternalDecision{
			Internal: EgressMode(cfg) == config.VPCEgressNone || EgressMode(cfg) == config.VPCEgressRouted,
			Reason:   "OVERCAST_VPC_EGRESS=" + string(EgressMode(cfg)),
		}

		// The deprecated pin, applied on top. It names one network, which is
		// why it is deprecated: egress is a property of the topology and this
		// only ever settled a third of it.
		pin, pinSet := cfg.LegacyControlPlaneInternal()
		switch pin {
		case config.ControlPlaneInternalTrue:
			d.Internal, d.Reason = true, "OVERCAST_CONTROL_PLANE_INTERNAL=true"
		case config.ControlPlaneInternalFalse:
			d.Internal, d.Reason = false, "OVERCAST_CONTROL_PLANE_INTERNAL=false"
		case config.ControlPlaneInternalAuto:
		}
		if pinSet {
			d.Warnings = appendOnce(&deprecationSaid, d.Warnings, deprecationNotice(cfg))
		}

		if !d.Internal {
			return d
		}

		// The one thing that can still overrule an isolated control plane: a
		// host where isolating it would strand every invocation at INIT. This
		// is the only place the host probe is consulted, and it decides nothing
		// about egress — see runtimeAPIReachableOnInternalPlane.
		if !runtimeAPIReachableOnInternalPlane(ctx, dc) {
			shortfall := ControlPlaneMustStayRoutableWarning
			if EgressMode(cfg) == config.VPCEgressRouted {
				shortfall = RoutedControlPlaneMustStayRoutableWarning
			}
			return docker.InternalDecision{
				Internal: false,
				Reason:   d.Reason + ", overridden: an internal control plane would sever the Runtime API on this host",
				Warnings: appendOnce(&shortfallSaid, d.Warnings, shortfall),
			}
		}

		// Isolation asked for by the mode is the mode working as documented and
		// needs no warning. Isolation arrived at by the deprecated pin while the
		// mode says `open` is a contradiction the operator should see.
		if EgressMode(cfg) == config.VPCEgressOpen {
			d.Warnings = appendOnce(&egressWarningSaid, d.Warnings, ControlPlaneEgressWarning)
		}
		return d
	}
}

// Internal returns the decision function for the default data plane —
// the network every container that named no VPC joins.
//
// It exists at all because `none` has to mean none. Before egress modes this
// plane was never `--internal`, so a machine could isolate its control plane
// and every VPC network and still have every non-VPC function reach the
// internet through this one: "hermetic" that leaked on the most common
// placement there is.
func Internal(cfg *config.Config) func(ctx context.Context, dc *docker.Client) docker.InternalDecision {
	return func(_ context.Context, _ *docker.Client) docker.InternalDecision {
		d := docker.InternalDecision{
			Internal: EgressMode(cfg) == config.VPCEgressNone,
			Reason:   "OVERCAST_VPC_EGRESS=" + string(EgressMode(cfg)),
		}
		// `routed` leaves this plane routable — the resources that named no
		// VPC, and those in a default VPC, have egress on AWS too. That is
		// only safe while a VPC-placed container is held to its VPC network
		// alone. Where it is not, this plane is a route out for every
		// container in every subnet, and the mode cannot withhold. Said here
		// because this is the network that carries it; cfg.DNSListening is
		// settled before Docker is probed (router.New starts the resolver
		// first), so the answer is the one that will hold.
		if EgressMode(cfg) == config.VPCEgressRouted && !enforceable(cfg) {
			d.Warnings = appendOnce(&routedPlacementSaid, d.Warnings, RoutedPlacementNotEnforcedWarning)
		}
		return d
	}
}

// VPCNetworkInternal reports whether the Docker network backing one VPC is
// created `--internal`.
//
// Under `none` it always is: the mode's promise is that nothing Overcast starts
// reaches outside the machine, and a routable VPC bridge would be a hole in it.
//
// Under `open` the VPC's own internet gateway decides, exactly as it did before
// egress modes — and that costs `open` nothing, which is the part worth being
// clear about. A container in a VPC sits on the control plane as well as on its
// VPC network, and Docker gives it a default route from whichever of them is
// routable. Under `open` the control plane is routable, so a container in a
// gateway-less VPC has full egress whether its own bridge is `--internal` or
// not. That is measured, not assumed: an end-to-end matrix over every VPC shape
// found a Lambda in an isolated subnet, on a correctly `Internal=true` network,
// reaching checkip.amazonaws.com and getting a 403 from real
// sts.us-east-1 — packets left the machine.
//
// So the flag stays honest about the template rather than being flattened, the
// gateway machinery that keeps it true stays exercised (#1570), and the flag no
// longer *decides* egress on its own, which is what made a private-with-NAT
// subnet indistinguishable from an isolated one: the mode decides that. The
// flag itself still differs per network under `open`, which is the point of
// keeping it.
//
// Under `routed` the VPC's plane is always `--internal`, gateway or not. It is
// the network every container in the VPC shares, and a route out is not a
// property of the VPC but of each subnet's route table — so egress is carried
// by a second, per-VPC network that only the containers whose subnet grants it
// join (VPCEgressNetworkSpec, Placement.EgressNetwork). The gateway fact is
// still recorded on the plane, for the readers that compute this same
// decision from labels alone.
func VPCNetworkInternal(cfg *config.Config, hasInternetGateway bool) bool {
	switch EgressMode(cfg) {
	case config.VPCEgressNone, config.VPCEgressRouted:
		return true
	case config.VPCEgressOpen:
		return !hasInternetGateway
	default:
		// Unreachable: Load rejects any other value at startup. Spelled out
		// rather than folded into the case above so the exhaustiveness check
		// keeps a mode added later from silently inheriting `open`'s answer.
		return !hasInternetGateway
	}
}

// VPCNetwork describes one VPC's Docker network to VPCNetworkSpec.
//
// A struct rather than six positional parameters because two of them are bools
// that mean opposite-sounding things — Internal and HasInternetGateway — and a
// call site that transposes them compiles and is wrong in the direction nobody
// checks.
type VPCNetwork struct {
	// VPCID names the VPC, and through cfg.VPCNetwork the Docker network.
	VPCID string

	// Subnet is the CIDR the strategy picked. Empty leaves IPAM to Docker.
	Subnet string

	// Owner is the EC2 service's instance identity (serviceutil.InstanceDomain),
	// stamped into docker.LabelInstance.
	//
	// Passed in rather than derived from cfg because it has to be the same
	// store-scoped identity every other Docker resource is stamped with. A VPC
	// network is the one Overcast resource whose name comes from an emulated
	// resource id rather than from configuration, so two instances on one daemon
	// can mint the same name — and it is the label, not the name, that decides
	// who may remove it. An instance whose identity cannot be established stamps
	// nothing and, by the same rule, removes nothing.
	Owner string

	// Internal is the isolation to create the network with. The caller decides
	// it, because the caller is what has the gateway fact — see
	// VPCNetworkInternal, which is the function that turns the fact into this.
	Internal bool

	// HasInternetGateway is that fact, recorded on the network as
	// docker.LabelGatewayAttached so a reader with no state store can compute
	// the same desired state this call did.
	HasInternetGateway bool
}

// VPCNetworkSpec is the full desired state of the Docker network backing one
// VPC, so a per-VPC network is verified against the same field-by-field
// comparison the planes are (docker.EnsureNetwork) rather than being the one
// network class nobody checks.
func VPCNetworkSpec(cfg *config.Config, n VPCNetwork) docker.NetworkSpec {
	labels := docker.ManagedLabels("ec2", n.VPCID)
	labels["overcast.vpc-id"] = n.VPCID
	labels[docker.LabelGatewayAttached] = strconv.FormatBool(n.HasInternetGateway)
	labels[docker.LabelVPCRole] = docker.VPCRolePlane
	if n.Owner != "" {
		labels[docker.LabelInstance] = n.Owner
	}
	return docker.NetworkSpec{
		Name:       cfg.VPCNetwork(n.VPCID),
		Internal:   n.Internal,
		Subnet:     n.Subnet,
		Labels:     labels,
		Owner:      n.Owner,
		Version:    cfg.Version,
		EgressMode: string(EgressMode(cfg)),
	}
}

// VPCEgressNetwork describes one VPC's egress network to VPCEgressNetworkSpec.
type VPCEgressNetwork struct {
	// VPCID names the VPC, and through cfg.VPCEgressNetwork the Docker network.
	VPCID string

	// Subnet is the /24 the EC2 service carved for it from
	// config.VPCEgressPool. Always pinned: an egress network that drew on
	// Docker's own address pools would count against the ~31-network ceiling
	// that made this mode unsafe to ship (#1571).
	Subnet string

	// Owner is the EC2 service's instance identity, as on VPCNetwork.
	Owner string
}

// VPCEgressNetworkSpec is the full desired state of the routable bridge that
// carries the default route for the containers in one VPC whose subnet's route
// table grants egress, under OVERCAST_VPC_EGRESS=routed.
//
// It is never `--internal` — that is its whole job — and it pins IP
// masquerading on, because that option is what actually carries egress on a
// bridge: a network with it off looks routable and behaves isolated, which is
// the drift class docker.NetworkSpec exists to catch. It carries the same
// resource and ownership labels as the VPC's plane plus docker.LabelVPCRole so
// a reader can tell the two apart without parsing the name.
func VPCEgressNetworkSpec(cfg *config.Config, n VPCEgressNetwork) docker.NetworkSpec {
	labels := docker.ManagedLabels("ec2", n.VPCID)
	labels["overcast.vpc-id"] = n.VPCID
	labels[docker.LabelVPCRole] = docker.VPCRoleEgress
	if n.Owner != "" {
		labels[docker.LabelInstance] = n.Owner
	}
	return docker.NetworkSpec{
		Name:       cfg.VPCEgressNetwork(n.VPCID),
		Internal:   false,
		Subnet:     n.Subnet,
		Options:    map[string]string{docker.OptionIPMasquerade: "true"},
		Labels:     labels,
		Owner:      n.Owner,
		Version:    cfg.Version,
		EgressMode: string(EgressMode(cfg)),
	}
}

// VPCNetworkEgressReason says, in one line, what a VPC network's isolation
// means for the containers on it.
//
// It exists because `Internal=true` beside a container that plainly has egress
// is the confusion #1564 was about, and prose in the docs is not where anyone
// meets it. Under `open` a VPC with no internet gateway keeps an `--internal`
// bridge — honest about the template — while its containers reach the internet
// through the routable control plane they are also on. Nothing in `docker
// network inspect` will ever explain that, so Overcast says it where it is
// read: here, in the startup log, and on `overcast network status`.
func VPCNetworkEgressReason(cfg *config.Config, internal bool) string {
	mode := EgressMode(cfg)
	switch {
	case mode == config.VPCEgressNone:
		return "OVERCAST_VPC_EGRESS=none: no egress from this network or any other"
	case mode == config.VPCEgressRouted:
		return "OVERCAST_VPC_EGRESS=routed: this is the VPC's internal plane, which every container " +
			"in the VPC joins; a container whose subnet has a 0.0.0.0/0 route to an internet or NAT " +
			"gateway also joins the VPC's egress network (" + config.VPCEgressNetworkSuffix + ") and " +
			"takes its default route from there — one with no such route has no route out"
	case !internal:
		return "OVERCAST_VPC_EGRESS=" + string(mode) + ": the VPC has an internet gateway, so this " +
			"network is routable and its containers have egress directly"
	default:
		return "OVERCAST_VPC_EGRESS=" + string(mode) + ": the VPC has no internet gateway, so this " +
			"network is internal — its containers still have egress, through the control plane (" +
			Primary(cfg) + ") they are also on"
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

// RoutedControlPlaneMustStayRoutableWarning is ControlPlaneMustStayRoutableWarning
// for `routed`, whose shortfall is a different one: not a hermetic stack that
// leaks, but a route-table decision that cannot withhold. With the control
// plane routable, a container in a subnet with no default route still has one
// — through this network — so `routed` grants everywhere it should withhold
// and the missing NAT gateway it exists to catch goes uncaught on this host.
const RoutedControlPlaneMustStayRoutableWarning = "OVERCAST_VPC_EGRESS=routed asked for an isolated control " +
	"plane, but on this host containers reach the Lambda Runtime API at the host's own routable address, " +
	"which `--internal` would sever — every invocation would strand at INIT. The control plane was left " +
	"routable, so every container has a route out through it whatever its subnet's route table says: " +
	"egress is granted where the template grants it AND where it withholds it. Run Overcast containerised, " +
	"or against a native Linux Docker daemon, for `routed` to withhold egress from a subnet with no default route"

// Each of these fires at most once per process.
//
// The control plane's InternalMode runs once per docker.Probe, and Probe is
// called by the router's supervisor, again by Lambda's own probe, and again by
// awaitDockerProbe after a reconnect — so an operator with the deprecated
// variable set saw the notice at least twice on a normal boot. A deprecation
// notice seen twice is one being tuned out, which is the whole argument for
// firing it only where it was actually set.
var (
	deprecationSaid     sync.Once
	shortfallSaid       sync.Once
	egressWarningSaid   sync.Once
	routedPlacementSaid sync.Once
)

// resetWarningsOnce re-arms the once-per-process guards. Tests use it: they
// exercise the same decision many times in one process, and a guard whose whole
// job is to fire once would otherwise make every case after the first assert
// against a warning that was correctly withheld.
func resetWarningsOnce() {
	deprecationSaid = sync.Once{}
	shortfallSaid = sync.Once{}
	egressWarningSaid = sync.Once{}
	routedPlacementSaid = sync.Once{}
}

// appendOnce adds warning to warnings the first time it is reached in this
// process, and leaves the list alone afterwards. The decision itself is
// unchanged — only whether it is said again.
func appendOnce(once *sync.Once, warnings []string, warning string) []string {
	said := false
	once.Do(func() { said = true })
	if !said {
		return warnings
	}
	return append(warnings, warning)
}

// deprecationNotice names the mode that expresses what the deprecated variable
// was set to mean, so the operator is told what to write instead rather than
// only that they are wrong.
func deprecationNotice(cfg *config.Config) string {
	replacement := "OVERCAST_VPC_EGRESS=open (the default)"
	if pin, _ := cfg.LegacyControlPlaneInternal(); pin == config.ControlPlaneInternalTrue {
		replacement = "OVERCAST_VPC_EGRESS=none"
	}
	return "OVERCAST_CONTROL_PLANE_INTERNAL is deprecated and will be removed: it pins one network's " +
		"isolation, and egress is a property of the whole topology — a container takes its default route " +
		"from whichever of its networks is routable, so isolating one of them settles nothing. Set " +
		replacement + " instead. The setting is still honoured for now"
}

// EgressMode is cfg.VPCEgress with the zero value read as the default, so a
// Config built directly in a test reports the mode Load would have given it.
func EgressMode(cfg *config.Config) config.VPCEgressMode {
	if cfg == nil || cfg.VPCEgress == "" {
		return config.VPCEgressOpen
	}
	return cfg.VPCEgress
}

// Routed reports whether egress is decided per subnet from its route table.
func Routed(cfg *config.Config) bool { return EgressMode(cfg) == config.VPCEgressRouted }

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
// Neither plane carries an owner. docker.LabelInstance is the EC2 service's
// store-scoped identity, and Probe runs before any store is read — so a plane
// is scoped by its *name*, which is what OVERCAST_NETWORK is for, and two
// instances sharing that name share the plane deliberately. Stamping
// cfg.Network into the same label a VPC network uses for a store identity would
// make one label mean two unrelated things depending on which network you read
// it off.
//
// One definition, used by both the supervisor and Lambda's independent probe,
// so the two cannot disagree about what exists or how it is isolated.
func PlaneSpecs(cfg *config.Config) []docker.NetworkSpec {
	return []docker.NetworkSpec{
		{
			Name:         cfg.Network,
			InternalMode: Internal(cfg),
			Labels:       docker.ManagedLabels(docker.ServiceCore, cfg.Network),
			Version:      cfg.Version,
			EgressMode:   string(EgressMode(cfg)),
		},
		{
			Name:         Primary(cfg),
			InternalMode: ControlPlaneInternal(cfg),
			Labels:       docker.ManagedLabels(docker.ServiceCore, Primary(cfg)),
			Version:      cfg.Version,
			EgressMode:   string(EgressMode(cfg)),
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

// PlacementEnforced reports whether a resource that named a VPC is held to it
// — whether DataNetworks returns its VPC network alone rather than that
// network *and* the shared data plane. See enforceable for what decides it.
//
// It matters beyond placement under OVERCAST_VPC_EGRESS=routed. The shared
// data plane is routable in that mode, as it must be for the resources that
// named no VPC, so a VPC-placed container that also sits on it takes a
// default route from it whatever its subnet's route table says — `routed`
// then grants egress everywhere and withholds it nowhere. The mode cannot
// deliver its half of the bargain on such a host, and says so:
// RoutedPlacementNotEnforcedWarning, and the vpc-egress-not-withheld health
// advisory, which router.checkEgressNotWithheld raises for both withholding
// modes.
func PlacementEnforced(cfg *config.Config) bool { return enforceable(cfg) }

// RoutedPlacementNotEnforcedWarning is what `routed` says on a host where a
// VPC-placed container also joins the shared data plane, which is routable.
const RoutedPlacementNotEnforcedWarning = "OVERCAST_VPC_EGRESS=routed decides egress from each subnet's " +
	"route table, but on this host Overcast's DNS resolver is not listening, so every VPC-placed container " +
	"also joins the routable data plane and takes a route out from it whatever its route table says. " +
	"Egress is granted where the template grants it AND where it withholds it. Run Overcast containerised, " +
	"or against a native Linux Docker daemon, for `routed` to withhold egress from a subnet with no default route"

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
	if err := attachEgress(ctx, dc, containerID, p); err != nil {
		return err
	}
	// Recorded after the attachments, so a placement on record is one that
	// was applied; a recorder that fails is logged by its owner and does not
	// fail the attach, which already did the thing that matters.
	if p.recorder != nil && p.VPCNetwork != "" {
		p.recorder.RecordPlacement(ctx, containerID, p)
	}
	return nil
}

// AttachEgress is the egress half of Attach on its own, for the one caller
// that joins the data plane by hand rather than through Attach — ECS's awsvpc
// path, which pins the task's ENI address on the connect and reads back what
// it got. It joins the egress network, when the placement has one, and
// records the placement as Attach would. Safe to repeat.
func AttachEgress(ctx context.Context, dc connector, containerID string, p Placement) error {
	if dc == nil || containerID == "" {
		return nil
	}
	if err := attachEgress(ctx, dc, containerID, p); err != nil {
		return err
	}
	if p.recorder != nil && p.VPCNetwork != "" {
		p.recorder.RecordPlacement(ctx, containerID, p)
	}
	return nil
}

// attachEgress joins the container to its egress network, ranked as its
// default-route source. See Placement.EgressNetwork and EgressGatewayPriority.
func attachEgress(ctx context.Context, dc connector, containerID string, p Placement) error {
	if p.EgressNetwork == "" {
		return nil
	}
	var err error
	if ec, ok := dc.(egressConnector); ok {
		err = ec.ConnectNetworkWithConfig(ctx, p.EgressNetwork, containerID,
			&docker.EndpointSettings{GwPriority: EgressGatewayPriority})
	} else {
		err = dc.ConnectNetworkWithAliases(ctx, p.EgressNetwork, containerID, nil)
	}
	if err != nil {
		return fmt.Errorf("attach container to egress network %s: %w", p.EgressNetwork, err)
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

// Networks returns every network a container placed by p sits on: control
// first, then its data planes, then its egress network when it has one. Useful
// for diagnostics and for callers that need the full set rather than the
// create-time/attach-time split Primary and Attach express.
func Networks(cfg *config.Config, p Placement) []string {
	networks := append([]string{Primary(cfg)}, DataNetworks(cfg, p)...)
	if p.EgressNetwork != "" {
		networks = append(networks, p.EgressNetwork)
	}
	return networks
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

// SubnetPlacer is the part of the EC2 service a placement decision needs once
// egress is decided per subnet (OVERCAST_VPC_EGRESS=routed). Optional: a
// resolver without it places by VPC alone, and every container in the VPC gets
// the same answer.
type SubnetPlacer interface {
	// EgressNetworkForSubnets returns the Docker network that carries the
	// default route for a container in the given subnets of vpcID, or "" when
	// none of them has a route out (or the mode is not `routed`). It may
	// create the network, and an error is the network the template grants
	// being unavailable — an exhausted pool, a daemon refusal — which fails
	// the placement rather than quietly withholding what the template grants.
	EgressNetworkForSubnets(ctx context.Context, vpcID string, subnetIDs []string) (string, error)
}

// PlacementRecorder is told which container a placement was applied to, so
// the service that decided it can revisit the decision later — under
// `routed`, when a route table changes and containers already running have to
// be moved on or off their VPC's egress network. Optional, as SubnetPlacer is.
type PlacementRecorder interface {
	RecordPlacement(ctx context.Context, containerID string, p Placement)
}

// PlaceInVPC resolves vpcID to the Placement a container in it should take.
//
// A resource with no VPC, or no resolver to ask, lands on the default data
// plane — the zero Placement. A VPC that cannot take containers is an error
// rather than a silent fallback: quietly placing a resource somewhere other
// than the VPC it asked for is how an unreachable endpoint gets minted.
//
// A placement made here names no subnets, so under `routed` it gets no egress
// network: a resource that asked for a VPC without saying where in it has no
// route table to read, and the answer that cannot be wrong in the direction
// that fails on AWS is to withhold. Callers that know the subnets use
// PlaceInSubnets.
func PlaceInVPC(ctx context.Context, r VPCResolver, vpcID string) (Placement, error) {
	return PlaceInSubnets(ctx, r, vpcID, nil)
}

// PlaceInSubnets is PlaceInVPC for a resource that named the subnets it wants
// to be in — a function's VpcConfig, a task's awsvpcConfiguration, a subnet
// group. The VPC decides the plane; under `routed` the subnets decide, through
// their route tables, whether the container also gets an egress network.
//
// A container in several subnets gets egress when any of them grants it. On
// AWS a function spread across a NAT-routed subnet and an isolated one reaches
// the internet from some of its ENIs and not others, which is not a state one
// container can be in; granting is the reading that does not make a working
// stack fail locally, and the placement names the subnets so the log can say
// which one decided.
func PlaceInSubnets(ctx context.Context, r VPCResolver, vpcID string, subnetIDs []string) (Placement, error) {
	if r == nil || vpcID == "" {
		return Placement{}, nil
	}
	if status := r.VPCNetworkStatus(ctx, vpcID); !Launchable(status) {
		return Placement{}, fmt.Errorf("VPC %s is not launchable (network status=%s)", vpcID, status)
	}
	p := Placement{
		VPCNetwork: r.DockerNetworkForVpc(ctx, vpcID),
		VPCID:      vpcID,
		SubnetIDs:  subnetIDs,
	}
	if sp, ok := r.(SubnetPlacer); ok && p.VPCNetwork != "" {
		egress, err := sp.EgressNetworkForSubnets(ctx, vpcID, subnetIDs)
		if err != nil {
			return Placement{}, fmt.Errorf("VPC %s: %w", vpcID, err)
		}
		p.EgressNetwork = egress
	}
	if rec, ok := r.(PlacementRecorder); ok {
		p.recorder = rec
	}
	return p, nil
}
