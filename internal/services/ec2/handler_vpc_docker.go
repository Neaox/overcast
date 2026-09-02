package ec2

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/containerendpoint"
	"github.com/overcast-sh/overcast/internal/dataplane"
	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/protocol"
)

// This file holds the low-level Docker-network primitives used by the VPC
// network strategies in vpc_strategy.go. All policy decisions (when to
// create, share, or skip a network) live in the strategies — this file only
// knows how to talk to Docker.

// reconcileNetworks is the entrypoint wired from router.reconcileDockerNetworks
// via Service.ReconcileNetworks. It delegates to the active strategy and must
// tolerate every error path without aborting overcastd startup.
func (h *Handler) reconcileNetworks(ctx context.Context, networks []docker.NetworkSummary) {
	log := h.log.WithRecorder(ctx)

	if !h.dockerReady.Load() {
		return
	}
	vpcs, aerr := h.store.listVPCs(ctx)
	if aerr != nil {
		log.Error("reconcile networks: list VPCs", zap.String("error", aerr.Message))
		return
	}
	h.publishInstanceIdentity(ctx)
	h.vpcStrategy.Reconcile(ctx, vpcs, h.networksInScope(ctx, networks))

	// An adopted network keeps the --internal flag it was created with, which
	// a gateway attached or detached since may contradict.
	h.reconcileVPCNetworkIsolation(ctx)

	// Reconcile adopts networks that already existed, so joining only on create
	// would leave Overcast off every network that survived a restart. Re-read:
	// both passes above may have moved a record onto a new network.
	vpcs, aerr = h.store.listVPCs(ctx)
	if aerr != nil {
		log.Error("reconcile networks: list VPCs after reconcile", zap.String("error", aerr.Message))
		return
	}
	for _, vpc := range vpcs {
		h.joinVPCNetwork(ctx, vpc.DockerNetworkID)
	}
}

// nsInstance holds this instance's sweep-domain identity, stamped into
// docker.LabelInstance on every VPC network it creates. See
// serviceutil.InstanceDomain.
const nsInstance = "ec2:instance"

// instanceDomain is this instance's identity, or "" when the store cannot
// establish one — in which case nothing is ours to remove.
func (h *Handler) instanceDomain(ctx context.Context) string {
	if h.instances == nil {
		return ""
	}
	return h.instances.Resolve(ctx)
}

// networksInScope narrows a daemon-wide snapshot of EC2 networks to the ones
// this instance may act on.
//
// The snapshot is every network on the daemon carrying overcast.service=ec2,
// whoever created it. Two Overcasts sharing a daemon — a developer's live
// instance and a test server, or two agents' instances — each see the other's
// VPC networks in it, and unfiltered the strategies' orphan pass removed them:
// a network is "orphaned" by having no VPC record in *this* store, and the
// other instance's records are not here. That is how a test run took a live
// instance's overcast-vpc-* network with it.
//
// The rule is the container GC's (docker.LabelInstance). A network labelled
// with another instance's identity is invisible: neither adopted nor removed.
// One labelled with ours is fully in scope. One with no label predates the
// label — it stays adoptable, so a VPC that survived an upgrade still finds its
// network, but removeOrphanedNetworks leaves it, because absence is not
// permission.
func (h *Handler) networksInScope(ctx context.Context, networks []docker.NetworkSummary) []docker.NetworkSummary {
	domain := h.instanceDomain(ctx)
	log := h.log.WithRecorder(ctx)
	in := make([]docker.NetworkSummary, 0, len(networks))
	for _, n := range networks {
		if owner := n.Instance(); owner != "" && owner != domain {
			log.Debug("reconcile networks: leaving a network another instance owns",
				zap.String("network", n.ID), zap.String("name", n.Name), zap.String("owner", owner))
			continue
		}
		in = append(in, n)
	}
	return in
}

// removeOrphanedNetworks removes what a strategy's adoption pass left
// unclaimed in byID — and only what this instance created. networksInScope has
// already dropped other instances' networks, so what remains is ours or
// unlabelled; the unlabelled are retained and said so, since their owner
// cannot be established and an unremovable orphan is a far cheaper mistake
// than deleting a network another instance is serving. prefix names the
// strategy in the log, as each strategy's own messages do.
func (h *Handler) removeOrphanedNetworks(ctx context.Context, byID map[string]*docker.NetworkSummary, prefix string) {
	log := h.log.WithRecorder(ctx)
	domain := h.instanceDomain(ctx)
	for id, n := range byID {
		if domain == "" || n.Instance() != domain {
			log.Info("reconcile networks: "+prefix+"retaining an unclaimed network whose owner cannot be established",
				zap.String("vpc", n.ResourceID()),
				zap.String("network", id))
			continue
		}
		log.Info("reconcile networks: "+prefix+"removing orphaned network",
			zap.String("vpc", n.ResourceID()),
			zap.String("network", id))
		if err := h.removeDockerVPCNetwork(ctx, id); err != nil {
			log.Warn("reconcile networks: "+prefix+"remove orphaned network",
				zap.String("network", id),
				zap.Error(err))
		}
	}
}

