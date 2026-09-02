package ec2

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/protocol"
)

// The default VPC.
//
// Every AWS account has one per region, with a default subnet per availability
// zone, an internet gateway, a main route table carrying the default route, and
// a default security group that permits all traffic between its own members. A
// resource created without an explicit VPC lands there. Overcast seeded none,
// so `DescribeVpcs --filters Name=isDefault,Values=true` returned nothing and
// CDK's `Vpc.fromLookup(isDefault: true)` found no VPC to adopt.
//
// Seeding one closes that gap, and does something else worth more: it collapses
// "this resource named no VPC" and "this resource is in the default VPC" into a
// single case. The default VPC's backing network *is* the default data plane
// (internal/dataplane), so a container with no VPC and a container in the
// default VPC are on the same Docker network by construction rather than by
// coincidence — which is what lets the later enforcement phase ask one question,
// "is the caller's plane the target's plane", instead of special-casing the
// empty VPC ID.
//
// The consequence is that this VPC's network is not a per-VPC bridge and must
// not be treated as one. defaultVPCGuard below is what keeps VPC lifecycle
// away from it.

const (
	// defaultVPCCidr is AWS's own default-VPC range, so a stack that hardcodes
	// an expectation about it sees what it would on AWS.
	defaultVPCCidr = "172.31.0.0/16"

	// defaultSubnetsPerVPC bounds how many default subnets are seeded — one per
	// availability zone, matching DescribeAvailabilityZones.
	defaultSubnetsPerVPC = 3
)

// ensureDefaultVPC seeds the region's default VPC if it has none, and returns
// it. Idempotent and safe under concurrent requests.
//
// Called from the read paths that would observe it rather than at startup: a
// region materialises on first use, and seeding every configured region eagerly
// would create records for regions nobody touches.
func (h *Handler) ensureDefaultVPC(ctx context.Context) (*VPC, *protocol.AWSError) {
	region := h.store.region(ctx)

	// One seeder per region. Two concurrent DescribeVpcs calls would otherwise
	// both find nothing and both seed, leaving two default VPCs.
	mu, _ := h.defaultVPCLocks.LoadOrStore(region, &sync.Mutex{})
	lock := mu.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	vpcs, aerr := h.store.listVPCs(ctx)
	if aerr != nil {
		return nil, aerr
	}
	for _, v := range vpcs {
		if v.IsDefault {
			return v, nil
		}
	}

	// A default VPC that was deleted stays deleted, as on AWS, where
	// CreateDefaultVpc is the only way back. Reseeding on the next describe
	// would both contradict the delete and leak the subnets, gateway and
	// security group the previous one left behind.
	if deleted, derr := h.defaultVPCDeleted(ctx); derr != nil {
		return nil, derr
	} else if deleted {
		return nil, nil
	}
	return h.seedDefaultVPC(ctx)
}

// defaultVPCTombstone marks a region whose default VPC was deleted. It is a
// store record rather than process state so the decision survives a restart.
const defaultVPCTombstone = "deleted"

