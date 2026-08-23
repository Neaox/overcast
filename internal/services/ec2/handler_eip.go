package ec2

// handler_eip.go — AllocateAddress, ReleaseAddress, DescribeAddresses,
// AssociateAddress, DisassociateAddress handlers.

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
)

// ── AllocateAddress ─────────────────────────────────────────────────────────

type xmlAllocateAddressResponse struct {
	XMLName      xml.Name `xml:"AllocateAddressResponse"`
	Xmlns        string   `xml:"xmlns,attr"`
	RequestID    string   `xml:"requestId"`
	PublicIP     string   `xml:"publicIp"`
	AllocationID string   `xml:"allocationId"`
	Domain       string   `xml:"domain"`
}

// AllocateAddress allocates an Elastic IP address.
func (h *Handler) AllocateAddress(w http.ResponseWriter, r *http.Request) {
	tags := parseTagSpecifications(r, "elastic-ip")
	// Checked before the address is allocated, as on AWS: a rejected tag must
	// fail the call rather than leave an untagged allocation behind (#1196).
	if aerr := validateTagSpecifications(tags); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	allocID := fmt.Sprintf("eipalloc-%s", shortID())
	// Generate a synthetic public IP in the 203.0.113.0/24 documentation range.
	ip := fmt.Sprintf("203.0.113.%d", syntheticIPCounter.Add(1)%254+1)

	domain := r.FormValue("Domain")
	if domain == "" {
		domain = "vpc"
	}

	addr := &ElasticIP{
		AllocationID: allocID,
		PublicIP:     ip,
		Domain:       domain,
	}
	if aerr := h.store.putElasticIP(r.Context(), addr); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	// AllocateAddressResult carries no tagSet in the model — the tags show up
	// through DescribeAddresses and DescribeTags, so the store is their only home.
	if aerr := h.putResourceTags(r.Context(), allocID, tags); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlAllocateAddressResponse{
		Xmlns:        ec2XMLNS,
		RequestID:    protocol.RequestIDFromContext(r.Context()),
		PublicIP:     addr.PublicIP,
		AllocationID: addr.AllocationID,
		Domain:       addr.Domain,
	})
}

// ── ReleaseAddress ──────────────────────────────────────────────────────────

type xmlReleaseAddressResponse struct {
	XMLName   xml.Name `xml:"ReleaseAddressResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

// ReleaseAddress releases an Elastic IP address.
func (h *Handler) ReleaseAddress(w http.ResponseWriter, r *http.Request) {
	allocID := r.FormValue("AllocationId")
	if allocID == "" {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "AllocationId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	if aerr := h.store.deleteElasticIP(r.Context(), allocID); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlReleaseAddressResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Return:    true,
	})
}

// ── DescribeAddresses ───────────────────────────────────────────────────────

type xmlDescribeAddressesResponse struct {
	XMLName    xml.Name     `xml:"DescribeAddressesResponse"`
	Xmlns      string       `xml:"xmlns,attr"`
	RequestID  string       `xml:"requestId"`
	AddressSet []xmlAddress `xml:"addressesSet>item"`
}

type xmlAddress struct {
	PublicIP       string   `xml:"publicIp"`
	AllocationID   string   `xml:"allocationId"`
	Domain         string   `xml:"domain"`
	AssociationID  string   `xml:"associationId,omitempty"`
	InstanceID     string   `xml:"instanceId,omitempty"`
	NetworkIfaceID string   `xml:"networkInterfaceId,omitempty"`
	PrivateIP      string   `xml:"privateIpAddress,omitempty"`
	TagSet         []xmlTag `xml:"tagSet>item,omitempty"`
}

// DescribeAddresses lists Elastic IP addresses.
func (h *Handler) DescribeAddresses(w http.ResponseWriter, r *http.Request) {
	resp, aerr := h.describeAddresses(r.Context(), requestQuery(r, "AllocationId"))
	writeDescribe(w, r, resp, aerr)
}

func (h *Handler) describeAddresses(ctx context.Context, q describeQuery) (*xmlDescribeAddressesResponse, *protocol.AWSError) {
	filters, aerr := addressFilters.parse(q.filters)
	if aerr != nil {
		return nil, aerr
	}
	requested := q.ids

	all, aerr := h.store.listElasticIPs(ctx)
	if aerr != nil {
		return nil, aerr
	}

	tagsView, aerr := h.tagViewFor(ctx, q.filters, true)
	if aerr != nil {
		return nil, aerr
	}

	items := make([]xmlAddress, 0, len(all))
	for _, a := range all {
		if !requested.has(a.AllocationID) || !filters.matches(a) {
			continue
		}
		tags, ok := tagsView.keep(a.AllocationID)
		if !ok {
			continue
		}
		items = append(items, xmlAddress{
			PublicIP:       a.PublicIP,
			AllocationID:   a.AllocationID,
			Domain:         a.Domain,
			AssociationID:  a.AssociationID,
			InstanceID:     a.InstanceID,
			NetworkIfaceID: a.NetworkInterfaceID,
			PrivateIP:      a.PrivateIP,
			TagSet:         xmlTagsOf(tags),
		})
	}
	return &xmlDescribeAddressesResponse{
		Xmlns:      ec2XMLNS,
		RequestID:  protocol.RequestIDFromContext(ctx),
		AddressSet: items,
	}, nil
}

// ── AssociateAddress ────────────────────────────────────────────────────────

type xmlAssociateAddressResponse struct {
	XMLName       xml.Name `xml:"AssociateAddressResponse"`
	Xmlns         string   `xml:"xmlns,attr"`
	RequestID     string   `xml:"requestId"`
	Return        bool     `xml:"return"`
	AssociationID string   `xml:"associationId"`
}

// AssociateAddress associates an Elastic IP with an instance.
func (h *Handler) AssociateAddress(w http.ResponseWriter, r *http.Request) {
	allocID := r.FormValue("AllocationId")
	instID := r.FormValue("InstanceId")
	if allocID == "" {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "AllocationId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	addr, aerr := h.store.getElasticIP(r.Context(), allocID)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	assocID := fmt.Sprintf("eipassoc-%s", shortID())
	addr.AssociationID = assocID
	addr.InstanceID = instID
	if aerr := h.store.putElasticIP(r.Context(), addr); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlAssociateAddressResponse{
		Xmlns:         ec2XMLNS,
		RequestID:     protocol.RequestIDFromContext(r.Context()),
		Return:        true,
		AssociationID: assocID,
	})
}

// ── DisassociateAddress ─────────────────────────────────────────────────────

type xmlDisassociateAddressResponse struct {
	XMLName   xml.Name `xml:"DisassociateAddressResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

// DisassociateAddress disassociates an Elastic IP from an instance.
func (h *Handler) DisassociateAddress(w http.ResponseWriter, r *http.Request) {
	assocID := r.FormValue("AssociationId")
	if assocID == "" {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "AssociationId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// Find the EIP by association ID.
	all, aerr := h.store.listElasticIPs(r.Context())
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	for _, a := range all {
		if a.AssociationID == assocID {
			a.AssociationID = ""
			a.InstanceID = ""
			a.NetworkInterfaceID = ""
			a.PrivateIP = ""
			if aerr := h.store.putElasticIP(r.Context(), a); aerr != nil {
				protocol.WriteEC2QueryXMLError(w, r, aerr)
				return
			}
			break
		}
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDisassociateAddressResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Return:    true,
	})
}
