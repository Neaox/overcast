// Package route53 provides emulation of Amazon Route 53 (Hosted Zones and Resource Record Sets).
//
// Implemented operations (REST-XML at /2013-04-01/):
//   - POST   /2013-04-01/hostedzone                      → CreateHostedZone
//   - GET    /2013-04-01/hostedzone                      → ListHostedZones
//   - GET    /2013-04-01/hostedzone/{Id}                 → GetHostedZone
//   - DELETE /2013-04-01/hostedzone/{Id}                 → DeleteHostedZone
//   - POST   /2013-04-01/hostedzone/{Id}/rrset           → ChangeResourceRecordSets
//   - GET    /2013-04-01/hostedzone/{Id}/rrset           → ListResourceRecordSets
//   - GET    /2013-04-01/change/{Id}                     → GetChange
//
// Route 53 is a global service — hosted zones are stored without a region key.
// ChangeResourceRecordSets returns Status=INSYNC immediately (no propagation delay).
package route53

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "route53"

const r53XMLNS = "https://route53.amazonaws.com/doc/2013-04-01/"

const (
	nsZones   = "route53:zones"
	nsRRSets  = "route53:rrsets"
	nsChanges = "route53:changes"
)

// ─── Types ────────────────────────────────────────────────────────────────────

// HostedZone represents a Route 53 hosted zone.
type HostedZone struct {
	Id              string    `json:"Id"`
	Name            string    `json:"Name"`
	CallerReference string    `json:"CallerReference"`
	CreatedAt       time.Time `json:"CreatedAt"`
	Comment         string    `json:"Comment,omitempty"`
	PrivateZone     bool      `json:"PrivateZone"`
	RecordCount     int       `json:"RecordCount"`
}

// ResourceRecordSet represents a DNS record set in a hosted zone.
type ResourceRecordSet struct {
	Name            string   `json:"Name"`
	Type            string   `json:"Type"`
	TTL             int64    `json:"TTL"`
	ResourceRecords []string `json:"ResourceRecords"`
	// AliasTarget fields
	AliasHostedZoneId         string `json:"AliasHostedZoneId,omitempty"`
	AliasDNSName              string `json:"AliasDNSName,omitempty"`
	AliasEvaluateTargetHealth bool   `json:"AliasEvaluateTargetHealth,omitempty"`
}

// rrsetKey returns a unique store key for a record set within a zone.
func rrsetKey(zoneID, name, rrType string) string {
	return zoneID + "/" + name + "/" + rrType
}

// ─── Service ──────────────────────────────────────────────────────────────────

// Service implements router.Service for Route 53.
type Service struct {
	cfg     *config.Config
	store   state.Store
	clk     clock.Clock
	log     *serviceutil.ServiceLogger
	typedOp map[string]op.Operation
}

// New returns a configured Route 53 service.
func New(cfg *config.Config, st state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	s := &Service{
		cfg:   cfg,
		store: st,
		clk:   clk,
		log:   serviceutil.NewServiceLogger(logger, serviceName),
	}
	s.typedOp = s.typedOps()
	return s
}

func (s *Service) Name() string { return serviceName }

// DispatchQuery satisfies router.QueryDispatcher. Route 53 natively uses
// REST-XML (path-based routing), but the typed operations also accept
// Query-protocol form-encoded POST requests with Action fields.
func (s *Service) DispatchQuery(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			c.WriteError(w, r, &protocol.AWSError{
				Code: "UnsupportedProtocol", Message: "Route 53 does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
		if typed, ok := s.typedOp[opName]; ok {
			typed.Invoke(w, r, c)
			return
		}
		c.WriteError(w, r, protocol.ErrNotImplemented)
		return
	}
	protocol.NotImplementedQueryXML(w, r)
}

// OwnsAction satisfies router.QueryActionOwner.
func (s *Service) OwnsAction(action string) bool {
	_, ok := s.typedOp[action]
	return ok
}

func (s *Service) RegisterRoutes(r chi.Router) {
	r.Route("/2013-04-01", func(r chi.Router) {
		// Hosted zone operations
		r.Post("/hostedzone", s.createHostedZone)
		r.Get("/hostedzone", s.listHostedZones)
		r.Get("/hostedzone/{zoneId}", s.getHostedZone)
		r.Delete("/hostedzone/{zoneId}", s.deleteHostedZone)

		// Resource record set operations
		r.Post("/hostedzone/{zoneId}/rrset", s.changeResourceRecordSets)
		r.Get("/hostedzone/{zoneId}/rrset", s.listResourceRecordSets)

		// Change status (always INSYNC)
		r.Get("/change/{changeId}", s.getChange)
	})
}

// ─── Store helpers ────────────────────────────────────────────────────────────

func (s *Service) putZone(ctx context.Context, z *HostedZone) error {
	raw, err := json.Marshal(z)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsZones, z.Id, string(raw))
}

