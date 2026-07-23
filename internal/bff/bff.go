// Package bff implements the browser-facing API layer (BFF) for the Overcast
// web console. It exposes a single http.Handler that serves:
//
//   - GET /api/* — thin proxies / adapters that call the emulator's internal
//     endpoints and return JSON the SPA expects
//   - /* — SPA static files with index.html fallback for client-side routing
//
// The BFF is a like-for-like Go port of web/api/src/ (Hono/Node). Every
// endpoint preserves the same method, path, request headers, response shape,
// and streaming behaviour as the Node original.
package bff

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Neaox/overcast/internal/docssearch"
	"github.com/go-chi/chi/v5"
)

const (
	endpointHeader = "x-overcast-endpoint"
	regionHeader   = "x-overcast-region"
)

// defaultAPIURL is the fallback endpoint used when the browser does not send
// an x-overcast-endpoint header. It is set from UIConfig.APIPort in
// NewHandler so that the BFF proxies to the correct port even when the
// emulator listens on a non-standard port.
var defaultAPIURL = "http://localhost:4566"

var bffHTTPClient = &http.Client{Timeout: 30 * time.Second}

// bffStreamingClient is used for long-lived streaming requests (SSE) where a
// total-request timeout would kill the connection prematurely.
var bffStreamingClient = &http.Client{}

// UIConfig is injected into the served index.html so the bundled SPA knows
// where to reach the emulator API without any client-side guessing.
type UIConfig struct {
	// APIPort is the port the emulator API is listening on (default 4566).
	APIPort int
	// Region is the default AWS region the emulator advertises.
	Region string
	// Debug indicates whether OVERCAST_DEBUG is enabled for the emulator.
	Debug bool
}

// NewHandler returns an http.Handler that mounts all BFF routes under /api/
// and serves the embedded SPA for everything else.
//
// staticFS must be rooted at the dist directory (files accessible as "index.html",
// "assets/...", etc.). docsFS must be rooted at the published docs directory
// (files accessible as "services/s3.md", "cdk/local-vpc.md", etc.).
//
// cfg is injected into every served index.html so the SPA can reach the API
// without user configuration. Pass a zero value from dev/test callers that
// don't embed the UI.
func NewHandler(staticFS, docsFS fs.FS, cfg UIConfig) http.Handler {
	if cfg.APIPort > 0 {
		defaultAPIURL = fmt.Sprintf("http://localhost:%d", cfg.APIPort)
	}

	r := chi.NewRouter()
	r.Use(corsMiddleware)

	// ── Simple JSON proxies ────────────────────────────────────────────────
	r.Get("/api/health", proxyJSONHandler("/_health"))
	r.Get("/api/metrics", proxyJSONHandler("/_metrics"))
	r.Get("/api/topology", handleTopology)
	r.Get("/api/debug/state", handleDebugState)
	r.Get("/api/debug/state/*", handleDebugNamespace)
	r.Get("/api/lambda/runtimes", proxyJSONHandler("/_lambda/runtimes"))
	r.Get("/api/lambda/instances", handleLambdaInstances)
	r.Get("/api/lambda/functions/{name}/source", handleLambdaSourceGet)
	r.Put("/api/lambda/functions/{name}/source", handleLambdaSourcePut)
	r.Post("/api/lambda/functions/{name}/invoke-with-progress", handleLambdaInvoke)
	r.Get("/api/lambda/functions/{name}/test-events", handleLambdaTestEventsGet)
	r.Put("/api/lambda/functions/{name}/test-events/{eventName}", handleLambdaTestEventPut)
	r.Delete("/api/lambda/functions/{name}/test-events/{eventName}", handleLambdaTestEventDelete)
	r.Get("/api/ecs/tasks/{taskArn}/logs/{container}", handleECSTaskLogs)
	r.Get("/api/ecs/clusters/{cluster}/tasks", handleECSClusterTasks)
	r.Get("/api/mail/messages", handleMailList)
	r.Get("/api/mail/messages/{id}", handleMailGet)
	r.Delete("/api/mail/messages", handleMailDeleteAll)
	r.Delete("/api/mail/messages/{id}", handleMailDeleteOne)
	r.Get("/api/inbox/messages", handleMailList)
	r.Get("/api/inbox/messages/{id}", handleMailGet)
	r.Delete("/api/inbox/messages", handleMailDeleteAll)
	r.Delete("/api/inbox/messages/{id}", handleMailDeleteOne)
	r.Get("/api/rds/instances/{id}/logs", handleRDSLogs)

	// ── SSE proxy ─────────────────────────────────────────────────────────
	r.Get("/api/events", handleEvents)

	// ── S3 routes ─────────────────────────────────────────────────────────
	r.Get("/api/s3/buckets/{bucket}/objects/{key:.+}/download", handleS3Download)
	r.Put("/api/s3/buckets/{bucket}/objects/{key:.+}", handleS3Upload)
	r.Delete("/api/s3/buckets/{bucket}/objects-by-prefix", handleS3BulkDelete)

	// ── SQS routes ────────────────────────────────────────────────────────
	r.Get("/api/sqs/queues/{name}/messages", handleSQSPeek)

	// ── Docs ──────────────────────────────────────────────────────────────
	r.Get("/api/docs/search", handleDocsSearch)
	r.Get("/api/docs/page", handleDocsPage(docsFS))
	r.Get("/api/docs/{service}", handleDocs(docsFS))

	// ── SPA fallback ──────────────────────────────────────────────────────
	r.NotFound(spaHandlerFunc(staticFS, cfg))
	r.Get("/*", spaHandlerFunc(staticFS, cfg))

	return r
}

