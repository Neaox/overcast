package serviceutil

import (
	"encoding/base64"
	"encoding/json"
)

// Page represents a single page of results from a paginated list operation.
// T is the item type (e.g. *s3.Object, *sqs.Queue).
type Page[T any] struct {
	// Items contains the items on this page.
	Items []T
	// NextToken is the opaque continuation token to retrieve the next page.
	// Empty string means this is the last page.
	NextToken string
	// IsTruncated is true when there are more items after this page.
	// It mirrors the AWS convention (used by S3's IsTruncated field).
	IsTruncated bool
}

// Paginate applies limit and continuation-token logic to a full item slice,
// returning a Page with at most maxItems items.
//
// The continuation token encodes the start index opaquely so that callers
// cannot make assumptions about the item ordering.
//
// Example:
//
//	allObjects, _ := h.store.listObjects(ctx, bucket, prefix)
//	page := serviceutil.Paginate(allObjects, maxKeys, req.ContinuationToken)
//	// page.Items    — items for this page
//	// page.NextToken — pass back to client as NextContinuationToken
//	// page.IsTruncated — set in the response envelope
func Paginate[T any](items []T, maxItems int, continuationToken string) Page[T] {
	if maxItems <= 0 {
		maxItems = 1000
	}

	start := 0
	if continuationToken != "" {
		if idx, ok := decodeToken(continuationToken); ok && idx >= 0 && idx < len(items) {
			start = idx
		}
	}

	end := start + maxItems
	if end >= len(items) {
		return Page[T]{
			Items:       items[start:],
			IsTruncated: false,
		}
	}

	return Page[T]{
		Items:       items[start:end],
		NextToken:   encodeToken(end),
		IsTruncated: true,
	}
}

// encodeToken encodes a start index as a base64 JSON token.
// The encoding is opaque to callers but deterministic for a given index.
func encodeToken(startIndex int) string {
	b, _ := json.Marshal(startIndex)
	return base64.URLEncoding.EncodeToString(b)
}

// decodeToken decodes a pagination token back to a start index.
// Returns (0, false) on any parse failure.
func decodeToken(token string) (int, bool) {
	b, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return 0, false
	}
	var idx int
	if err := json.Unmarshal(b, &idx); err != nil {
		return 0, false
	}
	return idx, true
}
