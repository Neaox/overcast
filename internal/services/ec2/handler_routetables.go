package ec2

import (
	"encoding/xml"
	"fmt"
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
)

// ── XML response types ───────────────────────────────────────────────────────

type xmlCreateRouteTableResponse struct {
	XMLName    xml.Name      `xml:"CreateRouteTableResponse"`
	Xmlns      string        `xml:"xmlns,attr"`
	RequestID  string        `xml:"requestId"`
	RouteTable xmlRouteTable `xml:"routeTable"`
}

type xmlDescribeRouteTablesResponse struct {
	XMLName       xml.Name        `xml:"DescribeRouteTablesResponse"`
	Xmlns         string          `xml:"xmlns,attr"`
	RequestID     string          `xml:"requestId"`
	RouteTableSet []xmlRouteTable `xml:"routeTableSet>item"`
}

type xmlRouteTable struct {
	RouteTableID   string                     `xml:"routeTableId"`
	VpcID          string                     `xml:"vpcId"`
	RouteSet       []xmlRoute                 `xml:"routeSet>item"`
	AssociationSet []xmlRouteTableAssociation `xml:"associationSet>item,omitempty"`
}

type xmlRoute struct {
	DestinationCidrBlock string `xml:"destinationCidrBlock"`
	GatewayID            string `xml:"gatewayId,omitempty"`
	NatGatewayID         string `xml:"natGatewayId,omitempty"`
	Origin               string `xml:"origin"`
	State                string `xml:"state"`
}

type xmlRouteTableAssociation struct {
	AssociationID string `xml:"routeTableAssociationId"`
	RouteTableID  string `xml:"routeTableId"`
	SubnetID      string `xml:"subnetId,omitempty"`
	Main          bool   `xml:"main"`
}

type xmlDeleteRouteTableResponse struct {
	XMLName   xml.Name `xml:"DeleteRouteTableResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type xmlCreateRouteResponse struct {
	XMLName   xml.Name `xml:"CreateRouteResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type xmlAssociateRouteTableResponse struct {
	XMLName       xml.Name `xml:"AssociateRouteTableResponse"`
	Xmlns         string   `xml:"xmlns,attr"`
	RequestID     string   `xml:"requestId"`
	AssociationID string   `xml:"newAssociationId"`
}

