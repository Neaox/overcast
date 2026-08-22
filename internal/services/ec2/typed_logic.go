package ec2

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
)

// ec2TagSpecification decodes one TagSpecification.N entry: the resource type
// it targets and the tags to apply if that type matches — the typed twin of
// parseTagSpecifications' per-N loop in tags.go.
type ec2TagSpecification struct {
	ResourceType string `json:"ResourceType"`
	Tags         []Tag  `json:"Tag"`
}

// typedTagSpecifications extracts resourceType's tags from decoded
// TagSpecification.N entries, mirroring parseTagSpecifications' resourceType
// filter (tags.go) for the typed request path.
func typedTagSpecifications(specs []ec2TagSpecification, resourceType string) []Tag {
	var tags []Tag
	for _, spec := range specs {
		if resourceType != "" && spec.ResourceType != resourceType {
			continue
		}
		tags = append(tags, spec.Tags...)
	}
	return tags
}

// ec2AttributeBoolValue decodes AWS's `Attribute.Value=true|false` boolean
// attribute wrapper — ModifySubnetAttribute's MapPublicIpOnLaunch,
// ModifyVpcAttribute's EnableDnsSupport/EnableDnsHostnames. A nil pointer
// means the caller did not send the attribute at all, which every one of
// these operations treats as "leave the stored value alone" rather than as a
// false value.
type ec2AttributeBoolValue struct {
	Value bool `json:"Value"`
}

// ec2AttributeStringValue is ec2AttributeBoolValue's string-valued twin —
// ModifyInstanceAttribute's InstanceType.Value.
type ec2AttributeStringValue struct {
	Value string `json:"Value"`
}

// ── Request types (json tags for codec.Decode QueryXML form mapping) ───
//
// Every Describe* request declares the two things an EC2 describe selects on:
// its `<Resource>Id.N` list and `Filter.N.Name` / `Filter.N.Value.M`. They used
// to be empty structs, which is why the typed path answered with every resource
// in the region however the caller filtered — see describe.go and #754. The
// bodies below hand both to the same function the legacy handler calls.

type describeRegionsReq struct {
	Filters []ec2Filter `json:"Filter"`
}

type describeAzsReq struct {
	Filters []ec2Filter `json:"Filter"`
}

type describeInstancesReq struct {
	InstanceIDs []string    `json:"InstanceId"`
	Filters     []ec2Filter `json:"Filter"`
}

type describeInstanceTypesReq struct {
	InstanceTypes []string    `json:"InstanceType"`
	Filters       []ec2Filter `json:"Filter"`
}

type createVpcReq struct {
	CidrBlock string `json:"CidrBlock"`
}

type describeVpcsReq struct {
	VpcIDs  []string    `json:"VpcId"`
	Filters []ec2Filter `json:"Filter"`
}

type deleteVpcReq struct {
	VpcID string `json:"VpcId"`
}

type createSubnetReq struct {
	VpcID            string `json:"VpcId"`
	CidrBlock        string `json:"CidrBlock"`
	AvailabilityZone string `json:"AvailabilityZone"`
}

type deleteSubnetReq struct {
	SubnetID string `json:"SubnetId"`
}

type createSecurityGroupReq struct {
	GroupName        string `json:"GroupName"`
	GroupDescription string `json:"GroupDescription"`
	VpcID            string `json:"VpcId"`
}

type deleteSecurityGroupReq struct {
	GroupID string `json:"GroupId"`
}

type authorizeSGIngressReq struct {
	GroupID       string         `json:"GroupId"`
	IpPermissions []IpPermission `json:"IpPermissions"`
}

type authorizeSGEgressReq struct {
	GroupID       string         `json:"GroupId"`
	IpPermissions []IpPermission `json:"IpPermissions"`
}

type revokeSGIngressReq struct {
	GroupID       string         `json:"GroupId"`
	IpPermissions []IpPermission `json:"IpPermissions"`
}

type revokeSGEgressReq struct {
	GroupID       string         `json:"GroupId"`
	IpPermissions []IpPermission `json:"IpPermissions"`
}

type describeSecurityGroupsReq struct {
	GroupIDs []string    `json:"GroupId"`
	Filters  []ec2Filter `json:"Filter"`
}

type describeSubnetsReq struct {
	SubnetIDs []string    `json:"SubnetId"`
	Filters   []ec2Filter `json:"Filter"`
}

type runInstancesReq struct {
	ImageID           string                `json:"ImageId"`
	InstanceType      string                `json:"InstanceType"`
	MinCount          int                   `json:"MinCount"`
	MaxCount          int                   `json:"MaxCount"`
	SubnetID          string                `json:"SubnetId"`
	SecurityGroupIDs  []string              `json:"SecurityGroupId"`
	TagSpecifications []ec2TagSpecification `json:"TagSpecification"`
}

type terminateInstancesReq struct {
	InstanceIDs []string `json:"InstanceId"`
}

type stopInstancesReq struct {
	InstanceIDs []string `json:"InstanceId"`
}

type startInstancesReq struct {
	InstanceIDs []string `json:"InstanceId"`
}

type describeImagesReq struct {
	ImageIDs []string    `json:"ImageId"`
	Filters  []ec2Filter `json:"Filter"`
}

type createKeyPairReq struct {
	KeyName           string                `json:"KeyName"`
	TagSpecifications []ec2TagSpecification `json:"TagSpecification"`
}

type describeKeyPairsReq struct {
	KeyNames []string    `json:"KeyName"`
	Filters  []ec2Filter `json:"Filter"`
}

type deleteKeyPairReq struct {
	KeyName string `json:"KeyName"`
}

type createRouteTableReq struct {
	VpcID string `json:"VpcId"`
}

type describeRouteTablesReq struct {
	RouteTableIDs []string    `json:"RouteTableId"`
	Filters       []ec2Filter `json:"Filter"`
}

type deleteRouteTableReq struct {
	RouteTableID string `json:"RouteTableId"`
}

type createRouteReq struct {
	RouteTableID         string `json:"RouteTableId"`
	DestinationCidrBlock string `json:"DestinationCidrBlock"`
	GatewayID            string `json:"GatewayId"`
	NatGatewayID         string `json:"NatGatewayId"`
}

type deleteRouteReq struct {
	RouteTableID         string `json:"RouteTableId"`
	DestinationCidrBlock string `json:"DestinationCidrBlock"`
}

type associateRouteTableReq struct {
	RouteTableID string `json:"RouteTableId"`
	SubnetID     string `json:"SubnetId"`
}

type disassociateRouteTableReq struct {
	AssociationID string `json:"AssociationId"`
}

type createIGWReq struct{}

type describeIGWsReq struct {
	InternetGatewayIDs []string    `json:"InternetGatewayId"`
	Filters            []ec2Filter `json:"Filter"`
}

type deleteIGWReq struct {
	InternetGatewayID string `json:"InternetGatewayId"`
}

type attachIGWReq struct {
	InternetGatewayID string `json:"InternetGatewayId"`
	VpcID             string `json:"VpcId"`
}

type detachIGWReq struct {
	InternetGatewayID string `json:"InternetGatewayId"`
	VpcID             string `json:"VpcId"`
}

type createVPCPeeringReq struct {
	VpcID             string                `json:"VpcId"`
	PeerVpcID         string                `json:"PeerVpcId"`
	TagSpecifications []ec2TagSpecification `json:"TagSpecification"`
}

type acceptVPCPeeringReq struct {
	VpcPeeringConnectionID string `json:"VpcPeeringConnectionId"`
}

type describeVPCPeeringsReq struct {
	VpcPeeringConnectionIDs []string    `json:"VpcPeeringConnectionId"`
	Filters                 []ec2Filter `json:"Filter"`
}

type deleteVPCPeeringReq struct {
	VpcPeeringConnectionID string `json:"VpcPeeringConnectionId"`
}

type createTagsReq struct {
	ResourceIDs []string        `json:"ResourceId"`
	Tags        []ec2TagRequest `json:"Tag"`
}

type ec2TagRequest struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

type deleteTagsReq struct {
	ResourceIDs []string `json:"ResourceId"`
	Tags        []Tag    `json:"Tag"`
}

type describeTagsReq struct {
	Filters []ec2Filter `json:"Filter"`
}

type allocateAddressReq struct {
	Domain string `json:"Domain"`
}

type releaseAddressReq struct {
	AllocationID string `json:"AllocationId"`
}

type describeAddressesReq struct {
	AllocationIDs []string    `json:"AllocationId"`
	Filters       []ec2Filter `json:"Filter"`
}

type associateAddressReq struct {
	AllocationID string `json:"AllocationId"`
	InstanceID   string `json:"InstanceId"`
}

type disassociateAddressReq struct {
	AssociationID string `json:"AssociationId"`
}

type createNatGatewayReq struct {
	SubnetID          string                `json:"SubnetId"`
	AllocationID      string                `json:"AllocationId"`
	TagSpecifications []ec2TagSpecification `json:"TagSpecification"`
}

type describeNatGatewaysReq struct {
	NatGatewayIDs []string    `json:"NatGatewayId"`
	Filters       []ec2Filter `json:"Filter"`
}

type deleteNatGatewayReq struct {
	NatGatewayID string `json:"NatGatewayId"`
}

type modifySubnetAttributeReq struct {
	SubnetID            string                 `json:"SubnetId"`
	MapPublicIpOnLaunch *ec2AttributeBoolValue `json:"MapPublicIpOnLaunch"`
}

type modifyVpcAttributeReq struct {
	VpcID              string                 `json:"VpcId"`
	EnableDnsSupport   *ec2AttributeBoolValue `json:"EnableDnsSupport"`
	EnableDnsHostnames *ec2AttributeBoolValue `json:"EnableDnsHostnames"`
}

type describeVpcAttributeReq struct {
	VpcID     string `json:"VpcId"`
	Attribute string `json:"Attribute"`
}

type describeDhcpOptionsReq struct {
	Filters []ec2Filter `json:"Filter"`
}

type describeAccountAttributesReq struct{}

type createVpnGatewayReq struct {
	Type              string                `json:"Type"`
	AmazonSideAsn     string                `json:"AmazonSideAsn"`
	AvailabilityZone  string                `json:"AvailabilityZone"`
	TagSpecifications []ec2TagSpecification `json:"TagSpecification"`
}

type attachVpnGatewayReq struct {
	VpnGatewayID string `json:"VpnGatewayId"`
	VpcID        string `json:"VpcId"`
}

type describeVpnGatewaysReq struct {
	VpnGatewayIDs []string    `json:"VpnGatewayId"`
	Filters       []ec2Filter `json:"Filter"`
}

type detachVpnGatewayReq struct {
	VpnGatewayID string `json:"VpnGatewayId"`
	VpcID        string `json:"VpcId"`
}

type deleteVpnGatewayReq struct {
	VpnGatewayID string `json:"VpnGatewayId"`
}

type createNetworkInterfaceReq struct {
	SubnetID    string `json:"SubnetId"`
	Description string `json:"Description"`
}

type describeNetworkInterfacesReq struct {
	NetworkInterfaceIDs []string    `json:"NetworkInterfaceId"`
	Filters             []ec2Filter `json:"Filter"`
}

type deleteNetworkInterfaceReq struct {
	NetworkInterfaceID string `json:"NetworkInterfaceId"`
}

type modifyInstanceAttributeReq struct {
	InstanceID   string                   `json:"InstanceId"`
	InstanceType *ec2AttributeStringValue `json:"InstanceType"`
}

type createVpcEndpointReq struct {
	VpcID             string                `json:"VpcId"`
	ServiceName       string                `json:"ServiceName"`
	VpcEndpointType   string                `json:"VpcEndpointType"`
	TagSpecifications []ec2TagSpecification `json:"TagSpecification"`
}

type describeVpcEndpointsReq struct {
	VpcEndpointIDs []string    `json:"VpcEndpointId"`
	Filters        []ec2Filter `json:"Filter"`
}

type deleteVpcEndpointsReq struct {
	VpcEndpointIDs []string `json:"VpcEndpointId"`
}

// ── Response types (xml tags for QueryXML codec WriteResponse) ────
//
// A Describe* migrated to the shared body in describe.go returns the legacy
// handler's own xmlDescribe…Response type rather than a second declaration of
// the same shape, so there is no second XML rendering to fall behind the first.
// What remains here belongs to the operations still on their own typed body.

