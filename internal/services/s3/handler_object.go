package s3

// handler_object.go contains all fully-implemented object-level S3 handlers
// and the route tables that map sub-resource query params to those handlers.
// Stubs (NotImplementedXML) live in handler_stubs.go.
// Dispatchers (ObjectGet, ObjectDelete, …, PutObjectOrCopy) live in handler.go.

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/your-org/overcast/internal/events"
	"github.com/your-org/overcast/internal/protocol"
	"github.com/your-org/overcast/internal/serviceutil"
)

// initObjectRoutes populates the four object-level dispatch tables.
// Called once by newHandler.
func (h *Handler) initObjectRoutes() {
	h.objectGetRoutes = []s3Route{
		{"acl", h.GetObjectAcl},
		{"tagging", h.GetObjectTagging},
		{"attributes", h.GetObjectAttributes},
		{"legal-hold", h.GetObjectLegalHold},
		{"retention", h.GetObjectRetention},
		{"torrent", h.GetObjectTorrent},
		{"uploadId", h.ListParts},
	}

	h.objectPutRoutes = []s3Route{
		{"acl", h.PutObjectAcl},
		{"tagging", h.PutObjectTagging},
		{"legal-hold", h.PutObjectLegalHold},
		{"retention", h.PutObjectRetention},
		{"rename", h.RenameObject},
		{"encryption", h.UpdateObjectEncryption},
	}

	h.objectDeleteRoutes = []s3Route{
		{"tagging", h.DeleteObjectTagging},
		{"uploadId", h.AbortMultipartUpload},
	}

	h.objectPostRoutes = []s3Route{
		{"uploads", h.CreateMultipartUpload},
		{"uploadId", h.CompleteMultipartUpload},
		{"restore", h.RestoreObject},
		{"select", h.SelectObjectContent},
		{"writeGetObjectResponse", h.WriteGetObjectResponse},
	}
}