// ── Middleware ─────────────────────────────────────────────────────────────

var allowedOrigins = map[string]bool{
	"http://localhost:3000":  true,
	"https://localhost:3000": true,
	"http://localhost:5173":  true,
	"https://localhost:5173": true,
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowedOrigins[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, x-overcast-endpoint, x-overcast-region")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ── Helpers ────────────────────────────────────────────────────────────────

// resolveEndpoint returns the emulator endpoint URL derived from the
// x-overcast-endpoint request header. Falls back to the configured API
// URL (default http://localhost:4566, overridden from UIConfig.APIPort).
func resolveEndpoint(r *http.Request) string {
	if ep := r.Header.Get(endpointHeader); ep != "" {
		return strings.TrimRight(ep, "/")
	}
	return defaultAPIURL
}

// resolveEndpointQP is like resolveEndpoint but also checks query parameters.
// This is needed for routes where the browser cannot send custom headers:
// <a> download links (navigational GET with no header control) and
// EventSource connections (which don't support custom headers).
func resolveEndpointQP(r *http.Request) string {
	if ep := r.Header.Get(endpointHeader); ep != "" {
		return strings.TrimRight(ep, "/")
	}
	if ep := r.URL.Query().Get(endpointHeader); ep != "" {
		return strings.TrimRight(ep, "/")
	}
	if ep := r.URL.Query().Get("ep"); ep != "" {
		return strings.TrimRight(ep, "/")
	}
	return defaultAPIURL
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func copyResponseBody(w http.ResponseWriter, src io.Reader) bool {
	if _, err := io.Copy(w, src); err != nil {
		return false
	}
	return true
}

// escapeKeySegments URL-escapes each segment of a key path, preserving "/"
// separators.  This matches how S3 path-style URLs represent object keys:
// "/" in the key becomes a literal "/" in the URL, not "%2F".
func escapeKeySegments(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func doGet(ctx context.Context, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	return bffHTTPClient.Do(req)
}

func doGetWithRegion(ctx context.Context, u string, incoming *http.Request) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	forwardRegion(req, incoming)
	return bffHTTPClient.Do(req)
}

func doPost(ctx context.Context, u, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	return bffHTTPClient.Do(req)
}

//nolint:unused // Kept for BFF routes that proxy Query-protocol form posts.
func doPostForm(ctx context.Context, u string, form url.Values) (*http.Response, error) {
	return doPost(ctx, u, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
}

// proxyJSONHandler proxies the request to the emulator's internal path and
// copies the JSON response verbatim. Used for simple pass-through routes.
func proxyJSONHandler(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ep := resolveEndpoint(r)
		resp, err := doGet(r.Context(), ep+path)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, "emulator unreachable")
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		if !copyResponseBody(w, resp.Body) {
			return
		}
	}
}

func handleDebugState(w http.ResponseWriter, r *http.Request) {
	proxyDebugState(w, r, "/_debug/state")
}

func handleDebugNamespace(w http.ResponseWriter, r *http.Request) {
	namespace := strings.TrimPrefix(r.URL.EscapedPath(), "/api/debug/state/")
	proxyDebugState(w, r, "/_debug/state/"+namespace)
}

func proxyDebugState(w http.ResponseWriter, r *http.Request, path string) {
	ep := resolveEndpoint(r)
	debugURL := ep + path
	if r.URL.RawQuery != "" {
		debugURL += "?" + r.URL.RawQuery
	}
	resp, err := doGet(r.Context(), debugURL)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "debug state fetch failed")
		return
	}
	defer resp.Body.Close()

	selectedKey := r.URL.Query().Get("key")
	if resp.StatusCode == http.StatusNotFound && selectedKey == "" {
		writeDebugDisabledError(w)
		return
	}
	if selectedKey != "" {
		copyDebugResponse(w, resp)
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		writeJSONError(w, http.StatusBadGateway, "debug state fetch failed")
		return
	}

	copyDebugResponse(w, resp)
}