func (s *Service) getZone(ctx context.Context, id string) (*HostedZone, bool, error) {
	raw, found, err := s.store.Get(ctx, nsZones, id)
	if err != nil || !found {
		return nil, found, err
	}
	var z HostedZone
	if err := json.Unmarshal([]byte(raw), &z); err != nil {
		return nil, false, err
	}
	return &z, true, nil
}

func (s *Service) listAllZones(ctx context.Context) ([]*HostedZone, error) {
	pairs, err := s.store.Scan(ctx, nsZones, "")
	if err != nil {
		return nil, err
	}
	out := make([]*HostedZone, 0, len(pairs))
	for _, kv := range pairs {
		var z HostedZone
		if err := json.Unmarshal([]byte(kv.Value), &z); err != nil {
			continue
		}
		out = append(out, &z)
	}
	return out, nil
}

func (s *Service) deleteZone(ctx context.Context, id string) error {
	return s.store.Delete(ctx, nsZones, id)
}

func (s *Service) putRRSet(ctx context.Context, zoneID string, rr *ResourceRecordSet) error {
	raw, err := json.Marshal(rr)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsRRSets, rrsetKey(zoneID, rr.Name, rr.Type), string(raw))
}

func (s *Service) deleteRRSet(ctx context.Context, zoneID, name, rrType string) error {
	return s.store.Delete(ctx, nsRRSets, rrsetKey(zoneID, name, rrType))
}

func (s *Service) listRRSets(ctx context.Context, zoneID string) ([]*ResourceRecordSet, error) {
	prefix := zoneID + "/"
	pairs, err := s.store.Scan(ctx, nsRRSets, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]*ResourceRecordSet, 0, len(pairs))
	for _, kv := range pairs {
		var rr ResourceRecordSet
		if err := json.Unmarshal([]byte(kv.Value), &rr); err != nil {
			continue
		}
		out = append(out, &rr)
	}
	return out, nil
}

func (s *Service) putChange(ctx context.Context, changeID string) error {
	return s.store.Set(ctx, nsChanges, changeID, "INSYNC")
}

func (s *Service) getChangeStatus(ctx context.Context, changeID string) (string, bool, error) {
	v, found, err := s.store.Get(ctx, nsChanges, changeID)
	return v, found, err
}

// ─── XML request/response types ───────────────────────────────────────────────

type xmlCreateHostedZoneRequest struct {
	XMLName          xml.Name `xml:"CreateHostedZoneRequest"`
	Name             string   `xml:"Name"`
	CallerReference  string   `xml:"CallerReference"`
	HostedZoneConfig struct {
		Comment     string `xml:"Comment"`
		PrivateZone bool   `xml:"PrivateZone"`
	} `xml:"HostedZoneConfig"`
}

type xmlHostedZone struct {
	Id              string `xml:"Id"`
	Name            string `xml:"Name"`
	CallerReference string `xml:"CallerReference"`
	Config          struct {
		Comment     string `xml:"Comment,omitempty"`
		PrivateZone bool   `xml:"PrivateZone"`
	} `xml:"Config"`
	ResourceRecordSetCount int `xml:"ResourceRecordSetCount"`
}

type xmlCreateHostedZoneResponse struct {
	XMLName    xml.Name      `xml:"CreateHostedZoneResponse"`
	Xmlns      string        `xml:"xmlns,attr"`
	HostedZone xmlHostedZone `xml:"HostedZone"`
	ChangeInfo xmlChangeInfo `xml:"ChangeInfo"`
	Location   string        `xml:"Location,omitempty"`
}

