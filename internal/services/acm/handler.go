package acm

import (
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// Handler holds ACM handler dependencies.
type Handler struct {
	ops     map[string]http.HandlerFunc
	typedOp map[string]op.Operation
	store   *acmStore
	cfg     *config.Config
	clk     clock.Clock
}

func newHandler(cfg *config.Config, store *acmStore, clk clock.Clock) *Handler {
	h := &Handler{cfg: cfg, store: store, clk: clk}
	h.initOps()
	return h
}

func (h *Handler) initOps() {
	h.ops = map[string]http.HandlerFunc{
		"RequestCertificate":        h.requestCertificate,
		"DescribeCertificate":       h.describeCertificate,
		"ListCertificates":          h.listCertificates,
		"DeleteCertificate":         h.deleteCertificate,
		"ListTagsForCertificate":    h.listTagsForCertificate,
		"AddTagsToCertificate":      h.addTagsToCertificate,
		"RemoveTagsFromCertificate": h.removeTagsFromCertificate,
	}
	h.typedOp = h.typedOps()
}

func (h *Handler) requestCertificate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DomainName              string   `json:"DomainName"`
		SubjectAlternativeNames []string `json:"SubjectAlternativeNames"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.DomainName == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "InvalidParameterException", Message: "DomainName is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	region := middleware.RegionFromContext(r.Context(), h.cfg.Region)
	certID := uuid.NewString()
	arn := fmt.Sprintf("arn:aws:acm:%s:%s:certificate/%s", region, h.cfg.AccountID, certID)
	now := float64(h.clk.Now().Unix())

	sans := req.SubjectAlternativeNames
	if len(sans) == 0 {
		sans = []string{req.DomainName}
	}

	cert := &Certificate{
		CertificateArn:          arn,
		DomainName:              req.DomainName,
		SubjectAlternativeNames: sans,
		Status:                  "ISSUED",
		Type:                    "AMAZON_ISSUED",
		CreatedAt:               now,
		IssuedAt:                now,
	}
	if err := h.store.putCert(r.Context(), cert); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"CertificateArn": arn})
}

func (h *Handler) describeCertificate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CertificateArn string `json:"CertificateArn"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	cert, found := h.store.getCert(r.Context(), req.CertificateArn)
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Certificate %s not found", req.CertificateArn),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Certificate": cert})
}

func (h *Handler) listCertificates(w http.ResponseWriter, r *http.Request) {
	certs, err := h.store.listCerts(r.Context())
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	summaries := make([]map[string]any, 0, len(certs))
	for _, c := range certs {
		summaries = append(summaries, map[string]any{
			"CertificateArn": c.CertificateArn,
			"DomainName":     c.DomainName,
			"Status":         c.Status,
			"Type":           c.Type,
		})
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"CertificateSummaryList": summaries})
}

func (h *Handler) deleteCertificate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CertificateArn string `json:"CertificateArn"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if _, found := h.store.getCert(r.Context(), req.CertificateArn); !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Certificate %s not found", req.CertificateArn),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	if err := h.store.deleteCert(r.Context(), req.CertificateArn); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (h *Handler) listTagsForCertificate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CertificateArn string `json:"CertificateArn"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	tags, err := h.store.getTags(r.Context(), req.CertificateArn)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Tags": tags})
}

func (h *Handler) addTagsToCertificate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CertificateArn string `json:"CertificateArn"`
		Tags           []Tag  `json:"Tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	existing, err := h.store.getTags(r.Context(), req.CertificateArn)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if err := h.store.setTags(r.Context(), req.CertificateArn, mergeTags(existing, req.Tags)); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (h *Handler) removeTagsFromCertificate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CertificateArn string `json:"CertificateArn"`
		Tags           []Tag  `json:"Tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	existing, err := h.store.getTags(r.Context(), req.CertificateArn)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	keys := make([]string, 0, len(req.Tags))
	for _, t := range req.Tags {
		keys = append(keys, t.Key)
	}
	if err := h.store.setTags(r.Context(), req.CertificateArn, removeTagKeys(existing, keys)); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}
