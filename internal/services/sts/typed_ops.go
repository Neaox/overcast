package sts

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (h *Handler) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"GetCallerIdentity":         op.NewTyped[struct{}, getCallerIdentityResp]("GetCallerIdentity", h.getCallerIdentityTyped),
		"GetSessionToken":           op.NewTyped[getSessionTokenReq, getSessionTokenResp]("GetSessionToken", h.getSessionTokenTyped),
		"GetFederationToken":        op.NewTyped[getFederationTokenReq, getFederationTokenResp]("GetFederationToken", h.getFederationTokenTyped),
		"AssumeRole":                op.NewTyped[assumeRoleReq, assumeRoleResp]("AssumeRole", h.assumeRoleTyped),
		"AssumeRoleWithWebIdentity": op.NewTyped[assumeRoleReq, assumeRoleWithWebIdentityResp]("AssumeRoleWithWebIdentity", h.assumeRoleWithWebIdentityTyped),
	}
}

// Operations implements router.ProtocolService.
func (s *Service) Operations() []op.Operation {
	ops := s.handler.typedOp
	out := make([]op.Operation, 0, len(ops))
	for _, operation := range ops {
		out = append(out, operation)
	}
	return out
}

// SupportedProtocols implements router.ProtocolService.
func (s *Service) SupportedProtocols() []codec.Codec {
	return []codec.Codec{codec.QueryXML}
}
