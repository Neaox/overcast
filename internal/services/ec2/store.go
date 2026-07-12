package ec2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	nsVPCs                  = "ec2:vpcs"
	nsSubnets               = "ec2:subnets"
	nsSecurityGroups        = "ec2:security-groups"
	nsInstances             = "ec2:instances"
	nsKeyPairs              = "ec2:keypairs"
	nsRouteTables           = "ec2:route-tables"
	nsInternetGateways      = "ec2:internet-gateways"
	nsVpnGateways           = "ec2:vpn-gateways"
	nsVpcPeeringConnections = "ec2:vpc-peering-connections"
	nsTags                  = "ec2:tags"
	nsElasticIPs            = "ec2:elastic-ips"
	nsNatGateways           = "ec2:nat-gateways"
	nsNetworkInterfaces     = "ec2:network-interfaces"
	nsVpcEndpoints          = "ec2:vpc-endpoints"
	nsVPCIPTranslations     = "ec2:vpc-ip-translations"
	nsVPCIPTranslationsReal = "ec2:vpc-ip-translations-real"
)

// VPC represents an EC2 VPC resource.
type VPC struct {
	VpcID           string `json:"VpcId"`
	CidrBlock       string `json:"CidrBlock"`
	State           string `json:"State"`
	IsDefault       bool   `json:"IsDefault,omitempty"`
	DockerNetworkID string `json:"DockerNetworkId,omitempty"`

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
}

// Subnet represents an EC2 Subnet resource.
type Subnet struct {
	SubnetID         string `json:"SubnetId"`
	VpcID            string `json:"VpcId"`
	CidrBlock        string `json:"CidrBlock"`
	AvailabilityZone string `json:"AvailabilityZone"`
	State            string `json:"State"`
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
}

func newEC2Store(store state.Store, defaultRegion string) *ec2Store {
	return &ec2Store{store: store, defaultRegion: defaultRegion}
}

// region extracts the per-request region from context, falling back to the default.
func (s *ec2Store) region(ctx context.Context) string {
	return middleware.RegionFromContext(ctx, s.defaultRegion)
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

func (s *ec2Store) deleteVPC(ctx context.Context, id string) *protocol.AWSError {
	if _, aerr := s.getVPC(ctx, id); aerr != nil {
		return aerr
	}
	if err := s.store.Delete(ctx, nsVPCs, serviceutil.RegionKey(s.region(ctx), id)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
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
	return nil
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
	return nil
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
	Tags             []Tag         `json:"Tags,omitempty"`
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
	raw, err := json.Marshal(inst)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsInstances, serviceutil.RegionKey(s.region(ctx), inst.InstanceID), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
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
	return nil
}

// ── Route Tables ──────────────────────────────────────────────────────────────

// Route represents a route in a route table.
type Route struct {
	DestinationCidrBlock string `json:"DestinationCidrBlock"`
	GatewayID            string `json:"GatewayId,omitempty"`
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
	return nil
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
	return nil
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
	Tags             []Tag                  `json:"Tags,omitempty"`
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
	return nil
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
	Tags                   []Tag                       `json:"Tags,omitempty"`
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
		return nil, &protocol.AWSError{
			Code:       "InvalidAddressID.NotFound",
			Message:    fmt.Sprintf("The address '%s' does not exist", allocID),
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
	return nil
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
	Tags         []Tag  `json:"Tags,omitempty"`
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
	return nil
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
	return nil
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
	return nil
}