type xmlGetHostedZoneResponse struct {
	XMLName    xml.Name      `xml:"GetHostedZoneResponse"`
	Xmlns      string        `xml:"xmlns,attr"`
	HostedZone xmlHostedZone `xml:"HostedZone"`
}

type xmlListHostedZonesResponse struct {
	XMLName     xml.Name        `xml:"ListHostedZonesResponse"`
	Xmlns       string          `xml:"xmlns,attr"`
	HostedZones []xmlHostedZone `xml:"HostedZones>HostedZone"`
	IsTruncated bool            `xml:"IsTruncated"`
	MaxItems    string          `xml:"MaxItems"`
}

type xmlDeleteHostedZoneResponse struct {
	XMLName    xml.Name      `xml:"DeleteHostedZoneResponse"`
	Xmlns      string        `xml:"xmlns,attr"`
	ChangeInfo xmlChangeInfo `xml:"ChangeInfo"`
}

type xmlChangeInfo struct {
	Id          string `xml:"Id"`
	Status      string `xml:"Status"`
	SubmittedAt string `xml:"SubmittedAt"`
}

type xmlChangeResourceRecordSetsRequest struct {
	XMLName     xml.Name       `xml:"ChangeResourceRecordSetsRequest"`
	ChangeBatch xmlChangeBatch `xml:"ChangeBatch"`
}

type xmlChangeBatch struct {
	Comment string      `xml:"Comment"`
	Changes []xmlChange `xml:"Changes>Change"`
}

type xmlChange struct {
	Action            string               `xml:"Action"`
	ResourceRecordSet xmlResourceRecordSet `xml:"ResourceRecordSet"`
}

type xmlResourceRecordSet struct {
	Name            string              `xml:"Name"`
	Type            string              `xml:"Type"`
	TTL             int64               `xml:"TTL"`
	ResourceRecords []xmlResourceRecord `xml:"ResourceRecords>ResourceRecord"`
	AliasTarget     *xmlAliasTarget     `xml:"AliasTarget"`
}

type xmlResourceRecord struct {
	Value string `xml:"Value"`
}

type xmlAliasTarget struct {
	HostedZoneId         string `xml:"HostedZoneId"`
	DNSName              string `xml:"DNSName"`
	EvaluateTargetHealth bool   `xml:"EvaluateTargetHealth"`
}

type xmlChangeResourceRecordSetsResponse struct {
	XMLName    xml.Name      `xml:"ChangeResourceRecordSetsResponse"`
	Xmlns      string        `xml:"xmlns,attr"`
	ChangeInfo xmlChangeInfo `xml:"ChangeInfo"`
}

type xmlListResourceRecordSetsResponse struct {
	XMLName            xml.Name               `xml:"ListResourceRecordSetsResponse"`
	Xmlns              string                 `xml:"xmlns,attr"`
	ResourceRecordSets []xmlResourceRecordSet `xml:"ResourceRecordSets>ResourceRecordSet"`
	IsTruncated        bool                   `xml:"IsTruncated"`
	MaxItems           string                 `xml:"MaxItems"`
}

type xmlGetChangeResponse struct {
	XMLName    xml.Name      `xml:"GetChangeResponse"`
	Xmlns      string        `xml:"xmlns,attr"`
	ChangeInfo xmlChangeInfo `xml:"ChangeInfo"`
}

// ─── Error helpers ────────────────────────────────────────────────────────────

func errNoSuchHostedZone(id string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "NoSuchHostedZone",
		Message:    fmt.Sprintf("No hosted zone found with ID: %s", id),
		HTTPStatus: http.StatusNotFound,
	}
}

func errInvalidInput(msg string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "InvalidInput",
		Message:    msg,
		HTTPStatus: http.StatusBadRequest,
	}
}

// ─── Handlers ─────────────────────────────────────────────────────────────────

