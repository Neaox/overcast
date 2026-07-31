package route53

import (
	"encoding/xml"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// REST-XML handlers for hosted zone operations. Handlers parse the wire
// format, delegate to the core logic, and render responses; they hold no
// business rules of their own.

// r53XMLNS is the Route 53 XML namespace.
const r53XMLNS = "https://route53.amazonaws.com/doc/2013-04-01/"

// writeError renders a Route 53 error in the ErrorResponse envelope AWS uses
// for this service (not S3's bare <Error> shape).
func writeError(w http.ResponseWriter, r *http.Request, aerr *protocol.AWSError) {
	protocol.WriteQueryXMLError(w, r, aerr)
}

// decodeXMLBody decodes a request body, rendering InvalidInput on failure.
func decodeXMLBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := xml.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, r, errInvalidInput("Could not parse request body: "+err.Error()))
		return false
	}
	return true
}

// maxItemsParam parses the maxitems query parameter, applying the
// operation's default. Route 53 rejects non-positive or non-numeric values.
func maxItemsParam(w http.ResponseWriter, r *http.Request, defaultVal int) (int, bool) {
	raw := r.URL.Query().Get("maxitems")
	if raw == "" {
		return defaultVal, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		writeError(w, r, errInvalidInput("Invalid request: Provided maxitems is not valid: "+raw))
		return 0, false
	}
	return n, true
}

// ─── Shared XML shapes ────────────────────────────────────────────────────────

type xmlChangeInfo struct {
	Id          string `xml:"Id"`
	Status      string `xml:"Status"`
	SubmittedAt string `xml:"SubmittedAt"`
}

func toXMLChangeInfo(c changeInfo) xmlChangeInfo {
	return xmlChangeInfo{Id: c.ID, Status: c.Status, SubmittedAt: c.SubmittedAt.UTC().Format(time.RFC3339)}
}

type xmlZoneConfig struct {
	Comment     string `xml:"Comment,omitempty"`
	PrivateZone bool   `xml:"PrivateZone"`
}

type xmlHostedZone struct {
	Id                     string        `xml:"Id"`
	Name                   string        `xml:"Name"`
	CallerReference        string        `xml:"CallerReference"`
	Config                 xmlZoneConfig `xml:"Config"`
	ResourceRecordSetCount int           `xml:"ResourceRecordSetCount"`
}

func toXMLHostedZone(z *HostedZone, rrCount int) xmlHostedZone {
	return xmlHostedZone{
		Id:                     z.Id,
		Name:                   z.Name,
		CallerReference:        z.CallerReference,
		Config:                 xmlZoneConfig{Comment: z.Comment, PrivateZone: z.PrivateZone},
		ResourceRecordSetCount: rrCount,
	}
}

type xmlDelegationSet struct {
	NameServers []string `xml:"NameServers>NameServer"`
}

// delegationSetFor returns the DelegationSet element for a zone, which AWS
// includes for public zones only.
func delegationSetFor(z *HostedZone) *xmlDelegationSet {
	if z.PrivateZone {
		return nil
	}
	return &xmlDelegationSet{NameServers: z.nameServers()}
}

// zonesToXML renders a zone page's zones with their record counts.
func zonesToXML(page zonePage) []xmlHostedZone {
	out := make([]xmlHostedZone, 0, len(page.Zones))
	for _, z := range page.Zones {
		out = append(out, toXMLHostedZone(z, page.Counts[z.Id]))
	}
	return out
}

// ─── CreateHostedZone ─────────────────────────────────────────────────────────

type xmlCreateHostedZoneRequest struct {
	XMLName          xml.Name `xml:"CreateHostedZoneRequest"`
	Name             string   `xml:"Name"`
	CallerReference  string   `xml:"CallerReference"`
	HostedZoneConfig struct {
		Comment     string `xml:"Comment"`
		PrivateZone bool   `xml:"PrivateZone"`
	} `xml:"HostedZoneConfig"`
	VPC *VPC `xml:"VPC"`
}

