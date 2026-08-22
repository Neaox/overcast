package middleware

import (
	"net"
	"net/http"
	"strings"
)

// RealIP rewrites r.RemoteAddr from the usual proxy headers — True-Client-IP,
// then X-Real-IP, then the leftmost X-Forwarded-For entry — so that everything
// downstream that reads RemoteAddr sees the client, not the last hop. Same
// precedence and validation as chi's middleware.RealIP, which this replaces:
// chi deprecated its copy (v5.3.0) because trusting those headers blindly is
// IP spoofing. Here that trust is the point. Overcast is not a security
// boundary, and the header is how a caller steers the source IP the emulator
// reports: a test exercises an `aws:SourceIp` policy condition (iam_enforce)
// or API Gateway's `$context.identity.sourceIp` by setting X-Forwarded-For,
// and `overcast bridge` — an httputil.ReverseProxy, which sets it — keeps the
// real client visible in request logs and traces instead of 127.0.0.1.
//
// A header that does not parse as an IP is ignored and RemoteAddr is left
// alone, as in chi. The rewritten value is a bare IP with no port, which is
// what every consumer already tolerates (they SplitHostPort with a fallback).
func RealIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ip := realIP(r); ip != "" {
			r.RemoteAddr = ip
		}
		next.ServeHTTP(w, r)
	})
}

func realIP(r *http.Request) string {
	var ip string
	if tcip := r.Header.Get("True-Client-IP"); tcip != "" {
		ip = tcip
	} else if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		ip = xrip
	} else if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ip, _, _ = strings.Cut(xff, ",")
	}
	ip = strings.TrimSpace(ip)
	if ip == "" || net.ParseIP(ip) == nil {
		return ""
	}
	return ip
}
