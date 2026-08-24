package lambda

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// runtime_api_telemetry_batching_test.go — batches and what happens when one
// is lost.
//
// Telemetry deliveries honour the subscription's buffering configuration: a
// batch is cut at maxItems, at maxBytes, or when its oldest event has waited
// timeoutMs — AWS's three bounds, with AWS's defaults and limits
// (https://docs.aws.amazon.com/lambda/latest/dg/telemetry-api.html#telemetry-api-buffering).
// A destination that loses a batch is told: the next batch cut for it opens
// with a platform.logsDropped event carrying the counts, which is also the
// shape AWS documents for a subscriber that fell behind.

// batchingDestination records every batch POSTed to it, optionally refusing
// at the transport level any POST whose body the refuse predicate matches —
// tying the refusal to a batch's content rather than to arrival order, which
// four concurrent delivery workers do not preserve.
type batchingDestination struct {
	*httptest.Server
	mu      sync.Mutex
	batches [][]map[string]any
	refuse  func(body []byte) bool
}

func newBatchingDestination(t *testing.T) *batchingDestination {
	t.Helper()
	d := &batchingDestination{}
	d.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		d.mu.Lock()
		refusing := d.refuse != nil && d.refuse(body)
		d.mu.Unlock()
		if refusing {
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Error("test server does not support hijacking")
				return
			}
			if conn, _, err := hj.Hijack(); err == nil {
				conn.Close()
			}
			return
		}
		var batch []map[string]any
		if err := json.Unmarshal(body, &batch); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		d.mu.Lock()
		d.batches = append(d.batches, batch)
		d.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(d.Close)
	return d
}

// refuseMatching makes the destination kill any POST whose body matches.
func (d *batchingDestination) refuseMatching(match func(body []byte) bool) {
	d.mu.Lock()
	d.refuse = match
	d.mu.Unlock()
}

