package cloudformation

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"

	"github.com/Neaox/overcast/internal/config"
)

// ── XML response types for EC2 ─────────────────────────────────────────────

type ec2CreateVpcResponse struct {
	XMLName xml.Name `xml:"CreateVpcResponse"`
	Vpc     struct {
		VpcID     string `xml:"vpcId"`
		CidrBlock string `xml:"cidrBlock"`
		State     string `xml:"state"`
	} `xml:"vpc"`
}

type ec2CreateSubnetResponse struct {
	XMLName xml.Name `xml:"CreateSubnetResponse"`
	Subnet  struct {
		SubnetID         string `xml:"subnetId"`
		VpcID            string `xml:"vpcId"`
		CidrBlock        string `xml:"cidrBlock"`
		AvailabilityZone string `xml:"availabilityZone"`
	} `xml:"subnet"`
}

type ec2CreateSGResponse struct {
	XMLName xml.Name `xml:"CreateSecurityGroupResponse"`
	GroupID string   `xml:"groupId"`
}

type ec2CreateIGWResponse struct {
	XMLName         xml.Name `xml:"CreateInternetGatewayResponse"`
	InternetGateway struct {
		InternetGatewayID string `xml:"internetGatewayId"`
	} `xml:"internetGateway"`
}

type ec2CreateRouteTableResponse struct {
	XMLName    xml.Name `xml:"CreateRouteTableResponse"`
	RouteTable struct {
		RouteTableID string `xml:"routeTableId"`
		VpcID        string `xml:"vpcId"`
	} `xml:"routeTable"`
}

type ec2AllocateAddressResponse struct {
	XMLName      xml.Name `xml:"AllocateAddressResponse"`
	PublicIP     string   `xml:"publicIp"`
	AllocationID string   `xml:"allocationId"`
	Domain       string   `xml:"domain"`
}

type ec2CreateNatGatewayResponse struct {
	XMLName    xml.Name `xml:"CreateNatGatewayResponse"`
	NatGateway struct {
		NatGatewayID string `xml:"natGatewayId"`
		SubnetID     string `xml:"subnetId"`
		VpcID        string `xml:"vpcId"`
		State        string `xml:"state"`
	} `xml:"natGateway"`
}

type ec2AssociateRouteTableResponse struct {
	XMLName       xml.Name `xml:"AssociateRouteTableResponse"`
	AssociationID string   `xml:"newAssociationId"`
}

// ── AWS::EC2::VPC ──────────────────────────────────────────────────────────

type ec2VPCHandler struct{}

func (h *ec2VPCHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	cidr, _ := props["CidrBlock"].(string)
	if cidr == "" {
		cidr = "10.0.0.0/16"
	}

	params := map[string]string{
		"Action":  "CreateVpc",
		"Version": "2016-11-15",
	}
	params["CidrBlock"] = cidr
	if v, ok := props["InstanceTenancy"].(string); ok {
		params["InstanceTenancy"] = v
	}

	rec, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("CreateVpc: %w", err)
	}

	var resp ec2CreateVpcResponse
	if err := xml.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateVpc: parse response: %w", err)
	}

	attrs := map[string]string{
		"VpcId":     resp.Vpc.VpcID,
		"CidrBlock": cidr,
	}
	return resp.Vpc.VpcID, attrs, nil
}

func (h *ec2VPCHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	params := map[string]string{
		"Action":  "DeleteVpc",
		"Version": "2016-11-15",
		"VpcId":   physicalID,
	}
	_, err := internalQuery(ctx, router, rCtx.Region, params)
	return err
}

// ── AWS::EC2::Subnet ───────────────────────────────────────────────────────

type ec2SubnetHandler struct{}

func (h *ec2SubnetHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	params := map[string]string{
		"Action":  "CreateSubnet",
		"Version": "2016-11-15",
	}
	if v, _ := props["VpcId"].(string); v != "" {
		params["VpcId"] = v
	}
	if v, _ := props["CidrBlock"].(string); v != "" {
		params["CidrBlock"] = v
	}
	if v, _ := props["AvailabilityZone"].(string); v != "" {
		params["AvailabilityZone"] = v
	}

	rec, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("CreateSubnet: %w", err)
	}

	var resp ec2CreateSubnetResponse
	if err := xml.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateSubnet: parse response: %w", err)
	}

	attrs := map[string]string{
		"SubnetId":         resp.Subnet.SubnetID,
		"VpcId":            resp.Subnet.VpcID,
		"CidrBlock":        resp.Subnet.CidrBlock,
		"AvailabilityZone": resp.Subnet.AvailabilityZone,
	}
	return resp.Subnet.SubnetID, attrs, nil
}

