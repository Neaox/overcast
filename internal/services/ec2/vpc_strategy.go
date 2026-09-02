package ec2

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/protocol"
)

// vpcNetworkStrategy is the policy that decides how stored VPCs map onto
// Docker networks. Each method must be safe under concurrent handler calls
// and must never block startup — per-VPC failures are logged and recorded
// on vpc.NetworkStatus so callers can surface them.
//
// Implemented strategies: "shared", "strict", and "remapped".
// "netns" is reserved for future work and is rejected at config load time.
// See docs/services/ec2.md § Advanced: VPC networking strategies for the full
// design of each.
type vpcNetworkStrategy interface {
	// Name returns the strategy's configuration identifier.
	Name() string

	// EnsureNetwork is called from CreateVpc before the VPC is persisted.
	// Implementations set vpc.DockerNetworkID and vpc.NetworkStatus in place.
	// A nil return with an empty DockerNetworkID means Docker was unavailable
	// — the VPC is stored in an `unbacked` state and picked up on the next
	// reconcile. A non-nil return means the strategy is actively rejecting
	// the creation (e.g. the strict strategy refusing an overlapping CIDR).
	EnsureNetwork(ctx context.Context, vpc *VPC) *protocol.AWSError

	// AllocatePrivateIP returns an API-visible private IP for resources in vpc.
	// For remapped VPCs, strategies must persist fake->real translation state so
	// container-side consumers can resolve the backing Docker-network address.
	AllocatePrivateIP(ctx context.Context, vpc *VPC) (string, error)

	// Reconcile synchronises one region's stored VPCs with the Docker networks
	// that actually exist: each record adopts the network that backs it from
	// the index, and one that has none gets one created. It is called once per
	// region on every Docker readiness transition and whenever the watcher
	// reconnects, so it must be idempotent. The index is shared by every
	// region of one pass — a network adopted here is gone from it for the next
	// region — and what no region claims is removed afterwards by the Handler,
	// not here; see Handler.reconcileNetworks.
	Reconcile(ctx context.Context, vpcs []*VPC, networks *vpcNetworkIndex)

	// OnDelete is called from DeleteVpc after the VPC has been removed from
	// the store. Strategies that share networks only tear down the Docker
	// network when the deleted VPC was its last user.
	OnDelete(ctx context.Context, vpc *VPC)
}

// An internet gateway's effect on a network — the --internal flip — is not a
// strategy concern: no strategy would do it differently, and it must hold the
// network's lock across the flip and the record. It lives on the Handler; see
// changeVPCGateway.

// VPC network status values recorded on VPC.NetworkStatus. Empty string is
// treated as vpcNetworkStatusOK for backwards compatibility with VPCs that
// predate the field.
const (
	vpcNetworkStatusOK       = "ok"       // this VPC owns its Docker network
	vpcNetworkStatusShared   = "shared"   // reuses a network owned by another VPC
	vpcNetworkStatusUnbacked = "unbacked" // no Docker network (Docker unavailable, or deferred)
	vpcNetworkStatusConflict = "conflict" // strict-mode collision
	vpcNetworkStatusRemapped = "remapped" // remapped-mode shadow CIDR
)

// resolveVPCNetworkStrategy returns a strategy for the configured name.
// "shared" (or empty), "strict", and "remapped" resolve to their real
// implementations. Anything else falls back to the shared strategy with a
// warning ("netns" never reaches here — config.Load rejects it outright).
func resolveVPCNetworkStrategy(name string, h *Handler) vpcNetworkStrategy {
	requested := strings.ToLower(strings.TrimSpace(name))
	shared := &sharedVPCStrategy{h: h}
	switch requested {
	case "", "shared":
		return shared
	case "strict":
		return &strictVPCStrategy{h: h}
	case "remapped":
		return &remappedVPCStrategy{h: h, allocator: newShadowCIDRAllocator()}
	default:
		h.log.Warn("unknown VPC network strategy — falling back to shared",
			zap.String("requested", requested),
			zap.String("using", shared.Name()))
		return shared
	}
}

// ─── shared strategy ────────────────────────────────────────────────────────

// sharedVPCStrategy maps each distinct CIDR to exactly one Docker network.
// VPCs that request an already-claimed CIDR reuse the existing Docker network
// ID and are marked NetworkStatus=shared. Container isolation between sharers
// is not enforced — this is the explicit trade-off documented in
// docs/services/ec2.md.
type sharedVPCStrategy struct {
	h *Handler
}

