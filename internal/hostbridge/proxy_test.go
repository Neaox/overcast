package hostbridge

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestNewProxy_hostMatchIsCaseInsensitive covers the bridge's own host-based
// routing decision. A hostname is case-insensitive (RFC 4343), so which backend
// a request reaches must not depend on how the caller typed it — the same rule
// the emulator's Host classification follows, and the one deriveAPIBaseURL next
// door already followed with strings.EqualFold.
//
// Sent case-sensitively, "Overcast-App.local" missed the UI branch and was
// proxied to the API instead, so the console served an emulator response.
func TestNewProxy_hostMatchIsCaseInsensitive(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("api"))
	}))
	defer api.Close()
	ui := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ui"))
	}))
	defer ui.Close()

	proxy := NewProxy(api.URL, ui.URL)

	cases := []struct{ host, want string }{
		{"overcast-app.local", "ui"},
		{"Overcast-App.local", "ui"},
		{"OVERCAST-APP.LOCAL", "ui"},
		{"overcast-app.local:80", "ui"},
		{"OVERCAST-APP.LOCAL:80", "ui"},
		{"overcast.local", "api"},
		{"OVERCAST.LOCAL", "api"},
		{"mybucket.localhost", "api"},
	}
	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://example/", nil)
			req.Host = tc.host
			rec := httptest.NewRecorder()

			proxy.ServeHTTP(rec, req)

			if got := rec.Body.String(); got != tc.want {
				t.Errorf("Host %q reached %q backend; want %q", tc.host, got, tc.want)
			}
		})
	}
}
