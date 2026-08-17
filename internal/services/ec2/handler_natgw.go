package ec2

// handler_natgw.go — CreateNatGateway, DescribeNatGateways, DeleteNatGateway handlers.

import (
	"encoding/xml"
	"fmt"
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
)

// ── CreateNatGateway ────────────────────────────────────────────────────────

type xmlCreateNatGatewayResponse struct {
	XMLName    xml.Name      `xml:"CreateNatGatewayResponse"`
	Xmlns      string        `xml:"xmlns,attr"`
	RequestID  string        `xml:"requestId"`
	NatGateway xmlNatGateway `xml:"natGateway"`
}

type xmlNatGateway struct {
	NatGatewayID string         `xml:"natGatewayId"`
	SubnetID     string         `xml:"subnetId"`
	VpcID        string         `xml:"vpcId"`
	State        string         `xml:"state"`
	CreateTime   string         `xml:"createTime"`
	Addresses    []xmlNatGWAddr `xml:"natGatewayAddressSet>item"`
	Tags         []xmlTag       `xml:"tagSet>item,omitempty"`
}

type xmlNatGWAddr struct {
	AllocationID       string `xml:"allocationId"`
	PublicIP           string `xml:"publicIp,omitempty"`
	PrivateIP          string `xml:"privateIp,omitempty"`
	NetworkInterfaceID string `xml:"networkInterfaceId,omitempty"`
}

// CreateNatGateway creates a NAT gateway in a subnet.
func (h *Handler) CreateNatGateway(w http.ResponseWriter, r *http.Request) {
	subnetID := r.FormValue("SubnetId")
	allocID := r.FormValue("AllocationId")
	if subnetID == "" {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "SubnetId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// Create-time tags are checked before anything is created, as on AWS: a
	// rejected tag must fail the call rather than leave a NAT gateway behind.
	tags := parseTagSpecifications(r, "natgateway")
	if aerr := validateTagSpecifications(tags); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	// Validate subnet and resolve VPC.
	sub, aerr := h.store.getSubnet(r.Context(), subnetID)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "InvalidSubnetID.NotFound",
			Message:    fmt.Sprintf("The subnet ID '%s' does not exist", subnetID),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// Resolve EIP public IP if alloc provided.
	var publicIP string
	if allocID != "" {
		eip, aerr := h.store.getElasticIP(r.Context(), allocID)
		if aerr == nil {
			publicIP = eip.PublicIP
		}
	}

	natID := fmt.Sprintf("nat-%s", shortID())
	now := h.clk.Now().Format("2006-01-02T15:04:05.000Z")

	ngw := &NatGateway{
		NatGatewayID: natID,
		SubnetID:     subnetID,
		VpcID:        sub.VpcID,
		State:        "available",
		AllocationID: allocID,
		PublicIP:     publicIP,
		PrivateIP:    "",
		CreateTime:   now,
	}
	privateIP, _, _ := h.allocatePrivateIPForSubnet(r.Context(), subnetID)
	ngw.PrivateIP = privateIP

	if aerr := h.store.putNatGateway(r.Context(), ngw); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	// Create-time tags go to the tag store, the same place CreateTags writes,
	// so a later describe sees both without having to read two sources.
	if aerr := h.putResourceTags(r.Context(), natID, tags); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	addrs := []xmlNatGWAddr{{
		AllocationID: allocID,
		PublicIP:     publicIP,
		PrivateIP:    ngw.PrivateIP,
	}}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlCreateNatGatewayResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		NatGateway: xmlNatGateway{
			NatGatewayID: natID,
			SubnetID:     subnetID,
			VpcID:        sub.VpcID,
			State:        "available",
			CreateTime:   now,
			Addresses:    addrs,
			Tags:         xmlTagsOf(sortTags(tags)),
		},
	})
}

// ── DescribeNatGateways ─────────────────────────────────────────────────────

type xmlDescribeNatGatewaysResponse struct {
	XMLName     xml.Name        `xml:"DescribeNatGatewaysResponse"`
	Xmlns       string          `xml:"xmlns,attr"`
	RequestID   string          `xml:"requestId"`
	NatGateways []xmlNatGateway `xml:"natGatewaySet>item"`
}

// DescribeNatGateways lists NAT gateways with optional filtering.
func (h *Handler) DescribeNatGateways(w http.ResponseWriter, r *http.Request) {
	filters, aerr := natGatewayFilters.parse(r)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	requested := requestedIDs(r, "NatGatewayId")

	all, aerr := h.store.listNatGateways(r.Context())
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	tagsView, aerr := h.tagViewFor(r.Context(), r, true)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	items := make([]xmlNatGateway, 0, len(all))
	for _, ngw := range all {
		if !requested.has(ngw.NatGatewayID) || !filters.matches(ngw) {
			continue
		}
		tags, ok := tagsView.keep(ngw.NatGatewayID)
		if !ok {
			continue
		}

		addrs := []xmlNatGWAddr{{
			AllocationID: ngw.AllocationID,
			PublicIP:     ngw.PublicIP,
			PrivateIP:    ngw.PrivateIP,
		}}

		items = append(items, xmlNatGateway{
			NatGatewayID: ngw.NatGatewayID,
			SubnetID:     ngw.SubnetID,
			VpcID:        ngw.VpcID,
			State:        ngw.State,
			CreateTime:   ngw.CreateTime,
			Addresses:    addrs,
			Tags:         xmlTagsOf(tags),
		})
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeNatGatewaysResponse{
		Xmlns:       ec2XMLNS,
		RequestID:   protocol.RequestIDFromContext(r.Context()),
		NatGateways: items,
	})
}

// ── DeleteNatGateway ────────────────────────────────────────────────────────

type xmlDeleteNatGatewayResponse struct {
	XMLName      xml.Name `xml:"DeleteNatGatewayResponse"`
	Xmlns        string   `xml:"xmlns,attr"`
	RequestID    string   `xml:"requestId"`
	NatGatewayID string   `xml:"natGatewayId"`
}

// DeleteNatGateway deletes a NAT gateway.
func (h *Handler) DeleteNatGateway(w http.ResponseWriter, r *http.Request) {
	natID := r.FormValue("NatGatewayId")
	if natID == "" {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "NatGatewayId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// Mark as deleting, then remove.
	ngw, aerr := h.store.getNatGateway(r.Context(), natID)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	ngw.State = "deleted"
	_ = h.store.putNatGateway(r.Context(), ngw)
	_ = h.store.deleteNatGateway(r.Context(), natID)

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDeleteNatGatewayResponse{
		Xmlns:        ec2XMLNS,
		RequestID:    protocol.RequestIDFromContext(r.Context()),
		NatGatewayID: natID,
	})
}
