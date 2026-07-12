package ec2

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Neaox/overcast/internal/protocol"
)

// ── Request types (json tags for codec.Decode QueryXML form mapping) ───

type describeRegionsReq struct{}

type describeAzsReq struct{}

type describeInstancesReq struct{}

type describeInstanceTypesReq struct{}

type createVpcReq struct {
	CidrBlock string `json:"CidrBlock"`
}

type describeVpcsReq struct{}

type deleteVpcReq struct {
	VpcID string `json:"VpcId"`
}

type createSubnetReq struct {
	VpcID     string `json:"VpcId"`
	CidrBlock string `json:"CidrBlock"`
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
	GroupID string `json:"GroupId"`
}

type authorizeSGEgressReq struct {
	GroupID string `json:"GroupId"`
}

type revokeSGIngressReq struct {
	GroupID string `json:"GroupId"`
}

type revokeSGEgressReq struct {
	GroupID string `json:"GroupId"`
}

type describeSecurityGroupsReq struct{}

type describeSubnetsReq struct{}

type runInstancesReq struct {
	ImageID      string `json:"ImageId"`
	InstanceType string `json:"InstanceType"`
	MinCount     int    `json:"MinCount"`
	MaxCount     int    `json:"MaxCount"`
	SubnetID     string `json:"SubnetId"`
}

type terminateInstancesReq struct{}

type stopInstancesReq struct{}

type startInstancesReq struct{}

type describeImagesReq struct{}

type createKeyPairReq struct {
	KeyName string `json:"KeyName"`
}

type describeKeyPairsReq struct{}

type deleteKeyPairReq struct {
	KeyName string `json:"KeyName"`
}

type createRouteTableReq struct {
	VpcID string `json:"VpcId"`
}

type describeRouteTablesReq struct{}

type deleteRouteTableReq struct {
	RouteTableID string `json:"RouteTableId"`
}

type createRouteReq struct {
	RouteTableID         string `json:"RouteTableId"`
	DestinationCidrBlock string `json:"DestinationCidrBlock"`
	GatewayID            string `json:"GatewayId"`
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

type describeIGWsReq struct{}

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
	VpcID     string `json:"VpcId"`
	PeerVpcID string `json:"PeerVpcId"`
}

type acceptVPCPeeringReq struct {
	VpcPeeringConnectionID string `json:"VpcPeeringConnectionId"`
}

type describeVPCPeeringsReq struct{}

type deleteVPCPeeringReq struct {
	VpcPeeringConnectionID string `json:"VpcPeeringConnectionId"`
}

type createTagsReq struct{}

type deleteTagsReq struct{}

type describeTagsReq struct{}

type allocateAddressReq struct{}

type releaseAddressReq struct {
	AllocationID string `json:"AllocationId"`
}

type describeAddressesReq struct{}

type associateAddressReq struct {
	AllocationID string `json:"AllocationId"`
	InstanceID   string `json:"InstanceId"`
}

type disassociateAddressReq struct {
	AssociationID string `json:"AssociationId"`
}

type createNatGatewayReq struct {
	SubnetID     string `json:"SubnetId"`
	AllocationID string `json:"AllocationId"`
}

type describeNatGatewaysReq struct{}

type deleteNatGatewayReq struct {
	NatGatewayID string `json:"NatGatewayId"`
}

type modifySubnetAttributeReq struct {
	SubnetID string `json:"SubnetId"`
}

type modifyVpcAttributeReq struct {
	VpcID string `json:"VpcId"`
}

type describeVpcAttributeReq struct {
	VpcID     string `json:"VpcId"`
	Attribute string `json:"Attribute"`
}

type describeDhcpOptionsReq struct{}

type describeAccountAttributesReq struct{}

type createNetworkInterfaceReq struct {
	SubnetID    string `json:"SubnetId"`
	Description string `json:"Description"`
}

type describeNetworkInterfacesReq struct{}

type deleteNetworkInterfaceReq struct {
	NetworkInterfaceID string `json:"NetworkInterfaceId"`
}

type modifyInstanceAttributeReq struct {
	InstanceID string `json:"InstanceId"`
}

type createVpcEndpointReq struct {
	VpcID       string `json:"VpcId"`
	ServiceName string `json:"ServiceName"`
}

type describeVpcEndpointsReq struct{}

type deleteVpcEndpointsReq struct{}

// ── Response types (xml tags for QueryXML codec WriteResponse) ────

type describeRegionsResp struct {
	XMLName    struct{}         `xml:"DescribeRegionsResponse"`
	Xmlns      string           `xml:"xmlns,attr"`
	RequestID  string           `xml:"requestId"`
	RegionInfo []typedRegionXML `xml:"regionInfo>item"`
}

type typedRegionXML struct {
	RegionName     string `xml:"regionName"`
	RegionEndpoint string `xml:"regionEndpoint"`
	OptInStatus    string `xml:"optInStatus"`
}

type describeAzsResp struct {
	XMLName              struct{}     `xml:"DescribeAvailabilityZonesResponse"`
	Xmlns                string       `xml:"xmlns,attr"`
	RequestID            string       `xml:"requestId"`
	AvailabilityZoneInfo []typedAzXML `xml:"availabilityZoneInfo>item"`
}

type typedAzXML struct {
	ZoneName   string `xml:"zoneName"`
	ZoneState  string `xml:"zoneState"`
	RegionName string `xml:"regionName"`
}

type describeInstancesResp struct {
	XMLName      struct{}              `xml:"DescribeInstancesResponse"`
	Xmlns        string                `xml:"xmlns,attr"`
	RequestID    string                `xml:"requestId"`
	Reservations []typedReservationXML `xml:"reservationSet>item"`
}

