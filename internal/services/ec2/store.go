package ec2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	"github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/middleware"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

const (
	nsVPCs                   = "ec2:vpcs"
	nsSubnets                = "ec2:subnets"
	nsSecurityGroups         = "ec2:security-groups"
	nsInstances              = "ec2:instances"
	nsKeyPairs               = "ec2:keypairs"
	nsRouteTables            = "ec2:route-tables"
	nsInternetGateways       = "ec2:internet-gateways"
	nsVpnGateways            = "ec2:vpn-gateways"
	nsVpcPeeringConnections  = "ec2:vpc-peering-connections"
	nsTags                   = "ec2:tags"
	nsElasticIPs             = "ec2:elastic-ips"
	nsNatGateways            = "ec2:nat-gateways"
	nsNetworkInterfaces      = "ec2:network-interfaces"
	nsVpcEndpoints           = "ec2:vpc-endpoints"
	nsDefaultVPC             = "ec2:default-vpc"
	nsVPCIPTranslations      = "ec2:vpc-ip-translations"
	nsVPCIPTranslationsReal  = "ec2:vpc-ip-translations-real"
	nsPlacements             = "ec2:placements"
	nsLaunchTemplates        = "ec2:launch-templates"
	nsLaunchTemplateVersions = "ec2:launch-template-versions"
)

// VPC represents an EC2 VPC resource.
type VPC struct {
	VpcID           string `json:"VpcId"`
	CidrBlock       string `json:"CidrBlock"`
	State           string `json:"State"`
	IsDefault       bool   `json:"IsDefault,omitempty"`
	DockerNetworkID string `json:"DockerNetworkId,omitempty"`

	// DockerNetworkName is the name of that network, recorded so the flip lock
	// can be keyed without asking Docker.
	//
	// The name is the lock's key because it survives a recreate, which is
	// exactly what the id does not (docker.LockNetwork). Resolving it by
	// inspecting the id would defeat that: during a flip the network is removed
	// before the successor id is written, so a sharer reading the old id in that
	// window gets a 404, falls back to keying on the id, and takes a different
	// and free mutex — the very race the name-keying closes.
	//
	// Under `strict` and `remapped` the name is cfg.VPCNetwork(VpcID); under
	// `shared` it is the *owner* VPC's name, which no other VPC can derive. So
	// it is written down rather than computed. Empty on a record from before
	// this field, which falls back to the derivable name.
	DockerNetworkName string `json:"DockerNetworkName,omitempty"`

	// DhcpOptionsId is the DHCP options set associated with this VPC. Real AWS
	// assigns every VPC the account's default set (a `dopt-` ID) at creation and
	// reports it stably thereafter; AssociateDhcpOptions is the only way it
	// changes. Minted once at create time and persisted here so DescribeVpcs
	// echoes the same value CreateVpc returned, rather than losing it on the
	// next read (#1277).
	DhcpOptionsId string `json:"DhcpOptionsId,omitempty"`

	// NetworkStatus describes the relationship between the stored VPC and
	// its backing Docker network. Populated by the active vpcNetworkStrategy.
	// Empty is treated as vpcNetworkOK for backwards compatibility with VPCs
	// written before this field existed.
	NetworkStatus string `json:"NetworkStatus,omitempty"`

	// DockerCidrBlock is the subnet actually used for the backing Docker
	// network. It differs from CidrBlock only when the active strategy
	// relocates the pool (e.g. the future `remapped` strategy). Empty means
	// the Docker network uses CidrBlock directly.
	DockerCidrBlock string `json:"DockerCidrBlock,omitempty"`

	// CreateTime is the UnixMillis timestamp when this VPC was created.
	// Used by reconcile to deterministically pick the winner when multiple
	// VPCs claim the same CIDR — earliest creation wins.
	CreateTime int64 `json:"CreateTime,omitempty"`

	// EnableDnsSupport controls whether the Amazon-provided DNS server
	// resolves names for instances in the VPC. Real AWS defaults this to
	// true for every VPC, default or not.
	EnableDnsSupport bool `json:"EnableDnsSupport"`

	// EnableDnsHostnames controls whether instances with a public IP are
	// also given a public DNS hostname. Real AWS defaults this to false for
	// VPCs created via CreateVpc, and true for the account's default VPC.
	EnableDnsHostnames bool `json:"EnableDnsHostnames"`

	// EgressNetworkID, EgressNetworkName and EgressCidrBlock describe the
	// VPC's egress network under OVERCAST_VPC_EGRESS=routed: the routable
	// bridge (config.VPCEgressNetwork) that carries the default route for the
	// containers whose subnet's route table grants one. Created on the first
	// placement that needs it, so all three are empty until then and in every
	// other mode. The CIDR is the /24 carved from config.VPCEgressPool, kept
	// so a restart recreates the same network rather than a neighbour of it.
	EgressNetworkID   string `json:"EgressNetworkId,omitempty"`
	EgressNetworkName string `json:"EgressNetworkName,omitempty"`
	EgressCidrBlock   string `json:"EgressCidrBlock,omitempty"`
}

// vpcPlacement records which subnets a container Overcast placed in a VPC
// asked for, so the egress decision those subnets' route tables produced can
// be revisited when a route table changes (see reconcileVPCEgress). Written
// by RecordPlacement under OVERCAST_VPC_EGRESS=routed; pruned when the
// container is found gone.
//
// Keyed by container ID and not by region: a container is one thing on one
// daemon, whichever region's VPC it sits in, and the VPC ID it carries is
// what scopes it.
type vpcPlacement struct {
	ContainerID string   `json:"ContainerId"`
	VpcID       string   `json:"VpcId"`
	SubnetIDs   []string `json:"SubnetIds,omitempty"`
	RecordedAt  int64    `json:"RecordedAt,omitempty"`
}

func (s *ec2Store) putPlacement(ctx context.Context, p *vpcPlacement) *protocol.AWSError {
	raw, err := json.Marshal(p)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsPlacements, p.ContainerID, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *ec2Store) deletePlacement(ctx context.Context, containerID string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsPlacements, containerID); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// listPlacements returns every recorded placement, or only those in vpcID