func (s *sharedVPCStrategy) Name() string { return "shared" }

func (s *sharedVPCStrategy) EnsureNetwork(ctx context.Context, vpc *VPC) *protocol.AWSError {
	log := s.h.log.WithRecorder(ctx)

	if !s.h.dockerReady.Load() {
		vpc.DockerNetworkID = ""
		vpc.NetworkStatus = vpcNetworkStatusUnbacked
		return nil
	}

	// Reuse an existing network if another VPC already owns one for this CIDR.
	//nolint:gocritic // shadowing 'ok' here is intentional
	if existing, ok := s.findSharerForCIDR(ctx, vpc.CidrBlock, vpc.VpcID); ok {
		vpc.DockerNetworkID = existing.DockerNetworkID
		// The owner's network *name* as well as its id. The flip lock keys on
		// the name, and a sharer that fell back to deriving its own would take a
		// different lock on the same network — which is exactly the collision
		// the lock exists to prevent.
		vpc.DockerNetworkName = existing.DockerNetworkName
		vpc.NetworkStatus = vpcNetworkStatusShared
		log.Info("vpc network: sharing existing Docker network",
			zap.String("vpc", vpc.VpcID),
			zap.String("cidr", vpc.CidrBlock),
			zap.String("owner", existing.VpcID),
			zap.String("network", existing.DockerNetworkID))
		return nil
	}

	netID, err := s.h.createDockerVPCNetwork(ctx, vpc)
	if err != nil {
		vpc.DockerNetworkID = ""
		vpc.NetworkStatus = vpcNetworkStatusUnbacked
		log.Warn("vpc network: create failed",
			zap.String("vpc", vpc.VpcID),
			zap.String("cidr", vpc.CidrBlock),
			zap.Error(err))
		return nil // swallow so CreateVpc still succeeds; strategy is best-effort
	}
	vpc.DockerNetworkID = netID
	vpc.NetworkStatus = vpcNetworkStatusOK
	return nil
}

func (s *sharedVPCStrategy) AllocatePrivateIP(ctx context.Context, vpc *VPC) (string, error) {
	log := s.h.log.WithRecorder(ctx)

	seq := syntheticIPCounter.Add(1)
	ip, ok := ipForCIDRSequence(vpc.CidrBlock, seq)
	if !ok {
		// Fallback to synthetic 10.0.0.x IP if CIDR parse failed.
		log.Debug("AllocatePrivateIP: CIDR parse failed, using synthetic IP",
			zap.String("vpc", vpc.VpcID),
			zap.String("cidr", vpc.CidrBlock),
			zap.Uint64("seq", uint64(seq)))
		return fmt.Sprintf("10.0.0.%d", seq%254+1), nil
	}
	return ip, nil
}

// ─── strict strategy ────────────────────────────────────────────────────────

// strictVPCStrategy refuses to create a VPC whose CIDR overlaps any existing
// VPC. Once created, VPCs that collide after the fact (e.g. loaded from an
// older store) are left with NetworkStatus=conflict and fail fast when any
// container-backed operation targets them.
type strictVPCStrategy struct {
	h *Handler
}

func (s *strictVPCStrategy) Name() string { return "strict" }

func (s *strictVPCStrategy) EnsureNetwork(ctx context.Context, vpc *VPC) *protocol.AWSError {
	log := s.h.log.WithRecorder(ctx)

	// Reject before persisting if the CIDR overlaps an existing VPC.
	vpcs, aerr := s.h.store.listVPCs(ctx)
	if aerr != nil {
		return aerr
	}
	for _, existing := range vpcs {
		if existing.VpcID == vpc.VpcID {
			continue
		}
		if cidrsOverlap(vpc.CidrBlock, existing.CidrBlock) {
			return &protocol.AWSError{
				Code:       "InvalidVpc.Range",
				Message:    "The CIDR '" + vpc.CidrBlock + "' conflicts with another subnet",
				HTTPStatus: http.StatusBadRequest,
			}
		}
	}

	// No overlap — create the Docker network as normal.
	if !s.h.dockerReady.Load() {
		vpc.DockerNetworkID = ""
		vpc.NetworkStatus = vpcNetworkStatusUnbacked
		return nil
	}
	netID, err := s.h.createDockerVPCNetwork(ctx, vpc)
	if err != nil {
		vpc.DockerNetworkID = ""
		vpc.NetworkStatus = vpcNetworkStatusUnbacked
		log.Warn("vpc network: strict: create failed",
			zap.String("vpc", vpc.VpcID),
			zap.String("cidr", vpc.CidrBlock),
			zap.Error(err))
		return nil
	}
	vpc.DockerNetworkID = netID
	vpc.NetworkStatus = vpcNetworkStatusOK
	return nil
}

