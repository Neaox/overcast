package protocol

import (
	"bytes"
	"errors"
	"io"
	"net/http"
)

// MaxQueryFormBody bounds how much of a request body is ever read into memory
// in order to treat it as an AWS Query form — by ParseFormPreservingBody here,
// and by the Query codec's direct-body-read fallback. Real Query requests are
// far smaller: the largest inline payloads AWS accepts on this protocol are SNS
// Publish and SQS SendMessageBatch at 256 KiB and a CloudFormation inline
// TemplateBody at 51,200 bytes. This is generous headroom, not a realistic limit
// for legitimate traffic. Anything past it is not a Query request, and buffering
// it would mean holding an arbitrarily large S3 upload in memory.
const MaxQueryFormBody = 1 << 20 // 1 MiB

// ErrFormBodyTooLarge reports that a request body exceeded MaxQueryFormBody, so
// its form fields were not parsed. The body is left intact and readable.
var ErrFormBodyTooLarge = errors.New("protocol: request body exceeds the AWS Query form parse limit")

// ParseFormPreservingBody populates r.Form and r.PostForm the way
// (*http.Request).ParseForm does, but leaves r.Body readable afterwards.
//
// This exists because Content-Type is not a routing decision. The standard
// library's ParseForm drains r.Body for any POST/PUT/PATCH labelled
// application/x-www-form-urlencoded, so shared code that sniffs a request for
// AWS Query fields — protocol detection, IAM action extraction — would silently
// swallow the payload of a request that turned out to belong to a REST service.
// S3 PutObject is the case that bites: `curl --data-binary` sends that content
// type by default, and AWS stores the body regardless.
//
// Callers that only ever see genuine Query traffic can keep using ParseForm;
// anything running ahead of routing must use this.
func ParseFormPreservingBody(r *http.Request) error {
	// ParseForm leaves PostForm non-nil on every path, so this both makes the
	// call idempotent and avoids re-buffering a body a previous call consumed.
	if r.PostForm != nil {
		return nil
	}
	if r.Body == nil || r.Body == http.NoBody {
		return r.ParseForm()
	}

	// One byte past the limit is enough to tell "at the limit" from "over" it.
	buffered, err := io.ReadAll(io.LimitReader(r.Body, MaxQueryFormBody+1))
	if err != nil {
		r.Body = &preservedBody{Reader: bytes.NewReader(buffered), closer: r.Body}
		return err
	}
	if len(buffered) > MaxQueryFormBody {
		r.Body = &preservedBody{
			Reader: io.MultiReader(bytes.NewReader(buffered), r.Body),
			closer: r.Body,
		}
		return ErrFormBodyTooLarge
	}

	original := r.Body
	r.Body = io.NopCloser(bytes.NewReader(buffered))
	parseErr := r.ParseForm()
	r.Body = &preservedBody{Reader: bytes.NewReader(buffered), closer: original}
	return parseErr
}

// preservedBody hands the handler a replayable body while keeping the original
// Close, so the server still releases the connection's read side.
type preservedBody struct {
	io.Reader
	closer io.Closer
}

func (b *preservedBody) Close() error { return b.closer.Close() }
