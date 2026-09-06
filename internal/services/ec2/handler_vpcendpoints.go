package ec2

// handler_vpcendpoints.go — CreateVpcEndpoint, DescribeVpcEndpoints, DeleteVpcEndpoints.
//
// VPC endpoints are metadata-only. Gateway endpoints (S3, DynamoDB) and
// Interface endpoints (any service) are accepted; no actual networking is
// configured. State is always "available" immediately after creation.

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"

	"github.com/overcast-sh/overcast/internal/protocol"
)

// ── XML types ──────────────────────────────────────────────────────────────────

type xmlVpcEndpoint struct {
	VpcEndpointID   string   `xml:"vpcEndpointId"`
	VpcID           string   `xml:"vpcId"`
	ServiceName     string   `xml:"serviceName"`
	State           string   `xml:"state"`
	VpcEndpointType string   `xml:"vpcEndpointType"`
	TagSet          []xmlTag `xml:"tagSet>item,omitempty"`
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
}

// ── CreateVpcEndpoint ─────────────────────────────────────────────────────────

// CreateVpcEndpoint handles Action=CreateVpcEndpoint.
func (h *Handler) CreateVpcEndpoint(w http.ResponseWriter, r *http.Request) {
	vpcID := r.FormValue("VpcId")
	serviceName := r.FormValue("ServiceName")
	epType := r.FormValue("VpcEndpointType")
	if vpcID == "" || serviceName == "" {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
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
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	tags := parseTagSpecifications(r, "vpc-endpoint")
	// Create-time tags are checked before anything is created, as on AWS: a
	// rejected tag must fail the call rather than leave an endpoint behind
	// (#1196).
	if aerr := validateTagSpecifications(tags); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
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
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	// Create-time tags go to the tag store, the same place CreateTags writes,
	// so a later describe sees both without reading two sources.
	if aerr := h.putResourceTags(r.Context(), id, tags); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
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
			TagSet:          xmlTagsOf(tags),
		},
	})
}

// ── DescribeVpcEndpoints ──────────────────────────────────────────────────────

// DescribeVpcEndpoints lists VPC endpoints, optionally selected by
// VpcEndpointId.N or filtered.
func (h *Handler) DescribeVpcEndpoints(w http.ResponseWriter, r *http.Request) {
	resp, aerr := h.describeVpcEndpoints(r.Context(), requestQuery(r, "VpcEndpointId"))
	writeDescribe(w, r, resp, aerr)
}

func (h *Handler) describeVpcEndpoints(ctx context.Context, q describeQuery) (*xmlDescribeVpcEndpointsResponse, *protocol.AWSError) {
	filters, aerr := vpcEndpointFilters.parse(q.filters)
	if aerr != nil {
		return nil, aerr
	}
	requested := q.ids

	all, aerr := h.store.listVpcEndpoints(ctx)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := resolveIDs(vpcEndpointIDScope, requested, all, func(ep *VpcEndpoint) string { return ep.VpcEndpointID }); aerr != nil {
		return nil, aerr
	}

	tagsView, aerr := h.tagViewFor(ctx, q.filters, true)
	if aerr != nil {
		return nil, aerr
	}

	var items []xmlVpcEndpoint
	for _, ep := range all {
		if !requested.has(ep.VpcEndpointID) || !filters.matches(ep) {
			continue
		}
		tags, _ := tagsView.keep(ep.VpcEndpointID)
		items = append(items, xmlVpcEndpoint{
			VpcEndpointID:   ep.VpcEndpointID,
			VpcID:           ep.VpcID,
			ServiceName:     ep.ServiceName,
			State:           ep.State,
			VpcEndpointType: ep.VpcEndpointType,
			TagSet:          xmlTagsOf(tags),
		})
	}

	return &xmlDescribeVpcEndpointsResponse{
		Xmlns:        ec2XMLNS,
		RequestID:    protocol.RequestIDFromContext(ctx),
		VpcEndpoints: items,
	}, nil
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
	})
}