// joinVPCNetwork attaches Overcast itself to a VPC's Docker network.
//
// A VPC network is a private bridge Overcast is not on by default, so anything
// in-process that has to *reach* a task — the ELB data plane forwarding to an
// awsvpc target, most of all — cannot dial the address the task actually has.
// Overcast already joins the ECS and Lambda networks for the same reason; this
// is that, per VPC. Connecting is idempotent, so callers may repeat it freely.
//
// Only meaningful when Overcast is itself containerised: on the host there is
// no container to attach, and reaching a bridge subnet is the host's routing
// question rather than ours.
func (h *Handler) joinVPCNetwork(ctx context.Context, netID string) {
	log := h.log.WithRecorder(ctx)

	self, ok := h.selfContainer()
	if !ok || netID == "" {
		return
	}
	if err := h.docker.ConnectNetwork(ctx, netID, self); err != nil {
		log.Warn("vpc network: could not attach Overcast to the VPC network — "+
			"the load balancer data plane cannot reach tasks in this VPC",
			zap.String("network", netID), zap.Error(err))
		return
	}
	log.Debug("vpc network: Overcast attached", zap.String("network", netID))
}

// leaveVPCNetwork detaches Overcast from a VPC network. Docker refuses to
// remove a network that still has endpoints, so the join above would otherwise
// make every VPC network undeletable.
func (h *Handler) leaveVPCNetwork(ctx context.Context, netID string) {
	log := h.log.WithRecorder(ctx)

	self, ok := h.selfContainer()
	if !ok || netID == "" {
		return
	}
	if err := h.docker.DisconnectNetwork(ctx, netID, self); err != nil {
		// Not being attached is the common case here (host mode, a network
		// created before this ran), and Docker reports it as an error.
		log.Debug("vpc network: detach Overcast",
			zap.String("network", netID), zap.Error(err))
	}
}

// The two environment probes behind selfContainer, indirected so a test can
// stand in for a machine it cannot arrange: whether this process is
// containerised, and which container that is.
var (
	runningInContainer = containerendpoint.RunningInContainer
	ownHostname        = os.Hostname
)

// selfContainer returns the container Overcast is running in. Docker accepts a
// container's short ID where it accepts a name, and inside a container the
// hostname is that short ID unless it was overridden — the same identification
// containerendpoint uses to find Overcast's address on the ECS network.
func (h *Handler) selfContainer() (string, bool) {
	if !h.dockerReady.Load() || h.docker == nil || !runningInContainer() {
		return "", false
	}
	hostname, err := ownHostname()
	if err != nil || hostname == "" {
		return "", false
	}
	return hostname, true
}

// createDockerVPCNetwork creates a Docker bridge network for the given VPC
// using its CidrBlock (or DockerCidrBlock if the active strategy has set one).
//
// Whether it is `--internal` follows OVERCAST_VPC_EGRESS, not the VPC's
// internet gateway: `open` (the default) leaves every network routable and
// `none` isolates every one of them. The gateway is read and passed through
// because `routed` will decide from it, and because the fact belongs in the
// decision even while the mode ignores it. See dataplane.VPCNetworkInternal.
//
// Returns the Docker network ID on success.
func (h *Handler) createDockerVPCNetwork(ctx context.Context, vpc *VPC) (string, error) {
	return h.createDockerVPCNetworkInternal(ctx, vpc,
		dataplane.VPCNetworkInternal(h.cfg, h.vpcHasInternetGateway(ctx, vpc.VpcID)))
}

// createDockerVPCNetworkInternal is createDockerVPCNetwork with the --internal
// flag supplied rather than derived. The internet-gateway toggle recreates the
// network to flip it, and knows the value it wants.
//
// The network is created from a dataplane.VPCNetworkSpec, which is what stamps
// the labels that make it verifiable later: this instance's identity
// (docker.LabelInstance, so a neighbour's reconcile leaves it alone) and the
// hash of the state it was created in (docker.LabelSpecHash, so the next start
// can tell whether it still holds). See vpcNetworkSpec.
func (h *Handler) createDockerVPCNetworkInternal(ctx context.Context, vpc *VPC, internal bool) (string, error) {
	netID, err := h.docker.CreateNetworkWithOptions(ctx,
		h.vpcNetworkSpec(ctx, vpc, internal).CreateOptions())
	if err != nil {
		return "", err
	}
	h.joinVPCNetwork(ctx, netID)
	return netID, nil
}

