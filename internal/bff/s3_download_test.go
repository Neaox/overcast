package bff

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The download route backs both the Download link on a version-history row and
// the object preview, so dropping ?versionId= on the proxy hop would serve the
// current bytes under an older version's metadata.
func TestHandleS3Download_forwardsVersionID(t *testing.T) {
	tests := []struct {
		name         string
		query        string
		wantRawQuery string
		wantBody     string
	}{
		{
			name:         "unversioned request sends no versionId",
			query:        "",
			wantRawQuery: "",
			wantBody:     "current bytes",
		},
		{
			name:         "versioned request addresses that version",
			query:        "?versionId=v1",
			wantRawQuery: "versionId=v1",
			wantBody:     "v1 bytes",
		},
		{
			// "null" is the version id every object written to an unversioned
			// or suspended bucket carries — a real value, not an absent one.
			name:         "null is a real version id",
			query:        "?versionId=null",
			wantRawQuery: "versionId=null",
			wantBody:     "null bytes",
		},
		{
			name:         "version ids are escaped, not pasted",
			query:        "?versionId=a%2Bb%20c",
			wantRawQuery: "versionId=a%2Bb+c",
			wantBody:     "escaped bytes",
		},
	}

	// Keyed by the version id the emulator should see, so a request that lost
	// or mangled the parameter reads back the wrong body rather than passing.
	bodies := map[string]string{
		"":      "current bytes",
		"v1":    "v1 bytes",
		"null":  "null bytes",
		"a+b c": "escaped bytes",
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath, gotRawQuery string
			_, restore := stubEmulatorForProxyTests(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotRawQuery = r.URL.RawQuery

				body, ok := bodies[r.URL.Query().Get("versionId")]
				if !ok {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.Header().Set("Content-Type", "text/plain")
				w.Write([]byte(body))
			}))
			defer restore()

			handler := NewHandler(nil, nil, UIConfig{})
			req := httptest.NewRequest(http.MethodGet,
				"/api/s3/buckets/my-bucket/objects/history.txt/download"+tc.query, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			if gotPath != "/my-bucket/history.txt" {
				t.Errorf("expected emulator path /my-bucket/history.txt, got %q", gotPath)
			}
			if gotRawQuery != tc.wantRawQuery {
				t.Errorf("expected emulator query %q, got %q", tc.wantRawQuery, gotRawQuery)
			}
			if got := rec.Body.String(); got != tc.wantBody {
				t.Errorf("expected body %q, got %q", tc.wantBody, got)
			}
			if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="history.txt"` {
				t.Errorf("unexpected Content-Disposition %q", got)
			}
		})
	}
}

// The endpoint query parameters resolveEndpointQP reads must not leak onto the
// upstream URL — only versionId belongs there.
func TestHandleS3Download_doesNotForwardEndpointParams(t *testing.T) {
	var gotRawQuery string
	emulator, restore := stubEmulatorForProxyTests(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRawQuery = r.URL.RawQuery
		w.Write([]byte("ok"))
	}))
	defer restore()

	handler := NewHandler(nil, nil, UIConfig{})
	req := httptest.NewRequest(http.MethodGet,
		"/api/s3/buckets/my-bucket/objects/history.txt/download?ep="+emulator.URL+"&versionId=v1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotRawQuery != "versionId=v1" {
		t.Errorf("expected emulator query versionId=v1, got %q", gotRawQuery)
	}
}

// A nested key arrives percent-encoded as one path segment — that is what
// encodeURIComponent produces on the client — and must reach the emulator with
// its "/" separators restored, version and all.
func TestHandleS3Download_versionedNestedKey(t *testing.T) {
	var gotPath, gotRawQuery string
	_, restore := stubEmulatorForProxyTests(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotRawQuery = r.URL.RawQuery
		w.Write([]byte("nested"))
	}))
	defer restore()

	handler := NewHandler(nil, nil, UIConfig{})
	req := httptest.NewRequest(http.MethodGet,
		"/api/s3/buckets/my-bucket/objects/logs%2F2026%2Fhistory.txt/download?versionId=v1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/my-bucket/logs/2026/history.txt" {
		t.Errorf("expected emulator path /my-bucket/logs/2026/history.txt, got %q", gotPath)
	}
	if gotRawQuery != "versionId=v1" {
		t.Errorf("expected emulator query versionId=v1, got %q", gotRawQuery)
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="history.txt"` {
		t.Errorf("unexpected Content-Disposition %q", got)
	}
}
