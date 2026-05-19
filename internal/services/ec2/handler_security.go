package ec2

import (
	"encoding/xml"
	"net/http"
	"strconv"

	"github.com/Neaox/overcast/internal/protocol"
)

// ── XML response types ───────────────────────────────────────────────────────

type xmlAuthorizeSGIngressResponse struct {
	XMLName   xml.Name `xml:"AuthorizeSecurityGroupIngressResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type xmlAuthorizeSGEgressResponse struct {
	XMLName   xml.Name `xml:"AuthorizeSecurityGroupEgressResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type xmlRevokeSGIngressResponse struct {
	XMLName   xml.Name `xml:"RevokeSecurityGroupIngressResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type xmlRevokeSGEgressResponse struct {
	XMLName   xml.Name `xml:"RevokeSecurityGroupEgressResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type xmlDescribeSecurityGroupsResponse struct {
	XMLName   xml.Name           `xml:"DescribeSecurityGroupsResponse"`
	Xmlns     string             `xml:"xmlns,attr"`
	RequestID string             `xml:"requestId"`
	Groups    []xmlSecurityGroup `xml:"securityGroupInfo>item"`
}

type xmlSecurityGroup struct {
	OwnerID             string            `xml:"ownerId"`
	GroupID             string            `xml:"groupId"`
	GroupName           string            `xml:"groupName"`
	GroupDescription    string            `xml:"groupDescription"`
	VpcID               string            `xml:"vpcId"`
	IpPermissions       []xmlIpPermission `xml:"ipPermissions>item,omitempty"`
	IpPermissionsEgress []xmlIpPermission `xml:"ipPermissionsEgress>item,omitempty"`
}

type xmlIpPermission struct {
	IpProtocol string       `xml:"ipProtocol"`
	FromPort   int          `xml:"fromPort"`
	ToPort     int          `xml:"toPort"`
	IpRanges   []xmlIpRange `xml:"ipRanges>item,omitempty"`
}

type xmlIpRange struct {
	CidrIp      string `xml:"cidrIp"`
	Description string `xml:"description,omitempty"`
}

type xmlDescribeSubnetsResponse struct {
	XMLName   xml.Name    `xml:"DescribeSubnetsResponse"`
	Xmlns     string      `xml:"xmlns,attr"`
	RequestID string      `xml:"requestId"`
	Subnets   []xmlSubnet `xml:"subnetSet>item"`
}

// ── parseIpPermissions ───────────────────────────────────────────────────────

// parseIpPermissions parses IpPermissions.N.* form params.
func parseIpPermissions(r *http.Request) []IpPermission {
	var perms []IpPermission
	for i := 1; ; i++ {
		idx := strconv.Itoa(i)
		proto := r.FormValue("IpPermissions." + idx + ".IpProtocol")
		if proto == "" {
			break
		}
		fromPort := formInt(r, "IpPermissions."+idx+".FromPort", 0)
		toPort := formInt(r, "IpPermissions."+idx+".ToPort", 0)

		var ranges []IpRange
		for j := 1; ; j++ {
			cidr := r.FormValue("IpPermissions." + idx + ".IpRanges." + strconv.Itoa(j) + ".CidrIp")
			if cidr == "" {
				break
			}
			desc := r.FormValue("IpPermissions." + idx + ".IpRanges." + strconv.Itoa(j) + ".Description")
			ranges = append(ranges, IpRange{CidrIp: cidr, Description: desc})
		}

		perms = append(perms, IpPermission{
			IpProtocol: proto,
			FromPort:   fromPort,
			ToPort:     toPort,
			IpRanges:   ranges,
		})
	}
	return perms
}

// ── AuthorizeSecurityGroupIngress ────────────────────────────────────────────

// AuthorizeSecurityGroupIngress adds inbound rules to a security group.
func (h *Handler) AuthorizeSecurityGroupIngress(w http.ResponseWriter, r *http.Request) {
	groupID := r.FormValue("GroupId")
	if groupID == "" {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "GroupId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	perms := parseIpPermissions(r)
	if len(perms) == 0 {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "IpPermissions are required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	sg, aerr := h.store.getSecurityGroup(r.Context(), groupID)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	sg.IpPermissions = append(sg.IpPermissions, perms...)
	if aerr := h.store.putSecurityGroup(r.Context(), sg); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlAuthorizeSGIngressResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Return:    true,
	})
}