// vpcNetworkSpec is the complete desired state of one VPC's Docker network,
// resolved and ready either to create from or to compare a live network
// against. One definition for both, so a network cannot be created in a state
// the verification would then call wrong.
//
// `internal` is what the caller decided; every caller here got it from
// dataplane.VPCNetworkInternal, which is the only thing that turns the gateway
// fact into an isolation. The fact itself is recorded on the network
// (docker.LabelGatewayAttached) so `overcast network status` — which has no
// state store to ask — can compute this same desired state instead of guessing
// at it.
func (h *Handler) vpcNetworkSpec(ctx context.Context, vpc *VPC, internal bool) docker.ResolvedNetworkSpec {
	return dataplane.VPCNetworkSpec(h.cfg, dataplane.VPCNetwork{
		VPCID:              vpc.VpcID,
		Subnet:             preferredDockerSubnet(vpc),
		Owner:              h.instanceDomain(ctx),
		Internal:           internal,
		HasInternetGateway: h.vpcHasInternetGateway(ctx, vpc.VpcID),
	}).Resolve(ctx, h.docker)
}

// removeDockerVPCNetwork removes a Docker network by ID. Missing networks
// are treated as success.
func (h *Handler) removeDockerVPCNetwork(ctx context.Context, netID string) error {
	if netID == "" {
		return nil
	}
	h.leaveVPCNetwork(ctx, netID)
	return h.docker.RemoveNetwork(ctx, netID)
}

// vpcHasInternetGateway returns true if any internet gateway is attached to
// the given VPC.
func (h *Handler) vpcHasInternetGateway(ctx context.Context, vpcID string) bool {
	igws, err := h.store.listInternetGateways(ctx)
	if err != nil {
		return false
	}
	for _, igw := range igws {
		for _, att := range igw.Attachments {
			if att.VpcID == vpcID && att.State == "attached" {
				return true
			}
		}
	}
	return false
}

// ── Internet-gateway isolation ───────────────────────────────────────────────
//
// A VPC network is `--internal` until an internet gateway is attached, and
// Docker fixes that flag when the network is created. Flipping it therefore
// means recreating the network — under whatever is already on it.

// vpcNetworkEndpoint is one container's attachment to a VPC network, captured
// before the network is recreated so the container can be put back exactly as
// it was: same address, same DNS names.
type vpcNetworkEndpoint struct {
	containerID string
	ipv4        string
	aliases     []string
}

// rejoinError is a flip that recreated the network but could not put every
// container back on it. The network carries the flag that was asked for, so
// the flip counts as done; the containers named are the problem.
type rejoinError struct {
	netID string
	err   error
}

func (e *rejoinError) Error() string {
	return fmt.Sprintf("containers could not rejoin the recreated network %s: %v", e.netID, e.err)
}

func (e *rejoinError) Unwrap() error { return e.err }

// changeVPCGateway applies an internet-gateway attach or detach to vpcID's
// Docker network and then records it, in that order and under the network's
// lock, so the record never names a gateway the network does not reflect:
// a flip that cannot be completed fails the call with nothing recorded, and
// the same call can be retried.
//
// record persists the attachment change. It runs under the same lock as the
// flip, so a sharer's detach cannot read the store between this VPC's flip
// and its record and undo it (#1569).
//
// Two VPCs are exempt from the flip and only recorded: one with no Docker
// network (the flag is derived from the gateway state when the network is
// eventually created), and the default VPC, whose network is the shared data
// plane — it already has the internet, and recreating it would take every
// container Overcast started with it.
func (h *Handler) changeVPCGateway(ctx context.Context, vpcID string, attach bool, record func() *protocol.AWSError) *protocol.AWSError {
	vpc, unlock, aerr := h.lockVPCNetwork(ctx, vpcID)
	if aerr != nil {
		return aerr
	}
	defer unlock()

	if vpc.IsDefault {
		h.log.WithRecorder(ctx).Warn("ignoring an internet-gateway change on the default VPC's network — "+
			"it is the shared data plane and cannot be recreated under running containers",
			zap.String("vpc", vpcID), zap.Bool("attach", attach))
		return record()
	}
	if !h.dockerReady.Load() || vpc.DockerNetworkID == "" {
		return record()
	}

	// A network several VPCs share is external while any of them has a
	// gateway. The shared strategy enforces no isolation between sharers to
	// begin with, so the most permissive of them sets the mode for all — the
	// alternative, one VPC's detach cutting off another's internet, is a
	// surprise nothing in the AWS model prepares a stack for. Only the shared
	// strategy can produce a sharer, so for strict and remapped this is a
	// per-VPC flip.
	hasGateway := attach
	if !attach {
		vpcs, attached, aerr := h.gatewayView(ctx)
		if aerr != nil {
			return aerr
		}
		hasGateway = networkHasGateway(vpcs, attached, vpc.DockerNetworkID, vpc.VpcID)
		if hasGateway {
			h.log.WithRecorder(ctx).Info("vpc network: staying external — another VPC on the shared network still has an internet gateway",
				zap.String("vpc", vpc.VpcID), zap.String("network", vpc.DockerNetworkID))
		}
	}
	// The gateway is a fact about the template; whether it decides isolation is
	// a question for the egress mode. Under `open` and `none` it does not —
	// those answer for every network alike — and under `routed` it will. See
	// dataplane.VPCNetworkInternal and docs/networking.md § Egress modes.
	internal := dataplane.VPCNetworkInternal(h.cfg, hasGateway)

	if netID, err := h.applyVPCNetworkInternal(ctx, vpc, internal); err != nil {
		// A flip that stopped before the removal changed nothing, and the
		// record still describes the network; one that lost the network on
		// the way is recorded for the advisories, since nothing about it
		// heals on its own.
		if netID != vpc.DockerNetworkID {
			h.noteNetworkProblem(vpc.VpcID, netID,
				fmt.Sprintf("the Docker network should be %s but could not be recreated: %v", isolationWord(internal), err))
		}
		return vpcNetworkFlipError(attach, vpcID, err)
	}
	return record()
}

