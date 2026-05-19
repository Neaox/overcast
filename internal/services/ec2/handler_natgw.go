package ec2

// handler_natgw.go — CreateNatGateway, DescribeNatGateways, DeleteNatGateway handlers.

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"

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
	NatGatewayID string           `xml:"natGatewayId"`
	SubnetID     string           `xml:"subnetId"`
	VpcID        string           `xml:"vpcId"`
	State        string           `xml:"state"`
	CreateTime   string           `xml:"createTime"`
	Addresses    []xmlNatGWAddr   `xml:"natGatewayAddressSet>item"`
	Tags         []xmlResourceTag `xml:"tagSet>item,omitempty"`
}

type xmlNatGWAddr struct {
	AllocationID       string `xml:"allocationId"`
	PublicIP           string `xml:"publicIp,omitempty"`
	PrivateIP          string `xml:"privateIp,omitempty"`
	NetworkInterfaceID string `xml:"networkInterfaceId,omitempty"`
}

type xmlResourceTag struct {
	Key   string `xml:"key"`
	Value string `xml:"value"`
}

// CreateNatGateway creates a NAT gateway in a subnet.
func (h *Handler) CreateNatGateway(w http.ResponseWriter, r *http.Request) {
	subnetID := r.FormValue("SubnetId")
	allocID := r.FormValue("AllocationId")
	if subnetID == "" {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "SubnetId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// Validate subnet and resolve VPC.
	sub, aerr := h.store.getSubnet(r.Context(), subnetID)
	if aerr != nil {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
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

	// Collect tags from TagSpecification.N.Tag.M.Key/Value.
	tags := collectTagSpecifications(r, "natgateway")
	if len(tags) > 0 {
		ngw.Tags = tags
	}

	if aerr := h.store.putNatGateway(r.Context(), ngw); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	addrs := []xmlNatGWAddr{{
		AllocationID: allocID,
		PublicIP:     publicIP,
		PrivateIP:    ngw.PrivateIP,
	}}

	var xmlTags []xmlResourceTag
	for _, t := range ngw.Tags {
		xmlTags = append(xmlTags, xmlResourceTag(t))
	}

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
			Tags:         xmlTags,
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
	all, aerr := h.store.listNatGateways(r.Context())
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	// Filter by NatGatewayId.N
	filterIDs := collectFormValues(r, "NatGatewayId.")

	// Filter by Filter.N (support nat-gateway-id, vpc-id, subnet-id, state)
	filters := collectFormFilters(r)

	items := make([]xmlNatGateway, 0, len(all))
	for _, ngw := range all {
		if len(filterIDs) > 0 && !containsStr(filterIDs, ngw.NatGatewayID) {
			continue
		}
		if !matchFilters(filters, map[string]string{
			"nat-gateway-id": ngw.NatGatewayID,
			"vpc-id":         ngw.VpcID,
			"subnet-id":      ngw.SubnetID,
			"state":          ngw.State,
		}) {
			continue
		}

		addrs := []xmlNatGWAddr{{
			AllocationID: ngw.AllocationID,
			PublicIP:     ngw.PublicIP,
			PrivateIP:    ngw.PrivateIP,
		}}

		var xmlTags []xmlResourceTag
		for _, t := range ngw.Tags {
			xmlTags = append(xmlTags, xmlResourceTag(t))
		}

		items = append(items, xmlNatGateway{
			NatGatewayID: ngw.NatGatewayID,
			SubnetID:     ngw.SubnetID,
			VpcID:        ngw.VpcID,
			State:        ngw.State,
			CreateTime:   ngw.CreateTime,
			Addresses:    addrs,
			Tags:         xmlTags,
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
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "NatGatewayId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// Mark as deleting, then remove.
	ngw, aerr := h.store.getNatGateway(r.Context(), natID)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
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

// ── Helpers ─────────────────────────────────────────────────────────────────

// collectTagSpecifications parses TagSpecification.N.Tag.M.Key/Value for a given resource type.
func collectTagSpecifications(r *http.Request, resourceType string) []Tag {
	var tags []Tag
	// Find the TagSpecification.N that matches our resource type, then collect its tags.
	for n := 1; n < 10; n++ {
		rtKey := fmt.Sprintf("TagSpecification.%d.ResourceType", n)
		rt := r.FormValue(rtKey)
		if rt == "" {
			break
		}
		if rt != resourceType {
			continue
		}
		for m := 1; m < 50; m++ {
			keyKey := fmt.Sprintf("TagSpecification.%d.Tag.%d.Key", n, m)
			valKey := fmt.Sprintf("TagSpecification.%d.Tag.%d.Value", n, m)
			k := r.FormValue(keyKey)
			if k == "" {
				break
			}
			tags = append(tags, Tag{Key: k, Value: r.FormValue(valKey)})
		}
	}
	return tags
}

// collectFormFilters extracts Filter.N.Name / Filter.N.Value.M from query/form params.
func collectFormFilters(r *http.Request) map[string][]string {
	filters := make(map[string][]string)
	for k, vals := range r.Form {
		if !strings.HasPrefix(k, "Filter.") || !strings.Contains(k, ".Name") {
			continue
		}
		if len(vals) == 0 {
			continue
		}
		name := vals[0]
		prefix := k[:len(k)-len("Name")]
		for vk, vvals := range r.Form {
			if strings.HasPrefix(vk, prefix+"Value.") && len(vvals) > 0 {
				filters[name] = append(filters[name], vvals[0])
			}
		}
	}
	return filters
}

// matchFilters checks if a resource's attributes match all provided filters.
func matchFilters(filters map[string][]string, attrs map[string]string) bool {
	for name, allowed := range filters {
		val, ok := attrs[name]
		if !ok {
			return false
		}
		if !containsStr(allowed, val) {
			return false
		}
	}
	return true
}
