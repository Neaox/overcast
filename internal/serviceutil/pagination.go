package serviceutil

import (
	"encoding/base64"
	"encoding/json"
	"errors"
)

// ErrInvalidPageToken is returned by Paginate when the caller-supplied
// continuation token cannot be decoded, or decodes to a start position that
// is no longer valid (negative, or past the end of the item set).
//
// Silently treating an invalid/garbled token as "start from page 1" is the
// most common AWS-fidelity divergence in this codebase (see
// docs/plans/pagination-plan.md, items H1/G3): a client polling with a
// stale or corrupted token receives the full item set again instead of an
// error, which looks like — and is handled by SDK retry logic as — a
// legitimate page, producing duplicate delivery.
//
// Callers MUST check this error (errors.Is) and map it to their own
// service's AWS error type/code instead of falling through to the returned
// (zero-value) Page. For example:
//
//	page, err := serviceutil.Paginate(items, maxResults, req.NextToken, opts)
//	if err != nil {
//	    // SSM: https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_DescribeParameters.html#API_DescribeParameters_Errors
//	    return nil, &protocol.AWSError{Code: "InvalidNextToken", Message: "The specified token isn't valid.", HTTPStatus: http.StatusBadRequest}
//	}
var ErrInvalidPageToken = errors.New("serviceutil: invalid pagination token")

// PaginateOptions configures the effective per-call page size. AWS
// documents a different default and cap for nearly every List/Describe
// operation (see the pagination-plan's per-op citations), so callers supply
// their own rather than relying on a single package-wide default.
type PaginateOptions struct {
	// DefaultLimit is used when the caller-requested limit is <= 0 (i.e.
	// the client omitted MaxResults/MaxItems/Limit entirely). If left at
	// the zero value, Paginate falls back to 1000 (S3 ListObjects'
	// documented default) so callers that don't set this field keep the
	// pre-H1 behavior.
	DefaultLimit int
	// MaxLimit caps the effective limit even when the caller requests (or
	// DefaultLimit specifies) more than AWS allows for this operation.
	// Zero means no cap is applied.
	MaxLimit int
}

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
// returning a Page with at most the effective limit's items.
//
// The continuation token encodes the start index opaquely (base64 of a JSON
// integer) so that callers cannot make assumptions about item ordering.
//
// requestedLimit is the raw value the client asked for (e.g. req.MaxResults);
// pass 0 or a negative number when the client omitted it. opts supplies the
// operation's AWS-documented default and cap.
//
// Returns ErrInvalidPageToken when continuationToken is non-empty but
// doesn't decode to a valid start position — see that error's doc comment
// for the caller contract this establishes.
//
// Example:
//
//	allObjects, _ := h.store.listObjects(ctx, bucket, prefix)
//	page, err := serviceutil.Paginate(allObjects, maxKeys, req.ContinuationToken,
//	    serviceutil.PaginateOptions{DefaultLimit: 1000})
//	if err != nil {
//	    // map to the service's AWS invalid-token error and return
//	}
//	// page.Items       — items for this page
//	// page.NextToken    — pass back to client as NextContinuationToken
//	// page.IsTruncated  — set in the response envelope
func Paginate[T any](items []T, requestedLimit int, continuationToken string, opts PaginateOptions) (Page[T], error) {
	limit := requestedLimit
	if limit <= 0 {
		limit = opts.DefaultLimit
		if limit <= 0 {
			limit = 1000
		}
	}
	if opts.MaxLimit > 0 && limit > opts.MaxLimit {
		limit = opts.MaxLimit
	}

	start := 0
	if continuationToken != "" {
		idx, ok := decodeToken(continuationToken)
		if !ok || idx < 0 || idx > len(items) {
			return Page[T]{}, ErrInvalidPageToken
		}
		start = idx
	}

	end := start + limit
	if end >= len(items) {
		return Page[T]{
			Items:       items[start:],
			IsTruncated: false,
		}, nil
	}

	return Page[T]{
		Items:       items[start:end],
		NextToken:   encodeToken(end),
		IsTruncated: true,
	}, nil
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
