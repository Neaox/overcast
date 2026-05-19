//go:build dev

package kms

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		capabilities.Capability{Service: "kms", Operation: "CreateKey", Category: "Key lifecycle", Status: capabilities.StatusSupported, Notes: "`SYMMETRIC_DEFAULT` (AES-256-GCM) and `RSA_2048` key specs supported"},
		capabilities.Capability{Service: "kms", Operation: "DescribeKey", Category: "Key lifecycle", Status: capabilities.StatusSupported, Notes: "Lookup by UUID, ARN, or alias"},
		capabilities.Capability{Service: "kms", Operation: "ListKeys", Category: "Key lifecycle", Status: capabilities.StatusSupported, Notes: "Excludes `PendingDeletion` keys; no pagination (Truncated=false)"},
		capabilities.Capability{Service: "kms", Operation: "EnableKey", Category: "Key lifecycle", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "kms", Operation: "DisableKey", Category: "Key lifecycle", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "kms", Operation: "ScheduleKeyDeletion", Category: "Key lifecycle", Status: capabilities.StatusSupported, Notes: "`PendingWindowInDays` honoured; defaults to 30 days"},
		capabilities.Capability{Service: "kms", Operation: "CancelKeyDeletion", Category: "Key lifecycle", Status: capabilities.StatusSupported, Notes: "Restores key to `Disabled` state"},

		capabilities.Capability{Service: "kms", Operation: "CreateAlias", Category: "Aliases", Status: capabilities.StatusSupported, Notes: "`alias/` prefix required"},
		capabilities.Capability{Service: "kms", Operation: "DeleteAlias", Category: "Aliases", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "kms", Operation: "ListAliases", Category: "Aliases", Status: capabilities.StatusSupported, Notes: "Optional `KeyId` filter (UUID, ARN, alias)"},
		capabilities.Capability{Service: "kms", Operation: "UpdateAlias", Category: "Aliases", Status: capabilities.StatusSupported, Notes: "Updates target key for an existing alias"},

		capabilities.Capability{Service: "kms", Operation: "Encrypt", Category: "Symmetric crypto", Status: capabilities.StatusSupported, Notes: "AES-256-GCM; ciphertext envelope includes key ID"},
		capabilities.Capability{Service: "kms", Operation: "Decrypt", Category: "Symmetric crypto", Status: capabilities.StatusSupported, Notes: "Extracts key ID from ciphertext envelope"},
		capabilities.Capability{Service: "kms", Operation: "GenerateDataKey", Category: "Symmetric crypto", Status: capabilities.StatusSupported, Notes: "`AES_256` and `AES_128` specs; returns plaintext + encrypted"},
		capabilities.Capability{Service: "kms", Operation: "GenerateDataKeyWithoutPlaintext", Category: "Symmetric crypto", Status: capabilities.StatusSupported, Notes: "Returns encrypted data key only"},
		capabilities.Capability{Service: "kms", Operation: "ReEncrypt", Category: "Symmetric crypto", Status: capabilities.StatusSupported, Notes: "Decrypts and re-encrypts ciphertext with destination key"},
		capabilities.Capability{Service: "kms", Operation: "GenerateDataKeyPair", Category: "Symmetric crypto", Status: capabilities.StatusSupported, Notes: "RSA_2048, RSA_3072, RSA_4096 key pair specs"},

		capabilities.Capability{Service: "kms", Operation: "Sign", Category: "Asymmetric crypto", Status: capabilities.StatusSupported, Notes: "RSA_2048 with RSASSA_PKCS1_V1_5_SHA_256"},
		capabilities.Capability{Service: "kms", Operation: "Verify", Category: "Asymmetric crypto", Status: capabilities.StatusSupported, Notes: "Returns `SignatureValid: true/false`"},
		capabilities.Capability{Service: "kms", Operation: "GetPublicKey", Category: "Asymmetric crypto", Status: capabilities.StatusSupported, Notes: "Returns DER-encoded public key for RSA keys"},
		capabilities.Capability{Service: "kms", Operation: "VerifyMac", Category: "Asymmetric crypto", Status: capabilities.StatusSupported, Notes: "HMAC_SHA_256, HMAC_SHA_384, HMAC_SHA_512"},

		capabilities.Capability{Service: "kms", Operation: "TagResource", Category: "Tags", Status: capabilities.StatusSupported, Notes: "Add tags to a KMS key"},
		capabilities.Capability{Service: "kms", Operation: "UntagResource", Category: "Tags", Status: capabilities.StatusSupported, Notes: "Remove tags from a KMS key"},
		capabilities.Capability{Service: "kms", Operation: "ListResourceTags", Category: "Tags", Status: capabilities.StatusSupported, Notes: "List tags for a KMS key"},

		capabilities.Capability{Service: "kms", Operation: "GetKeyPolicy", Category: "Key policies", Status: capabilities.StatusSupported, Notes: "Returns default or custom key policy"},
		capabilities.Capability{Service: "kms", Operation: "PutKeyPolicy", Category: "Key policies", Status: capabilities.StatusSupported, Notes: "Attaches a key policy document"},
		capabilities.Capability{Service: "kms", Operation: "ListKeyPolicies", Category: "Key policies", Status: capabilities.StatusSupported, Notes: "Returns list of policy names"},

		capabilities.Capability{Service: "kms", Operation: "CreateGrant", Category: "Grants", Status: capabilities.StatusSupported, Notes: "Creates a grant with optional constraints and retiring principal"},
		capabilities.Capability{Service: "kms", Operation: "ListGrants", Category: "Grants", Status: capabilities.StatusSupported, Notes: "Lists grants with optional KeyId, GrantId, and GranteePrincipal filters"},
		capabilities.Capability{Service: "kms", Operation: "RevokeGrant", Category: "Grants", Status: capabilities.StatusSupported, Notes: "Revokes a grant by ID"},
		capabilities.Capability{Service: "kms", Operation: "RetireGrant", Category: "Grants", Status: capabilities.StatusSupported, Notes: "Retires a grant by ID or token"},
		capabilities.Capability{Service: "kms", Operation: "ListRetirableGrants", Category: "Grants", Status: capabilities.StatusSupported, Notes: "Lists grants retirable by a principal"},
	)
}