func (h *ec2SubnetHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	params := map[string]string{
		"Action":   "DeleteSubnet",
		"Version":  "2016-11-15",
		"SubnetId": physicalID,
	}
	_, err := internalQuery(ctx, router, rCtx.Region, params)
	return err
}

// ── AWS::EC2::SecurityGroup ────────────────────────────────────────────────

type ec2SecurityGroupHandler struct{}

func (h *ec2SecurityGroupHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	groupName, _ := props["GroupName"].(string)
	if groupName == "" {
		groupName = fmt.Sprintf("%s-sg", rCtx.StackName)
	}
	groupDesc, _ := props["GroupDescription"].(string)
	if groupDesc == "" {
		groupDesc = groupName
	}

	params := map[string]string{
		"Action":           "CreateSecurityGroup",
		"Version":          "2016-11-15",
		"GroupName":        groupName,
		"GroupDescription": groupDesc,
	}
	if v, _ := props["VpcId"].(string); v != "" {
		params["VpcId"] = v
	}

	rec, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("CreateSecurityGroup: %w", err)
	}

	var resp ec2CreateSGResponse
	if err := xml.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateSecurityGroup: parse response: %w", err)
	}

	groupID := resp.GroupID

	// Add inline ingress rules if specified.
	if rules, ok := props["SecurityGroupIngress"].([]any); ok {
		for i, rule := range rules {
			ruleMap, ok := rule.(map[string]any)
			if !ok {
				continue
			}
			p := map[string]string{
				"Action":  "AuthorizeSecurityGroupIngress",
				"Version": "2016-11-15",
				"GroupId": groupID,
			}
			if v, _ := ruleMap["IpProtocol"].(string); v != "" {
				p["IpPermissions.1.IpProtocol"] = v
			}
			if v := fmt.Sprintf("%v", ruleMap["FromPort"]); v != "" && v != "<nil>" {
				p["IpPermissions.1.FromPort"] = v
			}
			if v := fmt.Sprintf("%v", ruleMap["ToPort"]); v != "" && v != "<nil>" {
				p["IpPermissions.1.ToPort"] = v
			}
			if cidr, _ := ruleMap["CidrIp"].(string); cidr != "" {
				p["IpPermissions.1.IpRanges.1.CidrIp"] = cidr
			}
			if _, err := internalQuery(ctx, router, rCtx.Region, p); err != nil {
				return "", nil, fmt.Errorf("AuthorizeSecurityGroupIngress rule %d: %w", i, err)
			}
		}
	}

	// Add inline egress rules if specified.
	if rules, ok := props["SecurityGroupEgress"].([]any); ok {
		for i, rule := range rules {
			ruleMap, ok := rule.(map[string]any)
			if !ok {
				continue
			}
			p := map[string]string{
				"Action":  "AuthorizeSecurityGroupEgress",
				"Version": "2016-11-15",
				"GroupId": groupID,
			}
			if v, _ := ruleMap["IpProtocol"].(string); v != "" {
				p["IpPermissions.1.IpProtocol"] = v
			}
			if v := fmt.Sprintf("%v", ruleMap["FromPort"]); v != "" && v != "<nil>" {
				p["IpPermissions.1.FromPort"] = v
			}
			if v := fmt.Sprintf("%v", ruleMap["ToPort"]); v != "" && v != "<nil>" {
				p["IpPermissions.1.ToPort"] = v
			}
			if cidr, _ := ruleMap["CidrIp"].(string); cidr != "" {
				p["IpPermissions.1.IpRanges.1.CidrIp"] = cidr
			}
			if _, err := internalQuery(ctx, router, rCtx.Region, p); err != nil {
				return "", nil, fmt.Errorf("AuthorizeSecurityGroupEgress rule %d: %w", i, err)
			}
		}
	}

	vpcID, _ := props["VpcId"].(string)
	attrs := map[string]string{
		"GroupId": groupID,
		"VpcId":   vpcID,
	}
	return groupID, attrs, nil
}