type xmlDisassociateRouteTableResponse struct {
	XMLName   xml.Name `xml:"DisassociateRouteTableResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

// ── CreateRouteTable ─────────────────────────────────────────────────────────

// CreateRouteTable creates a new route table in a VPC with a local route.
func (h *Handler) CreateRouteTable(w http.ResponseWriter, r *http.Request) {
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
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "InvalidVpcID.NotFound",
			Message:    fmt.Sprintf("The vpc ID '%s' does not exist", vpcID),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	rtID := fmt.Sprintf("rtb-%s", shortID())
	rt := &RouteTable{
		RouteTableID: rtID,
		VpcID:        vpcID,
		Routes: []Route{
			{
				DestinationCidrBlock: vpc.CidrBlock,
				GatewayID:            "local",
				Origin:               "CreateRouteTable",
			},
		},
	}
	if aerr := h.store.putRouteTable(r.Context(), rt); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlCreateRouteTableResponse{
		Xmlns:      ec2XMLNS,
		RequestID:  protocol.RequestIDFromContext(r.Context()),
		RouteTable: routeTableToXML(rt),
	})
}

// ── DescribeRouteTables ──────────────────────────────────────────────────────

// DescribeRouteTables lists route tables, optionally filtered.
func (h *Handler) DescribeRouteTables(w http.ResponseWriter, r *http.Request) {
	filterIDs := parseIndexedParam(r, "RouteTableId")
	filterIDSet := make(map[string]bool, len(filterIDs))
	for _, id := range filterIDs {
		filterIDSet[id] = true
	}

	filterRouteTableID := parseFilterValues(r, "route-table-id")
	filterVpcID := parseFilterValues(r, "vpc-id")

	all, aerr := h.store.listRouteTables(r.Context())
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	items := make([]xmlRouteTable, 0, len(all))
	for _, rt := range all {
		if len(filterIDSet) > 0 && !filterIDSet[rt.RouteTableID] {
			continue
		}
		if len(filterRouteTableID) > 0 && !filterRouteTableID[rt.RouteTableID] {
			continue
		}
		if len(filterVpcID) > 0 && !filterVpcID[rt.VpcID] {
			continue
		}
		items = append(items, routeTableToXML(rt))
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeRouteTablesResponse{
		Xmlns:         ec2XMLNS,
		RequestID:     protocol.RequestIDFromContext(r.Context()),
		RouteTableSet: items,
	})
}

// ── DeleteRouteTable ─────────────────────────────────────────────────────────

// DeleteRouteTable deletes a route table by ID.
func (h *Handler) DeleteRouteTable(w http.ResponseWriter, r *http.Request) {
	rtID := r.FormValue("RouteTableId")
	if rtID == "" {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "RouteTableId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	rt, aerr := h.store.getRouteTable(r.Context(), rtID)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	// Cannot delete a main route table.
	for _, assoc := range rt.Associations {
		if assoc.Main {
			protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
				Code:       "DependencyViolation",
				Message:    "The routeTable cannot be deleted because it is the main route table",
				HTTPStatus: http.StatusBadRequest,
			})
			return
		}
	}

	if aerr := h.store.deleteRouteTable(r.Context(), rtID); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDeleteRouteTableResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Return:    true,
	})
}

// ── CreateRoute ──────────────────────────────────────────────────────────────

// CreateRoute adds a route to an existing route table.
func (h *Handler) CreateRoute(w http.ResponseWriter, r *http.Request) {
	rtID := r.FormValue("RouteTableId")
	destCidr := r.FormValue("DestinationCidrBlock")
	gatewayID := r.FormValue("GatewayId")
	natGatewayID := r.FormValue("NatGatewayId")

	if rtID == "" || destCidr == "" {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "RouteTableId and DestinationCidrBlock are required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	rt, aerr := h.store.getRouteTable(r.Context(), rtID)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	rt.Routes = append(rt.Routes, Route{
		DestinationCidrBlock: destCidr,
		GatewayID:            gatewayID,
		NatGatewayID:         natGatewayID,
		Origin:               "CreateRoute",
	})

	if aerr := h.store.putRouteTable(r.Context(), rt); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlCreateRouteResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Return:    true,
	})
}

// ── DeleteRoute ──────────────────────────────────────────────────────────────

type xmlDeleteRouteResponse struct {
	XMLName   xml.Name `xml:"DeleteRouteResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

// DeleteRoute removes a route from a route table by destination CIDR.
func (h *Handler) DeleteRoute(w http.ResponseWriter, r *http.Request) {
	rtID := r.FormValue("RouteTableId")
	destCidr := r.FormValue("DestinationCidrBlock")

	if rtID == "" || destCidr == "" {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "RouteTableId and DestinationCidrBlock are required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	rt, aerr := h.store.getRouteTable(r.Context(), rtID)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	found := false
	routes := make([]Route, 0, len(rt.Routes))
	for _, route := range rt.Routes {
		if route.DestinationCidrBlock == destCidr {
			found = true
			continue
		}
		routes = append(routes, route)
	}

	if !found {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "InvalidRoute.NotFound",
			Message:    fmt.Sprintf("no route with destination-cidr-block %s in route table %s", destCidr, rtID),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	rt.Routes = routes
	if aerr := h.store.putRouteTable(r.Context(), rt); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDeleteRouteResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Return:    true,
	})
}

// ── AssociateRouteTable ──────────────────────────────────────────────────────

// AssociateRouteTable associates a route table with a subnet.
func (h *Handler) AssociateRouteTable(w http.ResponseWriter, r *http.Request) {
	rtID := r.FormValue("RouteTableId")
	subnetID := r.FormValue("SubnetId")

	if rtID == "" || subnetID == "" {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "RouteTableId and SubnetId are required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	rt, aerr := h.store.getRouteTable(r.Context(), rtID)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	assocID := fmt.Sprintf("rtbassoc-%s", shortID())
	rt.Associations = append(rt.Associations, RouteTableAssociation{
		AssociationID: assocID,
		RouteTableID:  rtID,
		SubnetID:      subnetID,
		Main:          false,
	})

	if aerr := h.store.putRouteTable(r.Context(), rt); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlAssociateRouteTableResponse{
		Xmlns:         ec2XMLNS,
		RequestID:     protocol.RequestIDFromContext(r.Context()),
		AssociationID: assocID,
	})
}

// ── DisassociateRouteTable ───────────────────────────────────────────────────

// DisassociateRouteTable disassociates a route table from a subnet.
func (h *Handler) DisassociateRouteTable(w http.ResponseWriter, r *http.Request) {
	assocID := r.FormValue("AssociationId")
	if assocID == "" {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "AssociationId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// Find the route table containing this association.
	all, aerr := h.store.listRouteTables(r.Context())
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	found := false
	for _, rt := range all {
		for i, assoc := range rt.Associations {
			if assoc.AssociationID == assocID {
				if assoc.Main {
					protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
						Code:       "InvalidParameterValue",
						Message:    "Cannot disassociate the main route table association",
						HTTPStatus: http.StatusBadRequest,
					})
					return
				}
				rt.Associations = append(rt.Associations[:i], rt.Associations[i+1:]...)
				if aerr := h.store.putRouteTable(r.Context(), rt); aerr != nil {
					protocol.WriteEC2QueryXMLError(w, r, aerr)
					return
				}
				found = true
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "InvalidAssociationID.NotFound",
			Message:    fmt.Sprintf("The association ID '%s' does not exist", assocID),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDisassociateRouteTableResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Return:    true,
	})
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func routeTableToXML(rt *RouteTable) xmlRouteTable {
	routes := make([]xmlRoute, 0, len(rt.Routes))
	for _, r := range rt.Routes {
		routes = append(routes, xmlRoute{
			DestinationCidrBlock: r.DestinationCidrBlock,
			GatewayID:            r.GatewayID,
			NatGatewayID:         r.NatGatewayID,
			Origin:               r.Origin,
			State:                "active",
		})
	}
	assocs := make([]xmlRouteTableAssociation, 0, len(rt.Associations))
	for _, a := range rt.Associations {
		assocs = append(assocs, xmlRouteTableAssociation(a))
	}
	return xmlRouteTable{
		RouteTableID:   rt.RouteTableID,
		VpcID:          rt.VpcID,
		RouteSet:       routes,
		AssociationSet: assocs,
	}
}
