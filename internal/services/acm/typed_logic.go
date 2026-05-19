package acm

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
)

type requestCertificateRequest struct {
	DomainName              string   `json:"DomainName"`
	SubjectAlternativeNames []string `json:"SubjectAlternativeNames"`
}

type requestCertificateResponse struct {
	CertificateArn string `json:"CertificateArn"`
}

type describeCertificateRequest struct {
	CertificateArn string `json:"CertificateArn"`
}

type describeCertificateResponse struct {
	Certificate *Certificate `json:"Certificate"`
}

type listCertificatesRequest struct{}

type listCertificatesResponse struct {
	CertificateSummaryList []certificateSummaryWire `json:"CertificateSummaryList"`
}

type certificateSummaryWire struct {
	CertificateArn string `json:"CertificateArn"`
	DomainName     string `json:"DomainName"`
	Status         string `json:"Status"`
	Type           string `json:"Type"`
}

type deleteCertificateRequest struct {
	CertificateArn string `json:"CertificateArn"`
}

type listTagsForCertificateRequest struct {
	CertificateArn string `json:"CertificateArn"`
}

type listTagsForCertificateResponse struct {
	Tags []Tag `json:"Tags"`
}

type addTagsToCertificateRequest struct {
	CertificateArn string `json:"CertificateArn"`
	Tags           []Tag  `json:"Tags"`
}

type removeTagsFromCertificateRequest struct {
	CertificateArn string `json:"CertificateArn"`
	Tags           []Tag  `json:"Tags"`
}

func (h *Handler) requestCertificateTyped(ctx context.Context, req *requestCertificateRequest) (*requestCertificateResponse, *protocol.AWSError) {
	if req.DomainName == "" {
		return nil, &protocol.AWSError{
			Code: "InvalidParameterException", Message: "DomainName is required",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	region := middleware.RegionFromContext(ctx, h.cfg.Region)
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
	if err := h.store.putCert(ctx, cert); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &requestCertificateResponse{CertificateArn: arn}, nil
}

func (h *Handler) describeCertificateTyped(ctx context.Context, req *describeCertificateRequest) (*describeCertificateResponse, *protocol.AWSError) {
	cert, found := h.store.getCert(ctx, req.CertificateArn)
	if !found {
		return nil, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Certificate %s not found", req.CertificateArn),
			HTTPStatus: http.StatusNotFound,
		}
	}
	return &describeCertificateResponse{Certificate: cert}, nil
}

func (h *Handler) listCertificatesTyped(ctx context.Context, req *listCertificatesRequest) (*listCertificatesResponse, *protocol.AWSError) {
	certs, err := h.store.listCerts(ctx)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	summaries := make([]certificateSummaryWire, 0, len(certs))
	for _, c := range certs {
		summaries = append(summaries, certificateSummaryWire{
			CertificateArn: c.CertificateArn,
			DomainName:     c.DomainName,
			Status:         c.Status,
			Type:           c.Type,
		})
	}
	return &listCertificatesResponse{CertificateSummaryList: summaries}, nil
}

func (h *Handler) deleteCertificateTyped(ctx context.Context, req *deleteCertificateRequest) (*struct{}, *protocol.AWSError) {
	if _, found := h.store.getCert(ctx, req.CertificateArn); !found {
		return nil, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Certificate %s not found", req.CertificateArn),
			HTTPStatus: http.StatusNotFound,
		}
	}
	if err := h.store.deleteCert(ctx, req.CertificateArn); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &struct{}{}, nil
}

func (h *Handler) listTagsForCertificateTyped(ctx context.Context, req *listTagsForCertificateRequest) (*listTagsForCertificateResponse, *protocol.AWSError) {
	tags, err := h.store.getTags(ctx, req.CertificateArn)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	return &listTagsForCertificateResponse{Tags: tags}, nil
}

func (h *Handler) addTagsToCertificateTyped(ctx context.Context, req *addTagsToCertificateRequest) (*struct{}, *protocol.AWSError) {
	existing, err := h.store.getTags(ctx, req.CertificateArn)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if err := h.store.setTags(ctx, req.CertificateArn, mergeTags(existing, req.Tags)); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &struct{}{}, nil
}

func (h *Handler) removeTagsFromCertificateTyped(ctx context.Context, req *removeTagsFromCertificateRequest) (*struct{}, *protocol.AWSError) {
	existing, err := h.store.getTags(ctx, req.CertificateArn)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	keys := make([]string, 0, len(req.Tags))
	for _, t := range req.Tags {
		keys = append(keys, t.Key)
	}
	if err := h.store.setTags(ctx, req.CertificateArn, removeTagKeys(existing, keys)); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &struct{}{}, nil
}