// ── AuthorizeSecurityGroupEgress ─────────────────────────────────────────────

// AuthorizeSecurityGroupEgress adds outbound rules to a security group.
func (h *Handler) AuthorizeSecurityGroupEgress(w http.ResponseWriter, r *http.Request) {
	groupID := r.FormValue("GroupId")
	if groupID == "" {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "GroupId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	perms := parseIpPermissions(r)
	if len(perms) == 0 {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "IpPermissions are required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	sg, aerr := h.store.getSecurityGroup(r.Context(), groupID)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	sg.IpPermissionsEgress = append(sg.IpPermissionsEgress, perms...)
	if aerr := h.store.putSecurityGroup(r.Context(), sg); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlAuthorizeSGEgressResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Return:    true,
	})
}

// ── RevokeSecurityGroupIngress ───────────────────────────────────────────────

// RevokeSecurityGroupIngress removes inbound rules from a security group.
func (h *Handler) RevokeSecurityGroupIngress(w http.ResponseWriter, r *http.Request) {
	groupID := r.FormValue("GroupId")
	if groupID == "" {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "GroupId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	perms := parseIpPermissions(r)
	if len(perms) == 0 {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "IpPermissions are required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	sg, aerr := h.store.getSecurityGroup(r.Context(), groupID)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	for _, revoke := range perms {
		if !removePermission(&sg.IpPermissions, revoke) {
			protocol.WriteXMLError(w, r, &protocol.AWSError{
				Code:       "InvalidPermission.NotFound",
				Message:    "The specified rule does not exist in this security group",
				HTTPStatus: http.StatusBadRequest,
			})
			return
		}
	}

	if aerr := h.store.putSecurityGroup(r.Context(), sg); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlRevokeSGIngressResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Return:    true,
	})
}

// ── RevokeSecurityGroupEgress ────────────────────────────────────────────────

// RevokeSecurityGroupEgress removes outbound rules from a security group.
func (h *Handler) RevokeSecurityGroupEgress(w http.ResponseWriter, r *http.Request) {
	groupID := r.FormValue("GroupId")
	if groupID == "" {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "GroupId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	perms := parseIpPermissions(r)
	if len(perms) == 0 {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "IpPermissions are required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	sg, aerr := h.store.getSecurityGroup(r.Context(), groupID)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	for _, revoke := range perms {
		if !removePermission(&sg.IpPermissionsEgress, revoke) {
			protocol.WriteXMLError(w, r, &protocol.AWSError{
				Code:       "InvalidPermission.NotFound",
				Message:    "The specified rule does not exist in this security group",
				HTTPStatus: http.StatusBadRequest,
			})
			return
		}
	}

	if aerr := h.store.putSecurityGroup(r.Context(), sg); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlRevokeSGEgressResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Return:    true,
	})
}

// removePermission removes the first matching rule from the slice. Returns false if not found.
func removePermission(perms *[]IpPermission, target IpPermission) bool {
	for i, p := range *perms {
		if p.IpProtocol == target.IpProtocol && p.FromPort == target.FromPort && p.ToPort == target.ToPort {
			if matchIpRanges(p.IpRanges, target.IpRanges) {
				*perms = append((*perms)[:i], (*perms)[i+1:]...)
				return true
			}
		}
	}
	return false
}

// matchIpRanges checks whether two IpRange slices match (by CidrIp).
func matchIpRanges(a, b []IpRange) bool {
	if len(a) != len(b) {
		return false
	}
	aSet := make(map[string]bool, len(a))
	for _, r := range a {
		aSet[r.CidrIp] = true
	}
	for _, r := range b {
		if !aSet[r.CidrIp] {
			return false
		}
	}
	return true
}

// ── DescribeSecurityGroups ───────────────────────────────────────────────────

