package route53

import (
	"encoding/xml"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/overcast-sh/overcast/internal/protocol"
)

// REST-XML handlers for record set and change operations.

// ─── Wire shapes ──────────────────────────────────────────────────────────────

type xmlResourceRecord struct {
	Value string `xml:"Value"`
}

type xmlAliasTarget struct {
	HostedZoneId         string `xml:"HostedZoneId"`
	DNSName              string `xml:"DNSName"`
	EvaluateTargetHealth bool   `xml:"EvaluateTargetHealth"`
}

// xmlResourceRecordSet mirrors AWS's ResourceRecordSet element order. TTL is
// a pointer on the way in so an absent TTL is distinguishable from zero.
type xmlResourceRecordSet struct {
	Name             string              `xml:"Name"`
	Type             string              `xml:"Type"`
	SetIdentifier    string              `xml:"SetIdentifier,omitempty"`
	Weight           *int64              `xml:"Weight,omitempty"`
	Region           string              `xml:"Region,omitempty"`
	GeoLocation      *GeoLocation        `xml:"GeoLocation,omitempty"`
	Failover         string              `xml:"Failover,omitempty"`
	MultiValueAnswer *bool               `xml:"MultiValueAnswer,omitempty"`
	TTL              *int64              `xml:"TTL,omitempty"`
	ResourceRecords  []xmlResourceRecord `xml:"ResourceRecords>ResourceRecord,omitempty"`
	AliasTarget      *xmlAliasTarget     `xml:"AliasTarget,omitempty"`
	HealthCheckId    string              `xml:"HealthCheckId,omitempty"`
}

// toDomainRRSet converts a wire record set into the persisted form.
func (x *xmlResourceRecordSet) toDomainRRSet() (ResourceRecordSet, bool) {
	rr := ResourceRecordSet{
		Name:             x.Name,
		Type:             x.Type,
		SetIdentifier:    x.SetIdentifier,
		Weight:           x.Weight,
		Region:           x.Region,
		Failover:         x.Failover,
		MultiValueAnswer: x.MultiValueAnswer,
		GeoLocation:      x.GeoLocation,
		HealthCheckId:    x.HealthCheckId,
	}
	if x.TTL != nil {
		rr.TTL = *x.TTL
	}
	if len(x.ResourceRecords) > 0 {
		rr.ResourceRecords = make([]string, len(x.ResourceRecords))
		for i, v := range x.ResourceRecords {
			rr.ResourceRecords[i] = v.Value
		}
	}
	if x.AliasTarget != nil {
		rr.AliasHostedZoneId = x.AliasTarget.HostedZoneId
		rr.AliasDNSName = x.AliasTarget.DNSName
		rr.AliasEvaluateTargetHealth = x.AliasTarget.EvaluateTargetHealth
	}
	return rr, x.TTL != nil
}

// toWireRRSet converts a persisted record set into the wire form.
func toWireRRSet(rr *ResourceRecordSet) xmlResourceRecordSet {
	x := xmlResourceRecordSet{
		Name:             rr.Name,
		Type:             rr.Type,
		SetIdentifier:    rr.SetIdentifier,
		Weight:           rr.Weight,
		Region:           rr.Region,
		GeoLocation:      rr.GeoLocation,
		Failover:         rr.Failover,
		MultiValueAnswer: rr.MultiValueAnswer,
		HealthCheckId:    rr.HealthCheckId,
	}
	if rr.isAlias() {
		x.AliasTarget = &xmlAliasTarget{
			HostedZoneId:         rr.AliasHostedZoneId,
			DNSName:              rr.AliasDNSName,
			EvaluateTargetHealth: rr.AliasEvaluateTargetHealth,
		}
	} else {
		ttl := rr.TTL
		x.TTL = &ttl
		x.ResourceRecords = make([]xmlResourceRecord, len(rr.ResourceRecords))
		for i, v := range rr.ResourceRecords {
			x.ResourceRecords[i] = xmlResourceRecord{Value: v}
		}
	}
	return x
}

// ─── ChangeResourceRecordSets ─────────────────────────────────────────────────

