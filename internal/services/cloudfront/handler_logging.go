package cloudfront

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// responseRecorder wraps ResponseWriter to capture status code and bytes written
// for access logging purposes.
type responseRecorder struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int
}

func newResponseRecorder(w http.ResponseWriter) *responseRecorder {
	return &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}
}

func (rr *responseRecorder) WriteHeader(code int) {
	rr.statusCode = code
	rr.ResponseWriter.WriteHeader(code)
}

func (rr *responseRecorder) Write(b []byte) (int, error) {
	n, err := rr.ResponseWriter.Write(b)
	rr.bytesWritten += n
	return n, err
}

// writeAccessLog writes a single W3C Extended Log Format entry to the
// distribution's configured S3 logging bucket. It fires asynchronously
// so it never blocks or fails the request.
func (h *Handler) writeAccessLog(
	cfg *DistributionConfig,
	distID string,
	r *http.Request,
	statusCode int,
	bytesSent int,
	cacheStatus string, // "Hit", "Miss", "Error"
	elapsed time.Duration,
) {
	if cfg.Logging == nil || !cfg.Logging.Enabled {
		return
	}

	// Extract bucket name from the Bucket field.
	// CF log bucket is typically "mybucket.s3.amazonaws.com" — strip the suffix.
	bucket := cfg.Logging.Bucket
	bucket = strings.TrimSuffix(bucket, ".s3.amazonaws.com")
	// Also handle regional: "mybucket.s3.us-east-1.amazonaws.com"
	if idx := strings.Index(bucket, ".s3."); idx > 0 {
		bucket = bucket[:idx]
	}
	if bucket == "" {
		return
	}

	prefix := cfg.Logging.Prefix

	now := h.clk.Now().UTC()
	dateStr := now.Format("2006-01-02")
	timeStr := now.Format("15:04:05")

	clientIP := r.RemoteAddr
	if ip, _, err := net.SplitHostPort(clientIP); err == nil {
		clientIP = ip
	}

	host := r.Header.Get("Host")
	if host == "" {
		host = distID + ".cloudfront.net"
	}

	referer := r.Header.Get("Referer")
	if referer == "" {
		referer = "-"
	}
	ua := r.Header.Get("User-Agent")
	if ua == "" {
		ua = "-"
	}
	qs := r.URL.RawQuery
	if qs == "" {
		qs = "-"
	}

	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}

	elapsedSec := fmt.Sprintf("%.3f", elapsed.Seconds())

	// W3C Extended Log Format (tab-separated), matching CloudFront's real format.
	line := strings.Join([]string{
		dateStr,                            // date
		timeStr,                            // time
		"DEV-P1",                           // x-edge-location
		fmt.Sprintf("%d", bytesSent),       // sc-bytes
		clientIP,                           // c-ip
		r.Method,                           // cs-method
		host,                               // cs(Host)
		r.URL.Path,                         // cs-uri-stem
		fmt.Sprintf("%d", statusCode),      // sc-status
		referer,                            // cs(Referer)
		ua,                                 // cs(User-Agent)
		qs,                                 // cs-uri-query
		"-",                                // cs(Cookie)
		cacheStatus + "FromCloudFront",     // x-edge-result-type
		distID,                             // x-edge-request-id
		host,                               // x-host-header
		scheme,                             // cs-protocol
		fmt.Sprintf("%d", r.ContentLength), // cs-bytes
		elapsedSec,                         // time-taken
	}, "\t") + "\n"

	// Key: {prefix}YYYY-MM-DD/{distId}-HH-MM-SS.log
	key := fmt.Sprintf("%s%s/%s-%s.log", prefix, dateStr, distID, now.Format("15-04-05"))

	port := h.cfg.Port
	// Fire-and-forget write to local S3 emulator.
	go func() {
		url := fmt.Sprintf("http://localhost:%d/%s/%s", port, bucket, key)
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, url, bytes.NewReader([]byte(line)))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "text/plain")
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			return
		}
		_ = resp.Body.Close()
	}()
}
