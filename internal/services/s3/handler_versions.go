package s3

// handler_versions.go implements ListObjectVersions.
//
// Two sources, one wire shape. A bucket that has never been versioned has no
// records in s3:versions at all, so its listing is built from the current-
// object namespace and every entry is the key's null version — which is
// literally what AWS reports for such a bucket. A versioned bucket is listed
// straight out of s3:versions, whose storage keys already sort the way AWS
// documents the response to be ordered ("in alphabetical order by key, and
// within each key, most recently stored first"), so the scan needs no sort and
// its cursor doubles as the pagination marker. See version.go for the keyspace.

import (
	"encoding/xml"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// listVersionsResult is the XML envelope for ListObjectVersions.
//
// Entries is a heterogeneous slice because AWS interleaves <Version> and
// <DeleteMarker> elements in one ordered run rather than emitting all of one
// then all of the other; each element names itself through its own XMLName, so
// Go's encoder preserves that order.
type listVersionsResult struct {
	XMLName             xml.Name       `xml:"ListVersionsResult"`
	Xmlns               string         `xml:"xmlns,attr"`
	Name                string         `xml:"Name"`
	Prefix              string         `xml:"Prefix"`
	Delimiter           string         `xml:"Delimiter,omitempty"`
	KeyMarker           string         `xml:"KeyMarker"`
	VersionIdMarker     string         `xml:"VersionIdMarker"`
	NextKeyMarker       string         `xml:"NextKeyMarker,omitempty"`
	NextVersionIdMarker string         `xml:"NextVersionIdMarker,omitempty"`
	MaxKeys             int            `xml:"MaxKeys"`
	IsTruncated         bool           `xml:"IsTruncated"`
	Entries             []any          ``
	CommonPrefixes      []commonPrefix `xml:"CommonPrefixes"`
}

// versionEntry is com.amazonaws.s3#ObjectVersion. A delete marker is a
// different shape — no ETag, size or storage class — so it gets its own struct
// rather than an ObjectVersion with holes in it.
type versionEntry struct {
	XMLName      xml.Name  `xml:"Version"`
	Key          string    `xml:"Key"`
	VersionId    string    `xml:"VersionId"`
	IsLatest     bool      `xml:"IsLatest"`
	LastModified time.Time `xml:"LastModified"`
	ETag         string    `xml:"ETag"`
	Size         int64     `xml:"Size"`
	StorageClass string    `xml:"StorageClass"`
	Owner        ownerXML  `xml:"Owner"`
}

// deleteMarkerEntry is com.amazonaws.s3#DeleteMarkerEntry.
type deleteMarkerEntry struct {
	XMLName      xml.Name  `xml:"DeleteMarker"`
	Key          string    `xml:"Key"`
	VersionId    string    `xml:"VersionId"`
	IsLatest     bool      `xml:"IsLatest"`
	LastModified time.Time `xml:"LastModified"`
	Owner        ownerXML  `xml:"Owner"`
}

type ownerXML struct {
	ID          string `xml:"ID"`
	DisplayName string `xml:"DisplayName"`
}

// versionsPageEntry is one effective entry in a ListObjectVersions page: a
// version, a delete marker, or a delimiter-collapsed common prefix. It carries
// the (key, version id) pair the next page's markers are built from.
type versionsPageEntry struct {
	isPrefix  bool
	key       string
	versionID string
	obj       *Object
}

// ListObjectVersions handles GET /{bucket}?versions.
// AWS docs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListObjectVersions.html
func (h *Handler) ListObjectVersions(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	prefix := serviceutil.QueryString(r, "prefix", "")
	delimiter := serviceutil.QueryString(r, "delimiter", "")
	keyMarker := serviceutil.QueryString(r, "key-marker", "")
	versionIDMarker := serviceutil.QueryString(r, "version-id-marker", "")
	maxKeys := serviceutil.QueryInt(r, "max-keys", 1000)

	b, aerr := h.store.getBucket(r.Context(), bucket)
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}
	if versionIDMarker != "" && keyMarker == "" {
		protocol.WriteXMLError(w, r, protocol.ErrInvalidArgument(
			"A version-id marker cannot be specified without a key marker."))
		return
	}

	var entries []versionsPageEntry
	if b.versioned() {
		if aerr := h.ensureVersionHistory(r.Context(), b); aerr != nil {
			protocol.WriteXMLError(w, r, aerr)
			return
		}
		entries, aerr = h.buildVersionsPage(r, bucket, prefix, delimiter, keyMarker, versionIDMarker, maxKeys)
	} else {
		entries, aerr = h.buildNullVersionsPage(r, bucket, prefix, delimiter, keyMarker, versionIDMarker, maxKeys)
	}
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	truncated := len(entries) > maxKeys
	if truncated {
		entries = entries[:maxKeys]
	}

	owner := ownerXML{ID: h.cfg.AccountID, DisplayName: "overcast"}
	resp := &listVersionsResult{
		Xmlns:           s3XMLNamespace,
		Name:            bucket,
		Prefix:          prefix,
		Delimiter:       delimiter,
		KeyMarker:       keyMarker,
		VersionIdMarker: versionIDMarker,
		MaxKeys:         maxKeys,
		IsTruncated:     truncated,
	}
	for _, e := range entries {
		switch {
		case e.isPrefix:
			resp.CommonPrefixes = append(resp.CommonPrefixes, commonPrefix{Prefix: e.key})
		case e.obj.DeleteMarker:
			resp.Entries = append(resp.Entries, deleteMarkerEntry{
				Key:          e.obj.Key,
				VersionId:    e.versionID,
				IsLatest:     e.obj.IsLatest,
				LastModified: e.obj.LastModified,
				Owner:        owner,
			})
		default:
			resp.Entries = append(resp.Entries, versionEntry{
				Key:          e.obj.Key,
				VersionId:    e.versionID,
				IsLatest:     e.obj.IsLatest,
				LastModified: e.obj.LastModified,
				ETag:         e.obj.ETag,
				Size:         e.obj.ContentLength,
				StorageClass: e.obj.effectiveStorageClass(),
				Owner:        owner,
			})
		}
	}
	if truncated && len(entries) > 0 {
		last := entries[len(entries)-1]
		resp.NextKeyMarker = last.key
		resp.NextVersionIdMarker = last.versionID
	}

	protocol.WriteXML(w, r, http.StatusOK, resp)
}