func copyDebugResponse(w http.ResponseWriter, resp *http.Response) {
	if contentType := resp.Header.Get("Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(resp.StatusCode)
	if !copyResponseBody(w, resp.Body) {
		return
	}
}

func writeDebugDisabledError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	json.NewEncoder(w).Encode(struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}{
		Error:   "DebugDisabled",
		Message: "OVERCAST_DEBUG must be enabled to inspect raw state.",
	})
}

// spaHandlerFunc serves static files from staticFS; unmatched paths fall back
// to index.html for client-side routing. When serving index.html it injects a
// <script>window.__OVERCAST__ = {...}</script> tag so the bundled SPA can
// reach the API without user configuration.
func spaHandlerFunc(staticFS fs.FS, cfg UIConfig) http.HandlerFunc {
	fileServer := http.FileServer(http.FS(staticFS))
	return func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		// Only index.html gets the config injection — other assets are hashed
		// and cached aggressively, so we serve them verbatim via FileServer.
		if p == "index.html" {
			serveIndexHTML(w, r, staticFS, cfg)
			return
		}
		f, err := staticFS.Open(p)
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// Client-side route — serve index.html
		serveIndexHTML(w, r, staticFS, cfg)
	}
}

// indexHeadClose matches </head> (case-insensitive). We insert the bootstrap
// script immediately before it so it executes before any bundled JS runs.
var indexHeadClose = regexp.MustCompile(`(?i)</head>`)

func serveIndexHTML(w http.ResponseWriter, r *http.Request, staticFS fs.FS, cfg UIConfig) {
	raw, err := fs.ReadFile(staticFS, "index.html")
	if err != nil {
		http.Error(w, "index.html not found", http.StatusInternalServerError)
		return
	}

	bootstrap := buildBootstrapScript(r, cfg)
	body := indexHeadClose.ReplaceAll(raw, []byte(bootstrap+"</head>"))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// index.html varies by request Host (for the bridge-vs-direct API URL) so
	// disable caching — the hashed bundle files it references are still cached.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	w.Write(body) //nolint:errcheck
}

// buildBootstrapScript returns a <script> tag that sets window.__OVERCAST__
// based on the incoming request's Host header. The derived apiBaseUrl is:
//
//   - Host "overcast-app.local"      → "http://overcast.local"   (bridge pairing)
//   - Host "<host>:<uiPort>"         → "http://<host>:<APIPort>"
//   - Host "<host>" (no port)        → "http://<host>"            (port 80 via bridge)
func buildBootstrapScript(r *http.Request, cfg UIConfig) string {
	apiBaseURL := deriveAPIBaseURL(r, cfg)
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}
	payload, _ := json.Marshal(map[string]any{
		"apiBaseUrl": apiBaseURL,
		"region":     region,
		"debug":      cfg.Debug,
	})
	return `<script>window.__OVERCAST__=` + string(payload) + `;</script>`
}

func deriveAPIBaseURL(r *http.Request, cfg UIConfig) string {
	host := r.Host
	if host == "" {
		host = "localhost"
	}
	// The bridge serves the UI under overcast-app.local and the API under
	// overcast.local on port 80. Pair them explicitly.
	if strings.EqualFold(host, "overcast-app.local") {
		return "http://overcast.local"
	}
	// Split host:port so we can swap the port for the API port.
	hostname := host
	hasPort := false
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = h
		hasPort = true
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if !hasPort {
		// Access came through a port-80 proxy (bridge) on an arbitrary host;
		// assume the paired API is also reachable on the same host at port 80.
		return scheme + "://" + hostname
	}
	apiPort := cfg.APIPort
	if apiPort == 0 {
		apiPort = 4566
	}
	return scheme + "://" + hostname + ":" + strconv.Itoa(apiPort)
}