type xmlChangeResourceRecordSetsRequest struct {
	XMLName     xml.Name `xml:"ChangeResourceRecordSetsRequest"`
	ChangeBatch struct {
		Comment string `xml:"Comment"`
		Changes []struct {
			Action            string               `xml:"Action"`
			ResourceRecordSet xmlResourceRecordSet `xml:"ResourceRecordSet"`
		} `xml:"Changes>Change"`
	} `xml:"ChangeBatch"`
}

type xmlChangeResourceRecordSetsResponse struct {
	XMLName    xml.Name      `xml:"ChangeResourceRecordSetsResponse"`
	Xmlns      string        `xml:"xmlns,attr"`
	ChangeInfo xmlChangeInfo `xml:"ChangeInfo"`
}

func (s *Service) changeResourceRecordSets(w http.ResponseWriter, r *http.Request) {
	var req xmlChangeResourceRecordSetsRequest
	if !decodeXMLBody(w, r, &req) {
		return
	}
	changes := make([]rrsetChange, len(req.ChangeBatch.Changes))
	for i, c := range req.ChangeBatch.Changes {
		rr, hasTTL := c.ResourceRecordSet.toDomainRRSet()
		changes[i] = rrsetChange{Action: c.Action, RRSet: rr, HasTTL: hasTTL}
	}
	change, aerr := s.changeRRSetsCore(r.Context(), chi.URLParam(r, "zoneId"), changes)
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}
	protocol.WriteXML(w, r, http.StatusOK, &xmlChangeResourceRecordSetsResponse{
		Xmlns:      r53XMLNS,
		ChangeInfo: toXMLChangeInfo(change),
	})
}

// ─── ListResourceRecordSets ───────────────────────────────────────────────────

type xmlListResourceRecordSetsResponse struct {
	XMLName              xml.Name               `xml:"ListResourceRecordSetsResponse"`
	Xmlns                string                 `xml:"xmlns,attr"`
	ResourceRecordSets   []xmlResourceRecordSet `xml:"ResourceRecordSets>ResourceRecordSet"`
	IsTruncated          bool                   `xml:"IsTruncated"`
	NextRecordName       string                 `xml:"NextRecordName,omitempty"`
	NextRecordType       string                 `xml:"NextRecordType,omitempty"`
	NextRecordIdentifier string                 `xml:"NextRecordIdentifier,omitempty"`
	MaxItems             string                 `xml:"MaxItems"`
}

func (s *Service) listResourceRecordSets(w http.ResponseWriter, r *http.Request) {
	maxItems, ok := maxItemsParam(w, r, 300)
	if !ok {
		return
	}
	q := r.URL.Query()
	page, aerr := s.listRRSetsCore(r.Context(), chi.URLParam(r, "zoneId"),
		q.Get("name"), q.Get("type"), q.Get("identifier"), maxItems)
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}
	resp := &xmlListResourceRecordSetsResponse{
		Xmlns:              r53XMLNS,
		ResourceRecordSets: make([]xmlResourceRecordSet, 0, len(page.RRSets)),
		IsTruncated:        page.IsTruncated,
		MaxItems:           strconv.Itoa(maxItems),
	}
	for _, rr := range page.RRSets {
		resp.ResourceRecordSets = append(resp.ResourceRecordSets, toWireRRSet(rr))
	}
	if page.Next != nil {
		resp.NextRecordName = page.Next.Name
		resp.NextRecordType = page.Next.Type
		resp.NextRecordIdentifier = page.Next.SetIdentifier
	}
	protocol.WriteXML(w, r, http.StatusOK, resp)
}

// ─── GetChange ────────────────────────────────────────────────────────────────

type xmlGetChangeResponse struct {
	XMLName    xml.Name      `xml:"GetChangeResponse"`
	Xmlns      string        `xml:"xmlns,attr"`
	ChangeInfo xmlChangeInfo `xml:"ChangeInfo"`
}

func (s *Service) getChange(w http.ResponseWriter, r *http.Request) {
	change, aerr := s.getChangeCore(r.Context(), chi.URLParam(r, "changeId"))
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}
	protocol.WriteXML(w, r, http.StatusOK, &xmlGetChangeResponse{
		Xmlns:      r53XMLNS,
		ChangeInfo: toXMLChangeInfo(change),
	})
}
