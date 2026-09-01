package acm

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/overcast-sh/overcast/internal/middleware"
	"github.com/overcast-sh/overcast/internal/protocol"
)

type requestCertificateRequest struct {
	DomainName              string   `json:"DomainName"`
	SubjectAlternativeNames []string `json:"SubjectAlternativeNames"`
	Tags                    []Tag    `json:"Tags"`
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

// The modern aliases. ACM added TagResource / UntagResource /
// ListTagsForResource alongside the *Certificate spellings; both address the
// same tag set on the same certificate.
//
// AWS spells the resource `ResourceArn` here rather than reusing
// `CertificateArn`: the pinned manifest carries operation names and HTTP
// bindings but not member shapes, so this follows the universal convention for
// operations named TagResource/UntagResource. See
// docs/plans/resource-tagging-coverage.md.

type tagResourceRequest struct {
	ResourceArn string `json:"ResourceArn"`
	Tags        []Tag  `json:"Tags"`
}

type untagResourceRequest struct {
	ResourceArn string   `json:"ResourceArn"`
	TagKeys     []string `json:"TagKeys"`
}

type listTagsForResourceRequest struct {
	ResourceArn string `json:"ResourceArn"`
}

type listTagsForResourceResponse struct {
	Tags []Tag `json:"Tags"`
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

	// Tag validation is a request-shape constraint, checked before the
	// certificate is created — the same ordering createLogGroupTyped uses
	// (internal/services/cloudwatch/logs/typed_logic.go) and for the same
	// reason: a rejected request must not leave a certificate behind with no
	// tags reachable to fix (#1052).
	merged := mergeTags(nil, req.Tags)
	if aerr := validateCertTags(merged); aerr != nil {
		return nil, aerr
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
	// AWS applies RequestCertificate's Tags when the certificate is issued,
	// and authorizes that separately from AddTagsToCertificate.
	if len(req.Tags) > 0 {
		if err := h.store.setTags(ctx, arn, merged); err != nil {
			return nil, protocol.ErrInternalError
		}
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
	if aerr := h.requireCert(ctx, req.CertificateArn); aerr != nil {
		return nil, aerr
	}
	tags, err := h.store.getTags(ctx, req.CertificateArn)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	return &listTagsForCertificateResponse{Tags: tags}, nil
}

func (h *Handler) addTagsToCertificateTyped(ctx context.Context, req *addTagsToCertificateRequest) (*struct{}, *protocol.AWSError) {
	if aerr := h.requireCert(ctx, req.CertificateArn); aerr != nil {
		return nil, aerr
	}
	existing, err := h.store.getTags(ctx, req.CertificateArn)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	merged := mergeTags(existing, req.Tags)
	if aerr := validateCertTags(merged); aerr != nil {
		return nil, aerr
	}
	if err := h.store.setTags(ctx, req.CertificateArn, merged); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &struct{}{}, nil
}

func (h *Handler) removeTagsFromCertificateTyped(ctx context.Context, req *removeTagsFromCertificateRequest) (*struct{}, *protocol.AWSError) {
	if aerr := h.requireCert(ctx, req.CertificateArn); aerr != nil {
		return nil, aerr
	}
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

// requireCert reports a missing certificate rather than letting a tag write
// land under an ARN nothing owns — DeleteCertificate is the only thing that
// clears the tag namespace, so an orphaned entry would never be collected.
func (h *Handler) requireCert(ctx context.Context, arn string) *protocol.AWSError {
	if arn == "" {
		return &protocol.AWSError{
			Code: "InvalidArnException", Message: "CertificateArn is required", HTTPStatus: http.StatusBadRequest,
		}
	}
	if _, found := h.store.getCert(ctx, arn); !found {
		return &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Certificate %s not found", arn),
			HTTPStatus: http.StatusNotFound,
		}
	}
	return nil
}

func (h *Handler) tagResourceTyped(ctx context.Context, req *tagResourceRequest) (*struct{}, *protocol.AWSError) {
	return h.addTagsToCertificateTyped(ctx, &addTagsToCertificateRequest{
		CertificateArn: req.ResourceArn, Tags: req.Tags,
	})
}

// untagResourceTyped takes TagKeys where RemoveTagsFromCertificate takes a
// Tags list, so it cannot simply delegate — the keys become the removal set.
func (h *Handler) untagResourceTyped(ctx context.Context, req *untagResourceRequest) (*struct{}, *protocol.AWSError) {
	if aerr := h.requireCert(ctx, req.ResourceArn); aerr != nil {
		return nil, aerr
	}
	existing, err := h.store.getTags(ctx, req.ResourceArn)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if err := h.store.setTags(ctx, req.ResourceArn, removeTagKeys(existing, req.TagKeys)); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &struct{}{}, nil
}

func (h *Handler) listTagsForResourceTyped(ctx context.Context, req *listTagsForResourceRequest) (*listTagsForResourceResponse, *protocol.AWSError) {
	out, aerr := h.listTagsForCertificateTyped(ctx, &listTagsForCertificateRequest{CertificateArn: req.ResourceArn})
	if aerr != nil {
		return nil, aerr
	}
	return &listTagsForResourceResponse{Tags: out.Tags}, nil
}