// ── Route handlers ─────────────────────────────────────────────────────────

func handleTopology(w http.ResponseWriter, r *http.Request) {
	ep := resolveEndpoint(r)
	qs := ""
	if region := r.URL.Query().Get("region"); region != "" {
		qs = "?region=" + url.QueryEscape(region)
	}
	resp, err := doGet(r.Context(), ep+"/_topology"+qs)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "topology fetch failed")
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if !copyResponseBody(w, resp.Body) {
		return
	}
}

// handleEvents proxies the SSE stream from /_events. Supports endpoint and
// region via query params (ep, region) in addition to headers, because
// browsers cannot send custom headers with EventSource.
func handleEvents(w http.ResponseWriter, r *http.Request) {
	ep := resolveEndpointQP(r)

	upstream := ep + "/_events"
	if sources := r.URL.Query()["source"]; len(sources) > 0 {
		q := url.Values{}
		for _, s := range sources {
			q.Add("source", s)
		}
		upstream += "?" + q.Encode()
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upstream, nil)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := bffStreamingClient.Do(req)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "event stream unavailable")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		writeJSONError(w, http.StatusBadGateway, "event stream unavailable")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, canFlush := w.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			if canFlush {
				flusher.Flush()
			}
		}
		if readErr != nil {
			return
		}
	}
}

// ── S3 ─────────────────────────────────────────────────────────────────────

func handleS3Download(w http.ResponseWriter, r *http.Request) {
	ep := resolveEndpointQP(r)
	bucket := chi.URLParam(r, "bucket")
	key := chi.URLParam(r, "key")

	// The chi param is raw (URL-encoded) from the request path.
	// Unescape to the real key, then escape each path segment individually
	// so that "/" separators are preserved.  url.PathEscape would encode
	// "/" to "%2F", but S3 path-style URLs use actual "/" as the key
	// hierarchy delimiter.
	realKey, _ := url.PathUnescape(key)
	resp, err := doGet(r.Context(), fmt.Sprintf("%s/%s/%s", ep, bucket, escapeKeySegments(realKey)))
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "emulator unreachable")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		w.WriteHeader(resp.StatusCode)
		if !copyResponseBody(w, resp.Body) {
			return
		}
		return
	}

	filename := realKey
	if i := strings.LastIndex(realKey, "/"); i >= 0 {
		filename = realKey[i+1:]
	}

	copyHeader := func(dst, src string) {
		if v := resp.Header.Get(src); v != "" {
			w.Header().Set(dst, v)
		}
	}
	copyHeader("Content-Type", "Content-Type")
	copyHeader("Content-Length", "Content-Length")
	copyHeader("ETag", "ETag")
	copyHeader("Last-Modified", "Last-Modified")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))

	if !copyResponseBody(w, resp.Body) {
		return
	}
}

func handleS3Upload(w http.ResponseWriter, r *http.Request) {
	ep := resolveEndpoint(r)
	bucket := chi.URLParam(r, "bucket")
	key := chi.URLParam(r, "key")

	realKey, _ := url.PathUnescape(key)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPut,
		fmt.Sprintf("%s/%s/%s", ep, bucket, escapeKeySegments(realKey)), r.Body)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Forward relevant headers
	for _, h := range []string{"Content-Type", "Content-Length", "x-amz-storage-class",
		"x-object-content-disposition", "x-object-content-encoding",
		"x-object-content-language", "x-object-cache-control", "x-object-expires"} {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	// Forward x-amz-meta-* headers
	for k, vv := range r.Header {
		if strings.HasPrefix(strings.ToLower(k), "x-amz-meta-") {
			for _, v := range vv {
				req.Header.Add(k, v)
			}
		}
	}

	resp, err := bffHTTPClient.Do(req)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "emulator unreachable")
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// S3 ListObjectsV2 and DeleteObjects XML shapes for bulk-delete pagination.

type listBucketResult struct {
	Contents              []struct{ Key string }
	IsTruncated           bool
	NextContinuationToken string
}

