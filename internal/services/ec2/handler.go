package ec2

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/lifecycle"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const ec2XMLNS = "http://ec2.amazonaws.com/doc/2016-11-15/"

// syntheticIPCounter generates incrementing IPs for Elastic IPs, NAT gateways, etc.
var syntheticIPCounter atomic.Uint32

// Handler handles EC2 Query-protocol requests.
type Handler struct {
	cfg         *config.Config
	store       *ec2Store
	log         *serviceutil.ServiceLogger
	clk         clock.Clock
	bus         *events.Bus
	scheduler   *lifecycle.Scheduler
	docker      *docker.Client
	dockerReady atomic.Bool
	vpcStrategy vpcNetworkStrategy

	ops     map[string]http.HandlerFunc
	typedOp map[string]op.Operation
}

func newHandler(cfg *config.Config, store state.Store, log *serviceutil.ServiceLogger, clk clock.Clock) *Handler {
	h := &Handler{
		cfg:       cfg,
		store:     newEC2Store(store, cfg.Region),
		log:       log,
		clk:       clk,
		scheduler: lifecycle.NewScheduler(clk),
	}
	h.vpcStrategy = resolveVPCNetworkStrategy(cfg.EC2VPCNetworkStrategy, h)
	h.buildOpsMap()
	h.buildTypedOps()
	return h
}

func (h *Handler) buildOpsMap() {
	h.ops = map[string]http.HandlerFunc{
		"DescribeRegions":              h.DescribeRegions,
		"DescribeAvailabilityZones":    h.DescribeAvailabilityZones,
		"DescribeInstances":            h.DescribeInstances,
		"DescribeInstanceTypes":        h.DescribeInstanceTypes,
		"CreateVpc":                    h.CreateVpc,
		"DescribeVpcs":                 h.DescribeVpcs,
		"DeleteVpc":                    h.DeleteVpc,
		"CreateSubnet":                 h.CreateSubnet,
		"DeleteSubnet":                 h.DeleteSubnet,
		"CreateSecurityGroup":          h.CreateSecurityGroup,
		"DeleteSecurityGroup":          h.DeleteSecurityGroup,
		"AuthorizeSecurityGroupIngress": h.AuthorizeSecurityGroupIngress,
		"AuthorizeSecurityGroupEgress":  h.AuthorizeSecurityGroupEgress,
		"RevokeSecurityGroupIngress":   h.RevokeSecurityGroupIngress,
		"RevokeSecurityGroupEgress":    h.RevokeSecurityGroupEgress,
		"DescribeSecurityGroups":       h.DescribeSecurityGroups,
		"DescribeSubnets":              h.DescribeSubnets,
		"RunInstances":                 h.RunInstances,
		"TerminateInstances":           h.TerminateInstances,
		"StartInstances":               h.StartInstances,
		"StopInstances":                h.StopInstances,
		"DescribeImages":               h.DescribeImages,
		"CreateKeyPair":                h.CreateKeyPair,
		"DescribeKeyPairs":             h.DescribeKeyPairs,
		"DeleteKeyPair":                h.DeleteKeyPair,
		"CreateRouteTable":             h.CreateRouteTable,
		"DescribeRouteTables":          h.DescribeRouteTables,
		"DeleteRouteTable":             h.DeleteRouteTable,
		"CreateRoute":                  h.CreateRoute,
		"DeleteRoute":                  h.DeleteRoute,
		"AssociateRouteTable":          h.AssociateRouteTable,
		"DisassociateRouteTable":       h.DisassociateRouteTable,
		"CreateInternetGateway":        h.CreateInternetGateway,
		"DescribeInternetGateways":     h.DescribeInternetGateways,
		"DeleteInternetGateway":        h.DeleteInternetGateway,
		"AttachInternetGateway":        h.AttachInternetGateway,
		"DetachInternetGateway":        h.DetachInternetGateway,
		"CreateVpcPeeringConnection":   h.CreateVpcPeeringConnection,
		"AcceptVpcPeeringConnection":   h.AcceptVpcPeeringConnection,
		"DescribeVpcPeeringConnections": h.DescribeVpcPeeringConnections,
		"DeleteVpcPeeringConnection":   h.DeleteVpcPeeringConnection,
		"CreateTags":                   h.CreateTags,
		"DeleteTags":                   h.DeleteTags,
		"DescribeTags":                 h.DescribeTags,
		"AllocateAddress":              h.AllocateAddress,
		"ReleaseAddress":               h.ReleaseAddress,
		"DescribeAddresses":            h.DescribeAddresses,
		"AssociateAddress":             h.AssociateAddress,
		"DisassociateAddress":          h.DisassociateAddress,
		"CreateNatGateway":             h.CreateNatGateway,
		"DescribeNatGateways":          h.DescribeNatGateways,
		"DeleteNatGateway":             h.DeleteNatGateway,
		"ModifySubnetAttribute":        h.ModifySubnetAttribute,
		"ModifyVpcAttribute":           h.ModifyVpcAttribute,
		"DescribeVpcAttribute":         h.DescribeVpcAttribute,
		"DescribeDhcpOptions":          h.DescribeDhcpOptions,
		"DescribeAccountAttributes":    h.DescribeAccountAttributes,
		"CreateNetworkInterface":       h.CreateNetworkInterface,
		"DescribeNetworkInterfaces":    h.DescribeNetworkInterfaces,
		"DeleteNetworkInterface":       h.DeleteNetworkInterface,
		"ModifyInstanceAttribute":      h.ModifyInstanceAttribute,
		"CreateVpcEndpoint":            h.CreateVpcEndpoint,
		"DescribeVpcEndpoints":         h.DescribeVpcEndpoints,
		"DeleteVpcEndpoints":           h.DeleteVpcEndpoints,
	}
}