func (s *strictVPCStrategy) AllocatePrivateIP(ctx context.Context, vpc *VPC) (string, error) {
	log := s.h.log.WithRecorder(ctx)

	seq := syntheticIPCounter.Add(1)
	ip, ok := ipForCIDRSequence(vpc.CidrBlock, seq)
	if !ok {
		// Fallback to synthetic 10.0.0.x IP if CIDR parse failed.
		log.Debug("AllocatePrivateIP: CIDR parse failed, using synthetic IP",
			zap.String("vpc", vpc.VpcID),
			zap.String("cidr", vpc.CidrBlock),
			zap.Uint64("seq", uint64(seq)))
		return fmt.Sprintf("10.0.0.%d", seq%254+1), nil
	}
	return ip, nil
}

func (s *strictVPCStrategy) Reconcile(ctx context.Context, vpcs []*VPC, networks *vpcNetworkIndex) {
	log := s.h.log.WithRecorder(ctx)

	// Sort deterministically: earliest creation wins, VpcID as tiebreaker.
	// First VPC per CIDR group wins the Docker network; later ones are
	// marked conflict.
	sort.Slice(vpcs, func(i, j int) bool {
		if vpcs[i].CreateTime != vpcs[j].CreateTime {
			return vpcs[i].CreateTime < vpcs[j].CreateTime
		}
		return vpcs[i].VpcID < vpcs[j].VpcID
	})

	// cidrOwner tracks the VPC that claimed the network for a given CIDR in
	// this reconcile pass. Subsequent VPCs with the same CIDR are conflicted.
	cidrOwner := make(map[string]string, len(vpcs)) // cidr → vpcID

	for _, vpc := range vpcs {
		// Check whether any live CIDR in this pass overlaps this VPC.
		conflictOwner := ""
		for existingCIDR, ownerID := range cidrOwner {
			if cidrsOverlap(vpc.CidrBlock, existingCIDR) {
				conflictOwner = ownerID
				break
			}
		}
		if conflictOwner != "" {
			if vpc.NetworkStatus != vpcNetworkStatusConflict || vpc.DockerNetworkID != "" {
				vpc.DockerNetworkID = ""
				vpc.NetworkStatus = vpcNetworkStatusConflict
				_ = s.h.store.putVPC(ctx, vpc)
			}
			log.Warn("reconcile networks: CIDR conflict — VPC has no Docker network",
				zap.String("vpc", vpc.VpcID),
				zap.String("cidr", vpc.CidrBlock),
				zap.String("conflicts_with", conflictOwner))
			continue
		}

		// Try to adopt an existing Docker network for this VPC.
		netID, ok := networks.adopt(vpc)
		if ok {
			if vpc.DockerNetworkID != netID || vpc.NetworkStatus != vpcNetworkStatusOK {
				vpc.DockerNetworkID = netID
				vpc.NetworkStatus = vpcNetworkStatusOK
				_ = s.h.store.putVPC(ctx, vpc)
			}
			cidrOwner[vpc.CidrBlock] = vpc.VpcID
			continue
		}

		netID, err := s.h.createDockerVPCNetwork(ctx, vpc)
		if err != nil {
			log.Warn("reconcile networks: strict: create failed",
				zap.String("vpc", vpc.VpcID),
				zap.String("cidr", vpc.CidrBlock),
				zap.Error(err))
			if vpc.NetworkStatus != vpcNetworkStatusUnbacked || vpc.DockerNetworkID != "" {
				vpc.DockerNetworkID = ""
				vpc.NetworkStatus = vpcNetworkStatusUnbacked
				_ = s.h.store.putVPC(ctx, vpc)
			}
			continue
		}
		vpc.DockerNetworkID = netID
		vpc.NetworkStatus = vpcNetworkStatusOK
		_ = s.h.store.putVPC(ctx, vpc)
		cidrOwner[vpc.CidrBlock] = vpc.VpcID
		log.Info("reconcile networks: strict: recreated VPC network",
			zap.String("vpc", vpc.VpcID),
			zap.String("network", netID))
	}
}

