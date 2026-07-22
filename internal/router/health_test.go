package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Neaox/overcast/internal/config"
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
