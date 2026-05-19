package appconfigdata

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (s *Service) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"StartConfigurationSession": op.NewTyped[startConfigurationSessionRequest, startConfigurationSessionResponse](
			"StartConfigurationSession", s.startConfigurationSessionTyped,
		),
		"GetLatestConfiguration": op.NewTyped[getLatestConfigurationRequest, getLatestConfigurationResponse](
			"GetLatestConfiguration", s.getLatestConfigurationTyped,
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
