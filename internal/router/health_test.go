package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

func TestInfoHandlerIncludesDebugFlag(t *testing.T) {
	handler := newInfoHandler(&config.Config{
		Region:    "ap-southeast-2",
		AccountID: "123456789012",
		Version:   "test-version",
		Debug:     true,
	})
	req := httptest.NewRequest(http.MethodGet, "/_/info", nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got infoResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !got.Debug {
		t.Fatalf("debug = false, want true")
	}
}

// TestInfoHandler_reportsIAMEnforcement covers the flag the web UI reads to
// tell an emulator-issued AccessDenied apart from an application bug. It is
// off unless the operator turned it on.
func TestInfoHandler_reportsIAMEnforcement(t *testing.T) {
	// Given: the default configuration
	handler := newInfoHandler(&config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/_/info", nil)
	rec := httptest.NewRecorder()

	// When: the info endpoint is called
	handler(rec, req)

	// Then: enforcement reads as off
	var got infoResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.IAMEnforce {
		t.Fatal("iam_enforce = true by default, want false")
	}

	// Given: enforcement switched on
	handler = newInfoHandler(&config.Config{EnforceIAM: true})
	rec = httptest.NewRecorder()

	// When/Then: the endpoint says so
	handler(rec, httptest.NewRequest(http.MethodGet, "/_/info", nil))
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !got.IAMEnforce {
		t.Fatal("iam_enforce = false with OVERCAST_ENFORCE_IAM set, want true")
	}
}

// TestHealthHandler_reportsAutoStateProvenance verifies that /_health's
// storage.configured field distinguishes "what was configured" from
// storage.default's "what backend is actually in effect" — the case that
// matters is Default: memory, Configured: auto, which means the
// OVERCAST_STATE=auto resolver picked memory because it found no evidence of
// persistence intent, not that anyone explicitly set OVERCAST_STATE=memory.
func TestHealthHandler_reportsAutoStateProvenance(t *testing.T) {
	cfg := &config.Config{
		Region:          "us-east-1",
		AccountID:       "000000000000",
		State:           config.StateBackendMemory,
		StateConfigured: "auto",
		StateSource:     config.StateSourceAuto,
	}
	handler := newHealthHandler(cfg, state.NewMemoryStore(), nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/_health", nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got healthResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Storage.Default != "memory" {
		t.Errorf("storage.default = %q, want %q", got.Storage.Default, "memory")
	}
	if got.Storage.Configured != "auto" {
		t.Errorf("storage.configured = %q, want %q", got.Storage.Configured, "auto")
	}
}

// TestHealthHandler_omitsConfiguredWhenNotPopulated verifies the additive
// nature of storage.configured: a Config built directly (not via
// config.Load()) leaves StateConfigured at its zero value, and the field
// must be omitted from the JSON response rather than emitted as "" — this is
// the shape every pre-existing caller of /_health (and every test built
// against tests/helpers.NewTestServer, which constructs Config directly)
// already expects, so it must not regress when this field was added.
func TestHealthHandler_omitsConfiguredWhenNotPopulated(t *testing.T) {
	cfg := &config.Config{
		Region:    "us-east-1",
		AccountID: "000000000000",
		State:     config.StateBackendMemory,
	}
	handler := newHealthHandler(cfg, state.NewMemoryStore(), nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/_health", nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	if !responseOmitsConfiguredKey(rec.Body.Bytes()) {
		t.Errorf("expected storage.configured to be omitted from the response, got %s", rec.Body.String())
	}
}

// responseOmitsConfiguredKey reports whether the raw JSON response's
// "storage" object has no "configured" key at all (as opposed to
// "configured":""), confirming the omitempty tag actually omits it.
func responseOmitsConfiguredKey(body []byte) bool {
	var raw struct {
		Storage map[string]json.RawMessage `json:"storage"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return false
	}
	_, present := raw.Storage["configured"]
	return !present
}
