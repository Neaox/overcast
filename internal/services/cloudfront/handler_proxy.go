package cloudfront

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// proxyClient is the HTTP client used for origin requests.
// It has a generous timeout; per-origin timeouts are applied via context.
var proxyClient = &http.Client{
	Timeout: 30 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse // don't follow redirects — pass through
	},
}

// ProxyRequest handles GET/HEAD/POST/PUT/DELETE/PATCH requests to
// /_cloudfront/{distId}/* and forwards them to the matched origin.
func (h *Handler) ProxyRequest(w http.ResponseWriter, r *http.Request) {
	distID := chi.URLParam(r, "distId")
	ctx := r.Context()

	// Wrap the response writer to capture status+bytes for access logging.
	rec := newResponseRecorder(w)
	w = rec

	var logCfg *DistributionConfig
	start := h.clk.Now()
	defer func() {
		if logCfg == nil {
			return
		}
		cacheStatus := "Miss"
		if xc := rec.Header().Get("X-Cache"); xc != "" {
			switch {
			case strings.Contains(xc, "Hit"):
				cacheStatus = "Hit"
			case strings.Contains(xc, "Error"):
				cacheStatus = "Error"
			}
		}
		h.writeAccessLog(logCfg, distID, r, rec.statusCode, rec.bytesWritten, cacheStatus, h.clk.Now().Sub(start))
	}()

	dist, aerr := h.requireDistribution(r, distID)
	if aerr != nil {
		http.Error(w, fmt.Sprintf("Distribution %q not found", distID), http.StatusBadGateway)
		return
	}

	if dist.Status != "Deployed" || !dist.DistributionConfig.Enabled {
		http.Error(w, "Distribution is not active", http.StatusServiceUnavailable)
		return
	}

	cfg := &dist.DistributionConfig
	logCfg = cfg

	// Continuous deployment: if this distribution has a CDP, potentially
	// route to the staging distribution based on the traffic weight.
	if cfg.ContinuousDeploymentPolicyId != "" {
		if stagingDistID := h.resolveStagingTarget(r.Context(), cfg, r); stagingDistID != "" {
			stagingDist, err := h.store.GetDistribution(r.Context(), stagingDistID)
			if err == nil && stagingDist != nil && stagingDist.DistributionConfig.Enabled {
				// Reuse the rest of ProxyRequest logic with staging dist config.
				// The simplest approach: rewrite distID and cfg, then continue.
				distID = stagingDistID
				cfg = &stagingDist.DistributionConfig
				logCfg = cfg
			}
		}
	}

	// Extract the downstream path (everything after /_cloudfront/{distId}).
	reqPath := chi.URLParam(r, "*")
	if reqPath == "" || reqPath[0] != '/' {
		reqPath = "/" + reqPath
	}

	// Apply DefaultRootObject when path is exactly "/".
	if reqPath == "/" && cfg.DefaultRootObject != "" {
		root := cfg.DefaultRootObject
		if root[0] != '/' {
			root = "/" + root
		}
		reqPath = root
	}

	// Match the request path against CacheBehaviors, fall back to DefaultCacheBehavior.
	targetOriginID := cfg.DefaultCacheBehavior.TargetOriginId
	viewerProtoPolicy := cfg.DefaultCacheBehavior.ViewerProtocolPolicy
	behaviorFAs := cfg.DefaultCacheBehavior.FunctionAssociations
	if cfg.CacheBehaviors != nil {
		for _, cb := range cfg.CacheBehaviors.Items {
			if matchPathPattern(cb.PathPattern, reqPath) {
				targetOriginID = cb.TargetOriginId
				viewerProtoPolicy = cb.ViewerProtocolPolicy
				behaviorFAs = cb.FunctionAssociations
				break
			}
		}
	}

	// ViewerProtocolPolicy enforcement.
	//
	// Enforced only when this server can actually answer over HTTPS. Overcast
	// listens for TLS only when OVERCAST_TLS_CERT and OVERCAST_TLS_KEY are both
	// set; without them, "redirect-to-https" would send a browser to a TLS
	// handshake against a plain-HTTP listener, and "https-only" would make the
	// distribution permanently unreachable — neither is a condition any client
	// can satisfy. Real AWS always serves HTTPS, so both are satisfiable there;
	// pointing a caller at a scheme the emulator does not serve is the worse
	// divergence, the same call docs/plans/host-routing-precedence.md §8 records
	// for AppSync's uris map.
	if isHTTPSViewerPolicy(viewerProtoPolicy) && !h.requestIsHTTPS(r) {
		if h.cfg != nil && h.cfg.TLSEnabled() {
			if viewerProtoPolicy == "https-only" {
				http.Error(w, "HTTPS is required", http.StatusForbidden)
				return
			}
			// Fold the Host into the Location we mint: a redirect target is
			// output, and output carries the canonical lowercase form.
			http.Redirect(w, r, "https://"+serviceutil.FoldHostname(r.Host)+r.RequestURI, http.StatusMovedPermanently)
			return
		}
		h.warnViewerPolicyNotEnforceable(distID, viewerProtoPolicy)
	}

	// Geo-restriction enforcement.
	if cfg.Restrictions != nil {
		rt := cfg.Restrictions.GeoRestriction.RestrictionType
		if rt == "whitelist" || rt == "blacklist" {
			clientCountry := r.Header.Get("CloudFront-Viewer-Country")
			if clientCountry == "" {
				clientCountry = r.Header.Get("CF-IPCountry") // fallback
			}
			if clientCountry != "" {
				inList := false
				for _, loc := range cfg.Restrictions.GeoRestriction.Items {
					if strings.EqualFold(loc, clientCountry) {
						inList = true
						break
					}
				}
				if rt == "whitelist" && !inList {
					w.Header().Set("X-Cache", "Error from cloudfront")
					http.Error(w, "Access denied by geo-restriction", http.StatusForbidden)
					return
				}
				if rt == "blacklist" && inList {
					w.Header().Set("X-Cache", "Error from cloudfront")
					http.Error(w, "Access denied by geo-restriction", http.StatusForbidden)
					return
				}
			}
			// If no country header is present, allow through (local dev — no real IP geolocation).
		}
	}

	// Run viewer-request CloudFront Functions.
	domainName := distID + ".cloudfront.net"
	if fnResult, fnErr := h.runViewerRequest(r, distID, domainName, reqPath, behaviorFAs); fnResult != nil && fnErr == nil {
		if fnResult.isResponse {
			w.Header().Set("X-Amz-Cf-Pop", "DEV-P1")
			w.Header().Set("X-Amz-Cf-Id", distID)
			w.Header().Set("X-Cache", "FunctionGeneratedResponse")
			for k, v := range fnResult.headers {
				w.Header().Set(k, v)
			}
			w.WriteHeader(fnResult.statusCode)
			return
		}
		// Apply URI changes from the function.
		if fnResult.uri != "" {
			reqPath = fnResult.uri
		}
	}

	// Cache check for GET/HEAD requests.
	cacheable := r.Method == http.MethodGet || r.Method == http.MethodHead
	var cacheKey string
	if cacheable {
		cacheKey = proxyCacheKey(distID, reqPath, r)
		if entry := h.cache.get(cacheKey); entry != nil {
			for k, vals := range entry.headers {
				for _, v := range vals {
					w.Header().Add(k, v)
				}
			}
			w.Header().Set("X-Amz-Cf-Pop", "DEV-P1")
			w.Header().Set("X-Amz-Cf-Id", distID)
			w.Header().Set("Via", fmt.Sprintf("1.1 %s.cloudfront.net (CloudFront)", distID))
			w.Header().Set("X-Cache", "Hit from cloudfront")
			w.WriteHeader(entry.statusCode)
			_, _ = w.Write(entry.body)
			return
		}
	}

	// Resolve the target. A behavior's TargetOriginId names either an origin or
	// an origin GROUP; a group resolves to its members in failover order.
	candidates, failoverCodes := resolveOriginTargets(cfg, targetOriginID)
	if len(candidates) == 0 {
		h.log.Error("origin not found in distribution config",
			zap.String("distId", distID),
			zap.String("targetOriginId", targetOriginID),
		)
		http.Error(w, "Origin not found", http.StatusBadGateway)
		return
	}

	resp, originURL, err := h.dispatchToOrigins(ctx, r, reqPath, candidates, failoverCodes)
	if err != nil {
		h.log.Error("origin request failed",
			zap.String("originURL", originURL),
			zap.Error(err),
		)
		http.Error(w, "Origin request failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Custom error responses — check before copying headers.
	if cfg.CustomErrorResponses != nil && resp.StatusCode >= 400 {
		for _, cer := range cfg.CustomErrorResponses.Items {
			if cer.ErrorCode == resp.StatusCode && cer.ResponseCode != "" {
				respCode, convErr := strconv.Atoi(cer.ResponseCode)
				if convErr != nil {
					respCode = cer.ErrorCode
				}
				w.Header().Set("X-Amz-Cf-Pop", "DEV-P1")
				w.Header().Set("X-Amz-Cf-Id", distID)
				w.Header().Set("Content-Type", "text/plain")
				w.Header().Set("X-Cache", "Error from cloudfront")
				w.WriteHeader(respCode)
				fmt.Fprintf(w, "Error %d: %s\n", cer.ErrorCode, cer.ResponsePagePath)
				return
			}
		}
	}

	// Extract cache tags from the origin response before viewer-response
	// functions may modify headers, matching real CloudFront behavior.
	var cachedTags []string
	if cfg.CacheTagConfig != nil && cfg.CacheTagConfig.HeaderName != "" {
		for k, vals := range resp.Header {
			if strings.EqualFold(k, cfg.CacheTagConfig.HeaderName) {
				for _, v := range vals {
					cachedTags = append(cachedTags, parseCacheTags(v)...)
				}
				break
			}
		}
	}

	// Run viewer-response CloudFront Functions (may modify headers).
	respHeaders := copyHeaders(resp.Header)
	h.runViewerResponse(r, distID, domainName, reqPath, behaviorFAs, resp.StatusCode, respHeaders)

	// Copy (possibly modified) origin response headers.
	for k, vals := range respHeaders {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}

	// Add CloudFront-specific headers.
	w.Header().Set("X-Amz-Cf-Pop", "DEV-P1")
	w.Header().Set("X-Amz-Cf-Id", distID)
	w.Header().Set("Via", fmt.Sprintf("1.1 %s.cloudfront.net (CloudFront)", distID))
	w.Header().Set("X-Cache", "Miss from cloudfront")

	// For cacheable GET requests with 2xx responses, buffer the body so we can
	// store it in the cache; for all other cases stream directly.
	const maxProxyResponseBytes = 100 * 1024 * 1024 // 100 MB
	if r.Method == http.MethodGet && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, maxProxyResponseBytes))
		if readErr != nil {
			h.log.Error("failed to read origin response body", zap.Error(readErr))
			http.Error(w, "Failed to read origin response", http.StatusBadGateway)
			return
		}
		ttl := h.cacheTTL(ctx, cfg, reqPath)

		h.cache.set(cacheKey, &cfCacheEntry{
			statusCode: resp.StatusCode,
			headers:    copyHeaders(respHeaders),
			body:       bodyBytes,
			tags:       cachedTags,
			expiresAt:  h.clk.Now().Add(ttl),
		})
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(bodyBytes)
	} else {
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}
}