// when it is given, ordered by container ID.
func (s *ec2Store) listPlacements(ctx context.Context, vpcID string) ([]*vpcPlacement, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsPlacements, "")
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	out := make([]*vpcPlacement, 0, len(pairs))
	for _, p := range pairs {
		var rec vpcPlacement
		if err := json.Unmarshal([]byte(p.Value), &rec); err != nil {
			continue
		}
		if vpcID != "" && rec.VpcID != vpcID {
			continue
		}
		out = append(out, &rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ContainerID < out[j].ContainerID })
	return out, nil
}

// Subnet represents an EC2 Subnet resource.
type Subnet struct {
	SubnetID         string `json:"SubnetId"`
	VpcID            string `json:"VpcId"`
	CidrBlock        string `json:"CidrBlock"`
	AvailabilityZone string `json:"AvailabilityZone"`
	State            string `json:"State"`

	// MapPublicIpOnLaunch controls whether instances launched into this
	// subnet are automatically assigned a public IP. Real AWS defaults this
	// to false for subnets created via CreateSubnet.
	MapPublicIpOnLaunch bool `json:"MapPublicIpOnLaunch"`
}

// IpRange represents a CIDR range in a security group rule.
type IpRange struct {
	CidrIp      string `json:"CidrIp"`
	Description string `json:"Description,omitempty"`
}

// IpPermission represents a security group rule (inbound or outbound).
type IpPermission struct {
	IpProtocol string    `json:"IpProtocol"`
	FromPort   int       `json:"FromPort"`
	ToPort     int       `json:"ToPort"`
	IpRanges   []IpRange `json:"IpRanges,omitempty"`
}

// SecurityGroup represents an EC2 Security Group resource.
type SecurityGroup struct {
	GroupID             string         `json:"GroupId"`
	GroupName           string         `json:"GroupName"`
	Description         string         `json:"Description"`
	VpcID               string         `json:"VpcId"`
	IpPermissions       []IpPermission `json:"IpPermissions,omitempty"`
	IpPermissionsEgress []IpPermission `json:"IpPermissionsEgress,omitempty"`
}

type ec2Store struct {
	store         state.Store
	defaultRegion string
	accountID     string

	// eventBridge is the EventBridge bus publisher wired by
	// Service.InitEventBridge. nil in most tests, and delivery is skipped
	// then rather than attempted against nothing — see notifyInstanceStateChange
	// in eventbridge.go.
	eventBridge events.BusPublisher
}

func newEC2Store(store state.Store, defaultRegion, accountID string) *ec2Store {
	return &ec2Store{store: store, defaultRegion: defaultRegion, accountID: accountID}
}

// region extracts the per-request region from context, falling back to the default.
func (s *ec2Store) region(ctx context.Context) string {
	return middleware.RegionFromContext(ctx, s.defaultRegion)
}

// notReady reports whether the backing store is still completing the one-time
// startup migration, during which a list reads none of the records that exist.
// A store with no such phase implements neither reporter, and its absence
// means ready — the convention state.NotReadyReporter documents.
func (s *ec2Store) notReady() bool {
	r, ok := s.store.(state.NotReadyReporter)
	return ok && r.NotReady()
}

// ── VPCs ──────────────────────────────────────────────────────────────────────

func (s *ec2Store) putVPC(ctx context.Context, v *VPC) *protocol.AWSError {
	raw, err := json.Marshal(v)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsVPCs, serviceutil.RegionKey(s.region(ctx), v.VpcID), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *ec2Store) getVPC(ctx context.Context, id string) (*VPC, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsVPCs, serviceutil.RegionKey(s.region(ctx), id))
	if err != nil || !ok {
		return nil, errNotFound("vpc", id)
	}
	var v VPC
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &v, nil
}

func (s *ec2Store) listVPCs(ctx context.Context) ([]*VPC, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsVPCs, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	vpcs := make([]*VPC, 0, len(pairs))
	for _, p := range pairs {
		var v VPC
		if err := json.Unmarshal([]byte(p.Value), &v); err != nil {
			continue
		}
		vpcs = append(vpcs, &v)
	}
	return vpcs, nil
}

// vpcsByRegion returns every VPC in the store, grouped by the region it is
// stored under. The startup reconcile uses it instead of listVPCs: with no
// request region the context resolves to the default region, and a list made
// through it silently covers that region alone.
func (s *ec2Store) vpcsByRegion(ctx context.Context) (map[string][]*VPC, *protocol.AWSError) {
	regioned, err := serviceutil.ScanRegions[VPC](ctx, s.store, nsVPCs, s.defaultRegion)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	byRegion := make(map[string][]*VPC)
	for _, r := range regioned {
		byRegion[r.Region] = append(byRegion[r.Region], r.Value)
	}
	return byRegion, nil
}

func (s *ec2Store) deleteVPC(ctx context.Context, id string) *protocol.AWSError {
	if _, aerr := s.getVPC(ctx, id); aerr != nil {
		return aerr
	}
	if err := s.store.Delete(ctx, nsVPCs, serviceutil.RegionKey(s.region(ctx), id)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return s.deleteTags(ctx, id)
}

// ── Subnets ───────────────────────────────────────────────────────────────────

func (s *ec2Store) putSubnet(ctx context.Context, sub *Subnet) *protocol.AWSError {
	raw, err := json.Marshal(sub)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsSubnets, serviceutil.RegionKey(s.region(ctx), sub.SubnetID), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *ec2Store) getSubnet(ctx context.Context, id string) (*Subnet, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsSubnets, serviceutil.RegionKey(s.region(ctx), id))
	if err != nil || !ok {
		return nil, errNotFound("subnet", id)
	}
	var sub Subnet
	if err := json.Unmarshal([]byte(raw), &sub); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &sub, nil
}

