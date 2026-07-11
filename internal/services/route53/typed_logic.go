package route53

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/protocol"
)

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
	XMLName    struct{}         `xml:"CreateHostedZoneResponse"`
	Xmlns      string           `xml:"xmlns,attr"`
	HostedZone r53XMLHostedZone `xml:"HostedZone"`
	ChangeInfo r53XMLChangeInfo `xml:"ChangeInfo"`
	Location   string           `xml:"Location,omitempty"`
	Meta       r53RespMeta      `xml:"ResponseMetadata"`
}

type r53XMLHostedZone struct {
	Id              string           `xml:"Id"`
	Name            string           `xml:"Name"`
	CallerReference string           `xml:"CallerReference"`
	Config          r53XMLZoneConfig `xml:"Config"`
	RecordSetCount  int              `xml:"ResourceRecordSetCount"`
}

type r53XMLZoneConfig struct {
	Comment     string `xml:"Comment,omitempty"`
	PrivateZone bool   `xml:"PrivateZone"`
}

type r53XMLChangeInfo struct {
	Id          string `xml:"Id"`
	Status      string `xml:"Status"`
	SubmittedAt string `xml:"SubmittedAt"`
}

type r53ListHostedZonesResp struct {
	XMLName     struct{}           `xml:"ListHostedZonesResponse"`
	Xmlns       string             `xml:"xmlns,attr"`
	HostedZones []r53XMLHostedZone `xml:"HostedZones>HostedZone"`
	IsTruncated bool               `xml:"IsTruncated"`
	MaxItems    string             `xml:"MaxItems"`
	Meta        r53RespMeta        `xml:"ResponseMetadata"`
}

type r53GetHostedZoneResp struct {
	XMLName    struct{}         `xml:"GetHostedZoneResponse"`
	Xmlns      string           `xml:"xmlns,attr"`
	HostedZone r53XMLHostedZone `xml:"HostedZone"`
	Meta       r53RespMeta      `xml:"ResponseMetadata"`
}

type r53DeleteHostedZoneResp struct {
	XMLName    struct{}         `xml:"DeleteHostedZoneResponse"`
	Xmlns      string           `xml:"xmlns,attr"`
	ChangeInfo r53XMLChangeInfo `xml:"ChangeInfo"`
	Meta       r53RespMeta      `xml:"ResponseMetadata"`
}

type r53ListResourceRecordSetsResp struct {
	XMLName            struct{}      `xml:"ListResourceRecordSetsResponse"`
	Xmlns              string        `xml:"xmlns,attr"`
	ResourceRecordSets []r53XMLRRSet `xml:"ResourceRecordSets>ResourceRecordSet"`
	IsTruncated        bool          `xml:"IsTruncated"`
	MaxItems           string        `xml:"MaxItems"`
	Meta               r53RespMeta   `xml:"ResponseMetadata"`
}

type r53XMLRRSet struct {
	Name            string       `xml:"Name"`
	Type            string       `xml:"Type"`
	TTL             int64        `xml:"TTL"`
	ResourceRecords []r53XMLRR   `xml:"ResourceRecords>ResourceRecord"`
	AliasTarget     *r53XMLAlias `xml:"AliasTarget"`
}

type r53XMLRR struct {
	Value string `xml:"Value"`
}

type r53XMLAlias struct {
	HostedZoneId         string `xml:"HostedZoneId"`
	DNSName              string `xml:"DNSName"`
	EvaluateTargetHealth bool   `xml:"EvaluateTargetHealth"`
}

type r53GetChangeResp struct {
	XMLName    struct{}         `xml:"GetChangeResponse"`
	Xmlns      string           `xml:"xmlns,attr"`
	ChangeInfo r53XMLChangeInfo `xml:"ChangeInfo"`
	Meta       r53RespMeta      `xml:"ResponseMetadata"`
}

// ---- Typed handler functions ----

func (s *Service) createHostedZoneTyped(ctx context.Context, req *r53CreateHostedZoneReq) (*r53CreateHostedZoneResp, *protocol.AWSError) {
	if req.Name == "" {
		return nil, &protocol.AWSError{Code: "InvalidInput", Message: "Name is required", HTTPStatus: 400}
	}
	name := req.Name
	if !strings.HasSuffix(name, ".") {
		name += "."
	}
	bareID := strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:12])
	zoneID := "/hostedzone/" + bareID
	now := s.clk.Now()
	zone := &HostedZone{
		Id:              zoneID,
		Name:            name,
		CallerReference: req.CallerReference,
		CreatedAt:       now,
		Comment:         req.HostedZoneConfig.Comment,
		PrivateZone:     req.HostedZoneConfig.PrivateZone,
	}
	if err := s.putZone(ctx, zone); err != nil {
		return nil, protocol.ErrInternalError
	}
	changeID := "/change/C" + strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:12])
	if err := s.putChange(ctx, strings.TrimPrefix(changeID, "/change/")); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &r53CreateHostedZoneResp{
		Xmlns: r53XMLNS,
		HostedZone: r53XMLHostedZone{
			Id:              zoneID,
			Name:            name,
			CallerReference: req.CallerReference,
			Config:          r53XMLZoneConfig{Comment: req.HostedZoneConfig.Comment, PrivateZone: req.HostedZoneConfig.PrivateZone},
		},
		ChangeInfo: r53XMLChangeInfo{Id: changeID, Status: "INSYNC", SubmittedAt: now.UTC().Format(time.RFC3339)},
		Location:   "/2013-04-01/hostedzone/" + bareID,
		Meta:       r53MetaFromCtx(ctx),
	}, nil
}