func (s *strictVPCStrategy) OnDelete(ctx context.Context, vpc *VPC) {
	log := s.h.log.WithRecorder(ctx)

	if !s.h.dockerReady.Load() || vpc.DockerNetworkID == "" {
		return
	}
	if err := s.h.removeDockerVPCNetwork(ctx, vpc.DockerNetworkID); err != nil {
		log.Warn("vpc network: strict: remove on delete",
			zap.String("vpc", vpc.VpcID),
			zap.Error(err))
	}
}

// ─── remapped strategy ─────────────────────────────────────────────────────

// remappedVPCStrategy gives every overlapping VPC a unique Docker subnet from
// 100.64.0.0/10, while preserving the user-requested CidrBlock in API output.
// VPCs without overlaps are left on their requested CIDR.
type remappedVPCStrategy struct {
	h         *Handler
	allocator *shadowCIDRAllocator
}

func (s *remappedVPCStrategy) Name() string { return "remapped" }

func (s *remappedVPCStrategy) EnsureNetwork(ctx context.Context, vpc *VPC) *protocol.AWSError {
	log := s.h.log.WithRecorder(ctx)

	vpcs, aerr := s.h.store.listVPCs(ctx)
	if aerr != nil {
		return aerr
	}
	if vpcOverlapsAny(vpc.CidrBlock, vpcs, vpc.VpcID) {
		shadow, ok := s.allocateShadowCIDR(vpcs)
		if !ok {
			return &protocol.AWSError{
				Code:       "InsufficientCidrBlocks",
				Message:    "No available remapped CIDR blocks in 100.64.0.0/10",
				HTTPStatus: http.StatusServiceUnavailable,
			}
		}
		vpc.DockerCidrBlock = shadow
		vpc.NetworkStatus = vpcNetworkStatusRemapped
	} else {
		vpc.DockerCidrBlock = ""
		vpc.NetworkStatus = vpcNetworkStatusOK
	}

	if !s.h.dockerReady.Load() {
		vpc.DockerNetworkID = ""
		vpc.NetworkStatus = vpcNetworkStatusUnbacked
		return nil
	}
	netID, err := s.h.createDockerVPCNetwork(ctx, vpc)
	if err != nil {
		vpc.DockerNetworkID = ""
		vpc.NetworkStatus = vpcNetworkStatusUnbacked
		log.Warn("vpc network: remapped: create failed",
			zap.String("vpc", vpc.VpcID),
			zap.String("cidr", vpc.CidrBlock),
			zap.String("docker_cidr", vpc.DockerCidrBlock),
			zap.Error(err))
		return nil
	}
	vpc.DockerNetworkID = netID
	if vpc.DockerCidrBlock != "" {
		vpc.NetworkStatus = vpcNetworkStatusRemapped
	} else {
		vpc.NetworkStatus = vpcNetworkStatusOK
	}
	return nil
}

func (s *remappedVPCStrategy) AllocatePrivateIP(ctx context.Context, vpc *VPC) (string, error) {
	seq := syntheticIPCounter.Add(1)
	if vpc.DockerCidrBlock == "" {
		ip, ok := ipForCIDRSequence(vpc.CidrBlock, seq)
		if !ok {
			return fmt.Sprintf("10.0.0.%d", seq%254+1), nil
		}
		return ip, nil
	}

	real, host, ok := ipForCIDRSequenceWithHost(vpc.DockerCidrBlock, seq)
	if !ok {
		return fmt.Sprintf("10.0.0.%d", seq%254+1), nil
	}
	fake, ok := ipForCIDRHost(vpc.CidrBlock, host)
	if !ok {
		fake = real
	}
	// Persist the fake→real translation immediately. Trade-off: eager consistency means
	// every IP allocation incurs a store write. For workloads creating many ENIs, this
	// could become a bottleneck. Future optimization could batch translations or cache.
	if err := s.h.store.putVPCIPTranslation(ctx, &VPCIPTranslation{
		VpcID:           vpc.VpcID,
		DockerNetworkID: vpc.DockerNetworkID,
		FakeIP:          fake,
		RealIP:          real,
	}); err != nil {
		return "", fmt.Errorf("persist VPC IP translation: %w", err)
	}
	return fake, nil
}

