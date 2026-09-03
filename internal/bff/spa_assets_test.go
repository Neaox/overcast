package bff

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// spaFS is testStaticFS plus one hashed bundle file, so both halves of the
// asset path — hit and miss — can be exercised.
func spaFS() fstest.MapFS {
	fsys := testStaticFS()
	fsys["assets/index-abc123.js"] = &fstest.MapFile{Data: []byte("console.log(1)\n")}
	return fsys
}

// TestSPA_servesHashedAsset is the control for the 404 case below: a file that
// exists under assets/ is still served verbatim.
func TestSPA_servesHashedAsset(t *testing.T) {
	handler := NewHandler(spaFS(), nil, UIConfig{})

	req := httptest.NewRequest(http.MethodGet, "/assets/index-abc123.js", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for an existing asset, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "console.log(1)") {
		t.Fatalf("expected the asset body, got %q", rec.Body.String())
	}
}

// TestSPA_missingAssetIs404 pins the fix for #1609's diagnostic dead end: a
// missing bundle file used to fall through to the SPA fallback and return
// index.html with a 200, so the browser reported `Unexpected token '<'` from a
// script that had "loaded" and `curl` of any asset URL succeeded whether or
// not the file was there. Nothing under assets/ is a client-side route.
func TestSPA_missingAssetIs404(t *testing.T) {
	handler := NewHandler(spaFS(), nil, UIConfig{})

	for _, path := range []string{
		"/assets/index-stale00.js",
		"/assets/event-stream.worker-stale00.js",
		"/assets/nested/style-stale00.css",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: expected 404, got %d (body %q)", path, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "<!doctype html>") {
			t.Errorf("GET %s: served index.html instead of a 404", path)
		}
	}
}

// TestSPA_clientRouteStillFallsBack guards the other side of the change: every
// path that is not under assets/ — including deep routes and ones with dots in
// them, which S3 bucket and object names routinely have — must still get
// index.html so client-side routing works on a full page load.
func TestSPA_clientRouteStillFallsBack(t *testing.T) {
	handler := NewHandler(spaFS(), nil, UIConfig{})

	for _, path := range []string{
		"/",
		"/sqs",
		"/s3/my.bucket.name",
		"/s3/my.bucket.name/objects/report.json",
		"/docs/services/sqs/operations",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: expected the SPA fallback (200), got %d", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "<!doctype html>") {
			t.Errorf("GET %s: expected index.html, got %q", path, rec.Body.String())
		}
	}
}
