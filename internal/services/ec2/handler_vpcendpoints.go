package ec2

// handler_vpcendpoints.go — CreateVpcEndpoint, DescribeVpcEndpoints, DeleteVpcEndpoints.
//
// VPC endpoints are metadata-only. Gateway endpoints (S3, DynamoDB) and
// Interface endpoints (any service) are accepted; no actual networking is
// configured. State is always "available" immediately after creation.

import (
	"encoding/xml"
	"fmt"
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
)

// ── XML types ──────────────────────────────────────────────────────────────────

type xmlVpcEndpoint struct {
	VpcEndpointID   string `xml:"vpcEndpointId"`
	VpcID           string `xml:"vpcId"`
	ServiceName     string `xml:"serviceName"`
	State           string `xml:"state"`
	VpcEndpointType string `xml:"vpcEndpointType"`
}

type xmlCreateVpcEndpointResponse struct {
	XMLName     xml.Name       `xml:"CreateVpcEndpointResponse"`
	Xmlns       string         `xml:"xmlns,attr"`
	RequestID   string         `xml:"requestId"`
	VpcEndpoint xmlVpcEndpoint `xml:"vpcEndpoint"`
}

type xmlDescribeVpcEndpointsResponse struct {
	XMLName      xml.Name         `xml:"DescribeVpcEndpointsResponse"`
	Xmlns        string           `xml:"xmlns,attr"`
	RequestID    string           `xml:"requestId"`
	VpcEndpoints []xmlVpcEndpoint `xml:"vpcEndpointSet>item"`
}

type xmlDeleteVpcEndpointsResponse struct {
	XMLName   xml.Name `xml:"DeleteVpcEndpointsResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

// ── CreateVpcEndpoint ─────────────────────────────────────────────────────────

// CreateVpcEndpoint handles Action=CreateVpcEndpoint.
func (h *Handler) CreateVpcEndpoint(w http.ResponseWriter, r *http.Request) {
	vpcID := r.FormValue("VpcId")
	serviceName := r.FormValue("ServiceName")
	epType := r.FormValue("VpcEndpointType")
	if vpcID == "" || serviceName == "" {
		protocol.WriteQueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "VpcId and ServiceName are required.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	if epType == "" {
		epType = "Gateway"
	}

	if _, aerr := h.store.getVPC(r.Context(), vpcID); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	id := fmt.Sprintf("vpce-%s", shortID())
	ep := &VpcEndpoint{
		VpcEndpointID:   id,
		VpcID:           vpcID,
		ServiceName:     serviceName,
		State:           "available",
		VpcEndpointType: epType,
	}
	if aerr := h.store.putVpcEndpoint(r.Context(), ep); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlCreateVpcEndpointResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		VpcEndpoint: xmlVpcEndpoint{
			VpcEndpointID:   ep.VpcEndpointID,
			VpcID:           ep.VpcID,
			ServiceName:     ep.ServiceName,
			State:           ep.State,
			VpcEndpointType: ep.VpcEndpointType,
		},
	})
}

// ── DescribeVpcEndpoints ──────────────────────────────────────────────────────

// DescribeVpcEndpoints handles Action=DescribeVpcEndpoints.
// Supports filter vpc-id and VpcEndpointId.N positional params.
func (h *Handler) DescribeVpcEndpoints(w http.ResponseWriter, r *http.Request) {
	// Collect requested endpoint IDs from VpcEndpointId.N params.
	requestedIDs := map[string]bool{}
	for i := 1; ; i++ {
		id := r.FormValue(fmt.Sprintf("VpcEndpointId.%d", i))
		if id == "" {
			break
		}
		requestedIDs[id] = true
	}

	// Collect vpc-id filter values.
	vpcFilter := parseFilterValues(r, "vpc-id")
	serviceFilter := parseFilterValues(r, "service-name")

	all, aerr := h.store.listVpcEndpoints(r.Context())
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	var items []xmlVpcEndpoint
	for _, ep := range all {
		if len(requestedIDs) > 0 && !requestedIDs[ep.VpcEndpointID] {
			continue
		}
		if len(vpcFilter) > 0 && !vpcFilter[ep.VpcID] {
			continue
		}
		if len(serviceFilter) > 0 && !serviceFilter[ep.ServiceName] {
			continue
		}
		items = append(items, xmlVpcEndpoint{
			VpcEndpointID:   ep.VpcEndpointID,
			VpcID:           ep.VpcID,
			ServiceName:     ep.ServiceName,
			State:           ep.State,
			VpcEndpointType: ep.VpcEndpointType,
		})
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeVpcEndpointsResponse{
		Xmlns:        ec2XMLNS,
		RequestID:    protocol.RequestIDFromContext(r.Context()),
		VpcEndpoints: items,
	})
}

// ── DeleteVpcEndpoints ────────────────────────────────────────────────────────

// DeleteVpcEndpoints handles Action=DeleteVpcEndpoints.
// Accepts VpcEndpointId.N positional params; silently skips unknown IDs.
func (h *Handler) DeleteVpcEndpoints(w http.ResponseWriter, r *http.Request) {
	for i := 1; ; i++ {
		id := r.FormValue(fmt.Sprintf("VpcEndpointId.%d", i))
		if id == "" {
			break
		}
		_ = h.store.deleteVpcEndpoint(r.Context(), id)
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDeleteVpcEndpointsResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Return:    true,
	})
}