func (h *Handler) defaultVPCDeleted(ctx context.Context) (bool, *protocol.AWSError) {
	got, found, err := h.store.store.Get(ctx, nsDefaultVPC, h.store.region(ctx))
	if err != nil {
		return false, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return found && got == defaultVPCTombstone, nil
}

// markDefaultVPCDeleted records that this region's default VPC is gone.
func (h *Handler) markDefaultVPCDeleted(ctx context.Context) {
	if err := h.store.store.Set(ctx, nsDefaultVPC, h.store.region(ctx), defaultVPCTombstone); err != nil {
		h.log.WithRecorder(ctx).Warn("could not record the default VPC deletion — "+
			"it may be seeded again on the next describe",
			zap.Error(err))
	}
}

// seedDefaultVPC writes the default VPC and the records CDK's VPC lookup and
// subnet classification actually read. The caller holds the region's lock.
func (h *Handler) seedDefaultVPC(ctx context.Context) (*VPC, *protocol.AWSError) {
	log := h.log.WithRecorder(ctx)

	vpc := &VPC{
		VpcID:      fmt.Sprintf("vpc-%s", shortID()),
		CidrBlock:  defaultVPCCidr,
		State:      "available",
		IsDefault:  true,
		CreateTime: h.clk.Now().UnixMilli(),

		// Every VPC — including the seeded default — gets a DHCP options set;
		// see CreateVpc (#1277) for why this must never be left empty.
		DhcpOptionsId: fmt.Sprintf("dopt-%s", shortID()),

		// The default data plane, by name. Every container Overcast starts
		// without a VPC is already here, which is the point: the default VPC
		// describes what the emulator was doing anyway.
		//
		// Deliberately not created through the strategy — the plane is owned by
		// the Docker supervisor, is not labelled as an EC2 resource, and
		// outlives any VPC record pointing at it.
		DockerNetworkID: h.cfg.Network,
		NetworkStatus:   vpcNetworkStatusOK,

		// The account's default VPC has both DNS attributes on, matching
		// real AWS.
		EnableDnsSupport:   true,
		EnableDnsHostnames: true,
	}

	// The VPC record is written last, so it is the commit point: everything
	// below is keyed to a VpcID nothing yet reports, and ensureDefaultVPC's
	// "is one already seeded" test is exactly this record. A store failure
	// part-way therefore leaves no half-seeded default VPC to be returned as
	// complete on the next call.

	igwID := fmt.Sprintf("igw-%s", shortID())
	if aerr := h.store.putInternetGateway(ctx, &InternetGateway{
		InternetGatewayID: igwID,
		Attachments:       []IGWAttachment{{VpcID: vpc.VpcID, State: "attached"}},
	}); aerr != nil {
		return nil, aerr
	}

	// The main route table, plus a default route through the gateway — which is
	// what CDK reads to classify a subnet as public rather than private.
	rt := newMainRouteTable(vpc.VpcID, defaultVPCCidr)
	rt.Routes = append(rt.Routes, Route{
		DestinationCidrBlock: "0.0.0.0/0",
		GatewayID:            igwID,
		Origin:               "CreateRoute",
	})
	if aerr := h.store.putRouteTable(ctx, rt); aerr != nil {
		return nil, aerr
	}

	// A default subnet per AZ, on AWS's /20 stride within the VPC range.
	region := h.store.region(ctx)
	for i := range defaultSubnetsPerVPC {
		subnet := &Subnet{
			SubnetID:         fmt.Sprintf("subnet-%s", shortID()),
			VpcID:            vpc.VpcID,
			CidrBlock:        fmt.Sprintf("172.31.%d.0/20", i*16),
			AvailabilityZone: fmt.Sprintf("%s%c", region, 'a'+rune(i)),
			State:            "available",
			// Default VPC subnets are public by default on real AWS.
			MapPublicIpOnLaunch: true,
		}
		if aerr := h.store.putSubnet(ctx, subnet); aerr != nil {
			return nil, aerr
		}
	}

	// The default security group. Unenforced, as every security group is here,
	// but its absence is visible: a stack that references the default group by
	// lookup would find nothing.
	if aerr := h.store.putSecurityGroup(ctx, &SecurityGroup{
		GroupID:     fmt.Sprintf("sg-%s", shortID()),
		GroupName:   "default",
		Description: "default VPC security group",
		VpcID:       vpc.VpcID,
	}); aerr != nil {
		return nil, aerr
	}

	if aerr := h.store.putVPC(ctx, vpc); aerr != nil {
		return nil, aerr
	}

	log.Info("seeded the region's default VPC",
		zap.String("vpc", vpc.VpcID),
		zap.String("region", region),
		zap.String("network", vpc.DockerNetworkID))
	return vpc, nil
}

// ensureDefaultVPCQuietly is ensureDefaultVPC for read paths that have no good
// way to report a seeding failure. A region that could not be seeded still
// answers with whatever it does have, which is what it did before there was a
// default VPC at all.
func (h *Handler) ensureDefaultVPCQuietly(ctx context.Context) {
	if _, aerr := h.ensureDefaultVPC(ctx); aerr != nil {
		h.log.WithRecorder(ctx).Warn("could not seed the default VPC",
			zap.String("error", aerr.Message))
	}
}

// ─── the guard ──────────────────────────────────────────────────────────────

// defaultVPCGuard wraps a vpcNetworkStrategy so VPC lifecycle never touches the
// default VPC's backing network.
//
// That network is the emulator's own default data plane: the Docker supervisor
// creates it, every container without a VPC is on it, and it must outlive any
// VPC record. Left to the strategies it would be adopted as an orphan,
// recreated as `overcast-vpc-*`, torn down with the record, or recreated to
// flip `--internal` — each of which severs every attached container.
//
// A decorator rather than a check inside each strategy: there are three of them
// plus a reserved fourth, and the rule belongs to the default VPC, not to any
// one mapping policy.
type defaultVPCGuard struct {
	inner vpcNetworkStrategy
	h     *Handler
}

func (g *defaultVPCGuard) Name() string { return g.inner.Name() }

func (g *defaultVPCGuard) EnsureNetwork(ctx context.Context, vpc *VPC) *protocol.AWSError {
	if vpc != nil && vpc.IsDefault {
		vpc.DockerNetworkID = g.h.cfg.Network
		vpc.NetworkStatus = vpcNetworkStatusOK
		return nil
	}
	return g.inner.EnsureNetwork(ctx, vpc)
}

func (g *defaultVPCGuard) AllocatePrivateIP(ctx context.Context, vpc *VPC) (string, error) {
	return g.inner.AllocatePrivateIP(ctx, vpc)
}

// Reconcile withholds default VPCs from the strategy and re-asserts their
// backing network instead. Passing one through would have it adopt a labelled
// EC2 network or create a fresh `overcast-vpc-*` bridge, and either way the
// record would stop naming the plane its containers are actually on.
func (g *defaultVPCGuard) Reconcile(ctx context.Context, vpcs []*VPC, existing []docker.NetworkSummary) {
	rest := make([]*VPC, 0, len(vpcs))
	for _, vpc := range vpcs {
		if vpc == nil || !vpc.IsDefault {
			rest = append(rest, vpc)
			continue
		}
		if vpc.DockerNetworkID != g.h.cfg.Network || vpc.NetworkStatus != vpcNetworkStatusOK {
			vpc.DockerNetworkID = g.h.cfg.Network
			vpc.NetworkStatus = vpcNetworkStatusOK
			_ = g.h.store.putVPC(ctx, vpc)
		}
	}
	g.inner.Reconcile(ctx, rest, existing)
}

// OnDelete drops the record and leaves the network. AWS lets you delete a
// default VPC (CreateDefaultVpc is the way back), so the call is allowed —
// but removing the plane would take every container in the emulator with it.
func (g *defaultVPCGuard) OnDelete(ctx context.Context, vpc *VPC) {
	if vpc != nil && vpc.IsDefault {
		g.h.markDefaultVPCDeleted(ctx)
		g.h.log.WithRecorder(ctx).Info(
			"default VPC deleted — its record is gone, the shared data plane is not",
			zap.String("vpc", vpc.VpcID), zap.String("network", vpc.DockerNetworkID))
		return
	}
	g.inner.OnDelete(ctx, vpc)
}

// An internet-gateway change on the default VPC is declined the same way, in
// Handler.changeVPCGateway: recorded as metadata, network left alone.
