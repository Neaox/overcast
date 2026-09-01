// Kinesis source retries and dead-lettering (issue #513).
//
// SourceParameters.KinesisStreamParameters.MaximumRetryAttempts and
// DeadLetterConfig were accepted and echoed back by CreatePipe/UpdatePipe but
// never read by the poller: a Kinesis batch that never succeeds was retried on
// every poll tick forever, so it could never reach a dead-letter queue and the
// shard cursor never advanced past it. DynamoDB Streams already had this
// behaviour, through executeStream (issue #582) — these tests give Kinesis the
// same contract, and pin the shape of what the DLQ receives and what happens
// with no DeadLetterConfig configured at all, which is the negative control.
package pipes_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// pipeDelivery is one row of the /_overcast/pipes/deliveries console feed.
type pipeDelivery struct {
	Pipe     string `json:"Pipe"`
	Outcome  string `json:"Outcome"`
	Attempts int    `json:"Attempts"`
	Error    string `json:"Error"`
}

// waitForDelivery polls the console's delivery feed for pipe until a record
// satisfies match, advancing the mock clock between checks — the same
// poll-and-advance shape pollForMessages uses for a target queue, applied to
// the outcome feed instead of a queue's messages.
func waitForDelivery(t *testing.T, srv *helpers.TestServer, pipe string, match func(pipeDelivery) bool) pipeDelivery {
	t.Helper()
	for i := 0; i < 40; i++ {
		resp, err := http.Get(srv.URL + "/_overcast/pipes/deliveries?pipe=" + pipe)
		if err != nil {
			t.Fatalf("GET /_overcast/pipes/deliveries: %v", err)
		}
		var out struct {
			Deliveries []pipeDelivery `json:"Deliveries"`
		}
		helpers.DecodeJSON(t, resp, &out)
		resp.Body.Close()
		for _, d := range out.Deliveries {
			if match(d) {
				return d
			}
		}
		advancePast(t, srv)
	}
	t.Fatalf("pipe %q: no delivery record matched before the deadline", pipe)
	return pipeDelivery{}
}

