//go:build dev

package ecr

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		capabilities.Capability{Service: "ecr", Operation: "CreateRepository", Category: "General", Status: capabilities.StatusSupported, Notes: "Returns ARN, URI, and createdAt"},
		capabilities.Capability{Service: "ecr", Operation: "DescribeRepositories", Category: "General", Status: capabilities.StatusSupported, Notes: "Lists all repos or filters by name"},
		capabilities.Capability{Service: "ecr", Operation: "DeleteRepository", Category: "General", Status: capabilities.StatusSupported, Notes: "Deletes the repository and all its image records"},
		capabilities.Capability{Service: "ecr", Operation: "GetAuthorizationToken", Category: "Auth", Status: capabilities.StatusSupported, Notes: "Returns `base64(\"AWS:<password>\")` and the registry proxy endpoint; token expiry is 12 hours"},
		capabilities.Capability{Service: "ecr", Operation: "DescribeRegistry", Category: "General", Status: capabilities.StatusSupported, Notes: "Returns registry metadata with empty replication rules"},
		capabilities.Capability{Service: "ecr", Operation: "ListImages", Category: "Images", Status: capabilities.StatusSupported, Notes: "Returns image IDs (tag + digest); reconciles local registry tags when Docker is available"},
		capabilities.Capability{Service: "ecr", Operation: "DescribeImages", Category: "Images", Status: capabilities.StatusSupported, Notes: "Returns image detail objects (digest, tags, media type); reconciles local registry manifests when Docker is available"},
		capabilities.Capability{Service: "ecr", Operation: "PutImage", Category: "Images", Status: capabilities.StatusSupported, Notes: "Stores an image manifest; generates a digest if none supplied"},
		capabilities.Capability{Service: "ecr", Operation: "BatchGetImage", Category: "Images", Status: capabilities.StatusSupported, Notes: "Fetches manifests by tag or digest"},
		capabilities.Capability{Service: "ecr", Operation: "DescribeImageScanFindings", Category: "Images", Status: capabilities.StatusSupported, Notes: "Returns empty/not-scanned findings; no scan engine is emulated"},
		capabilities.Capability{Service: "ecr", Operation: "BatchDeleteImage", Category: "Images", Status: capabilities.StatusSupported, Notes: "Deletes images by tag or digest"},
		capabilities.Capability{Service: "ecr", Operation: "SetRepositoryPolicy", Category: "Policy", Status: capabilities.StatusSupported, Notes: "Stores arbitrary IAM policy text"},
		capabilities.Capability{Service: "ecr", Operation: "GetRepositoryPolicy", Category: "Policy", Status: capabilities.StatusSupported, Notes: "Retrieves stored policy; returns 400 if none set"},
		capabilities.Capability{Service: "ecr", Operation: "DeleteRepositoryPolicy", Category: "Policy", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "ecr", Operation: "PutLifecyclePolicy", Category: "Policy", Status: capabilities.StatusSupported, Notes: "Stores lifecycle policy text for the repository"},
		capabilities.Capability{Service: "ecr", Operation: "GetLifecyclePolicy", Category: "Policy", Status: capabilities.StatusSupported, Notes: "Retrieves stored lifecycle policy; returns 400 if none set"},
		capabilities.Capability{Service: "ecr", Operation: "DeleteLifecyclePolicy", Category: "Policy", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "ecr", Operation: "TagResource", Category: "Tags", Status: capabilities.StatusSupported, Notes: "Adds/merges tags onto a repository ARN"},
		capabilities.Capability{Service: "ecr", Operation: "UntagResource", Category: "Tags", Status: capabilities.StatusSupported, Notes: "Removes tag keys from a repository ARN"},
		capabilities.Capability{Service: "ecr", Operation: "ListTagsForResource", Category: "Tags", Status: capabilities.StatusSupported},
	)
}
