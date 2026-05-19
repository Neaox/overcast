package hostbridge

import (
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
)

// NewProxy returns an http.Handler that routes requests to either apiAddr or
// uiAddr based on the Host header:
//
//   - overcast-app.local → uiAddr (web console)
//   - everything else   → apiAddr (emulator API + API Gateway custom domains)
func NewProxy(apiAddr, uiAddr string) http.Handler {
	apiProxy := newReverseProxy(apiAddr)
	uiProxy := newReverseProxy(uiAddr)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if host == "overcast-app.local" {
			uiProxy.ServeHTTP(w, r)
		} else {
			apiProxy.ServeHTTP(w, r)
		}
	})
}

func newReverseProxy(target string) *httputil.ReverseProxy {
	u, err := url.Parse(target)
	if err != nil {
		// Callers supply string literals; a parse error is a programming mistake.
		panic("hostbridge.NewProxy: invalid target URL: " + err.Error())
	}
	return httputil.NewSingleHostReverseProxy(u)
}
