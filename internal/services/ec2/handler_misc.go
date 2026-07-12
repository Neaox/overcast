package ec2

// handler_misc.go — ModifySubnetAttribute, ModifyVpcAttribute,
// DescribeDhcpOptions, DescribeAccountAttributes, DescribeVpcAttribute,
// DescribeVpnGateways, CreateNetworkInterface, DescribeNetworkInterfaces,
// DeleteNetworkInterface handlers.

import (
	"encoding/xml"
	"fmt"
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
)

// ── ModifySubnetAttribute ───────────────────────────────────────────────────

type xmlModifySubnetAttributeResponse struct {
	XMLName   xml.Name `xml:"ModifySubnetAttributeResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

// ModifySubnetAttribute modifies a subnet attribute (metadata-only).
func (h *Handler) ModifySubnetAttribute(w http.ResponseWriter, r *http.Request) {
	subnetID := r.FormValue("SubnetId")
	if subnetID == "" {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "SubnetId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	// Validate subnet exists.
	if _, aerr := h.store.getSubnet(r.Context(), subnetID); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	// Attributes are metadata-only — acknowledge the change.
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlModifySubnetAttributeResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Return:    true,
	})
}

// ── ModifyVpcAttribute ──────────────────────────────────────────────────────

type xmlModifyVpcAttributeResponse struct {
	XMLName   xml.Name `xml:"ModifyVpcAttributeResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

// ModifyVpcAttribute modifies a VPC attribute (metadata-only: EnableDnsSupport, EnableDnsHostnames).
func (h *Handler) ModifyVpcAttribute(w http.ResponseWriter, r *http.Request) {
	vpcID := r.FormValue("VpcId")
	if vpcID == "" {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "VpcId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	// Validate VPC exists.
	if _, aerr := h.store.getVPC(r.Context(), vpcID); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	// DNS attributes are metadata-only — acknowledge the change.
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlModifyVpcAttributeResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Return:    true,
	})
}

// ── DescribeVpcAttribute ────────────────────────────────────────────────────

type xmlDescribeVpcAttributeResponse struct {
	XMLName            xml.Name         `xml:"DescribeVpcAttributeResponse"`
	Xmlns              string           `xml:"xmlns,attr"`
	RequestID          string           `xml:"requestId"`
	VpcID              string           `xml:"vpcId"`
	EnableDnsSupport   *xmlAttributeVal `xml:"enableDnsSupport,omitempty"`
	EnableDnsHostnames *xmlAttributeVal `xml:"enableDnsHostnames,omitempty"`
}

type xmlAttributeVal struct {
	Value bool `xml:"value"`
}

// DescribeVpcAttribute returns a VPC attribute value (always true for DNS attributes).
func (h *Handler) DescribeVpcAttribute(w http.ResponseWriter, r *http.Request) {
	vpcID := r.FormValue("VpcId")
	attr := r.FormValue("Attribute")
	if vpcID == "" {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "VpcId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	if _, aerr := h.store.getVPC(r.Context(), vpcID); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	resp := &xmlDescribeVpcAttributeResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		VpcID:     vpcID,
	}
	switch attr {
	case "enableDnsSupport":
		resp.EnableDnsSupport = &xmlAttributeVal{Value: true}
	case "enableDnsHostnames":
		resp.EnableDnsHostnames = &xmlAttributeVal{Value: true}
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, resp)
}

// ── DescribeDhcpOptions ─────────────────────────────────────────────────────

type xmlDescribeDhcpOptionsResponse struct {
	XMLName        xml.Name        `xml:"DescribeDhcpOptionsResponse"`
	Xmlns          string          `xml:"xmlns,attr"`
	RequestID      string          `xml:"requestId"`
	DhcpOptionsSet []xmlDhcpOption `xml:"dhcpOptionsSet>item"`
}

type xmlDhcpOption struct {
	DhcpOptionsID        string                 `xml:"dhcpOptionsId"`
	DhcpConfigurationSet []xmlDhcpConfiguration `xml:"dhcpConfigurationSet>item"`
}

type xmlDhcpConfiguration struct {
	Key      string         `xml:"key"`
	ValueSet []xmlDhcpValue `xml:"valueSet>item"`
}

type xmlDhcpValue struct {
	Value string `xml:"value"`
}