// buildNullVersionsPage lists a bucket that has never been versioned. Every
// object is its key's only version, and AWS names that version "null".
//
// It reuses buildListPage, so prefix, delimiter and the marker's
// already-returned-entry rules behave exactly as they do for ListObjectsV2 —
// including the collapse rule that keeps a common prefix from reappearing on
// the next page.
func (h *Handler) buildNullVersionsPage(r *http.Request, bucket, prefix, delimiter, keyMarker, versionIDMarker string, maxKeys int) ([]versionsPageEntry, *protocol.AWSError) {
	if versionIDMarker != "" && versionIDMarker != nullVersionID {
		// The only version such a bucket has is the null one.
		return nil, errInvalidVersionID()
	}

	listed, aerr := h.buildListPage(r.Context(), bucket, prefix, delimiter, keyMarker, maxKeys)
	if aerr != nil {
		return nil, aerr
	}
	entries := make([]versionsPageEntry, 0, len(listed))
	for _, e := range listed {
		if e.isPrefix {
			entries = append(entries, versionsPageEntry{isPrefix: true, key: e.key})
			continue
		}
		obj := *e.obj
		obj.IsLatest = true
		entries = append(entries, versionsPageEntry{key: obj.Key, versionID: nullVersionID, obj: &obj})
	}
	return entries, nil
}

