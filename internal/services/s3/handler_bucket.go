package s3

// handler_bucket.go contains all fully-implemented bucket-level S3 handlers
// and the route tables that map sub-resource query params to those handlers.
// Stubs (NotImplementedXML) live in handler_stubs.go.
// Dispatchers (BucketGet, BucketPut, …) live in handler.go.

import (
	"encoding/base64"
	"encoding/xml"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/your-org/overcast/internal/protocol"
	"github.com/your-org/overcast/internal/serviceutil"
)

// initBucketRoutes populates the four bucket-level dispatch tables.
// Called once by newHandler.
func (h *Handler) initBucketRoutes() {
	h.bucketGetRoutes = []s3Route{
		{"list-type", h.ListObjects},
		{"location", h.GetBucketLocation},
		{"acl", h.GetBucketAcl},
		{"cors", h.GetBucketCors},
		{"policy", h.GetBucketPolicy},
		{"policyStatus", h.GetBucketPolicyStatus},
		{"lifecycle", h.GetBucketLifecycleConfiguration},
		{"versioning", h.GetBucketVersioning},
		{"notification", h.GetBucketNotificationConfiguration},
		{"tagging", h.GetBucketTagging},
		{"website", h.GetBucketWebsite},
		{"logging", h.GetBucketLogging},
		{"replication", h.GetBucketReplication},
		{"encryption", h.GetBucketEncryption},
		{"accelerate", h.GetBucketAccelerateConfiguration},
		{"requestPayment", h.GetBucketRequestPayment},
		{"ownershipControls", h.GetBucketOwnershipControls},
		{"publicAccessBlock", h.GetPublicAccessBlock},
		{"uploads", h.ListMultipartUploads},
		{"versions", h.ListObjectVersions},
		{"analytics", h.ListBucketAnalyticsConfigurations},
		{"intelligent-tiering", h.ListBucketIntelligentTieringConfigurations},
		{"inventory", h.ListBucketInventoryConfigurations},
		{"metrics", h.ListBucketMetricsConfigurations},
		{"object-lock", h.GetObjectLockConfiguration},
		{"abac", h.GetBucketAbac},
		{"metadata", h.GetBucketMetadataConfiguration},
		{"metadataTable", h.GetBucketMetadataTableConfiguration},
		{"session", h.CreateSession},
	}

	h.bucketPutRoutes = []s3Route{
		{"acl", h.PutBucketAcl},
		{"cors", h.PutBucketCors},
		{"policy", h.PutBucketPolicy},
		{"lifecycle", h.PutBucketLifecycleConfiguration},
		{"versioning", h.PutBucketVersioning},
		{"notification", h.PutBucketNotificationConfiguration},
		{"tagging", h.PutBucketTagging},
		{"website", h.PutBucketWebsite},
		{"logging", h.PutBucketLogging},
		{"replication", h.PutBucketReplication},
		{"encryption", h.PutBucketEncryption},
		{"accelerate", h.PutBucketAccelerateConfiguration},
		{"requestPayment", h.PutBucketRequestPayment},
		{"ownershipControls", h.PutBucketOwnershipControls},
		{"publicAccessBlock", h.PutPublicAccessBlock},
		{"analytics", h.PutBucketAnalyticsConfiguration},
		{"intelligent-tiering", h.PutBucketIntelligentTieringConfiguration},
		{"inventory", h.PutBucketInventoryConfiguration},
		{"metrics", h.PutBucketMetricsConfiguration},
		{"object-lock", h.PutObjectLockConfiguration},
		{"abac", h.PutBucketAbac},
		{"metadata", h.CreateBucketMetadataConfiguration},
		{"metadataTable", h.UpdateBucketMetadataTableConfiguration},
	}

	h.bucketDeleteRoutes = []s3Route{
		{"cors", h.DeleteBucketCors},
		{"policy", h.DeleteBucketPolicy},
		{"lifecycle", h.DeleteBucketLifecycle},
		{"tagging", h.DeleteBucketTagging},
		{"website", h.DeleteBucketWebsite},
		{"replication", h.DeleteBucketReplication},
		{"encryption", h.DeleteBucketEncryption},
		{"analytics", h.DeleteBucketAnalyticsConfiguration},
		{"intelligent-tiering", h.DeleteBucketIntelligentTieringConfiguration},
		{"inventory", h.DeleteBucketInventoryConfiguration},
		{"metrics", h.DeleteBucketMetricsConfiguration},
		{"ownershipControls", h.DeleteBucketOwnershipControls},
		{"publicAccessBlock", h.DeletePublicAccessBlock},
		{"metadata", h.DeleteBucketMetadataConfiguration},
		{"metadataTable", h.DeleteBucketMetadataTableConfiguration},
	}

	h.bucketPostRoutes = []s3Route{
		{"delete", h.DeleteObjects},
		{"metadataTable", h.CreateBucketMetadataTableConfiguration},
	}
}

