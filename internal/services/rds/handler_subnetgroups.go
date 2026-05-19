package rds

import (
	"net/http"
	"strconv"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
)

// ── CreateDBSubnetGroup ──────────────────────────────────────────────────────

// CreateDBSubnetGroup creates a new DB subnet group.
func (h *Handler) CreateDBSubnetGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("DBSubnetGroupName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("DBSubnetGroupName is required"))
		return
	}

	description := r.FormValue("DBSubnetGroupDescription")
	if description == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("DBSubnetGroupDescription is required"))
		return
	}

	// Check for duplicate.
	if _, aerr := h.store.getDBSubnetGroup(r.Context(), name); aerr == nil {
		protocol.WriteQueryXMLError(w, r, errDBSubnetGroupAlreadyExists(name))
		return
	}

	// Collect subnet IDs from SubnetIds.member.N (AWS query protocol) or
	// SubnetIds.SubnetIdentifier.N (AWS SDK v3 locationName).
	var subnetIds []string
	for i := 1; ; i++ {
		key := r.FormValue("SubnetIds.member." + formItoa(i))
		if key == "" {
			key = r.FormValue("SubnetIds.SubnetIdentifier." + formItoa(i))
		}
		if key == "" {
			break
		}
		subnetIds = append(subnetIds, key)
	}
	if len(subnetIds) == 0 {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("At least one SubnetId is required"))
		return
	}

	vpcId := r.FormValue("VpcId")
	if vpcId == "" && h.vpcResolver != nil && len(subnetIds) > 0 {
		vpcId = h.vpcResolver.VpcIDForSubnet(r.Context(), subnetIds[0])
	}
	if vpcId == "" {
		vpcId = "vpc-00000000"
	}

	region := h.store.region(r.Context())
	arn := protocol.ARN(region, h.cfg.AccountID, "rds", "subgrp:"+name)

	sg := &DBSubnetGroup{
		DBSubnetGroupName:        name,
		DBSubnetGroupDescription: description,
		DBSubnetGroupArn:         arn,
		VpcId:                    vpcId,
		SubnetIds:                subnetIds,
		Status:                   "Complete",
	}

	if aerr := h.store.putDBSubnetGroup(r.Context(), sg); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	h.publish(r, events.RDSSubnetGroupCreated, events.ResourcePayload{Name: name})

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlCreateDBSubnetGroupResponse{
		Xmlns:            rdsXMLNS,
		Result:           xmlCreateDBSubnetGroupResult{DBSubnetGroup: toXMLDBSubnetGroup(sg)},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ── DeleteDBSubnetGroup ──────────────────────────────────────────────────────

// DeleteDBSubnetGroup deletes a DB subnet group.
func (h *Handler) DeleteDBSubnetGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("DBSubnetGroupName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("DBSubnetGroupName is required"))
		return
	}

	if _, aerr := h.store.getDBSubnetGroup(r.Context(), name); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	if aerr := h.store.deleteDBSubnetGroup(r.Context(), name); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	h.publish(r, events.RDSSubnetGroupDeleted, events.ResourcePayload{Name: name})

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDeleteDBSubnetGroupResponse{
		Xmlns:            rdsXMLNS,
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ── DescribeDBSubnetGroups ───────────────────────────────────────────────────

// DescribeDBSubnetGroups returns DB subnet groups, optionally filtered by name.
func (h *Handler) DescribeDBSubnetGroups(w http.ResponseWriter, r *http.Request) {
	filterName := r.FormValue("DBSubnetGroupName")

	if filterName != "" {
		sg, aerr := h.store.getDBSubnetGroup(r.Context(), filterName)
		if aerr != nil {
			protocol.WriteQueryXMLError(w, r, aerr)
			return
		}
		protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeDBSubnetGroupsResponse{
			Xmlns: rdsXMLNS,
			Result: xmlDescribeDBSubnetGroupsResult{
				DBSubnetGroups: xmlDBSubnetGroups{Items: []xmlDBSubnetGroup{toXMLDBSubnetGroup(sg)}},
			},
			ResponseMetadata: protocol.QueryResponseMetadata(r),
		})
		return
	}

	all, aerr := h.store.listDBSubnetGroups(r.Context())
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	items := make([]xmlDBSubnetGroup, 0, len(all))
	for _, sg := range all {
		items = append(items, toXMLDBSubnetGroup(sg))
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeDBSubnetGroupsResponse{
		Xmlns: rdsXMLNS,
		Result: xmlDescribeDBSubnetGroupsResult{
			DBSubnetGroups: xmlDBSubnetGroups{Items: items},
		},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// formItoa is a tiny helper to generate "1", "2", etc. for numbered form params.
func formItoa(n int) string {
	return strconv.Itoa(n)
}
