package ec2

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"strconv"

	"github.com/overcast-sh/overcast/internal/protocol"
)

// ── XML response types ───────────────────────────────────────────────────────

type xmlCreateVpnGatewayResponse struct {
	XMLName    xml.Name      `xml:"CreateVpnGatewayResponse"`
	Xmlns      string        `xml:"xmlns,attr"`
	RequestID  string        `xml:"requestId"`
	VpnGateway xmlVpnGateway `xml:"vpnGateway"`
}

type xmlDescribeVpnGatewaysResponse struct {
	XMLName       xml.Name        `xml:"DescribeVpnGatewaysResponse"`
	Xmlns         string          `xml:"xmlns,attr"`
	RequestID     string          `xml:"requestId"`
	VpnGatewaySet []xmlVpnGateway `xml:"vpnGatewaySet>item"`
}

type xmlVpnGateway struct {
	VpnGatewayID     string                    `xml:"vpnGatewayId"`
	State            string                    `xml:"state"`
	Type             string                    `xml:"type"`
	AvailabilityZone string                    `xml:"availabilityZone,omitempty"`
	Attachments      []xmlVpnGatewayAttachment `xml:"attachments>item"`
	AmazonSideAsn    int64                     `xml:"amazonSideAsn"`
	Tags             []xmlTag                  `xml:"tagSet>item,omitempty"`
}

type xmlVpnGatewayAttachment struct {
	VpcID string `xml:"vpcId"`
	State string `xml:"state"`
}

type xmlAttachVpnGatewayResponse struct {
	XMLName    xml.Name                `xml:"AttachVpnGatewayResponse"`
	Xmlns      string                  `xml:"xmlns,attr"`
	RequestID  string                  `xml:"requestId"`
	Attachment xmlVpnGatewayAttachment `xml:"attachment"`
}

type xmlDetachVpnGatewayResponse struct {
	XMLName   xml.Name `xml:"DetachVpnGatewayResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type xmlDeleteVpnGatewayResponse struct {
	XMLName   xml.Name `xml:"DeleteVpnGatewayResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

// ── CreateVpnGateway ────────────────────────────────────────────────────────

// CreateVpnGateway creates a metadata-only virtual private gateway.
func (h *Handler) CreateVpnGateway(w http.ResponseWriter, r *http.Request) {
	vgwType := r.FormValue("Type")
	if vgwType == "" {
		protocol.WriteEC2QueryXMLError(w, r, ec2err("MissingParameter", "Type is required", http.StatusBadRequest))
		return
	}
	if vgwType != "ipsec.1" {
		protocol.WriteEC2QueryXMLError(w, r, ec2err("InvalidParameterValue", "Type must be ipsec.1", http.StatusBadRequest))
		return
	}
	asn, aerr := parseAmazonSideAsn(r.FormValue("AmazonSideAsn"))
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	// Create-time tags are checked before anything is created, as on AWS: a
	// rejected tag must fail the call rather than leave a gateway behind.
	tags := parseTagSpecifications(r, "vpn-gateway")
	if aerr := validateTagSpecifications(tags); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	vgw := &VpnGateway{
		VpnGatewayID:     fmt.Sprintf("vgw-%s", shortID()),
		State:            "available",
		Type:             vgwType,
		AmazonSideAsn:    asn,
		AvailabilityZone: r.FormValue("AvailabilityZone"),
	}
	if aerr := h.store.putVpnGateway(r.Context(), vgw); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	// Create-time tags go to the tag store, the same place CreateTags writes,
	// so a later describe sees both without having to read two sources.
	if aerr := h.putResourceTags(r.Context(), vgw.VpnGatewayID, tags); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlCreateVpnGatewayResponse{
		Xmlns:      ec2XMLNS,
		RequestID:  protocol.RequestIDFromContext(r.Context()),
		VpnGateway: vpnGatewayToXML(vgw, sortTags(tags)),
	})
}

// ── DescribeVpnGateways ─────────────────────────────────────────────────────

// DescribeVpnGateways lists virtual private gateways, optionally filtered.
func (h *Handler) DescribeVpnGateways(w http.ResponseWriter, r *http.Request) {
	resp, aerr := h.describeVpnGateways(r.Context(), requestQuery(r, "VpnGatewayId"))
	writeDescribe(w, r, resp, aerr)
}

func (h *Handler) describeVpnGateways(ctx context.Context, q describeQuery) (*xmlDescribeVpnGatewaysResponse, *protocol.AWSError) {
	filters, aerr := vpnGatewayFilters.parse(q.filters)
	if aerr != nil {
		return nil, aerr
	}
	requested := q.ids

	all, aerr := h.store.listVpnGateways(ctx)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := resolveIDs(vpnGatewayIDScope, requested, all, func(vgw *VpnGateway) string { return vgw.VpnGatewayID }); aerr != nil {
		return nil, aerr
	}

	tagsView, aerr := h.tagViewFor(ctx, q.filters, true)
	if aerr != nil {
		return nil, aerr
	}

	items := make([]xmlVpnGateway, 0, len(all))
	for _, vgw := range all {
		if !requested.has(vgw.VpnGatewayID) || !filters.matches(vgw) {
			continue
		}
		tags, ok := tagsView.keep(vgw.VpnGatewayID)
		if !ok {
			continue
		}
		items = append(items, vpnGatewayToXML(vgw, tags))
	}

	return &xmlDescribeVpnGatewaysResponse{
		Xmlns:         ec2XMLNS,
		RequestID:     protocol.RequestIDFromContext(ctx),
		VpnGatewaySet: items,
	}, nil
}

