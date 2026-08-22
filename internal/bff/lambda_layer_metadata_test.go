package bff

// lambda_layer_metadata_test.go — GET /api/lambda/layers/{layerName}/versions/{version}/metadata.
//
// Found missing from the Go BFF during the dev-BFF-consolidation audit
// (#1104): the Node dev server's mirror called the emulator's
// /_overcast/lambda/layers/.../metadata endpoint directly, so the layer
// detail page worked under `pnpm dev` but 404'd in a built binary. Proxying
// the dev server to this handler instead of re-implementing it fixes both at
// once — this test pins the one side of that fix that lives in Go.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLambdaLayerMetadata_proxiesPathAndRegion(t *testing.T) {
	var gotPath, gotRegion string
	_, restore := stubEmulatorForProxyTests(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotRegion = r.Header.Get("X-Overcast-Region")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"layerName":"deps","version":1,"hasExternalExtensions":false,"externalExtensions":[]}`))
	}))
	defer restore()

	req := httptest.NewRequest(http.MethodGet, "/api/lambda/layers/deps/versions/1/metadata", nil)
	req.Header.Set("X-Overcast-Region", "ap-southeast-2")
	rec := httptest.NewRecorder()
	NewHandler(nil, nil, UIConfig{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/_overcast/lambda/layers/deps/versions/1/metadata" {
		t.Errorf("proxied to %q, want the emulator's layer metadata path", gotPath)
	}
	if gotRegion != "ap-southeast-2" {
		t.Errorf("forwarded region = %q, want ap-southeast-2", gotRegion)
	}
	if !strings.Contains(rec.Body.String(), `"layerName":"deps"`) {
		t.Errorf("body = %s, want the upstream payload passed through", rec.Body.String())
	}
}

func TestLambdaLayerMetadata_missingLayerVersionIs404(t *testing.T) {
	_, restore := stubEmulatorForProxyTests(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"ResourceNotFoundException","message":"Layer version not found: deps:9"}`))
	}))
	defer restore()

	req := httptest.NewRequest(http.MethodGet, "/api/lambda/layers/deps/versions/9/metadata", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil, nil, UIConfig{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