// CreateBucket handles PUT /{bucket}
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_CreateBucket.html
func (h *Handler) CreateBucket(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")

	if aerr := serviceutil.BucketName(bucket); aerr != nil {
		// serviceutil returns "InvalidBucketName"; S3 historically uses
		// "InvalidArgument" in some validation paths. Preserve the code
		// expected by existing tests.
		protocol.WriteXMLError(w, r, protocol.ErrInvalidArgument(aerr.Message))
		return
	}

	exists, aerr := h.store.bucketExists(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	if exists {
		protocol.WriteXMLError(w, r, errBucketAlreadyExists(bucket))
		return
	}

	b := &Bucket{
		Name:         bucket,
		Region:       h.cfg.Region,
		CreationDate: h.clk.Now().UTC(),
	}
	if aerr := h.store.putBucket(r.Context(), b); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	w.Header().Set("Location", "/"+bucket)
	protocol.WriteEmpty(w, r, http.StatusOK)
}

// HeadBucket handles HEAD /{bucket}
// Returns 200 if the bucket exists, 404 if not.
func (h *Handler) HeadBucket(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")

	exists, aerr := h.store.bucketExists(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	if !exists {
		protocol.WriteXMLError(w, r, errNoSuchBucket(bucket))
		return
	}

	protocol.WriteEmpty(w, r, http.StatusOK)
}

// DeleteBucket handles DELETE /{bucket}
// AWS requires the bucket to be empty before deletion.
func (h *Handler) DeleteBucket(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")

	exists, aerr := h.store.bucketExists(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	if !exists {
		protocol.WriteXMLError(w, r, errNoSuchBucket(bucket))
		return
	}

	// Enforce AWS behaviour: bucket must be empty.
	objects, aerr := h.store.listObjects(r.Context(), bucket, "")
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	if len(objects) > 0 {
		protocol.WriteXMLError(w, r, errBucketNotEmpty(bucket))
		return
	}

	if aerr := h.store.deleteBucket(r.Context(), bucket); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	protocol.WriteEmpty(w, r, http.StatusNoContent)
}

// ListBuckets handles GET / — list all buckets owned by the account.
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListBuckets.html
func (h *Handler) ListBuckets(w http.ResponseWriter, r *http.Request) {
	buckets, aerr := h.store.listBuckets(r.Context())
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	type bucketEntry struct {
		Name         string    `xml:"Name"`
		CreationDate time.Time `xml:"CreationDate"`
	}
	type listBucketsResponse struct {
		XMLName xml.Name `xml:"ListAllMyBucketsResult"`
		Xmlns   string   `xml:"xmlns,attr"`
		Owner   struct {
			ID          string `xml:"ID"`
			DisplayName string `xml:"DisplayName"`
		} `xml:"Owner"`
		Buckets struct {
			Bucket []bucketEntry `xml:"Bucket"`
		} `xml:"Buckets"`
	}

	resp := listBucketsResponse{
		Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/",
	}
	resp.Owner.ID = h.cfg.AccountID
	resp.Owner.DisplayName = "overcast"
	resp.Buckets.Bucket = make([]bucketEntry, len(buckets))
	for i, b := range buckets {
		resp.Buckets.Bucket[i] = bucketEntry{Name: b.Name, CreationDate: b.CreationDate}
	}
	protocol.WriteXML(w, r, http.StatusOK, resp)
}

// listObjectsV2Response is the XML envelope for ListObjectsV2.
type listObjectsV2Response struct {
	XMLName               xml.Name        `xml:"ListBucketResult"`
	Xmlns                 string          `xml:"xmlns,attr"`
	Name                  string          `xml:"Name"`
	Prefix                string          `xml:"Prefix"`
	Delimiter             string          `xml:"Delimiter,omitempty"`
	KeyCount              int             `xml:"KeyCount"`
	MaxKeys               int             `xml:"MaxKeys"`
	IsTruncated           bool            `xml:"IsTruncated"`
	ContinuationToken     string          `xml:"ContinuationToken,omitempty"`
	NextContinuationToken string          `xml:"NextContinuationToken,omitempty"`
	Contents              []objectSummary `xml:"Contents"`
	CommonPrefixes        []commonPrefix  `xml:"CommonPrefixes"`
}

type objectSummary struct {
	Key          string    `xml:"Key"`
	LastModified time.Time `xml:"LastModified"`
	ETag         string    `xml:"ETag"`
	Size         int64     `xml:"Size"`
	StorageClass string    `xml:"StorageClass"`
}

type commonPrefix struct {
	Prefix string `xml:"Prefix"`
}

// ListObjectsV2 handles GET /{bucket}?list-type=2.
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListObjectsV2.html
func (h *Handler) ListObjectsV2(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	prefix := serviceutil.QueryString(r, "prefix", "")
	delimiter := serviceutil.QueryString(r, "delimiter", "")
	maxKeys := serviceutil.QueryInt(r, "max-keys", 1000)
	contToken := serviceutil.QueryString(r, "continuation-token", "")

	// Decode the opaque continuation token to a "start-after" key.
	// The token is base64(lastEffectiveKeyOnPreviousPage).
	var startAfter string
	if contToken != "" {
		if decoded, err := base64.StdEncoding.DecodeString(contToken); err == nil {
			startAfter = string(decoded)
		}
	}

	exists, aerr := h.store.bucketExists(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	if !exists {
		protocol.WriteXMLError(w, r, errNoSuchBucket(bucket))
		return
	}

	allObjects, aerr := h.store.listObjects(r.Context(), bucket, prefix)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	// Sort by key for deterministic lexicographic pagination.
	sort.Slice(allObjects, func(i, j int) bool { return allObjects[i].Key < allObjects[j].Key })

	// Build a deduplicated, paginated entry list.
	// Each entry has an "effective key":
	//   - for leaf objects: the object key itself
	//   - for collapsed common prefixes: the prefix string (e.g. "photos/")
	// Entries with effectiveKey <= startAfter are skipped (already returned on a
	// previous page). Because all objects inside a common prefix have actual keys
	// that are lexicographically greater than the prefix string (e.g. "photos/"
	// < "photos/img.jpg"), they are all correctly skipped by the same comparison.
	type pageEntry struct {
		isPrefix bool
		key      string // effective key
		obj      *Object
	}

	var entries []pageEntry
	seenPrefixes := map[string]bool{}

	for _, obj := range allObjects {
		effectiveKey := obj.Key
		isPrefix := false
		cpKey := ""

		if delimiter != "" {
			remainder := obj.Key[len(prefix):]
			if idx := strings.Index(remainder, delimiter); idx >= 0 {
				cpKey = prefix + remainder[:idx+len(delimiter)]
				effectiveKey = cpKey
				isPrefix = true
			}
		}

		// Skip entries from previous pages.
		if startAfter != "" && effectiveKey <= startAfter {
			continue
		}

		if isPrefix {
			if !seenPrefixes[cpKey] {
				seenPrefixes[cpKey] = true
				entries = append(entries, pageEntry{isPrefix: true, key: cpKey})
			}
		} else {
			entries = append(entries, pageEntry{key: obj.Key, obj: obj})
		}

		// Collect one extra entry to detect truncation without scanning everything.
		if len(entries) > maxKeys {
			break
		}
	}

	truncated := len(entries) > maxKeys
	if truncated {
		entries = entries[:maxKeys]
	}

	var nextToken string
	if truncated {
		nextToken = base64.StdEncoding.EncodeToString([]byte(entries[len(entries)-1].key))
	}

	// Build typed response slices.
	var contents []objectSummary
	var commonPrefixes []commonPrefix
	for _, e := range entries {
		if e.isPrefix {
			commonPrefixes = append(commonPrefixes, commonPrefix{Prefix: e.key})
		} else {
			contents = append(contents, objectSummary{
				Key:          e.obj.Key,
				LastModified: e.obj.LastModified,
				ETag:         e.obj.ETag,
				Size:         e.obj.ContentLength,
				StorageClass: "STANDARD",
			})
		}
	}

	resp := &listObjectsV2Response{
		Xmlns:                 "http://s3.amazonaws.com/doc/2006-03-01/",
		Name:                  bucket,
		Prefix:                prefix,
		Delimiter:             delimiter,
		KeyCount:              len(entries),
		MaxKeys:               maxKeys,
		IsTruncated:           truncated,
		ContinuationToken:     contToken,
		NextContinuationToken: nextToken,
		Contents:              contents,
		CommonPrefixes:        commonPrefixes,
	}

	protocol.WriteXML(w, r, http.StatusOK, resp)
}

// getBucketLocationResponse is the XML for GetBucketLocation.
type getBucketLocationResponse struct {
	XMLName            xml.Name `xml:"LocationConstraint"`
	Xmlns              string   `xml:"xmlns,attr"`
	LocationConstraint string   `xml:",chardata"`
}

// GetBucketLocation handles GET /{bucket}?location.
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketLocation.html
func (h *Handler) GetBucketLocation(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")

	b, aerr := h.store.getBucket(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	resp := &getBucketLocationResponse{
		Xmlns:              "http://s3.amazonaws.com/doc/2006-03-01/",
		LocationConstraint: b.Region,
	}
	protocol.WriteXML(w, r, http.StatusOK, resp)
}
