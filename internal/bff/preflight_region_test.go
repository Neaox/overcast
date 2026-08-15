package bff

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The console asks about the region it is showing, not the region the emulator
// happens to default to. If that header is dropped the advisory compares
// us-east-1 against itself and can never fire — which looks exactly like a
// healthy setup, so nothing would ever report the omission.
func TestHandlePreflightRegion_forwardsTheSelectedRegionAndTheKind(t *testing.T) {
	var gotRegion, gotQuery string
	_, restore := stubEmulatorForProxyTests(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRegion = r.Header.Get("X-Overcast-Region")
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"kind":      "cloudformation-stacks",
			"region":    "us-east-1",
			"count":     0,
			"elsewhere": []map[string]any{{"region": "ap-southeast-2", "count": 3}},
		})
	}))
	defer restore()

	handler := NewHandler(nil, nil, UIConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/preflight/region?kind=cloudformation-stacks", nil)
	req.Header.Set(regionHeader, "us-east-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotRegion != "us-east-1" {
		t.Errorf("expected the emulator to receive X-Overcast-Region=us-east-1, got %q", gotRegion)
	}
	if gotQuery != "kind=cloudformation-stacks" {
		t.Errorf("expected the kind to reach the emulator, got query %q", gotQuery)
	}

	var got struct {
		Elsewhere []struct {
			Region string `json:"region"`
			Count  int    `json:"count"`
		} `json:"elsewhere"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Elsewhere) != 1 || got.Elsewhere[0].Region != "ap-southeast-2" || got.Elsewhere[0].Count != 3 {
		t.Errorf("expected the advisory to survive the proxy, got %+v", got.Elsewhere)
	}
}

// An unknown kind is the emulator's answer, not the proxy's to reinterpret:
// the console needs to see the 400 to know it asked for something that does
// not exist, rather than a silence indistinguishable from "no advisory".
func TestHandlePreflightRegion_passesTheEmulatorsRejectionThrough(t *testing.T) {
	_, restore := stubEmulatorForProxyTests(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "UnknownKind"})
	}))
	defer restore()

	handler := NewHandler(nil, nil, UIConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/preflight/region?kind=not-a-kind", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected the emulator's 400 to reach the console, got %d", rec.Code)
	}
}