type typedInstanceXML struct {
	InstanceID    string                `xml:"instanceId"`
	ImageID       string                `xml:"imageId"`
	InstanceState typedInstanceStateXML `xml:"instanceState"`
	InstanceType  string                `xml:"instanceType"`
	LaunchTime    string                `xml:"launchTime"`
	SubnetID      string                `xml:"subnetId,omitempty"`
	VpcID         string                `xml:"vpcId,omitempty"`
	PrivateIP     string                `xml:"privateIpAddress,omitempty"`
	Placement     typedPlacementXML     `xml:"placement"`
	GroupSet      []typedSGRefXML       `xml:"groupSet>item,omitempty"`
	TagSet        []typedTagXML         `xml:"tagSet>item,omitempty"`
}

type typedSGRefXML struct {
	GroupID   string `xml:"groupId"`
	GroupName string `xml:"groupName"`
}

type typedInstanceStateXML struct {
	Code int    `xml:"code"`
	Name string `xml:"name"`
}

type typedPlacementXML struct {
	AvailabilityZone string `xml:"availabilityZone"`
}

type typedTagXML struct {
	Key   string `xml:"key"`
	Value string `xml:"value"`
}

type typedInstanceStateChangeXML struct {
	InstanceID    string                `xml:"instanceId"`
	PreviousState typedInstanceStateXML `xml:"previousState"`
	CurrentState  typedInstanceStateXML `xml:"currentState"`
}

type createVpcResp struct {
	XMLName   struct{}    `xml:"CreateVpcResponse"`
	Xmlns     string      `xml:"xmlns,attr"`
	RequestID string      `xml:"requestId"`
	Vpc       typedVpcXML `xml:"vpc"`
}

type typedVpcXML struct {
	VpcID                   string              `xml:"vpcId"`
	State                   string              `xml:"state"`
	CidrBlock               string              `xml:"cidrBlock"`
	DhcpOptionsID           string              `xml:"dhcpOptionsId"`
	InstanceTenancy         string              `xml:"instanceTenancy"`
	IsDefault               bool                `xml:"isDefault"`
	CidrBlockAssociationSet []typedCidrAssocXML `xml:"cidrBlockAssociationSet>item"`
	TagSet                  []typedTagXML       `xml:"tagSet>item,omitempty"`
}

type typedCidrAssocXML struct {
	AssociationID  string            `xml:"associationId"`
	CidrBlock      string            `xml:"cidrBlock"`
	CidrBlockState typedCidrStateXML `xml:"cidrBlockState"`
}

type typedCidrStateXML struct {
	State string `xml:"state"`
}

type deleteVpcResp struct {
	XMLName   struct{} `xml:"DeleteVpcResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type createSubnetResp struct {
	XMLName   struct{}       `xml:"CreateSubnetResponse"`
	Xmlns     string         `xml:"xmlns,attr"`
	RequestID string         `xml:"requestId"`
	Subnet    typedSubnetXML `xml:"subnet"`
}

type deleteSubnetResp struct {
	XMLName   struct{} `xml:"DeleteSubnetResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type typedSubnetXML struct {
	SubnetID                string        `xml:"subnetId"`
	State                   string        `xml:"state"`
	VpcID                   string        `xml:"vpcId"`
	CidrBlock               string        `xml:"cidrBlock"`
	AvailabilityZone        string        `xml:"availabilityZone"`
	AvailableIPAddressCount int           `xml:"availableIpAddressCount"`
	DefaultForAz            bool          `xml:"defaultForAz"`
	MapPublicIPOnLaunch     bool          `xml:"mapPublicIpOnLaunch"`
	TagSet                  []typedTagXML `xml:"tagSet>item,omitempty"`
}

type createSGResp struct {
	XMLName   struct{} `xml:"CreateSecurityGroupResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
	GroupID   string   `xml:"groupId"`
}

type deleteSGResp struct {
	XMLName   struct{} `xml:"DeleteSecurityGroupResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type authorizeSGIngressResp struct {
	XMLName   struct{} `xml:"AuthorizeSecurityGroupIngressResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type authorizeSGEgressResp struct {
	XMLName   struct{} `xml:"AuthorizeSecurityGroupEgressResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type revokeSGIngressResp struct {
	XMLName   struct{} `xml:"RevokeSecurityGroupIngressResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type revokeSGEgressResp struct {
	XMLName   struct{} `xml:"RevokeSecurityGroupEgressResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type runInstancesResp struct {
	XMLName       struct{}           `xml:"RunInstancesResponse"`
	Xmlns         string             `xml:"xmlns,attr"`
	RequestID     string             `xml:"requestId"`
	ReservationID string             `xml:"reservationId"`
	OwnerID       string             `xml:"ownerId"`
	Instances     []typedInstanceXML `xml:"instancesSet>item"`
}

type terminateInstancesResp struct {
	XMLName   struct{}                      `xml:"TerminateInstancesResponse"`
	Xmlns     string                        `xml:"xmlns,attr"`
	RequestID string                        `xml:"requestId"`
	Instances []typedInstanceStateChangeXML `xml:"instancesSet>item"`
}

type startInstancesResp struct {
	XMLName   struct{}                      `xml:"StartInstancesResponse"`
	Xmlns     string                        `xml:"xmlns,attr"`
	RequestID string                        `xml:"requestId"`
	Instances []typedInstanceStateChangeXML `xml:"instancesSet>item"`
}

type stopInstancesResp struct {
	XMLName   struct{}                      `xml:"StopInstancesResponse"`
	Xmlns     string                        `xml:"xmlns,attr"`
	RequestID string                        `xml:"requestId"`
	Instances []typedInstanceStateChangeXML `xml:"instancesSet>item"`
}

type createKeyPairResp struct {
	XMLName        struct{}      `xml:"CreateKeyPairResponse"`
	Xmlns          string        `xml:"xmlns,attr"`
	RequestID      string        `xml:"requestId"`
	KeyName        string        `xml:"keyName"`
	KeyFingerprint string        `xml:"keyFingerprint"`
	KeyMaterial    string        `xml:"keyMaterial"`
	KeyPairID      string        `xml:"keyPairId"`
	TagSet         []typedTagXML `xml:"tagSet>item,omitempty"`
}

type deleteKeyPairResp struct {
	XMLName   struct{} `xml:"DeleteKeyPairResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type createRouteTableResp struct {
	XMLName    struct{}           `xml:"CreateRouteTableResponse"`
	Xmlns      string             `xml:"xmlns,attr"`
	RequestID  string             `xml:"requestId"`
	RouteTable typedRouteTableXML `xml:"routeTable"`
}

type typedRouteTableXML struct {
	RouteTableID   string                          `xml:"routeTableId"`
	VpcID          string                          `xml:"vpcId"`
	RouteSet       []typedRouteXML                 `xml:"routeSet>item"`
	AssociationSet []typedRouteTableAssociationXML `xml:"associationSet>item,omitempty"`
	Tags           []typedTagXML                   `xml:"tagSet>item,omitempty"`
}

type typedRouteXML struct {
	DestinationCidrBlock string `xml:"destinationCidrBlock"`
	GatewayID            string `xml:"gatewayId,omitempty"`
	NatGatewayID         string `xml:"natGatewayId,omitempty"`
	Origin               string `xml:"origin"`
	State                string `xml:"state"`
}

type typedRouteTableAssociationXML struct {
	AssociationID string `xml:"routeTableAssociationId"`
	RouteTableID  string `xml:"routeTableId"`
	SubnetID      string `xml:"subnetId,omitempty"`
	Main          bool   `xml:"main"`
}

type deleteRouteTableResp struct {
	XMLName   struct{} `xml:"DeleteRouteTableResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type createRouteResp struct {
	XMLName   struct{} `xml:"CreateRouteResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type deleteRouteResp struct {
	XMLName   struct{} `xml:"DeleteRouteResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type associateRouteTableResp struct {
	XMLName       struct{} `xml:"AssociateRouteTableResponse"`
	Xmlns         string   `xml:"xmlns,attr"`
	RequestID     string   `xml:"requestId"`
	AssociationID string   `xml:"newAssociationId"`
}

type disassociateRouteTableResp struct {
	XMLName   struct{} `xml:"DisassociateRouteTableResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type createIGWResp struct {
	XMLName         struct{}    `xml:"CreateInternetGatewayResponse"`
	Xmlns           string      `xml:"xmlns,attr"`
	RequestID       string      `xml:"requestId"`
	InternetGateway typedIGWXML `xml:"internetGateway"`
}

type typedIGWXML struct {
	InternetGatewayID string                  `xml:"internetGatewayId"`
	AttachmentSet     []typedIGWAttachmentXML `xml:"attachmentSet>item,omitempty"`
	Tags              []typedTagXML           `xml:"tagSet>item,omitempty"`
}

type typedIGWAttachmentXML struct {
	VpcID string `xml:"vpcId"`
	State string `xml:"state"`
}

type deleteIGWResp struct {
	XMLName   struct{} `xml:"DeleteInternetGatewayResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type attachIGWResp struct {
	XMLName   struct{} `xml:"AttachInternetGatewayResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type detachIGWResp struct {
	XMLName   struct{} `xml:"DetachInternetGatewayResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type createVPCPeeringResp struct {
	XMLName              struct{}           `xml:"CreateVpcPeeringConnectionResponse"`
	Xmlns                string             `xml:"xmlns,attr"`
	RequestID            string             `xml:"requestId"`
	VpcPeeringConnection typedVPCPeeringXML `xml:"vpcPeeringConnection"`
}

type acceptVPCPeeringResp struct {
	XMLName              struct{}           `xml:"AcceptVpcPeeringConnectionResponse"`
	Xmlns                string             `xml:"xmlns,attr"`
	RequestID            string             `xml:"requestId"`
	VpcPeeringConnection typedVPCPeeringXML `xml:"vpcPeeringConnection"`
}

type typedVPCPeeringXML struct {
	VpcPeeringConnectionID string                    `xml:"vpcPeeringConnectionId"`
	RequesterVpcInfo       typedVPCPeeringVpcInfoXML `xml:"requesterVpcInfo"`
	AccepterVpcInfo        typedVPCPeeringVpcInfoXML `xml:"accepterVpcInfo"`
	Status                 typedVPCPeeringStatusXML  `xml:"status"`
	TagSet                 []typedTagXML             `xml:"tagSet>item,omitempty"`
}

type typedVPCPeeringVpcInfoXML struct {
	OwnerID   string `xml:"ownerId"`
	VpcID     string `xml:"vpcId"`
	CidrBlock string `xml:"cidrBlock,omitempty"`
	Region    string `xml:"region"`
}

type typedVPCPeeringStatusXML struct {
	Code    string `xml:"code"`
	Message string `xml:"message"`
}

type deleteVPCPeeringResp struct {
	XMLName   struct{} `xml:"DeleteVpcPeeringConnectionResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type createTagsResp struct {
	XMLName   struct{} `xml:"CreateTagsResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type deleteTagsResp struct {
	XMLName   struct{} `xml:"DeleteTagsResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type allocateAddressResp struct {
	XMLName      struct{} `xml:"AllocateAddressResponse"`
	Xmlns        string   `xml:"xmlns,attr"`
	RequestID    string   `xml:"requestId"`
	PublicIP     string   `xml:"publicIp"`
	AllocationID string   `xml:"allocationId"`
	Domain       string   `xml:"domain"`
}

type releaseAddressResp struct {
	XMLName   struct{} `xml:"ReleaseAddressResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type associateAddressResp struct {
	XMLName       struct{} `xml:"AssociateAddressResponse"`
	Xmlns         string   `xml:"xmlns,attr"`
	RequestID     string   `xml:"requestId"`
	Return        bool     `xml:"return"`
	AssociationID string   `xml:"associationId"`
}

