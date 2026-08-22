package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// RealIP replaced chi's deprecated middleware.RealIP and must keep its
// contract: header precedence True-Client-IP > X-Real-IP > leftmost
// X-Forwarded-For, an unparseable value ignored, RemoteAddr untouched when no
// header applies. iam_enforce (aws:SourceIp) and API Gateway's sourceIp read
// the result.
func TestRealIP(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{
			name: "no headers leaves RemoteAddr alone",
			want: "10.0.0.9:51234",
		},
		{
			name:    "X-Forwarded-For takes the leftmost entry",
			headers: map[string]string{"X-Forwarded-For": "203.0.113.7, 10.0.0.1, 10.0.0.2"},
			want:    "203.0.113.7",
		},
		{
			name:    "X-Forwarded-For whitespace is tolerated",
			headers: map[string]string{"X-Forwarded-For": "  203.0.113.7  "},
			want:    "203.0.113.7",
		},
		{
			name:    "X-Real-IP beats X-Forwarded-For",
			headers: map[string]string{"X-Real-IP": "198.51.100.4", "X-Forwarded-For": "203.0.113.7"},
			want:    "198.51.100.4",
		},
		{
			name: "True-Client-IP beats both",
			headers: map[string]string{
				"True-Client-IP":  "192.0.2.10",
				"X-Real-IP":       "198.51.100.4",
				"X-Forwarded-For": "203.0.113.7",
			},
			want: "192.0.2.10",
		},
		{
			name:    "IPv6 is accepted",
			headers: map[string]string{"X-Forwarded-For": "2001:db8::1"},
			want:    "2001:db8::1",
		},
		{
			name:    "a value that is not an IP is ignored",
			headers: map[string]string{"X-Forwarded-For": "not-an-ip"},
			want:    "10.0.0.9:51234",
		},
		{
			name:    "the winning header being invalid does not fall through to the next",
			headers: map[string]string{"True-Client-IP": "garbage", "X-Forwarded-For": "203.0.113.7"},
			want:    "10.0.0.9:51234",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given a request arriving from 10.0.0.9 with the proxy headers set
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = "10.0.0.9:51234"
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}

			// When it passes through RealIP
			var seen string
			RealIP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				seen = r.RemoteAddr
			})).ServeHTTP(httptest.NewRecorder(), req)

			// Then the handler observes the expected RemoteAddr
			if seen != tc.want {
				t.Fatalf("RemoteAddr = %q, want %q", seen, tc.want)
			}
		})
	}
}