func (s *remappedVPCStrategy) Reconcile(ctx context.Context, vpcs []*VPC, networks *vpcNetworkIndex) {
	log := s.h.log.WithRecorder(ctx)

	// Sort deterministically: earliest creation wins, VpcID as tiebreaker.
	sort.Slice(vpcs, func(i, j int) bool {
		if vpcs[i].CreateTime != vpcs[j].CreateTime {
			return vpcs[i].CreateTime < vpcs[j].CreateTime
		}
		return vpcs[i].VpcID < vpcs[j].VpcID
	})

	assigned := make([]*VPC, 0, len(vpcs))
	for _, vpc := range vpcs {
		if vpcOverlapsAny(vpc.CidrBlock, assigned, vpc.VpcID) {
			if vpc.DockerCidrBlock == "" {
				shadow, ok := s.allocateShadowCIDR(vpcs)
				if !ok {
					log.Warn("reconcile networks: remapped: no shadow CIDR available",
						zap.String("vpc", vpc.VpcID),
						zap.String("cidr", vpc.CidrBlock))
					vpc.DockerNetworkID = ""
					vpc.NetworkStatus = vpcNetworkStatusUnbacked
					_ = s.h.store.putVPC(ctx, vpc)
					continue
				}
				vpc.DockerCidrBlock = shadow
			}
			vpc.NetworkStatus = vpcNetworkStatusRemapped
		} else {
			vpc.DockerCidrBlock = ""
			vpc.NetworkStatus = vpcNetworkStatusOK
		}

		netID, ok := networks.adopt(vpc)
		if ok {
			if vpc.DockerNetworkID != netID {
				vpc.DockerNetworkID = netID
				_ = s.h.store.putVPC(ctx, vpc)
			}
			assigned = append(assigned, vpc)
			continue
		}

		netID, err := s.h.createDockerVPCNetwork(ctx, vpc)
		if err != nil {
			log.Warn("reconcile networks: remapped: create failed",
				zap.String("vpc", vpc.VpcID),
				zap.String("cidr", vpc.CidrBlock),
				zap.String("docker_cidr", vpc.DockerCidrBlock),
				zap.Error(err))
			if vpc.NetworkStatus != vpcNetworkStatusUnbacked || vpc.DockerNetworkID != "" {
				vpc.DockerNetworkID = ""
				vpc.NetworkStatus = vpcNetworkStatusUnbacked
				_ = s.h.store.putVPC(ctx, vpc)
			}
			continue
		}
		vpc.DockerNetworkID = netID
		_ = s.h.store.putVPC(ctx, vpc)
		assigned = append(assigned, vpc)
	}
}

func (s *remappedVPCStrategy) OnDelete(ctx context.Context, vpc *VPC) {
	log := s.h.log.WithRecorder(ctx)

	s.releaseShadowCIDR(vpc.DockerCidrBlock)
	if !s.h.dockerReady.Load() || vpc.DockerNetworkID == "" {
		return
	}
	if err := s.h.removeDockerVPCNetwork(ctx, vpc.DockerNetworkID); err != nil {
		log.Warn("vpc network: remapped: remove on delete",
			zap.String("vpc", vpc.VpcID),
			zap.Error(err))
	}
}

// ─── shared helpers ──────────────────────────────────────────────────────────

// vpcNetworkIndex is the set of Docker networks one reconcile pass may adopt,
// indexed by ID (liveness), by the resource-id label (original owner) and by
// IPAM subnet (label drift), which is the order adopt tries them in.
//
// One index serves every region of a pass, and that is what makes the pass
// safe to run per region. The store keys VPCs by region while the daemon
// knows nothing of regions, so a region reconciled against a private view of
// the daemon would take every other region's network for litter — which is
// what happened while only the default region was reconciled at all: its pass
// removed the ap-southeast-2 VPC's network as unclaimed. Adopting deletes the
// network from all three maps, so a network claimed by a VPC in one region
// cannot be claimed again by one in another, and what is left when the last
// region has run is what no VPC anywhere names. A nil index adopts nothing,
// which is what a caller with no daemon snapshot passes.
//
// Regions run in turn, so the index also has to know which VPCs exist in
// which region (reserve): a region whose VPC lost its network runs before the
// region whose VPC on the same subnet still has one, and the fallback that
// adopts by subnet would otherwise hand the second's network to the first.
// One CIDR reused across regions is the common case, not a corner — CDK's
// default puts every region on 10.0.0.0/16. Within a region the fallback is
// unchanged: two same-CIDR VPCs there are sharers or a conflict by the
// strategy's own rules, and the earlier one adopting the later one's network
// by subnet is how a VPC created while Docker was down gets backed.
type vpcNetworkIndex struct {
	byID         map[string]*docker.NetworkSummary
	byResourceID map[string]*docker.NetworkSummary
	bySubnet     map[string]*docker.NetworkSummary
	// reserved maps every VPC ID the store holds to the region it lives in.
	// A network labelled for a VPC in another region is that VPC's to adopt
	// by ID or label when its turn comes, never this region's to take by
	// subnet.
	reserved map[string]string
	// region is the one whose pass is running, set by the Handler before each
	// region's Reconcile. The passes are sequential, so one field suffices.
	region string
}