// ── AttachVpnGateway ────────────────────────────────────────────────────────

// AttachVpnGateway attaches a virtual private gateway to a VPC.
func (h *Handler) AttachVpnGateway(w http.ResponseWriter, r *http.Request) {
	vgwID := r.FormValue("VpnGatewayId")
	vpcID := r.FormValue("VpcId")
	if vgwID == "" || vpcID == "" {
		protocol.WriteEC2QueryXMLError(w, r, ec2err("MissingParameter", "VpnGatewayId and VpcId are required", http.StatusBadRequest))
		return
	}
	vgw, aerr := h.store.getVpnGateway(r.Context(), vgwID)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	if _, aerr := h.store.getVPC(r.Context(), vpcID); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, ec2err("InvalidVpcID.NotFound", fmt.Sprintf("The vpc ID '%s' does not exist", vpcID), http.StatusBadRequest))
		return
	}
	for _, att := range vgw.Attachments {
		if att.VpcID == vpcID {
			protocol.WriteEC2QueryXMLError(w, r, ec2err("Resource.AlreadyAssociated", fmt.Sprintf("The vpnGateway '%s' is already attached to vpc '%s'", vgwID, vpcID), http.StatusBadRequest))
			return
		}
	}
	if len(vgw.Attachments) > 0 {
		protocol.WriteEC2QueryXMLError(w, r, ec2err("Resource.AlreadyAssociated", fmt.Sprintf("The vpnGateway '%s' is already attached", vgwID), http.StatusBadRequest))
		return
	}
	vgw.Attachments = append(vgw.Attachments, VpnGatewayAttachment{VpcID: vpcID, State: "attached"})
	if aerr := h.store.putVpnGateway(r.Context(), vgw); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlAttachVpnGatewayResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Attachment: xmlVpnGatewayAttachment{
			VpcID: vpcID,
			State: "attached",
		},
	})
}

// ── DetachVpnGateway ────────────────────────────────────────────────────────

// DetachVpnGateway detaches a virtual private gateway from a VPC.
func (h *Handler) DetachVpnGateway(w http.ResponseWriter, r *http.Request) {
	vgwID := r.FormValue("VpnGatewayId")
	vpcID := r.FormValue("VpcId")
	if vgwID == "" || vpcID == "" {
		protocol.WriteEC2QueryXMLError(w, r, ec2err("MissingParameter", "VpnGatewayId and VpcId are required", http.StatusBadRequest))
		return
	}
	vgw, aerr := h.store.getVpnGateway(r.Context(), vgwID)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	found := false
	for i, att := range vgw.Attachments {
		if att.VpcID == vpcID {
			vgw.Attachments = append(vgw.Attachments[:i], vgw.Attachments[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		protocol.WriteEC2QueryXMLError(w, r, ec2err("Gateway.NotAttached", fmt.Sprintf("The vpnGateway '%s' is not attached to vpc '%s'", vgwID, vpcID), http.StatusBadRequest))
		return
	}
	if aerr := h.store.putVpnGateway(r.Context(), vgw); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDetachVpnGatewayResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Return:    true,
	})
}

// ── DeleteVpnGateway ────────────────────────────────────────────────────────

// DeleteVpnGateway deletes a detached virtual private gateway.
func (h *Handler) DeleteVpnGateway(w http.ResponseWriter, r *http.Request) {
	vgwID := r.FormValue("VpnGatewayId")
	if vgwID == "" {
		protocol.WriteEC2QueryXMLError(w, r, ec2err("MissingParameter", "VpnGatewayId is required", http.StatusBadRequest))
		return
	}
	vgw, aerr := h.store.getVpnGateway(r.Context(), vgwID)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	if len(vgw.Attachments) > 0 {
		protocol.WriteEC2QueryXMLError(w, r, ec2err("DependencyViolation", fmt.Sprintf("The vpnGateway '%s' has dependencies and cannot be deleted", vgwID), http.StatusBadRequest))
		return
	}
	if aerr := h.store.deleteVpnGateway(r.Context(), vgwID); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDeleteVpnGatewayResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Return:    true,
	})
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func parseAmazonSideAsn(raw string) (int64, *protocol.AWSError) {
	if raw == "" {
		return 64512, nil
	}
	asn, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, ec2err("InvalidParameterValue", "AmazonSideAsn must be an integer", http.StatusBadRequest)
	}
	if (asn >= 64512 && asn <= 65534) || (asn >= 4200000000 && asn <= 4294967294) {
		return asn, nil
	}
	return 0, ec2err("InvalidParameterValue", "AmazonSideAsn is out of range", http.StatusBadRequest)
}

func vpnGatewayToXML(vgw *VpnGateway, tags []Tag) xmlVpnGateway {
	attachments := make([]xmlVpnGatewayAttachment, 0, len(vgw.Attachments))
	for _, att := range vgw.Attachments {
		attachments = append(attachments, xmlVpnGatewayAttachment(att))
	}
	return xmlVpnGateway{
		VpnGatewayID:     vgw.VpnGatewayID,
		State:            vgw.State,
		Type:             vgw.Type,
		AvailabilityZone: vgw.AvailabilityZone,
		Attachments:      attachments,
		AmazonSideAsn:    vgw.AmazonSideAsn,
		Tags:             xmlTagsOf(tags),
	}
}
