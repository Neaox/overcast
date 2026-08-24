package bff

import (
	"net/http"
	"net/http/httptest"
	"strconv"
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

// The object preview reads the first megabyte rather than the whole object and
// treats the 206 as its "truncated" signal, so Range has to survive the hop out
// and the partial-content status has to survive the hop back. With the header
// dropped the emulator answered 200 with the entire body: every preview pulled
// the whole object into the browser and then reported itself complete.
func TestHandleS3Download_forwardsRange(t *testing.T) {
	const body = "partial body" // 12 bytes

	// Keyed by the Range header the emulator should see, so a request that
	// lost or mangled it reads back the wrong answer rather than passing.
	answers := map[string]struct {
		status       int
		contentRange string
		body         string
	}{
		"":          {http.StatusOK, "", body},
		"bytes=0-3": {http.StatusPartialContent, "bytes 0-3/12", "part"},
		"bytes=-4":  {http.StatusPartialContent, "bytes 8-11/12", "body"},
	}

	tests := []struct {
		name              string
		rangeHeader       string
		wantStatus        int
		wantBody          string
		wantContentRange  string
		wantContentLength string
	}{
		{
			name:              "an unranged request is unchanged",
			rangeHeader:       "",
			wantStatus:        http.StatusOK,
			wantBody:          body,
			wantContentRange:  "",
			wantContentLength: "12",
		},
		{
			name:              "a ranged request is answered 206",
			rangeHeader:       "bytes=0-3",
			wantStatus:        http.StatusPartialContent,
			wantBody:          "part",
			wantContentRange:  "bytes 0-3/12",
			wantContentLength: "4",
		},
		{
			// A suffix range is a shape the proxy must not try to interpret:
			// it is relayed verbatim and S3 resolves it against the size.
			name:              "a suffix range reaches the emulator verbatim",
			rangeHeader:       "bytes=-4",
			wantStatus:        http.StatusPartialContent,
			wantBody:          "body",
			wantContentRange:  "bytes 8-11/12",
			wantContentLength: "4",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotRange string
			var sawRangeHeader bool
			_, restore := stubEmulatorForProxyTests(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, sawRangeHeader = r.Header["Range"]
				gotRange = r.Header.Get("Range")

				answer, ok := answers[gotRange]
				if !ok {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "text/plain")
				w.Header().Set("ETag", `"abc123"`)
				w.Header().Set("Accept-Ranges", "bytes")
				if answer.contentRange != "" {
					w.Header().Set("Content-Range", answer.contentRange)
				}
				w.Header().Set("Content-Length", strconv.Itoa(len(answer.body)))
				w.WriteHeader(answer.status)
				w.Write([]byte(answer.body))
			}))
			defer restore()

			handler := NewHandler(nil, nil, UIConfig{})
			req := httptest.NewRequest(http.MethodGet,
				"/api/s3/buckets/my-bucket/objects/history.txt/download", nil)
			if tc.rangeHeader != "" {
				req.Header.Set("Range", tc.rangeHeader)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if gotRange != tc.rangeHeader {
				t.Errorf("expected emulator Range %q, got %q", tc.rangeHeader, gotRange)
			}
			// An unranged request must arrive with no Range at all rather than
			// with an empty one, which S3 would answer 416 on.
			if tc.rangeHeader == "" && sawRangeHeader {
				t.Error("expected no Range header on the upstream request")
			}
			if rec.Code != tc.wantStatus {
				t.Fatalf("expected %d, got %d: %s", tc.wantStatus, rec.Code, rec.Body.String())
			}
			if got := rec.Body.String(); got != tc.wantBody {
				t.Errorf("expected body %q, got %q", tc.wantBody, got)
			}
			if got := rec.Header().Get("Content-Range"); got != tc.wantContentRange {
				t.Errorf("expected Content-Range %q, got %q", tc.wantContentRange, got)
			}
			if got := rec.Header().Get("Content-Length"); got != tc.wantContentLength {
				t.Errorf("expected Content-Length %q, got %q", tc.wantContentLength, got)
			}
			if got := rec.Header().Get("Accept-Ranges"); got != "bytes" {
				t.Errorf("expected Accept-Ranges bytes, got %q", got)
			}
			// The headers a whole-object download depends on stay put on the
			// ranged answer too — it is the same download route either way.
			if got := rec.Header().Get("Content-Type"); got != "text/plain" {
				t.Errorf("expected Content-Type text/plain, got %q", got)
			}
			if got := rec.Header().Get("ETag"); got != `"abc123"` {
				t.Errorf("unexpected ETag %q", got)
			}
			if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="history.txt"` {
				t.Errorf("unexpected Content-Disposition %q", got)
			}
		})
	}
}

// A range past the end of the object is answered 416 with the object's real
// size, which is how a caller learns what it should have asked for. Relaying it
// as an opaque error would lose that.
func TestHandleS3Download_relaysUnsatisfiableRange(t *testing.T) {
	_, restore := stubEmulatorForProxyTests(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes */12")
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
	}))
	defer restore()

	handler := NewHandler(nil, nil, UIConfig{})
	req := httptest.NewRequest(http.MethodGet,
		"/api/s3/buckets/my-bucket/objects/history.txt/download", nil)
	req.Header.Set("Range", "bytes=9999-")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("expected 416, got %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Range"); got != "bytes */12" {
		t.Errorf("expected Content-Range bytes */12, got %q", got)
	}
	// Nothing was served, so nothing should be offered as a download.
	if got := rec.Header().Get("Content-Disposition"); got != "" {
		t.Errorf("expected no Content-Disposition on 416, got %q", got)
	}
}