func (s *Service) createHostedZone(w http.ResponseWriter, r *http.Request) {
	var req xmlCreateHostedZoneRequest
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		protocol.WriteXMLError(w, r, errInvalidInput("Could not parse request body: "+err.Error()))
		return
	}
	if req.Name == "" {
		protocol.WriteXMLError(w, r, errInvalidInput("Name is required"))
		return
	}
	// Normalise: ensure trailing dot
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
	if err := s.putZone(r.Context(), zone); err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}

	changeID := "/change/C" + strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:12])
	if err := s.putChange(r.Context(), strings.TrimPrefix(changeID, "/change/")); err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}

	hz := toXMLHostedZone(zone, 0)
	protocol.WriteXML(w, r, http.StatusCreated, &xmlCreateHostedZoneResponse{
		Xmlns:      r53XMLNS,
		HostedZone: hz,
		ChangeInfo: xmlChangeInfo{
			Id:          changeID,
			Status:      "INSYNC",
			SubmittedAt: now.UTC().Format(time.RFC3339),
		},
		Location: "/2013-04-01/hostedzone/" + bareID,
	})
}

func (s *Service) listHostedZones(w http.ResponseWriter, r *http.Request) {
	zones, err := s.listAllZones(r.Context())
	if err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}
	sort.Slice(zones, func(i, j int) bool { return zones[i].Name < zones[j].Name })

	xmlZones := make([]xmlHostedZone, 0, len(zones))
	for _, z := range zones {
		count, _ := s.countRRSets(r.Context(), z.Id)
		xmlZones = append(xmlZones, toXMLHostedZone(z, count))
	}
	protocol.WriteXML(w, r, http.StatusOK, &xmlListHostedZonesResponse{
		Xmlns:       r53XMLNS,
		HostedZones: xmlZones,
		IsTruncated: false,
		MaxItems:    "100",
	})
}

func (s *Service) getHostedZone(w http.ResponseWriter, r *http.Request) {
	bareID := chi.URLParam(r, "zoneId")
	zoneID := "/hostedzone/" + bareID
	zone, found, err := s.getZone(r.Context(), zoneID)
	if err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		protocol.WriteXMLError(w, r, errNoSuchHostedZone(zoneID))
		return
	}
	count, _ := s.countRRSets(r.Context(), zoneID)
	protocol.WriteXML(w, r, http.StatusOK, &xmlGetHostedZoneResponse{
		Xmlns:      r53XMLNS,
		HostedZone: toXMLHostedZone(zone, count),
	})
}

func (s *Service) deleteHostedZone(w http.ResponseWriter, r *http.Request) {
	bareID := chi.URLParam(r, "zoneId")
	zoneID := "/hostedzone/" + bareID
	_, found, err := s.getZone(r.Context(), zoneID)
	if err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		protocol.WriteXMLError(w, r, errNoSuchHostedZone(zoneID))
		return
	}
	if err := s.deleteZone(r.Context(), zoneID); err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}

	changeID := "/change/C" + strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:12])
	_ = s.putChange(r.Context(), strings.TrimPrefix(changeID, "/change/"))

	protocol.WriteXML(w, r, http.StatusOK, &xmlDeleteHostedZoneResponse{
		Xmlns: r53XMLNS,
		ChangeInfo: xmlChangeInfo{
			Id:          changeID,
			Status:      "INSYNC",
			SubmittedAt: s.clk.Now().UTC().Format(time.RFC3339),
		},
	})
}

func (s *Service) changeResourceRecordSets(w http.ResponseWriter, r *http.Request) {
	bareID := chi.URLParam(r, "zoneId")
	zoneID := "/hostedzone/" + bareID

	if _, found, err := s.getZone(r.Context(), zoneID); err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	} else if !found {
		protocol.WriteXMLError(w, r, errNoSuchHostedZone(zoneID))
		return
	}

	var req xmlChangeResourceRecordSetsRequest
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		protocol.WriteXMLError(w, r, errInvalidInput("Could not parse request body: "+err.Error()))
		return
	}

	ctx := r.Context()
	for _, change := range req.ChangeBatch.Changes {
		rr := change.ResourceRecordSet
		switch strings.ToUpper(change.Action) {
		case "CREATE", "UPSERT":
			values := make([]string, 0, len(rr.ResourceRecords))
			for _, v := range rr.ResourceRecords {
				values = append(values, v.Value)
			}
			record := &ResourceRecordSet{
				Name:            rr.Name,
				Type:            rr.Type,
				TTL:             rr.TTL,
				ResourceRecords: values,
			}
			if rr.AliasTarget != nil {
				record.AliasHostedZoneId = rr.AliasTarget.HostedZoneId
				record.AliasDNSName = rr.AliasTarget.DNSName
				record.AliasEvaluateTargetHealth = rr.AliasTarget.EvaluateTargetHealth
			}
			if err := s.putRRSet(ctx, zoneID, record); err != nil {
				protocol.WriteXMLError(w, r, protocol.ErrInternalError)
				return
			}
		case "DELETE":
			if err := s.deleteRRSet(ctx, zoneID, rr.Name, rr.Type); err != nil {
				protocol.WriteXMLError(w, r, protocol.ErrInternalError)
				return
			}
		}
	}

	changeID := "/change/C" + strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:12])
	_ = s.putChange(ctx, strings.TrimPrefix(changeID, "/change/"))

	protocol.WriteXML(w, r, http.StatusOK, &xmlChangeResourceRecordSetsResponse{
		Xmlns: r53XMLNS,
		ChangeInfo: xmlChangeInfo{
			Id:          changeID,
			Status:      "INSYNC",
			SubmittedAt: s.clk.Now().UTC().Format(time.RFC3339),
		},
	})
}

