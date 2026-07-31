package route53

import (
	"encoding/xml"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// REST-XML handlers for health check operations.

type xmlHealthCheck struct {
	Id                 string            `xml:"Id"`
	CallerReference    string            `xml:"CallerReference"`
	HealthCheckConfig  HealthCheckConfig `xml:"HealthCheckConfig"`
	HealthCheckVersion int64             `xml:"HealthCheckVersion"`
}

func toXMLHealthCheck(hc *HealthCheck) xmlHealthCheck {
	return xmlHealthCheck{
		Id:                 hc.Id,
		CallerReference:    hc.CallerReference,
		HealthCheckConfig:  hc.Config,
		HealthCheckVersion: hc.Version,
	}
}

// ─── CreateHealthCheck ────────────────────────────────────────────────────────

type xmlCreateHealthCheckRequest struct {
	XMLName           xml.Name          `xml:"CreateHealthCheckRequest"`
	CallerReference   string            `xml:"CallerReference"`
	HealthCheckConfig HealthCheckConfig `xml:"HealthCheckConfig"`
}

type xmlCreateHealthCheckResponse struct {
	XMLName     xml.Name       `xml:"CreateHealthCheckResponse"`
	Xmlns       string         `xml:"xmlns,attr"`
	HealthCheck xmlHealthCheck `xml:"HealthCheck"`
}

func (s *Service) createHealthCheck(w http.ResponseWriter, r *http.Request) {
	var req xmlCreateHealthCheckRequest
	if !decodeXMLBody(w, r, &req) {
		return
	}
	hc, aerr := s.createHealthCheckCore(r.Context(), req.CallerReference, req.HealthCheckConfig)
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}
	// As with hosted zones, AWS returns the new resource's URL in the
	// Location header.
	w.Header().Set("Location", serviceutil.ClientBaseURL(s.cfg, r)+"/2013-04-01/healthcheck/"+hc.Id)
	protocol.WriteXML(w, r, http.StatusCreated, &xmlCreateHealthCheckResponse{
		Xmlns:       r53XMLNS,
		HealthCheck: toXMLHealthCheck(hc),
	})
}

// ─── GetHealthCheck ───────────────────────────────────────────────────────────

type xmlGetHealthCheckResponse struct {
	XMLName     xml.Name       `xml:"GetHealthCheckResponse"`
	Xmlns       string         `xml:"xmlns,attr"`
	HealthCheck xmlHealthCheck `xml:"HealthCheck"`
}

func (s *Service) getHealthCheckHandler(w http.ResponseWriter, r *http.Request) {
	hc, aerr := s.requireHealthCheck(r.Context(), chi.URLParam(r, "healthCheckId"))
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}
	protocol.WriteXML(w, r, http.StatusOK, &xmlGetHealthCheckResponse{
		Xmlns:       r53XMLNS,
		HealthCheck: toXMLHealthCheck(hc),
	})
}

// ─── ListHealthChecks ─────────────────────────────────────────────────────────

type xmlListHealthChecksResponse struct {
	XMLName      xml.Name         `xml:"ListHealthChecksResponse"`
	Xmlns        string           `xml:"xmlns,attr"`
	HealthChecks []xmlHealthCheck `xml:"HealthChecks>HealthCheck"`
	Marker       string           `xml:"Marker,omitempty"`
	IsTruncated  bool             `xml:"IsTruncated"`
	NextMarker   string           `xml:"NextMarker,omitempty"`
	MaxItems     string           `xml:"MaxItems"`
}

func (s *Service) listHealthChecks(w http.ResponseWriter, r *http.Request) {
	maxItems, ok := maxItemsParam(w, r, 100)
	if !ok {
		return
	}
	marker := r.URL.Query().Get("marker")
	page, aerr := s.listHealthChecksCore(r.Context(), marker, maxItems)
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}
	resp := &xmlListHealthChecksResponse{
		Xmlns:        r53XMLNS,
		HealthChecks: make([]xmlHealthCheck, 0, len(page.HealthChecks)),
		Marker:       marker,
		IsTruncated:  page.IsTruncated,
		NextMarker:   page.NextMarker,
		MaxItems:     strconv.Itoa(maxItems),
	}
	for _, hc := range page.HealthChecks {
		resp.HealthChecks = append(resp.HealthChecks, toXMLHealthCheck(hc))
	}
	protocol.WriteXML(w, r, http.StatusOK, resp)
}