func (s *ec2Store) deleteSubnet(ctx context.Context, id string) *protocol.AWSError {
	if _, aerr := s.getSubnet(ctx, id); aerr != nil {
		return aerr
	}
	if err := s.store.Delete(ctx, nsSubnets, serviceutil.RegionKey(s.region(ctx), id)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return s.deleteTags(ctx, id)
}

func (s *ec2Store) listSubnets(ctx context.Context) ([]*Subnet, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsSubnets, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	subs := make([]*Subnet, 0, len(pairs))
	for _, p := range pairs {
		var sub Subnet
		if err := json.Unmarshal([]byte(p.Value), &sub); err != nil {
			continue
		}
		subs = append(subs, &sub)
	}
	return subs, nil
}

// ── Security Groups ───────────────────────────────────────────────────────────

func (s *ec2Store) putSecurityGroup(ctx context.Context, sg *SecurityGroup) *protocol.AWSError {
	raw, err := json.Marshal(sg)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsSecurityGroups, serviceutil.RegionKey(s.region(ctx), sg.GroupID), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *ec2Store) getSecurityGroup(ctx context.Context, id string) (*SecurityGroup, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsSecurityGroups, serviceutil.RegionKey(s.region(ctx), id))
	if err != nil || !ok {
		return nil, errNotFound("security group", id)
	}
	var sg SecurityGroup
	if err := json.Unmarshal([]byte(raw), &sg); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &sg, nil
}

func (s *ec2Store) deleteSecurityGroup(ctx context.Context, id string) *protocol.AWSError {
	if _, aerr := s.getSecurityGroup(ctx, id); aerr != nil {
		return aerr
	}
	if err := s.store.Delete(ctx, nsSecurityGroups, serviceutil.RegionKey(s.region(ctx), id)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return s.deleteTags(ctx, id)
}

func (s *ec2Store) listSecurityGroups(ctx context.Context) ([]*SecurityGroup, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsSecurityGroups, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	sgs := make([]*SecurityGroup, 0, len(pairs))
	for _, p := range pairs {
		var sg SecurityGroup
		if err := json.Unmarshal([]byte(p.Value), &sg); err != nil {
			continue
		}
		sgs = append(sgs, &sg)
	}
	return sgs, nil
}

// ── Instances ─────────────────────────────────────────────────────────────────

// Instance represents an EC2 instance resource.
type Instance struct {
	InstanceID       string        `json:"InstanceId"`
	ImageID          string        `json:"ImageId"`
	InstanceType     string        `json:"InstanceType"`
	State            InstanceState `json:"State"`
	LaunchTime       string        `json:"LaunchTime"`
	SubnetID         string        `json:"SubnetId,omitempty"`
	VpcID            string        `json:"VpcId,omitempty"`
	PrivateIPAddress string        `json:"PrivateIpAddress,omitempty"`
	SecurityGroups   []InstanceSG  `json:"SecurityGroups,omitempty"`
	Placement        Placement     `json:"Placement"`
}

// VPCIPTranslation persists fakeIP↔realIP mapping for remapped VPCs.
// FakeIP is what EC2 APIs return; RealIP is what Docker-backed workloads use.
type VPCIPTranslation struct {
	VpcID           string `json:"VpcId"`
	DockerNetworkID string `json:"DockerNetworkId,omitempty"`
	FakeIP          string `json:"FakeIp"`
	RealIP          string `json:"RealIp"`
}

// InstanceState holds the state code and name for an EC2 instance.
type InstanceState struct {
	Code int    `json:"Code"`
	Name string `json:"Name"`
}

// InstanceSG is a security group reference attached to an instance.
type InstanceSG struct {
	GroupID   string `json:"GroupId"`
	GroupName string `json:"GroupName"`
}

// Placement describes the placement of an instance.
type Placement struct {
	AvailabilityZone string `json:"AvailabilityZone"`
}

// Tag is a key-value pair attached to an EC2 resource.
type Tag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

func (s *ec2Store) putInstance(ctx context.Context, inst *Instance) *protocol.AWSError {
	// Read the prior record before overwriting it so the write below can tell
	// whether the instance's state actually changed — see
	// notifyInstanceStateChange in eventbridge.go. A lookup failure (including
	// "not found", the very first write for this instance) just means prev is
	// nil; it is not fatal to the put itself.
	prev, _ := s.getInstance(ctx, inst.InstanceID)

	raw, err := json.Marshal(inst)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsInstances, serviceutil.RegionKey(s.region(ctx), inst.InstanceID), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	s.notifyInstanceStateChange(ctx, prev, inst)
	return nil
}

func (s *ec2Store) getInstance(ctx context.Context, id string) (*Instance, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsInstances, serviceutil.RegionKey(s.region(ctx), id))
	if err != nil || !ok {
		return nil, errNotFound("instance", id)
	}
	var inst Instance
	if err := json.Unmarshal([]byte(raw), &inst); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &inst, nil
}

func (s *ec2Store) listInstances(ctx context.Context) ([]*Instance, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsInstances, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	instances := make([]*Instance, 0, len(pairs))
	for _, p := range pairs {
		var inst Instance
		if err := json.Unmarshal([]byte(p.Value), &inst); err != nil {
			continue
		}
		instances = append(instances, &inst)
	}
	return instances, nil
}

func (s *ec2Store) putVPCIPTranslation(ctx context.Context, tr *VPCIPTranslation) *protocol.AWSError {
	raw, err := json.Marshal(tr)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	region := s.region(ctx)
	if err := s.store.Set(ctx, nsVPCIPTranslations, serviceutil.RegionKey(region, tr.VpcID+"|"+tr.FakeIP), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsVPCIPTranslationsReal, serviceutil.RegionKey(region, tr.VpcID+"|"+tr.RealIP), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *ec2Store) getVPCIPTranslationByFake(ctx context.Context, vpcID, fakeIP string) (*VPCIPTranslation, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsVPCIPTranslations, serviceutil.RegionKey(s.region(ctx), vpcID+"|"+fakeIP))
	if err != nil || !ok {
		return nil, nil
	}
	var tr VPCIPTranslation
	if err := json.Unmarshal([]byte(raw), &tr); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &tr, nil
}