func (h *Handler) buildTypedOps() {
	h.typedOp = h.typedOps()
}

// ── DescribeRegions ──────────────────────────────────────────────────────────

type xmlDescribeRegionsResponse struct {
	XMLName    xml.Name    `xml:"DescribeRegionsResponse"`
	Xmlns      string      `xml:"xmlns,attr"`
	RequestID  string      `xml:"requestId"`
	RegionInfo []xmlRegion `xml:"regionInfo>item"`
}

type xmlRegion struct {
	RegionName     string `xml:"regionName"`
	RegionEndpoint string `xml:"regionEndpoint"`
	OptInStatus    string `xml:"optInStatus"`
}

// DescribeRegions returns a hardcoded list of AWS regions.
func (h *Handler) DescribeRegions(w http.ResponseWriter, r *http.Request) {
	regions := []xmlRegion{
		{RegionName: "us-east-1", RegionEndpoint: "ec2.us-east-1.amazonaws.com", OptInStatus: "opt-in-not-required"},
		{RegionName: "us-east-2", RegionEndpoint: "ec2.us-east-2.amazonaws.com", OptInStatus: "opt-in-not-required"},
		{RegionName: "us-west-1", RegionEndpoint: "ec2.us-west-1.amazonaws.com", OptInStatus: "opt-in-not-required"},
		{RegionName: "us-west-2", RegionEndpoint: "ec2.us-west-2.amazonaws.com", OptInStatus: "opt-in-not-required"},
		{RegionName: "eu-west-1", RegionEndpoint: "ec2.eu-west-1.amazonaws.com", OptInStatus: "opt-in-not-required"},
		{RegionName: "eu-central-1", RegionEndpoint: "ec2.eu-central-1.amazonaws.com", OptInStatus: "opt-in-not-required"},
		{RegionName: "ap-southeast-1", RegionEndpoint: "ec2.ap-southeast-1.amazonaws.com", OptInStatus: "opt-in-not-required"},
		{RegionName: "ap-northeast-1", RegionEndpoint: "ec2.ap-northeast-1.amazonaws.com", OptInStatus: "opt-in-not-required"},
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeRegionsResponse{
		Xmlns:      ec2XMLNS,
		RequestID:  protocol.RequestIDFromContext(r.Context()),
		RegionInfo: regions,
	})
}

// ── DescribeAvailabilityZones ────────────────────────────────────────────────

type xmlDescribeAZsResponse struct {
	XMLName              xml.Name `xml:"DescribeAvailabilityZonesResponse"`
	Xmlns                string   `xml:"xmlns,attr"`
	RequestID            string   `xml:"requestId"`
	AvailabilityZoneInfo []xmlAZ  `xml:"availabilityZoneInfo>item"`
}

type xmlAZ struct {
	ZoneName   string `xml:"zoneName"`
	ZoneState  string `xml:"zoneState"`
	RegionName string `xml:"regionName"`
}

// DescribeAvailabilityZones returns the AZs for the configured region.
func (h *Handler) DescribeAvailabilityZones(w http.ResponseWriter, r *http.Request) {
	region := h.cfg.Region
	azs := []xmlAZ{
		{ZoneName: region + "a", ZoneState: "available", RegionName: region},
		{ZoneName: region + "b", ZoneState: "available", RegionName: region},
		{ZoneName: region + "c", ZoneState: "available", RegionName: region},
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeAZsResponse{
		Xmlns:                ec2XMLNS,
		RequestID:            protocol.RequestIDFromContext(r.Context()),
		AvailabilityZoneInfo: azs,
	})
}