// DescribeDhcpOptions returns a default DHCP options set.
func (h *Handler) DescribeDhcpOptions(w http.ResponseWriter, r *http.Request) {
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeDhcpOptionsResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		DhcpOptionsSet: []xmlDhcpOption{{
			DhcpOptionsID: fmt.Sprintf("dopt-%s", shortID()),
			DhcpConfigurationSet: []xmlDhcpConfiguration{
				{Key: "domain-name", ValueSet: []xmlDhcpValue{{Value: "ec2.internal"}}},
				{Key: "domain-name-servers", ValueSet: []xmlDhcpValue{{Value: "AmazonProvidedDNS"}}},
			},
		}},
	})
}

// ── DescribeAccountAttributes ───────────────────────────────────────────────

type xmlDescribeAccountAttributesResponse struct {
	XMLName             xml.Name              `xml:"DescribeAccountAttributesResponse"`
	Xmlns               string                `xml:"xmlns,attr"`
	RequestID           string                `xml:"requestId"`
	AccountAttributeSet []xmlAccountAttribute `xml:"accountAttributeSet>item"`
}

type xmlAccountAttribute struct {
	AttributeName     string                `xml:"attributeName"`
	AttributeValueSet []xmlAccountAttrValue `xml:"attributeValueSet>item"`
}

type xmlAccountAttrValue struct {
	AttributeValue string `xml:"attributeValue"`
}

// DescribeAccountAttributes returns default account attributes.
func (h *Handler) DescribeAccountAttributes(w http.ResponseWriter, r *http.Request) {
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeAccountAttributesResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		AccountAttributeSet: []xmlAccountAttribute{
			{AttributeName: "supported-platforms", AttributeValueSet: []xmlAccountAttrValue{{AttributeValue: "VPC"}}},
			{AttributeName: "default-vpc", AttributeValueSet: []xmlAccountAttrValue{{AttributeValue: "none"}}},
			{AttributeName: "max-instances", AttributeValueSet: []xmlAccountAttrValue{{AttributeValue: "20"}}},
			{AttributeName: "vpc-max-security-groups-per-interface", AttributeValueSet: []xmlAccountAttrValue{{AttributeValue: "5"}}},
			{AttributeName: "max-elastic-ips", AttributeValueSet: []xmlAccountAttrValue{{AttributeValue: "5"}}},
			{AttributeName: "vpc-max-elastic-ips", AttributeValueSet: []xmlAccountAttrValue{{AttributeValue: "5"}}},
		},
	})
}

// ── DescribeVpnGateways ─────────────────────────────────────────────────────

type xmlDescribeVpnGatewaysResponse struct {
	XMLName       xml.Name `xml:"DescribeVpnGatewaysResponse"`
	Xmlns         string   `xml:"xmlns,attr"`
	RequestID     string   `xml:"requestId"`
	VpnGatewaySet struct{} `xml:"vpnGatewaySet"`
}

// DescribeVpnGateways returns no virtual private gateways. Most local VPC/CDK
// lookups only need a well-formed empty result to confirm none are attached.
func (h *Handler) DescribeVpnGateways(w http.ResponseWriter, r *http.Request) {
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeVpnGatewaysResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
	})
}

// ── CreateNetworkInterface ──────────────────────────────────────────────────

type xmlCreateNetworkInterfaceResponse struct {
	XMLName          xml.Name            `xml:"CreateNetworkInterfaceResponse"`
	Xmlns            string              `xml:"xmlns,attr"`
	RequestID        string              `xml:"requestId"`
	NetworkInterface xmlNetworkInterface `xml:"networkInterface"`
}

type xmlNetworkInterface struct {
	NetworkInterfaceID string `xml:"networkInterfaceId"`
	SubnetID           string `xml:"subnetId"`
	VpcID              string `xml:"vpcId"`
	AvailabilityZone   string `xml:"availabilityZone"`
	Description        string `xml:"description"`
	PrivateIPAddress   string `xml:"privateIpAddress"`
	Status             string `xml:"status"`
	MacAddress         string `xml:"macAddress"`
}