func (h *ec2SecurityGroupHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	params := map[string]string{
		"Action":  "DeleteSecurityGroup",
		"Version": "2016-11-15",
		"GroupId": physicalID,
	}
	_, err := internalQuery(ctx, router, rCtx.Region, params)
	return err
}

// ── AWS::EC2::InternetGateway ──────────────────────────────────────────────

type ec2InternetGatewayHandler struct{}

func (h *ec2InternetGatewayHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	params := map[string]string{
		"Action":  "CreateInternetGateway",
		"Version": "2016-11-15",
	}

	rec, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("CreateInternetGateway: %w", err)
	}

	var resp ec2CreateIGWResponse
	if err := xml.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateInternetGateway: parse response: %w", err)
	}

	igwID := resp.InternetGateway.InternetGatewayID
	attrs := map[string]string{
		"InternetGatewayId": igwID,
	}
	return igwID, attrs, nil
}

func (h *ec2InternetGatewayHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	params := map[string]string{
		"Action":            "DeleteInternetGateway",
		"Version":           "2016-11-15",
		"InternetGatewayId": physicalID,
	}
	_, err := internalQuery(ctx, router, rCtx.Region, params)
	return err
}

// ── AWS::EC2::VPCGatewayAttachment ─────────────────────────────────────────

type ec2VPCGatewayAttachmentHandler struct{}

func (h *ec2VPCGatewayAttachmentHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	vpcID, _ := props["VpcId"].(string)
	igwID, _ := props["InternetGatewayId"].(string)

	params := map[string]string{
		"Action":            "AttachInternetGateway",
		"Version":           "2016-11-15",
		"InternetGatewayId": igwID,
		"VpcId":             vpcID,
	}

	if _, err := internalQuery(ctx, router, rCtx.Region, params); err != nil {
		return "", nil, fmt.Errorf("AttachInternetGateway: %w", err)
	}

	// Store both IDs so we can detach on delete.
	physicalID := vpcID + "|" + igwID
	return physicalID, nil, nil
}

func (h *ec2VPCGatewayAttachmentHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts := strings.SplitN(physicalID, "|", 2)
	if len(parts) != 2 {
		return fmt.Errorf("VPCGatewayAttachment: invalid physical ID: %s", physicalID)
	}
	params := map[string]string{
		"Action":            "DetachInternetGateway",
		"Version":           "2016-11-15",
		"InternetGatewayId": parts[1],
		"VpcId":             parts[0],
	}
	_, err := internalQuery(ctx, router, rCtx.Region, params)
	return err
}

// ── AWS::EC2::RouteTable ───────────────────────────────────────────────────

type ec2RouteTableHandler struct{}

func (h *ec2RouteTableHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	params := map[string]string{
		"Action":  "CreateRouteTable",
		"Version": "2016-11-15",
	}
	if v, _ := props["VpcId"].(string); v != "" {
		params["VpcId"] = v
	}

	rec, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("CreateRouteTable: %w", err)
	}

	var resp ec2CreateRouteTableResponse
	if err := xml.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateRouteTable: parse response: %w", err)
	}

	rtID := resp.RouteTable.RouteTableID
	attrs := map[string]string{
		"RouteTableId": rtID,
	}
	return rtID, attrs, nil
}

func (h *ec2RouteTableHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	params := map[string]string{
		"Action":       "DeleteRouteTable",
		"Version":      "2016-11-15",
		"RouteTableId": physicalID,
	}
	_, err := internalQuery(ctx, router, rCtx.Region, params)
	return err
}

// ── AWS::EC2::Route ────────────────────────────────────────────────────────

type ec2RouteHandler struct{}