// vpcNetworkFlipError is the answer when the VPC's Docker network could not be
// brought in line with the gateway change. AWS has no such failure — its
// gateways are metadata — so the code is Overcast's own InternalError, and the
// message says what Docker refused rather than hiding it behind the generic
// wording, because the remedy is on the user's side of the socket.
func vpcNetworkFlipError(attach bool, vpcID string, err error) *protocol.AWSError {
	verb := "attached to"
	if !attach {
		verb = "detached from"
	}
	return &protocol.AWSError{
		Code: protocol.ErrInternalError.Code,
		Message: fmt.Sprintf("The internet gateway was not %s vpc '%s': its Docker network could not be brought to the new isolation mode: %v",
			verb, vpcID, err),
		HTTPStatus: protocol.ErrInternalError.HTTPStatus,
	}
}

// lockVPCNetwork returns vpcID's current record with its Docker network's flip
// lock held.
//
// The lock is per network rather than per VPC because sharers must contend with
// each other: two gateway calls on two VPCs of one network, or a call racing
// the startup reconcile, would otherwise both disconnect the same endpoints and
// both try to remove and recreate the same subnet, with the loser marking every
// record on it unbacked.
//
// **Keyed by the network's name, not its ID, and that is the whole point.** A
// flip removes the network and creates it again, so the ID it started with is
// gone by the time renameVPCNetwork moves the records onto the successor — and
// a holder keyed by the old ID goes on holding a mutex nothing else will ever
// look up. A sharer arriving in the window between the recreate and the record
// write would take a different, free mutex and proceed straight into the same
// endpoints. The name survives the recreate (recreateDockerVPCNetwork keeps
// it), so a successor ID resolves to the same key and the exclusion holds all
// the way through record().
//
// It is also the lock docker.EnsureNetwork and `overcast network reset` take,
// for the same networks. One lock, or the startup verification and the gateway
// flip would each be serialised only against themselves.
//
// The record is re-read after the lock is taken — the network the caller
// resolved may have been replaced while it waited — and the acquisition
// restarts only when the replacement is a *different network*; a new ID under
// the same name is the flip this lock just waited out, and re-locking would
// spin. A VPC with no network needs no lock; unlock is then a no-op.
func (h *Handler) lockVPCNetwork(ctx context.Context, vpcID string) (*VPC, func(), *protocol.AWSError) {
	const attempts = 8
	for range attempts {
		vpc, aerr := h.store.getVPC(ctx, vpcID)
		if aerr != nil {
			return nil, func() {}, aerr
		}
		if vpc.DockerNetworkID == "" || vpc.IsDefault {
			// The default VPC's network is the shared data plane, which no
			// gateway change ever recreates (defaultVPCGuard). Nothing to
			// serialise, and asking the daemon its name would be a Docker call
			// made purely to lock something that is never touched.
			return vpc, func() {}, nil
		}
		key := h.vpcNetworkLockKey(ctx, vpc.DockerNetworkID)
		unlock := docker.LockNetwork(key)
		current, aerr := h.store.getVPC(ctx, vpcID)
		if aerr != nil {
			unlock()
			return nil, func() {}, aerr
		}
		if current.DockerNetworkID == vpc.DockerNetworkID ||
			h.vpcNetworkLockKey(ctx, current.DockerNetworkID) == key {
			return current, unlock, nil
		}
		unlock() // a different network now: lock the one it is actually on
	}
	return nil, func() {}, protocol.Wrap(protocol.ErrInternalError,
		fmt.Errorf("vpc %s: its Docker network kept changing while waiting to lock it", vpcID))
}