func handleS3BulkDelete(w http.ResponseWriter, r *http.Request) {
	ep := resolveEndpoint(r)
	bucket := chi.URLParam(r, "bucket")
	prefix := r.URL.Query().Get("prefix")
	if prefix == "" {
		writeJSONError(w, http.StatusBadRequest, "prefix query parameter is required")
		return
	}

	deleted := 0
	token := ""

	for {
		listURL := fmt.Sprintf("%s/%s?list-type=2&prefix=%s&max-keys=1000",
			ep, bucket, url.QueryEscape(prefix))
		if token != "" {
			listURL += "&continuation-token=" + url.QueryEscape(token)
		}
		resp, err := doGet(r.Context(), listURL)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, "emulator unreachable")
			return
		}
		var result listBucketResult
		if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			writeJSONError(w, http.StatusBadGateway, "listing parse error")
			return
		}
		resp.Body.Close()

		if len(result.Contents) > 0 {
			var sb strings.Builder
			sb.WriteString(`<Delete xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Quiet>true</Quiet>`)
			for _, obj := range result.Contents {
				sb.WriteString("<Object><Key>")
				if err := xml.EscapeText(&sb, []byte(obj.Key)); err != nil {
					writeJSONError(w, http.StatusInternalServerError, "failed to encode object key")
					return
				}
				sb.WriteString("</Key></Object>")
			}
			sb.WriteString("</Delete>")

			delResp, err := doPost(
				r.Context(),
				fmt.Sprintf("%s/%s?delete", ep, bucket),
				"application/xml",
				strings.NewReader(sb.String()),
			)
			if err != nil {
				writeJSONError(w, http.StatusBadGateway, "emulator unreachable")
				return
			}
			delResp.Body.Close()
			deleted += len(result.Contents)
		}

		if !result.IsTruncated {
			break
		}
		token = result.NextContinuationToken
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "deleted": deleted})
}

// ── SQS ────────────────────────────────────────────────────────────────────

type sqsMessage struct {
	MessageID               string                `json:"messageId"`
	ReceiptHandle           string                `json:"receiptHandle"`
	Body                    string                `json:"body"`
	MD5OfBody               string                `json:"md5OfBody"`
	Attributes              map[string]string     `json:"attributes"`
	MessageAttributes       map[string]sqsMsgAttr `json:"messageAttributes"`
	Inflight                bool                  `json:"inflight"`
	Delayed                 bool                  `json:"delayed"`
	VisibleAfter            float64               `json:"visibleAfter"`
	ApproximateReceiveCount int                   `json:"approximateReceiveCount"`
}

type sqsMsgAttr struct {
	DataType    string `json:"dataType"`
	StringValue string `json:"stringValue"`
}

func handleSQSPeek(w http.ResponseWriter, r *http.Request) {
	ep := resolveEndpoint(r)
	name := chi.URLParam(r, "name")

	// GetQueueUrl via SQS JSON protocol (the emulator uses X-Amz-Target dispatch).
	body := `{"QueueName":"` + name + `"}`
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, ep+"/", strings.NewReader(body))
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "emulator unreachable")
		return
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.GetQueueUrl")
	forwardRegion(req, r)

	resp, err := bffHTTPClient.Do(req)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "emulator unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()

	// Read the body once so we can use it for both error and success paths.
	respBody, _ := io.ReadAll(resp.Body)

	// If the emulator returned an error, forward it instead of silently masking it.
	if resp.StatusCode != http.StatusOK {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(respBody) //nolint:errcheck
		return
	}

	var queueURLResult struct {
		QueueUrl string `json:"QueueUrl"`
	}
	if err := json.Unmarshal(respBody, &queueURLResult); err != nil {
		writeJSONError(w, http.StatusBadGateway, "queue URL parse error: "+string(respBody))
		return
	}

	queueURL := queueURLResult.QueueUrl
	if queueURL == "" {
		writeJSONError(w, http.StatusNotFound, "queue not found: GetQueueUrl returned empty (body: "+string(respBody)+")")
		return
	}

	// Peek: GET the queue URL on the emulator endpoint (not the returned host).
	peekPath := func() string {
		u, err := url.Parse(queueURL)
		if err != nil {
			return queueURL
		}
		return ep + u.Path
	}()

	peekResp, err := doGetWithRegion(r.Context(), peekPath, r)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "emulator unreachable")
		return
	}
	defer peekResp.Body.Close()

	var raw struct {
		Messages []struct {
			MessageId         string
			ReceiptHandle     string
			Body              string
			MD5OfBody         string
			Attributes        map[string]string
			MessageAttributes map[string]struct {
				DataType    string
				StringValue string
			}
			Inflight                bool
			Delayed                 bool
			VisibleAfter            float64
			ApproximateReceiveCount int
		}
	}
	if err := json.NewDecoder(peekResp.Body).Decode(&raw); err != nil {
		writeJSONError(w, http.StatusBadGateway, "message parse error")
		return
	}

	messages := make([]sqsMessage, 0, len(raw.Messages))
	for _, m := range raw.Messages {
		attrs := make(map[string]sqsMsgAttr, len(m.MessageAttributes))
		for k, v := range m.MessageAttributes {
			attrs[k] = sqsMsgAttr{DataType: v.DataType, StringValue: v.StringValue}
		}
		messages = append(messages, sqsMessage{
			MessageID:               m.MessageId,
			ReceiptHandle:           m.ReceiptHandle,
			Body:                    m.Body,
			MD5OfBody:               m.MD5OfBody,
			Attributes:              m.Attributes,
			MessageAttributes:       attrs,
			Inflight:                m.Inflight,
			Delayed:                 m.Delayed,
			VisibleAfter:            m.VisibleAfter,
			ApproximateReceiveCount: m.ApproximateReceiveCount,
		})
	}
	if messages == nil {
		messages = []sqsMessage{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"messages": messages})
}