// awaitBatch waits for a batch satisfying match and returns it.
func (d *batchingDestination) awaitBatch(t *testing.T, match func([]map[string]any) bool) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		d.mu.Lock()
		for _, batch := range d.batches {
			if match(batch) {
				d.mu.Unlock()
				return batch
			}
		}
		n := len(d.batches)
		d.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("no matching batch arrived; destination saw %d batches", n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// subscribeFunctionLogs subscribes an extension to function records with the
// given buffering fields.
func subscribeFunctionLogs(t *testing.T, addr, destURI string, buffering map[string]any) {
	t.Helper()
	extID := registerExtension(t, http.DefaultClient, addr, "batcher")
	body := map[string]any{
		"types":       []string{"function"},
		"destination": map[string]string{"protocol": "HTTP", "URI": destURI},
	}
	if buffering != nil {
		body["buffering"] = buffering
	}
	if status, resp := subscribeVia(t, addr, "/2022-07-01/telemetry", extID, body); status != http.StatusOK {
		t.Fatalf("subscribe = %d (%s)", status, resp)
	}
}

// TestTelemetryBatching_maxItemsCutsABatch pins the item bound: the 1000th
// event (the documented minimum for maxItems) cuts the batch at once, with
// every event present and in publish order — no timer involved, which is what
// makes the cut deterministic.
func TestTelemetryBatching_maxItemsCutsABatch(t *testing.T) {
	srv, addr := newTelemetryTestServer(t, logFormatText)
	dest := newBatchingDestination(t)
	// timeoutMs at the documented maximum so a timer cannot be what flushes.
	subscribeFunctionLogs(t, addr, dest.URL, map[string]any{"maxItems": 1000, "timeoutMs": 30000})

	for i := 0; i < 1000; i++ {
		srv.PublishExtensionLog("127.0.0.1", "function", fmt.Sprintf("line-%04d", i))
	}

	batch := dest.awaitBatch(t, func(b []map[string]any) bool { return len(b) == 1000 })
	if batch[0]["record"] != "line-0000" || batch[999]["record"] != "line-0999" {
		t.Errorf("batch order: first %v, last %v", batch[0]["record"], batch[999]["record"])
	}
}

// TestTelemetryBatching_maxBytesCutsABatch pins the size bound: events that
// together reach maxBytes cut the batch without waiting for the item count or
// the timer.
func TestTelemetryBatching_maxBytesCutsABatch(t *testing.T) {
	srv, addr := newTelemetryTestServer(t, logFormatText)
	dest := newBatchingDestination(t)
	subscribeFunctionLogs(t, addr, dest.URL, map[string]any{"maxBytes": 262144, "timeoutMs": 30000})

	// Four 64 KiB lines reach the 256 KiB documented minimum on the fourth.
	line := strings.Repeat("x", 64*1024)
	for i := 0; i < 4; i++ {
		srv.PublishExtensionLog("127.0.0.1", "function", line)
	}

	dest.awaitBatch(t, func(b []map[string]any) bool { return len(b) == 4 })
}

// TestTelemetryBatching_timeoutFlushesAPartialBatch pins the time bound: one
// lone event still arrives, after timeoutMs rather than never.
func TestTelemetryBatching_timeoutFlushesAPartialBatch(t *testing.T) {
	srv, addr := newTelemetryTestServer(t, logFormatText)
	dest := newBatchingDestination(t)
	subscribeFunctionLogs(t, addr, dest.URL, map[string]any{"timeoutMs": 25})

	srv.PublishExtensionLog("127.0.0.1", "function", "lonely")

	batch := dest.awaitBatch(t, func(b []map[string]any) bool { return len(b) == 1 })
	if batch[0]["record"] != "lonely" {
		t.Errorf("batch = %#v", batch)
	}
}

// TestTelemetryBatching_lostBatchIsReportedToTheSubscriber pins the drop
// contract: a destination that failed a delivery outright is told what it
// missed — the next batch opens with a platform.logsDropped event carrying
// the counts, per AWS's own record shape. The report rides the ordinary
// delivery path, so a destination that never recovers accumulates counters,
// not an amplifying stream of reports about its own drops.
func TestTelemetryBatching_lostBatchIsReportedToTheSubscriber(t *testing.T) {
	srv, addr := newTelemetryTestServer(t, logFormatText)
	dest := newBatchingDestination(t)
	subscribeFunctionLogs(t, addr, dest.URL, map[string]any{"timeoutMs": 25})

	// Every attempt to deliver the doomed event dies at the socket — however
	// many workers try, and whatever else rides its batch — so its batch is
	// dropped once the retries are exhausted.
	dest.refuseMatching(func(body []byte) bool { return strings.Contains(string(body), "doomed") })
	srv.PublishExtensionLog("127.0.0.1", "function", "doomed")
	// Let the batch cut and its retries exhaust before anything survivable
	// is published, so the doomed batch holds exactly one event and the
	// counters read one record.
	time.Sleep(time.Second)

	// The next batches must lead with the news.
	deadline := time.Now().Add(15 * time.Second)
	for {
		srv.PublishExtensionLog("127.0.0.1", "function", "survivor")
		dest.mu.Lock()
		var found []map[string]any
		for _, batch := range dest.batches {
			if len(batch) > 0 && batch[0]["type"] == "platform.logsDropped" {
				found = batch
			}
		}
		dest.mu.Unlock()
		if found != nil {
			record, _ := found[0]["record"].(map[string]any)
			if record == nil {
				t.Fatalf("logsDropped record is not an object: %#v", found[0])
			}
			if record["droppedRecords"] != float64(1) {
				t.Errorf("droppedRecords = %v, want 1", record["droppedRecords"])
			}
			if bytes, _ := record["droppedBytes"].(float64); bytes <= 0 {
				t.Errorf("droppedBytes = %v, want a positive count", record["droppedBytes"])
			}
			if reason, _ := record["reason"].(string); reason == "" {
				t.Error("the logsDropped record carries no reason")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the subscriber was never told about the dropped batch")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
