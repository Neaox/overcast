//go:build dev

package secretsmanager

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		capabilities.Capability{Service: "secretsmanager", Operation: "CreateSecret", Category: "Secret CRUD", Status: capabilities.StatusSupported, Notes: "String + binary, tags, description"},
		capabilities.Capability{Service: "secretsmanager", Operation: "GetSecretValue", Category: "Secret CRUD", Status: capabilities.StatusSupported, Notes: "By name, ARN, or version ID"},
		capabilities.Capability{Service: "secretsmanager", Operation: "DescribeSecret", Category: "Secret CRUD", Status: capabilities.StatusSupported, Notes: "Metadata, tags, version map"},
		capabilities.Capability{Service: "secretsmanager", Operation: "PutSecretValue", Category: "Secret CRUD", Status: capabilities.StatusSupported, Notes: "Creates new AWSCURRENT version"},
		capabilities.Capability{Service: "secretsmanager", Operation: "UpdateSecret", Category: "Secret CRUD", Status: capabilities.StatusSupported, Notes: "Description + optional new value"},
		capabilities.Capability{Service: "secretsmanager", Operation: "ListSecrets", Category: "Secret CRUD", Status: capabilities.StatusSupported, Notes: "All secrets, sorted by name"},
		capabilities.Capability{Service: "secretsmanager", Operation: "ListSecretVersionIds", Category: "Secret CRUD", Status: capabilities.StatusSupported, Notes: "All versions with staging labels"},
		capabilities.Capability{Service: "secretsmanager", Operation: "DeleteSecret", Category: "Secret CRUD", Status: capabilities.StatusSupported, Notes: "Immediate (ForceDelete) only"},
		capabilities.Capability{Service: "secretsmanager", Operation: "BatchGetSecretValue", Category: "Secret CRUD", Status: capabilities.StatusSupported, Notes: "Partial results on missing secrets"},
		capabilities.Capability{Service: "secretsmanager", Operation: "RotateSecret", Category: "Rotation", Status: capabilities.StatusSupported, Notes: "Config only (no Lambda invocation)"},
		capabilities.Capability{Service: "secretsmanager", Operation: "CancelRotateSecret", Category: "Rotation", Status: capabilities.StatusSupported, Notes: "Clears rotation config"},
		capabilities.Capability{Service: "secretsmanager", Operation: "TagResource", Category: "Tags", Status: capabilities.StatusSupported, Notes: "Merge/overwrite tags"},
		capabilities.Capability{Service: "secretsmanager", Operation: "UntagResource", Category: "Tags", Status: capabilities.StatusSupported, Notes: "Removes specified tag keys"},
		capabilities.Capability{Service: "secretsmanager", Operation: "GetRandomPassword", Category: "Password", Status: capabilities.StatusSupported, Notes: "Configurable length + exclusions"},
		capabilities.Capability{Service: "secretsmanager", Operation: "RestoreSecret", Category: "Policy/Misc", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "secretsmanager", Operation: "GetResourcePolicy", Category: "Policy/Misc", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "secretsmanager", Operation: "PutResourcePolicy", Category: "Policy/Misc", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "secretsmanager", Operation: "DeleteResourcePolicy", Category: "Policy/Misc", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "secretsmanager", Operation: "ReplicateSecretToRegions", Category: "Policy/Misc", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "secretsmanager", Operation: "RemoveRegionsFromReplication", Category: "Policy/Misc", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "secretsmanager", Operation: "ValidateResourcePolicy", Category: "Policy/Misc", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
	)
}