func TestPipeDelivery_kinesisSourceDeadLettersExhaustedBatch(t *testing.T) {
	// Given: a Kinesis-sourced pipe whose target queue does not exist, so every
	// delivery attempt fails, a retry budget of one, and a dead-letter queue
	// configured on the source
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	streamARN := mustCreateKinesisStream(t, srv, "events")
	dlqURL, dlqARN := mustCreateQueue(t, srv, "kinesis-dlq")
	targetARN := fmt.Sprintf("arn:aws:sqs:%s:%s:missing-target", srv.Config.Region, srv.Config.AccountID)

	mustCreateRunningPipe(t, srv, "kinesis-dlq-pipe", map[string]any{
		"Source": streamARN,
		"Target": targetARN,
		"SourceParameters": map[string]any{
			"KinesisStreamParameters": map[string]any{
				"StartingPosition":     "TRIM_HORIZON",
				"MaximumRetryAttempts": 1,
				"DeadLetterConfig":     map[string]any{"Arn": dlqARN},
			},
		},
	})

	// When: a record is put on the stream
	resp := jsonCall(t, srv, "Kinesis_20131202.PutRecord", map[string]any{
		"StreamName":   "events",
		"PartitionKey": "pk-1",
		"Data":         []byte(`{"orderId":"o-1"}`),
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: once the retry budget (1 retry, so 2 attempts total) is exhausted,
	// the batch reaches the dead-letter queue as the source records themselves
	bodies := pollForMessages(t, srv, dlqURL)
	if len(bodies) != 1 {
		t.Fatalf("dead-letter queue received %d messages, want 1", len(bodies))
	}
	var records []map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &records); err != nil || len(records) != 1 {
		t.Fatalf("dead-lettered body is not a one-record array: %v\nbody: %s", err, bodies[0])
	}
	if records[0]["eventSource"] != "aws:kinesis" {
		t.Errorf("eventSource = %v, want aws:kinesis", records[0]["eventSource"])
	}
	if records[0]["partitionKey"] != "pk-1" {
		t.Errorf("partitionKey = %v, want pk-1", records[0]["partitionKey"])
	}

	// And: the console's delivery feed reports the outcome as dlq, not dropped
	d := waitForDelivery(t, srv, "kinesis-dlq-pipe", func(d pipeDelivery) bool { return d.Outcome == "dlq" })
	if d.Attempts != 2 {
		t.Errorf("Attempts = %d, want 2 (1 initial + 1 retry)", d.Attempts)
	}

	// And: the poison batch is not retried forever — the shard cursor moved
	// past it, so nothing more arrives on the dead-letter queue on later ticks
	advancePast(t, srv)
	advancePast(t, srv)
	if again := receiveMessages(t, srv, dlqURL); len(again) != 0 {
		t.Errorf("dead-letter queue received %d more messages — the batch was retried after being dead-lettered", len(again))
	}
}

// TestPipeDelivery_kinesisSourceWithoutDeadLetterConfigDropsExhaustedBatch is
// the negative control: with no DeadLetterConfig, a batch that exhausts its
// retry budget has nowhere to go. Before #513, a batch with no DLQ was instead
// retried forever, since MaximumRetryAttempts was never read for a Kinesis
// source either — this pins the new behaviour, which matches what DynamoDB
// Streams has already done since #582: exhaust the budget, report the outcome
// as failed on the console feed, and move on rather than blocking the shard.
func TestPipeDelivery_kinesisSourceWithoutDeadLetterConfigDropsExhaustedBatch(t *testing.T) {
	// Given: a Kinesis-sourced pipe with a retry budget and no DeadLetterConfig
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	streamARN := mustCreateKinesisStream(t, srv, "events")
	targetARN := fmt.Sprintf("arn:aws:sqs:%s:%s:missing-target-2", srv.Config.Region, srv.Config.AccountID)

	mustCreateRunningPipe(t, srv, "kinesis-no-dlq", map[string]any{
		"Source": streamARN,
		"Target": targetARN,
		"SourceParameters": map[string]any{
			"KinesisStreamParameters": map[string]any{
				"StartingPosition":     "TRIM_HORIZON",
				"MaximumRetryAttempts": 1,
			},
		},
	})

	// When: a record is put on the stream and every delivery attempt fails
	resp := jsonCall(t, srv, "Kinesis_20131202.PutRecord", map[string]any{
		"StreamName":   "events",
		"PartitionKey": "pk-1",
		"Data":         []byte(`{"orderId":"o-1"}`),
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: once the retry budget is exhausted the outcome is reported as
	// failed, naming both attempts, rather than dlq
	d := waitForDelivery(t, srv, "kinesis-no-dlq", func(d pipeDelivery) bool {
		return d.Outcome == "failed" && d.Attempts == 2
	})
	if d.Error == "" {
		t.Errorf("Error = %q, want a reason the delivery failed", d.Error)
	}
}

func TestCreatePipe_rejectsUnsupportedDeadLetterTarget(t *testing.T) {
	// Given: a DeadLetterConfig naming a destination Overcast cannot deliver
	// to — refused loudly rather than stored and discovered only once a batch
	// exhausts its retries
	srv := helpers.NewTestServer(t)
	streamARN := mustCreateKinesisStream(t, srv, "events")
	_, targetARN := mustCreateQueue(t, srv, "target")

	resp := createPipeBody(t, srv, "bad-dlq", map[string]any{
		"Source": streamARN,
		"Target": targetARN,
		"SourceParameters": map[string]any{
			"KinesisStreamParameters": map[string]any{
				"DeadLetterConfig": map[string]any{
					"Arn": "arn:aws:lambda:us-east-1:000000000000:function:not-a-queue",
				},
			},
		},
	})

	assertValidationRejection(t, resp, "DeadLetterConfig")
}

func TestCreatePipe_rejectsUnsupportedDeadLetterTargetOnDynamoDBSource(t *testing.T) {
	// Given: the same rejection, on the DynamoDB Streams source's DeadLetterConfig
	srv := helpers.NewTestServer(t)
	streamARN := mustCreateStreamTable(t, srv, "orders")
	_, targetARN := mustCreateQueue(t, srv, "target")

	resp := createPipeBody(t, srv, "bad-dlq-ddb", map[string]any{
		"Source": streamARN,
		"Target": targetARN,
		"SourceParameters": map[string]any{
			"DynamoDBStreamParameters": map[string]any{
				"StartingPosition": "TRIM_HORIZON",
				"DeadLetterConfig": map[string]any{
					"Arn": "arn:aws:lambda:us-east-1:000000000000:function:not-a-queue",
				},
			},
		},
	})

	assertValidationRejection(t, resp, "DeadLetterConfig")
}

func TestUpdatePipe_rejectsUnsupportedDeadLetterTarget(t *testing.T) {
	// Given: a running, supported pipe
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	streamARN := mustCreateKinesisStream(t, srv, "events")
	_, targetARN := mustCreateQueue(t, srv, "target")
	mustCreateRunningPipe(t, srv, "p", map[string]any{"Source": streamARN, "Target": targetARN})

	// When: a DeadLetterConfig naming an undeliverable destination is added
	resp := updatePipe(t, srv, "p", map[string]any{
		"SourceParameters": map[string]any{
			"KinesisStreamParameters": map[string]any{
				"DeadLetterConfig": map[string]any{
					"Arn": "arn:aws:lambda:us-east-1:000000000000:function:not-a-queue",
				},
			},
		},
	})

	// Then: the update is refused, not stored
	assertValidationRejection(t, resp, "DeadLetterConfig")
}