type xmlCreateHostedZoneResponse struct {
	XMLName       xml.Name          `xml:"CreateHostedZoneResponse"`
	Xmlns         string            `xml:"xmlns,attr"`
	HostedZone    xmlHostedZone     `xml:"HostedZone"`
	ChangeInfo    xmlChangeInfo     `xml:"ChangeInfo"`
	DelegationSet *xmlDelegationSet `xml:"DelegationSet,omitempty"`
	VPC           *VPC              `xml:"VPC,omitempty"`
}

func (s *Service) createHostedZone(w http.ResponseWriter, r *http.Request) {
	var req xmlCreateHostedZoneRequest
	if !decodeXMLBody(w, r, &req) {
		return
	}
	zone, change, aerr := s.createHostedZoneCore(r.Context(), createZoneInput{
		Name:            req.Name,
		CallerReference: req.CallerReference,
		Comment:         req.HostedZoneConfig.Comment,
		PrivateZone:     req.HostedZoneConfig.PrivateZone,
		VPC:             req.VPC,
	})
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}

	resp := &xmlCreateHostedZoneResponse{
		Xmlns:         r53XMLNS,
		HostedZone:    toXMLHostedZone(zone, len(defaultRecordSets(zone))),
		ChangeInfo:    toXMLChangeInfo(change),
		DelegationSet: delegationSetFor(zone),
	}
	if len(zone.VPCs) > 0 {
		resp.VPC = &zone.VPCs[0]
	}
	// AWS returns the new zone's URL in the Location header, not the body.
	w.Header().Set("Location", serviceutil.ClientBaseURL(s.cfg, r)+"/2013-04-01"+zone.Id)
	protocol.WriteXML(w, r, http.StatusCreated, resp)
}

// ─── GetHostedZone ────────────────────────────────────────────────────────────

type xmlGetHostedZoneResponse struct {
	XMLName       xml.Name          `xml:"GetHostedZoneResponse"`
	Xmlns         string            `xml:"xmlns,attr"`
	HostedZone    xmlHostedZone     `xml:"HostedZone"`
	DelegationSet *xmlDelegationSet `xml:"DelegationSet,omitempty"`
	VPCs          []VPC             `xml:"VPCs>VPC,omitempty"`
}

func (s *Service) getHostedZone(w http.ResponseWriter, r *http.Request) {
	zone, count, aerr := s.getHostedZoneCore(r.Context(), chi.URLParam(r, "zoneId"))
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}
	protocol.WriteXML(w, r, http.StatusOK, &xmlGetHostedZoneResponse{
		Xmlns:         r53XMLNS,
		HostedZone:    toXMLHostedZone(zone, count),
		DelegationSet: delegationSetFor(zone),
		VPCs:          zone.VPCs,
	})
}

// ─── ListHostedZones ──────────────────────────────────────────────────────────

type xmlListHostedZonesResponse struct {
	XMLName     xml.Name        `xml:"ListHostedZonesResponse"`
	Xmlns       string          `xml:"xmlns,attr"`
	HostedZones []xmlHostedZone `xml:"HostedZones>HostedZone"`
	Marker      string          `xml:"Marker,omitempty"`
	IsTruncated bool            `xml:"IsTruncated"`
	NextMarker  string          `xml:"NextMarker,omitempty"`
	MaxItems    string          `xml:"MaxItems"`
}

func (s *Service) listHostedZones(w http.ResponseWriter, r *http.Request) {
	maxItems, ok := maxItemsParam(w, r, 100)
	if !ok {
		return
	}
	marker := r.URL.Query().Get("marker")
	page, aerr := s.listHostedZonesCore(r.Context(), marker, maxItems)
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}
	protocol.WriteXML(w, r, http.StatusOK, &xmlListHostedZonesResponse{
		Xmlns:       r53XMLNS,
		HostedZones: zonesToXML(page),
		Marker:      marker,
		IsTruncated: page.IsTruncated,
		NextMarker:  page.NextMarker,
		MaxItems:    strconv.Itoa(maxItems),
	})
}

// ─── ListHostedZonesByName ────────────────────────────────────────────────────