// newVPCNetworkIndex indexes existing. The maps point into the slice.
func newVPCNetworkIndex(existing []docker.NetworkSummary) *vpcNetworkIndex {
	ix := &vpcNetworkIndex{
		byID:         make(map[string]*docker.NetworkSummary, len(existing)),
		byResourceID: make(map[string]*docker.NetworkSummary, len(existing)),
		bySubnet:     make(map[string]*docker.NetworkSummary, len(existing)),
		reserved:     make(map[string]string),
	}
	for i := range existing {
		n := &existing[i]
		ix.byID[n.ID] = n
		if rid := n.ResourceID(); rid != "" {
			ix.byResourceID[rid] = n
		}
		if sub := n.Subnet(); sub != "" {
			ix.bySubnet[sub] = n
		}
	}
	return ix
}

// reserve records vpcs as existing in region, for every region, before any
// region's pass runs. See the type comment.
func (ix *vpcNetworkIndex) reserve(region string, vpcs []*VPC) {
	if ix == nil {
		return
	}
	for _, vpc := range vpcs {
		if vpc != nil && vpc.VpcID != "" {
			ix.reserved[vpc.VpcID] = region
		}
	}
}

// enter names the region whose pass is about to run.
func (ix *vpcNetworkIndex) enter(region string) {
	if ix != nil {
		ix.region = region
	}
}

// adopt finds the network vpc should be backed by — the one its record names,
// else the one labelled with its ID, else the one on its subnet — and claims
// it, so each network is adopted by at most one VPC. It also records the
// adopted network's name on the record: the flip lock keys on that name and
// must not have to ask Docker for it — see Handler.vpcNetworkLockKey — and
// under the shared strategy the name belongs to whichever VPC created the
// network, which no sharer can derive.
//
// The subnet fallback is for label drift, for a network whose VPC is gone,
// and for a same-region VPC on the same CIDR. It does not take a network
// labelled for a VPC that still exists in another region — that VPC adopts it
// by ID or label when its own region's turn comes — so a record that lost its
// network is left to create one, and Docker's refusal of the overlapping pool
// lands on the VPC that actually has no network.
func (ix *vpcNetworkIndex) adopt(vpc *VPC) (string, bool) {
	if ix == nil {
		return "", false
	}
	if vpc.DockerNetworkID != "" {
		if n, ok := ix.byID[vpc.DockerNetworkID]; ok {
			return ix.claim(vpc, n), true
		}
	}
	if n, ok := ix.byResourceID[vpc.VpcID]; ok {
		return ix.claim(vpc, n), true
	}
	if n, ok := ix.bySubnet[preferredDockerSubnet(vpc)]; ok && !ix.belongsElsewhere(n, vpc) {
		return ix.claim(vpc, n), true
	}
	return "", false
}

// belongsElsewhere reports whether n is labelled for a VPC other than vpc
// that the store still holds in a region other than the one being reconciled.
func (ix *vpcNetworkIndex) belongsElsewhere(n *docker.NetworkSummary, vpc *VPC) bool {
	rid := n.ResourceID()
	if rid == "" || rid == vpc.VpcID {
		return false
	}
	region, live := ix.reserved[rid]
	return live && region != ix.region
}

// claim takes n out of every index for vpc and returns its ID.
func (ix *vpcNetworkIndex) claim(vpc *VPC, n *docker.NetworkSummary) string {
	delete(ix.byID, n.ID)
	delete(ix.byResourceID, n.ResourceID())
	delete(ix.bySubnet, n.Subnet())
	vpc.DockerNetworkName = n.Name
	return n.ID
}