func (s *ec2Store) getVPCIPTranslationByReal(ctx context.Context, vpcID, realIP string) (*VPCIPTranslation, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsVPCIPTranslationsReal, serviceutil.RegionKey(s.region(ctx), vpcID+"|"+realIP))
	if err != nil || !ok {
		return nil, nil
	}
	var tr VPCIPTranslation
	if err := json.Unmarshal([]byte(raw), &tr); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &tr, nil
}

// ── Errors ────────────────────────────────────────────────────────────────────

func errNotFound(resource, id string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "InvalidId.NotFound",
		Message:    fmt.Sprintf("The %s ID '%s' does not exist", resource, id),
		HTTPStatus: http.StatusBadRequest,
	}
}

// ── Key Pairs ─────────────────────────────────────────────────────────────────

// KeyPair represents an EC2 key pair resource.
type KeyPair struct {
	KeyName        string `json:"KeyName"`
	KeyFingerprint string `json:"KeyFingerprint"`
	KeyPairID      string `json:"KeyPairId"`
	KeyMaterial    string `json:"KeyMaterial,omitempty"`
}

func (s *ec2Store) putKeyPair(ctx context.Context, kp *KeyPair) *protocol.AWSError {
	raw, err := json.Marshal(kp)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsKeyPairs, serviceutil.RegionKey(s.region(ctx), kp.KeyName), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *ec2Store) getKeyPair(ctx context.Context, name string) (*KeyPair, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsKeyPairs, serviceutil.RegionKey(s.region(ctx), name))
	if err != nil || !ok {
		return nil, &protocol.AWSError{
			Code:       "InvalidKeyPair.NotFound",
			Message:    fmt.Sprintf("The key pair '%s' does not exist", name),
			HTTPStatus: http.StatusBadRequest,
		}
	}
	var kp KeyPair
	if err := json.Unmarshal([]byte(raw), &kp); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &kp, nil
}

func (s *ec2Store) listKeyPairs(ctx context.Context) ([]*KeyPair, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsKeyPairs, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	kps := make([]*KeyPair, 0, len(pairs))
	for _, p := range pairs {
		var kp KeyPair
		if err := json.Unmarshal([]byte(p.Value), &kp); err != nil {
			continue
		}
		kps = append(kps, &kp)
	}
	return kps, nil
}

func (s *ec2Store) deleteKeyPair(ctx context.Context, name string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsKeyPairs, serviceutil.RegionKey(s.region(ctx), name)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return s.deleteTags(ctx, name)
}

// ── Route Tables ──────────────────────────────────────────────────────────────

// Route represents a route in a route table.
type Route struct {
	DestinationCidrBlock string `json:"DestinationCidrBlock"`
	GatewayID            string `json:"GatewayId,omitempty"`
	NatGatewayID         string `json:"NatGatewayId,omitempty"`
	Origin               string `json:"Origin"` // CreateRouteTable | CreateRoute
}

// RouteTableAssociation represents an association between a route table and a subnet.
type RouteTableAssociation struct {
	AssociationID string `json:"RouteTableAssociationId"`
	RouteTableID  string `json:"RouteTableId"`
	SubnetID      string `json:"SubnetId,omitempty"`
	Main          bool   `json:"Main"`
}

// RouteTable represents an EC2 route table resource.
type RouteTable struct {
	RouteTableID string                  `json:"RouteTableId"`
	VpcID        string                  `json:"VpcId"`
	Routes       []Route                 `json:"Routes"`
	Associations []RouteTableAssociation `json:"Associations"`
}

func (s *ec2Store) putRouteTable(ctx context.Context, rt *RouteTable) *protocol.AWSError {
	raw, err := json.Marshal(rt)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsRouteTables, serviceutil.RegionKey(s.region(ctx), rt.RouteTableID), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *ec2Store) getRouteTable(ctx context.Context, id string) (*RouteTable, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsRouteTables, serviceutil.RegionKey(s.region(ctx), id))
	if err != nil || !ok {
		return nil, &protocol.AWSError{
			Code:       "InvalidRouteTableID.NotFound",
			Message:    fmt.Sprintf("The routeTable ID '%s' does not exist", id),
			HTTPStatus: http.StatusBadRequest,
		}
	}
	var rt RouteTable
	if err := json.Unmarshal([]byte(raw), &rt); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &rt, nil
}

func (s *ec2Store) listRouteTables(ctx context.Context) ([]*RouteTable, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsRouteTables, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	rts := make([]*RouteTable, 0, len(pairs))
	for _, p := range pairs {
		var rt RouteTable
		if err := json.Unmarshal([]byte(p.Value), &rt); err != nil {
			continue
		}
		rts = append(rts, &rt)
	}
	return rts, nil
}

func (s *ec2Store) deleteRouteTable(ctx context.Context, id string) *protocol.AWSError {
	if _, aerr := s.getRouteTable(ctx, id); aerr != nil {
		return aerr
	}
	if err := s.store.Delete(ctx, nsRouteTables, serviceutil.RegionKey(s.region(ctx), id)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return s.deleteTags(ctx, id)
}

// ── Internet Gateways ─────────────────────────────────────────────────────────

// InternetGateway represents an EC2 internet gateway resource.
type InternetGateway struct {
	InternetGatewayID string          `json:"InternetGatewayId"`
	Attachments       []IGWAttachment `json:"Attachments,omitempty"`
}

// IGWAttachment represents an internet gateway VPC attachment.
type IGWAttachment struct {
	VpcID string `json:"VpcId"`
	State string `json:"State"`
}

func (s *ec2Store) putInternetGateway(ctx context.Context, igw *InternetGateway) *protocol.AWSError {
	raw, err := json.Marshal(igw)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsInternetGateways, serviceutil.RegionKey(s.region(ctx), igw.InternetGatewayID), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *ec2Store) getInternetGateway(ctx context.Context, id string) (*InternetGateway, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsInternetGateways, serviceutil.RegionKey(s.region(ctx), id))
	if err != nil || !ok {
		return nil, &protocol.AWSError{
			Code:       "InvalidInternetGatewayID.NotFound",
			Message:    fmt.Sprintf("The internetGateway ID '%s' does not exist", id),
			HTTPStatus: http.StatusBadRequest,
		}
	}
	var igw InternetGateway
	if err := json.Unmarshal([]byte(raw), &igw); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &igw, nil
}

