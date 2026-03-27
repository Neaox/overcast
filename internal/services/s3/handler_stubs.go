package s3

// handler_stubs.go contains every S3 handler that is not yet implemented.
// Each method returns HTTP 501 Not Implemented with x-emulator-unsupported: true.
//
// Convention: when an operation is implemented, move its method body out of this
// file and into handler.go (or handler_<group>.go for large feature groups).
// handler.go is the authoritative inventory of what actually works.

import (
	"net/http"

	"github.com/your-org/overcast/internal/protocol"
)

// ---- Bucket GET stubs ------------------------------------------------------

// GetBucketAcl handles GET /{bucket}?acl
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketAcl.html
func (h *Handler) GetBucketAcl(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// GetBucketCors handles GET /{bucket}?cors
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketCors.html
func (h *Handler) GetBucketCors(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// GetBucketPolicy handles GET /{bucket}?policy
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketPolicy.html
func (h *Handler) GetBucketPolicy(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// GetBucketPolicyStatus handles GET /{bucket}?policyStatus
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketPolicyStatus.html
func (h *Handler) GetBucketPolicyStatus(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// GetBucketLifecycleConfiguration handles GET /{bucket}?lifecycle
// Covers both GetBucketLifecycle (deprecated) and GetBucketLifecycleConfiguration.
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketLifecycleConfiguration.html
func (h *Handler) GetBucketLifecycleConfiguration(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// GetBucketVersioning handles GET /{bucket}?versioning
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketVersioning.html
func (h *Handler) GetBucketVersioning(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// GetBucketTagging handles GET /{bucket}?tagging
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketTagging.html
func (h *Handler) GetBucketTagging(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// GetBucketWebsite handles GET /{bucket}?website
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketWebsite.html
func (h *Handler) GetBucketWebsite(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// GetBucketLogging handles GET /{bucket}?logging
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketLogging.html
func (h *Handler) GetBucketLogging(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// GetBucketReplication handles GET /{bucket}?replication
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketReplication.html
func (h *Handler) GetBucketReplication(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// GetBucketEncryption handles GET /{bucket}?encryption
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketEncryption.html
func (h *Handler) GetBucketEncryption(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// GetBucketAccelerateConfiguration handles GET /{bucket}?accelerate
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketAccelerateConfiguration.html
func (h *Handler) GetBucketAccelerateConfiguration(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// GetBucketRequestPayment handles GET /{bucket}?requestPayment
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketRequestPayment.html
func (h *Handler) GetBucketRequestPayment(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// GetBucketOwnershipControls handles GET /{bucket}?ownershipControls
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketOwnershipControls.html
func (h *Handler) GetBucketOwnershipControls(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// GetPublicAccessBlock handles GET /{bucket}?publicAccessBlock
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetPublicAccessBlock.html
func (h *Handler) GetPublicAccessBlock(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// ListMultipartUploads handles GET /{bucket}?uploads
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListMultipartUploads.html
func (h *Handler) ListMultipartUploads(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// ListObjectVersions handles GET /{bucket}?versions
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListObjectVersions.html
func (h *Handler) ListObjectVersions(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// ListObjects handles GET /{bucket} (legacy v1 listing, no list-type param).
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListObjects.html
//
// TODO(priority:P3): return v1-format response with Marker instead of ContinuationToken.
func (h *Handler) ListObjects(w http.ResponseWriter, r *http.Request) {
	// Temporary: delegate to ListObjectsV2 — same data, different pagination params.
	h.ListObjectsV2(w, r)
}

// ListBucketAnalyticsConfigurations handles GET /{bucket}?analytics
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListBucketAnalyticsConfigurations.html
func (h *Handler) ListBucketAnalyticsConfigurations(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// ListBucketIntelligentTieringConfigurations handles GET /{bucket}?intelligent-tiering
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListBucketIntelligentTieringConfigurations.html
func (h *Handler) ListBucketIntelligentTieringConfigurations(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// ListBucketInventoryConfigurations handles GET /{bucket}?inventory
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListBucketInventoryConfigurations.html
func (h *Handler) ListBucketInventoryConfigurations(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// ListBucketMetricsConfigurations handles GET /{bucket}?metrics
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListBucketMetricsConfigurations.html
func (h *Handler) ListBucketMetricsConfigurations(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// GetObjectLockConfiguration handles GET /{bucket}?object-lock
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObjectLockConfiguration.html
func (h *Handler) GetObjectLockConfiguration(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// GetBucketAbac handles GET /{bucket}?abac
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketAbac.html
func (h *Handler) GetBucketAbac(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// GetBucketMetadataConfiguration handles GET /{bucket}?metadata
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketMetadataConfiguration.html
func (h *Handler) GetBucketMetadataConfiguration(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// GetBucketMetadataTableConfiguration handles GET /{bucket}?metadataTable
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketMetadataTableConfiguration.html
func (h *Handler) GetBucketMetadataTableConfiguration(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// CreateSession handles GET /{bucket}?session
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_CreateSession.html
func (h *Handler) CreateSession(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// ListDirectoryBuckets handles GET /?directory-buckets
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListDirectoryBuckets.html
func (h *Handler) ListDirectoryBuckets(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// ---- Bucket PUT stubs ------------------------------------------------------

// PutBucketAcl handles PUT /{bucket}?acl
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketAcl.html
func (h *Handler) PutBucketAcl(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// PutBucketCors handles PUT /{bucket}?cors
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketCors.html
func (h *Handler) PutBucketCors(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// PutBucketPolicy handles PUT /{bucket}?policy
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketPolicy.html
func (h *Handler) PutBucketPolicy(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// PutBucketLifecycleConfiguration handles PUT /{bucket}?lifecycle
// Covers both PutBucketLifecycle (deprecated) and PutBucketLifecycleConfiguration.
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketLifecycleConfiguration.html
func (h *Handler) PutBucketLifecycleConfiguration(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// PutBucketVersioning handles PUT /{bucket}?versioning
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketVersioning.html
func (h *Handler) PutBucketVersioning(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// PutBucketTagging handles PUT /{bucket}?tagging
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketTagging.html
func (h *Handler) PutBucketTagging(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// PutBucketWebsite handles PUT /{bucket}?website
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketWebsite.html
func (h *Handler) PutBucketWebsite(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// PutBucketLogging handles PUT /{bucket}?logging
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketLogging.html
func (h *Handler) PutBucketLogging(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// PutBucketReplication handles PUT /{bucket}?replication
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketReplication.html
func (h *Handler) PutBucketReplication(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// PutBucketEncryption handles PUT /{bucket}?encryption
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketEncryption.html
func (h *Handler) PutBucketEncryption(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// PutBucketAccelerateConfiguration handles PUT /{bucket}?accelerate
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketAccelerateConfiguration.html
func (h *Handler) PutBucketAccelerateConfiguration(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// PutBucketRequestPayment handles PUT /{bucket}?requestPayment
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketRequestPayment.html
func (h *Handler) PutBucketRequestPayment(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// PutBucketOwnershipControls handles PUT /{bucket}?ownershipControls
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketOwnershipControls.html
func (h *Handler) PutBucketOwnershipControls(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// PutPublicAccessBlock handles PUT /{bucket}?publicAccessBlock
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutPublicAccessBlock.html
func (h *Handler) PutPublicAccessBlock(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// PutBucketAnalyticsConfiguration handles PUT /{bucket}?analytics
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketAnalyticsConfiguration.html
func (h *Handler) PutBucketAnalyticsConfiguration(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// PutBucketIntelligentTieringConfiguration handles PUT /{bucket}?intelligent-tiering
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketIntelligentTieringConfiguration.html
func (h *Handler) PutBucketIntelligentTieringConfiguration(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// PutBucketInventoryConfiguration handles PUT /{bucket}?inventory
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketInventoryConfiguration.html
func (h *Handler) PutBucketInventoryConfiguration(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// PutBucketMetricsConfiguration handles PUT /{bucket}?metrics
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketMetricsConfiguration.html
func (h *Handler) PutBucketMetricsConfiguration(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// PutObjectLockConfiguration handles PUT /{bucket}?object-lock
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObjectLockConfiguration.html
func (h *Handler) PutObjectLockConfiguration(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// PutBucketAbac handles PUT /{bucket}?abac
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketAbac.html
func (h *Handler) PutBucketAbac(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// CreateBucketMetadataConfiguration handles PUT /{bucket}?metadata
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_CreateBucketMetadataConfiguration.html
func (h *Handler) CreateBucketMetadataConfiguration(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// UpdateBucketMetadataTableConfiguration handles PUT /{bucket}?metadataTable
// Covers UpdateBucketMetadataInventoryTableConfiguration and UpdateBucketMetadataJournalTableConfiguration.
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_UpdateBucketMetadataInventoryTableConfiguration.html
func (h *Handler) UpdateBucketMetadataTableConfiguration(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// ---- Bucket DELETE stubs ---------------------------------------------------

// DeleteBucketCors handles DELETE /{bucket}?cors
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteBucketCors.html
func (h *Handler) DeleteBucketCors(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// DeleteBucketPolicy handles DELETE /{bucket}?policy
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteBucketPolicy.html
func (h *Handler) DeleteBucketPolicy(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// DeleteBucketLifecycle handles DELETE /{bucket}?lifecycle
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteBucketLifecycle.html
func (h *Handler) DeleteBucketLifecycle(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// DeleteBucketTagging handles DELETE /{bucket}?tagging
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteBucketTagging.html
func (h *Handler) DeleteBucketTagging(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// DeleteBucketWebsite handles DELETE /{bucket}?website
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteBucketWebsite.html
func (h *Handler) DeleteBucketWebsite(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// DeleteBucketReplication handles DELETE /{bucket}?replication
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteBucketReplication.html
func (h *Handler) DeleteBucketReplication(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// DeleteBucketEncryption handles DELETE /{bucket}?encryption
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteBucketEncryption.html
func (h *Handler) DeleteBucketEncryption(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// DeleteBucketAnalyticsConfiguration handles DELETE /{bucket}?analytics
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteBucketAnalyticsConfiguration.html
func (h *Handler) DeleteBucketAnalyticsConfiguration(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// DeleteBucketIntelligentTieringConfiguration handles DELETE /{bucket}?intelligent-tiering
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteBucketIntelligentTieringConfiguration.html
func (h *Handler) DeleteBucketIntelligentTieringConfiguration(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// DeleteBucketInventoryConfiguration handles DELETE /{bucket}?inventory
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteBucketInventoryConfiguration.html
func (h *Handler) DeleteBucketInventoryConfiguration(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// DeleteBucketMetricsConfiguration handles DELETE /{bucket}?metrics
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteBucketMetricsConfiguration.html
func (h *Handler) DeleteBucketMetricsConfiguration(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// DeleteBucketOwnershipControls handles DELETE /{bucket}?ownershipControls
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteBucketOwnershipControls.html
func (h *Handler) DeleteBucketOwnershipControls(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// DeletePublicAccessBlock handles DELETE /{bucket}?publicAccessBlock
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeletePublicAccessBlock.html
func (h *Handler) DeletePublicAccessBlock(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// DeleteBucketMetadataConfiguration handles DELETE /{bucket}?metadata
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteBucketMetadataConfiguration.html
func (h *Handler) DeleteBucketMetadataConfiguration(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// DeleteBucketMetadataTableConfiguration handles DELETE /{bucket}?metadataTable
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteBucketMetadataTableConfiguration.html
func (h *Handler) DeleteBucketMetadataTableConfiguration(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// ---- Bucket POST stubs -----------------------------------------------------

// CreateBucketMetadataTableConfiguration handles POST /{bucket}?metadataTable
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_CreateBucketMetadataTableConfiguration.html
func (h *Handler) CreateBucketMetadataTableConfiguration(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// ---- Object GET stubs ------------------------------------------------------

// GetObjectAcl handles GET /{bucket}/{key}?acl
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObjectAcl.html
func (h *Handler) GetObjectAcl(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// GetObjectTagging handles GET /{bucket}/{key}?tagging
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObjectTagging.html
func (h *Handler) GetObjectTagging(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// GetObjectAttributes handles GET /{bucket}/{key}?attributes
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObjectAttributes.html
func (h *Handler) GetObjectAttributes(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// GetObjectLegalHold handles GET /{bucket}/{key}?legal-hold
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObjectLegalHold.html
func (h *Handler) GetObjectLegalHold(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// GetObjectRetention handles GET /{bucket}/{key}?retention
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObjectRetention.html
func (h *Handler) GetObjectRetention(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// GetObjectTorrent handles GET /{bucket}/{key}?torrent
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObjectTorrent.html
func (h *Handler) GetObjectTorrent(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// ListParts handles GET /{bucket}/{key}?uploadId=xxx
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListParts.html
func (h *Handler) ListParts(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// ---- Object PUT stubs ------------------------------------------------------

// PutObjectAcl handles PUT /{bucket}/{key}?acl
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObjectAcl.html
func (h *Handler) PutObjectAcl(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// PutObjectTagging handles PUT /{bucket}/{key}?tagging
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObjectTagging.html
func (h *Handler) PutObjectTagging(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// PutObjectLegalHold handles PUT /{bucket}/{key}?legal-hold
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObjectLegalHold.html
func (h *Handler) PutObjectLegalHold(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// PutObjectRetention handles PUT /{bucket}/{key}?retention
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObjectRetention.html
func (h *Handler) PutObjectRetention(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// RenameObject handles PUT /{bucket}/{key}?rename
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_RenameObject.html
func (h *Handler) RenameObject(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// UpdateObjectEncryption handles PUT /{bucket}/{key}?encryption
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_UpdateObjectEncryption.html
func (h *Handler) UpdateObjectEncryption(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// UploadPart handles PUT /{bucket}/{key}?partNumber=N&uploadId=xxx
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_UploadPart.html
func (h *Handler) UploadPart(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// UploadPartCopy handles PUT /{bucket}/{key}?partNumber=N with x-amz-copy-source header.
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_UploadPartCopy.html
func (h *Handler) UploadPartCopy(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// ---- Object DELETE stubs ---------------------------------------------------

// DeleteObjectTagging handles DELETE /{bucket}/{key}?tagging
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteObjectTagging.html
func (h *Handler) DeleteObjectTagging(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// AbortMultipartUpload handles DELETE /{bucket}/{key}?uploadId=xxx
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_AbortMultipartUpload.html
func (h *Handler) AbortMultipartUpload(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// ---- Object POST stubs -----------------------------------------------------

// CreateMultipartUpload handles POST /{bucket}/{key}?uploads
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_CreateMultipartUpload.html
func (h *Handler) CreateMultipartUpload(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// CompleteMultipartUpload handles POST /{bucket}/{key}?uploadId=xxx
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_CompleteMultipartUpload.html
func (h *Handler) CompleteMultipartUpload(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// RestoreObject handles POST /{bucket}/{key}?restore
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_RestoreObject.html
func (h *Handler) RestoreObject(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// SelectObjectContent handles POST /{bucket}/{key}?select&select-type=2
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_SelectObjectContent.html
func (h *Handler) SelectObjectContent(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// WriteGetObjectResponse handles POST /{bucket}/{key}?writeGetObjectResponse
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_WriteGetObjectResponse.html
func (h *Handler) WriteGetObjectResponse(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}