func (h *ec2RouteHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	rtID, _ := props["RouteTableId"].(string)
	destCidr, _ := props["DestinationCidrBlock"].(string)

	params := map[string]string{
		"Action":               "CreateRoute",
		"Version":              "2016-11-15",
		"RouteTableId":         rtID,
		"DestinationCidrBlock": destCidr,
	}
	if v, _ := props["GatewayId"].(string); v != "" {
		params["GatewayId"] = v
	}
	if v, _ := props["NatGatewayId"].(string); v != "" {
		params["NatGatewayId"] = v
	}

	if _, err := internalQuery(ctx, router, rCtx.Region, params); err != nil {
		return "", nil, fmt.Errorf("CreateRoute: %w", err)
	}

	// Physical ID: compose route table + destination for delete.
	physicalID := rtID + "|" + destCidr
	return physicalID, nil, nil
}

func (h *ec2RouteHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts := strings.SplitN(physicalID, "|", 2)
	if len(parts) != 2 {
		return fmt.Errorf("Route: invalid physical ID: %s", physicalID)
	}
	params := map[string]string{
		"Action":               "DeleteRoute",
		"Version":              "2016-11-15",
		"RouteTableId":         parts[0],
		"DestinationCidrBlock": parts[1],
	}
	_, err := internalQuery(ctx, router, rCtx.Region, params)
	return err
}

// ── AWS::EC2::SubnetRouteTableAssociation ──────────────────────────────────

type ec2SubnetRouteTableAssociationHandler struct{}

func (h *ec2SubnetRouteTableAssociationHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	params := map[string]string{
		"Action":  "AssociateRouteTable",
		"Version": "2016-11-15",
	}
	if v, _ := props["RouteTableId"].(string); v != "" {
		params["RouteTableId"] = v
	}
	if v, _ := props["SubnetId"].(string); v != "" {
		params["SubnetId"] = v
	}

	rec, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("AssociateRouteTable: %w", err)
	}

	var resp ec2AssociateRouteTableResponse
	if err := xml.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("AssociateRouteTable: parse response: %w", err)
	}

	return resp.AssociationID, nil, nil
}

func (h *ec2SubnetRouteTableAssociationHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	params := map[string]string{
		"Action":        "DisassociateRouteTable",
		"Version":       "2016-11-15",
		"AssociationId": physicalID,
	}
	_, err := internalQuery(ctx, router, rCtx.Region, params)
	return err
}

// ── AWS::EC2::EIP ──────────────────────────────────────────────────────────

type ec2EIPHandler struct{}

func (h *ec2EIPHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	params := map[string]string{
		"Action":  "AllocateAddress",
		"Version": "2016-11-15",
		"Domain":  "vpc",
	}

	rec, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("AllocateAddress: %w", err)
	}

	var resp ec2AllocateAddressResponse
	if err := xml.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("AllocateAddress: parse response: %w", err)
	}

	attrs := map[string]string{
		"AllocationId": resp.AllocationID,
		"PublicIp":     resp.PublicIP,
	}
	return resp.AllocationID, attrs, nil
}

func (h *ec2EIPHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	params := map[string]string{
		"Action":       "ReleaseAddress",
		"Version":      "2016-11-15",
		"AllocationId": physicalID,
	}
	_, err := internalQuery(ctx, router, rCtx.Region, params)
	return err
}

// ── AWS::EC2::NatGateway ───────────────────────────────────────────────────

type ec2NatGatewayHandler struct{}

func (h *ec2NatGatewayHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	params := map[string]string{
		"Action":  "CreateNatGateway",
		"Version": "2016-11-15",
	}
	if v, _ := props["SubnetId"].(string); v != "" {
		params["SubnetId"] = v
	}
	if v, _ := props["AllocationId"].(string); v != "" {
		params["AllocationId"] = v
	}

	rec, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("CreateNatGateway: %w", err)
	}

	var resp ec2CreateNatGatewayResponse
	if err := xml.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateNatGateway: parse response: %w", err)
	}

	natID := resp.NatGateway.NatGatewayID
	attrs := map[string]string{
		"NatGatewayId": natID,
	}
	return natID, attrs, nil
}

func (h *ec2NatGatewayHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	params := map[string]string{
		"Action":       "DeleteNatGateway",
		"Version":      "2016-11-15",
		"NatGatewayId": physicalID,
	}
	_, err := internalQuery(ctx, router, rCtx.Region, params)
	return err
}