func (s *ec2Store) listInternetGateways(ctx context.Context) ([]*InternetGateway, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsInternetGateways, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	igws := make([]*InternetGateway, 0, len(pairs))
	for _, p := range pairs {
		var igw InternetGateway
		if err := json.Unmarshal([]byte(p.Value), &igw); err != nil {
			continue
		}
		igws = append(igws, &igw)
	}
	return igws, nil
}

func (s *ec2Store) deleteInternetGateway(ctx context.Context, id string) *protocol.AWSError {
	if _, aerr := s.getInternetGateway(ctx, id); aerr != nil {
		return aerr
	}
	if err := s.store.Delete(ctx, nsInternetGateways, serviceutil.RegionKey(s.region(ctx), id)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return s.deleteTags(ctx, id)
}

// ── VPN Gateways ─────────────────────────────────────────────────────────────

// VpnGateway represents an EC2 virtual private gateway resource.
type VpnGateway struct {
	VpnGatewayID     string                 `json:"VpnGatewayId"`
	State            string                 `json:"State"`
	Type             string                 `json:"Type"`
	AmazonSideAsn    int64                  `json:"AmazonSideAsn"`
	AvailabilityZone string                 `json:"AvailabilityZone,omitempty"`
	Attachments      []VpnGatewayAttachment `json:"Attachments,omitempty"`
}

// VpnGatewayAttachment represents a virtual private gateway VPC attachment.
type VpnGatewayAttachment struct {
	VpcID string `json:"VpcId"`
	State string `json:"State"`
}

func (s *ec2Store) putVpnGateway(ctx context.Context, vgw *VpnGateway) *protocol.AWSError {
	raw, err := json.Marshal(vgw)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsVpnGateways, serviceutil.RegionKey(s.region(ctx), vgw.VpnGatewayID), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *ec2Store) getVpnGateway(ctx context.Context, id string) (*VpnGateway, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsVpnGateways, serviceutil.RegionKey(s.region(ctx), id))
	if err != nil || !ok {
		return nil, &protocol.AWSError{
			Code:       "InvalidVpnGatewayID.NotFound",
			Message:    fmt.Sprintf("The vpnGateway ID '%s' does not exist", id),
			HTTPStatus: http.StatusBadRequest,
		}
	}
	var vgw VpnGateway
	if err := json.Unmarshal([]byte(raw), &vgw); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &vgw, nil
}

func (s *ec2Store) listVpnGateways(ctx context.Context) ([]*VpnGateway, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsVpnGateways, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	vgws := make([]*VpnGateway, 0, len(pairs))
	for _, p := range pairs {
		var vgw VpnGateway
		if err := json.Unmarshal([]byte(p.Value), &vgw); err != nil {
			continue
		}
		vgws = append(vgws, &vgw)
	}
	return vgws, nil
}

func (s *ec2Store) deleteVpnGateway(ctx context.Context, id string) *protocol.AWSError {
	if _, aerr := s.getVpnGateway(ctx, id); aerr != nil {
		return aerr
	}
	if err := s.store.Delete(ctx, nsVpnGateways, serviceutil.RegionKey(s.region(ctx), id)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return s.deleteTags(ctx, id)
}

// ── VPC Peering Connections ───────────────────────────────────────────────────

// VpcPeeringConnectionVpcInfo describes one side (requester or accepter) of a peering connection.
type VpcPeeringConnectionVpcInfo struct {
	VpcID     string `json:"VpcId"`
	OwnerID   string `json:"OwnerId"`
	CidrBlock string `json:"CidrBlock"`
	Region    string `json:"Region"`
}

// VpcPeeringConnectionStatus is the current state of a peering connection.
type VpcPeeringConnectionStatus struct {
	Code    string `json:"Code"`
	Message string `json:"Message"`
}

// VpcPeeringConnection represents an EC2 VPC peering connection resource.
type VpcPeeringConnection struct {
	VpcPeeringConnectionID string                      `json:"VpcPeeringConnectionId"`
	RequesterVpcInfo       VpcPeeringConnectionVpcInfo `json:"RequesterVpcInfo"`
	AccepterVpcInfo        VpcPeeringConnectionVpcInfo `json:"AccepterVpcInfo"`
	Status                 VpcPeeringConnectionStatus  `json:"Status"`
}

func (s *ec2Store) putVpcPeeringConnection(ctx context.Context, pcx *VpcPeeringConnection) *protocol.AWSError {
	raw, err := json.Marshal(pcx)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsVpcPeeringConnections, serviceutil.RegionKey(s.region(ctx), pcx.VpcPeeringConnectionID), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *ec2Store) getVpcPeeringConnection(ctx context.Context, id string) (*VpcPeeringConnection, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsVpcPeeringConnections, serviceutil.RegionKey(s.region(ctx), id))
	if err != nil || !ok {
		return nil, &protocol.AWSError{
			Code:       "InvalidVpcPeeringConnectionID.NotFound",
			Message:    fmt.Sprintf("The vpcPeeringConnection ID '%s' does not exist", id),
			HTTPStatus: http.StatusBadRequest,
		}
	}
	var pcx VpcPeeringConnection
	if err := json.Unmarshal([]byte(raw), &pcx); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &pcx, nil
}

func (s *ec2Store) listVpcPeeringConnections(ctx context.Context) ([]*VpcPeeringConnection, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsVpcPeeringConnections, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	pcxs := make([]*VpcPeeringConnection, 0, len(pairs))
	for _, p := range pairs {
		var pcx VpcPeeringConnection
		if err := json.Unmarshal([]byte(p.Value), &pcx); err != nil {
			continue
		}
		pcxs = append(pcxs, &pcx)
	}
	return pcxs, nil
}

