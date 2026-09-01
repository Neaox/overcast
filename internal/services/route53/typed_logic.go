package route53

import (
	"context"

	"github.com/overcast-sh/overcast/internal/protocol"
)

// Typed (Query-protocol) handlers. These are thin wrappers over the same
// core logic as the REST-XML handlers, so behaviour cannot drift between the
// two dispatch paths.

// ---- Request types ----

type r53CreateHostedZoneReq struct {
	Name             string              `json:"Name"`
	CallerReference  string              `json:"CallerReference"`
	HostedZoneConfig r53HostedZoneConfig `json:"HostedZoneConfig"`
}

type r53HostedZoneConfig struct {
	Comment     string `json:"Comment"`
	PrivateZone bool   `json:"PrivateZone"`
}

type r53ListHostedZonesReq struct{}

type r53GetHostedZoneReq struct {
	Id string `json:"Id"`
}

type r53DeleteHostedZoneReq struct {
	Id string `json:"Id"`
}

type r53ListResourceRecordSetsReq struct {
	Id string `json:"Id"`
}

type r53GetChangeReq struct {
	Id string `json:"Id"`
}

// ---- Response types ----

type r53RespMeta struct {
	RequestId string `xml:"RequestId"`
}

type r53CreateHostedZoneResp struct {
	XMLName       struct{}          `xml:"CreateHostedZoneResponse"`
	Xmlns         string            `xml:"xmlns,attr"`
	HostedZone    xmlHostedZone     `xml:"HostedZone"`
	ChangeInfo    xmlChangeInfo     `xml:"ChangeInfo"`
	DelegationSet *xmlDelegationSet `xml:"DelegationSet,omitempty"`
	Meta          r53RespMeta       `xml:"ResponseMetadata"`
}

type r53ListHostedZonesResp struct {
	XMLName     struct{}        `xml:"ListHostedZonesResponse"`
	Xmlns       string          `xml:"xmlns,attr"`
	HostedZones []xmlHostedZone `xml:"HostedZones>HostedZone"`
	IsTruncated bool            `xml:"IsTruncated"`
	MaxItems    string          `xml:"MaxItems"`
	Meta        r53RespMeta     `xml:"ResponseMetadata"`
}

type r53GetHostedZoneResp struct {
	XMLName       struct{}          `xml:"GetHostedZoneResponse"`
	Xmlns         string            `xml:"xmlns,attr"`
	HostedZone    xmlHostedZone     `xml:"HostedZone"`
	DelegationSet *xmlDelegationSet `xml:"DelegationSet,omitempty"`
	Meta          r53RespMeta       `xml:"ResponseMetadata"`
}

type r53DeleteHostedZoneResp struct {
	XMLName    struct{}      `xml:"DeleteHostedZoneResponse"`
	Xmlns      string        `xml:"xmlns,attr"`
	ChangeInfo xmlChangeInfo `xml:"ChangeInfo"`
	Meta       r53RespMeta   `xml:"ResponseMetadata"`
}

type r53ListResourceRecordSetsResp struct {
	XMLName            struct{}               `xml:"ListResourceRecordSetsResponse"`
	Xmlns              string                 `xml:"xmlns,attr"`
	ResourceRecordSets []xmlResourceRecordSet `xml:"ResourceRecordSets>ResourceRecordSet"`
	IsTruncated        bool                   `xml:"IsTruncated"`
	MaxItems           string                 `xml:"MaxItems"`
	Meta               r53RespMeta            `xml:"ResponseMetadata"`
}

type r53GetChangeResp struct {
	XMLName    struct{}      `xml:"GetChangeResponse"`
	Xmlns      string        `xml:"xmlns,attr"`
	ChangeInfo xmlChangeInfo `xml:"ChangeInfo"`
	Meta       r53RespMeta   `xml:"ResponseMetadata"`
}

// ---- Typed handler functions ----

// typedListMax is the page size for the typed list operations, which do not
// accept pagination parameters.
const typedListMax = 100