func (s *Service) listResourceRecordSets(w http.ResponseWriter, r *http.Request) {
	bareID := chi.URLParam(r, "zoneId")
	zoneID := "/hostedzone/" + bareID

	if _, found, err := s.getZone(r.Context(), zoneID); err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	} else if !found {
		protocol.WriteXMLError(w, r, errNoSuchHostedZone(zoneID))
		return
	}

	rrsets, err := s.listRRSets(r.Context(), zoneID)
	if err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}
	sort.Slice(rrsets, func(i, j int) bool {
		if rrsets[i].Name != rrsets[j].Name {
			return rrsets[i].Name < rrsets[j].Name
		}
		return rrsets[i].Type < rrsets[j].Type
	})

	xmlRRSets := make([]xmlResourceRecordSet, 0, len(rrsets))
	for _, rr := range rrsets {
		xmlRR := xmlResourceRecordSet{
			Name: rr.Name,
			Type: rr.Type,
			TTL:  rr.TTL,
		}
		for _, v := range rr.ResourceRecords {
			xmlRR.ResourceRecords = append(xmlRR.ResourceRecords, xmlResourceRecord{Value: v})
		}
		if rr.AliasDNSName != "" {
			xmlRR.AliasTarget = &xmlAliasTarget{
				HostedZoneId:         rr.AliasHostedZoneId,
				DNSName:              rr.AliasDNSName,
				EvaluateTargetHealth: rr.AliasEvaluateTargetHealth,
			}
		}
		xmlRRSets = append(xmlRRSets, xmlRR)
	}

	protocol.WriteXML(w, r, http.StatusOK, &xmlListResourceRecordSetsResponse{
		Xmlns:              r53XMLNS,
		ResourceRecordSets: xmlRRSets,
		IsTruncated:        false,
		MaxItems:           "300",
	})
}

func (s *Service) getChange(w http.ResponseWriter, r *http.Request) {
	changeID := chi.URLParam(r, "changeId")
	status, found, err := s.getChangeStatus(r.Context(), changeID)
	if err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		// Unknown change IDs return INSYNC — CDK blocks on GetChange waiting for INSYNC
		status = "INSYNC"
	}
	protocol.WriteXML(w, r, http.StatusOK, &xmlGetChangeResponse{
		Xmlns: r53XMLNS,
		ChangeInfo: xmlChangeInfo{
			Id:          "/change/" + changeID,
			Status:      status,
			SubmittedAt: s.clk.Now().UTC().Format(time.RFC3339),
		},
	})
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func toXMLHostedZone(z *HostedZone, rrCount int) xmlHostedZone {
	hz := xmlHostedZone{
		Id:                     z.Id,
		Name:                   z.Name,
		CallerReference:        z.CallerReference,
		ResourceRecordSetCount: rrCount,
	}
	hz.Config.Comment = z.Comment
	hz.Config.PrivateZone = z.PrivateZone
	return hz
}

func (s *Service) countRRSets(ctx context.Context, zoneID string) (int, error) {
	rrs, err := s.listRRSets(ctx, zoneID)
	return len(rrs), err
}