// proxyCacheKey builds a cache key for a proxy request.
// The key includes distID and the request path+query so that different query
// strings get different cache entries.
func proxyCacheKey(distID, reqPath string, r *http.Request) string {
	qs := r.URL.RawQuery
	if qs != "" {
		return distID + ":" + reqPath + "?" + qs
	}
	return distID + ":" + reqPath
}

// cacheTTL returns the TTL to use for a cached response.
// It reads from the distribution's matching CacheBehavior CachePolicyId if set,
// falling back to 86400 (24h).
func (h *Handler) cacheTTL(ctx context.Context, cfg *DistributionConfig, reqPath string) time.Duration {
	defaultTTL := int64(86400)
	cachePolicyID := cfg.DefaultCacheBehavior.CachePolicyId
	if cfg.CacheBehaviors != nil {
		for i := range cfg.CacheBehaviors.Items {
			if matchPathPattern(cfg.CacheBehaviors.Items[i].PathPattern, reqPath) {
				cachePolicyID = cfg.CacheBehaviors.Items[i].CachePolicyId
				break
			}
		}
	}
	if cachePolicyID != "" {
		if pol, err := h.store.GetCachePolicy(ctx, cachePolicyID); err == nil && pol != nil {
			if pol.CachePolicyConfig.DefaultTTL != nil {
				defaultTTL = *pol.CachePolicyConfig.DefaultTTL
			}
		}
	}
	if defaultTTL <= 0 {
		defaultTTL = 86400
	}
	return time.Duration(defaultTTL) * time.Second
}