// ── Tags ──────────────────────────────────────────────────────────────────────

func (s *ec2Store) putTags(ctx context.Context, resourceID string, tags map[string]string) *protocol.AWSError {
	raw, err := json.Marshal(tags)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsTags, serviceutil.RegionKey(s.region(ctx), resourceID), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// deleteTags removes a resource's tags, and is called by every delete above.
//
// Tags are keyed by resource ID in a namespace of their own, so nothing ties
// them to the record's lifetime. Left behind they outlive the resource:
// DescribeTags goes on reporting them, the store grows for the life of the
// session, and anything created with the same ID inherits them.
func (s *ec2Store) deleteTags(ctx context.Context, resourceID string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsTags, serviceutil.RegionKey(s.region(ctx), resourceID)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *ec2Store) getTags(ctx context.Context, resourceID string) (map[string]string, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsTags, serviceutil.RegionKey(s.region(ctx), resourceID))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !ok {
		return nil, nil
	}
	var tags map[string]string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return tags, nil
}

func (s *ec2Store) listAllTags(ctx context.Context) (map[string]map[string]string, *protocol.AWSError) {
	prefix := serviceutil.RegionKey(s.region(ctx), "")
	pairs, err := s.store.Scan(ctx, nsTags, prefix)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	result := make(map[string]map[string]string, len(pairs))
	for _, p := range pairs {
		rid := p.Key[len(prefix):]
		var tags map[string]string
		if err := json.Unmarshal([]byte(p.Value), &tags); err != nil {
			continue
		}
		result[rid] = tags
	}
	return result, nil
}

// ── Elastic IPs ───────────────────────────────────────────────────────────────

// ElasticIP represents an EC2 Elastic IP address.
type ElasticIP struct {
	AllocationID       string `json:"AllocationId"`
	PublicIP           string `json:"PublicIp"`
	Domain             string `json:"Domain"`
	AssociationID      string `json:"AssociationId,omitempty"`
	InstanceID         string `json:"InstanceId,omitempty"`
	NetworkInterfaceID string `json:"NetworkInterfaceId,omitempty"`
	PrivateIP          string `json:"PrivateIpAddress,omitempty"`
}

func (s *ec2Store) putElasticIP(ctx context.Context, eip *ElasticIP) *protocol.AWSError {
	raw, err := json.Marshal(eip)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsElasticIPs, serviceutil.RegionKey(s.region(ctx), eip.AllocationID), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *ec2Store) getElasticIP(ctx context.Context, allocID string) (*ElasticIP, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsElasticIPs, serviceutil.RegionKey(s.region(ctx), allocID))
	if err != nil || !ok {
		// InvalidAllocationID.NotFound, not InvalidAddressID.NotFound.
		//
		// The reference's description column reads as though the two split by
		// operation — InvalidAddressID.NotFound is worded "the allocation ID
		// for the Elastic IP address you are trying to release", and
		// InvalidAllocationID.NotFound "the allocation ID you are trying to
		// describe or associate". Real EC2 does not split them: it answers
		// InvalidAllocationID.NotFound to a release, an associate and a
		// describe alike, which is what the probe sweep behind #1708 measured
		// and what every reported capture shows —
		// hashicorp/terraform#1815, on an associate: "InvalidAllocationID.NotFound:
		// The allocation ID 'eipalloc-9b0b7cfe' does not exist".
		//
		// Every caller of this getter names an allocation ID (ReleaseAddress,
		// AssociateAddress, CreateNatGateway), so the code belongs here rather
		// than at one call site.
		return nil, &protocol.AWSError{
			Code:       "InvalidAllocationID.NotFound",
			Message:    fmt.Sprintf("The allocation ID '%s' does not exist", allocID),
			HTTPStatus: http.StatusBadRequest,
		}
	}
	var eip ElasticIP
	if err := json.Unmarshal([]byte(raw), &eip); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &eip, nil
}

func (s *ec2Store) listElasticIPs(ctx context.Context) ([]*ElasticIP, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsElasticIPs, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	eips := make([]*ElasticIP, 0, len(pairs))
	for _, p := range pairs {
		var eip ElasticIP
		if err := json.Unmarshal([]byte(p.Value), &eip); err != nil {
			continue
		}
		eips = append(eips, &eip)
	}
	return eips, nil
}

func (s *ec2Store) deleteElasticIP(ctx context.Context, allocID string) *protocol.AWSError {
	if _, aerr := s.getElasticIP(ctx, allocID); aerr != nil {
		return aerr
	}
	if err := s.store.Delete(ctx, nsElasticIPs, serviceutil.RegionKey(s.region(ctx), allocID)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return s.deleteTags(ctx, allocID)
}

// ── NAT Gateways ──────────────────────────────────────────────────────────────

// NatGateway represents an EC2 NAT gateway resource.
type NatGateway struct {
	NatGatewayID string `json:"NatGatewayId"`
	SubnetID     string `json:"SubnetId"`
	VpcID        string `json:"VpcId"`
	State        string `json:"State"`
	AllocationID string `json:"AllocationId,omitempty"`
	PublicIP     string `json:"PublicIp,omitempty"`
	PrivateIP    string `json:"PrivateIp,omitempty"`
	CreateTime   string `json:"CreateTime"`
}

func (s *ec2Store) putNatGateway(ctx context.Context, ngw *NatGateway) *protocol.AWSError {
	raw, err := json.Marshal(ngw)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsNatGateways, serviceutil.RegionKey(s.region(ctx), ngw.NatGatewayID), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *ec2Store) getNatGateway(ctx context.Context, id string) (*NatGateway, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsNatGateways, serviceutil.RegionKey(s.region(ctx), id))
	if err != nil || !ok {
		return nil, &protocol.AWSError{
			Code:       "NatGatewayNotFound",
			Message:    fmt.Sprintf("The natGateway ID '%s' does not exist", id),
			HTTPStatus: http.StatusBadRequest,
		}
	}
	var ngw NatGateway
	if err := json.Unmarshal([]byte(raw), &ngw); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &ngw, nil
}

