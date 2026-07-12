package ec2

import (
	"encoding/xml"
	"fmt"
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
)

// ── XML response types ───────────────────────────────────────────────────────

type xmlCreateVpcPeeringConnectionResponse struct {
	XMLName              xml.Name                `xml:"CreateVpcPeeringConnectionResponse"`
	Xmlns                string                  `xml:"xmlns,attr"`
	RequestID            string                  `xml:"requestId"`
	VpcPeeringConnection xmlVpcPeeringConnection `xml:"vpcPeeringConnection"`
}

type xmlAcceptVpcPeeringConnectionResponse struct {
	XMLName              xml.Name                `xml:"AcceptVpcPeeringConnectionResponse"`
	Xmlns                string                  `xml:"xmlns,attr"`
	RequestID            string                  `xml:"requestId"`
	VpcPeeringConnection xmlVpcPeeringConnection `xml:"vpcPeeringConnection"`
}

type xmlDescribeVpcPeeringConnectionsResponse struct {
	XMLName                 xml.Name                  `xml:"DescribeVpcPeeringConnectionsResponse"`
	Xmlns                   string                    `xml:"xmlns,attr"`
	RequestID               string                    `xml:"requestId"`
	VpcPeeringConnectionSet []xmlVpcPeeringConnection `xml:"vpcPeeringConnectionSet>item"`
}

