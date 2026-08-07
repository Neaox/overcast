package bff

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleSQSPeek_forwardsRegionHeader(t *testing.T) {
	var gotRegion string
	_, restore := stubEmulatorForProxyTests(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRegion = r.Header.Get("X-Overcast-Region")

		target := r.Header.Get("X-Amz-Target")
		if target == "AmazonSQS.GetQueueUrl" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"QueueUrl": emulatorURL(r) + "/000000000000/my-queue",
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"Messages": []any{},
		})
	}))
	defer restore()

	handler := NewHandler(nil, nil, UIConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/sqs/queues/my-queue/messages", nil)
	req.Header.Set(regionHeader, "ap-southeast-2")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotRegion != "ap-southeast-2" {
		t.Errorf("expected emulator to receive X-Overcast-Region=ap-southeast-2, got %q", gotRegion)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func emulatorURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
