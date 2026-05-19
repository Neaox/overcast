package ses

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (h *Handler) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"SendEmail":                            op.NewTyped[sendEmailReq, sendEmailResp]("SendEmail", h.sendEmailTyped),
		"SendRawEmail":                         op.NewTyped[sendRawEmailReq, sendRawEmailResp]("SendRawEmail", h.sendRawEmailTyped),
		"VerifyEmailIdentity":                  op.NewTyped[verifyEmailIdentityReq, verifyEmailIdentityResp]("VerifyEmailIdentity", h.verifyEmailIdentityTyped),
		"VerifyEmailAddress":                   op.NewTyped[verifyEmailIdentityReq, verifyEmailIdentityResp]("VerifyEmailIdentity", h.verifyEmailIdentityTyped),
		"VerifyDomainIdentity":                 op.NewTyped[verifyDomainIdentityReq, verifyDomainIdentityResp]("VerifyDomainIdentity", h.verifyDomainIdentityTyped),
		"ListIdentities":                       op.NewTyped[listIdentitiesReq, listIdentitiesResp]("ListIdentities", h.listIdentitiesTyped),
		"ListVerifiedEmailAddresses":           op.NewTyped[listVerifiedEmailAddressesReq, listVerifiedEmailAddressesResp]("ListVerifiedEmailAddresses", h.listVerifiedEmailAddressesTyped),
		"GetIdentityVerificationAttributes":    op.NewTyped[getIdentityVerificationAttributesReq, getIdentityVerificationAttributesResp]("GetIdentityVerificationAttributes", h.getIdentityVerificationAttributesTyped),
		"DeleteIdentity":                       op.NewTyped[deleteIdentityReq, deleteIdentityResp]("DeleteIdentity", h.deleteIdentityTyped),
		"DeleteVerifiedEmailAddress":           op.NewTyped[deleteIdentityReq, deleteIdentityResp]("DeleteIdentity", h.deleteIdentityTyped),
		"CreateTemplate":                       op.NewTyped[createTemplateReq, createTemplateResp]("CreateTemplate", h.createTemplateTyped),
		"GetTemplate":                          op.NewTyped[getTemplateReq, getTemplateResp]("GetTemplate", h.getTemplateTyped),
		"UpdateTemplate":                       op.NewTyped[updateTemplateReq, updateTemplateResp]("UpdateTemplate", h.updateTemplateTyped),
		"ListTemplates":                        op.NewTyped[listTemplatesReq, listTemplatesResp]("ListTemplates", h.listTemplatesTyped),
		"DeleteTemplate":                       op.NewTyped[deleteTemplateReq, deleteTemplateResp]("DeleteTemplate", h.deleteTemplateTyped),
		"SendTemplatedEmail":                   op.NewTyped[sendTemplatedEmailReq, sendTemplatedEmailResp]("SendTemplatedEmail", h.sendTemplatedEmailTyped),
		"GetSendQuota":                         op.NewTyped[getSendQuotaReq, getSendQuotaResp]("GetSendQuota", h.getSendQuotaTyped),
		"GetSendStatistics":                    op.NewTyped[getSendStatisticsReq, getSendStatisticsResp]("GetSendStatistics", h.getSendStatisticsTyped),
		"SetIdentityFeedbackForwardingEnabled": op.NewTyped[setIdentityFeedbackForwardingEnabledReq, setIdentityFeedbackForwardingEnabledResp]("SetIdentityFeedbackForwardingEnabled", h.setIdentityFeedbackForwardingEnabledTyped),
	}
}

func (s *Service) Operations() []op.Operation {
	ops := s.handler.typedOp
	out := make([]op.Operation, 0, len(ops))
	for _, o := range ops {
		out = append(out, o)
	}
	return out
}

func (s *Service) SupportedProtocols() []codec.Codec {
	return []codec.Codec{codec.QueryXML}
}