type xmlListHostedZonesByNameResponse struct {
	XMLName          xml.Name        `xml:"ListHostedZonesByNameResponse"`
	Xmlns            string          `xml:"xmlns,attr"`
	HostedZones      []xmlHostedZone `xml:"HostedZones>HostedZone"`
	DNSName          string          `xml:"DNSName,omitempty"`
	HostedZoneId     string          `xml:"HostedZoneId,omitempty"`
	IsTruncated      bool            `xml:"IsTruncated"`
	NextDNSName      string          `xml:"NextDNSName,omitempty"`
	NextHostedZoneId string          `xml:"NextHostedZoneId,omitempty"`
	MaxItems         string          `xml:"MaxItems"`
}

func (s *Service) listHostedZonesByName(w http.ResponseWriter, r *http.Request) {
	maxItems, ok := maxItemsParam(w, r, 100)
	if !ok {
		return
	}
	q := r.URL.Query()
	dnsname, hostedZoneID := q.Get("dnsname"), q.Get("hostedzoneid")
	page, aerr := s.listHostedZonesByNameCore(r.Context(), dnsname, hostedZoneID, maxItems)
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}
	resp := &xmlListHostedZonesByNameResponse{
		Xmlns:        r53XMLNS,
		HostedZones:  zonesToXML(page),
		DNSName:      dnsname,
		HostedZoneId: hostedZoneID,
		IsTruncated:  page.IsTruncated,
		MaxItems:     strconv.Itoa(maxItems),
	}
	if page.NextZone != nil {
		resp.NextDNSName = page.NextZone.Name
		resp.NextHostedZoneId = page.NextZone.bareID()
	}
	protocol.WriteXML(w, r, http.StatusOK, resp)
}

// ─── GetHostedZoneCount ───────────────────────────────────────────────────────

type xmlGetHostedZoneCountResponse struct {
	XMLName         xml.Name `xml:"GetHostedZoneCountResponse"`
	Xmlns           string   `xml:"xmlns,attr"`
	HostedZoneCount int      `xml:"HostedZoneCount"`
}

func (s *Service) getHostedZoneCount(w http.ResponseWriter, r *http.Request) {
	count, aerr := s.countHostedZonesCore(r.Context())
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}
	protocol.WriteXML(w, r, http.StatusOK, &xmlGetHostedZoneCountResponse{
		Xmlns:           r53XMLNS,
		HostedZoneCount: count,
	})
}

// ─── UpdateHostedZoneComment ──────────────────────────────────────────────────

type xmlUpdateHostedZoneCommentRequest struct {
	XMLName xml.Name `xml:"UpdateHostedZoneCommentRequest"`
	Comment string   `xml:"Comment"`
}

type xmlUpdateHostedZoneCommentResponse struct {
	XMLName    xml.Name      `xml:"UpdateHostedZoneCommentResponse"`
	Xmlns      string        `xml:"xmlns,attr"`
	HostedZone xmlHostedZone `xml:"HostedZone"`
}

func (s *Service) updateHostedZoneComment(w http.ResponseWriter, r *http.Request) {
	var req xmlUpdateHostedZoneCommentRequest
	if !decodeXMLBody(w, r, &req) {
		return
	}
	zone, count, aerr := s.updateZoneCommentCore(r.Context(), chi.URLParam(r, "zoneId"), req.Comment)
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}
	protocol.WriteXML(w, r, http.StatusOK, &xmlUpdateHostedZoneCommentResponse{
		Xmlns:      r53XMLNS,
		HostedZone: toXMLHostedZone(zone, count),
	})
}

// ─── DeleteHostedZone ─────────────────────────────────────────────────────────

type xmlDeleteHostedZoneResponse struct {
	XMLName    xml.Name      `xml:"DeleteHostedZoneResponse"`
	Xmlns      string        `xml:"xmlns,attr"`
	ChangeInfo xmlChangeInfo `xml:"ChangeInfo"`
}

func (s *Service) deleteHostedZone(w http.ResponseWriter, r *http.Request) {
	change, aerr := s.deleteHostedZoneCore(r.Context(), chi.URLParam(r, "zoneId"))
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}
	protocol.WriteXML(w, r, http.StatusOK, &xmlDeleteHostedZoneResponse{
		Xmlns:      r53XMLNS,
		ChangeInfo: toXMLChangeInfo(change),
	})
}