// DescribeInstances is implemented in handler_instances.go.

// ── DescribeInstanceTypes ────────────────────────────────────────────────────

type xmlDescribeInstanceTypesResponse struct {
	XMLName           xml.Name              `xml:"DescribeInstanceTypesResponse"`
	Xmlns             string                `xml:"xmlns,attr"`
	RequestID         string                `xml:"requestId"`
	InstanceTypeItems []xmlInstanceTypeItem `xml:"instanceTypeSet>item"`
}

type xmlInstanceTypeItem struct {
	InstanceType      string        `xml:"instanceType"`
	CurrentGeneration bool          `xml:"currentGeneration"`
	VCpuInfo          xmlVCpuInfo   `xml:"vCpuInfo"`
	MemoryInfo        xmlMemoryInfo `xml:"memoryInfo"`
}

type xmlVCpuInfo struct {
	DefaultVCpus int `xml:"defaultVCpus"`
}

type xmlMemoryInfo struct {
	SizeInMiB int `xml:"sizeInMiB"`
}

// DescribeInstanceTypes returns info for requested instance types.
func (h *Handler) DescribeInstanceTypes(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		protocol.WriteQueryXMLError(w, r, protocol.ErrInvalidArgument("invalid request form encoding"))
		return
	}
	// Collect requested types from InstanceType.N query parameters.
	var items []xmlInstanceTypeItem
	requested := map[string]bool{}
	for k, vals := range r.Form {
		if strings.HasPrefix(k, "InstanceType.") {
			for _, v := range vals {
				requested[v] = true
			}
		}
	}
	// If none specified, return empty set.
	if len(requested) == 0 {
		protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeInstanceTypesResponse{
			Xmlns:     ec2XMLNS,
			RequestID: protocol.RequestIDFromContext(r.Context()),
		})
		return
	}
	// Return info for each requested type.
	typeInfo := map[string]xmlInstanceTypeItem{
		"t3.micro":  {InstanceType: "t3.micro", CurrentGeneration: true, VCpuInfo: xmlVCpuInfo{2}, MemoryInfo: xmlMemoryInfo{1024}},
		"t3.small":  {InstanceType: "t3.small", CurrentGeneration: true, VCpuInfo: xmlVCpuInfo{2}, MemoryInfo: xmlMemoryInfo{2048}},
		"t3.medium": {InstanceType: "t3.medium", CurrentGeneration: true, VCpuInfo: xmlVCpuInfo{2}, MemoryInfo: xmlMemoryInfo{4096}},
		"m5.large":  {InstanceType: "m5.large", CurrentGeneration: true, VCpuInfo: xmlVCpuInfo{2}, MemoryInfo: xmlMemoryInfo{8192}},
		"m5.xlarge": {InstanceType: "m5.xlarge", CurrentGeneration: true, VCpuInfo: xmlVCpuInfo{4}, MemoryInfo: xmlMemoryInfo{16384}},
	}
	for t := range requested {
		if info, ok := typeInfo[t]; ok {
			items = append(items, info)
		} else {
			// Return a generic entry for unknown types.
			items = append(items, xmlInstanceTypeItem{
				InstanceType:      t,
				CurrentGeneration: false,
				VCpuInfo:          xmlVCpuInfo{1},
				MemoryInfo:        xmlMemoryInfo{512},
			})
		}
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeInstanceTypesResponse{
		Xmlns:             ec2XMLNS,
		RequestID:         protocol.RequestIDFromContext(r.Context()),
		InstanceTypeItems: items,
	})
}

// ── CreateVpc ────────────────────────────────────────────────────────────────

