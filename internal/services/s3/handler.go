package s3

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/your-org/overcast/internal/clock"
	"github.com/your-org/overcast/internal/config"
	"github.com/your-org/overcast/internal/events"
	"github.com/your-org/overcast/internal/protocol"
	"github.com/your-org/overcast/internal/serviceutil"
	"github.com/your-org/overcast/internal/state"
)

// objectKey extracts the object key from a chi wildcard route.
// chi's "*" param returns the path after /{bucket}/, so "/my-bucket/a/b/c"
// yields "a/b/c". This is needed because chi's {key:.+} doesn't match slashes.
func objectKey(r *http.Request) string {
	return chi.URLParam(r, "*")
}

// Handler holds the dependencies for S3 HTTP handlers.
// All handler methods hang off this struct — this is the standard Go pattern
// for grouping related handlers (equivalent to a TypeScript class with methods).
type Handler struct {
	cfg   *config.Config
	store *s3Store
	log   *serviceutil.ServiceLogger
	clk   clock.Clock
	bus   *events.Bus

	bucketGetRoutes    []s3Route
	bucketPutRoutes    []s3Route
	bucketDeleteRoutes []s3Route
	bucketPostRoutes   []s3Route
	objectGetRoutes    []s3Route
	objectPutRoutes    []s3Route
	objectDeleteRoutes []s3Route
	objectPostRoutes   []s3Route
}

// s3Route maps a query-parameter name to its handler.
// Order matters: the first matching param wins (mirrors AWS priority behaviour).
type s3Route struct {
	param string
	fn    http.HandlerFunc
}

// dispatchByQuery iterates routes in order and calls the first handler whose
// query param is present. Falls back to fallback if none match.
func dispatchByQuery(w http.ResponseWriter, r *http.Request, routes []s3Route, fallback http.HandlerFunc) {
	for _, rt := range routes {
		if serviceutil.HasQueryParam(r, rt.param) {
			rt.fn(w, r)
			return
		}
	}
	fallback(w, r)
}

func newHandler(cfg *config.Config, store state.Store, log *serviceutil.ServiceLogger, clk clock.Clock, bus *events.Bus) *Handler {
	h := &Handler{
		cfg:   cfg,
		store: newS3Store(store, cfg.DataDir),
		log:   log,
		clk:   clk,
		bus:   bus,
	}

	h.initBucketRoutes()
	h.initObjectRoutes()

	return h
}

// ---- Bucket dispatchers ---------------------------------------------------

// BucketGet dispatches GET /{bucket} by sub-resource query param.
func (h *Handler) BucketGet(w http.ResponseWriter, r *http.Request) {
	dispatchByQuery(w, r, h.bucketGetRoutes, h.ListObjectsV2)
}

// BucketPut dispatches PUT /{bucket} by sub-resource query param.
func (h *Handler) BucketPut(w http.ResponseWriter, r *http.Request) {
	dispatchByQuery(w, r, h.bucketPutRoutes, h.CreateBucket)
}

// BucketDelete dispatches DELETE /{bucket} by sub-resource query param.
func (h *Handler) BucketDelete(w http.ResponseWriter, r *http.Request) {
	dispatchByQuery(w, r, h.bucketDeleteRoutes, h.DeleteBucket)
}

// BucketPost dispatches POST /{bucket} by sub-resource query param.
func (h *Handler) BucketPost(w http.ResponseWriter, r *http.Request) {
	dispatchByQuery(w, r, h.bucketPostRoutes, protocol.NotImplementedXML)
}

// ListObjectsV2OrLocation dispatches GET /{bucket} based on query parameters:
// ?list-type=2        → ListObjectsV2
// ?location           → GetBucketLocation
// (no params)         → ListObjectsV2 (default)
//
// Deprecated: use BucketGet which handles all S3 bucket-level query params.
func (h *Handler) ListObjectsV2OrLocation(w http.ResponseWriter, r *http.Request) {
	h.BucketGet(w, r)
}

// ---- Object handlers -------------------------------------------------------

// ObjectGet dispatches GET /{bucket}/{key} by sub-resource query param.
func (h *Handler) ObjectGet(w http.ResponseWriter, r *http.Request) {
	dispatchByQuery(w, r, h.objectGetRoutes, h.GetObject)
}

// ObjectDelete dispatches DELETE /{bucket}/{key} by sub-resource query param.
func (h *Handler) ObjectDelete(w http.ResponseWriter, r *http.Request) {
	dispatchByQuery(w, r, h.objectDeleteRoutes, h.DeleteObject)
}

// ObjectPost dispatches POST /{bucket}/{key} by sub-resource query param.
func (h *Handler) ObjectPost(w http.ResponseWriter, r *http.Request) {
	dispatchByQuery(w, r, h.objectPostRoutes, protocol.NotImplementedXML)
}

// PutObjectOrCopy dispatches PUT /{bucket}/{key}.
// partNumber is checked first because it requires a secondary header discriminant
// that the route table can't express. All other sub-resources use the table.
func (h *Handler) PutObjectOrCopy(w http.ResponseWriter, r *http.Request) {
	if serviceutil.HasQueryParam(r, "partNumber") {
		if r.Header.Get("x-amz-copy-source") != "" {
			h.UploadPartCopy(w, r)
		} else {
			h.UploadPart(w, r)
		}
		return
	}
	dispatchByQuery(w, r, h.objectPutRoutes, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-amz-copy-source") != "" {
			h.CopyObject(w, r)
		} else {
			h.PutObject(w, r)
		}
	})
}

// ---- Root-level dispatcher -------------------------------------------------

// RootGet dispatches GET / — either ListBuckets or ListDirectoryBuckets.
func (h *Handler) RootGet(w http.ResponseWriter, r *http.Request) {
	if serviceutil.HasQueryParam(r, "directory-buckets") {
		h.ListDirectoryBuckets(w, r)
		return
	}
	h.ListBuckets(w, r)
}
