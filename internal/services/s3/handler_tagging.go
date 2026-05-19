package s3

// handler_tagging.go — Object and bucket tagging handlers.
// AWS docs:
//   https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketTagging.html
//   https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketTagging.html
//   https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteBucketTagging.html
//   https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObjectTagging.html
//   https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObjectTagging.html
//   https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteObjectTagging.html

import (
	"encoding/xml"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/protocol"
)

// ---- XML wire types --------------------------------------------------------

type xmlTag struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

type xmlTagSet struct {
	Tags []xmlTag `xml:"Tag"`
}

type xmlTagging struct {
	XMLName xml.Name  `xml:"Tagging"`
	TagSet  xmlTagSet `xml:"TagSet"`
}

// tagsToXML converts a string map to a sorted slice of xmlTag.  Sorting
// ensures deterministic responses independent of map iteration order.
func tagsToXML(m map[string]string) []xmlTag {
	tags := make([]xmlTag, 0, len(m))
	for k, v := range m {
		tags = append(tags, xmlTag{Key: k, Value: v})
	}
	sort.Slice(tags, func(i, j int) bool { return tags[i].Key < tags[j].Key })
	return tags
}

func tagsFromXML(tags []xmlTag) map[string]string {
	out := make(map[string]string, len(tags))
	for _, t := range tags {
		out[t.Key] = t.Value
	}
	return out
}

// ---- Bucket tagging --------------------------------------------------------

// GetBucketTagging handles GET /{bucket}?tagging.
func (h *Handler) GetBucketTagging(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	b, aerr := h.store.getBucket(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	resp := xmlTagging{TagSet: xmlTagSet{Tags: tagsToXML(b.Tags)}}
	protocol.WriteXML(w, r, http.StatusOK, resp)
}

// PutBucketTagging handles PUT /{bucket}?tagging.
func (h *Handler) PutBucketTagging(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	var req xmlTagging
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInvalidArgument("malformed XML"))
		return
	}
	b, aerr := h.store.getBucket(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	b.Tags = tagsFromXML(req.TagSet.Tags)
	if aerr := h.store.putBucket(r.Context(), b); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	protocol.WriteEmpty(w, r, http.StatusNoContent)
}

// DeleteBucketTagging handles DELETE /{bucket}?tagging.
func (h *Handler) DeleteBucketTagging(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	b, aerr := h.store.getBucket(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	b.Tags = nil
	if aerr := h.store.putBucket(r.Context(), b); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	protocol.WriteEmpty(w, r, http.StatusNoContent)
}

// DeleteBucketTagging --------------------------------------------------------

// GetObjectTagging handles GET /{bucket}/{key}?tagging.
func (h *Handler) GetObjectTagging(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	key := objectKey(r)
	obj, aerr := h.store.getObjectMeta(r.Context(), bucket, key)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	resp := xmlTagging{TagSet: xmlTagSet{Tags: tagsToXML(obj.Tags)}}
	protocol.WriteXML(w, r, http.StatusOK, resp)
}

// PutObjectTagging handles PUT /{bucket}/{key}?tagging.
func (h *Handler) PutObjectTagging(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	key := objectKey(r)
	var req xmlTagging
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		protocol.WriteXMLError(w, r, protocol.ErrInvalidArgument("malformed XML"))
		return
	}
	obj, aerr := h.store.getObjectMeta(r.Context(), bucket, key)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	obj.Tags = tagsFromXML(req.TagSet.Tags)
	if aerr := h.store.putObjectMeta(r.Context(), obj); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	protocol.WriteEmpty(w, r, http.StatusOK)
}

// DeleteObjectTagging handles DELETE /{bucket}/{key}?tagging.
func (h *Handler) DeleteObjectTagging(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	key := objectKey(r)
	obj, aerr := h.store.getObjectMeta(r.Context(), bucket, key)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	obj.Tags = nil
	if aerr := h.store.putObjectMeta(r.Context(), obj); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	protocol.WriteEmpty(w, r, http.StatusNoContent)
}