type xmlCreateVpcResponse struct {
	XMLName   xml.Name `xml:"CreateVpcResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Vpc       xmlVpc   `xml:"vpc"`
}

type xmlVpc struct {
	VpcID                   string         `xml:"vpcId"`
	State                   string         `xml:"state"`
	CidrBlock               string         `xml:"cidrBlock"`
	DhcpOptionsID           string         `xml:"dhcpOptionsId"`
	InstanceTenancy         string         `xml:"instanceTenancy"`
	IsDefault               bool           `xml:"isDefault"`
	CidrBlockAssociationSet []xmlCidrAssoc `xml:"cidrBlockAssociationSet>item"`
	TagSet                  []xmlTag       `xml:"tagSet>item,omitempty"`
}

type xmlCidrAssoc struct {
	AssociationID  string       `xml:"associationId"`
	CidrBlock      string       `xml:"cidrBlock"`
	CidrBlockState xmlCidrState `xml:"cidrBlockState"`
}

type xmlCidrState struct {
	State string `xml:"state"`
}

// publish emits an event if the bus is wired.
func (h *Handler) publish(r *http.Request, t events.Type, payload any) {
	if h.bus != nil {
		h.bus.Publish(r.Context(), events.Event{Type: t, Payload: payload})
	}
}

// CreateVpc creates a new VPC.
func (h *Handler) CreateVpc(w http.ResponseWriter, r *http.Request) {
	cidr := r.FormValue("CidrBlock")
	if cidr == "" {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "CidrBlock is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	vpcID := fmt.Sprintf("vpc-%s", shortID())
	vpc := &VPC{VpcID: vpcID, CidrBlock: cidr, State: "available", CreateTime: h.clk.Now().UnixMilli()}

	// Strategy decides whether to create a new Docker network or share one
	// from another VPC with the same CIDR. EnsureNetwork mutates vpc in place.
	if aerr := h.vpcStrategy.EnsureNetwork(r.Context(), vpc); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	if aerr := h.store.putVPC(r.Context(), vpc); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	h.publish(r, events.EC2VpcCreated, events.ResourcePayload{Name: vpcID})
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlCreateVpcResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Vpc: xmlVpc{
			VpcID:           vpcID,
			State:           "available",
			CidrBlock:       cidr,
			DhcpOptionsID:   fmt.Sprintf("dopt-%s", shortID()),
			InstanceTenancy: "default",
			IsDefault:       false,
			CidrBlockAssociationSet: []xmlCidrAssoc{
				{
					AssociationID:  fmt.Sprintf("vpc-cidr-assoc-%s", shortID()),
					CidrBlock:      cidr,
					CidrBlockState: xmlCidrState{State: "associated"},
				},
			},
		},
	})
}

// ── DescribeVpcs ─────────────────────────────────────────────────────────────

type xmlDescribeVpcsResponse struct {
	XMLName   xml.Name `xml:"DescribeVpcsResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	VpcSet    []xmlVpc `xml:"vpcSet>item"`
}

// DescribeVpcs returns all VPCs.
func (h *Handler) DescribeVpcs(w http.ResponseWriter, r *http.Request) {
	vpcs, aerr := h.store.listVPCs(r.Context())
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	items := make([]xmlVpc, 0, len(vpcs))
	for _, v := range vpcs {
		ns := v.NetworkStatus
		if ns == "" {
			ns = vpcNetworkStatusOK
		}
		items = append(items, xmlVpc{
			VpcID:           v.VpcID,
			State:           v.State,
			CidrBlock:       v.CidrBlock,
			InstanceTenancy: "default",
			IsDefault:       false,
			TagSet: []xmlTag{
				{Key: "overcast:network-status", Value: ns},
			},
		})
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeVpcsResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		VpcSet:    items,
	})
}

// ── DeleteVpc ────────────────────────────────────────────────────────────────

