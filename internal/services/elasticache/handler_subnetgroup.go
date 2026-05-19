package elasticache

import (
	"encoding/xml"
	"fmt"
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
)

// ── XML types for subnet groups ──────────────────────────────────────────────

type xmlCreateCacheSubnetGroupResponse struct {
	XMLName          xml.Name                        `xml:"CreateCacheSubnetGroupResponse"`
	Xmlns            string                          `xml:"xmlns,attr"`
	Result           xmlCreateCacheSubnetGroupResult `xml:"CreateCacheSubnetGroupResult"`
	ResponseMetadata protocol.ResponseMetadata       `xml:"ResponseMetadata"`
}

type xmlCreateCacheSubnetGroupResult struct {
	CacheSubnetGroup xmlCacheSubnetGroup `xml:"CacheSubnetGroup"`
}

type xmlDeleteCacheSubnetGroupResponse struct {
	XMLName          xml.Name                  `xml:"DeleteCacheSubnetGroupResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlDescribeCacheSubnetGroupsResponse struct {
	XMLName          xml.Name                           `xml:"DescribeCacheSubnetGroupsResponse"`
	Xmlns            string                             `xml:"xmlns,attr"`
	Result           xmlDescribeCacheSubnetGroupsResult `xml:"DescribeCacheSubnetGroupsResult"`
	ResponseMetadata protocol.ResponseMetadata          `xml:"ResponseMetadata"`
}

type xmlDescribeCacheSubnetGroupsResult struct {
	CacheSubnetGroups xmlCacheSubnetGroups `xml:"CacheSubnetGroups"`
}

type xmlCacheSubnetGroups struct {
	Items []xmlCacheSubnetGroup `xml:"CacheSubnetGroup"`
}

type xmlCacheSubnetGroup struct {
	CacheSubnetGroupName        string     `xml:"CacheSubnetGroupName"`
	CacheSubnetGroupDescription string     `xml:"CacheSubnetGroupDescription"`
	ARN                         string     `xml:"ARN"`
	VpcId                       string     `xml:"VpcId"`
	Subnets                     xmlSubnets `xml:"Subnets"`
}

type xmlSubnets struct {
	Items []xmlSubnet `xml:"Subnet"`
}

type xmlSubnet struct {
	SubnetIdentifier string `xml:"SubnetIdentifier"`
}

// ── CreateCacheSubnetGroup ────────────────────────────────────────────────────

func (h *Handler) CreateCacheSubnetGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("CacheSubnetGroupName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("CacheSubnetGroupName is required"))
		return
	}

	if _, aerr := h.store.getCacheSubnetGroup(r.Context(), name); aerr == nil {
		protocol.WriteQueryXMLError(w, r, errSubnetGroupAlreadyExists(name))
		return
	}

	description := r.FormValue("CacheSubnetGroupDescription")
	vpcID := r.FormValue("VpcId")

	// Subnets arrive as SubnetIds.SubnetIdentifier.N
	var subnetIDs []string
	for i := 1; ; i++ {
		id := r.FormValue(fmt.Sprintf("SubnetIds.SubnetIdentifier.%d", i))
		if id == "" {
			break
		}
		subnetIDs = append(subnetIDs, id)
	}

	region := h.store.region(r.Context())
	arn := fmt.Sprintf("arn:aws:elasticache:%s:%s:subnetgroup:%s", region, h.cfg.AccountID, name)

	sg := &CacheSubnetGroup{
		CacheSubnetGroupName:        name,
		CacheSubnetGroupDescription: description,
		ARN:                         arn,
		VpcId:                       vpcID,
		SubnetIds:                   subnetIDs,
	}

	if aerr := h.store.putCacheSubnetGroup(r.Context(), sg); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlCreateCacheSubnetGroupResponse{
		Xmlns:            cacheXMLNS,
		Result:           xmlCreateCacheSubnetGroupResult{CacheSubnetGroup: toXMLCacheSubnetGroup(sg)},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ── DescribeCacheSubnetGroups ─────────────────────────────────────────────────

func (h *Handler) DescribeCacheSubnetGroups(w http.ResponseWriter, r *http.Request) {
	filterName := r.FormValue("CacheSubnetGroupName")

	if filterName != "" {
		sg, aerr := h.store.getCacheSubnetGroup(r.Context(), filterName)
		if aerr != nil {
			protocol.WriteQueryXMLError(w, r, aerr)
			return
		}
		protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeCacheSubnetGroupsResponse{
			Xmlns: cacheXMLNS,
			Result: xmlDescribeCacheSubnetGroupsResult{
				CacheSubnetGroups: xmlCacheSubnetGroups{Items: []xmlCacheSubnetGroup{toXMLCacheSubnetGroup(sg)}},
			},
			ResponseMetadata: protocol.QueryResponseMetadata(r),
		})
		return
	}

	all, aerr := h.store.listCacheSubnetGroups(r.Context())
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	items := make([]xmlCacheSubnetGroup, 0, len(all))
	for _, sg := range all {
		items = append(items, toXMLCacheSubnetGroup(sg))
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeCacheSubnetGroupsResponse{
		Xmlns:            cacheXMLNS,
		Result:           xmlDescribeCacheSubnetGroupsResult{CacheSubnetGroups: xmlCacheSubnetGroups{Items: items}},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ── DeleteCacheSubnetGroup ────────────────────────────────────────────────────

func (h *Handler) DeleteCacheSubnetGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("CacheSubnetGroupName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("CacheSubnetGroupName is required"))
		return
	}

	if _, aerr := h.store.getCacheSubnetGroup(r.Context(), name); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	if aerr := h.store.deleteCacheSubnetGroup(r.Context(), name); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDeleteCacheSubnetGroupResponse{
		Xmlns:            cacheXMLNS,
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ── Helper ────────────────────────────────────────────────────────────────────

func toXMLCacheSubnetGroup(sg *CacheSubnetGroup) xmlCacheSubnetGroup {
	subnets := make([]xmlSubnet, 0, len(sg.SubnetIds))
	for _, id := range sg.SubnetIds {
		subnets = append(subnets, xmlSubnet{SubnetIdentifier: id})
	}
	return xmlCacheSubnetGroup{
		CacheSubnetGroupName:        sg.CacheSubnetGroupName,
		CacheSubnetGroupDescription: sg.CacheSubnetGroupDescription,
		ARN:                         sg.ARN,
		VpcId:                       sg.VpcId,
		Subnets:                     xmlSubnets{Items: subnets},
	}
}