// ── Lambda ─────────────────────────────────────────────────────────────────

func handleLambdaInstances(w http.ResponseWriter, r *http.Request) {
	ep := resolveEndpoint(r)
	resp, err := doGet(r.Context(), ep+"/_lambda/instances")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"instances": []any{}})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"instances": []any{}})
		return
	}

	var raw struct {
		Instances []map[string]any `json:"instances"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"instances": []any{}})
		return
	}

	for i, inst := range raw.Instances {
		if _, ok := inst["instanceId"]; !ok {
			if id, ok := inst["id"]; ok {
				raw.Instances[i]["instanceId"] = id
			} else {
				raw.Instances[i]["instanceId"] = ""
			}
		}
	}
	if raw.Instances == nil {
		raw.Instances = []map[string]any{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"instances": raw.Instances})
}

// forwardRegion copies the X-Overcast-Region header from the incoming BFF
// request to the outgoing upstream request. Without this, the emulator's Region
// middleware falls back to its configured default, causing region-scoped store
// lookups to miss when the client is using a non-default region.
func forwardRegion(upstream *http.Request, incoming *http.Request) {
	if region := incoming.Header.Get(regionHeader); region != "" {
		upstream.Header.Set("X-Overcast-Region", region)
	}
}

func handleLambdaSourceGet(w http.ResponseWriter, r *http.Request) {
	ep := resolveEndpoint(r)
	name := chi.URLParam(r, "name")
	qs := ""
	if f := r.URL.Query().Get("file"); f != "" {
		qs = "?file=" + url.QueryEscape(f)
	}
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet,
		fmt.Sprintf("%s/2015-03-31/functions/%s/source%s", ep, url.PathEscape(name), qs), nil)
	forwardRegion(req, r)
	resp, err := bffHTTPClient.Do(req)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "emulator unreachable")
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if !copyResponseBody(w, resp.Body) {
		return
	}
}

func handleLambdaSourcePut(w http.ResponseWriter, r *http.Request) {
	ep := resolveEndpoint(r)
	name := chi.URLParam(r, "name")
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodPut,
		fmt.Sprintf("%s/2015-03-31/functions/%s/source", ep, url.PathEscape(name)), r.Body)
	req.Header.Set("Content-Type", "application/json")
	forwardRegion(req, r)
	resp, err := bffHTTPClient.Do(req)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "emulator unreachable")
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if !copyResponseBody(w, resp.Body) {
		return
	}
}

func handleLambdaInvoke(w http.ResponseWriter, r *http.Request) {
	ep := resolveEndpoint(r)
	name := chi.URLParam(r, "name")
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost,
		fmt.Sprintf("%s/2015-03-31/functions/%s/invoke-with-progress", ep, url.PathEscape(name)),
		r.Body)
	req.Header.Set("Content-Type", "application/json")
	forwardRegion(req, r)
	resp, err := bffHTTPClient.Do(req)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "emulator unreachable")
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, canFlush := w.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			if canFlush {
				flusher.Flush()
			}
		}
		if readErr != nil {
			return
		}
	}
}

func handleLambdaTestEventsGet(w http.ResponseWriter, r *http.Request) {
	ep := resolveEndpoint(r)
	name := chi.URLParam(r, "name")
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet,
		fmt.Sprintf("%s/2015-03-31/functions/%s/test-events", ep, url.PathEscape(name)), nil)
	forwardRegion(req, r)
	resp, err := bffHTTPClient.Do(req)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "emulator unreachable")
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if !copyResponseBody(w, resp.Body) {
		return
	}
}

func handleLambdaTestEventPut(w http.ResponseWriter, r *http.Request) {
	ep := resolveEndpoint(r)
	name := chi.URLParam(r, "name")
	eventName := chi.URLParam(r, "eventName")
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodPut,
		fmt.Sprintf("%s/2015-03-31/functions/%s/test-events/%s",
			ep, url.PathEscape(name), url.PathEscape(eventName)), r.Body)
	req.Header.Set("Content-Type", "application/json")
	forwardRegion(req, r)
	resp, err := bffHTTPClient.Do(req)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "emulator unreachable")
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if !copyResponseBody(w, resp.Body) {
		return
	}
}

func handleLambdaTestEventDelete(w http.ResponseWriter, r *http.Request) {
	ep := resolveEndpoint(r)
	name := chi.URLParam(r, "name")
	eventName := chi.URLParam(r, "eventName")
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodDelete,
		fmt.Sprintf("%s/2015-03-31/functions/%s/test-events/%s",
			ep, url.PathEscape(name), url.PathEscape(eventName)), nil)
	forwardRegion(req, r)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "emulator unreachable")
		return
	}
	resp.Body.Close()
	w.WriteHeader(http.StatusNoContent)
}

// ── ECS ────────────────────────────────────────────────────────────────────

func handleECSTaskLogs(w http.ResponseWriter, r *http.Request) {
	ep := resolveEndpoint(r)
	taskArn := chi.URLParam(r, "taskArn")
	container := chi.URLParam(r, "container")
	resp, err := doGet(r.Context(), fmt.Sprintf("%s/_ecs/tasks/%s/logs/%s",
		ep, url.PathEscape(taskArn), url.PathEscape(container)))
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "emulator unreachable")
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if !copyResponseBody(w, resp.Body) {
		return
	}
}

func handleECSClusterTasks(w http.ResponseWriter, r *http.Request) {
	ep := resolveEndpoint(r)
	cluster := chi.URLParam(r, "cluster")
	resp, err := doGet(r.Context(), fmt.Sprintf("%s/_ecs/clusters/%s/tasks",
		ep, url.PathEscape(cluster)))
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "emulator unreachable")
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if !copyResponseBody(w, resp.Body) {
		return
	}
}

// ── Mail ───────────────────────────────────────────────────────────────────

func handleMailList(w http.ResponseWriter, r *http.Request) {
	ep := resolveEndpoint(r)
	u := ep + "/_overcast/inbox/messages"
	if limit := r.URL.Query().Get("limit"); limit != "" {
		u += "?limit=" + url.QueryEscape(limit)
	}
	resp, err := doGet(r.Context(), u)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "mail fetch failed")
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if !copyResponseBody(w, resp.Body) {
		return
	}
}

func handleMailGet(w http.ResponseWriter, r *http.Request) {
	ep := resolveEndpoint(r)
	id := chi.URLParam(r, "id")
	resp, err := doGet(r.Context(), fmt.Sprintf("%s/_overcast/inbox/messages/%s", ep, url.PathEscape(id)))
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "mail fetch failed")
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if !copyResponseBody(w, resp.Body) {
		return
	}
}

func handleMailDeleteAll(w http.ResponseWriter, r *http.Request) {
	ep := resolveEndpoint(r)
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodDelete,
		ep+"/_overcast/inbox/messages", nil)
	resp, err := bffHTTPClient.Do(req)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "mail clear failed")
		return
	}
	resp.Body.Close()
	w.WriteHeader(http.StatusNoContent)
}

func handleMailDeleteOne(w http.ResponseWriter, r *http.Request) {
	ep := resolveEndpoint(r)
	id := chi.URLParam(r, "id")
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodDelete,
		fmt.Sprintf("%s/_overcast/inbox/messages/%s", ep, url.PathEscape(id)), nil)
	resp, err := bffHTTPClient.Do(req)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "mail delete failed")
		return
	}
	resp.Body.Close()
	w.WriteHeader(resp.StatusCode)
}

// ── RDS ────────────────────────────────────────────────────────────────────

func handleRDSLogs(w http.ResponseWriter, r *http.Request) {
	ep := resolveEndpoint(r)
	id := chi.URLParam(r, "id")
	resp, err := doGet(r.Context(), fmt.Sprintf("%s/_rds/instances/%s/logs", ep, url.PathEscape(id)))
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "emulator unreachable")
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if !copyResponseBody(w, resp.Body) {
		return
	}
}

// ── Docs ───────────────────────────────────────────────────────────────────

var safeServiceName = regexp.MustCompile(`^[a-z0-9_-]+$`)

func handleDocsSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := 10
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err == nil && parsed > 0 && parsed <= 50 {
			limit = parsed
		}
	}
	results := docssearch.Search(query, limit)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"query":   query,
		"results": results,
	})
}

func handleDocsPage(docsFS fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimSpace(r.URL.Query().Get("path"))
		if !safeDocsPath(path) {
			writeJSONError(w, http.StatusNotFound, "NotFound")
			return
		}
		content, err := fs.ReadFile(docsFS, path)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, "NotFound")
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Write(stripFrontmatter(content))
	}
}

var frontmatterDelim = []byte("---")

// stripFrontmatter removes a leading YAML frontmatter block (delimited by
// "---" lines) from doc content, as added by scripts/docs-index.go. Docs
// served without frontmatter (e.g. service reference pages) are returned
// unchanged. Malformed frontmatter (no closing delimiter) is also returned
// unchanged rather than guessed at, so a bad doc never renders truncated or
// mangled content.
//
// Line endings are handled a line at a time via IndexByte rather than a
// literal "\n---\n" search, so the result doesn't depend on the file's LF
// vs CRLF convention (checkout/editor dependent) and the function never
// allocates beyond the trailing slice it returns.
func stripFrontmatter(content []byte) []byte {
	first, rest, ok := cutLine(content)
	if !ok || !bytes.Equal(bytes.TrimRight(first, "\r"), frontmatterDelim) {
		return content
	}
	for {
		line, next, ok := cutLine(rest)
		if !ok {
			// No closing "---" found — leave content untouched.
			return content
		}
		if bytes.Equal(bytes.TrimRight(line, "\r"), frontmatterDelim) {
			return trimLeadingBlankLine(next)
		}
		rest = next
	}
}

// trimLeadingBlankLine removes a single leading newline (LF or CRLF) — the
// blank-line separator conventionally left between a frontmatter block and
// the doc body — so callers don't see a stray blank line at the top.
func trimLeadingBlankLine(b []byte) []byte {
	if bytes.HasPrefix(b, []byte("\r\n")) {
		return b[2:]
	}
	return bytes.TrimPrefix(b, []byte("\n"))
}

// cutLine splits b at the first '\n', returning the line (without the
// newline) and the remainder. ok is false if b contains no '\n', meaning
// there is no complete line left to consume.
func cutLine(b []byte) (line, rest []byte, ok bool) {
	idx := bytes.IndexByte(b, '\n')
	if idx == -1 {
		return nil, nil, false
	}
	return b[:idx], b[idx+1:], true
}

func safeDocsPath(path string) bool {
	if path == "" || strings.Contains(path, "..") || strings.HasPrefix(path, "/") || strings.HasPrefix(path, "\\") {
		return false
	}
	if path == "plans" || strings.HasPrefix(path, "plans/") {
		return false
	}
	return strings.HasSuffix(path, ".md")
}

func handleDocs(docsFS fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		service := chi.URLParam(r, "service")
		if !safeServiceName.MatchString(service) {
			writeJSONError(w, http.StatusNotFound, "NotFound")
			return
		}
		content, err := fs.ReadFile(docsFS, "services/"+service+".md")
		if err != nil {
			writeJSONError(w, http.StatusNotFound, "NotFound")
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Write(content)
	}
}