// vpcNetworkLockKey is the name of the network with this ID — the key
// lockVPCNetwork serialises on.
//
// Falls back to the ID when the network cannot be inspected. That is the
// degenerate case in both directions: a network the daemon will not describe is
// one the flip is about to fail on anyway, and keying by ID is still correct
// exclusion for as long as that ID is what the records name.
//
// An empty ID has no network to lock and gets a key of its own that nothing
// else will collide with.
func (h *Handler) vpcNetworkLockKey(ctx context.Context, netID string) string {
	if netID == "" {
		return ""
	}
	if h.docker == nil {
		return netID
	}
	info, err := h.docker.InspectNetwork(ctx, netID)
	if err != nil || info == nil || info.Name == "" {
		return netID
	}
	return info.Name
}

// gatewayView reads, once, what a flip decision needs: every VPC record, and
// the set of VPC IDs with an internet gateway attached.
func (h *Handler) gatewayView(ctx context.Context) ([]*VPC, map[string]bool, *protocol.AWSError) {
	vpcs, aerr := h.store.listVPCs(ctx)
	if aerr != nil {
		return nil, nil, aerr
	}
	igws, aerr := h.store.listInternetGateways(ctx)
	if aerr != nil {
		return nil, nil, aerr
	}
	attached := make(map[string]bool)
	for _, igw := range igws {
		for _, att := range igw.Attachments {
			if att.State == "attached" {
				attached[att.VpcID] = true
			}
		}
	}
	return vpcs, attached, nil
}

// networkHasGateway reports whether any VPC backed by netID, other than
// excludeVpcID, has an internet gateway attached. Under the shared strategy
// several VPCs can name one network; under the others this is one lookup.
func networkHasGateway(vpcs []*VPC, attached map[string]bool, netID, excludeVpcID string) bool {
	for _, v := range vpcs {
		if v.VpcID != excludeVpcID && v.DockerNetworkID == netID && attached[v.VpcID] {
			return true
		}
	}
	return false
}

// applyVPCNetworkInternal flips vpc's network and brings every stored record
// that named the old network — vpc and, under the shared strategy, its
// sharers — to the one that replaced it. The caller holds the network's flip
// lock (lockVPCNetwork). It returns the network that backs the VPC afterwards.
//
// Three outcomes. Success: the records follow the network and any problem
// recorded against them is cleared. A recreate whose containers could not all
// rejoin (rejoinError): the network carries the flag that was asked for, so
// the records follow it, the containers are recorded as a problem for the
// advisories, and the caller sees success — the alternative, failing the call
// over a network already in the requested state, is the inconsistency this
// whole path exists to prevent. Any other failure is returned as is; the
// caller decides whether it is worth recording, since the API path changes
// nothing when the flip stops before the removal.
func (h *Handler) applyVPCNetworkInternal(ctx context.Context, vpc *VPC, internal bool) (string, error) {
	log := h.log.WithRecorder(ctx)

	oldID := vpc.DockerNetworkID
	netID, err := h.flipDockerVPCNetworkInternal(ctx, vpc, internal)
	moved := []string{vpc.VpcID}
	if netID != oldID {
		moved = h.renameVPCNetwork(ctx, oldID, netID)
	}

	var rejoin *rejoinError
	switch {
	case err == nil:
		for _, id := range moved {
			h.netProblems.Delete(id)
		}
		if netID != oldID {
			log.Info("vpc network: isolation changed",
				zap.String("vpc", vpc.VpcID), zap.Bool("internal", internal),
				zap.String("old_network", oldID), zap.String("network", netID))
		}
		return netID, nil
	case errors.As(err, &rejoin):
		detail := fmt.Sprintf("the Docker network was recreated %s, but %v", isolationWord(internal), rejoin.err)
		for _, id := range moved {
			h.noteNetworkProblem(id, netID, detail)
		}
		log.Error("vpc network: isolation changed, but containers could not rejoin the recreated network",
			zap.String("vpc", vpc.VpcID), zap.String("network", netID), zap.Error(rejoin.err))
		return netID, nil
	default:
		log.Error("vpc network: could not change the network's isolation — "+
			"containers in this VPC keep the internet reachability they had",
			zap.String("vpc", vpc.VpcID), zap.String("network", oldID),
			zap.Bool("internal", internal), zap.Error(err))
		return netID, err
	}
}

// isolationWord names a flag in a problem or advisory.
func isolationWord(internal bool) string {
	if internal {
		return "--internal (no internet gateway is attached)"
	}
	return "external (an internet gateway is attached)"
}