// copyHeaders makes a deep copy of an http.Header map.
func copyHeaders(h http.Header) map[string][]string {
	out := make(map[string][]string, len(h))
	for k, v := range h {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// buildOriginURL constructs the full URL to forward to the origin.
func buildOriginURL(origin *Origin, reqPath string, emulatorPort int) string {
	domain := origin.DomainName
	originPath := strings.TrimRight(origin.OriginPath, "/")

	// S3 origins: domain ends with .s3.amazonaws.com or .s3.{region}.amazonaws.com
	// Rewrite to local emulator endpoint.
	if isS3Origin(domain) {
		bucket := extractS3Bucket(domain)
		return fmt.Sprintf("http://localhost:%d/%s%s%s", emulatorPort, bucket, originPath, reqPath)
	}

	// Custom origin — use the configured protocol/port.
	scheme := "http"
	port := 80
	if origin.CustomOriginConfig != nil {
		switch origin.CustomOriginConfig.OriginProtocolPolicy {
		case "https-only", "match-viewer":
			scheme = "https"
			port = origin.CustomOriginConfig.HTTPSPort
		default: // http-only
			port = origin.CustomOriginConfig.HTTPPort
		}
	}

	// If the domain is "localhost" or looks like an internal emulator reference,
	// keep it as-is.
	host := domain
	if port != 80 && port != 443 {
		host = fmt.Sprintf("%s:%d", domain, port)
	}

	return fmt.Sprintf("%s://%s%s%s", scheme, host, originPath, reqPath)
}

// isS3Origin checks if a domain looks like an S3 origin.
func isS3Origin(domain string) bool {
	return strings.HasSuffix(domain, ".s3.amazonaws.com") ||
		strings.Contains(domain, ".s3.") && strings.HasSuffix(domain, ".amazonaws.com")
}

// extractS3Bucket extracts the bucket name from an S3 origin domain.
// Formats: {bucket}.s3.amazonaws.com or {bucket}.s3.{region}.amazonaws.com.
func extractS3Bucket(domain string) string {
	// Remove suffix
	domain = strings.TrimSuffix(domain, ".amazonaws.com")
	// Now "{bucket}.s3" or "{bucket}.s3.{region}"
	idx := strings.Index(domain, ".s3")
	if idx > 0 {
		return domain[:idx]
	}
	return domain
}

// forwardHeaders copies relevant request headers to the outbound origin request.
func forwardHeaders(src, dst *http.Request) {
	for _, h := range []string{
		"Content-Type", "Accept", "Accept-Encoding", "Accept-Language",
		"Authorization", "Range", "If-Modified-Since", "If-None-Match",
		"Cache-Control", "Pragma", "Cookie",
	} {
		if v := src.Header.Get(h); v != "" {
			dst.Header.Set(h, v)
		}
	}
	// Forward X-Forwarded-For.
	xff := src.Header.Get("X-Forwarded-For")
	if xff != "" {
		dst.Header.Set("X-Forwarded-For", xff+", "+src.RemoteAddr)
	} else {
		dst.Header.Set("X-Forwarded-For", src.RemoteAddr)
	}
}

// matchPathPattern matches a CloudFront path pattern against a request path.
// CloudFront patterns use * as a wildcard and ? as a single-char wildcard.
func matchPathPattern(pattern, reqPath string) bool {
	// path.Match uses * and ? the same way as CloudFront.
	// However, CloudFront's * matches across slashes while path.Match's * does not.
	// We handle this by checking if the pattern has a simple prefix+suffix around *.
	if pattern == "*" || pattern == "/*" {
		return true
	}

	// For patterns like /images/* or /api/v1/*.json, use simple prefix matching
	// when the wildcard is at the end.
	if strings.HasSuffix(pattern, "/*") {
		prefix := strings.TrimSuffix(pattern, "/*")
		return strings.HasPrefix(reqPath, prefix+"/") || reqPath == prefix
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(reqPath, prefix)
	}

	// Fall back to path.Match for simple ? wildcards and exact matches.
	matched, _ := path.Match(pattern, reqPath)
	return matched
}

// resolveStagingTarget checks the continuous deployment policy for the distribution
// and returns a staging distribution ID to route to, or "" to use the primary.
func (h *Handler) resolveStagingTarget(ctx context.Context, cfg *DistributionConfig, r *http.Request) string {
	pol, err := h.store.GetContinuousDeploymentPolicy(ctx, cfg.ContinuousDeploymentPolicyId)
	if err != nil || pol == nil || !pol.ContinuousDeploymentPolicyConfig.Enabled {
		return ""
	}

	tc := pol.ContinuousDeploymentPolicyConfig.TrafficConfig
	if tc == nil {
		return ""
	}

	// Find the staging distribution ID from the policy's StagingDistributionDnsNames.
	// The ID is the first label of the DomainName — which holds for AWS's
	// "{id}.cloudfront.net" and for the "{id}.cloudfront.{host}" Overcast mints.
	stagingDNS := pol.ContinuousDeploymentPolicyConfig.StagingDistributionDnsNames
	if len(stagingDNS.Items) == 0 {
		return ""
	}
	dnsName := stagingDNS.Items[0]
	stagingDistID := distributionIDFromDomainName(dnsName)
	if stagingDistID == dnsName {
		return "" // doesn't match expected format
	}

	switch tc.Type {
	case "SingleWeight":
		if tc.SingleWeightConfig == nil {
			return ""
		}
		weight := tc.SingleWeightConfig.Weight // float64 between 0 and 1
		if rand.Float64() < weight {
			return stagingDistID
		}
	case "SingleHeader":
		if tc.SingleHeaderConfig == nil {
			return ""
		}
		if r.Header.Get(tc.SingleHeaderConfig.Header) == tc.SingleHeaderConfig.Value {
			return stagingDistID
		}
	}
	return ""
}

// HostRouteRewrite adapts a request on a distribution's real CloudFront
// hostname ({distributionId}.cloudfront.net) to the internal proxy route this
// file serves.
//
// Unlike the other host-routed services the region is absent from the grammar:
// CloudFront is global, so the address is {id}.cloudfront.net with no region
// segment. middleware.ParseHostRoute already tolerates that — Region is
// optional — so "cloudfront" needs no special parsing, only this rewrite.
//
// The ID is upper-cased because a Host is case-insensitive (RFC 3986 §3.2.2)
// and browsers lowercase it before sending, while a distribution ID is
// uppercase ("E" + 13 uppercase alphanumerics, generateDistributionID) and the
// store looks it up verbatim. Without this, pasting a minted DomainName into a
// browser 502s with `Distribution "e…" not found` while curl -H Host works.
// CloudFront is the only host-routed service whose IDs are not already
// lowercase, which is why only this rewrite needs to canonicalise.
func (s *Service) HostRouteRewrite(r *http.Request, m middleware.HostRouteMatch) {
	path := r.URL.Path
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	r.URL.Path = "/_cloudfront/" + strings.ToUpper(m.ID) + path
	if r.URL.RawPath != "" {
		r.URL.RawPath = r.URL.Path
	}
}

// isHTTPSViewerPolicy reports whether a ViewerProtocolPolicy requires the
// viewer connection to be HTTPS. "allow-all" (and any unrecognised value, which
// CreateDistribution validation should already have rejected) does not.
func isHTTPSViewerPolicy(policy string) bool {
	return policy == "https-only" || policy == "redirect-to-https"
}

// requestIsHTTPS reports whether the viewer hop was already HTTPS — either
// terminated by this server, or by a proxy in front of it that said so.
func (h *Handler) requestIsHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// warnViewerPolicyNotEnforceable explains, once per process, why a
// distribution's HTTPS viewer policy is being served as allow-all. Silence here
// would be its own trap: the policy is in the distribution config the user
// wrote, so its absence needs a reason they can find.
func (h *Handler) warnViewerPolicyNotEnforceable(distID, policy string) {
	h.tlsPolicyWarnOnce.Do(func() {
		h.log.Warn("serving an HTTPS-only viewer protocol policy over plain HTTP",
			zap.String("distributionId", distID),
			zap.String("viewerProtocolPolicy", policy),
			zap.String("reason", "this server is not serving TLS, so the policy cannot be satisfied by any client"),
			zap.String("resolution", "set OVERCAST_TLS_CERT and OVERCAST_TLS_KEY to enforce it, or use a TLS-terminating proxy that sets X-Forwarded-Proto: https"),
		)
	})
}

// resolveOriginTargets maps a cache behavior's TargetOriginId to the origins
// that may serve it, in the order they should be tried.
//
// TargetOriginId names either an Origin or an OriginGroup — validateOriginRefs
// accepts both, per AWS's origin failover documentation. An origin resolves to
// itself with no failover; a group resolves to its members in declared order,
// with the status codes its FailoverCriteria lists.
//
// A group member naming an origin that does not exist is skipped rather than
// failing the whole lookup: the remaining members are still serviceable, and a
// group with no resolvable member returns nothing, which the caller reports as
// the same "Origin not found" a bad TargetOriginId gets.
func resolveOriginTargets(cfg *DistributionConfig, targetID string) ([]*Origin, map[int]bool) {
	byID := make(map[string]*Origin, len(cfg.Origins.Items))
	for i := range cfg.Origins.Items {
		byID[cfg.Origins.Items[i].ID] = &cfg.Origins.Items[i]
	}

	if origin, ok := byID[targetID]; ok {
		return []*Origin{origin}, nil
	}

	if cfg.OriginGroups == nil {
		return nil, nil
	}
	for _, group := range cfg.OriginGroups.Items {
		if group.ID != targetID {
			continue
		}
		members := make([]*Origin, 0, len(group.Members.Items))
		for _, m := range group.Members.Items {
			if origin, ok := byID[m.OriginId]; ok {
				members = append(members, origin)
			}
		}
		codes := make(map[int]bool, len(group.FailoverCriteria.StatusCodes.Items))
		for _, c := range group.FailoverCriteria.StatusCodes.Items {
			codes[c] = true
		}
		return members, codes
	}
	return nil, nil
}

// requestCanFailOver reports whether a viewer request may be retried against
// the next member of an origin group.
//
// AWS fails over only for GET, HEAD and OPTIONS — for any other method
// CloudFront returns the primary's response as-is, because replaying a request
// that changes state is not safe. The body check enforces the same thing
// mechanically: a request whose body has already been streamed to the first
// origin cannot be replayed to the second, and ContentLength 0 is the only case
// where there is provably nothing to replay (-1 means chunked, i.e. unknown).
func requestCanFailOver(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return r.ContentLength == 0
	default:
		return false
	}
}

// dispatchToOrigins sends the viewer request to the first candidate origin,
// falling back through the rest when the response is a failover trigger.
//
// Returns the response that should be served, the URL it came from (for error
// logging), and an error only when no candidate produced a response at all.
func (h *Handler) dispatchToOrigins(
	ctx context.Context, r *http.Request, reqPath string,
	candidates []*Origin, failoverCodes map[int]bool,
) (*http.Response, string, error) {
	canFailOver := len(candidates) > 1 && requestCanFailOver(r)

	var lastErr error
	var lastURL string
	for i, origin := range candidates {
		originURL := buildOriginURL(origin, reqPath, h.cfg.Port)
		lastURL = originURL
		isLast := i == len(candidates)-1

		outReq, err := http.NewRequestWithContext(ctx, r.Method, originURL, r.Body)
		if err != nil {
			lastErr = fmt.Errorf("build origin request: %w", err)
			if isLast || !canFailOver {
				return nil, originURL, lastErr
			}
			continue
		}
		forwardHeaders(r, outReq)
		if origin.CustomHeaders != nil {
			for _, ch := range origin.CustomHeaders.Items {
				outReq.Header.Set(ch.HeaderName, ch.HeaderValue)
			}
		}

		resp, err := proxyClient.Do(outReq)
		if err != nil {
			// A connection failure is a failover trigger in its own right, not
			// only a listed status code.
			lastErr = err
			if isLast || !canFailOver {
				return nil, originURL, lastErr
			}
			h.log.Warn("origin unreachable, failing over to the next group member",
				zap.String("originId", origin.ID),
				zap.String("originURL", originURL),
				zap.Error(err),
			)
			continue
		}

		if !isLast && canFailOver && failoverCodes[resp.StatusCode] {
			resp.Body.Close()
			h.log.Warn("origin returned a failover status, trying the next group member",
				zap.String("originId", origin.ID),
				zap.Int("status", resp.StatusCode),
			)
			continue
		}
		return resp, originURL, nil
	}
	return nil, lastURL, lastErr
}
