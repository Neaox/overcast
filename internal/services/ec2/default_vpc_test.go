package ec2

import (
	"context"
	"encoding/xml"
	"net/url"
	"slices"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

func defaultVPCHandler(t *testing.T) *Handler {
	t.Helper()
	return newHandler(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", Network: "overcast"},
		state.NewMemoryStore(),
		serviceutil.NewServiceLogger(zap.NewNop(), "ec2"),
		clock.New(),
	)
}

// AWS gives every region a default VPC. Overcast seeded none, so
// `DescribeVpcs --filters Name=isDefault,Values=true` and CDK's
// Vpc.fromLookup(isDefault: true) found nothing to adopt.
func TestEnsureDefaultVPC_seedsWhatAVPCLookupReads(t *testing.T) {
	h := defaultVPCHandler(t)
	ctx := context.Background()

	vpc, aerr := h.ensureDefaultVPC(ctx)
	if aerr != nil {
		t.Fatalf("ensureDefaultVPC: %v", aerr.Message)
	}

	if !vpc.IsDefault {
		t.Error("seeded VPC is not marked default")
	}
	if vpc.CidrBlock != defaultVPCCidr {
		t.Errorf("CidrBlock = %q, want %q", vpc.CidrBlock, defaultVPCCidr)
	}

	// Its network is the shared data plane, not a per-VPC bridge. This is what
	// makes "no VPC" and "the default VPC" the same place.
	if vpc.DockerNetworkID != "overcast" {
		t.Errorf("DockerNetworkID = %q, want the default data plane", vpc.DockerNetworkID)
	}

	subnets, _ := h.store.listSubnets(ctx)
	if len(subnets) != defaultSubnetsPerVPC {
		t.Errorf("subnets = %d, want %d", len(subnets), defaultSubnetsPerVPC)
	}
	for _, s := range subnets {
		if s.VpcID != vpc.VpcID {
			t.Errorf("subnet %s belongs to %q, want the default VPC", s.SubnetID, s.VpcID)
		}
	}

	// CDK classifies a subnet as public from a default route through an
	// internet gateway, so both records have to be there.
	igws, _ := h.store.listInternetGateways(ctx)
	if len(igws) != 1 || len(igws[0].Attachments) != 1 || igws[0].Attachments[0].VpcID != vpc.VpcID {
		t.Fatalf("internet gateways = %#v, want one attached to the default VPC", igws)
	}
	tables, _ := h.store.listRouteTables(ctx)
	if len(tables) != 1 {
		t.Fatalf("route tables = %d, want 1", len(tables))
	}
	var foundDefaultRoute bool
	for _, route := range tables[0].Routes {
		if route.DestinationCidrBlock == "0.0.0.0/0" && route.GatewayID == igws[0].InternetGatewayID {
			foundDefaultRoute = true
		}
	}
	if !foundDefaultRoute {
		t.Errorf("main route table has no default route through the gateway: %#v", tables[0].Routes)
	}
}

func TestEnsureDefaultVPC_isIdempotent(t *testing.T) {
	h := defaultVPCHandler(t)
	ctx := context.Background()

	first, _ := h.ensureDefaultVPC(ctx)
	second, aerr := h.ensureDefaultVPC(ctx)
	if aerr != nil {
		t.Fatalf("ensureDefaultVPC: %v", aerr.Message)
	}

	if first.VpcID != second.VpcID {
		t.Fatalf("seeded twice: %q then %q", first.VpcID, second.VpcID)
	}
	vpcs, _ := h.store.listVPCs(ctx)
	if len(vpcs) != 1 {
		t.Fatalf("VPCs = %d, want 1", len(vpcs))
	}
}

// The default VPC's network is the emulator's own data plane: every container
// without a VPC is attached to it. Removing it with the record would sever all
// of them, so the record goes and the network stays.
func TestDefaultVPCGuard_deleteKeepsTheNetwork(t *testing.T) {
	h := defaultVPCHandler(t)
	ctx := context.Background()
	vpc, _ := h.ensureDefaultVPC(ctx)

	inner := &recordingStrategy{}
	guard := &defaultVPCGuard{inner: inner, h: h}

	guard.OnDelete(ctx, vpc)
	if inner.onDeleteCalls != 0 {
		t.Error("OnDelete reached the strategy for the default VPC")
	}

	// An ordinary VPC still goes through.
	guard.OnDelete(ctx, &VPC{VpcID: "vpc-other"})
	if inner.onDeleteCalls != 1 {
		t.Errorf("OnDelete calls for an ordinary VPC = %d, want 1", inner.onDeleteCalls)
	}
}

// Reconcile would otherwise adopt a labelled EC2 network for the default VPC or
// create a fresh overcast-vpc-* bridge, and the record would stop naming the
// plane its containers are actually on.
func TestDefaultVPCGuard_withholdsTheDefaultFromReconcile(t *testing.T) {
	h := defaultVPCHandler(t)
	ctx := context.Background()
	vpc, _ := h.ensureDefaultVPC(ctx)
	other := &VPC{VpcID: "vpc-other", CidrBlock: "10.0.0.0/16"}

	inner := &recordingStrategy{}
	guard := &defaultVPCGuard{inner: inner, h: h}

	guard.Reconcile(ctx, []*VPC{vpc, other}, nil)

	if len(inner.reconciled) != 1 || inner.reconciled[0].VpcID != "vpc-other" {
		t.Fatalf("strategy saw %#v, want only the ordinary VPC", inner.reconciled)
	}
}

// A default VPC whose record drifted is put back on the plane rather than
// rebuilt, so a restart cannot leave it naming a network that is not there.
func TestDefaultVPCGuard_reassertsADriftedNetwork(t *testing.T) {
	h := defaultVPCHandler(t)
	ctx := context.Background()
	vpc, _ := h.ensureDefaultVPC(ctx)

	vpc.DockerNetworkID = "overcast-vpc-stale"
	vpc.NetworkStatus = vpcNetworkStatusUnbacked
	if aerr := h.store.putVPC(ctx, vpc); aerr != nil {
		t.Fatalf("putVPC: %v", aerr.Message)
	}

	guard := &defaultVPCGuard{inner: &recordingStrategy{}, h: h}
	guard.Reconcile(ctx, []*VPC{vpc}, nil)

	got, aerr := h.store.getVPC(ctx, vpc.VpcID)
	if aerr != nil {
		t.Fatalf("getVPC: %v", aerr.Message)
	}
	if got.DockerNetworkID != "overcast" || got.NetworkStatus != vpcNetworkStatusOK {
		t.Fatalf("after reconcile: network=%q status=%q, want the data plane and ok",
			got.DockerNetworkID, got.NetworkStatus)
	}
}

// Seeding a default VPC means a region routinely holds more than one VPC. CDK's
// context provider sends filters, treats the response as already filtered, and
// fails unless exactly one comes back — so an unfiltered DescribeVpcs would
// break `Vpc.fromLookup` for anyone who has a VPC of their own.
func TestDescribeVpcs_selectors(t *testing.T) {
	cases := []struct {
		name   string
		params url.Values
		want   []string
	}{
		{name: "no selectors returns everything", params: url.Values{}, want: []string{"vpc-default", "vpc-mine"}},
		{name: "VpcId.N", params: url.Values{"VpcId.1": {"vpc-mine"}}, want: []string{"vpc-mine"}},
		{name: "vpc-id filter", params: filterParams("vpc-id", "vpc-mine"), want: []string{"vpc-mine"}},
		{name: "isDefault true", params: filterParams("isDefault", "true"), want: []string{"vpc-default"}},
		{name: "isDefault false", params: filterParams("isDefault", "false"), want: []string{"vpc-mine"}},
		{name: "isDefault is case-insensitive", params: filterParams("isDefault", "True"), want: []string{"vpc-default"}},
		{name: "a value may be a wildcard", params: filterParams("vpc-id", "vpc-m*"), want: []string{"vpc-mine"}},
		{
			name: "selectors are AND-ed",
			params: url.Values{
				"VpcId.1":          {"vpc-mine"},
				"Filter.1.Name":    {"isDefault"},
				"Filter.1.Value.1": {"true"},
			},
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := defaultVPCHandler(t)
			ctx := context.Background()
			for _, v := range []*VPC{
				{VpcID: "vpc-default", State: "available", CidrBlock: "172.31.0.0/16", IsDefault: true},
				{VpcID: "vpc-mine", State: "available", CidrBlock: "10.42.0.0/16"},
			} {
				if aerr := h.store.putVPC(ctx, v); aerr != nil {
					t.Fatalf("putVPC: %v", aerr.Message)
				}
			}

			var resp xmlDescribeVpcsResponse
			if err := xml.Unmarshal(describe(t, h, h.DescribeVpcs, tc.params), &resp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			ids := make([]string, 0, len(resp.VpcSet))
			for _, v := range resp.VpcSet {
				ids = append(ids, v.VpcID)
			}
			if !slices.Equal(ids, tc.want) {
				t.Fatalf("DescribeVpcs = %#v, want %#v", ids, tc.want)
			}
		})
	}
}

// On AWS a deleted default VPC stays deleted until CreateDefaultVpc. Reseeding
// on the next describe would contradict the delete and leak the subnets,
// gateway and security group the previous one left behind.
func TestEnsureDefaultVPC_doesNotReseedAfterDelete(t *testing.T) {
	h := defaultVPCHandler(t)
	ctx := context.Background()

	vpc, _ := h.ensureDefaultVPC(ctx)
	h.vpcStrategy.OnDelete(ctx, vpc)
	if aerr := h.store.deleteVPC(ctx, vpc.VpcID); aerr != nil {
		t.Fatalf("deleteVPC: %v", aerr.Message)
	}

	got, aerr := h.ensureDefaultVPC(ctx)
	if aerr != nil {
		t.Fatalf("ensureDefaultVPC: %v", aerr.Message)
	}
	if got != nil {
		t.Fatalf("reseeded %q after the default VPC was deleted", got.VpcID)
	}
	vpcs, _ := h.store.listVPCs(ctx)
	if len(vpcs) != 0 {
		t.Fatalf("VPCs = %d, want 0", len(vpcs))
	}
}

// recordingStrategy is a vpcNetworkStrategy that only remembers what it was
// asked to do, so the guard's delegation is observable without Docker.
type recordingStrategy struct {
	onDeleteCalls int
	reconciled    []*VPC
}

func (s *recordingStrategy) Name() string { return "recording" }

func (s *recordingStrategy) EnsureNetwork(context.Context, *VPC) *protocol.AWSError { return nil }

func (s *recordingStrategy) AllocatePrivateIP(context.Context, *VPC) (string, error) {
	return "10.0.0.1", nil
}

func (s *recordingStrategy) Reconcile(_ context.Context, vpcs []*VPC, _ *vpcNetworkIndex) {
	s.reconciled = vpcs
}

func (s *recordingStrategy) OnDelete(context.Context, *VPC) { s.onDeleteCalls++ }
