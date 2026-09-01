package ec2

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"

	"github.com/overcast-sh/overcast/internal/protocol"
)

// ── XML response types ───────────────────────────────────────────────────────

type xmlCreateInternetGatewayResponse struct {
	XMLName         xml.Name           `xml:"CreateInternetGatewayResponse"`
	Xmlns           string             `xml:"xmlns,attr"`
	RequestID       string             `xml:"requestId"`
	InternetGateway xmlInternetGateway `xml:"internetGateway"`
}

type xmlDescribeInternetGatewaysResponse struct {
	XMLName            xml.Name             `xml:"DescribeInternetGatewaysResponse"`
	Xmlns              string               `xml:"xmlns,attr"`
	RequestID          string               `xml:"requestId"`
	InternetGatewaySet []xmlInternetGateway `xml:"internetGatewaySet>item"`
}

type xmlInternetGateway struct {
	InternetGatewayID string             `xml:"internetGatewayId"`
	AttachmentSet     []xmlIGWAttachment `xml:"attachmentSet>item,omitempty"`
	TagSet            []xmlTag           `xml:"tagSet>item,omitempty"`
}

type xmlIGWAttachment struct {
	VpcID string `xml:"vpcId"`
	State string `xml:"state"`
}

type xmlDeleteInternetGatewayResponse struct {
	XMLName   xml.Name `xml:"DeleteInternetGatewayResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type xmlAttachInternetGatewayResponse struct {
	XMLName   xml.Name `xml:"AttachInternetGatewayResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type xmlDetachInternetGatewayResponse struct {
	XMLName   xml.Name `xml:"DetachInternetGatewayResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

// ── CreateInternetGateway ────────────────────────────────────────────────────

// CreateInternetGateway creates a new internet gateway.
func (h *Handler) CreateInternetGateway(w http.ResponseWriter, r *http.Request) {
	tags := parseTagSpecifications(r, "internet-gateway")
	// Create-time tags are checked before anything is created, as on AWS: a
	// rejected tag must fail the call rather than leave a gateway behind
	// (#1196).
	if aerr := validateTagSpecifications(tags); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	igwID := fmt.Sprintf("igw-%s", shortID())
	igw := &InternetGateway{InternetGatewayID: igwID}
	if aerr := h.store.putInternetGateway(r.Context(), igw); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	// Create-time tags go to the tag store, the same place CreateTags writes,
	// so a later describe sees both without reading two sources.
	if aerr := h.putResourceTags(r.Context(), igwID, tags); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlCreateInternetGatewayResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		InternetGateway: xmlInternetGateway{
			InternetGatewayID: igwID,
			TagSet:            xmlTagsOf(tags),
		},
	})
}

// ── DescribeInternetGateways ─────────────────────────────────────────────────

// DescribeInternetGateways lists internet gateways, optionally filtered.
func (h *Handler) DescribeInternetGateways(w http.ResponseWriter, r *http.Request) {
	resp, aerr := h.describeInternetGateways(r.Context(), requestQuery(r, "InternetGatewayId"))
	writeDescribe(w, r, resp, aerr)
}

func (h *Handler) describeInternetGateways(ctx context.Context, q describeQuery) (*xmlDescribeInternetGatewaysResponse, *protocol.AWSError) {
	filters, aerr := internetGatewayFilters.parse(q.filters)
	if aerr != nil {
		return nil, aerr
	}
	requested := q.ids

	all, aerr := h.store.listInternetGateways(ctx)
	if aerr != nil {
		return nil, aerr
	}

	tagsView, aerr := h.tagViewFor(ctx, q.filters, true)
	if aerr != nil {
		return nil, aerr
	}

	items := make([]xmlInternetGateway, 0, len(all))
	for _, igw := range all {
		if !requested.has(igw.InternetGatewayID) || !filters.matches(igw) {
			continue
		}
		tags, ok := tagsView.keep(igw.InternetGatewayID)
		if !ok {
			continue
		}
		items = append(items, igwToXML(igw, tags))
	}

	return &xmlDescribeInternetGatewaysResponse{
		Xmlns:              ec2XMLNS,
		RequestID:          protocol.RequestIDFromContext(ctx),
		InternetGatewaySet: items,
	}, nil
}

// ── DeleteInternetGateway ────────────────────────────────────────────────────

// DeleteInternetGateway deletes an internet gateway (must be detached).
func (h *Handler) DeleteInternetGateway(w http.ResponseWriter, r *http.Request) {
	igwID := r.FormValue("InternetGatewayId")
	if igwID == "" {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "InternetGatewayId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	igw, aerr := h.store.getInternetGateway(r.Context(), igwID)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	if len(igw.Attachments) > 0 {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "DependencyViolation",
			Message:    fmt.Sprintf("The internetGateway '%s' has dependencies and cannot be deleted", igwID),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	if aerr := h.store.deleteInternetGateway(r.Context(), igwID); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDeleteInternetGatewayResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Return:    true,
	})
}

// ── AttachInternetGateway ────────────────────────────────────────────────────

// AttachInternetGateway attaches an internet gateway to a VPC.
func (h *Handler) AttachInternetGateway(w http.ResponseWriter, r *http.Request) {
	igwID := r.FormValue("InternetGatewayId")
	vpcID := r.FormValue("VpcId")

	if igwID == "" || vpcID == "" {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "InternetGatewayId and VpcId are required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	igw, aerr := h.store.getInternetGateway(r.Context(), igwID)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	// Validate VPC exists.
	if _, aerr := h.store.getVPC(r.Context(), vpcID); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "InvalidVpcID.NotFound",
			Message:    fmt.Sprintf("The vpc ID '%s' does not exist", vpcID),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// Check if already attached.
	for _, att := range igw.Attachments {
		if att.VpcID == vpcID {
			protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
				Code:       "Resource.AlreadyAssociated",
				Message:    fmt.Sprintf("The internetGateway '%s' is already attached to vpc '%s'", igwID, vpcID),
				HTTPStatus: http.StatusBadRequest,
			})
			return
		}
	}

	igw.Attachments = append(igw.Attachments, IGWAttachment{
		VpcID: vpcID,
		State: "attached",
	})

	if aerr := h.store.putInternetGateway(r.Context(), igw); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	// Toggle Docker network to external (non-internal) mode.
	h.vpcStrategy.SetInternal(r.Context(), vpcID, false)

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlAttachInternetGatewayResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Return:    true,
	})
}

// ── DetachInternetGateway ────────────────────────────────────────────────────

// DetachInternetGateway detaches an internet gateway from a VPC.
func (h *Handler) DetachInternetGateway(w http.ResponseWriter, r *http.Request) {
	igwID := r.FormValue("InternetGatewayId")
	vpcID := r.FormValue("VpcId")

	if igwID == "" || vpcID == "" {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "InternetGatewayId and VpcId are required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	igw, aerr := h.store.getInternetGateway(r.Context(), igwID)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	found := false
	for i, att := range igw.Attachments {
		if att.VpcID == vpcID {
			igw.Attachments = append(igw.Attachments[:i], igw.Attachments[i+1:]...)
			found = true
			break
		}
	}

	if !found {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "Gateway.NotAttached",
			Message:    fmt.Sprintf("The internetGateway '%s' is not attached to vpc '%s'", igwID, vpcID),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	if aerr := h.store.putInternetGateway(r.Context(), igw); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	// Toggle Docker network back to internal mode.
	h.vpcStrategy.SetInternal(r.Context(), vpcID, true)

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDetachInternetGatewayResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Return:    true,
	})
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func igwToXML(igw *InternetGateway, tags []Tag) xmlInternetGateway {
	atts := make([]xmlIGWAttachment, 0, len(igw.Attachments))
	for _, a := range igw.Attachments {
		atts = append(atts, xmlIGWAttachment(a))
	}
	return xmlInternetGateway{
		InternetGatewayID: igw.InternetGatewayID,
		AttachmentSet:     atts,
		TagSet:            xmlTagsOf(tags),
	}
}
