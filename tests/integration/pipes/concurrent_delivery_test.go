// Concurrency coverage for DynamoDB-sourced pipe delivery.
//
// A burst of concurrent writes to the source table must deliver every record.
// This is the shape of the report that led to the retry/dead-letter handling in
// internal/services/pipes/delivery.go: the happy path holds under load, and
// what used to be lost was a batch whose target delivery failed, because a
// stream record has nowhere to be redelivered from. That failure path is pinned
// directly in internal/services/pipes/stream_retry_test.go; this test guards the
// load itself, which no test covered before.
package pipes_test

import (
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/tests/helpers"
)

func TestPipeDelivery_concurrentWritesLoseNoRecords(t *testing.T) {
	const writes = 50

	// Given: a DDB stream table, an SQS queue and a RUNNING pipe between them
	srv := helpers.NewTestServer(t)
	streamARN := mustCreateStreamTable(t, srv, "orders")
	queueURL, queueARN := mustCreateQueue(t, srv, "order-events")

	resp := createPipe(t, srv, "orders-to-sqs", streamARN, queueARN)
	resp.Body.Close()
	waitForRunning(t, srv, "orders-to-sqs")

	// When: many items are written concurrently
	var wg sync.WaitGroup
	for i := range writes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			putResp := ddbCall(t, srv, "PutItem", map[string]any{
				"TableName": "orders",
				"Item":      map[string]any{"id": map[string]any{"S": "order-" + strconv.Itoa(i)}},
			})
			defer putResp.Body.Close()
			if putResp.StatusCode != http.StatusOK {
				t.Errorf("PutItem %d: status %d", i, putResp.StatusCode)
			}
		}(i)
	}
	wg.Wait()

	// Then: every record reaches the target queue
	if got := drainQueue(t, srv, queueURL, writes); got != writes {
		t.Fatalf("delivered %d of %d records to the pipe target", got, writes)
	}
}

// waitForRunning polls DescribePipe until the pipe reports RUNNING.
func waitForRunning(t *testing.T, srv *helpers.TestServer, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp := describePipe(t, srv, name)
		var p struct {
			CurrentState string `json:"CurrentState"`
		}
		helpers.DecodeJSON(t, resp, &p)
		resp.Body.Close()
		if p.CurrentState == "RUNNING" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("pipe %q never reached RUNNING", name)
}

// drainQueue receives from the queue until want messages have arrived, or until
// it has stayed empty long enough that nothing more is coming.
func drainQueue(t *testing.T, srv *helpers.TestServer, queueURL string, want int) int {
	t.Helper()
	total, idle := 0, 0
	for total < want && idle < 40 {
		msgs := receiveMessages(t, srv, queueURL)
		if len(msgs) == 0 {
			idle++
			time.Sleep(25 * time.Millisecond)
			continue
		}
		idle = 0
		total += len(msgs)
	}
	return total
}