// DescribeSecurityGroups returns security groups, optionally filtered.
func (h *Handler) DescribeSecurityGroups(w http.ResponseWriter, r *http.Request) {
	// Parse GroupId.N params.
	filterIDs := parseIndexedParam(r, "GroupId")
	filterIDSet := make(map[string]bool, len(filterIDs))
	for _, id := range filterIDs {
		filterIDSet[id] = true
	}

	// Parse filters.
	filterGroupID := parseFilterValues(r, "group-id")
	filterGroupName := parseFilterValues(r, "group-name")
	filterVpcID := parseFilterValues(r, "vpc-id")

	all, aerr := h.store.listSecurityGroups(r.Context())
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	items := make([]xmlSecurityGroup, 0, len(all))
	for _, sg := range all {
		if len(filterIDSet) > 0 && !filterIDSet[sg.GroupID] {
			continue
		}
		if len(filterGroupID) > 0 && !filterGroupID[sg.GroupID] {
			continue
		}
		if len(filterGroupName) > 0 && !filterGroupName[sg.GroupName] {
			continue
		}
		if len(filterVpcID) > 0 && !filterVpcID[sg.VpcID] {
			continue
		}

		ingress := make([]xmlIpPermission, 0, len(sg.IpPermissions))
		for _, p := range sg.IpPermissions {
			ranges := make([]xmlIpRange, 0, len(p.IpRanges))
			for _, r := range p.IpRanges {
				ranges = append(ranges, xmlIpRange(r))
			}
			ingress = append(ingress, xmlIpPermission{
				IpProtocol: p.IpProtocol,
				FromPort:   p.FromPort,
				ToPort:     p.ToPort,
				IpRanges:   ranges,
			})
		}

		egress := make([]xmlIpPermission, 0, len(sg.IpPermissionsEgress))
		for _, p := range sg.IpPermissionsEgress {
			ranges := make([]xmlIpRange, 0, len(p.IpRanges))
			for _, r := range p.IpRanges {
				ranges = append(ranges, xmlIpRange(r))
			}
			egress = append(egress, xmlIpPermission{
				IpProtocol: p.IpProtocol,
				FromPort:   p.FromPort,
				ToPort:     p.ToPort,
				IpRanges:   ranges,
			})
		}

		items = append(items, xmlSecurityGroup{
			OwnerID:             "000000000000",
			GroupID:             sg.GroupID,
			GroupName:           sg.GroupName,
			GroupDescription:    sg.Description,
			VpcID:               sg.VpcID,
			IpPermissions:       ingress,
			IpPermissionsEgress: egress,
		})
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeSecurityGroupsResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Groups:    items,
	})
}

// ── DescribeSubnets ──────────────────────────────────────────────────────────

// DescribeSubnets returns subnets, optionally filtered.
func (h *Handler) DescribeSubnets(w http.ResponseWriter, r *http.Request) {
	// Parse SubnetId.N params.
	filterIDs := parseIndexedParam(r, "SubnetId")
	filterIDSet := make(map[string]bool, len(filterIDs))
	for _, id := range filterIDs {
		filterIDSet[id] = true
	}

	// Parse filters.
	filterSubnetID := parseFilterValues(r, "subnet-id")
	filterVpcID := parseFilterValues(r, "vpc-id")
	filterAZ := parseFilterValues(r, "availability-zone")

	all, aerr := h.store.listSubnets(r.Context())
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	items := make([]xmlSubnet, 0, len(all))
	for _, sub := range all {
		if len(filterIDSet) > 0 && !filterIDSet[sub.SubnetID] {
			continue
		}
		if len(filterSubnetID) > 0 && !filterSubnetID[sub.SubnetID] {
			continue
		}
		if len(filterVpcID) > 0 && !filterVpcID[sub.VpcID] {
			continue
		}
		if len(filterAZ) > 0 && !filterAZ[sub.AvailabilityZone] {
			continue
		}

		items = append(items, xmlSubnet{
			SubnetID:                sub.SubnetID,
			State:                   sub.State,
			VpcID:                   sub.VpcID,
			CidrBlock:               sub.CidrBlock,
			AvailabilityZone:        sub.AvailabilityZone,
			AvailableIPAddressCount: 251,
			DefaultForAz:            false,
			MapPublicIPOnLaunch:     false,
		})
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeSubnetsResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Subnets:   items,
	})
}
