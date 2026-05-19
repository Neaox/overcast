//go:build dev

package s3

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// Buckets
		capabilities.Capability{Service: "s3", Operation: "CreateBucket", Category: "Buckets",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "s3", Operation: "DeleteBucket", Category: "Buckets",
			Status: capabilities.StatusSupported, Notes: "Bucket must be empty"},
		capabilities.Capability{Service: "s3", Operation: "HeadBucket", Category: "Buckets",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "s3", Operation: "ListBuckets", Category: "Buckets",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "s3", Operation: "GetBucketLocation", Category: "Buckets",
			Status: capabilities.StatusSupported},

		// Objects
		capabilities.Capability{Service: "s3", Operation: "PutObject", Category: "Objects",
			Status: capabilities.StatusSupported, Notes: "Stores body + x-amz-meta-* headers"},
		capabilities.Capability{Service: "s3", Operation: "GetObject", Category: "Objects",
			Status: capabilities.StatusSupported, Notes: "Returns body, ETag, metadata headers"},
		capabilities.Capability{Service: "s3", Operation: "HeadObject", Category: "Objects",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "s3", Operation: "DeleteObject", Category: "Objects",
			Status: capabilities.StatusSupported, Notes: "Idempotent — 204 for missing keys"},
		capabilities.Capability{Service: "s3", Operation: "CopyObject", Category: "Objects",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "s3", Operation: "ListObjectsV2", Category: "Objects",
			Status: capabilities.StatusSupported, Notes: "Supports prefix, delimiter, max-keys, start-after, and continuation-token pagination"},
		capabilities.Capability{Service: "s3", Operation: "DeleteObjects", Category: "Objects",
			Status: capabilities.StatusSupported, Notes: "Batch delete up to 1000 keys; quiet mode supported"},
		capabilities.Capability{Service: "s3", Operation: "ListObjects", Category: "Objects",
			Status: capabilities.StatusSupported, Notes: "Marker-based pagination; supports prefix, delimiter"},
		capabilities.Capability{Service: "s3", Operation: "GetObjectAttributes", Category: "Objects",
			Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "s3", Operation: "PutObjectTagging", Category: "Objects",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "s3", Operation: "GetObjectTagging", Category: "Objects",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "s3", Operation: "DeleteObjectTagging", Category: "Objects",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "s3", Operation: "RestoreObject", Category: "Objects",
			Status: capabilities.StatusUnsupported, Notes: "Glacier restore simulation"},
		capabilities.Capability{Service: "s3", Operation: "SelectObjectContent", Category: "Objects",
			Status: capabilities.StatusUnsupported, Notes: "S3 Select (SQL queries on objects)"},

		// Multipart uploads
		capabilities.Capability{Service: "s3", Operation: "CreateMultipartUpload", Category: "Multipart uploads",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "s3", Operation: "UploadPart", Category: "Multipart uploads",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "s3", Operation: "UploadPartCopy", Category: "Multipart uploads",
			Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "s3", Operation: "CompleteMultipartUpload", Category: "Multipart uploads",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "s3", Operation: "AbortMultipartUpload", Category: "Multipart uploads",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "s3", Operation: "ListMultipartUploads", Category: "Multipart uploads",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "s3", Operation: "ListParts", Category: "Multipart uploads",
			Status: capabilities.StatusSupported},

		// ACLs & policies
		capabilities.Capability{Service: "s3", Operation: "GetBucketAcl", Category: "ACLs & policies",
			Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "s3", Operation: "PutBucketAcl", Category: "ACLs & policies",
			Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "s3", Operation: "GetObjectAcl", Category: "ACLs & policies",
			Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "s3", Operation: "PutObjectAcl", Category: "ACLs & policies",
			Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "s3", Operation: "GetBucketPolicy", Category: "ACLs & policies",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "s3", Operation: "PutBucketPolicy", Category: "ACLs & policies",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "s3", Operation: "DeleteBucketPolicy", Category: "ACLs & policies",
			Status: capabilities.StatusSupported},

		// Versioning
		capabilities.Capability{Service: "s3", Operation: "GetBucketVersioning", Category: "Versioning",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "s3", Operation: "PutBucketVersioning", Category: "Versioning",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "s3", Operation: "ListObjectVersions", Category: "Versioning",
			Status: capabilities.StatusSupported},

		// Tagging
		capabilities.Capability{Service: "s3", Operation: "GetBucketTagging", Category: "Tagging",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "s3", Operation: "PutBucketTagging", Category: "Tagging",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "s3", Operation: "DeleteBucketTagging", Category: "Tagging",
			Status: capabilities.StatusSupported},

		// Lifecycle
		capabilities.Capability{Service: "s3", Operation: "GetBucketLifecycleConfiguration", Category: "Lifecycle",
			Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "s3", Operation: "PutBucketLifecycleConfiguration", Category: "Lifecycle",
			Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "s3", Operation: "DeleteBucketLifecycle", Category: "Lifecycle",
			Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},

		// Notifications
		capabilities.Capability{Service: "s3", Operation: "GetBucketNotificationConfiguration", Category: "Notifications",
			Status: capabilities.StatusSupported, Notes: "Returns empty config if none set"},
		capabilities.Capability{Service: "s3", Operation: "PutBucketNotificationConfiguration", Category: "Notifications",
			Status: capabilities.StatusSupported, Notes: "SQS, SNS, Lambda targets; prefix/suffix filters"},
	)
}