type xmlDeleteVpcPeeringConnectionResponse struct {
	XMLName   xml.Name `xml:"DeleteVpcPeeringConnectionResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type xmlVpcPeeringConnection struct {
	VpcPeeringConnectionID string               `xml:"vpcPeeringConnectionId"`
	RequesterVpcInfo       xmlVpcPeeringVpcInfo `xml:"requesterVpcInfo"`
	AccepterVpcInfo        xmlVpcPeeringVpcInfo `xml:"accepterVpcInfo"`
	Status                 xmlVpcPeeringStatus  `xml:"status"`
}

type xmlVpcPeeringVpcInfo struct {
	OwnerID   string `xml:"ownerId"`
	VpcID     string `xml:"vpcId"`
	CidrBlock string `xml:"cidrBlock,omitempty"`
	Region    string `xml:"region"`
}

type xmlVpcPeeringStatus struct {
	Code    string `xml:"code"`
	Message string `xml:"message"`
}

// ── CreateVpcPeeringConnection ───────────────────────────────────────────────

// CreateVpcPeeringConnection creates a peering connection between two VPCs.
func (h *Handler) CreateVpcPeeringConnection(w http.ResponseWriter, r *http.Request) {
	vpcID := r.FormValue("VpcId")
	peerVpcID := r.FormValue("PeerVpcId")

	if vpcID == "" || peerVpcID == "" {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "VpcId and PeerVpcId are required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	ctx := r.Context()

	// Validate requester VPC exists.
	requesterVpc, aerr := h.store.getVPC(ctx, vpcID)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "InvalidVpcID.NotFound",
			Message:    fmt.Sprintf("The vpc ID '%s' does not exist", vpcID),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// Validate accepter VPC exists.
	accepterVpc, aerr := h.store.getVPC(ctx, peerVpcID)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "InvalidVpcID.NotFound",
			Message:    fmt.Sprintf("The vpc ID '%s' does not exist", peerVpcID),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	region := h.store.region(ctx)
	ownerID := h.cfg.AccountID

	pcxID := fmt.Sprintf("pcx-%s", shortID())
	pcx := &VpcPeeringConnection{
		VpcPeeringConnectionID: pcxID,
		RequesterVpcInfo: VpcPeeringConnectionVpcInfo{
			VpcID:     requesterVpc.VpcID,
			OwnerID:   ownerID,
			CidrBlock: requesterVpc.CidrBlock,
			Region:    region,
		},
		AccepterVpcInfo: VpcPeeringConnectionVpcInfo{
			VpcID:     accepterVpc.VpcID,
			OwnerID:   ownerID,
			CidrBlock: accepterVpc.CidrBlock,
			Region:    region,
		},
		Status: VpcPeeringConnectionStatus{
			Code:    "pending-acceptance",
			Message: fmt.Sprintf("Initiating Request to %s", ownerID),
		},
	}

	if aerr := h.store.putVpcPeeringConnection(ctx, pcx); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlCreateVpcPeeringConnectionResponse{
		Xmlns:                ec2XMLNS,
		RequestID:            protocol.RequestIDFromContext(ctx),
		VpcPeeringConnection: pcxToXML(pcx),
	})
}

// ── AcceptVpcPeeringConnection ───────────────────────────────────────────────

// AcceptVpcPeeringConnection accepts a pending peering connection.
func (h *Handler) AcceptVpcPeeringConnection(w http.ResponseWriter, r *http.Request) {
	pcxID := r.FormValue("VpcPeeringConnectionId")
	if pcxID == "" {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "VpcPeeringConnectionId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	ctx := r.Context()
	pcx, aerr := h.store.getVpcPeeringConnection(ctx, pcxID)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	if pcx.Status.Code != "pending-acceptance" {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "InvalidStateTransition",
			Message:    fmt.Sprintf("The peering connection '%s' is in state '%s' and cannot be accepted", pcxID, pcx.Status.Code),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	pcx.Status = VpcPeeringConnectionStatus{
		Code:    "active",
		Message: "Active",
	}

	if aerr := h.store.putVpcPeeringConnection(ctx, pcx); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlAcceptVpcPeeringConnectionResponse{
		Xmlns:                ec2XMLNS,
		RequestID:            protocol.RequestIDFromContext(ctx),
		VpcPeeringConnection: pcxToXML(pcx),
	})
}

// ── DescribeVpcPeeringConnections ────────────────────────────────────────────

// DescribeVpcPeeringConnections lists peering connections, optionally filtered.
func (h *Handler) DescribeVpcPeeringConnections(w http.ResponseWriter, r *http.Request) {
	filterIDs := parseIndexedParam(r, "VpcPeeringConnectionId")
	filterIDSet := make(map[string]bool, len(filterIDs))
	for _, id := range filterIDs {
		filterIDSet[id] = true
	}

	filterStatusCode := parseFilterValues(r, "status-code")
	filterRequesterVpc := parseFilterValues(r, "requester-vpc-info.vpc-id")
	filterAccepterVpc := parseFilterValues(r, "accepter-vpc-info.vpc-id")

	ctx := r.Context()
	all, aerr := h.store.listVpcPeeringConnections(ctx)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	items := make([]xmlVpcPeeringConnection, 0, len(all))
	for _, pcx := range all {
		if len(filterIDSet) > 0 && !filterIDSet[pcx.VpcPeeringConnectionID] {
			continue
		}
		if len(filterStatusCode) > 0 && !filterStatusCode[pcx.Status.Code] {
			continue
		}
		if len(filterRequesterVpc) > 0 && !filterRequesterVpc[pcx.RequesterVpcInfo.VpcID] {
			continue
		}
		if len(filterAccepterVpc) > 0 && !filterAccepterVpc[pcx.AccepterVpcInfo.VpcID] {
			continue
		}
		items = append(items, pcxToXML(pcx))
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeVpcPeeringConnectionsResponse{
		Xmlns:                   ec2XMLNS,
		RequestID:               protocol.RequestIDFromContext(ctx),
		VpcPeeringConnectionSet: items,
	})
}

// ── DeleteVpcPeeringConnection ───────────────────────────────────────────────

// DeleteVpcPeeringConnection deletes a peering connection.
func (h *Handler) DeleteVpcPeeringConnection(w http.ResponseWriter, r *http.Request) {
	pcxID := r.FormValue("VpcPeeringConnectionId")
	if pcxID == "" {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "VpcPeeringConnectionId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	ctx := r.Context()
	pcx, aerr := h.store.getVpcPeeringConnection(ctx, pcxID)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	// Can only delete from active or pending-acceptance.
	switch pcx.Status.Code {
	case "active", "pending-acceptance":
		// OK to delete.
	default:
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "InvalidStateTransition",
			Message:    fmt.Sprintf("The peering connection '%s' is in state '%s' and cannot be deleted", pcxID, pcx.Status.Code),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// Transition to deleted (keep in store so Describe can still see it).
	pcx.Status = VpcPeeringConnectionStatus{
		Code:    "deleted",
		Message: "Deleted",
	}

	if aerr := h.store.putVpcPeeringConnection(ctx, pcx); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDeleteVpcPeeringConnectionResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Return:    true,
	})
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func pcxToXML(pcx *VpcPeeringConnection) xmlVpcPeeringConnection {
	return xmlVpcPeeringConnection{
		VpcPeeringConnectionID: pcx.VpcPeeringConnectionID,
		RequesterVpcInfo: xmlVpcPeeringVpcInfo{
			OwnerID:   pcx.RequesterVpcInfo.OwnerID,
			VpcID:     pcx.RequesterVpcInfo.VpcID,
			CidrBlock: pcx.RequesterVpcInfo.CidrBlock,
			Region:    pcx.RequesterVpcInfo.Region,
		},
		AccepterVpcInfo: xmlVpcPeeringVpcInfo{
			OwnerID:   pcx.AccepterVpcInfo.OwnerID,
			VpcID:     pcx.AccepterVpcInfo.VpcID,
			CidrBlock: pcx.AccepterVpcInfo.CidrBlock,
			Region:    pcx.AccepterVpcInfo.Region,
		},
		Status: xmlVpcPeeringStatus{
			Code:    pcx.Status.Code,
			Message: pcx.Status.Message,
		},
	}
}