// ─── GetHealthCheckCount ──────────────────────────────────────────────────────

type xmlGetHealthCheckCountResponse struct {
	XMLName          xml.Name `xml:"GetHealthCheckCountResponse"`
	Xmlns            string   `xml:"xmlns,attr"`
	HealthCheckCount int      `xml:"HealthCheckCount"`
}

func (s *Service) getHealthCheckCount(w http.ResponseWriter, r *http.Request) {
	count, aerr := s.countHealthChecksCore(r.Context())
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}
	protocol.WriteXML(w, r, http.StatusOK, &xmlGetHealthCheckCountResponse{
		Xmlns:            r53XMLNS,
		HealthCheckCount: count,
	})
}

// ─── UpdateHealthCheck ────────────────────────────────────────────────────────

type xmlUpdateHealthCheckRequest struct {
	XMLName                      xml.Name         `xml:"UpdateHealthCheckRequest"`
	HealthCheckVersion           *int64           `xml:"HealthCheckVersion"`
	IPAddress                    *string          `xml:"IPAddress"`
	Port                         *int             `xml:"Port"`
	ResourcePath                 *string          `xml:"ResourcePath"`
	FullyQualifiedDomainName     *string          `xml:"FullyQualifiedDomainName"`
	SearchString                 *string          `xml:"SearchString"`
	FailureThreshold             *int             `xml:"FailureThreshold"`
	Inverted                     *bool            `xml:"Inverted"`
	Disabled                     *bool            `xml:"Disabled"`
	HealthThreshold              *int             `xml:"HealthThreshold"`
	ChildHealthChecks            []string         `xml:"ChildHealthChecks>ChildHealthCheck"`
	EnableSNI                    *bool            `xml:"EnableSNI"`
	Regions                      []string         `xml:"Regions>Region"`
	AlarmIdentifier              *AlarmIdentifier `xml:"AlarmIdentifier"`
	InsufficientDataHealthStatus *string          `xml:"InsufficientDataHealthStatus"`
}

type xmlUpdateHealthCheckResponse struct {
	XMLName     xml.Name       `xml:"UpdateHealthCheckResponse"`
	Xmlns       string         `xml:"xmlns,attr"`
	HealthCheck xmlHealthCheck `xml:"HealthCheck"`
}

func (s *Service) updateHealthCheck(w http.ResponseWriter, r *http.Request) {
	var req xmlUpdateHealthCheckRequest
	if !decodeXMLBody(w, r, &req) {
		return
	}
	hc, aerr := s.updateHealthCheckCore(r.Context(), chi.URLParam(r, "healthCheckId"), updateHealthCheckInput{
		HealthCheckVersion:           req.HealthCheckVersion,
		IPAddress:                    req.IPAddress,
		Port:                         req.Port,
		ResourcePath:                 req.ResourcePath,
		FullyQualifiedDomainName:     req.FullyQualifiedDomainName,
		SearchString:                 req.SearchString,
		FailureThreshold:             req.FailureThreshold,
		Inverted:                     req.Inverted,
		Disabled:                     req.Disabled,
		HealthThreshold:              req.HealthThreshold,
		ChildHealthChecks:            req.ChildHealthChecks,
		EnableSNI:                    req.EnableSNI,
		Regions:                      req.Regions,
		AlarmIdentifier:              req.AlarmIdentifier,
		InsufficientDataHealthStatus: req.InsufficientDataHealthStatus,
	})
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}
	protocol.WriteXML(w, r, http.StatusOK, &xmlUpdateHealthCheckResponse{
		Xmlns:       r53XMLNS,
		HealthCheck: toXMLHealthCheck(hc),
	})
}

// ─── DeleteHealthCheck ────────────────────────────────────────────────────────

type xmlDeleteHealthCheckResponse struct {
	XMLName xml.Name `xml:"DeleteHealthCheckResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
}

func (s *Service) deleteHealthCheckHandler(w http.ResponseWriter, r *http.Request) {
	if aerr := s.deleteHealthCheckCore(r.Context(), chi.URLParam(r, "healthCheckId")); aerr != nil {
		writeError(w, r, aerr)
		return
	}
	protocol.WriteXML(w, r, http.StatusOK, &xmlDeleteHealthCheckResponse{Xmlns: r53XMLNS})
}