// unclaimed returns, by ID, the networks no VPC adopted.
func (ix *vpcNetworkIndex) unclaimed() map[string]*docker.NetworkSummary {
	if ix == nil {
		return nil
	}
	return ix.byID
}

func preferredDockerSubnet(vpc *VPC) string {
	if vpc.DockerCidrBlock != "" {
		return vpc.DockerCidrBlock
	}
	return vpc.CidrBlock
}

// cidrsOverlap reports whether two CIDR blocks have any address in common.
// Returns false if either CIDR fails to parse.
func cidrsOverlap(a, b string) bool {
	_, netA, errA := net.ParseCIDR(a)
	_, netB, errB := net.ParseCIDR(b)
	if errA != nil || errB != nil {
		return false
	}
	return netA.Contains(netB.IP) || netB.Contains(netA.IP)
}

func vpcOverlapsAny(cidr string, vpcs []*VPC, excludeID string) bool {
	for _, v := range vpcs {
		if v.VpcID == excludeID {
			continue
		}
		if cidrsOverlap(cidr, v.CidrBlock) {
			return true
		}
	}
	return false
}

func allocateShadowCIDR(vpcs []*VPC) (string, bool) {
	for secondOctet := 64; secondOctet <= 127; secondOctet++ {
		candidate := fmt.Sprintf("100.%d.0.0/16", secondOctet)
		inUse := false
		for _, v := range vpcs {
			if v.DockerCidrBlock == candidate {
				inUse = true
				break
			}
			if cidrsOverlap(v.CidrBlock, candidate) {
				inUse = true
				break
			}
		}
		if !inUse {
			return candidate, true
		}
	}
	return "", false
}

// shadowCIDRAllocator manages the allocation and release of shadow CIDR blocks
// used by remappedVPCStrategy to provide unique Docker networks for overlapping VPCs.
// Lifecycle: Allocate() is called during CreateVpc to assign a shadow CIDR from 100.64.0.0/10
// (CGNAT space). Release() is called from remappedVPCStrategy.OnDelete to return the CIDR
// to the released pool for potential reuse. The allocator prefers reusing released CIDRs
// over allocating fresh ones to minimize fragmentation in CGNAT space.
// Thread-safe: all accesses are protected by mu.
type shadowCIDRAllocator struct {
	mu       sync.Mutex
	released map[string]struct{}
}

func newShadowCIDRAllocator() *shadowCIDRAllocator {
	return &shadowCIDRAllocator{released: make(map[string]struct{})}
}

func (a *shadowCIDRAllocator) Allocate(vpcs []*VPC) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	for cidr := range a.released {
		inUse := false
		for _, v := range vpcs {
			if v.DockerCidrBlock == cidr || cidrsOverlap(v.CidrBlock, cidr) {
				inUse = true
				break
			}
		}
		if inUse {
			continue
		}
		delete(a.released, cidr)
		return cidr, true
	}

	return allocateShadowCIDR(vpcs)
}

func (a *shadowCIDRAllocator) Release(cidr string) {
	if cidr == "" {
		return
	}
	a.mu.Lock()
	a.released[cidr] = struct{}{}
	a.mu.Unlock()
}

// allocateShadowCIDRForVPCs attempts to allocate a shadow CIDR from the pool of
// previously released CIDRs, or falls back to allocating a fresh one from 100.64.0.0/10.
// This method delegates to the allocator's Allocate() for pool management.
func (s *remappedVPCStrategy) allocateShadowCIDR(vpcs []*VPC) (string, bool) {
	if s.allocator == nil {
		return allocateShadowCIDR(vpcs)
	}
	return s.allocator.Allocate(vpcs)
}

func (s *remappedVPCStrategy) releaseShadowCIDR(cidr string) {
	if s.allocator == nil {
		return
	}
	s.allocator.Release(cidr)
}

