package acm

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (h *Handler) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"RequestCertificate": op.NewTyped[requestCertificateRequest, requestCertificateResponse](
			"RequestCertificate", h.requestCertificateTyped,
		),
		"DescribeCertificate": op.NewTyped[describeCertificateRequest, describeCertificateResponse](
			"DescribeCertificate", h.describeCertificateTyped,
		),
		"ListCertificates": op.NewTyped[listCertificatesRequest, listCertificatesResponse](
			"ListCertificates", h.listCertificatesTyped,
		),
		"DeleteCertificate": op.NewTyped[deleteCertificateRequest, struct{}](
			"DeleteCertificate", h.deleteCertificateTyped,
		),
		"ListTagsForCertificate": op.NewTyped[listTagsForCertificateRequest, listTagsForCertificateResponse](
			"ListTagsForCertificate", h.listTagsForCertificateTyped,
		),
		"AddTagsToCertificate": op.NewTyped[addTagsToCertificateRequest, struct{}](
			"AddTagsToCertificate", h.addTagsToCertificateTyped,
		),
		"RemoveTagsFromCertificate": op.NewTyped[removeTagsFromCertificateRequest, struct{}](
			"RemoveTagsFromCertificate", h.removeTagsFromCertificateTyped,
		),
	}
}

func (s *Service) Operations() []op.Operation {
	ops := s.handler.typedOp
	out := make([]op.Operation, 0, len(ops))
	for _, operation := range ops {
		out = append(out, operation)
	}
	return out
}

func (s *Service) SupportedProtocols() []codec.Codec {
	return []codec.Codec{codec.JSON10, codec.JSON11, codec.RPCv2CBOR}
}
