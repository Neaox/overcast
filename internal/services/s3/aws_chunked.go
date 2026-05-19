package s3

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// maybeDecodeAWSChunked wraps r.Body with an aws-chunked decoder when the
// request uses AWS streaming upload (SigV4 chunked signing or unsigned chunked).
// AWS SDK for .NET v4 and newer Rust/Java SDKs default to streaming uploads
// even for tiny payloads, which means the on-wire body is a sequence of
//
//	<hex-len>;chunk-signature=...\r\n<data>\r\n
//
// chunks terminated by a zero-length chunk. Without decoding, we'd store the
// signature framing as the object body. The signal is either:
//
//   - Content-Encoding contains "aws-chunked"
//   - x-amz-content-sha256 starts with "STREAMING-"
//
// Returns the wrapped body and the decoded length (from
// x-amz-decoded-content-length), or the original body and -1 if no decoding
// is needed.
func maybeDecodeAWSChunked(r *http.Request) (io.ReadCloser, int64) {
	if !isAWSChunked(r) {
		return r.Body, -1
	}
	decoded := int64(-1)
	if v := r.Header.Get("X-Amz-Decoded-Content-Length"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			decoded = n
		}
	}
	return &chunkedReadCloser{r: bufio.NewReader(r.Body), src: r.Body}, decoded
}

func isAWSChunked(r *http.Request) bool {
	if strings.Contains(strings.ToLower(r.Header.Get("Content-Encoding")), "aws-chunked") {
		return true
	}
	if strings.HasPrefix(r.Header.Get("X-Amz-Content-Sha256"), "STREAMING-") {
		return true
	}
	return false
}

// stripAWSChunkedEncoding removes "aws-chunked" from a Content-Encoding value
// so it isn't echoed back on GetObject. Returns "" if aws-chunked was the
// only value.
func stripAWSChunkedEncoding(v string) string {
	if v == "" {
		return ""
	}
	parts := strings.Split(v, ",")
	out := parts[:0]
	for _, p := range parts {
		if strings.EqualFold(strings.TrimSpace(p), "aws-chunked") {
			continue
		}
		out = append(out, p)
	}
	return strings.TrimSpace(strings.Join(out, ","))
}

// chunkedReadCloser decodes an aws-chunked stream on the fly.
type chunkedReadCloser struct {
	r       *bufio.Reader
	src     io.Closer
	remain  int64 // bytes left in current data chunk
	done    bool
	pending error
}

func (c *chunkedReadCloser) Read(p []byte) (int, error) {
	if c.pending != nil {
		return 0, c.pending
	}
	if c.done {
		return 0, io.EOF
	}
	if c.remain == 0 {
		if err := c.nextChunk(); err != nil {
			c.pending = err
			return 0, err
		}
		if c.done {
			return 0, io.EOF
		}
	}
	n := int64(len(p))
	if n > c.remain {
		n = c.remain
	}
	read, err := c.r.Read(p[:n])
	c.remain -= int64(read)
	if c.remain == 0 && err == nil {
		// consume trailing CRLF after chunk data
		if _, err := c.r.Discard(2); err != nil {
			return read, err
		}
	}
	return read, err
}

func (c *chunkedReadCloser) Close() error {
	if c.src != nil {
		return c.src.Close()
	}
	return nil
}

// nextChunk parses the next chunk header: <hex-len>[;chunk-signature=...]\r\n
// and sets c.remain to the decoded length. A zero-length chunk marks EOF
// (optionally followed by trailers we drain and discard).
func (c *chunkedReadCloser) nextChunk() error {
	line, err := c.r.ReadString('\n')
	if err != nil {
		return err
	}
	line = strings.TrimRight(line, "\r\n")
	hexPart := line
	if i := strings.IndexByte(line, ';'); i >= 0 {
		hexPart = line[:i]
	}
	if hexPart == "" {
		return fmt.Errorf("aws-chunked: empty chunk size line")
	}
	n, err := strconv.ParseInt(hexPart, 16, 64)
	if err != nil {
		return fmt.Errorf("aws-chunked: invalid chunk size %q: %w", hexPart, err)
	}
	if n == 0 {
		// Drain trailers until the blank line terminator.
		for {
			l, err := c.r.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					break
				}
				return err
			}
			if strings.TrimRight(l, "\r\n") == "" {
				break
			}
		}
		c.done = true
		return nil
	}
	c.remain = n
	return nil
}