// PutObject handles PUT /{bucket}/{key}.
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObject.html
func (h *Handler) PutObject(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	key := objectKey(r)

	exists, aerr := h.store.bucketExists(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	if !exists {
		protocol.WriteXMLError(w, r, errNoSuchBucket(bucket))
		return
	}

	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// Extract x-amz-meta-* headers into the metadata map.
	meta := serviceutil.HeaderPrefix(r, "X-Amz-Meta-")

	obj := &Object{
		Bucket:             bucket,
		Key:                key,
		ContentType:        contentType,
		LastModified:       h.clk.Now().UTC(),
		Metadata:           meta,
		ContentDisposition: r.Header.Get("Content-Disposition"),
		ContentEncoding:    r.Header.Get("Content-Encoding"),
		ContentLanguage:    r.Header.Get("Content-Language"),
		CacheControl:       r.Header.Get("Cache-Control"),
		Expires:            r.Header.Get("Expires"),
	}

	// Stream the body to disk while computing the MD5 ETag in one pass.
	// The body is never fully buffered in memory.
	etag, size, aerr := h.store.putObjectStream(r.Context(), obj, r.Body)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	h.bus.Publish(r.Context(), events.Event{
		Type:   events.S3ObjectCreated,
		Time:   obj.LastModified,
		Source: "s3",
		Payload: events.S3ObjectPayload{
			Bucket:    bucket,
			Key:       key,
			Size:      size,
			ETag:      etag,
			EventName: "ObjectCreated:Put",
		},
	})

	w.Header().Set("ETag", etag)
	protocol.WriteEmpty(w, r, http.StatusOK)
}

// GetObject handles GET /{bucket}/{key}.
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObject.html
func (h *Handler) GetObject(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	key := objectKey(r)

	// Load metadata only — body is streamed separately.
	obj, aerr := h.store.getObjectMeta(r.Context(), bucket, key)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	// Open the body file for streaming.
	f, aerr := h.store.openBody(bucket, key)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", obj.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(obj.ContentLength, 10))
	w.Header().Set("ETag", obj.ETag)
	w.Header().Set("Last-Modified", obj.LastModified.UTC().Format(http.TimeFormat))
	w.Header().Set("x-amz-request-id", protocol.RequestIDFromContext(r.Context()))

	// Restore stored response headers.
	if obj.ContentDisposition != "" {
		w.Header().Set("Content-Disposition", obj.ContentDisposition)
	}
	if obj.ContentEncoding != "" {
		w.Header().Set("Content-Encoding", obj.ContentEncoding)
	}
	if obj.ContentLanguage != "" {
		w.Header().Set("Content-Language", obj.ContentLanguage)
	}
	if obj.CacheControl != "" {
		w.Header().Set("Cache-Control", obj.CacheControl)
	}
	if obj.Expires != "" {
		w.Header().Set("Expires", obj.Expires)
	}

	// Restore user metadata headers.
	for k, v := range obj.Metadata {
		w.Header().Set("x-amz-meta-"+k, v)
	}

	w.WriteHeader(http.StatusOK)
	io.Copy(w, f)
}

// HeadObject handles HEAD /{bucket}/{key}
// Returns the same headers as GetObject but no body.
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_HeadObject.html
func (h *Handler) HeadObject(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	key := objectKey(r)

	// Metadata only — no body read from disk.
	obj, aerr := h.store.getObjectMeta(r.Context(), bucket, key)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	w.Header().Set("Content-Type", obj.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(obj.ContentLength, 10))
	w.Header().Set("ETag", obj.ETag)
	w.Header().Set("Last-Modified", obj.LastModified.UTC().Format(http.TimeFormat))
	w.Header().Set("x-amz-request-id", protocol.RequestIDFromContext(r.Context()))
	if obj.ContentDisposition != "" {
		w.Header().Set("Content-Disposition", obj.ContentDisposition)
	}
	if obj.ContentEncoding != "" {
		w.Header().Set("Content-Encoding", obj.ContentEncoding)
	}
	if obj.ContentLanguage != "" {
		w.Header().Set("Content-Language", obj.ContentLanguage)
	}
	if obj.CacheControl != "" {
		w.Header().Set("Cache-Control", obj.CacheControl)
	}
	if obj.Expires != "" {
		w.Header().Set("Expires", obj.Expires)
	}
	w.WriteHeader(http.StatusOK)
}

// ---- Batch delete -----------------------------------------------------------

// deleteObjectsRequest is the XML body for POST /{bucket}?delete.
type deleteObjectsRequest struct {
	XMLName xml.Name            `xml:"Delete"`
	Quiet   bool                `xml:"Quiet"`
	Objects []deleteObjectEntry `xml:"Object"`
}

type deleteObjectEntry struct {
	Key       string `xml:"Key"`
	VersionId string `xml:"VersionId,omitempty"`
}

// deleteObjectsResponse is the XML response for DeleteObjects.
type deleteObjectsResponse struct {
	XMLName xml.Name            `xml:"DeleteResult"`
	XMLNS   string              `xml:"xmlns,attr"`
	Deleted []deletedObject     `xml:"Deleted,omitempty"`
	Errors  []deleteObjectError `xml:"Error,omitempty"`
}

type deletedObject struct {
	Key string `xml:"Key"`
}

type deleteObjectError struct {
	Key     string `xml:"Key"`
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

// DeleteObjects handles POST /{bucket}?delete — batch delete up to 1000 keys.
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteObjects.html
func (h *Handler) DeleteObjects(w http.ResponseWriter, r *http.Request) {
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

	var req deleteObjectsRequest
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		protocol.WriteXMLError(w, r, &protocol.AWSError{Code: "MalformedXML", HTTPStatus: http.StatusBadRequest, Message: "The XML you provided was not well-formed"})
		return
	}

	if len(req.Objects) > 1000 {
		protocol.WriteXMLError(w, r, &protocol.AWSError{Code: "MalformedXML", HTTPStatus: http.StatusBadRequest, Message: "The XML you provided had more than 1000 objects"})
		return
	}

	resp := deleteObjectsResponse{
		XMLNS: "http://s3.amazonaws.com/doc/2006-03-01/",
	}

	now := h.clk.Now().UTC()

	for _, obj := range req.Objects {
		if delErr := h.store.deleteObject(r.Context(), bucket, obj.Key); delErr != nil {
			resp.Errors = append(resp.Errors, deleteObjectError{
				Key:     obj.Key,
				Code:    delErr.Code,
				Message: delErr.Message,
			})
		} else {
			if !req.Quiet {
				resp.Deleted = append(resp.Deleted, deletedObject{Key: obj.Key})
			}
			h.bus.Publish(r.Context(), events.Event{
				Type:   events.S3ObjectRemoved,
				Time:   now,
				Source: "s3",
				Payload: events.S3ObjectPayload{
					Bucket:    bucket,
					Key:       obj.Key,
					EventName: "ObjectRemoved:Delete",
				},
			})
		}
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	xml.NewEncoder(w).Encode(resp)
}

// DeleteObject handles DELETE /{bucket}/{key}.
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteObject.html
func (h *Handler) DeleteObject(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	key := objectKey(r)

	// Verify bucket exists first (AWS returns NoSuchBucket, not NoSuchKey).
	exists, aerr := h.store.bucketExists(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	if !exists {
		protocol.WriteXMLError(w, r, errNoSuchBucket(bucket))
		return
	}

	// AWS DeleteObject is idempotent — deleting a non-existent key returns 204.
	if aerr := h.store.deleteObject(r.Context(), bucket, key); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	h.bus.Publish(r.Context(), events.Event{
		Type:   events.S3ObjectRemoved,
		Time:   h.clk.Now().UTC(),
		Source: "s3",
		Payload: events.S3ObjectPayload{
			Bucket:    bucket,
			Key:       key,
			EventName: "ObjectRemoved:Delete",
		},
	})

	protocol.WriteEmpty(w, r, http.StatusNoContent)
}

// copyObjectResponse is the XML for a successful CopyObject.
type copyObjectResponse struct {
	XMLName      xml.Name  `xml:"CopyObjectResult"`
	LastModified time.Time `xml:"LastModified"`
	ETag         string    `xml:"ETag"`
}

// CopyObject handles PUT /{bucket}/{key} with x-amz-copy-source header.
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_CopyObject.html
func (h *Handler) CopyObject(w http.ResponseWriter, r *http.Request) {
	destBucket := chi.URLParam(r, "bucket")
	destKey := objectKey(r)

	// Copy source format: /sourceBucket/sourceKey (leading slash is optional).
	copySource := r.Header.Get("x-amz-copy-source")
	copySource = strings.TrimPrefix(copySource, "/")
	parts := strings.SplitN(copySource, "/", 2)
	if len(parts) != 2 {
		protocol.WriteXMLError(w, r, protocol.ErrInvalidArgument("Invalid copy source"))
		return
	}
	srcBucket, srcKey := parts[0], parts[1]

	// Load source metadata only — body is streamed via copyBody.
	src, aerr := h.store.getObjectMeta(r.Context(), srcBucket, srcKey)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	// Stream source body to destination file, computing MD5 incrementally.
	etag, n, err := h.store.copyBody(srcBucket, srcKey, destBucket, destKey)
	if err != nil {
		protocol.WriteXMLError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	now := h.clk.Now().UTC()
	dest := &Object{
		Bucket:        destBucket,
		Key:           destKey,
		ContentType:   src.ContentType,
		ContentLength: n,
		ETag:          etag,
		LastModified:  now,
		Metadata:      src.Metadata,
	}

	// Persist destination metadata (body already on disk from copyBody).
	raw, err := json.Marshal(dest)
	if err != nil {
		protocol.WriteXMLError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if err := h.store.store.Set(r.Context(), nsObjects, objectStoreKey(destBucket, destKey), string(raw)); err != nil {
		protocol.WriteXMLError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	h.bus.Publish(r.Context(), events.Event{
		Type:   events.S3ObjectCreated,
		Time:   now,
		Source: "s3",
		Payload: events.S3ObjectPayload{
			Bucket:    destBucket,
			Key:       destKey,
			Size:      n,
			ETag:      etag,
			EventName: "ObjectCreated:Copy",
		},
	})

	protocol.WriteXML(w, r, http.StatusOK, &copyObjectResponse{
		LastModified: now,
		ETag:         etag,
	})
}