func (s *Service) listHostedZonesTyped(ctx context.Context, _ *r53ListHostedZonesReq) (*r53ListHostedZonesResp, *protocol.AWSError) {
	zones, err := s.listAllZones(ctx)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	xmlZones := make([]r53XMLHostedZone, 0, len(zones))
	for _, z := range zones {
		count, _ := s.countRRSets(ctx, z.Id)
		xmlZones = append(xmlZones, r53XMLHostedZone{
			Id:              z.Id,
			Name:            z.Name,
			CallerReference: z.CallerReference,
			Config:          r53XMLZoneConfig{Comment: z.Comment, PrivateZone: z.PrivateZone},
			RecordSetCount:  count,
		})
	}
	return &r53ListHostedZonesResp{
		Xmlns:       r53XMLNS,
		HostedZones: xmlZones,
		IsTruncated: false,
		MaxItems:    "100",
		Meta:        r53MetaFromCtx(ctx),
	}, nil
}

func (s *Service) getHostedZoneTyped(ctx context.Context, req *r53GetHostedZoneReq) (*r53GetHostedZoneResp, *protocol.AWSError) {
	zoneID := req.Id
	if !strings.HasPrefix(zoneID, "/hostedzone/") {
		zoneID = "/hostedzone/" + zoneID
	}
	zone, found, err := s.getZone(ctx, zoneID)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, &protocol.AWSError{Code: "NoSuchHostedZone", Message: fmt.Sprintf("No hosted zone found with ID: %s", zoneID), HTTPStatus: 404}
	}
	count, _ := s.countRRSets(ctx, zoneID)
	return &r53GetHostedZoneResp{
		Xmlns: r53XMLNS,
		HostedZone: r53XMLHostedZone{
			Id:              zone.Id,
			Name:            zone.Name,
			CallerReference: zone.CallerReference,
			Config:          r53XMLZoneConfig{Comment: zone.Comment, PrivateZone: zone.PrivateZone},
			RecordSetCount:  count,
		},
		Meta: r53MetaFromCtx(ctx),
	}, nil
}

func (s *Service) deleteHostedZoneTyped(ctx context.Context, req *r53DeleteHostedZoneReq) (*r53DeleteHostedZoneResp, *protocol.AWSError) {
	zoneID := req.Id
	if !strings.HasPrefix(zoneID, "/hostedzone/") {
		zoneID = "/hostedzone/" + zoneID
	}
	_, found, err := s.getZone(ctx, zoneID)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, &protocol.AWSError{Code: "NoSuchHostedZone", Message: fmt.Sprintf("No hosted zone found with ID: %s", zoneID), HTTPStatus: 404}
	}
	if err := s.deleteZone(ctx, zoneID); err != nil {
		return nil, protocol.ErrInternalError
	}
	changeID := "/change/C" + strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:12])
	_ = s.putChange(ctx, strings.TrimPrefix(changeID, "/change/"))
	return &r53DeleteHostedZoneResp{
		Xmlns:      r53XMLNS,
		ChangeInfo: r53XMLChangeInfo{Id: changeID, Status: "INSYNC", SubmittedAt: s.clk.Now().UTC().Format(time.RFC3339)},
		Meta:       r53MetaFromCtx(ctx),
	}, nil
}

func (s *Service) listResourceRecordSetsTyped(ctx context.Context, req *r53ListResourceRecordSetsReq) (*r53ListResourceRecordSetsResp, *protocol.AWSError) {
	zoneID := req.Id
	if !strings.HasPrefix(zoneID, "/hostedzone/") {
		zoneID = "/hostedzone/" + zoneID
	}
	if _, found, err := s.getZone(ctx, zoneID); err != nil {
		return nil, protocol.ErrInternalError
	} else if !found {
		return nil, &protocol.AWSError{Code: "NoSuchHostedZone", Message: fmt.Sprintf("No hosted zone found with ID: %s", zoneID), HTTPStatus: 404}
	}
	rrsets, err := s.listRRSets(ctx, zoneID)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	xmlRRSets := make([]r53XMLRRSet, 0, len(rrsets))
	for _, rr := range rrsets {
		xmlRR := r53XMLRRSet{Name: rr.Name, Type: rr.Type, TTL: rr.TTL}
		for _, v := range rr.ResourceRecords {
			xmlRR.ResourceRecords = append(xmlRR.ResourceRecords, r53XMLRR{Value: v})
		}
		if rr.AliasDNSName != "" {
			xmlRR.AliasTarget = &r53XMLAlias{HostedZoneId: rr.AliasHostedZoneId, DNSName: rr.AliasDNSName, EvaluateTargetHealth: rr.AliasEvaluateTargetHealth}
		}
		xmlRRSets = append(xmlRRSets, xmlRR)
	}
	return &r53ListResourceRecordSetsResp{
		Xmlns:              r53XMLNS,
		ResourceRecordSets: xmlRRSets,
		IsTruncated:        false,
		MaxItems:           "300",
		Meta:               r53MetaFromCtx(ctx),
	}, nil
}

func (s *Service) getChangeTyped(ctx context.Context, req *r53GetChangeReq) (*r53GetChangeResp, *protocol.AWSError) {
	changeID := req.Id
	status, found, err := s.getChangeStatus(ctx, changeID)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		status = "INSYNC"
	}
	return &r53GetChangeResp{
		Xmlns:      r53XMLNS,
		ChangeInfo: r53XMLChangeInfo{Id: "/change/" + changeID, Status: status, SubmittedAt: s.clk.Now().UTC().Format(time.RFC3339)},
		Meta:       r53MetaFromCtx(ctx),
	}, nil
}

// ---- Helpers ----

func r53MetaFromCtx(ctx context.Context) r53RespMeta {
	return r53RespMeta{RequestId: protocol.RequestIDFromContext(ctx)}
}