type typedReservationXML struct {
	ReservationID string             `xml:"reservationId"`
	OwnerID       string             `xml:"ownerId"`
	Instances     []typedInstanceXML `xml:"instancesSet>item"`
}

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

type describeInstanceTypesResp struct {
	XMLName           struct{}               `xml:"DescribeInstanceTypesResponse"`
	Xmlns             string                 `xml:"xmlns,attr"`
	RequestID         string                 `xml:"requestId"`
	InstanceTypeItems []typedInstanceTypeXML `xml:"instanceTypeSet>item"`
}

type typedInstanceTypeXML struct {
	InstanceType      string             `xml:"instanceType"`
	CurrentGeneration bool               `xml:"currentGeneration"`
	VCpuInfo          typedVCpuInfoXML   `xml:"vCpuInfo"`
	MemoryInfo        typedMemoryInfoXML `xml:"memoryInfo"`
}

type typedVCpuInfoXML struct {
	DefaultVCpus int `xml:"defaultVCpus"`
}

type typedMemoryInfoXML struct {
	SizeInMiB int `xml:"sizeInMiB"`
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

type describeVpcsResp struct {
	XMLName   struct{}      `xml:"DescribeVpcsResponse"`
	Xmlns     string        `xml:"xmlns,attr"`
	RequestID string        `xml:"requestId"`
	VpcSet    []typedVpcXML `xml:"vpcSet>item"`
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
	SubnetID                string `xml:"subnetId"`
	State                   string `xml:"state"`
	VpcID                   string `xml:"vpcId"`
	CidrBlock               string `xml:"cidrBlock"`
	AvailabilityZone        string `xml:"availabilityZone"`
	AvailableIPAddressCount int    `xml:"availableIpAddressCount"`
	DefaultForAz            bool   `xml:"defaultForAz"`
	MapPublicIPOnLaunch     bool   `xml:"mapPublicIpOnLaunch"`
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

type describeSGsResp struct {
	XMLName   struct{}     `xml:"DescribeSecurityGroupsResponse"`
	Xmlns     string       `xml:"xmlns,attr"`
	RequestID string       `xml:"requestId"`
	Groups    []typedSGXML `xml:"securityGroupInfo>item"`
}

type typedSGXML struct {
	OwnerID             string           `xml:"ownerId"`
	GroupID             string           `xml:"groupId"`
	GroupName           string           `xml:"groupName"`
	GroupDescription    string           `xml:"groupDescription"`
	VpcID               string           `xml:"vpcId"`
	IpPermissions       []typedIpPermXML `xml:"ipPermissions>item,omitempty"`
	IpPermissionsEgress []typedIpPermXML `xml:"ipPermissionsEgress>item,omitempty"`
}

type typedIpPermXML struct {
	IpProtocol string            `xml:"ipProtocol"`
	FromPort   int               `xml:"fromPort"`
	ToPort     int               `xml:"toPort"`
	IpRanges   []typedIpRangeXML `xml:"ipRanges>item,omitempty"`
}

type typedIpRangeXML struct {
	CidrIp      string `xml:"cidrIp"`
	Description string `xml:"description,omitempty"`
}

type describeSubnetsResp struct {
	XMLName   struct{}         `xml:"DescribeSubnetsResponse"`
	Xmlns     string           `xml:"xmlns,attr"`
	RequestID string           `xml:"requestId"`
	Subnets   []typedSubnetXML `xml:"subnetSet>item"`
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

type describeImagesResp struct {
	XMLName   struct{}        `xml:"DescribeImagesResponse"`
	Xmlns     string          `xml:"xmlns,attr"`
	RequestID string          `xml:"requestId"`
	ImagesSet []typedImageXML `xml:"imagesSet>item"`
}

type typedImageXML struct {
	ImageID            string `xml:"imageId"`
	Name               string `xml:"name"`
	Description        string `xml:"description"`
	ImageState         string `xml:"imageState"`
	ImageType          string `xml:"imageType"`
	Architecture       string `xml:"architecture"`
	RootDeviceType     string `xml:"rootDeviceType"`
	VirtualizationType string `xml:"virtualizationType"`
	IsPublic           bool   `xml:"isPublic"`
	OwnerID            string `xml:"ownerId"`
}

type createKeyPairResp struct {
	XMLName        struct{} `xml:"CreateKeyPairResponse"`
	Xmlns          string   `xml:"xmlns,attr"`
	RequestID      string   `xml:"requestId"`
	KeyName        string   `xml:"keyName"`
	KeyFingerprint string   `xml:"keyFingerprint"`
	KeyMaterial    string   `xml:"keyMaterial"`
	KeyPairID      string   `xml:"keyPairId"`
}

type describeKeyPairsResp struct {
	XMLName   struct{}          `xml:"DescribeKeyPairsResponse"`
	Xmlns     string            `xml:"xmlns,attr"`
	RequestID string            `xml:"requestId"`
	KeySet    []typedKeyPairXML `xml:"keySet>item"`
}

type typedKeyPairXML struct {
	KeyName        string `xml:"keyName"`
	KeyFingerprint string `xml:"keyFingerprint"`
	KeyPairID      string `xml:"keyPairId"`
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

type describeRouteTablesResp struct {
	XMLName       struct{}             `xml:"DescribeRouteTablesResponse"`
	Xmlns         string               `xml:"xmlns,attr"`
	RequestID     string               `xml:"requestId"`
	RouteTableSet []typedRouteTableXML `xml:"routeTableSet>item"`
}

type typedRouteTableXML struct {
	RouteTableID   string                          `xml:"routeTableId"`
	VpcID          string                          `xml:"vpcId"`
	RouteSet       []typedRouteXML                 `xml:"routeSet>item"`
	AssociationSet []typedRouteTableAssociationXML `xml:"associationSet>item,omitempty"`
}

type typedRouteXML struct {
	DestinationCidrBlock string `xml:"destinationCidrBlock"`
	GatewayID            string `xml:"gatewayId,omitempty"`
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

type describeIGWsResp struct {
	XMLName            struct{}      `xml:"DescribeInternetGatewaysResponse"`
	Xmlns              string        `xml:"xmlns,attr"`
	RequestID          string        `xml:"requestId"`
	InternetGatewaySet []typedIGWXML `xml:"internetGatewaySet>item"`
}

type typedIGWXML struct {
	InternetGatewayID string                  `xml:"internetGatewayId"`
	AttachmentSet     []typedIGWAttachmentXML `xml:"attachmentSet>item,omitempty"`
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

type describeVPCPeeringsResp struct {
	XMLName                 struct{}             `xml:"DescribeVpcPeeringConnectionsResponse"`
	Xmlns                   string               `xml:"xmlns,attr"`
	RequestID               string               `xml:"requestId"`
	VpcPeeringConnectionSet []typedVPCPeeringXML `xml:"vpcPeeringConnectionSet>item"`
}

type typedVPCPeeringXML struct {
	VpcPeeringConnectionID string                    `xml:"vpcPeeringConnectionId"`
	RequesterVpcInfo       typedVPCPeeringVpcInfoXML `xml:"requesterVpcInfo"`
	AccepterVpcInfo        typedVPCPeeringVpcInfoXML `xml:"accepterVpcInfo"`
	Status                 typedVPCPeeringStatusXML  `xml:"status"`
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

type describeTagsResp struct {
	XMLName   struct{}          `xml:"DescribeTagsResponse"`
	Xmlns     string            `xml:"xmlns,attr"`
	RequestID string            `xml:"requestId"`
	TagSet    []typedTagItemXML `xml:"tagSet>item"`
}

type typedTagItemXML struct {
	ResourceID   string `xml:"resourceId"`
	ResourceType string `xml:"resourceType"`
	Key          string `xml:"key"`
	Value        string `xml:"value"`
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

type describeAddressesResp struct {
	XMLName    struct{}          `xml:"DescribeAddressesResponse"`
	Xmlns      string            `xml:"xmlns,attr"`
	RequestID  string            `xml:"requestId"`
	AddressSet []typedAddressXML `xml:"addressesSet>item"`
}

type typedAddressXML struct {
	PublicIP       string `xml:"publicIp"`
	AllocationID   string `xml:"allocationId"`
	Domain         string `xml:"domain"`
	AssociationID  string `xml:"associationId,omitempty"`
	InstanceID     string `xml:"instanceId,omitempty"`
	NetworkIfaceID string `xml:"networkInterfaceId,omitempty"`
	PrivateIP      string `xml:"privateIpAddress,omitempty"`
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

type describeNatGatewaysResp struct {
	XMLName     struct{}             `xml:"DescribeNatGatewaysResponse"`
	Xmlns       string               `xml:"xmlns,attr"`
	RequestID   string               `xml:"requestId"`
	NatGateways []typedNatGatewayXML `xml:"natGatewaySet>item"`
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

type describeVpcAttributeResp struct {
	XMLName            struct{}         `xml:"DescribeVpcAttributeResponse"`
	Xmlns              string           `xml:"xmlns,attr"`
	RequestID          string           `xml:"requestId"`
	VpcID              string           `xml:"vpcId"`
	EnableDnsSupport   *typedAttrValXML `xml:"enableDnsSupport,omitempty"`
	EnableDnsHostnames *typedAttrValXML `xml:"enableDnsHostnames,omitempty"`
}

type typedAttrValXML struct {
	Value bool `xml:"value"`
}

type describeDhcpOptionsResp struct {
	XMLName        struct{}             `xml:"DescribeDhcpOptionsResponse"`
	Xmlns          string               `xml:"xmlns,attr"`
	RequestID      string               `xml:"requestId"`
	DhcpOptionsSet []typedDhcpOptionXML `xml:"dhcpOptionsSet>item"`
}

type typedDhcpOptionXML struct {
	DhcpOptionsID        string                      `xml:"dhcpOptionsId"`
	DhcpConfigurationSet []typedDhcpConfigurationXML `xml:"dhcpConfigurationSet>item"`
}

type typedDhcpConfigurationXML struct {
	Key      string              `xml:"key"`
	ValueSet []typedDhcpValueXML `xml:"valueSet>item"`
}

type typedDhcpValueXML struct {
	Value string `xml:"value"`
}

type describeAccountAttributesResp struct {
	XMLName             struct{}              `xml:"DescribeAccountAttributesResponse"`
	Xmlns               string                `xml:"xmlns,attr"`
	RequestID           string                `xml:"requestId"`
	AccountAttributeSet []typedAccountAttrXML `xml:"accountAttributeSet>item"`
}

type typedAccountAttrXML struct {
	AttributeName     string                     `xml:"attributeName"`
	AttributeValueSet []typedAccountAttrValueXML `xml:"attributeValueSet>item"`
}

type typedAccountAttrValueXML struct {
	AttributeValue string `xml:"attributeValue"`
}

type createNetworkInterfaceResp struct {
	XMLName          struct{}                 `xml:"CreateNetworkInterfaceResponse"`
	Xmlns            string                   `xml:"xmlns,attr"`
	RequestID        string                   `xml:"requestId"`
	NetworkInterface typedNetworkInterfaceXML `xml:"networkInterface"`
}

type typedNetworkInterfaceXML struct {
	NetworkInterfaceID string `xml:"networkInterfaceId"`
	SubnetID           string `xml:"subnetId"`
	VpcID              string `xml:"vpcId"`
	AvailabilityZone   string `xml:"availabilityZone"`
	Description        string `xml:"description"`
	PrivateIPAddress   string `xml:"privateIpAddress"`
	Status             string `xml:"status"`
	MacAddress         string `xml:"macAddress"`
}

type describeNetworkInterfacesResp struct {
	XMLName             struct{}                   `xml:"DescribeNetworkInterfacesResponse"`
	Xmlns               string                     `xml:"xmlns,attr"`
	RequestID           string                     `xml:"requestId"`
	NetworkInterfaceSet []typedNetworkInterfaceXML `xml:"networkInterfaceSet>item"`
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
	VpcEndpointID   string `xml:"vpcEndpointId"`
	VpcID           string `xml:"vpcId"`
	ServiceName     string `xml:"serviceName"`
	State           string `xml:"state"`
	VpcEndpointType string `xml:"vpcEndpointType"`
}

type describeVpcEndpointsResp struct {
	XMLName      struct{}              `xml:"DescribeVpcEndpointsResponse"`
	Xmlns        string                `xml:"xmlns,attr"`
	RequestID    string                `xml:"requestId"`
	VpcEndpoints []typedVpcEndpointXML `xml:"vpcEndpointSet>item"`
}

type deleteVpcEndpointsResp struct {
	XMLName   struct{} `xml:"DeleteVpcEndpointsResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

// ── Typed handler functions ────────────────────────────────────────

func (h *Handler) describeRegionsTyped(ctx context.Context, _ *describeRegionsReq) (*describeRegionsResp, *protocol.AWSError) {
	regions := []typedRegionXML{
		{RegionName: "us-east-1", RegionEndpoint: "ec2.us-east-1.amazonaws.com", OptInStatus: "opt-in-not-required"},
		{RegionName: "us-east-2", RegionEndpoint: "ec2.us-east-2.amazonaws.com", OptInStatus: "opt-in-not-required"},
		{RegionName: "us-west-1", RegionEndpoint: "ec2.us-west-1.amazonaws.com", OptInStatus: "opt-in-not-required"},
		{RegionName: "us-west-2", RegionEndpoint: "ec2.us-west-2.amazonaws.com", OptInStatus: "opt-in-not-required"},
		{RegionName: "eu-west-1", RegionEndpoint: "ec2.eu-west-1.amazonaws.com", OptInStatus: "opt-in-not-required"},
		{RegionName: "eu-central-1", RegionEndpoint: "ec2.eu-central-1.amazonaws.com", OptInStatus: "opt-in-not-required"},
		{RegionName: "ap-southeast-1", RegionEndpoint: "ec2.ap-southeast-1.amazonaws.com", OptInStatus: "opt-in-not-required"},
		{RegionName: "ap-northeast-1", RegionEndpoint: "ec2.ap-northeast-1.amazonaws.com", OptInStatus: "opt-in-not-required"},
	}
	return &describeRegionsResp{
		Xmlns:      ec2XMLNS,
		RequestID:  protocol.RequestIDFromContext(ctx),
		RegionInfo: regions,
	}, nil
}

func (h *Handler) describeAzsTyped(ctx context.Context, _ *describeAzsReq) (*describeAzsResp, *protocol.AWSError) {
	region := h.cfg.Region
	azs := []typedAzXML{
		{ZoneName: region + "a", ZoneState: "available", RegionName: region},
		{ZoneName: region + "b", ZoneState: "available", RegionName: region},
		{ZoneName: region + "c", ZoneState: "available", RegionName: region},
	}
	return &describeAzsResp{
		Xmlns:                ec2XMLNS,
		RequestID:            protocol.RequestIDFromContext(ctx),
		AvailabilityZoneInfo: azs,
	}, nil
}

func (h *Handler) describeInstancesTyped(ctx context.Context, _ *describeInstancesReq) (*describeInstancesResp, *protocol.AWSError) {
	all, aerr := h.store.listInstances(ctx)
	if aerr != nil {
		return nil, aerr
	}
	items := make([]typedInstanceXML, 0, len(all))
	for _, inst := range all {
		xmlTags := make([]typedTagXML, 0, len(inst.Tags))
		for _, tag := range inst.Tags {
			xmlTags = append(xmlTags, typedTagXML(tag))
		}
		xmlSGs := make([]typedSGRefXML, 0, len(inst.SecurityGroups))
		for _, sg := range inst.SecurityGroups {
			xmlSGs = append(xmlSGs, typedSGRefXML(sg))
		}
		items = append(items, typedInstanceXML{
			InstanceID:    inst.InstanceID,
			ImageID:       inst.ImageID,
			InstanceState: typedInstanceStateXML{Code: inst.State.Code, Name: inst.State.Name},
			InstanceType:  inst.InstanceType,
			LaunchTime:    inst.LaunchTime,
			SubnetID:      inst.SubnetID,
			VpcID:         inst.VpcID,
			PrivateIP:     h.privateIPForAPI(ctx, inst.VpcID, inst.PrivateIPAddress),
			Placement:     typedPlacementXML{AvailabilityZone: inst.Placement.AvailabilityZone},
			GroupSet:      xmlSGs,
			TagSet:        xmlTags,
		})
	}
	var reservations []typedReservationXML
	if len(items) > 0 {
		reservations = []typedReservationXML{{
			ReservationID: fmt.Sprintf("r-%s", shortID()),
			OwnerID:       "123456789012",
			Instances:     items,
		}}
	}
	return &describeInstancesResp{
		Xmlns:        ec2XMLNS,
		RequestID:    protocol.RequestIDFromContext(ctx),
		Reservations: reservations,
	}, nil
}

func (h *Handler) describeInstanceTypesTyped(ctx context.Context, _ *describeInstanceTypesReq) (*describeInstanceTypesResp, *protocol.AWSError) {
	return &describeInstanceTypesResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
	}, nil
}

func (h *Handler) createVpcTyped(ctx context.Context, req *createVpcReq) (*createVpcResp, *protocol.AWSError) {
	if req.CidrBlock == "" {
		return nil, ec2err("MissingParameter", "CidrBlock is required", http.StatusBadRequest)
	}
	vpcID := fmt.Sprintf("vpc-%s", shortID())
	vpc := &VPC{VpcID: vpcID, CidrBlock: req.CidrBlock, State: "available", CreateTime: h.clk.Now().UnixMilli()}
	if h.vpcStrategy != nil {
		if aerr := h.vpcStrategy.EnsureNetwork(ctx, vpc); aerr != nil {
			return nil, aerr
		}
	}
	if aerr := h.putVPCWithMainRouteTable(ctx, vpc); aerr != nil {
		return nil, aerr
	}
	return &createVpcResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Vpc: typedVpcXML{
			VpcID:           vpcID,
			State:           "available",
			CidrBlock:       req.CidrBlock,
			DhcpOptionsID:   fmt.Sprintf("dopt-%s", shortID()),
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

func (h *Handler) describeVpcsTyped(ctx context.Context, _ *describeVpcsReq) (*describeVpcsResp, *protocol.AWSError) {
	vpcs, aerr := h.store.listVPCs(ctx)
	if aerr != nil {
		return nil, aerr
	}
	items := make([]typedVpcXML, 0, len(vpcs))
	for _, v := range vpcs {
		ns := v.NetworkStatus
		if ns == "" {
			ns = vpcNetworkStatusOK
		}
		items = append(items, typedVpcXML{
			VpcID:           v.VpcID,
			State:           v.State,
			CidrBlock:       v.CidrBlock,
			InstanceTenancy: "default",
			IsDefault:       false,
			TagSet: []typedTagXML{
				{Key: "overcast:network-status", Value: ns},
			},
		})
	}
	return &describeVpcsResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		VpcSet:    items,
	}, nil
}

func (h *Handler) deleteVpcTyped(ctx context.Context, req *deleteVpcReq) (*deleteVpcResp, *protocol.AWSError) {
	if req.VpcID == "" {
		return nil, ec2err("MissingParameter", "VpcId is required", http.StatusBadRequest)
	}
	vpc, _ := h.store.getVPC(ctx, req.VpcID)
	if aerr := h.store.deleteVPC(ctx, req.VpcID); aerr != nil {
		return nil, aerr
	}
	if aerr := h.deleteRouteTablesForVPC(ctx, req.VpcID); aerr != nil {
		return nil, aerr
	}
	if vpc != nil && h.vpcStrategy != nil {
		h.vpcStrategy.OnDelete(ctx, vpc)
	}
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
	az := h.cfg.Region + "a"
	subnet := &Subnet{SubnetID: subnetID, VpcID: req.VpcID, CidrBlock: req.CidrBlock, AvailabilityZone: az, State: "available"}
	if aerr := h.store.putSubnet(ctx, subnet); aerr != nil {
		return nil, aerr
	}
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
	if aerr := h.store.deleteSubnet(ctx, req.SubnetID); aerr != nil {
		return nil, aerr
	}
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
	if aerr := h.store.deleteSecurityGroup(ctx, req.GroupID); aerr != nil {
		return nil, aerr
	}
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
	return &revokeSGEgressResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

func (h *Handler) describeSecurityGroupsTyped(ctx context.Context, _ *describeSecurityGroupsReq) (*describeSGsResp, *protocol.AWSError) {
	all, aerr := h.store.listSecurityGroups(ctx)
	if aerr != nil {
		return nil, aerr
	}
	items := make([]typedSGXML, 0, len(all))
	for _, sg := range all {
		ingress := make([]typedIpPermXML, 0, len(sg.IpPermissions))
		for _, p := range sg.IpPermissions {
			ranges := make([]typedIpRangeXML, 0, len(p.IpRanges))
			for _, r := range p.IpRanges {
				ranges = append(ranges, typedIpRangeXML(r))
			}
			ingress = append(ingress, typedIpPermXML{
				IpProtocol: p.IpProtocol,
				FromPort:   p.FromPort,
				ToPort:     p.ToPort,
				IpRanges:   ranges,
			})
		}
		egress := make([]typedIpPermXML, 0, len(sg.IpPermissionsEgress))
		for _, p := range sg.IpPermissionsEgress {
			ranges := make([]typedIpRangeXML, 0, len(p.IpRanges))
			for _, r := range p.IpRanges {
				ranges = append(ranges, typedIpRangeXML(r))
			}
			egress = append(egress, typedIpPermXML{
				IpProtocol: p.IpProtocol,
				FromPort:   p.FromPort,
				ToPort:     p.ToPort,
				IpRanges:   ranges,
			})
		}
		items = append(items, typedSGXML{
			OwnerID:             "000000000000",
			GroupID:             sg.GroupID,
			GroupName:           sg.GroupName,
			GroupDescription:    sg.Description,
			VpcID:               sg.VpcID,
			IpPermissions:       ingress,
			IpPermissionsEgress: egress,
		})
	}
	return &describeSGsResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Groups:    items,
	}, nil
}

func (h *Handler) describeSubnetsTyped(ctx context.Context, _ *describeSubnetsReq) (*describeSubnetsResp, *protocol.AWSError) {
	all, aerr := h.store.listSubnets(ctx)
	if aerr != nil {
		return nil, aerr
	}
	items := make([]typedSubnetXML, 0, len(all))
	for _, sub := range all {
		items = append(items, typedSubnetXML{
			SubnetID:                sub.SubnetID,
			State:                   sub.State,
			VpcID:                   sub.VpcID,
			CidrBlock:               sub.CidrBlock,
			AvailabilityZone:        sub.AvailabilityZone,
			AvailableIPAddressCount: 251,
			DefaultForAz:            false,
			MapPublicIPOnLaunch:     false,
		})
	}
	return &describeSubnetsResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Subnets:   items,
	}, nil
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
			Placement:        Placement{AvailabilityZone: az},
			VpcID:            vpcID,
		}
		if aerr := h.store.putInstance(ctx, inst); aerr != nil {
			return nil, aerr
		}
		id := instID
		h.scheduler.After(id+":start", 0, func() {
			bgCtx := context.Background()
			got, aerr := h.store.getInstance(bgCtx, id)
			if aerr != nil {
				return
			}
			if got.State.Code == 0 {
				got.State = InstanceState{Code: 16, Name: "running"}
				_ = h.store.putInstance(bgCtx, got)
			}
		})
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
		})
	}
	return &runInstancesResp{
		Xmlns:         ec2XMLNS,
		RequestID:     protocol.RequestIDFromContext(ctx),
		ReservationID: fmt.Sprintf("r-%s", shortID()),
		OwnerID:       "123456789012",
		Instances:     instances,
	}, nil
}

func (h *Handler) terminateInstancesTyped(ctx context.Context, _ *terminateInstancesReq) (*terminateInstancesResp, *protocol.AWSError) {
	return &terminateInstancesResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
	}, nil
}

func (h *Handler) stopInstancesTyped(ctx context.Context, _ *stopInstancesReq) (*stopInstancesResp, *protocol.AWSError) {
	return &stopInstancesResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
	}, nil
}

func (h *Handler) startInstancesTyped(ctx context.Context, _ *startInstancesReq) (*startInstancesResp, *protocol.AWSError) {
	return &startInstancesResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
	}, nil
}

func (h *Handler) describeImagesTyped(ctx context.Context, _ *describeImagesReq) (*describeImagesResp, *protocol.AWSError) {
	typedImages := make([]typedImageXML, 0, len(syntheticAMIs))
	for _, ami := range syntheticAMIs {
		typedImages = append(typedImages, typedImageXML(ami))
	}
	return &describeImagesResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		ImagesSet: typedImages,
	}, nil
}

func (h *Handler) createKeyPairTyped(ctx context.Context, req *createKeyPairReq) (*createKeyPairResp, *protocol.AWSError) {
	if req.KeyName == "" {
		return nil, ec2err("MissingParameter", "KeyName is required", http.StatusBadRequest)
	}
	if _, aerr := h.store.getKeyPair(ctx, req.KeyName); aerr == nil {
		return nil, ec2err("InvalidKeyPair.Duplicate", fmt.Sprintf("The keypair '%s' already exists", req.KeyName), http.StatusBadRequest)
	}
	fingerprint := randomFingerprint()
	material := dummyKeyMaterial()
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
	return &createKeyPairResp{
		Xmlns:          ec2XMLNS,
		RequestID:      protocol.RequestIDFromContext(ctx),
		KeyName:        req.KeyName,
		KeyFingerprint: fingerprint,
		KeyMaterial:    material,
		KeyPairID:      kpID,
	}, nil
}

func (h *Handler) describeKeyPairsTyped(ctx context.Context, _ *describeKeyPairsReq) (*describeKeyPairsResp, *protocol.AWSError) {
	all, aerr := h.store.listKeyPairs(ctx)
	if aerr != nil {
		return nil, aerr
	}
	items := make([]typedKeyPairXML, 0, len(all))
	for _, kp := range all {
		items = append(items, typedKeyPairXML{
			KeyName:        kp.KeyName,
			KeyFingerprint: kp.KeyFingerprint,
			KeyPairID:      kp.KeyPairID,
		})
	}
	return &describeKeyPairsResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		KeySet:    items,
	}, nil
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

func (h *Handler) describeRouteTablesTyped(ctx context.Context, _ *describeRouteTablesReq) (*describeRouteTablesResp, *protocol.AWSError) {
	all, aerr := h.store.listRouteTables(ctx)
	if aerr != nil {
		return nil, aerr
	}
	items := make([]typedRouteTableXML, 0, len(all))
	for _, rt := range all {
		items = append(items, routeTableToTypedXML(rt))
	}
	return &describeRouteTablesResp{
		Xmlns:         ec2XMLNS,
		RequestID:     protocol.RequestIDFromContext(ctx),
		RouteTableSet: items,
	}, nil
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

func (h *Handler) describeIGWsTyped(ctx context.Context, _ *describeIGWsReq) (*describeIGWsResp, *protocol.AWSError) {
	all, aerr := h.store.listInternetGateways(ctx)
	if aerr != nil {
		return nil, aerr
	}
	items := make([]typedIGWXML, 0, len(all))
	for _, igw := range all {
		items = append(items, igwToTypedXML(igw))
	}
	return &describeIGWsResp{
		Xmlns:              ec2XMLNS,
		RequestID:          protocol.RequestIDFromContext(ctx),
		InternetGatewaySet: items,
	}, nil
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
	return &createVPCPeeringResp{
		Xmlns:                ec2XMLNS,
		RequestID:            protocol.RequestIDFromContext(ctx),
		VpcPeeringConnection: pcxToTypedXML(pcx),
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
	return &acceptVPCPeeringResp{
		Xmlns:                ec2XMLNS,
		RequestID:            protocol.RequestIDFromContext(ctx),
		VpcPeeringConnection: pcxToTypedXML(pcx),
	}, nil
}

func (h *Handler) describeVPCPeeringsTyped(ctx context.Context, _ *describeVPCPeeringsReq) (*describeVPCPeeringsResp, *protocol.AWSError) {
	all, aerr := h.store.listVpcPeeringConnections(ctx)
	if aerr != nil {
		return nil, aerr
	}
	items := make([]typedVPCPeeringXML, 0, len(all))
	for _, pcx := range all {
		items = append(items, pcxToTypedXML(pcx))
	}
	return &describeVPCPeeringsResp{
		Xmlns:                   ec2XMLNS,
		RequestID:               protocol.RequestIDFromContext(ctx),
		VpcPeeringConnectionSet: items,
	}, nil
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

func (h *Handler) createTagsTyped(ctx context.Context, _ *createTagsReq) (*createTagsResp, *protocol.AWSError) {
	return &createTagsResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

func (h *Handler) deleteTagsTyped(ctx context.Context, _ *deleteTagsReq) (*deleteTagsResp, *protocol.AWSError) {
	return &deleteTagsResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

func (h *Handler) describeTagsTyped(ctx context.Context, _ *describeTagsReq) (*describeTagsResp, *protocol.AWSError) {
	allTags, _ := h.store.listAllTags(ctx)
	items := make([]typedTagItemXML, 0)
	for rid, tags := range allTags {
		resType := inferResourceType(rid)
		for k, v := range tags {
			items = append(items, typedTagItemXML{
				ResourceID:   rid,
				ResourceType: resType,
				Key:          k,
				Value:        v,
			})
		}
	}
	return &describeTagsResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		TagSet:    items,
	}, nil
}

func (h *Handler) allocateAddressTyped(ctx context.Context, _ *allocateAddressReq) (*allocateAddressResp, *protocol.AWSError) {
	allocID := fmt.Sprintf("eipalloc-%s", shortID())
	ip := fmt.Sprintf("203.0.113.%d", syntheticIPCounter.Add(1)%254+1)
	addr := &ElasticIP{
		AllocationID: allocID,
		PublicIP:     ip,
		Domain:       "vpc",
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

func (h *Handler) describeAddressesTyped(ctx context.Context, _ *describeAddressesReq) (*describeAddressesResp, *protocol.AWSError) {
	all, aerr := h.store.listElasticIPs(ctx)
	if aerr != nil {
		return nil, aerr
	}
	items := make([]typedAddressXML, 0, len(all))
	for _, a := range all {
		items = append(items, typedAddressXML{
			PublicIP:       a.PublicIP,
			AllocationID:   a.AllocationID,
			Domain:         a.Domain,
			AssociationID:  a.AssociationID,
			InstanceID:     a.InstanceID,
			NetworkIfaceID: a.NetworkInterfaceID,
			PrivateIP:      a.PrivateIP,
		})
	}
	return &describeAddressesResp{
		Xmlns:      ec2XMLNS,
		RequestID:  protocol.RequestIDFromContext(ctx),
		AddressSet: items,
	}, nil
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
		},
	}, nil
}

func (h *Handler) describeNatGatewaysTyped(ctx context.Context, _ *describeNatGatewaysReq) (*describeNatGatewaysResp, *protocol.AWSError) {
	all, aerr := h.store.listNatGateways(ctx)
	if aerr != nil {
		return nil, aerr
	}
	items := make([]typedNatGatewayXML, 0, len(all))
	for _, ngw := range all {
		addrs := []typedNatGWAddrXML{{AllocationID: ngw.AllocationID, PublicIP: ngw.PublicIP, PrivateIP: ngw.PrivateIP}}
		items = append(items, typedNatGatewayXML{
			NatGatewayID: ngw.NatGatewayID,
			SubnetID:     ngw.SubnetID,
			VpcID:        ngw.VpcID,
			State:        ngw.State,
			CreateTime:   ngw.CreateTime,
			Addresses:    addrs,
		})
	}
	return &describeNatGatewaysResp{
		Xmlns:       ec2XMLNS,
		RequestID:   protocol.RequestIDFromContext(ctx),
		NatGateways: items,
	}, nil
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
	if _, aerr := h.store.getSubnet(ctx, req.SubnetID); aerr != nil {
		return nil, aerr
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
	if _, aerr := h.store.getVPC(ctx, req.VpcID); aerr != nil {
		return nil, aerr
	}
	return &modifyVpcAttributeResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	}, nil
}

func (h *Handler) describeVpcAttributeTyped(ctx context.Context, req *describeVpcAttributeReq) (*describeVpcAttributeResp, *protocol.AWSError) {
	if req.VpcID == "" {
		return nil, ec2err("MissingParameter", "VpcId is required", http.StatusBadRequest)
	}
	if _, aerr := h.store.getVPC(ctx, req.VpcID); aerr != nil {
		return nil, aerr
	}
	resp := &describeVpcAttributeResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		VpcID:     req.VpcID,
	}
	switch req.Attribute {
	case "enableDnsSupport":
		resp.EnableDnsSupport = &typedAttrValXML{Value: true}
	case "enableDnsHostnames":
		resp.EnableDnsHostnames = &typedAttrValXML{Value: true}
	}
	return resp, nil
}

func (h *Handler) describeDhcpOptionsTyped(ctx context.Context, _ *describeDhcpOptionsReq) (*describeDhcpOptionsResp, *protocol.AWSError) {
	return &describeDhcpOptionsResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		DhcpOptionsSet: []typedDhcpOptionXML{{
			DhcpOptionsID: fmt.Sprintf("dopt-%s", shortID()),
			DhcpConfigurationSet: []typedDhcpConfigurationXML{
				{Key: "domain-name", ValueSet: []typedDhcpValueXML{{Value: "ec2.internal"}}},
				{Key: "domain-name-servers", ValueSet: []typedDhcpValueXML{{Value: "AmazonProvidedDNS"}}},
			},
		}},
	}, nil
}

func (h *Handler) describeAccountAttributesTyped(ctx context.Context, _ *describeAccountAttributesReq) (*describeAccountAttributesResp, *protocol.AWSError) {
	return &describeAccountAttributesResp{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		AccountAttributeSet: []typedAccountAttrXML{
			{AttributeName: "supported-platforms", AttributeValueSet: []typedAccountAttrValueXML{{AttributeValue: "VPC"}}},
			{AttributeName: "default-vpc", AttributeValueSet: []typedAccountAttrValueXML{{AttributeValue: "none"}}},
			{AttributeName: "max-instances", AttributeValueSet: []typedAccountAttrValueXML{{AttributeValue: "20"}}},
			{AttributeName: "vpc-max-security-groups-per-interface", AttributeValueSet: []typedAccountAttrValueXML{{AttributeValue: "5"}}},
			{AttributeName: "max-elastic-ips", AttributeValueSet: []typedAccountAttrValueXML{{AttributeValue: "5"}}},
			{AttributeName: "vpc-max-elastic-ips", AttributeValueSet: []typedAccountAttrValueXML{{AttributeValue: "5"}}},
		},
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

func (h *Handler) describeNetworkInterfacesTyped(ctx context.Context, _ *describeNetworkInterfacesReq) (*describeNetworkInterfacesResp, *protocol.AWSError) {
	all, aerr := h.store.listNetworkInterfaces(ctx)
	if aerr != nil {
		return nil, aerr
	}
	items := make([]typedNetworkInterfaceXML, 0, len(all))
	for _, eni := range all {
		items = append(items, typedNetworkInterfaceXML{
			NetworkInterfaceID: eni.NetworkInterfaceID,
			SubnetID:           eni.SubnetID,
			VpcID:              eni.VpcID,
			AvailabilityZone:   eni.AvailabilityZone,
			Description:        eni.Description,
			PrivateIPAddress:   h.privateIPForAPI(ctx, eni.VpcID, eni.PrivateIPAddress),
			Status:             eni.Status,
			MacAddress:         eni.MacAddress,
		})
	}
	return &describeNetworkInterfacesResp{
		Xmlns:               ec2XMLNS,
		RequestID:           protocol.RequestIDFromContext(ctx),
		NetworkInterfaceSet: items,
	}, nil
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
	_ = inst
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
	if _, aerr := h.store.getVPC(ctx, req.VpcID); aerr != nil {
		return nil, aerr
	}
	id := fmt.Sprintf("vpce-%s", shortID())
	ep := &VpcEndpoint{
		VpcEndpointID:   id,
		VpcID:           req.VpcID,
		ServiceName:     req.ServiceName,
		State:           "available",
		VpcEndpointType: "Gateway",
	}
	if aerr := h.store.putVpcEndpoint(ctx, ep); aerr != nil {
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
		},
	}, nil
}

func (h *Handler) describeVpcEndpointsTyped(ctx context.Context, _ *describeVpcEndpointsReq) (*describeVpcEndpointsResp, *protocol.AWSError) {
	all, aerr := h.store.listVpcEndpoints(ctx)
	if aerr != nil {
		return nil, aerr
	}
	var items []typedVpcEndpointXML
	for _, ep := range all {
		items = append(items, typedVpcEndpointXML{
			VpcEndpointID:   ep.VpcEndpointID,
			VpcID:           ep.VpcID,
			ServiceName:     ep.ServiceName,
			State:           ep.State,
			VpcEndpointType: ep.VpcEndpointType,
		})
	}
	return &describeVpcEndpointsResp{
		Xmlns:        ec2XMLNS,
		RequestID:    protocol.RequestIDFromContext(ctx),
		VpcEndpoints: items,
	}, nil
}

func (h *Handler) deleteVpcEndpointsTyped(ctx context.Context, _ *deleteVpcEndpointsReq) (*deleteVpcEndpointsResp, *protocol.AWSError) {
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

func igwToTypedXML(igw *InternetGateway) typedIGWXML {
	atts := make([]typedIGWAttachmentXML, 0, len(igw.Attachments))
	for _, a := range igw.Attachments {
		atts = append(atts, typedIGWAttachmentXML(a))
	}
	return typedIGWXML{
		InternetGatewayID: igw.InternetGatewayID,
		AttachmentSet:     atts,
	}
}

func pcxToTypedXML(pcx *VpcPeeringConnection) typedVPCPeeringXML {
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
		Status: typedVPCPeeringStatusXML{
			Code:    pcx.Status.Code,
			Message: pcx.Status.Message,
		},
	}
}
