package s3

// handler_multipart.go contains fully-implemented multipart upload handlers.
// Stubs (NotImplementedXML) live in handler_stubs.go.
// Dispatchers (ObjectPost, ObjectDelete, PutObjectOrCopy, BucketGet) live in
// handler.go.

import (
	"crypto/md5"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// ---- XML types for multipart operations ------------------------------------

type xmlInitiateMultipartUploadResult struct {
	XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
	NS       string   `xml:"xmlns,attr"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	UploadId string   `xml:"UploadId"`
}

type xmlCompleteMultipartUpload struct {
	Parts []xmlCompletePart `xml:"Part"`
}

type xmlCompletePart struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

type xmlCompleteMultipartUploadResult struct {
	XMLName  xml.Name `xml:"CompleteMultipartUploadResult"`
	NS       string   `xml:"xmlns,attr"`
	Location string   `xml:"Location"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	ETag     string   `xml:"ETag"`
}

type xmlListPartsResult struct {
	XMLName  xml.Name  `xml:"ListPartsResult"`
	NS       string    `xml:"xmlns,attr"`
	Bucket   string    `xml:"Bucket"`
	Key      string    `xml:"Key"`
	UploadId string    `xml:"UploadId"`
	Parts    []xmlPart `xml:"Part"`
}

type xmlPart struct {
	PartNumber   int    `xml:"PartNumber"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
	LastModified string `xml:"LastModified"`
}

type xmlListMultipartUploadsResult struct {
	XMLName xml.Name    `xml:"ListMultipartUploadsResult"`
	NS      string      `xml:"xmlns,attr"`
	Bucket  string      `xml:"Bucket"`
	Uploads []xmlUpload `xml:"Upload"`
}

type xmlUpload struct {
	UploadId  string `xml:"UploadId"`
	Key       string `xml:"Key"`
	Initiated string `xml:"Initiated"`
}

const s3XMLNamespace = "http://s3.amazonaws.com/doc/2006-03-01/"

// ---- CreateMultipartUpload -------------------------------------------------

// CreateMultipartUpload handles POST /{bucket}/{key}?uploads
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_CreateMultipartUpload.html
func (h *Handler) CreateMultipartUpload(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	key := objectKey(r)

	if aerr := h.requireBucket(r, bucket); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	upload := &MultipartUpload{
		UploadID:    uuid.New().String(),
		Bucket:      bucket,
		Key:         key,
		ContentType: contentType,
		Metadata:    serviceutil.HeaderPrefix(r, "X-Amz-Meta-"),
		Initiated:   h.clk.Now().UTC(),
	}

	if aerr := h.store.createMultipartUpload(r.Context(), upload); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	protocol.WriteXML(w, r, http.StatusOK, &xmlInitiateMultipartUploadResult{
		NS:       s3XMLNamespace,
		Bucket:   bucket,
		Key:      key,
		UploadId: upload.UploadID,
	})
}

// ---- UploadPart ------------------------------------------------------------

// UploadPart handles PUT /{bucket}/{key}?partNumber=N&uploadId=xxx
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_UploadPart.html
func (h *Handler) UploadPart(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	key := objectKey(r)
	uploadID := r.URL.Query().Get("uploadId")
	partNumberStr := r.URL.Query().Get("partNumber")

	partNumber, err := strconv.Atoi(partNumberStr)
	if err != nil || partNumber < 1 || partNumber > 10000 {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "InvalidArgument",
			Message:    "Part number must be an integer between 1 and 10000",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// Verify the upload exists and belongs to this bucket/key.
	upload, aerr := h.store.getMultipartUpload(r.Context(), uploadID)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	if upload.Bucket != bucket || upload.Key != key {
		protocol.WriteXMLError(w, r, errNoSuchUpload(uploadID))
		return
	}

	body, _ := maybeDecodeAWSChunked(r)
	etag, n, streamErr := h.store.putPartStream(uploadID, partNumber, body)
	if streamErr != nil {
		protocol.WriteXMLError(w, r, protocol.Wrap(protocol.ErrInternalError, streamErr))
		return
	}

	part := &Part{
		PartNumber:   partNumber,
		ETag:         etag,
		Size:         n,
		LastModified: h.clk.Now().UTC(),
	}
	if aerr := h.store.savePart(r.Context(), uploadID, part); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	w.Header().Set("ETag", etag)
	protocol.WriteEmpty(w, r, http.StatusOK)
}

// ---- CompleteMultipartUpload -----------------------------------------------

// CompleteMultipartUpload handles POST /{bucket}/{key}?uploadId=xxx
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_CompleteMultipartUpload.html
func (h *Handler) CompleteMultipartUpload(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	key := objectKey(r)
	uploadID := r.URL.Query().Get("uploadId")

	upload, aerr := h.store.getMultipartUpload(r.Context(), uploadID)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	if upload.Bucket != bucket || upload.Key != key {
		protocol.WriteXMLError(w, r, errNoSuchUpload(uploadID))
		return
	}

	// Parse the list of parts from the request body.
	var req xmlCompleteMultipartUpload
	if decodeErr := xml.NewDecoder(r.Body).Decode(&req); decodeErr != nil {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "MalformedXML",
			Message:    "The XML you provided was not well-formed",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// Retrieve stored parts in request order (parts must be in ascending order).
	storedParts, aerr := h.store.listParts(r.Context(), uploadID)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	// Index stored parts by part number.
	partByNum := make(map[int]*Part, len(storedParts))
	for _, p := range storedParts {
		partByNum[p.PartNumber] = p
	}

	orderedParts := make([]*Part, 0, len(req.Parts))
	for _, rp := range req.Parts {
		found, ok := partByNum[rp.PartNumber]
		if !ok {
			protocol.WriteXMLError(w, r, &protocol.AWSError{
				Code:       "InvalidPart",
				Message:    fmt.Sprintf("One or more of the specified parts could not be found: %d", rp.PartNumber),
				HTTPStatus: http.StatusBadRequest,
			})
			return
		}
		orderedParts = append(orderedParts, found)
	}

	// Stream all part bodies to the final object body path, computing MD5.
	finalPath := h.store.bodyPath(bucket, key)
	if mkdirErr := os.MkdirAll(filepath.Dir(finalPath), 0o755); mkdirErr != nil {
		protocol.WriteXMLError(w, r, protocol.Wrap(protocol.ErrInternalError, mkdirErr))
		return
	}

	finalFile, createErr := os.Create(finalPath)
	if createErr != nil {
		protocol.WriteXMLError(w, r, protocol.Wrap(protocol.ErrInternalError, createErr))
		return
	}

	mhash := md5.New()
	mw := io.MultiWriter(finalFile, mhash)
	var totalSize int64

	for _, p := range orderedParts {
		pf, openErr := h.store.openPartBody(uploadID, p.PartNumber)
		if openErr != nil {
			finalFile.Close()
			protocol.WriteXMLError(w, r, protocol.Wrap(protocol.ErrInternalError, openErr))
			return
		}
		n, copyErr := io.Copy(mw, pf)
		pf.Close()
		if copyErr != nil {
			finalFile.Close()
			protocol.WriteXMLError(w, r, protocol.Wrap(protocol.ErrInternalError, copyErr))
			return
		}
		totalSize += n
	}

	if cerr := finalFile.Close(); cerr != nil {
		protocol.WriteXMLError(w, r, protocol.Wrap(protocol.ErrInternalError, cerr))
		return
	}

	etag := fmt.Sprintf(`"%x-%d"`, mhash.Sum(nil), len(orderedParts))

	// Save final object metadata.
	now := h.clk.Now().UTC()
	obj := &Object{
		Bucket:        bucket,
		Key:           key,
		ContentType:   upload.ContentType,
		ContentLength: totalSize,
		ETag:          etag,
		LastModified:  now,
		Metadata:      upload.Metadata,
	}
	rawMeta, jsonErr := json.Marshal(obj)
	if jsonErr != nil {
		protocol.WriteXMLError(w, r, protocol.Wrap(protocol.ErrInternalError, jsonErr))
		return
	}
	if storeErr := h.store.store.Set(r.Context(), nsObjects, objectStoreKey(bucket, key), string(rawMeta)); storeErr != nil {
		protocol.WriteXMLError(w, r, protocol.Wrap(protocol.ErrInternalError, storeErr))
		return
	}

	// Clean up: delete parts + upload record.
	_ = h.store.deleteAllParts(r.Context(), uploadID)        // best-effort cleanup
	_ = h.store.deleteMultipartUpload(r.Context(), uploadID) // best-effort cleanup

	// Emit S3ObjectCreated event.
	h.bus.Publish(r.Context(), events.Event{
		Type:   events.S3ObjectCreated,
		Time:   now,
		Source: "s3",
		Payload: events.S3ObjectPayload{
			Bucket:    bucket,
			Key:       key,
			Size:      totalSize,
			ETag:      etag,
			EventName: "ObjectCreated:CompleteMultipartUpload",
		},
	})

	location := fmt.Sprintf("/%s/%s", bucket, key)
	protocol.WriteXML(w, r, http.StatusOK, &xmlCompleteMultipartUploadResult{
		NS:       s3XMLNamespace,
		Location: location,
		Bucket:   bucket,
		Key:      key,
		ETag:     etag,
	})
}

// ---- AbortMultipartUpload --------------------------------------------------

// AbortMultipartUpload handles DELETE /{bucket}/{key}?uploadId=xxx
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_AbortMultipartUpload.html
func (h *Handler) AbortMultipartUpload(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	key := objectKey(r)
	uploadID := r.URL.Query().Get("uploadId")

	upload, aerr := h.store.getMultipartUpload(r.Context(), uploadID)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	if upload.Bucket != bucket || upload.Key != key {
		protocol.WriteXMLError(w, r, errNoSuchUpload(uploadID))
		return
	}

	_ = h.store.deleteAllParts(r.Context(), uploadID) // best-effort cleanup
	if aerr := h.store.deleteMultipartUpload(r.Context(), uploadID); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	protocol.WriteEmpty(w, r, http.StatusNoContent)
}

// ---- ListParts -------------------------------------------------------------

// ListParts handles GET /{bucket}/{key}?uploadId=xxx
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListParts.html
func (h *Handler) ListParts(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	key := objectKey(r)
	uploadID := r.URL.Query().Get("uploadId")

	upload, aerr := h.store.getMultipartUpload(r.Context(), uploadID)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	if upload.Bucket != bucket || upload.Key != key {
		protocol.WriteXMLError(w, r, errNoSuchUpload(uploadID))
		return
	}

	parts, aerr := h.store.listParts(r.Context(), uploadID)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	xmlParts := make([]xmlPart, 0, len(parts))
	for _, p := range parts {
		xmlParts = append(xmlParts, xmlPart{
			PartNumber:   p.PartNumber,
			ETag:         p.ETag,
			Size:         p.Size,
			LastModified: p.LastModified.Format(time.RFC3339),
		})
	}

	protocol.WriteXML(w, r, http.StatusOK, &xmlListPartsResult{
		NS:       s3XMLNamespace,
		Bucket:   bucket,
		Key:      key,
		UploadId: uploadID,
		Parts:    xmlParts,
	})
}

// ---- ListMultipartUploads --------------------------------------------------

// ListMultipartUploads handles GET /{bucket}?uploads
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListMultipartUploads.html
func (h *Handler) ListMultipartUploads(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")

	if aerr := h.requireBucket(r, bucket); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	uploads, aerr := h.store.listMultipartUploads(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	xmlUploads := make([]xmlUpload, 0, len(uploads))
	for _, u := range uploads {
		xmlUploads = append(xmlUploads, xmlUpload{
			UploadId:  u.UploadID,
			Key:       u.Key,
			Initiated: u.Initiated.Format(time.RFC3339),
		})
	}

	protocol.WriteXML(w, r, http.StatusOK, &xmlListMultipartUploadsResult{
		NS:      s3XMLNamespace,
		Bucket:  bucket,
		Uploads: xmlUploads,
	})
}

// ---- requireBucket (shared helper) -----------------------------------------

// requireBucket checks that bucket exists and returns an AWSError if not.
func (h *Handler) requireBucket(r *http.Request, bucket string) *protocol.AWSError {
	exists, aerr := h.store.bucketExists(r.Context(), bucket)
	if aerr != nil {
		return aerr
	}
	if !exists {
		return errNoSuchBucket(bucket)
	}
	return nil
}
