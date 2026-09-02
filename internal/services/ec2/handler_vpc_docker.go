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
	"github.com/overcast-sh/overcast/internal/docker"
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
// using its CidrBlock (or DockerCidrBlock if the active strategy has set
// one). The network is --internal unless the VPC has an attached internet
// gateway. Returns the Docker network ID on success.
func (h *Handler) createDockerVPCNetwork(ctx context.Context, vpc *VPC) (string, error) {
	return h.createDockerVPCNetworkInternal(ctx, vpc, !h.vpcHasInternetGateway(ctx, vpc.VpcID))
}

// createDockerVPCNetworkInternal is createDockerVPCNetwork with the --internal
// flag supplied rather than derived. The internet-gateway toggle recreates the
// network to flip it, and knows the value it wants.
func (h *Handler) createDockerVPCNetworkInternal(ctx context.Context, vpc *VPC, internal bool) (string, error) {
	// Stamped with this instance's identity, which is what lets a later
	// reconcile — ours after a restart, or a neighbour's on the same daemon —
	// tell whose network this is. See networksInScope.
	labels := h.instances.ManagedLabels(ctx, "ec2", vpc.VpcID)
	labels["overcast.vpc-id"] = vpc.VpcID

	netID, err := h.docker.CreateNetworkWithOptions(ctx, docker.CreateNetworkOptions{
		Name:     "overcast-vpc-" + vpc.VpcID,
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

// NetworkProblem is a VPC whose Docker network does not carry the isolation
// its internet-gateway state calls for, and could not be made to. The router
// reports the set as a health advisory (router.checkVPCNetworkIsolation), so
// the condition is visible somewhere other than a log line that has scrolled
// past. An entry is cleared the next time the network is brought into line.
type NetworkProblem struct {
	VpcID     string
	NetworkID string
	Detail    string
}

// setVPCNetworkInternal is what every strategy's SetInternal does: bring the
// VPC's Docker network to the requested `--internal` state. The strategies
// differ in how networks are allocated, not in what a gateway means for one.
//
// A network several VPCs share is external while any of them has a gateway.
// The shared strategy enforces no isolation between sharers to begin with, so
// the most permissive of them sets the mode for all — the alternative, one
// VPC's detach cutting off another's internet, is a surprise nothing in the
// AWS model prepares a stack for. Only the shared strategy can produce a
// sharer, so for strict and remapped this is exactly a per-VPC flip.
func (h *Handler) setVPCNetworkInternal(ctx context.Context, vpcID string, internal bool) error {
	if !h.dockerReady.Load() {
		return nil
	}
	vpc, aerr := h.store.getVPC(ctx, vpcID)
	if aerr != nil || vpc.DockerNetworkID == "" {
		// An unbacked VPC has nothing to flip: the flag is derived from the
		// gateway state when its network is eventually created.
		return nil
	}
	if internal && h.vpcNetworkHasGateway(ctx, vpc.DockerNetworkID, vpc.VpcID) {
		h.log.WithRecorder(ctx).Info("vpc network: staying external — another VPC on the shared network still has an internet gateway",
			zap.String("vpc", vpc.VpcID), zap.String("network", vpc.DockerNetworkID))
		return nil
	}
	return h.applyVPCNetworkInternal(ctx, vpc, internal)
}

// applyVPCNetworkInternal flips vpc's network and brings every stored record
// that named the old network — vpc and, under the shared strategy, its
// sharers — to the one that replaced it.
//
// The returned error is the flip's. When it left the VPC on a network other
// than the one it started on (recreated with the wrong flag, or none at all),
// the state is also recorded as a NetworkProblem, because nothing about it
// rolls back on its own. A flip that failed before changing anything is only
// an error: the network is as it was, and the record still describes it.
func (h *Handler) applyVPCNetworkInternal(ctx context.Context, vpc *VPC, internal bool) error {
	log := h.log.WithRecorder(ctx)

	oldID := vpc.DockerNetworkID
	netID, err := h.flipDockerVPCNetworkInternal(ctx, vpc, internal)
	if netID != oldID {
		h.renameVPCNetwork(ctx, oldID, netID)
	}
	if err != nil {
		if netID != oldID {
			h.noteNetworkProblem(vpc.VpcID, netID, internal, err)
		}
		log.Error("vpc network: could not change the network's isolation — "+
			"containers in this VPC keep the internet reachability they had",
			zap.String("vpc", vpc.VpcID), zap.String("network", oldID),
			zap.Bool("internal", internal), zap.Error(err))
		return err
	}
	h.netProblems.Delete(vpc.VpcID)
	if netID != oldID {
		log.Info("vpc network: isolation changed",
			zap.String("vpc", vpc.VpcID), zap.Bool("internal", internal),
			zap.String("old_network", oldID), zap.String("network", netID))
	}
	return nil
}

// flipDockerVPCNetworkInternal makes vpc's Docker network carry the given
// `--internal` flag, recreating it when it does not already. It returns the
// network that backs the VPC afterwards — the same ID when nothing needed
// doing, empty when the VPC has been left with none.
//
// Docker refuses to remove a network that still has endpoints, so the
// containers on it — a Lambda function, an ECS task, a database — are moved
// rather than being a reason to give up: each is disconnected, the network is
// recreated, and each is reconnected with the address and DNS aliases it had,
// so nothing that had resolved or dialled it has to notice. Their control-plane
// attachment is untouched throughout (see internal/dataplane), so an in-flight
// invocation keeps its Runtime API connection; only connections across the VPC
// bridge itself are dropped, as they are on AWS when routing changes under a
// live ENI.
//
// Failure leaves things as they were wherever there is a way back: a refused
// disconnect or removal reconnects what had already been moved and returns
// with the original network intact. A failed recreate is the case with none —
// the containers are put on a network with the old flag if one can be made,
// and the error says so.
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
	if info.Internal == internal {
		return info.ID, nil
	}

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

	netID, err := h.createDockerVPCNetworkInternal(ctx, vpc, internal)
	if err != nil {
		fallbackID, ferr := h.createDockerVPCNetworkInternal(ctx, vpc, info.Internal)
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
		return netID, fmt.Errorf("reconnect containers to the recreated network %s: %w", netID, err)
	}
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

// renameVPCNetwork moves every stored VPC record that names oldID onto newID.
// Under the shared strategy that is the owner and every sharer; elsewhere it
// is one record. An empty newID means the network is gone, and the records
// say so.
func (h *Handler) renameVPCNetwork(ctx context.Context, oldID, newID string) {
	log := h.log.WithRecorder(ctx)

	vpcs, aerr := h.store.listVPCs(ctx)
	if aerr != nil {
		log.Error("vpc network: list VPCs to record the recreated network", zap.String("error", aerr.Message))
		return
	}
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
		}
	}
}

// vpcNetworkHasGateway reports whether any VPC backed by netID, other than
// excludeVpcID, has an internet gateway attached. Under the shared strategy
// several VPCs can name one network; under the others this is one lookup.
func (h *Handler) vpcNetworkHasGateway(ctx context.Context, netID, excludeVpcID string) bool {
	vpcs, aerr := h.store.listVPCs(ctx)
	if aerr != nil {
		return false
	}
	for _, v := range vpcs {
		if v.VpcID == excludeVpcID || v.DockerNetworkID != netID {
			continue
		}
		if h.vpcHasInternetGateway(ctx, v.VpcID) {
			return true
		}
	}
	return false
}

// reconcileVPCNetworkIsolation runs after the strategy has matched records to
// networks: a network adopted from a previous run carries whatever flag it was
// created with, and a gateway attached while that run could not act on it
// leaves the two disagreeing. Each backed network is checked once — inspect is
// the whole cost when they agree — and repaired when they do not. A repair
// that fails is recorded as a NetworkProblem so it reaches the health
// advisories rather than only the startup log.
func (h *Handler) reconcileVPCNetworkIsolation(ctx context.Context) {
	log := h.log.WithRecorder(ctx)

	vpcs, aerr := h.store.listVPCs(ctx)
	if aerr != nil {
		log.Error("reconcile networks: list VPCs for isolation check", zap.String("error", aerr.Message))
		return
	}
	sort.Slice(vpcs, func(i, j int) bool { return vpcs[i].VpcID < vpcs[j].VpcID })

	checked := make(map[string]struct{}, len(vpcs))
	for _, vpc := range vpcs {
		// The default VPC's network is the shared data plane, never a
		// per-VPC bridge; its gateway is metadata (see defaultVPCGuard).
		if vpc.IsDefault || vpc.DockerNetworkID == "" || vpc.DockerNetworkID == h.cfg.Network {
			continue
		}
		if _, done := checked[vpc.DockerNetworkID]; done {
			continue
		}
		checked[vpc.DockerNetworkID] = struct{}{}

		internal := !h.vpcNetworkHasGateway(ctx, vpc.DockerNetworkID, "")
		if err := h.applyVPCNetworkInternal(ctx, vpc, internal); err != nil {
			netID := ""
			if current, aerr := h.store.getVPC(ctx, vpc.VpcID); aerr == nil {
				netID = current.DockerNetworkID
			}
			h.noteNetworkProblem(vpc.VpcID, netID, internal, err)
		}
	}
}

// noteNetworkProblem records that vpc's network could not be brought to the
// isolation wanted. One entry per VPC: a later failure replaces an earlier
// one, and a later success clears it.
func (h *Handler) noteNetworkProblem(vpcID, netID string, internal bool, err error) {
	want := "external (an internet gateway is attached)"
	if internal {
		want = "--internal (no internet gateway is attached)"
	}
	h.netProblems.Store(vpcID, NetworkProblem{
		VpcID:     vpcID,
		NetworkID: netID,
		Detail:    fmt.Sprintf("the Docker network should be %s but could not be recreated: %v", want, err),
	})
}

// networkProblems returns the recorded problems, ordered by VPC ID.
func (h *Handler) networkProblems() []NetworkProblem {
	var out []NetworkProblem
	h.netProblems.Range(func(_, v any) bool {
		out = append(out, v.(NetworkProblem))
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].VpcID < out[j].VpcID })
	return out
}
