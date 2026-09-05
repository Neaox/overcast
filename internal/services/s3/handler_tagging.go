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
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"sort"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/overcast-sh/overcast/internal/protocol"
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
	Xmlns   string    `xml:"xmlns,attr,omitempty"`
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

// Tag-set limits, from "Using cost allocation S3 bucket tags" in the S3 User
// Guide: "A tag set can contain as many as 50 tags, or it can be empty. Keys
// must be unique within a tag set", with a key of "1 to 128 Unicode
// characters" and a value of "0 to 256 Unicode characters". Object tagging
// carries the tighter 10-tag limit S3 documents for objects.
const (
	maxBucketTags   = 50
	maxObjectTags   = 10
	maxTagKeyRunes  = 128
	maxTagValRunes  = 256
	bucketTagTarget = "Bucket"
	objectTagTarget = "Object"
)

// errInvalidTag and errTooManyTags carry the codes real S3 answers with. The
// PutBucketTagging reference lists InvalidTag ("The tag provided was not a
// valid tag. This error can occur if the tag did not pass input validation")
// as one of the operation's special errors but does not spell the messages
// out; these are the ones Versity's AWS conformance suite pins. Note that the
// count limit is *not* an InvalidTag — no individual tag is at fault — and
// answers with the bare BadRequest code instead.
func errInvalidTag(message string) *protocol.AWSError {
	return &protocol.AWSError{Code: "InvalidTag", Message: message, HTTPStatus: http.StatusBadRequest}
}

func errTooManyTags(target string, limit int) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "BadRequest",
		Message:    fmt.Sprintf("%s tag count cannot be greater than %d", target, limit),
		HTTPStatus: http.StatusBadRequest,
	}
}

// tagsFromXML validates a decoded TagSet against AWS's limits and converts it
// to the stored map. Validation has to happen before the map is built:
// collapsing duplicate keys into a map is exactly the divergence being fixed
// — S3 rejects a tag set that repeats a key rather than keeping whichever
// element came last.
//
// The one documented rule not enforced here is the allowed character set. The
// only place AWS writes it down is the S3 *Control* Tag shape's pattern, and
// guessing a Unicode class wrong would reject tags real S3 accepts, so the
// character set stays unvalidated and is recorded as a gap in
// docs/dev/compatibility/services/s3.yaml rather than approximated.
func tagsFromXML(tags []xmlTag, target string, limit int) (map[string]string, *protocol.AWSError) {
	if len(tags) > limit {
		return nil, errTooManyTags(target, limit)
	}
	out := make(map[string]string, len(tags))
	for _, t := range tags {
		if n := utf8.RuneCountInString(t.Key); n == 0 || n > maxTagKeyRunes {
			return nil, errInvalidTag("The TagKey you have provided is invalid")
		}
		if utf8.RuneCountInString(t.Value) > maxTagValRunes {
			return nil, errInvalidTag("The TagValue you have provided is invalid")
		}
		if _, dup := out[t.Key]; dup {
			return nil, errInvalidTag("Cannot provide multiple Tags with the same key")
		}
		out[t.Key] = t.Value
	}
	return out, nil
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
	if b.Tags == nil {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code: "NoSuchTagSet", Message: "The TagSet does not exist", HTTPStatus: http.StatusNotFound,
		})
		return
	}
	resp := xmlTagging{Xmlns: s3XMLNamespace, TagSet: xmlTagSet{Tags: tagsToXML(b.Tags)}}
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
	tags, aerr := tagsFromXML(req.TagSet.Tags, bucketTagTarget, maxBucketTags)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	b, aerr := h.store.getBucket(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	b.Tags = tags
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

// currentObjectForTagging loads the key's current version for a tagging
// operation. A key whose current version is a delete marker has nothing to tag,
// and AWS answers it as it answers any other read hidden behind a marker.
// Uses readObjectMeta rather than getObjectMeta directly so a nonexistent
// bucket answers NoSuchBucket, not NoSuchKey (#1635).
func (h *Handler) currentObjectForTagging(ctx context.Context, bucket, key string) (*Object, *protocol.AWSError) {
	obj, aerr := h.readObjectMeta(ctx, bucket, key)
	if aerr != nil {
		return nil, aerr
	}
	if obj.DeleteMarker {
		return nil, errNoSuchKey(key)
	}
	return obj, nil
}

// GetObjectTagging handles GET /{bucket}/{key}?tagging.
func (h *Handler) GetObjectTagging(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	key := objectKey(r)
	obj, aerr := h.currentObjectForTagging(r.Context(), bucket, key)
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
	tags, aerr := tagsFromXML(req.TagSet.Tags, objectTagTarget, maxObjectTags)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	obj, aerr := h.currentObjectForTagging(r.Context(), bucket, key)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	obj.Tags = tags
	if aerr := h.saveCurrentObject(r.Context(), obj); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	protocol.WriteEmpty(w, r, http.StatusOK)
}

// DeleteObjectTagging handles DELETE /{bucket}/{key}?tagging.
func (h *Handler) DeleteObjectTagging(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	key := objectKey(r)
	obj, aerr := h.currentObjectForTagging(r.Context(), bucket, key)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	obj.Tags = nil
	if aerr := h.saveCurrentObject(r.Context(), obj); aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	protocol.WriteEmpty(w, r, http.StatusNoContent)
}