// buildVersionsPage lists a versioned bucket straight out of s3:versions.
//
// The scan already delivers AWS's order, so the only work here is the delimiter
// collapse, dropping entries a previous page already returned, and stopping one
// entry past maxKeys so the caller can detect truncation. Memory is bounded by
// the internal chunk size rather than by the number of versions in the bucket.
func (h *Handler) buildVersionsPage(r *http.Request, bucket, prefix, delimiter, keyMarker, versionIDMarker string, maxKeys int) ([]versionsPageEntry, *protocol.AWSError) {
	cursor, markerSeq, aerr := h.versionsCursor(r, bucket, keyMarker, versionIDMarker)
	if aerr != nil {
		return nil, aerr
	}

	var (
		entries      []versionsPageEntry
		seenPrefixes = map[string]bool{}
		latestSeen   = map[string]bool{}
	)
	for {
		versions, next, aerr := h.store.listVersionsPage(r.Context(), bucket, prefix, cursor, internalListPageChunk)
		if aerr != nil {
			return nil, aerr
		}

		for _, v := range versions {
			// The first version seen for a key is the newest, which is the
			// current one — the invariant the promotion path maintains.
			v.IsLatest = !latestSeen[v.Key]
			latestSeen[v.Key] = true

			effectiveKey, isPrefix := collapseToPrefix(v.Key, prefix, delimiter)
			if skipVersionEntry(effectiveKey, isPrefix, v.Seq, keyMarker, markerSeq) {
				continue
			}
			if isPrefix {
				if seenPrefixes[effectiveKey] {
					continue
				}
				seenPrefixes[effectiveKey] = true
				entries = append(entries, versionsPageEntry{isPrefix: true, key: effectiveKey})
			} else {
				entries = append(entries, versionsPageEntry{key: v.Key, versionID: v.wireVersionID(), obj: v})
			}
			if len(entries) > maxKeys {
				return entries, nil
			}
		}

		if next == "" {
			return entries, nil
		}
		cursor = next
	}
}

// versionsCursor turns the request's markers into the storage-key suffix the
// scan resumes after. It is an optimisation, not the correctness boundary —
// skipVersionEntry re-applies the marker rules to whatever the scan returns —
// but it keeps a deep page from rescanning the bucket from the top.
func (h *Handler) versionsCursor(r *http.Request, bucket, keyMarker, versionIDMarker string) (cursor, markerSeq string, aerr *protocol.AWSError) {
	if keyMarker == "" {
		return "", "", nil
	}
	if versionIDMarker == "" {
		// Resume after every version of the marker key. versionScanHigh sorts
		// above any sort token, so it lands past that key's whole group.
		return keyMarker + versionSep + versionScanHigh, "", nil
	}
	// The marker is a wire version id, and the null version's id is not its
	// sort token, so it has to be resolved to a record before it can be
	// compared against one.
	target, found, aerr := h.store.findVersion(r.Context(), bucket, keyMarker, versionIDMarker)
	if aerr != nil {
		return "", "", aerr
	}
	if !found {
		return "", "", errInvalidVersionID()
	}
	return keyMarker + versionSep + target.Seq, target.Seq, nil
}

// collapseToPrefix applies the delimiter rule: a key whose remainder after the
// request prefix contains the delimiter is reported as a common prefix instead
// of an entry of its own.
func collapseToPrefix(key, prefix, delimiter string) (effectiveKey string, isPrefix bool) {
	if delimiter == "" || !strings.HasPrefix(key, prefix) {
		return key, false
	}
	remainder := key[len(prefix):]
	idx := strings.Index(remainder, delimiter)
	if idx < 0 {
		return key, false
	}
	return prefix + remainder[:idx+len(delimiter)], true
}

// skipVersionEntry reports whether an entry was already returned by an earlier
// page.
//
// The comparison is against the ORIGINAL markers rather than the advancing scan
// cursor, for the same reason ListObjectsV2's buildListPage compares against its
// original startAfter: objects inside a collapsed common prefix have real keys
// that sort after the prefix string, so a raw-key cursor alone would let them
// back in as duplicates of a prefix already emitted.
func skipVersionEntry(effectiveKey string, isPrefix bool, seq, keyMarker, markerSeq string) bool {
	if keyMarker == "" {
		return false
	}
	if effectiveKey < keyMarker {
		return true
	}
	if effectiveKey > keyMarker {
		return false
	}
	// Same key as the marker.
	if isPrefix {
		// The whole collapsed group was returned with the marker.
		return true
	}
	if markerSeq == "" {
		// No version named, so every version of the marker key is behind us.
		return true
	}
	// Sort tokens descend with recency, so "already returned" is "sorts at or
	// before the marker's token".
	return seq <= markerSeq
}