type xmlDeleteVpcResponse struct {
	XMLName   xml.Name `xml:"DeleteVpcResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

// DeleteVpc deletes a VPC by ID.
func (h *Handler) DeleteVpc(w http.ResponseWriter, r *http.Request) {
	vpcID := r.FormValue("VpcId")
	if vpcID == "" {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "VpcId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	// Fetch VPC before deleting so we can clean up the Docker network.
	vpc, _ := h.store.getVPC(r.Context(), vpcID)

	if aerr := h.store.deleteVPC(r.Context(), vpcID); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	// Strategy decides whether to actually tear down the Docker network
	// (shared strategies keep it alive while other VPCs still use it).
	if vpc != nil {
		h.vpcStrategy.OnDelete(r.Context(), vpc)
	}

	h.publish(r, events.EC2VpcDeleted, events.ResourcePayload{Name: vpcID})
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDeleteVpcResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Return:    true,
	})
}

// ── CreateSubnet ─────────────────────────────────────────────────────────────

type xmlCreateSubnetResponse struct {
	XMLName   xml.Name  `xml:"CreateSubnetResponse"`
	Xmlns     string    `xml:"xmlns,attr"`
	RequestID string    `xml:"requestId"`
	Subnet    xmlSubnet `xml:"subnet"`
}

type xmlSubnet struct {
	SubnetID                string `xml:"subnetId"`
	State                   string `xml:"state"`
	VpcID                   string `xml:"vpcId"`
	CidrBlock               string `xml:"cidrBlock"`
	AvailabilityZone        string `xml:"availabilityZone"`
	AvailableIPAddressCount int    `xml:"availableIpAddressCount"`
	DefaultForAz            bool   `xml:"defaultForAz"`
	MapPublicIPOnLaunch     bool   `xml:"mapPublicIpOnLaunch"`
}

// CreateSubnet creates a new subnet in a VPC.
func (h *Handler) CreateSubnet(w http.ResponseWriter, r *http.Request) {
	vpcID := r.FormValue("VpcId")
	cidr := r.FormValue("CidrBlock")
	if vpcID == "" || cidr == "" {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "VpcId and CidrBlock are required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	// Validate VPC exists.
	if _, aerr := h.store.getVPC(r.Context(), vpcID); aerr != nil {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "InvalidVpcID.NotFound",
			Message:    fmt.Sprintf("The vpc ID '%s' does not exist", vpcID),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	subnetID := fmt.Sprintf("subnet-%s", shortID())
	az := h.cfg.Region + "a"
	subnet := &Subnet{SubnetID: subnetID, VpcID: vpcID, CidrBlock: cidr, AvailabilityZone: az, State: "available"}
	if aerr := h.store.putSubnet(r.Context(), subnet); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	h.publish(r, events.EC2SubnetCreated, events.ResourcePayload{Name: subnetID})
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlCreateSubnetResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Subnet: xmlSubnet{
			SubnetID:                subnetID,
			State:                   "available",
			VpcID:                   vpcID,
			CidrBlock:               cidr,
			AvailabilityZone:        az,
			AvailableIPAddressCount: 251,
			DefaultForAz:            false,
			MapPublicIPOnLaunch:     false,
		},
	})
}

// ── DeleteSubnet ─────────────────────────────────────────────────────────────

type xmlDeleteSubnetResponse struct {
	XMLName   xml.Name `xml:"DeleteSubnetResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

// DeleteSubnet deletes a subnet by ID.
func (h *Handler) DeleteSubnet(w http.ResponseWriter, r *http.Request) {
	subnetID := r.FormValue("SubnetId")
	if subnetID == "" {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "SubnetId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	if aerr := h.store.deleteSubnet(r.Context(), subnetID); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	h.publish(r, events.EC2SubnetDeleted, events.ResourcePayload{Name: subnetID})
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDeleteSubnetResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Return:    true,
	})
}

// ── CreateSecurityGroup ──────────────────────────────────────────────────────

type xmlCreateSGResponse struct {
	XMLName   xml.Name `xml:"CreateSecurityGroupResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
	GroupID   string   `xml:"groupId"`
}

// CreateSecurityGroup creates a new VPC security group.
func (h *Handler) CreateSecurityGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("GroupName")
	desc := r.FormValue("GroupDescription")
	vpcID := r.FormValue("VpcId")
	if name == "" {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "GroupName is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	groupID := fmt.Sprintf("sg-%s", shortID())
	sg := &SecurityGroup{
		GroupID:     groupID,
		GroupName:   name,
		Description: desc,
		VpcID:       vpcID,
		IpPermissionsEgress: []IpPermission{{
			IpProtocol: "-1",
			IpRanges:   []IpRange{{CidrIp: "0.0.0.0/0"}},
		}},
	}
	if aerr := h.store.putSecurityGroup(r.Context(), sg); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	h.publish(r, events.EC2SecurityGroupCreated, events.ResourcePayload{Name: name})
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlCreateSGResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Return:    true,
		GroupID:   groupID,
	})
}

// ── DeleteSecurityGroup ──────────────────────────────────────────────────────

type xmlDeleteSGResponse struct {
	XMLName   xml.Name `xml:"DeleteSecurityGroupResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

// DeleteSecurityGroup deletes a security group by ID.
func (h *Handler) DeleteSecurityGroup(w http.ResponseWriter, r *http.Request) {
	groupID := r.FormValue("GroupId")
	if groupID == "" {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "GroupId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	if aerr := h.store.deleteSecurityGroup(r.Context(), groupID); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	h.publish(r, events.EC2SecurityGroupDeleted, events.ResourcePayload{Name: groupID})
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDeleteSGResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Return:    true,
	})
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// shortID generates an 8-character hex short ID from a UUID.
func shortID() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
}