type disassociateAddressResp struct {
	XMLName   struct{} `xml:"DisassociateAddressResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type createNatGatewayResp struct {
	XMLName    struct{}           `xml:"CreateNatGatewayResponse"`
	Xmlns      string             `xml:"xmlns,attr"`
	RequestID  string             `xml:"requestId"`
	NatGateway typedNatGatewayXML `xml:"natGateway"`
}

type typedNatGatewayXML struct {
	NatGatewayID string                `xml:"natGatewayId"`
	SubnetID     string                `xml:"subnetId"`
	VpcID        string                `xml:"vpcId"`
	State        string                `xml:"state"`
	CreateTime   string                `xml:"createTime"`
	Addresses    []typedNatGWAddrXML   `xml:"natGatewayAddressSet>item"`
	Tags         []typedResourceTagXML `xml:"tagSet>item,omitempty"`
}

type typedNatGWAddrXML struct {
	AllocationID       string `xml:"allocationId"`
	PublicIP           string `xml:"publicIp,omitempty"`
	PrivateIP          string `xml:"privateIp,omitempty"`
	NetworkInterfaceID string `xml:"networkInterfaceId,omitempty"`
}

type typedResourceTagXML struct {
	Key   string `xml:"key"`
	Value string `xml:"value"`
}

type deleteNatGatewayResp struct {
	XMLName      struct{} `xml:"DeleteNatGatewayResponse"`
	Xmlns        string   `xml:"xmlns,attr"`
	RequestID    string   `xml:"requestId"`
	NatGatewayID string   `xml:"natGatewayId"`
}

type modifySubnetAttributeResp struct {
	XMLName   struct{} `xml:"ModifySubnetAttributeResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type modifyVpcAttributeResp struct {
	XMLName   struct{} `xml:"ModifyVpcAttributeResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type createVpnGatewayResp struct {
	XMLName    struct{}           `xml:"CreateVpnGatewayResponse"`
	Xmlns      string             `xml:"xmlns,attr"`
	RequestID  string             `xml:"requestId"`
	VpnGateway typedVpnGatewayXML `xml:"vpnGateway"`
}

type attachVpnGatewayResp struct {
	XMLName    struct{}                     `xml:"AttachVpnGatewayResponse"`
	Xmlns      string                       `xml:"xmlns,attr"`
	RequestID  string                       `xml:"requestId"`
	Attachment typedVpnGatewayAttachmentXML `xml:"attachment"`
}

type detachVpnGatewayResp struct {
	XMLName   struct{} `xml:"DetachVpnGatewayResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type deleteVpnGatewayResp struct {
	XMLName   struct{} `xml:"DeleteVpnGatewayResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type typedVpnGatewayXML struct {
	VpnGatewayID     string                         `xml:"vpnGatewayId"`
	State            string                         `xml:"state"`
	Type             string                         `xml:"type"`
	AvailabilityZone string                         `xml:"availabilityZone,omitempty"`
	Attachments      []typedVpnGatewayAttachmentXML `xml:"attachments>item"`
	AmazonSideAsn    int64                          `xml:"amazonSideAsn"`
	Tags             []typedResourceTagXML          `xml:"tagSet>item,omitempty"`
}

type typedVpnGatewayAttachmentXML struct {
	VpcID string `xml:"vpcId"`
	State string `xml:"state"`
}

type createNetworkInterfaceResp struct {
	XMLName          struct{}                 `xml:"CreateNetworkInterfaceResponse"`
	Xmlns            string                   `xml:"xmlns,attr"`
	RequestID        string                   `xml:"requestId"`
	NetworkInterface typedNetworkInterfaceXML `xml:"networkInterface"`
}

type typedNetworkInterfaceXML struct {
	NetworkInterfaceID string        `xml:"networkInterfaceId"`
	SubnetID           string        `xml:"subnetId"`
	VpcID              string        `xml:"vpcId"`
	AvailabilityZone   string        `xml:"availabilityZone"`
	Description        string        `xml:"description"`
	PrivateIPAddress   string        `xml:"privateIpAddress"`
	Status             string        `xml:"status"`
	MacAddress         string        `xml:"macAddress"`
	TagSet             []typedTagXML `xml:"tagSet>item,omitempty"`
}

type deleteNetworkInterfaceResp struct {
	XMLName   struct{} `xml:"DeleteNetworkInterfaceResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type modifyInstanceAttributeResp struct {
	XMLName   struct{} `xml:"ModifyInstanceAttributeResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type createVpcEndpointResp struct {
	XMLName     struct{}            `xml:"CreateVpcEndpointResponse"`
	Xmlns       string              `xml:"xmlns,attr"`
	RequestID   string              `xml:"requestId"`
	VpcEndpoint typedVpcEndpointXML `xml:"vpcEndpoint"`
}

type typedVpcEndpointXML struct {
	VpcEndpointID   string        `xml:"vpcEndpointId"`
	VpcID           string        `xml:"vpcId"`
	ServiceName     string        `xml:"serviceName"`
	State           string        `xml:"state"`
	VpcEndpointType string        `xml:"vpcEndpointType"`
	TagSet          []typedTagXML `xml:"tagSet>item,omitempty"`
}

type deleteVpcEndpointsResp struct {
	XMLName   struct{} `xml:"DeleteVpcEndpointsResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

// ── Typed handler functions ────────────────────────────────────────

func (h *Handler) describeRegionsTyped(ctx context.Context, req *describeRegionsReq) (*xmlDescribeRegionsResponse, *protocol.AWSError) {
	return h.describeRegions(ctx, typedQuery(nil, req.Filters))
}

func (h *Handler) describeAzsTyped(ctx context.Context, req *describeAzsReq) (*xmlDescribeAZsResponse, *protocol.AWSError) {
	return h.describeAvailabilityZones(ctx, typedQuery(nil, req.Filters))
}

func (h *Handler) describeInstancesTyped(ctx context.Context, req *describeInstancesReq) (*xmlDescribeInstancesResponse, *protocol.AWSError) {
	return h.describeInstances(ctx, typedQuery(req.InstanceIDs, req.Filters))
}

func (h *Handler) describeInstanceTypesTyped(ctx context.Context, req *describeInstanceTypesReq) (*xmlDescribeInstanceTypesResponse, *protocol.AWSError) {
	return h.describeInstanceTypes(ctx, typedQuery(nil, req.Filters), req.InstanceTypes)
}

func (h *Handler) createVpcTyped(ctx context.Context, req *createVpcReq) (*createVpcResp, *protocol.AWSError) {
	if req.CidrBlock == "" {
		return nil, ec2err("MissingParameter", "CidrBlock is required", http.StatusBadRequest)
	}
	vpcID := fmt.Sprintf("vpc-%s", shortID())
	// Every VPC gets a DHCP options set at creation, real AWS's default-set
	// behavior absent an explicit association. Minted once and persisted on
	// the record so DescribeVpcs echoes the same value back (#1277). See
	// legacy's CreateVpc for the same treatment.
	dhcpOptionsID := fmt.Sprintf("dopt-%s", shortID())
	// Matches real AWS: every new VPC has DNS resolution on; DNS hostnames
	// stay off until ModifyVpcAttribute turns them on. See legacy's CreateVpc
	// and #1144 — EnableDnsSupport is a stored field, not a wire default this
	// body gets to skip.
	vpc := &VPC{
		VpcID: vpcID, CidrBlock: req.CidrBlock, State: "available", CreateTime: h.clk.Now().UnixMilli(),
		EnableDnsSupport: true, EnableDnsHostnames: false, DhcpOptionsId: dhcpOptionsID,
	}
	if h.vpcStrategy != nil {
		if aerr := h.vpcStrategy.EnsureNetwork(ctx, vpc); aerr != nil {
			return nil, aerr
		}
	}
	if aerr := h.putVPCWithMainRouteTable(ctx, vpc); aerr != nil {
		return nil, aerr
	}
	h.publish(ctx, events.EC2VpcCreated, events.ResourcePayload{Name: vpcID})
	return &createVpcResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Vpc: typedVpcXML{
			VpcID:           vpcID,
			State:           "available",
			CidrBlock:       req.CidrBlock,
			DhcpOptionsID:   dhcpOptionsID,
			InstanceTenancy: "default",
			IsDefault:       false,
			CidrBlockAssociationSet: []typedCidrAssocXML{{
				AssociationID:  fmt.Sprintf("vpc-cidr-assoc-%s", shortID()),
				CidrBlock:      req.CidrBlock,
				CidrBlockState: typedCidrStateXML{State: "associated"},
			}},
		},
	}, nil
}

func (h *Handler) describeVpcsTyped(ctx context.Context, req *describeVpcsReq) (*xmlDescribeVpcsResponse, *protocol.AWSError) {
	return h.describeVpcs(ctx, typedQuery(req.VpcIDs, req.Filters))
}

func (h *Handler) deleteVpcTyped(ctx context.Context, req *deleteVpcReq) (*deleteVpcResp, *protocol.AWSError) {
	if req.VpcID == "" {
		return nil, ec2err("MissingParameter", "VpcId is required", http.StatusBadRequest)
	}
	vpc, _ := h.store.getVPC(ctx, req.VpcID)
	if vpc != nil {
		if aerr := h.vpcDependencyError(ctx, req.VpcID); aerr != nil {
			return nil, aerr
		}
	}
	if aerr := h.store.deleteVPC(ctx, req.VpcID); aerr != nil {
		return nil, aerr
	}
	if aerr := h.deleteRouteTablesForVPC(ctx, req.VpcID); aerr != nil {
		return nil, aerr
	}
	if vpc != nil && h.vpcStrategy != nil {
		h.vpcStrategy.OnDelete(ctx, vpc)
	}
	h.publish(ctx, events.EC2VpcDeleted, events.ResourcePayload{Name: req.VpcID})
	return &deleteVpcResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

func (h *Handler) createSubnetTyped(ctx context.Context, req *createSubnetReq) (*createSubnetResp, *protocol.AWSError) {
	if req.VpcID == "" || req.CidrBlock == "" {
		return nil, ec2err("MissingParameter", "VpcId and CidrBlock are required", http.StatusBadRequest)
	}
	if _, aerr := h.store.getVPC(ctx, req.VpcID); aerr != nil {
		return nil, ec2err("InvalidVpcID.NotFound", fmt.Sprintf("The vpc ID '%s' does not exist", req.VpcID), http.StatusBadRequest)
	}
	subnetID := fmt.Sprintf("subnet-%s", shortID())
	az := req.AvailabilityZone
	if az == "" {
		az = h.cfg.Region + "a"
	}
	subnet := &Subnet{SubnetID: subnetID, VpcID: req.VpcID, CidrBlock: req.CidrBlock, AvailabilityZone: az, State: "available"}
	if aerr := h.store.putSubnet(ctx, subnet); aerr != nil {
		return nil, aerr
	}
	h.publish(ctx, events.EC2SubnetCreated, events.ResourcePayload{Name: subnetID})
	return &createSubnetResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Subnet: typedSubnetXML{
			SubnetID:                subnetID,
			State:                   "available",
			VpcID:                   req.VpcID,
			CidrBlock:               req.CidrBlock,
			AvailabilityZone:        az,
			AvailableIPAddressCount: 251,
			DefaultForAz:            false,
			MapPublicIPOnLaunch:     false,
		},
	}, nil
}

func (h *Handler) deleteSubnetTyped(ctx context.Context, req *deleteSubnetReq) (*deleteSubnetResp, *protocol.AWSError) {
	if req.SubnetID == "" {
		return nil, ec2err("MissingParameter", "SubnetId is required", http.StatusBadRequest)
	}
	if aerr := h.subnetDependencyError(ctx, req.SubnetID); aerr != nil {
		return nil, aerr
	}
	if aerr := h.store.deleteSubnet(ctx, req.SubnetID); aerr != nil {
		return nil, aerr
	}
	h.publish(ctx, events.EC2SubnetDeleted, events.ResourcePayload{Name: req.SubnetID})
	return &deleteSubnetResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

func (h *Handler) createSecurityGroupTyped(ctx context.Context, req *createSecurityGroupReq) (*createSGResp, *protocol.AWSError) {
	if req.GroupName == "" {
		return nil, ec2err("MissingParameter", "GroupName is required", http.StatusBadRequest)
	}
	groupID := fmt.Sprintf("sg-%s", shortID())
	sg := &SecurityGroup{
		GroupID:     groupID,
		GroupName:   req.GroupName,
		Description: req.GroupDescription,
		VpcID:       req.VpcID,
		IpPermissionsEgress: []IpPermission{{
			IpProtocol: "-1",
			IpRanges:   []IpRange{{CidrIp: "0.0.0.0/0"}},
		}},
	}
	if aerr := h.store.putSecurityGroup(ctx, sg); aerr != nil {
		return nil, aerr
	}
	h.publish(ctx, events.EC2SecurityGroupCreated, events.ResourcePayload{Name: req.GroupName})
	return &createSGResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
		GroupID:   groupID,
	}, nil
}