func (s *Service) createHostedZoneTyped(ctx context.Context, req *r53CreateHostedZoneReq) (*r53CreateHostedZoneResp, *protocol.AWSError) {
	zone, change, aerr := s.createHostedZoneCore(ctx, createZoneInput{
		Name:            req.Name,
		CallerReference: req.CallerReference,
		Comment:         req.HostedZoneConfig.Comment,
		PrivateZone:     req.HostedZoneConfig.PrivateZone,
	})
	if aerr != nil {
		return nil, aerr
	}
	return &r53CreateHostedZoneResp{
		Xmlns:         r53XMLNS,
		HostedZone:    toXMLHostedZone(zone, len(defaultRecordSets(zone))),
		ChangeInfo:    toXMLChangeInfo(change),
		DelegationSet: delegationSetFor(zone),
		Meta:          r53MetaFromCtx(ctx),
	}, nil
}

func (s *Service) listHostedZonesTyped(ctx context.Context, _ *r53ListHostedZonesReq) (*r53ListHostedZonesResp, *protocol.AWSError) {
	page, aerr := s.listHostedZonesCore(ctx, "", typedListMax)
	if aerr != nil {
		return nil, aerr
	}
	return &r53ListHostedZonesResp{
		Xmlns:       r53XMLNS,
		HostedZones: zonesToXML(page),
		IsTruncated: page.IsTruncated,
		MaxItems:    "100",
		Meta:        r53MetaFromCtx(ctx),
	}, nil
}

func (s *Service) getHostedZoneTyped(ctx context.Context, req *r53GetHostedZoneReq) (*r53GetHostedZoneResp, *protocol.AWSError) {
	zone, count, aerr := s.getHostedZoneCore(ctx, req.Id)
	if aerr != nil {
		return nil, aerr
	}
	return &r53GetHostedZoneResp{
		Xmlns:         r53XMLNS,
		HostedZone:    toXMLHostedZone(zone, count),
		DelegationSet: delegationSetFor(zone),
		Meta:          r53MetaFromCtx(ctx),
	}, nil
}

func (s *Service) deleteHostedZoneTyped(ctx context.Context, req *r53DeleteHostedZoneReq) (*r53DeleteHostedZoneResp, *protocol.AWSError) {
	change, aerr := s.deleteHostedZoneCore(ctx, req.Id)
	if aerr != nil {
		return nil, aerr
	}
	return &r53DeleteHostedZoneResp{
		Xmlns:      r53XMLNS,
		ChangeInfo: toXMLChangeInfo(change),
		Meta:       r53MetaFromCtx(ctx),
	}, nil
}

func (s *Service) listResourceRecordSetsTyped(ctx context.Context, req *r53ListResourceRecordSetsReq) (*r53ListResourceRecordSetsResp, *protocol.AWSError) {
	page, aerr := s.listRRSetsCore(ctx, req.Id, "", "", "", 300)
	if aerr != nil {
		return nil, aerr
	}
	rrsets := make([]xmlResourceRecordSet, 0, len(page.RRSets))
	for _, rr := range page.RRSets {
		rrsets = append(rrsets, toWireRRSet(rr))
	}
	return &r53ListResourceRecordSetsResp{
		Xmlns:              r53XMLNS,
		ResourceRecordSets: rrsets,
		IsTruncated:        page.IsTruncated,
		MaxItems:           "300",
		Meta:               r53MetaFromCtx(ctx),
	}, nil
}

func (s *Service) getChangeTyped(ctx context.Context, req *r53GetChangeReq) (*r53GetChangeResp, *protocol.AWSError) {
	change, aerr := s.getChangeCore(ctx, req.Id)
	if aerr != nil {
		return nil, aerr
	}
	return &r53GetChangeResp{
		Xmlns:      r53XMLNS,
		ChangeInfo: toXMLChangeInfo(change),
		Meta:       r53MetaFromCtx(ctx),
	}, nil
}

// ---- Helpers ----

func r53MetaFromCtx(ctx context.Context) r53RespMeta {
	return r53RespMeta{RequestId: protocol.RequestIDFromContext(ctx)}
}
