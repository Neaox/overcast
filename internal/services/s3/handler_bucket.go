package s3

// handler_bucket.go contains all fully-implemented bucket-level S3 handlers
// and the route tables that map sub-resource query params to those handlers.
// Stubs (NotImplementedXML) live in handler_stubs.go.
// Dispatchers (BucketGet, BucketPut, …) live in handler.go.

import (
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// initBucketRoutes populates the four bucket-level dispatch tables.
// Called once by newHandler.
func (h *Handler) initBucketRoutes() {
	h.bucketGetRoutes = []s3Route{
		{"list-type", h.listTypeDispatch},
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
		Region:       middleware.RegionFromContext(r.Context(), h.cfg.Region),
		CreationDate: h.clk.Now().UTC(),
	}
	if aerr := h.store.putBucket(r.Context(), b); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	w.Header().Set("Location", "/"+bucket)
	if h.bus != nil {
		h.bus.Publish(r.Context(), events.Event{
			Type:    events.S3BucketCreated,
			Time:    h.clk.Now(),
			Source:  "s3",
			Payload: events.ResourcePayload{Name: bucket},
		})
	}
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

	if h.bus != nil {
		h.bus.Publish(r.Context(), events.Event{
			Type:    events.S3BucketDeleted,
			Time:    h.clk.Now(),
			Source:  "s3",
			Payload: events.ResourcePayload{Name: bucket},
		})
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

// listObjectsV1Response is the XML envelope for ListObjects (v1).
// Differs from v2: uses Marker/NextMarker, no KeyCount, no ContinuationToken.
type listObjectsV1Response struct {
	XMLName        xml.Name        `xml:"ListBucketResult"`
	Xmlns          string          `xml:"xmlns,attr"`
	Name           string          `xml:"Name"`
	Prefix         string          `xml:"Prefix"`
	Delimiter      string          `xml:"Delimiter,omitempty"`
	Marker         string          `xml:"Marker"`
	NextMarker     string          `xml:"NextMarker,omitempty"`
	MaxKeys        int             `xml:"MaxKeys"`
	IsTruncated    bool            `xml:"IsTruncated"`
	Contents       []objectSummary `xml:"Contents"`
	CommonPrefixes []commonPrefix  `xml:"CommonPrefixes"`
}

// ListObjectsV1 handles GET /{bucket} (no list-type) and GET /{bucket}?list-type=1.
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListObjects.html
func (h *Handler) ListObjectsV1(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	prefix := serviceutil.QueryString(r, "prefix", "")
	delimiter := serviceutil.QueryString(r, "delimiter", "")
	maxKeys := serviceutil.QueryInt(r, "max-keys", 1000)
	marker := serviceutil.QueryString(r, "marker", "")

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

	sort.Slice(allObjects, func(i, j int) bool { return allObjects[i].Key < allObjects[j].Key })

	type pageEntry struct {
		isPrefix bool
		key      string
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

		// Skip entries at or before the marker (already returned on previous page).
		if marker != "" && effectiveKey <= marker {
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

		if len(entries) > maxKeys {
			break
		}
	}

	truncated := len(entries) > maxKeys
	if truncated {
		entries = entries[:maxKeys]
	}

	var nextMarker string
	if truncated {
		nextMarker = entries[len(entries)-1].key
	}

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

	resp := &listObjectsV1Response{
		Xmlns:          "http://s3.amazonaws.com/doc/2006-03-01/",
		Name:           bucket,
		Prefix:         prefix,
		Delimiter:      delimiter,
		Marker:         marker,
		NextMarker:     nextMarker,
		MaxKeys:        maxKeys,
		IsTruncated:    truncated,
		Contents:       contents,
		CommonPrefixes: commonPrefixes,
	}

	protocol.WriteXML(w, r, http.StatusOK, resp)
}

// listTypeDispatch routes list-type=2 to ListObjectsV2 and all other values
// (including blank/1) to ListObjectsV1.
func (h *Handler) listTypeDispatch(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("list-type") == "2" {
		h.ListObjectsV2(w, r)
	} else {
		h.ListObjectsV1(w, r)
	}
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
	StartAfter            string          `xml:"StartAfter,omitempty"`
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
	requestedStartAfter := serviceutil.QueryString(r, "start-after", "")
	startAfter := requestedStartAfter

	// Decode the opaque continuation token to a "start-after" key.
	// The token is base64(lastEffectiveKeyOnPreviousPage).
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
		StartAfter:            requestedStartAfter,
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

// ---- Versioning ------------------------------------------------------------

type versioningConfigurationXML struct {
	XMLName xml.Name `xml:"VersioningConfiguration"`
	Xmlns   string   `xml:"xmlns,attr,omitempty"`
	Status  string   `xml:"Status,omitempty"`
}

// GetBucketVersioning handles GET /{bucket}?versioning.
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketVersioning.html
func (h *Handler) GetBucketVersioning(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	b, aerr := h.store.getBucket(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	resp := &versioningConfigurationXML{
		Xmlns:  "http://s3.amazonaws.com/doc/2006-03-01/",
		Status: b.VersioningStatus,
	}
	protocol.WriteXML(w, r, http.StatusOK, resp)
}

// listVersionsResult is the XML envelope for ListObjectVersions.
type listVersionsResult struct {
	XMLName             xml.Name       `xml:"ListVersionsResult"`
	Xmlns               string         `xml:"xmlns,attr"`
	Name                string         `xml:"Name"`
	Prefix              string         `xml:"Prefix"`
	KeyMarker           string         `xml:"KeyMarker"`
	VersionIdMarker     string         `xml:"VersionIdMarker"`
	NextKeyMarker       string         `xml:"NextKeyMarker,omitempty"`
	NextVersionIdMarker string         `xml:"NextVersionIdMarker,omitempty"`
	MaxKeys             int            `xml:"MaxKeys"`
	IsTruncated         bool           `xml:"IsTruncated"`
	Versions            []versionEntry `xml:"Version"`
}

type versionEntry struct {
	Key          string    `xml:"Key"`
	VersionId    string    `xml:"VersionId"`
	IsLatest     bool      `xml:"IsLatest"`
	LastModified time.Time `xml:"LastModified"`
	ETag         string    `xml:"ETag"`
	Size         int64     `xml:"Size"`
	StorageClass string    `xml:"StorageClass"`
}

// ListObjectVersions handles GET /{bucket}?versions.
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListObjectVersions.html
//
// The store does not support true multi-versioning: each key has exactly one
// live object stored. So every object is returned as a Version entry with
// VersionId="null" and IsLatest=true. This satisfies the AWS SDK cleanup
// pattern (ListObjectVersions → DeleteObjects) without requiring a full
// versioning store redesign.
func (h *Handler) ListObjectVersions(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	prefix := serviceutil.QueryString(r, "prefix", "")
	keyMarker := serviceutil.QueryString(r, "key-marker", "")
	maxKeys := serviceutil.QueryInt(r, "max-keys", 1000)

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

	// Sort lexicographically for deterministic pagination.
	sort.Slice(allObjects, func(i, j int) bool { return allObjects[i].Key < allObjects[j].Key })

	// Apply key-marker: skip all keys <= keyMarker.
	if keyMarker != "" {
		i := 0
		for i < len(allObjects) && allObjects[i].Key <= keyMarker {
			i++
		}
		allObjects = allObjects[i:]
	}

	// Collect up to maxKeys+1 entries to detect truncation.
	cap := maxKeys + 1
	if len(allObjects) < cap {
		cap = len(allObjects)
	}
	entries := allObjects[:cap]
	truncated := len(entries) > maxKeys
	if truncated {
		entries = entries[:maxKeys]
	}

	versions := make([]versionEntry, len(entries))
	for i, obj := range entries {
		versions[i] = versionEntry{
			Key:          obj.Key,
			VersionId:    "null",
			IsLatest:     true,
			LastModified: obj.LastModified,
			ETag:         obj.ETag,
			Size:         obj.ContentLength,
			StorageClass: "STANDARD",
		}
	}

	var nextKeyMarker string
	if truncated && len(versions) > 0 {
		nextKeyMarker = versions[len(versions)-1].Key
	}

	resp := &listVersionsResult{
		Xmlns:         "http://s3.amazonaws.com/doc/2006-03-01/",
		Name:          bucket,
		Prefix:        prefix,
		KeyMarker:     keyMarker,
		NextKeyMarker: nextKeyMarker,
		MaxKeys:       maxKeys,
		IsTruncated:   truncated,
		Versions:      versions,
	}
	protocol.WriteXML(w, r, http.StatusOK, resp)
}

// PutBucketVersioning handles PUT /{bucket}?versioning.
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketVersioning.html
func (h *Handler) PutBucketVersioning(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	var req versioningConfigurationXML
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInvalidArgument("malformed XML"))
		return
	}
	b, aerr := h.store.getBucket(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	b.VersioningStatus = req.Status
	if aerr := h.store.putBucket(r.Context(), b); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	protocol.WriteEmpty(w, r, http.StatusOK)
}

// ─── Website configuration ─────────────────────────────────────────────────────

type websiteConfigurationXML struct {
	XMLName       xml.Name          `xml:"WebsiteConfiguration"`
	IndexDocument *indexDocumentXML `xml:"IndexDocument"`
	ErrorDocument *errorDocumentXML `xml:"ErrorDocument"`
}

type indexDocumentXML struct {
	Suffix string `xml:"Suffix"`
}

type errorDocumentXML struct {
	Key string `xml:"Key"`
}

// putBucketWebsite handles PUT /{bucket}?website.
func (h *Handler) putBucketWebsite(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	var req websiteConfigurationXML
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInvalidArgument("malformed XML"))
		return
	}
	b, aerr := h.store.getBucket(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	wc := &WebsiteConfiguration{}
	if req.IndexDocument != nil {
		wc.IndexDocument = req.IndexDocument.Suffix
	}
	if req.ErrorDocument != nil {
		wc.ErrorDocument = req.ErrorDocument.Key
	}
	b.WebsiteConfig = wc
	if aerr := h.store.putBucket(r.Context(), b); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	protocol.WriteEmpty(w, r, http.StatusOK)
}

// getBucketWebsite handles GET /{bucket}?website.
func (h *Handler) getBucketWebsite(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	b, aerr := h.store.getBucket(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	if b.WebsiteConfig == nil {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "NoSuchWebsiteConfiguration",
			Message:    "The specified bucket does not have a website configuration",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	type response struct {
		XMLName       xml.Name          `xml:"WebsiteConfiguration"`
		IndexDocument *indexDocumentXML `xml:"IndexDocument,omitempty"`
		ErrorDocument *errorDocumentXML `xml:"ErrorDocument,omitempty"`
	}
	resp := response{}
	if b.WebsiteConfig.IndexDocument != "" {
		resp.IndexDocument = &indexDocumentXML{Suffix: b.WebsiteConfig.IndexDocument}
	}
	if b.WebsiteConfig.ErrorDocument != "" {
		resp.ErrorDocument = &errorDocumentXML{Key: b.WebsiteConfig.ErrorDocument}
	}
	protocol.WriteXML(w, r, http.StatusOK, &resp)
}

// ─── CORS configuration ────────────────────────────────────────────────────────

type corsConfigurationXML struct {
	XMLName   xml.Name      `xml:"CORSConfiguration"`
	CORSRules []corsRuleXML `xml:"CORSRule"`
}

type corsRuleXML struct {
	AllowedHeader []string `xml:"AllowedHeader"`
	AllowedMethod []string `xml:"AllowedMethod"`
	AllowedOrigin []string `xml:"AllowedOrigin"`
	ExposeHeader  []string `xml:"ExposeHeader"`
	MaxAgeSeconds int      `xml:"MaxAgeSeconds,omitempty"`
}

// putBucketCors handles PUT /{bucket}?cors.
func (h *Handler) putBucketCors(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	var req corsConfigurationXML
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInvalidArgument("malformed XML"))
		return
	}
	b, aerr := h.store.getBucket(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	rules := make([]CORSRule, 0, len(req.CORSRules))
	for _, rx := range req.CORSRules {
		rules = append(rules, CORSRule{
			AllowedHeaders: rx.AllowedHeader,
			AllowedMethods: rx.AllowedMethod,
			AllowedOrigins: rx.AllowedOrigin,
			ExposeHeaders:  rx.ExposeHeader,
			MaxAgeSeconds:  rx.MaxAgeSeconds,
		})
	}
	b.CORSRules = rules
	if aerr := h.store.putBucket(r.Context(), b); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	protocol.WriteEmpty(w, r, http.StatusOK)
}

// getBucketCors handles GET /{bucket}?cors.
func (h *Handler) getBucketCors(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	b, aerr := h.store.getBucket(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	if len(b.CORSRules) == 0 {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "NoSuchCORSConfiguration",
			Message:    "The CORS configuration does not exist",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	xmlRules := make([]corsRuleXML, 0, len(b.CORSRules))
	for _, rule := range b.CORSRules {
		xmlRules = append(xmlRules, corsRuleXML{
			AllowedHeader: rule.AllowedHeaders,
			AllowedMethod: rule.AllowedMethods,
			AllowedOrigin: rule.AllowedOrigins,
			ExposeHeader:  rule.ExposeHeaders,
			MaxAgeSeconds: rule.MaxAgeSeconds,
		})
	}
	protocol.WriteXML(w, r, http.StatusOK, &corsConfigurationXML{CORSRules: xmlRules})
}

// PutBucketPolicy handles PUT /{bucket}?policy
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketPolicy.html
func (h *Handler) PutBucketPolicy(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	body, err := io.ReadAll(io.LimitReader(r.Body, 20*1024+1))
	if err != nil || len(body) == 0 {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "MalformedPolicy",
			Message:    "Missing or invalid policy document",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	if len(body) > 20*1024 {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "MalformedPolicy",
			Message:    "Policy document must not exceed 20 KB",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	if !json.Valid(body) {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "MalformedPolicy",
			Message:    "Policy is not valid JSON",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	b, aerr := h.store.getBucket(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	b.Policy = string(body)
	if aerr := h.store.putBucket(r.Context(), b); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	protocol.WriteEmpty(w, r, http.StatusOK)
}

// GetBucketPolicy handles GET /{bucket}?policy
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketPolicy.html
func (h *Handler) GetBucketPolicy(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	b, aerr := h.store.getBucket(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	if b.Policy == "" {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "NoSuchBucketPolicy",
			Message:    "The bucket policy does not exist",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("x-amz-request-id", protocol.RequestIDFromContext(r.Context()))
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(b.Policy)) //nolint:errcheck
}

// GetBucketPolicyStatus handles GET /{bucket}?policyStatus
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketPolicyStatus.html
func (h *Handler) GetBucketPolicyStatus(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedXML(w, r)
}

// DeleteBucketPolicy handles DELETE /{bucket}?policy
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteBucketPolicy.html
func (h *Handler) DeleteBucketPolicy(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	b, aerr := h.store.getBucket(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	b.Policy = ""
	if aerr := h.store.putBucket(r.Context(), b); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	protocol.WriteEmpty(w, r, http.StatusNoContent)
}
