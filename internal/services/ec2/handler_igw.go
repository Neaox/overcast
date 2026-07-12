package ec2

import (
	"encoding/xml"
	"fmt"
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
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
	igwID := fmt.Sprintf("igw-%s", shortID())
	igw := &InternetGateway{InternetGatewayID: igwID}
	if aerr := h.store.putInternetGateway(r.Context(), igw); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlCreateInternetGatewayResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		InternetGateway: xmlInternetGateway{
			InternetGatewayID: igwID,
		},
	})
}

// ── DescribeInternetGateways ─────────────────────────────────────────────────

// DescribeInternetGateways lists internet gateways, optionally filtered.
func (h *Handler) DescribeInternetGateways(w http.ResponseWriter, r *http.Request) {
	filterIDs := parseIndexedParam(r, "InternetGatewayId")
	filterIDSet := make(map[string]bool, len(filterIDs))
	for _, id := range filterIDs {
		filterIDSet[id] = true
	}

	filterIGWID := parseFilterValues(r, "internet-gateway-id")
	filterAttachVpc := parseFilterValues(r, "attachment.vpc-id")

	all, aerr := h.store.listInternetGateways(r.Context())
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	items := make([]xmlInternetGateway, 0, len(all))
	for _, igw := range all {
		if len(filterIDSet) > 0 && !filterIDSet[igw.InternetGatewayID] {
			continue
		}
		if len(filterIGWID) > 0 && !filterIGWID[igw.InternetGatewayID] {
			continue
		}
		if len(filterAttachVpc) > 0 {
			matched := false
			for _, att := range igw.Attachments {
				if filterAttachVpc[att.VpcID] {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		items = append(items, igwToXML(igw))
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeInternetGatewaysResponse{
		Xmlns:              ec2XMLNS,
		RequestID:          protocol.RequestIDFromContext(r.Context()),
		InternetGatewaySet: items,
	})
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

func igwToXML(igw *InternetGateway) xmlInternetGateway {
	atts := make([]xmlIGWAttachment, 0, len(igw.Attachments))
	for _, a := range igw.Attachments {
		atts = append(atts, xmlIGWAttachment(a))
	}
	return xmlInternetGateway{
		InternetGatewayID: igw.InternetGatewayID,
		AttachmentSet:     atts,
	}
}