// flipDockerVPCNetworkInternal makes vpc's Docker network carry the given
// `--internal` flag, recreating it when it does not already. It returns the
// network that backs the VPC afterwards — the same ID when nothing needed
// doing, empty when the VPC has been left with none.
//
// Docker refuses to remove a network that still has endpoints, so the
// containers on it are moved rather than being a reason to give up: each is
// disconnected, the network is recreated, and each is reconnected with the
// address and DNS aliases it had, so nothing that had resolved or dialled it
// has to notice. Every container on the network is moved, including one a
// user attached by hand — it is exactly what would otherwise block the
// removal. Their control-plane attachment is untouched throughout (see
// internal/dataplane), so an in-flight invocation keeps its Runtime API
// connection; only connections across the VPC bridge itself are dropped, as
// they are on AWS when routing changes under a live ENI.
//
// Failure leaves things as they were wherever there is a way back: a refused
// disconnect or removal reconnects what had already been moved and returns
// with the original network intact. A failed recreate is the case with none —
// the containers are put on a network with the old flag if one can be made,
// and the error says so. A recreate that succeeded but could not take every
// container back is a rejoinError: the caller treats the flip as done.
func (h *Handler) flipDockerVPCNetworkInternal(ctx context.Context, vpc *VPC, internal bool) (string, error) {
	log := h.log.WithRecorder(ctx)

	info, err := h.docker.InspectNetwork(ctx, vpc.DockerNetworkID)
	if err != nil {
		if docker.IsNotFound(err) {
			// The record names a network that no longer exists; the flag it
			// needs is known, so this is a plain create.
			return h.createDockerVPCNetworkInternal(ctx, vpc, internal)
		}
		return vpc.DockerNetworkID, fmt.Errorf("inspect network %s: %w", vpc.DockerNetworkID, err)
	}
	// Not `info.Internal == internal`. Docker's create call returns an existing
	// network unchanged, so a network adopted from an older Overcast, a
	// different egress mode, or another tool keeps every setting it was born
	// with — driver, subnet, driver options — while its isolation happens to
	// look right. Comparing the whole spec is what makes an adopted network
	// verified rather than assumed, and the recreate below is the repair: it
	// moves the containers across and rejoins them, which is why this can
	// afford to be strict.
	spec := h.vpcNetworkSpec(ctx, vpc, internal)
	diffs := spec.Diff(info)
	if len(diffs) == 0 {
		return info.ID, nil
	}
	log.Info("vpc network: not in the state this configuration asks for — recreating it",
		zap.String("vpc", vpc.VpcID), zap.String("network", info.ID),
		zap.Strings("differs", docker.DiffStrings(diffs)))

	endpoints, err := h.vpcNetworkEndpoints(ctx, info)
	if err != nil {
		return info.ID, err
	}
	var moved []vpcNetworkEndpoint
	for _, ep := range endpoints {
		if err := h.docker.DisconnectNetwork(ctx, info.ID, ep.containerID); err != nil {
			h.restoreVPCNetworkEndpoints(ctx, info.ID, moved)
			return info.ID, fmt.Errorf("disconnect container %s from network %s: %w", ep.containerID, info.ID, err)
		}
		moved = append(moved, ep)
	}
	if err := h.removeDockerVPCNetwork(ctx, info.ID); err != nil {
		h.joinVPCNetwork(ctx, info.ID)
		h.restoreVPCNetworkEndpoints(ctx, info.ID, moved)
		return info.ID, err
	}

	netID, err := h.recreateDockerVPCNetwork(ctx, vpc, info, internal)
	if err != nil {
		fallbackID, ferr := h.recreateDockerVPCNetwork(ctx, vpc, info, info.Internal)
		if ferr != nil {
			return "", fmt.Errorf("recreate network for %s: %w (and could not restore the old one: %v)", vpc.VpcID, err, ferr)
		}
		if rerr := h.reconnectVPCNetworkEndpoints(ctx, fallbackID, moved); rerr != nil {
			log.Error("vpc network: containers could not rejoin the restored network",
				zap.String("vpc", vpc.VpcID), zap.String("network", fallbackID), zap.Error(rerr))
		}
		return fallbackID, fmt.Errorf("recreate network for %s with internal=%t: %w (restored with internal=%t)", vpc.VpcID, internal, err, info.Internal)
	}
	if err := h.reconnectVPCNetworkEndpoints(ctx, netID, moved); err != nil {
		return netID, &rejoinError{netID: netID, err: err}
	}
	return netID, nil
}