// CreateNetworkInterface creates a network interface in a subnet.
func (h *Handler) CreateNetworkInterface(w http.ResponseWriter, r *http.Request) {
	subnetID := r.FormValue("SubnetId")
	desc := r.FormValue("Description")
	if subnetID == "" {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "SubnetId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	sub, aerr := h.store.getSubnet(r.Context(), subnetID)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	if vpc, aerr := h.store.getVPC(r.Context(), sub.VpcID); aerr == nil {
		ns := vpc.NetworkStatus
		if ns == "" {
			ns = vpcNetworkStatusOK
		}
		if ns == vpcNetworkStatusConflict {
			protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
				Code:       "InvalidVpc.NetworkStatus",
				Message:    fmt.Sprintf("VPC %s has network status %q: cannot create network interface", sub.VpcID, ns),
				HTTPStatus: http.StatusBadRequest,
			})
			return
		}
	}

	eniID := fmt.Sprintf("eni-%s", shortID())
	apiPrivateIP, realPrivateIP, _ := h.allocatePrivateIPForSubnet(r.Context(), subnetID)
	mac := fmt.Sprintf("02:%s:%s:%s:%s:%s",
		shortID()[:2], shortID()[:2], shortID()[:2], shortID()[:2], shortID()[:2])

	eni := &NetworkInterface{
		NetworkInterfaceID: eniID,
		SubnetID:           subnetID,
		VpcID:              sub.VpcID,
		AvailabilityZone:   sub.AvailabilityZone,
		Description:        desc,
		PrivateIPAddress:   realPrivateIP,
		Status:             "available",
		MacAddress:         mac,
	}
	if aerr := h.store.putNetworkInterface(r.Context(), eni); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlCreateNetworkInterfaceResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		NetworkInterface: xmlNetworkInterface{
			NetworkInterfaceID: eniID,
			SubnetID:           subnetID,
			VpcID:              sub.VpcID,
			AvailabilityZone:   sub.AvailabilityZone,
			Description:        desc,
			PrivateIPAddress:   apiPrivateIP,
			Status:             "available",
			MacAddress:         mac,
		},
	})
}

// ── DescribeNetworkInterfaces ───────────────────────────────────────────────

type xmlDescribeNetworkInterfacesResponse struct {
	XMLName             xml.Name              `xml:"DescribeNetworkInterfacesResponse"`
	Xmlns               string                `xml:"xmlns,attr"`
	RequestID           string                `xml:"requestId"`
	NetworkInterfaceSet []xmlNetworkInterface `xml:"networkInterfaceSet>item"`
}

// DescribeNetworkInterfaces lists network interfaces with optional filtering.
func (h *Handler) DescribeNetworkInterfaces(w http.ResponseWriter, r *http.Request) {
	all, aerr := h.store.listNetworkInterfaces(r.Context())
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	filterIDs := collectFormValues(r, "NetworkInterfaceId.")
	filters := collectFormFilters(r)

	items := make([]xmlNetworkInterface, 0, len(all))
	for _, eni := range all {
		if len(filterIDs) > 0 && !containsStr(filterIDs, eni.NetworkInterfaceID) {
			continue
		}
		if !matchFilters(filters, map[string]string{
			"network-interface-id": eni.NetworkInterfaceID,
			"subnet-id":            eni.SubnetID,
			"vpc-id":               eni.VpcID,
		}) {
			continue
		}
		items = append(items, xmlNetworkInterface{
			NetworkInterfaceID: eni.NetworkInterfaceID,
			SubnetID:           eni.SubnetID,
			VpcID:              eni.VpcID,
			AvailabilityZone:   eni.AvailabilityZone,
			Description:        eni.Description,
			PrivateIPAddress:   h.privateIPForAPI(r.Context(), eni.VpcID, eni.PrivateIPAddress),
			Status:             eni.Status,
			MacAddress:         eni.MacAddress,
		})
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeNetworkInterfacesResponse{
		Xmlns:               ec2XMLNS,
		RequestID:           protocol.RequestIDFromContext(r.Context()),
		NetworkInterfaceSet: items,
	})
}

// ── DeleteNetworkInterface ──────────────────────────────────────────────────

type xmlDeleteNetworkInterfaceResponse struct {
	XMLName   xml.Name `xml:"DeleteNetworkInterfaceResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

// DeleteNetworkInterface deletes a network interface.
func (h *Handler) DeleteNetworkInterface(w http.ResponseWriter, r *http.Request) {
	eniID := r.FormValue("NetworkInterfaceId")
	if eniID == "" {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "NetworkInterfaceId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	if aerr := h.store.deleteNetworkInterface(r.Context(), eniID); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDeleteNetworkInterfaceResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Return:    true,
	})
}