func (h *Handler) deleteSecurityGroupTyped(ctx context.Context, req *deleteSecurityGroupReq) (*deleteSGResp, *protocol.AWSError) {
	if req.GroupID == "" {
		return nil, ec2err("MissingParameter", "GroupId is required", http.StatusBadRequest)
	}
	sg, aerr := h.store.getSecurityGroup(ctx, req.GroupID)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := h.securityGroupDependencyError(ctx, sg); aerr != nil {
		return nil, aerr
	}
	if aerr := h.store.deleteSecurityGroup(ctx, req.GroupID); aerr != nil {
		return nil, aerr
	}
	h.publish(ctx, events.EC2SecurityGroupDeleted, events.ResourcePayload{Name: req.GroupID})
	return &deleteSGResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

func (h *Handler) authorizeSGIngressTyped(ctx context.Context, req *authorizeSGIngressReq) (*authorizeSGIngressResp, *protocol.AWSError) {
	if req.GroupID == "" {
		return nil, ec2err("MissingParameter", "GroupId is required", http.StatusBadRequest)
	}
	if len(req.IpPermissions) == 0 {
		return nil, ec2err("MissingParameter", "IpPermissions are required", http.StatusBadRequest)
	}
	sg, aerr := h.store.getSecurityGroup(ctx, req.GroupID)
	if aerr != nil {
		return nil, aerr
	}
	sg.IpPermissions = append(sg.IpPermissions, req.IpPermissions...)
	if aerr := h.store.putSecurityGroup(ctx, sg); aerr != nil {
		return nil, aerr
	}
	return &authorizeSGIngressResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

func (h *Handler) authorizeSGEgressTyped(ctx context.Context, req *authorizeSGEgressReq) (*authorizeSGEgressResp, *protocol.AWSError) {
	if req.GroupID == "" {
		return nil, ec2err("MissingParameter", "GroupId is required", http.StatusBadRequest)
	}
	if len(req.IpPermissions) == 0 {
		return nil, ec2err("MissingParameter", "IpPermissions are required", http.StatusBadRequest)
	}
	sg, aerr := h.store.getSecurityGroup(ctx, req.GroupID)
	if aerr != nil {
		return nil, aerr
	}
	sg.IpPermissionsEgress = append(sg.IpPermissionsEgress, req.IpPermissions...)
	if aerr := h.store.putSecurityGroup(ctx, sg); aerr != nil {
		return nil, aerr
	}
	return &authorizeSGEgressResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

func (h *Handler) revokeSGIngressTyped(ctx context.Context, req *revokeSGIngressReq) (*revokeSGIngressResp, *protocol.AWSError) {
	if req.GroupID == "" {
		return nil, ec2err("MissingParameter", "GroupId is required", http.StatusBadRequest)
	}
	if len(req.IpPermissions) == 0 {
		return nil, ec2err("MissingParameter", "IpPermissions are required", http.StatusBadRequest)
	}
	sg, aerr := h.store.getSecurityGroup(ctx, req.GroupID)
	if aerr != nil {
		return nil, aerr
	}
	for _, revoke := range req.IpPermissions {
		if !removePermission(&sg.IpPermissions, revoke) {
			return nil, ec2err("InvalidPermission.NotFound", "The specified rule does not exist in this security group", http.StatusBadRequest)
		}
	}
	if aerr := h.store.putSecurityGroup(ctx, sg); aerr != nil {
		return nil, aerr
	}
	return &revokeSGIngressResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

func (h *Handler) revokeSGEgressTyped(ctx context.Context, req *revokeSGEgressReq) (*revokeSGEgressResp, *protocol.AWSError) {
	if req.GroupID == "" {
		return nil, ec2err("MissingParameter", "GroupId is required", http.StatusBadRequest)
	}
	if len(req.IpPermissions) == 0 {
		return nil, ec2err("MissingParameter", "IpPermissions are required", http.StatusBadRequest)
	}
	sg, aerr := h.store.getSecurityGroup(ctx, req.GroupID)
	if aerr != nil {
		return nil, aerr
	}
	for _, revoke := range req.IpPermissions {
		if !removePermission(&sg.IpPermissionsEgress, revoke) {
			return nil, ec2err("InvalidPermission.NotFound", "The specified rule does not exist in this security group", http.StatusBadRequest)
		}
	}
	if aerr := h.store.putSecurityGroup(ctx, sg); aerr != nil {
		return nil, aerr
	}
	return &revokeSGEgressResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

func (h *Handler) describeSecurityGroupsTyped(ctx context.Context, req *describeSecurityGroupsReq) (*xmlDescribeSecurityGroupsResponse, *protocol.AWSError) {
	return h.describeSecurityGroups(ctx, typedQuery(req.GroupIDs, req.Filters))
}

func (h *Handler) describeSubnetsTyped(ctx context.Context, req *describeSubnetsReq) (*xmlDescribeSubnetsResponse, *protocol.AWSError) {
	return h.describeSubnets(ctx, typedQuery(req.SubnetIDs, req.Filters))
}

func (h *Handler) runInstancesTyped(ctx context.Context, req *runInstancesReq) (*runInstancesResp, *protocol.AWSError) {
	if req.ImageID == "" {
		return nil, ec2err("MissingParameter", "ImageId is required", http.StatusBadRequest)
	}
	instanceType := req.InstanceType
	if instanceType == "" {
		instanceType = "t3.micro"
	}
	minCount := req.MinCount
	if minCount < 1 {
		minCount = 1
	}
	maxCount := req.MaxCount
	if maxCount < minCount {
		maxCount = minCount
	}
	subnetID := req.SubnetID
	tags := typedTagSpecifications(req.TagSpecifications, "instance")
	// Create-time tags are checked before anything is launched, as on AWS: a
	// rejected tag must fail the call rather than leave instances running.
	if aerr := validateTagSpecifications(tags); aerr != nil {
		return nil, aerr
	}
	// Resolve SG names for the response.
	sgRefs := make([]InstanceSG, 0, len(req.SecurityGroupIDs))
	for _, sgID := range req.SecurityGroupIDs {
		sg, _ := h.store.getSecurityGroup(ctx, sgID)
		name := ""
		if sg != nil {
			name = sg.GroupName
		}
		sgRefs = append(sgRefs, InstanceSG{GroupID: sgID, GroupName: name})
	}
	now := h.clk.Now().UTC().Format(time.RFC3339)
	az := h.cfg.Region + "a"
	resolvedVpcID := ""
	if subnetID != "" {
		if sub, aerr := h.store.getSubnet(ctx, subnetID); aerr == nil {
			resolvedVpcID = sub.VpcID
			if vpc, aerr := h.store.getVPC(ctx, sub.VpcID); aerr == nil {
				ns := vpc.NetworkStatus
				if ns == "" {
					ns = vpcNetworkStatusOK
				}
				if ns == vpcNetworkStatusConflict {
					return nil, ec2err("InvalidVpc.NetworkStatus",
						fmt.Sprintf("VPC %s has network status %q: cannot launch instances", sub.VpcID, ns),
						http.StatusBadRequest)
				}
			}
		}
	}
	instances := make([]typedInstanceXML, 0, maxCount)
	for i := 0; i < maxCount; i++ {
		instID := fmt.Sprintf("i-%s", shortID())
		apiPrivateIP, realPrivateIP, vpcID := h.allocatePrivateIPForSubnet(ctx, subnetID)
		if resolvedVpcID != "" {
			vpcID = resolvedVpcID
		}
		inst := &Instance{
			InstanceID:       instID,
			ImageID:          req.ImageID,
			InstanceType:     instanceType,
			State:            InstanceState{Code: 0, Name: "pending"},
			LaunchTime:       now,
			SubnetID:         subnetID,
			PrivateIPAddress: realPrivateIP,
			SecurityGroups:   sgRefs,
			Placement:        Placement{AvailabilityZone: az},
			VpcID:            vpcID,
		}
		if aerr := h.store.putInstance(ctx, inst); aerr != nil {
			return nil, aerr
		}
		// Create-time tags go to the tag store, the same place CreateTags
		// writes, so a later describe sees both without reading two sources.
		if aerr := h.putResourceTags(ctx, instID, tags); aerr != nil {
			return nil, aerr
		}
		id := instID
		h.scheduler.AfterScoped(h.store.region(ctx), id, "start", 0, func(bgCtx context.Context) {
			got, aerr := h.store.getInstance(bgCtx, id)
			if aerr != nil {
				return
			}
			if got.State.Code == 0 {
				got.State = InstanceState{Code: 16, Name: "running"}
				_ = h.store.putInstance(bgCtx, got)
			}
		})
		h.publish(ctx, events.EC2InstanceLaunched, events.ResourcePayload{Name: instID})
		xmlSGs := make([]typedSGRefXML, 0, len(sgRefs))
		for _, sg := range sgRefs {
			xmlSGs = append(xmlSGs, typedSGRefXML(sg))
		}
		instances = append(instances, typedInstanceXML{
			InstanceID:    instID,
			ImageID:       req.ImageID,
			InstanceState: typedInstanceStateXML{Code: 0, Name: "pending"},
			InstanceType:  instanceType,
			LaunchTime:    now,
			SubnetID:      subnetID,
			VpcID:         inst.VpcID,
			PrivateIP:     apiPrivateIP,
			Placement:     typedPlacementXML{AvailabilityZone: az},
			GroupSet:      xmlSGs,
			TagSet:        typedTagsOf(sortTags(tags)),
		})
	}
	return &runInstancesResp{
		Xmlns:         ec2XMLNS,
		RequestID:     protocol.RequestIDFromContext(ctx),
		ReservationID: fmt.Sprintf("r-%s", shortID()),
		OwnerID:       h.cfg.AccountID,
		Instances:     instances,
	}, nil
}

func (h *Handler) terminateInstancesTyped(ctx context.Context, req *terminateInstancesReq) (*terminateInstancesResp, *protocol.AWSError) {
	if len(req.InstanceIDs) == 0 {
		return nil, ec2err("MissingParameter", "InstanceId is required", http.StatusBadRequest)
	}
	changes := make([]typedInstanceStateChangeXML, 0, len(req.InstanceIDs))
	for _, id := range req.InstanceIDs {
		inst, aerr := h.store.getInstance(ctx, id)
		if aerr != nil {
			return nil, aerr
		}
		prev := typedInstanceStateXML{Code: inst.State.Code, Name: inst.State.Name}
		inst.State = InstanceState{Code: 32, Name: "shutting-down"}
		if aerr := h.store.putInstance(ctx, inst); aerr != nil {
			return nil, aerr
		}
		instID := id
		h.scheduler.AfterScoped(h.store.region(ctx), instID, "terminate", 500*time.Millisecond, func(bgCtx context.Context) {
			got, aerr := h.store.getInstance(bgCtx, instID)
			if aerr != nil {
				return
			}
			if got.State.Code == 32 {
				got.State = InstanceState{Code: 48, Name: "terminated"}
				_ = h.store.putInstance(bgCtx, got)
			}
		})
		h.publish(ctx, events.EC2InstanceTerminated, events.ResourcePayload{Name: id})
		changes = append(changes, typedInstanceStateChangeXML{
			InstanceID:    id,
			PreviousState: prev,
			CurrentState:  typedInstanceStateXML{Code: 32, Name: "shutting-down"},
		})
	}
	return &terminateInstancesResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Instances: changes,
	}, nil
}

func (h *Handler) stopInstancesTyped(ctx context.Context, req *stopInstancesReq) (*stopInstancesResp, *protocol.AWSError) {
	if len(req.InstanceIDs) == 0 {
		return nil, ec2err("MissingParameter", "InstanceId is required", http.StatusBadRequest)
	}
	changes := make([]typedInstanceStateChangeXML, 0, len(req.InstanceIDs))
	for _, id := range req.InstanceIDs {
		inst, aerr := h.store.getInstance(ctx, id)
		if aerr != nil {
			return nil, aerr
		}
		if inst.State.Code != 16 && inst.State.Code != 0 {
			return nil, ec2err("IncorrectInstanceState", fmt.Sprintf("Instance %s is not in the 'running' state", id), http.StatusBadRequest)
		}
		prev := typedInstanceStateXML{Code: inst.State.Code, Name: inst.State.Name}
		inst.State = InstanceState{Code: 64, Name: "stopping"}
		if aerr := h.store.putInstance(ctx, inst); aerr != nil {
			return nil, aerr
		}
		instID := id
		h.scheduler.AfterScoped(h.store.region(ctx), instID, "stop", 0, func(bgCtx context.Context) {
			got, aerr := h.store.getInstance(bgCtx, instID)
			if aerr != nil {
				return
			}
			if got.State.Code == 64 {
				got.State = InstanceState{Code: 80, Name: "stopped"}
				_ = h.store.putInstance(bgCtx, got)
			}
		})
		h.publish(ctx, events.EC2InstanceStopped, events.ResourcePayload{Name: id})
		changes = append(changes, typedInstanceStateChangeXML{
			InstanceID:    id,
			PreviousState: prev,
			CurrentState:  typedInstanceStateXML{Code: 64, Name: "stopping"},
		})
	}
	return &stopInstancesResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Instances: changes,
	}, nil
}

func (h *Handler) startInstancesTyped(ctx context.Context, req *startInstancesReq) (*startInstancesResp, *protocol.AWSError) {
	if len(req.InstanceIDs) == 0 {
		return nil, ec2err("MissingParameter", "InstanceId is required", http.StatusBadRequest)
	}
	changes := make([]typedInstanceStateChangeXML, 0, len(req.InstanceIDs))
	for _, id := range req.InstanceIDs {
		inst, aerr := h.store.getInstance(ctx, id)
		if aerr != nil {
			return nil, aerr
		}
		if inst.State.Code != 80 {
			return nil, ec2err("IncorrectInstanceState", fmt.Sprintf("Instance %s is not in the 'stopped' state", id), http.StatusBadRequest)
		}
		prev := typedInstanceStateXML{Code: inst.State.Code, Name: inst.State.Name}
		inst.State = InstanceState{Code: 0, Name: "pending"}
		if aerr := h.store.putInstance(ctx, inst); aerr != nil {
			return nil, aerr
		}
		instID := id
		h.scheduler.AfterScoped(h.store.region(ctx), instID, "start", 0, func(bgCtx context.Context) {
			got, aerr := h.store.getInstance(bgCtx, instID)
			if aerr != nil {
				return
			}
			if got.State.Code == 0 {
				got.State = InstanceState{Code: 16, Name: "running"}
				_ = h.store.putInstance(bgCtx, got)
			}
		})
		h.publish(ctx, events.EC2InstanceStarted, events.ResourcePayload{Name: id})
		changes = append(changes, typedInstanceStateChangeXML{
			InstanceID:    id,
			PreviousState: prev,
			CurrentState:  typedInstanceStateXML{Code: 0, Name: "pending"},
		})
	}
	return &startInstancesResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Instances: changes,
	}, nil
}

func (h *Handler) describeImagesTyped(ctx context.Context, req *describeImagesReq) (*xmlDescribeImagesResponse, *protocol.AWSError) {
	return h.describeImages(ctx, typedQuery(req.ImageIDs, req.Filters))
}

func (h *Handler) createKeyPairTyped(ctx context.Context, req *createKeyPairReq) (*createKeyPairResp, *protocol.AWSError) {
	if req.KeyName == "" {
		return nil, ec2err("MissingParameter", "KeyName is required", http.StatusBadRequest)
	}
	if _, aerr := h.store.getKeyPair(ctx, req.KeyName); aerr == nil {
		return nil, ec2err("InvalidKeyPair.Duplicate", fmt.Sprintf("The keypair '%s' already exists", req.KeyName), http.StatusBadRequest)
	}
	tags := typedTagSpecifications(req.TagSpecifications, "key-pair")
	// Create-time tags are checked before anything is created, as on AWS: a
	// rejected tag must fail the call rather than leave a key pair behind
	// (#1197 typed-path parity).
	if aerr := validateTagSpecifications(tags); aerr != nil {
		return nil, aerr
	}
	fingerprint, material := h.keyMaterialFor()
	kpID := fmt.Sprintf("key-%s", shortID())
	kp := &KeyPair{
		KeyName:        req.KeyName,
		KeyFingerprint: fingerprint,
		KeyPairID:      kpID,
		KeyMaterial:    material,
	}
	if aerr := h.store.putKeyPair(ctx, kp); aerr != nil {
		return nil, aerr
	}
	// Keyed by KeyName, matching deleteKeyPair's existing tag cleanup.
	if aerr := h.putResourceTags(ctx, req.KeyName, tags); aerr != nil {
		return nil, aerr
	}
	return &createKeyPairResp{
		Xmlns:          ec2XMLNS,
		RequestID:      protocol.RequestIDFromContext(ctx),
		KeyName:        req.KeyName,
		KeyFingerprint: fingerprint,
		KeyMaterial:    material,
		KeyPairID:      kpID,
		TagSet:         typedTagsOf(tags),
	}, nil
}

func (h *Handler) describeKeyPairsTyped(ctx context.Context, req *describeKeyPairsReq) (*xmlDescribeKeyPairsResponse, *protocol.AWSError) {
	return h.describeKeyPairs(ctx, typedQuery(req.KeyNames, req.Filters))
}

func (h *Handler) deleteKeyPairTyped(ctx context.Context, req *deleteKeyPairReq) (*deleteKeyPairResp, *protocol.AWSError) {
	if req.KeyName == "" {
		return nil, ec2err("MissingParameter", "KeyName is required", http.StatusBadRequest)
	}
	_ = h.store.deleteKeyPair(ctx, req.KeyName)
	return &deleteKeyPairResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

func (h *Handler) createRouteTableTyped(ctx context.Context, req *createRouteTableReq) (*createRouteTableResp, *protocol.AWSError) {
	if req.VpcID == "" {
		return nil, ec2err("MissingParameter", "VpcId is required", http.StatusBadRequest)
	}
	vpc, aerr := h.store.getVPC(ctx, req.VpcID)
	if aerr != nil {
		return nil, ec2err("InvalidVpcID.NotFound", fmt.Sprintf("The vpc ID '%s' does not exist", req.VpcID), http.StatusBadRequest)
	}
	rtID := fmt.Sprintf("rtb-%s", shortID())
	rt := &RouteTable{
		RouteTableID: rtID,
		VpcID:        req.VpcID,
		Routes: []Route{{
			DestinationCidrBlock: vpc.CidrBlock,
			GatewayID:            "local",
			Origin:               "CreateRouteTable",
		}},
	}
	if aerr := h.store.putRouteTable(ctx, rt); aerr != nil {
		return nil, aerr
	}
	return &createRouteTableResp{
		Xmlns:      ec2XMLNS,
		RequestID:  protocol.RequestIDFromContext(ctx),
		RouteTable: routeTableToTypedXML(rt),
	}, nil
}

func (h *Handler) describeRouteTablesTyped(ctx context.Context, req *describeRouteTablesReq) (*xmlDescribeRouteTablesResponse, *protocol.AWSError) {
	return h.describeRouteTables(ctx, typedQuery(req.RouteTableIDs, req.Filters))
}

func (h *Handler) deleteRouteTableTyped(ctx context.Context, req *deleteRouteTableReq) (*deleteRouteTableResp, *protocol.AWSError) {
	if req.RouteTableID == "" {
		return nil, ec2err("MissingParameter", "RouteTableId is required", http.StatusBadRequest)
	}
	rt, aerr := h.store.getRouteTable(ctx, req.RouteTableID)
	if aerr != nil {
		return nil, aerr
	}
	for _, assoc := range rt.Associations {
		if assoc.Main {
			return nil, ec2err("DependencyViolation", "The routeTable cannot be deleted because it is the main route table", http.StatusBadRequest)
		}
	}
	if aerr := h.store.deleteRouteTable(ctx, req.RouteTableID); aerr != nil {
		return nil, aerr
	}
	return &deleteRouteTableResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

func (h *Handler) createRouteTyped(ctx context.Context, req *createRouteReq) (*createRouteResp, *protocol.AWSError) {
	if req.RouteTableID == "" || req.DestinationCidrBlock == "" {
		return nil, ec2err("MissingParameter", "RouteTableId and DestinationCidrBlock are required", http.StatusBadRequest)
	}
	rt, aerr := h.store.getRouteTable(ctx, req.RouteTableID)
	if aerr != nil {
		return nil, aerr
	}
	rt.Routes = append(rt.Routes, Route{
		DestinationCidrBlock: req.DestinationCidrBlock,
		GatewayID:            req.GatewayID,
		NatGatewayID:         req.NatGatewayID,
		Origin:               "CreateRoute",
	})
	if aerr := h.store.putRouteTable(ctx, rt); aerr != nil {
		return nil, aerr
	}
	return &createRouteResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

func (h *Handler) deleteRouteTyped(ctx context.Context, req *deleteRouteReq) (*deleteRouteResp, *protocol.AWSError) {
	if req.RouteTableID == "" || req.DestinationCidrBlock == "" {
		return nil, ec2err("MissingParameter", "RouteTableId and DestinationCidrBlock are required", http.StatusBadRequest)
	}
	rt, aerr := h.store.getRouteTable(ctx, req.RouteTableID)
	if aerr != nil {
		return nil, aerr
	}
	found := false
	routes := make([]Route, 0, len(rt.Routes))
	for _, route := range rt.Routes {
		if route.DestinationCidrBlock == req.DestinationCidrBlock {
			found = true
			continue
		}
		routes = append(routes, route)
	}
	if !found {
		return nil, ec2err("InvalidRoute.NotFound", fmt.Sprintf("no route with destination-cidr-block %s in route table %s", req.DestinationCidrBlock, req.RouteTableID), http.StatusBadRequest)
	}
	rt.Routes = routes
	if aerr := h.store.putRouteTable(ctx, rt); aerr != nil {
		return nil, aerr
	}
	return &deleteRouteResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

func (h *Handler) associateRouteTableTyped(ctx context.Context, req *associateRouteTableReq) (*associateRouteTableResp, *protocol.AWSError) {
	if req.RouteTableID == "" || req.SubnetID == "" {
		return nil, ec2err("MissingParameter", "RouteTableId and SubnetId are required", http.StatusBadRequest)
	}
	rt, aerr := h.store.getRouteTable(ctx, req.RouteTableID)
	if aerr != nil {
		return nil, aerr
	}
	assocID := fmt.Sprintf("rtbassoc-%s", shortID())
	rt.Associations = append(rt.Associations, RouteTableAssociation{
		AssociationID: assocID,
		RouteTableID:  req.RouteTableID,
		SubnetID:      req.SubnetID,
		Main:          false,
	})
	if aerr := h.store.putRouteTable(ctx, rt); aerr != nil {
		return nil, aerr
	}
	return &associateRouteTableResp{
		Xmlns:         ec2XMLNS,
		RequestID:     protocol.RequestIDFromContext(ctx),
		AssociationID: assocID,
	}, nil
}

func (h *Handler) disassociateRouteTableTyped(ctx context.Context, req *disassociateRouteTableReq) (*disassociateRouteTableResp, *protocol.AWSError) {
	if req.AssociationID == "" {
		return nil, ec2err("MissingParameter", "AssociationId is required", http.StatusBadRequest)
	}
	all, aerr := h.store.listRouteTables(ctx)
	if aerr != nil {
		return nil, aerr
	}
	for _, rt := range all {
		for i, assoc := range rt.Associations {
			if assoc.AssociationID == req.AssociationID {
				if assoc.Main {
					return nil, ec2err("InvalidParameterValue", "Cannot disassociate the main route table association", http.StatusBadRequest)
				}
				rt.Associations = append(rt.Associations[:i], rt.Associations[i+1:]...)
				if aerr := h.store.putRouteTable(ctx, rt); aerr != nil {
					return nil, aerr
				}
				return &disassociateRouteTableResp{
					Xmlns:     ec2XMLNS,
					RequestID: protocol.RequestIDFromContext(ctx),
					Return:    true,
				}, nil
			}
		}
	}
	return nil, ec2err("InvalidAssociationID.NotFound", fmt.Sprintf("The association ID '%s' does not exist", req.AssociationID), http.StatusBadRequest)
}

func (h *Handler) createIGWTyped(ctx context.Context, _ *createIGWReq) (*createIGWResp, *protocol.AWSError) {
	igwID := fmt.Sprintf("igw-%s", shortID())
	igw := &InternetGateway{InternetGatewayID: igwID}
	if aerr := h.store.putInternetGateway(ctx, igw); aerr != nil {
		return nil, aerr
	}
	return &createIGWResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		InternetGateway: typedIGWXML{
			InternetGatewayID: igwID,
		},
	}, nil
}

func (h *Handler) describeIGWsTyped(ctx context.Context, req *describeIGWsReq) (*xmlDescribeInternetGatewaysResponse, *protocol.AWSError) {
	return h.describeInternetGateways(ctx, typedQuery(req.InternetGatewayIDs, req.Filters))
}

func (h *Handler) deleteIGWTyped(ctx context.Context, req *deleteIGWReq) (*deleteIGWResp, *protocol.AWSError) {
	if req.InternetGatewayID == "" {
		return nil, ec2err("MissingParameter", "InternetGatewayId is required", http.StatusBadRequest)
	}
	igw, aerr := h.store.getInternetGateway(ctx, req.InternetGatewayID)
	if aerr != nil {
		return nil, aerr
	}
	if len(igw.Attachments) > 0 {
		return nil, ec2err("DependencyViolation", fmt.Sprintf("The internetGateway '%s' has dependencies and cannot be deleted", req.InternetGatewayID), http.StatusBadRequest)
	}
	if aerr := h.store.deleteInternetGateway(ctx, req.InternetGatewayID); aerr != nil {
		return nil, aerr
	}
	return &deleteIGWResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

func (h *Handler) attachIGWTyped(ctx context.Context, req *attachIGWReq) (*attachIGWResp, *protocol.AWSError) {
	if req.InternetGatewayID == "" || req.VpcID == "" {
		return nil, ec2err("MissingParameter", "InternetGatewayId and VpcId are required", http.StatusBadRequest)
	}
	igw, aerr := h.store.getInternetGateway(ctx, req.InternetGatewayID)
	if aerr != nil {
		return nil, aerr
	}
	if _, aerr := h.store.getVPC(ctx, req.VpcID); aerr != nil {
		return nil, ec2err("InvalidVpcID.NotFound", fmt.Sprintf("The vpc ID '%s' does not exist", req.VpcID), http.StatusBadRequest)
	}
	for _, att := range igw.Attachments {
		if att.VpcID == req.VpcID {
			return nil, ec2err("Resource.AlreadyAssociated", fmt.Sprintf("The internetGateway '%s' is already attached to vpc '%s'", req.InternetGatewayID, req.VpcID), http.StatusBadRequest)
		}
	}
	igw.Attachments = append(igw.Attachments, IGWAttachment{VpcID: req.VpcID, State: "attached"})
	if aerr := h.store.putInternetGateway(ctx, igw); aerr != nil {
		return nil, aerr
	}
	if h.vpcStrategy != nil {
		h.vpcStrategy.SetInternal(ctx, req.VpcID, false)
	}
	return &attachIGWResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

func (h *Handler) detachIGWTyped(ctx context.Context, req *detachIGWReq) (*detachIGWResp, *protocol.AWSError) {
	if req.InternetGatewayID == "" || req.VpcID == "" {
		return nil, ec2err("MissingParameter", "InternetGatewayId and VpcId are required", http.StatusBadRequest)
	}
	igw, aerr := h.store.getInternetGateway(ctx, req.InternetGatewayID)
	if aerr != nil {
		return nil, aerr
	}
	found := false
	for i, att := range igw.Attachments {
		if att.VpcID == req.VpcID {
			igw.Attachments = append(igw.Attachments[:i], igw.Attachments[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		return nil, ec2err("Gateway.NotAttached", fmt.Sprintf("The internetGateway '%s' is not attached to vpc '%s'", req.InternetGatewayID, req.VpcID), http.StatusBadRequest)
	}
	if aerr := h.store.putInternetGateway(ctx, igw); aerr != nil {
		return nil, aerr
	}
	if h.vpcStrategy != nil {
		h.vpcStrategy.SetInternal(ctx, req.VpcID, true)
	}
	return &detachIGWResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

func (h *Handler) createVPCPeeringTyped(ctx context.Context, req *createVPCPeeringReq) (*createVPCPeeringResp, *protocol.AWSError) {
	if req.VpcID == "" || req.PeerVpcID == "" {
		return nil, ec2err("MissingParameter", "VpcId and PeerVpcId are required", http.StatusBadRequest)
	}
	requesterVpc, aerr := h.store.getVPC(ctx, req.VpcID)
	if aerr != nil {
		return nil, ec2err("InvalidVpcID.NotFound", fmt.Sprintf("The vpc ID '%s' does not exist", req.VpcID), http.StatusBadRequest)
	}
	accepterVpc, aerr := h.store.getVPC(ctx, req.PeerVpcID)
	if aerr != nil {
		return nil, ec2err("InvalidVpcID.NotFound", fmt.Sprintf("The vpc ID '%s' does not exist", req.PeerVpcID), http.StatusBadRequest)
	}
	tags := typedTagSpecifications(req.TagSpecifications, "vpc-peering-connection")
	// Create-time tags are checked before anything is created, as on AWS: a
	// rejected tag must fail the call rather than leave a connection behind
	// (#1197 typed-path parity).
	if aerr := validateTagSpecifications(tags); aerr != nil {
		return nil, aerr
	}
	region := h.store.region(ctx)
	ownerID := h.cfg.AccountID
	pcxID := fmt.Sprintf("pcx-%s", shortID())
	pcx := &VpcPeeringConnection{
		VpcPeeringConnectionID: pcxID,
		RequesterVpcInfo: VpcPeeringConnectionVpcInfo{
			VpcID: requesterVpc.VpcID, OwnerID: ownerID, CidrBlock: requesterVpc.CidrBlock, Region: region,
		},
		AccepterVpcInfo: VpcPeeringConnectionVpcInfo{
			VpcID: accepterVpc.VpcID, OwnerID: ownerID, CidrBlock: accepterVpc.CidrBlock, Region: region,
		},
		Status: VpcPeeringConnectionStatus{Code: "pending-acceptance", Message: fmt.Sprintf("Initiating Request to %s", ownerID)},
	}
	if aerr := h.store.putVpcPeeringConnection(ctx, pcx); aerr != nil {
		return nil, aerr
	}
	if aerr := h.putResourceTags(ctx, pcxID, tags); aerr != nil {
		return nil, aerr
	}
	return &createVPCPeeringResp{
		Xmlns:                ec2XMLNS,
		RequestID:            protocol.RequestIDFromContext(ctx),
		VpcPeeringConnection: pcxToTypedXML(pcx, tags),
	}, nil
}

func (h *Handler) acceptVPCPeeringTyped(ctx context.Context, req *acceptVPCPeeringReq) (*acceptVPCPeeringResp, *protocol.AWSError) {
	if req.VpcPeeringConnectionID == "" {
		return nil, ec2err("MissingParameter", "VpcPeeringConnectionId is required", http.StatusBadRequest)
	}
	pcx, aerr := h.store.getVpcPeeringConnection(ctx, req.VpcPeeringConnectionID)
	if aerr != nil {
		return nil, aerr
	}
	if pcx.Status.Code != "pending-acceptance" {
		return nil, ec2err("InvalidStateTransition", fmt.Sprintf("The peering connection '%s' is in state '%s' and cannot be accepted", req.VpcPeeringConnectionID, pcx.Status.Code), http.StatusBadRequest)
	}
	pcx.Status = VpcPeeringConnectionStatus{Code: "active", Message: "Active"}
	if aerr := h.store.putVpcPeeringConnection(ctx, pcx); aerr != nil {
		return nil, aerr
	}
	tags, aerr := h.store.getTags(ctx, req.VpcPeeringConnectionID)
	if aerr != nil {
		return nil, aerr
	}
	return &acceptVPCPeeringResp{
		Xmlns:                ec2XMLNS,
		RequestID:            protocol.RequestIDFromContext(ctx),
		VpcPeeringConnection: pcxToTypedXML(pcx, sortedTags(tags)),
	}, nil
}

func (h *Handler) describeVPCPeeringsTyped(ctx context.Context, req *describeVPCPeeringsReq) (*xmlDescribeVpcPeeringConnectionsResponse, *protocol.AWSError) {
	return h.describeVpcPeeringConnections(ctx, typedQuery(req.VpcPeeringConnectionIDs, req.Filters))
}

func (h *Handler) deleteVPCPeeringTyped(ctx context.Context, req *deleteVPCPeeringReq) (*deleteVPCPeeringResp, *protocol.AWSError) {
	if req.VpcPeeringConnectionID == "" {
		return nil, ec2err("MissingParameter", "VpcPeeringConnectionId is required", http.StatusBadRequest)
	}
	pcx, aerr := h.store.getVpcPeeringConnection(ctx, req.VpcPeeringConnectionID)
	if aerr != nil {
		return nil, aerr
	}
	switch pcx.Status.Code {
	case "active", "pending-acceptance":
	default:
		return nil, ec2err("InvalidStateTransition", fmt.Sprintf("The peering connection '%s' is in state '%s' and cannot be deleted", req.VpcPeeringConnectionID, pcx.Status.Code), http.StatusBadRequest)
	}
	pcx.Status = VpcPeeringConnectionStatus{Code: "deleted", Message: "Deleted"}
	if aerr := h.store.putVpcPeeringConnection(ctx, pcx); aerr != nil {
		return nil, aerr
	}
	return &deleteVPCPeeringResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

func (h *Handler) createTagsTyped(ctx context.Context, req *createTagsReq) (*createTagsResp, *protocol.AWSError) {
	if len(req.ResourceIDs) == 0 {
		return nil, ec2err("MissingParameter", "ResourceId is required", http.StatusBadRequest)
	}
	// The same merge-and-validate the Query path uses, so the two wire paths
	// cannot come to different conclusions about what a tag write means.
	tags := make([]Tag, 0, len(req.Tags))
	for _, tag := range req.Tags {
		if tag.Key != "" {
			tags = append(tags, Tag(tag))
		}
	}
	for _, rid := range req.ResourceIDs {
		if aerr := h.putResourceTags(ctx, rid, tags); aerr != nil {
			return nil, aerr
		}
	}
	return &createTagsResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

func (h *Handler) deleteTagsTyped(ctx context.Context, req *deleteTagsReq) (*deleteTagsResp, *protocol.AWSError) {
	// The same key-only match the Query path uses (collectFormTags, tags.go):
	// a Tag.N entry deletes by key regardless of the value sent alongside it.
	keys := make(map[string]bool, len(req.Tags))
	for _, t := range req.Tags {
		keys[t.Key] = true
	}
	for _, rid := range req.ResourceIDs {
		existing, _ := h.store.getTags(ctx, rid)
		if existing == nil {
			continue
		}
		for k := range keys {
			delete(existing, k)
		}
		if aerr := h.store.putTags(ctx, rid, existing); aerr != nil {
			return nil, aerr
		}
	}
	return &deleteTagsResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

func (h *Handler) describeTagsTyped(ctx context.Context, req *describeTagsReq) (*xmlDescribeTagsResponse, *protocol.AWSError) {
	return h.describeTags(ctx, typedQuery(nil, req.Filters))
}

func (h *Handler) allocateAddressTyped(ctx context.Context, req *allocateAddressReq) (*allocateAddressResp, *protocol.AWSError) {
	allocID := fmt.Sprintf("eipalloc-%s", shortID())
	ip := fmt.Sprintf("203.0.113.%d", syntheticIPCounter.Add(1)%254+1)
	domain := req.Domain
	if domain == "" {
		domain = "vpc"
	}
	addr := &ElasticIP{
		AllocationID: allocID,
		PublicIP:     ip,
		Domain:       domain,
	}
	if aerr := h.store.putElasticIP(ctx, addr); aerr != nil {
		return nil, aerr
	}
	return &allocateAddressResp{
		Xmlns:        ec2XMLNS,
		RequestID:    protocol.RequestIDFromContext(ctx),
		PublicIP:     addr.PublicIP,
		AllocationID: addr.AllocationID,
		Domain:       addr.Domain,
	}, nil
}

func (h *Handler) releaseAddressTyped(ctx context.Context, req *releaseAddressReq) (*releaseAddressResp, *protocol.AWSError) {
	if req.AllocationID == "" {
		return nil, ec2err("MissingParameter", "AllocationId is required", http.StatusBadRequest)
	}
	if aerr := h.store.deleteElasticIP(ctx, req.AllocationID); aerr != nil {
		return nil, aerr
	}
	return &releaseAddressResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

func (h *Handler) describeAddressesTyped(ctx context.Context, req *describeAddressesReq) (*xmlDescribeAddressesResponse, *protocol.AWSError) {
	return h.describeAddresses(ctx, typedQuery(req.AllocationIDs, req.Filters))
}

func (h *Handler) associateAddressTyped(ctx context.Context, req *associateAddressReq) (*associateAddressResp, *protocol.AWSError) {
	if req.AllocationID == "" {
		return nil, ec2err("MissingParameter", "AllocationId is required", http.StatusBadRequest)
	}
	addr, aerr := h.store.getElasticIP(ctx, req.AllocationID)
	if aerr != nil {
		return nil, aerr
	}
	assocID := fmt.Sprintf("eipassoc-%s", shortID())
	addr.AssociationID = assocID
	addr.InstanceID = req.InstanceID
	if aerr := h.store.putElasticIP(ctx, addr); aerr != nil {
		return nil, aerr
	}
	return &associateAddressResp{
		Xmlns:         ec2XMLNS,
		RequestID:     protocol.RequestIDFromContext(ctx),
		Return:        true,
		AssociationID: assocID,
	}, nil
}

func (h *Handler) disassociateAddressTyped(ctx context.Context, req *disassociateAddressReq) (*disassociateAddressResp, *protocol.AWSError) {
	if req.AssociationID == "" {
		return nil, ec2err("MissingParameter", "AssociationId is required", http.StatusBadRequest)
	}
	all, aerr := h.store.listElasticIPs(ctx)
	if aerr != nil {
		return nil, aerr
	}
	for _, a := range all {
		if a.AssociationID == req.AssociationID {
			a.AssociationID = ""
			a.InstanceID = ""
			a.NetworkInterfaceID = ""
			a.PrivateIP = ""
			if aerr := h.store.putElasticIP(ctx, a); aerr != nil {
				return nil, aerr
			}
			break
		}
	}
	return &disassociateAddressResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

func (h *Handler) createNatGatewayTyped(ctx context.Context, req *createNatGatewayReq) (*createNatGatewayResp, *protocol.AWSError) {
	if req.SubnetID == "" {
		return nil, ec2err("MissingParameter", "SubnetId is required", http.StatusBadRequest)
	}
	// Create-time tags are checked before anything is created, as on AWS: a
	// rejected tag must fail the call rather than leave a NAT gateway behind.
	tags := typedTagSpecifications(req.TagSpecifications, "natgateway")
	if aerr := validateTagSpecifications(tags); aerr != nil {
		return nil, aerr
	}
	sub, aerr := h.store.getSubnet(ctx, req.SubnetID)
	if aerr != nil {
		return nil, ec2err("InvalidSubnetID.NotFound", fmt.Sprintf("The subnet ID '%s' does not exist", req.SubnetID), http.StatusBadRequest)
	}
	var publicIP string
	if req.AllocationID != "" {
		eip, aerr := h.store.getElasticIP(ctx, req.AllocationID)
		if aerr == nil {
			publicIP = eip.PublicIP
		}
	}
	natID := fmt.Sprintf("nat-%s", shortID())
	now := h.clk.Now().Format("2006-01-02T15:04:05.000Z")
	ngw := &NatGateway{
		NatGatewayID: natID,
		SubnetID:     req.SubnetID,
		VpcID:        sub.VpcID,
		State:        "available",
		AllocationID: req.AllocationID,
		PublicIP:     publicIP,
		CreateTime:   now,
	}
	privateIP, _, _ := h.allocatePrivateIPForSubnet(ctx, req.SubnetID)
	ngw.PrivateIP = privateIP
	if aerr := h.store.putNatGateway(ctx, ngw); aerr != nil {
		return nil, aerr
	}
	// Create-time tags go to the tag store, the same place CreateTags writes,
	// so a later describe sees both without having to read two sources.
	if aerr := h.putResourceTags(ctx, natID, tags); aerr != nil {
		return nil, aerr
	}
	addrs := []typedNatGWAddrXML{{AllocationID: req.AllocationID, PublicIP: publicIP, PrivateIP: ngw.PrivateIP}}
	return &createNatGatewayResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		NatGateway: typedNatGatewayXML{
			NatGatewayID: natID,
			SubnetID:     req.SubnetID,
			VpcID:        sub.VpcID,
			State:        "available",
			CreateTime:   now,
			Addresses:    addrs,
			Tags:         typedResourceTagsOf(sortTags(tags)),
		},
	}, nil
}

func (h *Handler) describeNatGatewaysTyped(ctx context.Context, req *describeNatGatewaysReq) (*xmlDescribeNatGatewaysResponse, *protocol.AWSError) {
	return h.describeNatGateways(ctx, typedQuery(req.NatGatewayIDs, req.Filters))
}

func (h *Handler) deleteNatGatewayTyped(ctx context.Context, req *deleteNatGatewayReq) (*deleteNatGatewayResp, *protocol.AWSError) {
	if req.NatGatewayID == "" {
		return nil, ec2err("MissingParameter", "NatGatewayId is required", http.StatusBadRequest)
	}
	ngw, aerr := h.store.getNatGateway(ctx, req.NatGatewayID)
	if aerr != nil {
		return nil, aerr
	}
	ngw.State = "deleted"
	_ = h.store.putNatGateway(ctx, ngw)
	_ = h.store.deleteNatGateway(ctx, req.NatGatewayID)
	return &deleteNatGatewayResp{
		Xmlns:        ec2XMLNS,
		RequestID:    protocol.RequestIDFromContext(ctx),
		NatGatewayID: req.NatGatewayID,
	}, nil
}

func (h *Handler) modifySubnetAttributeTyped(ctx context.Context, req *modifySubnetAttributeReq) (*modifySubnetAttributeResp, *protocol.AWSError) {
	if req.SubnetID == "" {
		return nil, ec2err("MissingParameter", "SubnetId is required", http.StatusBadRequest)
	}
	sub, aerr := h.store.getSubnet(ctx, req.SubnetID)
	if aerr != nil {
		return nil, aerr
	}
	if req.MapPublicIpOnLaunch != nil {
		sub.MapPublicIpOnLaunch = req.MapPublicIpOnLaunch.Value
		if aerr := h.store.putSubnet(ctx, sub); aerr != nil {
			return nil, aerr
		}
	}
	return &modifySubnetAttributeResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

func (h *Handler) modifyVpcAttributeTyped(ctx context.Context, req *modifyVpcAttributeReq) (*modifyVpcAttributeResp, *protocol.AWSError) {
	if req.VpcID == "" {
		return nil, ec2err("MissingParameter", "VpcId is required", http.StatusBadRequest)
	}
	vpc, aerr := h.store.getVPC(ctx, req.VpcID)
	if aerr != nil {
		return nil, aerr
	}
	changed := false
	if req.EnableDnsSupport != nil {
		vpc.EnableDnsSupport = req.EnableDnsSupport.Value
		changed = true
	}
	if req.EnableDnsHostnames != nil {
		vpc.EnableDnsHostnames = req.EnableDnsHostnames.Value
		changed = true
	}
	if changed {
		if aerr := h.store.putVPC(ctx, vpc); aerr != nil {
			return nil, aerr
		}
	}
	return &modifyVpcAttributeResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

// describeVpcAttributeTyped had answered `true` for both DNS attributes
// whatever the VPC held, which is the shape #1144 fixed on the legacy path
// alone: the CloudFormation provisioner's ModifyVpcAttribute write was
// readable only through the legacy handler. Both paths now read the store.
func (h *Handler) describeVpcAttributeTyped(ctx context.Context, req *describeVpcAttributeReq) (*xmlDescribeVpcAttributeResponse, *protocol.AWSError) {
	return h.describeVpcAttribute(ctx, req.VpcID, req.Attribute)
}

func (h *Handler) describeDhcpOptionsTyped(ctx context.Context, req *describeDhcpOptionsReq) (*xmlDescribeDhcpOptionsResponse, *protocol.AWSError) {
	return h.describeDhcpOptions(ctx, typedQuery(nil, req.Filters))
}

func (h *Handler) describeAccountAttributesTyped(ctx context.Context, _ *describeAccountAttributesReq) (*xmlDescribeAccountAttributesResponse, *protocol.AWSError) {
	return h.describeAccountAttributes(ctx)
}

func (h *Handler) createVpnGatewayTyped(ctx context.Context, req *createVpnGatewayReq) (*createVpnGatewayResp, *protocol.AWSError) {
	if req.Type == "" {
		return nil, ec2err("MissingParameter", "Type is required", http.StatusBadRequest)
	}
	if req.Type != "ipsec.1" {
		return nil, ec2err("InvalidParameterValue", "Type must be ipsec.1", http.StatusBadRequest)
	}
	asn, aerr := parseAmazonSideAsn(req.AmazonSideAsn)
	if aerr != nil {
		return nil, aerr
	}
	// Create-time tags are checked before anything is created, as on AWS: a
	// rejected tag must fail the call rather than leave a gateway behind.
	tags := typedTagSpecifications(req.TagSpecifications, "vpn-gateway")
	if aerr := validateTagSpecifications(tags); aerr != nil {
		return nil, aerr
	}
	vgw := &VpnGateway{
		VpnGatewayID:     fmt.Sprintf("vgw-%s", shortID()),
		State:            "available",
		Type:             req.Type,
		AmazonSideAsn:    asn,
		AvailabilityZone: req.AvailabilityZone,
	}
	if aerr := h.store.putVpnGateway(ctx, vgw); aerr != nil {
		return nil, aerr
	}
	// Create-time tags go to the tag store, the same place CreateTags writes,
	// so a later describe sees both without having to read two sources.
	if aerr := h.putResourceTags(ctx, vgw.VpnGatewayID, tags); aerr != nil {
		return nil, aerr
	}
	return &createVpnGatewayResp{
		Xmlns:      ec2XMLNS,
		RequestID:  protocol.RequestIDFromContext(ctx),
		VpnGateway: vpnGatewayToTypedXML(vgw, sortTags(tags)),
	}, nil
}

func (h *Handler) attachVpnGatewayTyped(ctx context.Context, req *attachVpnGatewayReq) (*attachVpnGatewayResp, *protocol.AWSError) {
	if req.VpnGatewayID == "" || req.VpcID == "" {
		return nil, ec2err("MissingParameter", "VpnGatewayId and VpcId are required", http.StatusBadRequest)
	}
	vgw, aerr := h.store.getVpnGateway(ctx, req.VpnGatewayID)
	if aerr != nil {
		return nil, aerr
	}
	if _, aerr := h.store.getVPC(ctx, req.VpcID); aerr != nil {
		return nil, ec2err("InvalidVpcID.NotFound", fmt.Sprintf("The vpc ID '%s' does not exist", req.VpcID), http.StatusBadRequest)
	}
	for _, att := range vgw.Attachments {
		if att.VpcID == req.VpcID {
			return nil, ec2err("Resource.AlreadyAssociated", fmt.Sprintf("The vpnGateway '%s' is already attached to vpc '%s'", req.VpnGatewayID, req.VpcID), http.StatusBadRequest)
		}
	}
	if len(vgw.Attachments) > 0 {
		return nil, ec2err("Resource.AlreadyAssociated", fmt.Sprintf("The vpnGateway '%s' is already attached", req.VpnGatewayID), http.StatusBadRequest)
	}
	vgw.Attachments = append(vgw.Attachments, VpnGatewayAttachment{VpcID: req.VpcID, State: "attached"})
	if aerr := h.store.putVpnGateway(ctx, vgw); aerr != nil {
		return nil, aerr
	}
	return &attachVpnGatewayResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Attachment: typedVpnGatewayAttachmentXML{
			VpcID: req.VpcID,
			State: "attached",
		},
	}, nil
}

func (h *Handler) describeVpnGatewaysTyped(ctx context.Context, req *describeVpnGatewaysReq) (*xmlDescribeVpnGatewaysResponse, *protocol.AWSError) {
	return h.describeVpnGateways(ctx, typedQuery(req.VpnGatewayIDs, req.Filters))
}

func (h *Handler) detachVpnGatewayTyped(ctx context.Context, req *detachVpnGatewayReq) (*detachVpnGatewayResp, *protocol.AWSError) {
	if req.VpnGatewayID == "" || req.VpcID == "" {
		return nil, ec2err("MissingParameter", "VpnGatewayId and VpcId are required", http.StatusBadRequest)
	}
	vgw, aerr := h.store.getVpnGateway(ctx, req.VpnGatewayID)
	if aerr != nil {
		return nil, aerr
	}
	found := false
	for i, att := range vgw.Attachments {
		if att.VpcID == req.VpcID {
			vgw.Attachments = append(vgw.Attachments[:i], vgw.Attachments[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		return nil, ec2err("Gateway.NotAttached", fmt.Sprintf("The vpnGateway '%s' is not attached to vpc '%s'", req.VpnGatewayID, req.VpcID), http.StatusBadRequest)
	}
	if aerr := h.store.putVpnGateway(ctx, vgw); aerr != nil {
		return nil, aerr
	}
	return &detachVpnGatewayResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

func (h *Handler) deleteVpnGatewayTyped(ctx context.Context, req *deleteVpnGatewayReq) (*deleteVpnGatewayResp, *protocol.AWSError) {
	if req.VpnGatewayID == "" {
		return nil, ec2err("MissingParameter", "VpnGatewayId is required", http.StatusBadRequest)
	}
	vgw, aerr := h.store.getVpnGateway(ctx, req.VpnGatewayID)
	if aerr != nil {
		return nil, aerr
	}
	if len(vgw.Attachments) > 0 {
		return nil, ec2err("DependencyViolation", fmt.Sprintf("The vpnGateway '%s' has dependencies and cannot be deleted", req.VpnGatewayID), http.StatusBadRequest)
	}
	if aerr := h.store.deleteVpnGateway(ctx, req.VpnGatewayID); aerr != nil {
		return nil, aerr
	}
	return &deleteVpnGatewayResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

func (h *Handler) createNetworkInterfaceTyped(ctx context.Context, req *createNetworkInterfaceReq) (*createNetworkInterfaceResp, *protocol.AWSError) {
	if req.SubnetID == "" {
		return nil, ec2err("MissingParameter", "SubnetId is required", http.StatusBadRequest)
	}
	sub, aerr := h.store.getSubnet(ctx, req.SubnetID)
	if aerr != nil {
		return nil, aerr
	}
	if vpc, aerr := h.store.getVPC(ctx, sub.VpcID); aerr == nil {
		ns := vpc.NetworkStatus
		if ns == "" {
			ns = vpcNetworkStatusOK
		}
		if ns == vpcNetworkStatusConflict {
			return nil, ec2err("InvalidVpc.NetworkStatus",
				fmt.Sprintf("VPC %s has network status %q: cannot create network interface", sub.VpcID, ns),
				http.StatusBadRequest)
		}
	}
	eniID := fmt.Sprintf("eni-%s", shortID())
	apiPrivateIP, realPrivateIP, _ := h.allocatePrivateIPForSubnet(ctx, req.SubnetID)
	mac := fmt.Sprintf("02:%s:%s:%s:%s:%s", shortID()[:2], shortID()[:2], shortID()[:2], shortID()[:2], shortID()[:2])
	eni := &NetworkInterface{
		NetworkInterfaceID: eniID,
		SubnetID:           req.SubnetID,
		VpcID:              sub.VpcID,
		AvailabilityZone:   sub.AvailabilityZone,
		Description:        req.Description,
		PrivateIPAddress:   realPrivateIP,
		Status:             "available",
		MacAddress:         mac,
	}
	if aerr := h.store.putNetworkInterface(ctx, eni); aerr != nil {
		return nil, aerr
	}
	return &createNetworkInterfaceResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		NetworkInterface: typedNetworkInterfaceXML{
			NetworkInterfaceID: eniID,
			SubnetID:           req.SubnetID,
			VpcID:              sub.VpcID,
			AvailabilityZone:   sub.AvailabilityZone,
			Description:        req.Description,
			PrivateIPAddress:   apiPrivateIP,
			Status:             "available",
			MacAddress:         mac,
		},
	}, nil
}

func (h *Handler) describeNetworkInterfacesTyped(ctx context.Context, req *describeNetworkInterfacesReq) (*xmlDescribeNetworkInterfacesResponse, *protocol.AWSError) {
	return h.describeNetworkInterfaces(ctx, typedQuery(req.NetworkInterfaceIDs, req.Filters))
}

func (h *Handler) deleteNetworkInterfaceTyped(ctx context.Context, req *deleteNetworkInterfaceReq) (*deleteNetworkInterfaceResp, *protocol.AWSError) {
	if req.NetworkInterfaceID == "" {
		return nil, ec2err("MissingParameter", "NetworkInterfaceId is required", http.StatusBadRequest)
	}
	if aerr := h.store.deleteNetworkInterface(ctx, req.NetworkInterfaceID); aerr != nil {
		return nil, aerr
	}
	return &deleteNetworkInterfaceResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

func (h *Handler) modifyInstanceAttributeTyped(ctx context.Context, req *modifyInstanceAttributeReq) (*modifyInstanceAttributeResp, *protocol.AWSError) {
	if req.InstanceID == "" {
		return nil, ec2err("MissingParameter", "The request must contain InstanceId.", http.StatusBadRequest)
	}
	inst, aerr := h.store.getInstance(ctx, req.InstanceID)
	if aerr != nil {
		return nil, aerr
	}
	if req.InstanceType != nil && req.InstanceType.Value != "" {
		inst.InstanceType = req.InstanceType.Value
		if aerr := h.store.putInstance(ctx, inst); aerr != nil {
			return nil, aerr
		}
	}
	return &modifyInstanceAttributeResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

func (h *Handler) createVpcEndpointTyped(ctx context.Context, req *createVpcEndpointReq) (*createVpcEndpointResp, *protocol.AWSError) {
	if req.VpcID == "" || req.ServiceName == "" {
		return nil, ec2err("MissingParameter", "VpcId and ServiceName are required.", http.StatusBadRequest)
	}
	epType := req.VpcEndpointType
	if epType == "" {
		epType = "Gateway"
	}
	if _, aerr := h.store.getVPC(ctx, req.VpcID); aerr != nil {
		return nil, aerr
	}
	tags := typedTagSpecifications(req.TagSpecifications, "vpc-endpoint")
	// Create-time tags are checked before anything is created, as on AWS: a
	// rejected tag must fail the call rather than leave an endpoint behind
	// (#1197 typed-path parity).
	if aerr := validateTagSpecifications(tags); aerr != nil {
		return nil, aerr
	}
	id := fmt.Sprintf("vpce-%s", shortID())
	ep := &VpcEndpoint{
		VpcEndpointID:   id,
		VpcID:           req.VpcID,
		ServiceName:     req.ServiceName,
		State:           "available",
		VpcEndpointType: epType,
	}
	if aerr := h.store.putVpcEndpoint(ctx, ep); aerr != nil {
		return nil, aerr
	}
	if aerr := h.putResourceTags(ctx, id, tags); aerr != nil {
		return nil, aerr
	}
	return &createVpcEndpointResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		VpcEndpoint: typedVpcEndpointXML{
			VpcEndpointID:   ep.VpcEndpointID,
			VpcID:           ep.VpcID,
			ServiceName:     ep.ServiceName,
			State:           ep.State,
			VpcEndpointType: ep.VpcEndpointType,
			TagSet:          typedTagsOf(tags),
		},
	}, nil
}

func (h *Handler) describeVpcEndpointsTyped(ctx context.Context, req *describeVpcEndpointsReq) (*xmlDescribeVpcEndpointsResponse, *protocol.AWSError) {
	return h.describeVpcEndpoints(ctx, typedQuery(req.VpcEndpointIDs, req.Filters))
}

func (h *Handler) deleteVpcEndpointsTyped(ctx context.Context, req *deleteVpcEndpointsReq) (*deleteVpcEndpointsResp, *protocol.AWSError) {
	for _, id := range req.VpcEndpointIDs {
		_ = h.store.deleteVpcEndpoint(ctx, id)
	}
	return &deleteVpcEndpointsResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

// ── Typed helpers ───────────────────────────────────────────────────

func ec2err(code, message string, httpStatus int) *protocol.AWSError {
	return &protocol.AWSError{Code: code, Message: message, HTTPStatus: httpStatus}
}

func routeTableToTypedXML(rt *RouteTable) typedRouteTableXML {
	routes := make([]typedRouteXML, 0, len(rt.Routes))
	for _, r := range rt.Routes {
		routes = append(routes, typedRouteXML{
			DestinationCidrBlock: r.DestinationCidrBlock,
			GatewayID:            r.GatewayID,
			NatGatewayID:         r.NatGatewayID,
			Origin:               r.Origin,
			State:                "active",
		})
	}
	assocs := make([]typedRouteTableAssociationXML, 0, len(rt.Associations))
	for _, a := range rt.Associations {
		assocs = append(assocs, typedRouteTableAssociationXML(a))
	}
	return typedRouteTableXML{
		RouteTableID:   rt.RouteTableID,
		VpcID:          rt.VpcID,
		RouteSet:       routes,
		AssociationSet: assocs,
	}
}

// typedResourceTagsOf renders tags for the typed operations that use
// typedResourceTagXML rather than typedTagXML for their tagSet — NAT gateway
// and VPN gateway, whose response types predate typedTagsOf (tags.go).
func typedResourceTagsOf(tags []Tag) []typedResourceTagXML {
	out := make([]typedResourceTagXML, 0, len(tags))
	for _, tag := range tags {
		out = append(out, typedResourceTagXML(tag))
	}
	return out
}

func vpnGatewayToTypedXML(vgw *VpnGateway, tags []Tag) typedVpnGatewayXML {
	attachments := make([]typedVpnGatewayAttachmentXML, 0, len(vgw.Attachments))
	for _, att := range vgw.Attachments {
		attachments = append(attachments, typedVpnGatewayAttachmentXML(att))
	}
	return typedVpnGatewayXML{
		VpnGatewayID:     vgw.VpnGatewayID,
		State:            vgw.State,
		Type:             vgw.Type,
		AvailabilityZone: vgw.AvailabilityZone,
		Attachments:      attachments,
		AmazonSideAsn:    vgw.AmazonSideAsn,
		Tags:             typedResourceTagsOf(tags),
	}
}

func pcxToTypedXML(pcx *VpcPeeringConnection, tags []Tag) typedVPCPeeringXML {
	return typedVPCPeeringXML{
		VpcPeeringConnectionID: pcx.VpcPeeringConnectionID,
		RequesterVpcInfo: typedVPCPeeringVpcInfoXML{
			OwnerID:   pcx.RequesterVpcInfo.OwnerID,
			VpcID:     pcx.RequesterVpcInfo.VpcID,
			CidrBlock: pcx.RequesterVpcInfo.CidrBlock,
			Region:    pcx.RequesterVpcInfo.Region,
		},
		AccepterVpcInfo: typedVPCPeeringVpcInfoXML{
			OwnerID:   pcx.AccepterVpcInfo.OwnerID,
			VpcID:     pcx.AccepterVpcInfo.VpcID,
			CidrBlock: pcx.AccepterVpcInfo.CidrBlock,
			Region:    pcx.AccepterVpcInfo.Region,
		},
		TagSet: typedTagsOf(tags),
		Status: typedVPCPeeringStatusXML{
			Code:    pcx.Status.Code,
			Message: pcx.Status.Message,
		},
	}
}