// recreateDockerVPCNetwork creates the network that replaces info, with the
// new flag and everything else as it was: the same name, the same labels
// (the resource ID and the instance ownership stamp among them), the same
// subnet. Under the shared strategy the network is named for the VPC that
// created it, and whichever sharer flips it must not rename it out from
// under the others; and a reconcile after a restart adopts by ID, then label,
// then subnet, so the labels are what let it find the network again.
func (h *Handler) recreateDockerVPCNetwork(ctx context.Context, vpc *VPC, info *docker.NetworkInspect, internal bool) (string, error) {
	if info.Name == "" || len(info.Labels) == 0 {
		return h.createDockerVPCNetworkInternal(ctx, vpc, internal)
	}
	// The original labels are kept — under the shared strategy the network is
	// named and labelled for the VPC that created it, and a sharer flipping it
	// must not relabel it out from under the others — but the spec labels are
	// refreshed from the state being created, or a rebuilt network would carry
	// the hash of the state it used to be in and read as drifted forever.
	opts := h.vpcNetworkSpec(ctx, vpc, internal).CreateOptions()
	labels := make(map[string]string, len(info.Labels)+len(opts.Labels))
	for k, v := range info.Labels {
		labels[k] = v
	}
	for _, k := range []string{docker.LabelSpecHash, docker.LabelSpecVersion, docker.LabelEgressMode} {
		if v, ok := opts.Labels[k]; ok {
			labels[k] = v
		}
	}
	netID, err := h.docker.CreateNetworkWithOptions(ctx, docker.CreateNetworkOptions{
		Name:     info.Name,
		Labels:   labels,
		Subnet:   preferredDockerSubnet(vpc),
		Internal: internal,
	})
	if err != nil {
		return "", err
	}
	h.joinVPCNetwork(ctx, netID)
	return netID, nil
}

// vpcNetworkEndpoints captures every container on the network other than
// Overcast itself, which removeDockerVPCNetwork detaches and
// createDockerVPCNetworkInternal rejoins on its own. The address and aliases
// come from the container's side of the attachment; the network's own view
// carries neither reliably.
func (h *Handler) vpcNetworkEndpoints(ctx context.Context, info *docker.NetworkInspect) ([]vpcNetworkEndpoint, error) {
	self, _ := h.selfContainer()
	ids := make([]string, 0, len(info.Containers))
	for id := range info.Containers {
		if self != "" && strings.HasPrefix(id, self) {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)

	endpoints := make([]vpcNetworkEndpoint, 0, len(ids))
	for _, id := range ids {
		c, err := h.docker.InspectContainer(ctx, id)
		if err != nil {
			if docker.IsNotFound(err) {
				continue // gone between the two inspects; nothing to move
			}
			return nil, fmt.Errorf("inspect container %s on network %s: %w", id, info.ID, err)
		}
		ep := vpcNetworkEndpoint{containerID: id}
		for name, n := range c.NetworkSettings.Networks {
			if n.NetworkID == info.ID || name == info.Name {
				ep.ipv4, ep.aliases = n.IPAddress, n.Aliases
				break
			}
		}
		endpoints = append(endpoints, ep)
	}
	return endpoints, nil
}

// reconnectVPCNetworkEndpoints puts endpoints back on netID with the address
// each had. Docker rejects a pinned address it cannot honour, so a container
// whose old address is somehow taken is reconnected with a fresh one rather
// than left off the network — its endpoint hostnames still resolve, only a
// caller holding the raw address would notice. Every endpoint is attempted;
// the error names the ones that failed.
func (h *Handler) reconnectVPCNetworkEndpoints(ctx context.Context, netID string, endpoints []vpcNetworkEndpoint) error {
	log := h.log.WithRecorder(ctx)

	var errs []error
	for _, ep := range endpoints {
		cfg := &docker.EndpointSettings{Aliases: ep.aliases}
		if ep.ipv4 != "" {
			cfg.IPAMConfig = &docker.EndpointIPAMConfig{IPv4Address: ep.ipv4}
		}
		err := h.docker.ConnectNetworkWithConfig(ctx, netID, ep.containerID, cfg)
		if err != nil && cfg.IPAMConfig != nil {
			log.Warn("vpc network: container could not keep its address across the recreate — reconnecting with a new one",
				zap.String("container", ep.containerID), zap.String("address", ep.ipv4),
				zap.String("network", netID), zap.Error(err))
			cfg.IPAMConfig = nil
			err = h.docker.ConnectNetworkWithConfig(ctx, netID, ep.containerID, cfg)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("container %s: %w", ep.containerID, err))
		}
	}
	return errors.Join(errs...)
}

// restoreVPCNetworkEndpoints is reconnectVPCNetworkEndpoints on the way back
// from a flip that could not proceed: the network is the one the containers
// were on, so a failure here is logged rather than returned — the caller is
// already reporting the error that stopped the flip.
func (h *Handler) restoreVPCNetworkEndpoints(ctx context.Context, netID string, endpoints []vpcNetworkEndpoint) {
	if err := h.reconnectVPCNetworkEndpoints(ctx, netID, endpoints); err != nil {
		h.log.WithRecorder(ctx).Error("vpc network: containers could not rejoin the network after an abandoned flip",
			zap.String("network", netID), zap.Error(err))
	}
}

