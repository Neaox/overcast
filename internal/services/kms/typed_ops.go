package kms

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (h *Handler) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"CreateKey": op.NewTyped[createKeyRequest, keyMetadataResponse](
			"CreateKey", h.createKeyTyped,
		),
		"DescribeKey": op.NewTyped[keyIDRequest, keyMetadataResponse](
			"DescribeKey", h.describeKeyTyped,
		),
		"ListKeys": op.NewTyped[listKeysRequest, listKeysResponse](
			"ListKeys", h.listKeysTyped,
		),
		"DisableKey": op.NewTyped[keyIDRequest, struct{}](
			"DisableKey", h.disableKeyTyped,
		),
		"EnableKey": op.NewTyped[keyIDRequest, struct{}](
			"EnableKey", h.enableKeyTyped,
		),
		"ScheduleKeyDeletion": op.NewTyped[scheduleKeyDeletionRequest, scheduleKeyDeletionResponse](
			"ScheduleKeyDeletion", h.scheduleKeyDeletionTyped,
		),
		"CancelKeyDeletion": op.NewTyped[keyIDRequest, cancelKeyDeletionResponse](
			"CancelKeyDeletion", h.cancelKeyDeletionTyped,
		),
		"CreateAlias": op.NewTyped[createAliasRequest, struct{}](
			"CreateAlias", h.createAliasTyped,
		),
		"DeleteAlias": op.NewTyped[deleteAliasRequest, struct{}](
			"DeleteAlias", h.deleteAliasTyped,
		),
		"ListAliases": op.NewTyped[keyIDRequest, listAliasesResponse](
			"ListAliases", h.listAliasesTyped,
		),
		"Encrypt": op.NewTyped[encryptRequest, encryptResponse](
			"Encrypt", h.encryptTyped,
		),
		"Decrypt": op.NewTyped[decryptRequest, decryptResponse](
			"Decrypt", h.decryptTyped,
		),
		"GenerateDataKey": op.NewTyped[generateDataKeyRequest, generateDataKeyResponse](
			"GenerateDataKey", h.generateDataKeyTyped,
		),
		"GenerateDataKeyWithoutPlaintext": op.NewTyped[generateDataKeyRequest, generateDataKeyWithoutPlaintextResponse](
			"GenerateDataKeyWithoutPlaintext", h.generateDataKeyWithoutPlaintextTyped,
		),
		"Sign": op.NewTyped[signRequest, signResponse](
			"Sign", h.signTyped,
		),
		"Verify": op.NewTyped[verifyRequest, verifyResponse](
			"Verify", h.verifyTyped,
		),
		"TagResource": op.NewTyped[tagResourceRequest, struct{}](
			"TagResource", h.tagResourceTyped,
		),
		"UntagResource": op.NewTyped[untagResourceRequest, struct{}](
			"UntagResource", h.untagResourceTyped,
		),
		"ListResourceTags": op.NewTyped[keyIDRequest, listResourceTagsResponse](
			"ListResourceTags", h.listResourceTagsTyped,
		),
		"GetPublicKey": op.NewTyped[keyIDRequest, getPublicKeyResponse](
			"GetPublicKey", h.getPublicKeyTyped,
		),
		"UpdateAlias": op.NewTyped[updateAliasRequest, struct{}](
			"UpdateAlias", h.updateAliasTyped,
		),
		"ReEncrypt": op.NewTyped[reEncryptRequest, reEncryptResponse](
			"ReEncrypt", h.reEncryptTyped,
		),
		"GenerateDataKeyPair": op.NewTyped[generateDataKeyPairRequest, generateDataKeyPairResponse](
			"GenerateDataKeyPair", h.generateDataKeyPairTyped,
		),
		"VerifyMac": op.NewTyped[verifyMacRequest, verifyMacResponse](
			"VerifyMac", h.verifyMacTyped,
		),
		"GetKeyPolicy": op.NewTyped[keyPolicyRequest, getKeyPolicyResponse](
			"GetKeyPolicy", h.getKeyPolicyTyped,
		),
		"PutKeyPolicy": op.NewTyped[putKeyPolicyRequest, struct{}](
			"PutKeyPolicy", h.putKeyPolicyTyped,
		),
		"ListKeyPolicies": op.NewTyped[keyIDRequest, listKeyPoliciesResponse](
			"ListKeyPolicies", h.listKeyPoliciesTyped,
		),
		"CreateGrant": op.NewTyped[createGrantRequest, createGrantResponse](
			"CreateGrant", h.createGrantTyped,
		),
		"ListGrants": op.NewTyped[listGrantsRequest, listGrantsResponse](
			"ListGrants", h.listGrantsTyped,
		),
		"RevokeGrant": op.NewTyped[revokeGrantRequest, struct{}](
			"RevokeGrant", h.revokeGrantTyped,
		),
		"RetireGrant": op.NewTyped[retireGrantRequest, struct{}](
			"RetireGrant", h.retireGrantTyped,
		),
		"ListRetirableGrants": op.NewTyped[listRetirableGrantsRequest, listRetirableGrantsResponse](
			"ListRetirableGrants", h.listRetirableGrantsTyped,
		),
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
	return []codec.Codec{codec.JSON10, codec.JSON11, codec.RPCv2CBOR}
}
