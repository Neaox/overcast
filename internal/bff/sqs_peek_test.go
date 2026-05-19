package bff

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandleSQSPeek_forwardsRegionHeader verifies that the BFF peek handler
// forwards the X-Overcast-Region header from the incoming browser request
// to the upstream emulator. Without this, the emulator defaults to us-east-1
// and region-scoped GetQueueUrl lookups fail with NonExistentQueue.
func TestHandleSQSPeek_forwardsRegionHeader(t *testing.T) {
	// Given: a fake emulator that records the X-Overcast-Region it receives.
	var gotRegion string
	emulator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRegion = r.Header.Get("X-Overcast-Region")

		target := r.Header.Get("X-Amz-Target")
		if target == "AmazonSQS.GetQueueUrl" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"QueueUrl": emulatorURL(r) + "/000000000000/my-queue",
			})
			return
		}
		// Peek endpoint: return empty messages list.
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"Messages": []any{},
		})
	}))
	defer emulator.Close()

	// Swap the BFF HTTP client so it talks to our fake emulator.
	origClient := bffHTTPClient
	bffHTTPClient = emulator.Client()
	defer func() { bffHTTPClient = origClient }()

	// When: the browser sends a peek request with X-Overcast-Region: ap-southeast-2.
	handler := NewHandler(nil, nil, UIConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/sqs/queues/my-queue/messages", nil)
	req.Header.Set(endpointHeader, emulator.URL)
	req.Header.Set(regionHeader, "ap-southeast-2")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Then: the emulator receives the region header.
	if gotRegion != "ap-southeast-2" {
		t.Errorf("expected emulator to receive X-Overcast-Region=ap-southeast-2, got %q", gotRegion)
	}

	// And: the response is 200 with messages.
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