func (s *sharedVPCStrategy) Reconcile(ctx context.Context, vpcs []*VPC, networks *vpcNetworkIndex) {
	log := s.h.log.WithRecorder(ctx)

	// Sort for deterministic owner selection: earliest creation wins,
	// VpcID as tiebreaker.
	sort.Slice(vpcs, func(i, j int) bool {
		if vpcs[i].CreateTime != vpcs[j].CreateTime {
			return vpcs[i].CreateTime < vpcs[j].CreateTime
		}
		return vpcs[i].VpcID < vpcs[j].VpcID
	})

	// cidrOwner tracks which VPC claimed the Docker network for a given CIDR
	// during this reconcile pass. Later VPCs with the same CIDR reuse it and
	// are marked as sharers.
	cidrOwner := make(map[string]*VPC, len(vpcs))

	for _, vpc := range vpcs {
		if owner, ok := cidrOwner[vpc.CidrBlock]; ok && vpc != owner {
			if vpc.DockerNetworkID != owner.DockerNetworkID ||
				vpc.DockerNetworkName != owner.DockerNetworkName ||
				vpc.NetworkStatus != vpcNetworkStatusShared {
				vpc.DockerNetworkID = owner.DockerNetworkID
				// The owner's *name*, not this VPC's: the flip lock keys on it,
				// and two sharers that derived their own names would take two
				// different locks on one network.
				vpc.DockerNetworkName = owner.DockerNetworkName
				vpc.NetworkStatus = vpcNetworkStatusShared
				_ = s.h.store.putVPC(ctx, vpc)
			}
			continue
		}

		if netID, ok := networks.adopt(vpc); ok {
			if vpc.DockerNetworkID != netID || vpc.NetworkStatus != vpcNetworkStatusOK {
				vpc.DockerNetworkID = netID
				vpc.NetworkStatus = vpcNetworkStatusOK
				_ = s.h.store.putVPC(ctx, vpc)
			}
			cidrOwner[vpc.CidrBlock] = vpc
			continue
		}

		netID, err := s.h.createDockerVPCNetwork(ctx, vpc)
		if err != nil {
			log.Warn("reconcile networks: create failed",
				zap.String("vpc", vpc.VpcID),
				zap.String("cidr", vpc.CidrBlock),
				zap.Error(err))
			if vpc.NetworkStatus != vpcNetworkStatusUnbacked || vpc.DockerNetworkID != "" {
				vpc.DockerNetworkID = ""
				vpc.NetworkStatus = vpcNetworkStatusUnbacked
				_ = s.h.store.putVPC(ctx, vpc)
			}
			continue
		}
		vpc.DockerNetworkID = netID
		vpc.NetworkStatus = vpcNetworkStatusOK
		_ = s.h.store.putVPC(ctx, vpc)
		cidrOwner[vpc.CidrBlock] = vpc
		log.Info("reconcile networks: recreated VPC network",
			zap.String("vpc", vpc.VpcID),
			zap.String("network", netID))
	}
}

func (s *sharedVPCStrategy) OnDelete(ctx context.Context, vpc *VPC) {
	log := s.h.log.WithRecorder(ctx)

	if !s.h.dockerReady.Load() || vpc.DockerNetworkID == "" {
		return
	}
	// Don't remove the network if another VPC still uses it (shared mode).
	remaining, aerr := s.h.store.listVPCs(ctx)
	if aerr == nil {
		for _, other := range remaining {
			if other.DockerNetworkID == vpc.DockerNetworkID {
				log.Info("vpc network: retaining shared network after delete",
					zap.String("vpc", vpc.VpcID),
					zap.String("network", vpc.DockerNetworkID),
					zap.String("still_used_by", other.VpcID))
				return
			}
		}
	}
	if err := s.h.removeDockerVPCNetwork(ctx, vpc.DockerNetworkID); err != nil {
		log.Warn("vpc network: remove on delete",
			zap.String("vpc", vpc.VpcID),
			zap.Error(err))
	}
}

// findSharerForCIDR returns an existing VPC (other than excludeID) whose CIDR
// matches cidr and has a live Docker network. Used by CreateVpc to decide
// whether to reuse a network. The lookup is O(n) in the VPC count, which is
// cheap at realistic scales (hundreds of VPCs max).
func (s *sharedVPCStrategy) findSharerForCIDR(ctx context.Context, cidr, excludeID string) (*VPC, bool) {
	vpcs, aerr := s.h.store.listVPCs(ctx)
	if aerr != nil {
		return nil, false
	}
	for _, v := range vpcs {
		if v.VpcID == excludeID || v.CidrBlock != cidr || v.DockerNetworkID == "" {
			continue
		}
		return v, true
	}
	return nil, false
}