func (s *ec2Store) listNatGateways(ctx context.Context) ([]*NatGateway, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsNatGateways, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	ngws := make([]*NatGateway, 0, len(pairs))
	for _, p := range pairs {
		var ngw NatGateway
		if err := json.Unmarshal([]byte(p.Value), &ngw); err != nil {
			continue
		}
		ngws = append(ngws, &ngw)
	}
	return ngws, nil
}

func (s *ec2Store) deleteNatGateway(ctx context.Context, id string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsNatGateways, serviceutil.RegionKey(s.region(ctx), id)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return s.deleteTags(ctx, id)
}

// ── Network Interfaces ────────────────────────────────────────────────────────

// NetworkInterface represents an EC2 network interface resource.
type NetworkInterface struct {
	NetworkInterfaceID string `json:"NetworkInterfaceId"`
	SubnetID           string `json:"SubnetId"`
	VpcID              string `json:"VpcId"`
	AvailabilityZone   string `json:"AvailabilityZone"`
	Description        string `json:"Description"`
	PrivateIPAddress   string `json:"PrivateIpAddress"`
	Status             string `json:"Status"`
	MacAddress         string `json:"MacAddress"`
}

func (s *ec2Store) putNetworkInterface(ctx context.Context, eni *NetworkInterface) *protocol.AWSError {
	raw, err := json.Marshal(eni)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsNetworkInterfaces, serviceutil.RegionKey(s.region(ctx), eni.NetworkInterfaceID), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *ec2Store) listNetworkInterfaces(ctx context.Context) ([]*NetworkInterface, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsNetworkInterfaces, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	enis := make([]*NetworkInterface, 0, len(pairs))
	for _, p := range pairs {
		var eni NetworkInterface
		if err := json.Unmarshal([]byte(p.Value), &eni); err != nil {
			continue
		}
		enis = append(enis, &eni)
	}
	return enis, nil
}

func (s *ec2Store) deleteNetworkInterface(ctx context.Context, id string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsNetworkInterfaces, serviceutil.RegionKey(s.region(ctx), id)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return s.deleteTags(ctx, id)
}

// ── VPC Endpoints ─────────────────────────────────────────────────────────────

// VpcEndpoint represents an EC2 VPC endpoint resource.
type VpcEndpoint struct {
	VpcEndpointID   string `json:"VpcEndpointId"`
	VpcID           string `json:"VpcId"`
	ServiceName     string `json:"ServiceName"`
	State           string `json:"State"`
	VpcEndpointType string `json:"VpcEndpointType"`
}

func (s *ec2Store) putVpcEndpoint(ctx context.Context, ep *VpcEndpoint) *protocol.AWSError {
	raw, err := json.Marshal(ep)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsVpcEndpoints, serviceutil.RegionKey(s.region(ctx), ep.VpcEndpointID), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *ec2Store) listVpcEndpoints(ctx context.Context) ([]*VpcEndpoint, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsVpcEndpoints, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	eps := make([]*VpcEndpoint, 0, len(pairs))
	for _, p := range pairs {
		var ep VpcEndpoint
		if err := json.Unmarshal([]byte(p.Value), &ep); err != nil {
			continue
		}
		eps = append(eps, &ep)
	}
	return eps, nil
}

func (s *ec2Store) deleteVpcEndpoint(ctx context.Context, id string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsVpcEndpoints, serviceutil.RegionKey(s.region(ctx), id)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return s.deleteTags(ctx, id)
}

// ── Launch Templates ─────────────────────────────────────────────────────────

// LaunchTemplate is the metadata record for an EC2 launch template. The launch
// parameters themselves live in the versions (LaunchTemplateVersion); this
// record only tracks which of them is the default and which is the latest —
// the two numbers `$Default` and `$Latest` resolve to.
type LaunchTemplate struct {
	LaunchTemplateID     string `json:"LaunchTemplateId"`
	LaunchTemplateName   string `json:"LaunchTemplateName"`
	CreateTime           string `json:"CreateTime"`
	CreatedBy            string `json:"CreatedBy"`
	DefaultVersionNumber int64  `json:"DefaultVersionNumber"`
	LatestVersionNumber  int64  `json:"LatestVersionNumber"`
}

// LaunchTemplateVersion is one immutable version of a launch template.
//
// AWS reports defaultVersion on every version it returns, but that is derived
// from the owning template's DefaultVersionNumber rather than stored here:
// recording it on the version too would mean rewriting every version each time
// ModifyLaunchTemplate moves the default.
type LaunchTemplateVersion struct {
	LaunchTemplateID   string             `json:"LaunchTemplateId"`
	LaunchTemplateName string             `json:"LaunchTemplateName"`
	VersionNumber      int64              `json:"VersionNumber"`
	VersionDescription string             `json:"VersionDescription,omitempty"`
	CreateTime         string             `json:"CreateTime"`
	CreatedBy          string             `json:"CreatedBy"`
	Data               LaunchTemplateData `json:"LaunchTemplateData"`
}

// LaunchTemplateData is the subset of AWS's RequestLaunchTemplateData that
// Overcast stores and merges into RunInstances. AWS's shape carries around
// thirty members; the ones here are those an emulated instance record can act
// on, plus the identity fields a caller reads back.
type LaunchTemplateData struct {
	ImageID            string                           `json:"ImageId,omitempty"`
	InstanceType       string                           `json:"InstanceType,omitempty"`
	KeyName            string                           `json:"KeyName,omitempty"`
	UserData           string                           `json:"UserData,omitempty"`
	SecurityGroupIDs   []string                         `json:"SecurityGroupIds,omitempty"`
	SecurityGroups     []string                         `json:"SecurityGroups,omitempty"`
	IamInstanceProfile *LaunchTemplateIamProfile        `json:"IamInstanceProfile,omitempty"`
	NetworkInterfaces  []LaunchTemplateNetworkInterface `json:"NetworkInterfaces,omitempty"`
	TagSpecifications  []LaunchTemplateTagSpecification `json:"TagSpecifications,omitempty"`
}

