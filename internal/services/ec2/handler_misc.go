package ec2

// handler_misc.go — ModifySubnetAttribute, ModifyVpcAttribute,
// DescribeDhcpOptions, DescribeAccountAttributes, DescribeVpcAttribute,
// CreateNetworkInterface, DescribeNetworkInterfaces, DeleteNetworkInterface handlers.

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"

	"github.com/overcast-sh/overcast/internal/protocol"
)

// ── ModifySubnetAttribute ───────────────────────────────────────────────────

type xmlModifySubnetAttributeResponse struct {
	XMLName   xml.Name `xml:"ModifySubnetAttributeResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

// ModifySubnetAttribute modifies a subnet attribute. MapPublicIpOnLaunch is
// the only attribute actually persisted; the rest (AssignIpv6AddressOnCreation,
// EnableDns64, etc.) are accepted but not stored — see #529.
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
	sub, aerr := h.store.getSubnet(r.Context(), subnetID)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	if v := r.FormValue("MapPublicIpOnLaunch.Value"); v != "" {
		sub.MapPublicIpOnLaunch = strings.EqualFold(v, "true")
		if aerr := h.store.putSubnet(r.Context(), sub); aerr != nil {
			protocol.WriteEC2QueryXMLError(w, r, aerr)
			return
		}
	}
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

// ModifyVpcAttribute modifies EnableDnsSupport and/or EnableDnsHostnames on a
// VPC. Real AWS accepts only one attribute per call; this handler accepts
// either or both in one request since it is only ever called internally (by
// the CloudFormation provisioner) rather than validated against a client SDK.
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
	vpc, aerr := h.store.getVPC(r.Context(), vpcID)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	changed := false
	if v := r.FormValue("EnableDnsSupport.Value"); v != "" {
		vpc.EnableDnsSupport = strings.EqualFold(v, "true")
		changed = true
	}
	if v := r.FormValue("EnableDnsHostnames.Value"); v != "" {
		vpc.EnableDnsHostnames = strings.EqualFold(v, "true")
		changed = true
	}
	if changed {
		if aerr := h.store.putVPC(r.Context(), vpc); aerr != nil {
			protocol.WriteEC2QueryXMLError(w, r, aerr)
			return
		}
	}
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

// DescribeVpcAttribute returns the stored value of one VPC DNS attribute.
func (h *Handler) DescribeVpcAttribute(w http.ResponseWriter, r *http.Request) {
	resp, aerr := h.describeVpcAttribute(r.Context(), r.FormValue("VpcId"), r.FormValue("Attribute"))
	writeDescribe(w, r, resp, aerr)
}

func (h *Handler) describeVpcAttribute(ctx context.Context, vpcID, attr string) (*xmlDescribeVpcAttributeResponse, *protocol.AWSError) {
	if vpcID == "" {
		return nil, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "VpcId is required",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	vpc, aerr := h.store.getVPC(ctx, vpcID)
	if aerr != nil {
		return nil, aerr
	}

	resp := &xmlDescribeVpcAttributeResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		VpcID:     vpcID,
	}
	switch attr {
	case "enableDnsSupport":
		resp.EnableDnsSupport = &xmlAttributeVal{Value: vpc.EnableDnsSupport}
	case "enableDnsHostnames":
		resp.EnableDnsHostnames = &xmlAttributeVal{Value: vpc.EnableDnsHostnames}
	}
	return resp, nil
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
	resp, aerr := h.describeDhcpOptions(r.Context(), unselectedQuery(r))
	writeDescribe(w, r, resp, aerr)
}

func (h *Handler) describeDhcpOptions(ctx context.Context, q describeQuery) (*xmlDescribeDhcpOptionsResponse, *protocol.AWSError) {
	// The option set below is fabricated per call rather than stored, so there
	// is no record for a filter to select against and dhcpOptionsFilters
	// implements none. Parsing is still what refuses one, rather than accepting
	// it and answering as though it had been applied.
	if _, aerr := dhcpOptionsFilters.parse(q.filters); aerr != nil {
		return nil, aerr
	}
	return &xmlDescribeDhcpOptionsResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		DhcpOptionsSet: []xmlDhcpOption{{
			DhcpOptionsID: fmt.Sprintf("dopt-%s", shortID()),
			DhcpConfigurationSet: []xmlDhcpConfiguration{
				{Key: "domain-name", ValueSet: []xmlDhcpValue{{Value: "ec2.internal"}}},
				{Key: "domain-name-servers", ValueSet: []xmlDhcpValue{{Value: "AmazonProvidedDNS"}}},
			},
		}},
	}, nil
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
	resp, aerr := h.describeAccountAttributes(r.Context())
	writeDescribe(w, r, resp, aerr)
}

func (h *Handler) describeAccountAttributes(ctx context.Context) (*xmlDescribeAccountAttributesResponse, *protocol.AWSError) {
	return &xmlDescribeAccountAttributesResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		AccountAttributeSet: []xmlAccountAttribute{
			{AttributeName: "supported-platforms", AttributeValueSet: []xmlAccountAttrValue{{AttributeValue: "VPC"}}},
			{AttributeName: "default-vpc", AttributeValueSet: []xmlAccountAttrValue{{AttributeValue: "none"}}},
			{AttributeName: "max-instances", AttributeValueSet: []xmlAccountAttrValue{{AttributeValue: "20"}}},
			{AttributeName: "vpc-max-security-groups-per-interface", AttributeValueSet: []xmlAccountAttrValue{{AttributeValue: "5"}}},
			{AttributeName: "max-elastic-ips", AttributeValueSet: []xmlAccountAttrValue{{AttributeValue: "5"}}},
			{AttributeName: "vpc-max-elastic-ips", AttributeValueSet: []xmlAccountAttrValue{{AttributeValue: "5"}}},
		},
	}, nil
}

// ── CreateNetworkInterface ──────────────────────────────────────────────────

type xmlCreateNetworkInterfaceResponse struct {
	XMLName          xml.Name            `xml:"CreateNetworkInterfaceResponse"`
	Xmlns            string              `xml:"xmlns,attr"`
	RequestID        string              `xml:"requestId"`
	NetworkInterface xmlNetworkInterface `xml:"networkInterface"`
}

type xmlNetworkInterface struct {
	NetworkInterfaceID string   `xml:"networkInterfaceId"`
	SubnetID           string   `xml:"subnetId"`
	VpcID              string   `xml:"vpcId"`
	AvailabilityZone   string   `xml:"availabilityZone"`
	Description        string   `xml:"description"`
	PrivateIPAddress   string   `xml:"privateIpAddress"`
	Status             string   `xml:"status"`
	MacAddress         string   `xml:"macAddress"`
	TagSet             []xmlTag `xml:"tagSet>item,omitempty"`
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

	tags := parseTagSpecifications(r, "network-interface")
	// Create-time tags are checked before anything is created, as on AWS: a
	// rejected tag must fail the call rather than leave an interface behind
	// (#1196).
	if aerr := validateTagSpecifications(tags); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
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
	// Create-time tags go to the tag store, the same place CreateTags writes,
	// so a later describe sees both without reading two sources.
	if aerr := h.putResourceTags(r.Context(), eniID, tags); aerr != nil {
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
			TagSet:             xmlTagsOf(tags),
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
	resp, aerr := h.describeNetworkInterfaces(r.Context(), requestQuery(r, "NetworkInterfaceId"))
	writeDescribe(w, r, resp, aerr)
}

func (h *Handler) describeNetworkInterfaces(ctx context.Context, q describeQuery) (*xmlDescribeNetworkInterfacesResponse, *protocol.AWSError) {
	filters, aerr := networkInterfaceFilters.parse(q.filters)
	if aerr != nil {
		return nil, aerr
	}
	requested := q.ids

	all, aerr := h.store.listNetworkInterfaces(ctx)
	if aerr != nil {
		return nil, aerr
	}

	tagsView, aerr := h.tagViewFor(ctx, q.filters, true)
	if aerr != nil {
		return nil, aerr
	}

	items := make([]xmlNetworkInterface, 0, len(all))
	for _, eni := range all {
		if !requested.has(eni.NetworkInterfaceID) || !filters.matches(eni) {
			continue
		}
		tags, ok := tagsView.keep(eni.NetworkInterfaceID)
		if !ok {
			continue
		}
		items = append(items, xmlNetworkInterface{
			NetworkInterfaceID: eni.NetworkInterfaceID,
			SubnetID:           eni.SubnetID,
			VpcID:              eni.VpcID,
			AvailabilityZone:   eni.AvailabilityZone,
			Description:        eni.Description,
			PrivateIPAddress:   h.privateIPForAPI(ctx, eni.VpcID, eni.PrivateIPAddress),
			Status:             eni.Status,
			MacAddress:         eni.MacAddress,
			TagSet:             xmlTagsOf(tags),
		})
	}

	return &xmlDescribeNetworkInterfacesResponse{
		Xmlns:               ec2XMLNS,
		RequestID:           protocol.RequestIDFromContext(ctx),
		NetworkInterfaceSet: items,
	}, nil
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