// renameVPCNetwork moves every stored VPC record that names oldID onto newID
// and returns the IDs of the VPCs it moved. Under the shared strategy that is
// the owner and every sharer; elsewhere it is one record. An empty newID
// means the network is gone, and the records say so.
func (h *Handler) renameVPCNetwork(ctx context.Context, oldID, newID string) []string {
	log := h.log.WithRecorder(ctx)

	vpcs, aerr := h.store.listVPCs(ctx)
	if aerr != nil {
		log.Error("vpc network: list VPCs to record the recreated network", zap.String("error", aerr.Message))
		return nil
	}
	var moved []string
	for _, v := range vpcs {
		if v.DockerNetworkID != oldID {
			continue
		}
		v.DockerNetworkID = newID
		if newID == "" {
			v.NetworkStatus = vpcNetworkStatusUnbacked
		}
		if aerr := h.store.putVPC(ctx, v); aerr != nil {
			log.Error("vpc network: record the recreated network",
				zap.String("vpc", v.VpcID), zap.String("error", aerr.Message))
			continue
		}
		moved = append(moved, v.VpcID)
	}
	return moved
}

// reconcileVPCNetworkIsolation runs after the strategy has matched records to
// networks: a network adopted from a previous run carries whatever flag it was
// created with, and a gateway attached or detached while that run could not
// act on it leaves the two disagreeing. Each backed network is checked once,
// under its flip lock — inspect is the whole cost when they agree — and
// repaired when they do not. A repair that fails is recorded as a problem so
// it reaches the health advisories rather than only the startup log.
func (h *Handler) reconcileVPCNetworkIsolation(ctx context.Context) {
	log := h.log.WithRecorder(ctx)

	vpcs, attached, aerr := h.gatewayView(ctx)
	if aerr != nil {
		log.Error("reconcile networks: read VPCs and gateways for the isolation check", zap.String("error", aerr.Message))
		return
	}
	sort.Slice(vpcs, func(i, j int) bool { return vpcs[i].VpcID < vpcs[j].VpcID })

	checked := make(map[string]struct{}, len(vpcs))
	for _, listed := range vpcs {
		// The default VPC's network is the shared data plane, never a
		// per-VPC bridge; its gateway is metadata (see defaultVPCGuard).
		if listed.IsDefault || listed.DockerNetworkID == "" || listed.DockerNetworkID == h.cfg.Network {
			continue
		}
		if _, done := checked[listed.DockerNetworkID]; done {
			continue
		}
		vpc, unlock, aerr := h.lockVPCNetwork(ctx, listed.VpcID)
		if aerr != nil {
			log.Error("reconcile networks: lock VPC network", zap.String("vpc", listed.VpcID), zap.String("error", aerr.Message))
			continue
		}
		if _, done := checked[vpc.DockerNetworkID]; done || vpc.DockerNetworkID == "" {
			unlock()
			continue
		}
		checked[vpc.DockerNetworkID] = struct{}{}

		internal := dataplane.VPCNetworkInternal(h.cfg,
			networkHasGateway(vpcs, attached, vpc.DockerNetworkID, ""))
		netID, err := h.applyVPCNetworkInternal(ctx, vpc, internal)
		if err != nil {
			h.noteNetworkProblem(vpc.VpcID, netID,
				fmt.Sprintf("the Docker network should be %s but could not be recreated: %v", isolationWord(internal), err))
		}
		checked[netID] = struct{}{}
		unlock()
	}
}

// noteNetworkProblem records that vpcID's network is not in the state its
// gateway calls for, with what stood in the way. One entry per VPC: a later
// failure replaces an earlier one, and a later success clears it.
func (h *Handler) noteNetworkProblem(vpcID, netID, detail string) {
	h.netProblems.Store(vpcID, dataplane.VPCNetworkProblem{VpcID: vpcID, NetworkID: netID, Detail: detail})
}

// forgetVPCNetwork is the network side of deleting a VPC: the strategy
// decides what happens to the Docker network, and any problem recorded
// against the VPC goes with the record, so a deleted VPC cannot keep an
// advisory alive.
func (h *Handler) forgetVPCNetwork(ctx context.Context, vpc *VPC) {
	if vpc == nil {
		return
	}
	h.netProblems.Delete(vpc.VpcID)
	if h.vpcStrategy != nil {
		h.vpcStrategy.OnDelete(ctx, vpc)
	}
}

// networkProblems returns the recorded problems, ordered by VPC ID.
func (h *Handler) networkProblems() []dataplane.VPCNetworkProblem {
	var out []dataplane.VPCNetworkProblem
	h.netProblems.Range(func(_, v any) bool {
		out = append(out, v.(dataplane.VPCNetworkProblem))
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].VpcID < out[j].VpcID })
	return out
}
