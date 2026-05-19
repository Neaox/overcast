package opensearch

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (s *Service) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"CreateDomain": op.NewTyped[createDomainRequest, createDomainResponse](
			"CreateDomain", s.createDomainTyped,
		),
		"DescribeDomain": op.NewTyped[describeDomainRequest, describeDomainResponse](
			"DescribeDomain", s.describeDomainTyped,
		),
		"DeleteDomain": op.NewTyped[deleteDomainRequest, deleteDomainResponse](
			"DeleteDomain", s.deleteDomainTyped,
		),
		"ListDomainNames": op.NewTyped[listDomainNamesRequest, listDomainNamesResponse](
			"ListDomainNames", s.listDomainNamesTyped,
		),
		"DescribeDomains": op.NewTyped[describeDomainsRequest, describeDomainsResponse](
			"DescribeDomains", s.describeDomainsTyped,
		),
		"AddTags": op.NewTyped[addTagsRequest, struct{}](
			"AddTags", s.addTagsTyped,
		),
		"ListTags": op.NewTyped[listTagsRequest, listTagsResponse](
			"ListTags", s.listTagsTyped,
		),
		"RemoveTags": op.NewTyped[removeTagsRequest, struct{}](
			"RemoveTags", s.removeTagsTyped,
		),
	}
}

func (s *Service) Operations() []op.Operation {
	ops := s.typedOp
	out := make([]op.Operation, 0, len(ops))
	for _, operation := range ops {
		out = append(out, operation)
	}
	return out
}

func (s *Service) SupportedProtocols() []codec.Codec {
	return []codec.Codec{codec.JSON10, codec.JSON11, codec.RPCv2CBOR}
}