// LaunchTemplateIamProfile names the instance profile launched instances get.
type LaunchTemplateIamProfile struct {
	ARN  string `json:"Arn,omitempty"`
	Name string `json:"Name,omitempty"`
}

// LaunchTemplateNetworkInterface is one entry of a template's
// NetworkInterface.N. Only the members an emulated instance record can act on
// are kept: which subnet it lands in and which security groups it joins.
type LaunchTemplateNetworkInterface struct {
	DeviceIndex              *int     `json:"DeviceIndex,omitempty"`
	SubnetID                 string   `json:"SubnetId,omitempty"`
	Groups                   []string `json:"Groups,omitempty"`
	AssociatePublicIPAddress *bool    `json:"AssociatePublicIpAddress,omitempty"`
}

// LaunchTemplateTagSpecification is one entry of a template's
// LaunchTemplateData.TagSpecification.N — the tags applied to the resources an
// instance launch creates, not to the template itself.
type LaunchTemplateTagSpecification struct {
	ResourceType string `json:"ResourceType"`
	Tags         []Tag  `json:"Tags,omitempty"`
}

// launchTemplateVersionKey builds a version's store key. The number is
// zero-padded so a prefix scan returns versions in numeric order rather than
// lexical order, which is what would otherwise put version 10 before version 9.
func launchTemplateVersionKey(templateID string, version int64) string {
	return fmt.Sprintf("%s/%010d", templateID, version)
}

func (s *ec2Store) putLaunchTemplate(ctx context.Context, lt *LaunchTemplate) *protocol.AWSError {
	raw, err := json.Marshal(lt)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	key := serviceutil.RegionKey(s.region(ctx), lt.LaunchTemplateID)
	if err := s.store.Set(ctx, nsLaunchTemplates, key, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// getLaunchTemplate returns one template by ID, or nil when it does not exist.
// A record that will not decode is treated as absent rather than failing the
// call, per the malformed-persisted-state rule.
func (s *ec2Store) getLaunchTemplate(ctx context.Context, id string) (*LaunchTemplate, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsLaunchTemplates, serviceutil.RegionKey(s.region(ctx), id))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !ok {
		return nil, nil
	}
	var lt LaunchTemplate
	if err := json.Unmarshal([]byte(raw), &lt); err != nil {
		return nil, nil
	}
	return &lt, nil
}

func (s *ec2Store) listLaunchTemplates(ctx context.Context) ([]*LaunchTemplate, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsLaunchTemplates, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	out := make([]*LaunchTemplate, 0, len(pairs))
	for _, p := range pairs {
		var lt LaunchTemplate
		if err := json.Unmarshal([]byte(p.Value), &lt); err != nil {
			continue
		}
		out = append(out, &lt)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LaunchTemplateID < out[j].LaunchTemplateID })
	return out, nil
}

// getLaunchTemplateByName resolves a template name to its record. Launch
// template names are unique per account and region on AWS, so the first match
// is the only one.
func (s *ec2Store) getLaunchTemplateByName(ctx context.Context, name string) (*LaunchTemplate, *protocol.AWSError) {
	all, aerr := s.listLaunchTemplates(ctx)
	if aerr != nil {
		return nil, aerr
	}
	for _, lt := range all {
		if lt.LaunchTemplateName == name {
			return lt, nil
		}
	}
	return nil, nil
}

func (s *ec2Store) putLaunchTemplateVersion(ctx context.Context, v *LaunchTemplateVersion) *protocol.AWSError {
	raw, err := json.Marshal(v)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	key := serviceutil.RegionKey(s.region(ctx), launchTemplateVersionKey(v.LaunchTemplateID, v.VersionNumber))
	if err := s.store.Set(ctx, nsLaunchTemplateVersions, key, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// getLaunchTemplateVersion returns one version, or nil when it does not exist.
func (s *ec2Store) getLaunchTemplateVersion(ctx context.Context, templateID string, version int64) (*LaunchTemplateVersion, *protocol.AWSError) {
	key := serviceutil.RegionKey(s.region(ctx), launchTemplateVersionKey(templateID, version))
	raw, ok, err := s.store.Get(ctx, nsLaunchTemplateVersions, key)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !ok {
		return nil, nil
	}
	var v LaunchTemplateVersion
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, nil
	}
	return &v, nil
}

// listLaunchTemplateVersions returns a template's versions in numeric order.
func (s *ec2Store) listLaunchTemplateVersions(ctx context.Context, templateID string) ([]*LaunchTemplateVersion, *protocol.AWSError) {
	prefix := serviceutil.RegionKey(s.region(ctx), templateID+"/")
	pairs, err := s.store.Scan(ctx, nsLaunchTemplateVersions, prefix)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	out := make([]*LaunchTemplateVersion, 0, len(pairs))
	for _, p := range pairs {
		var v LaunchTemplateVersion
		if err := json.Unmarshal([]byte(p.Value), &v); err != nil {
			continue
		}
		out = append(out, &v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].VersionNumber < out[j].VersionNumber })
	return out, nil
}

func (s *ec2Store) deleteLaunchTemplateVersion(ctx context.Context, templateID string, version int64) *protocol.AWSError {
	key := serviceutil.RegionKey(s.region(ctx), launchTemplateVersionKey(templateID, version))
	if err := s.store.Delete(ctx, nsLaunchTemplateVersions, key); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// deleteLaunchTemplate removes a template, every version it owns and its tags.
func (s *ec2Store) deleteLaunchTemplate(ctx context.Context, templateID string) *protocol.AWSError {
	versions, aerr := s.listLaunchTemplateVersions(ctx, templateID)
	if aerr != nil {
		return aerr
	}
	for _, v := range versions {
		if aerr := s.deleteLaunchTemplateVersion(ctx, templateID, v.VersionNumber); aerr != nil {
			return aerr
		}
	}
	if err := s.store.Delete(ctx, nsLaunchTemplates, serviceutil.RegionKey(s.region(ctx), templateID)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return s.deleteTags(ctx, templateID)
}
