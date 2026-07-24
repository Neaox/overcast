// Package kinesis_test contains integration tests for the Kinesis emulator.
//
// Run: go test ./tests/integration/kinesis/...
package kinesis_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/fxamacker/cbor/v2"

	"github.com/Neaox/overcast/tests/helpers"
)

// kinesisCall sends a Kinesis JSON-RPC request via X-Amz-Target header.
func kinesisCall(t *testing.T, srv *helpers.TestServer, op string, body any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "Kinesis_20131202."+op)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func kinesisCBORCall(t *testing.T, srv *helpers.TestServer, op string, body map[string]any) *http.Response {
	t.Helper()
	payload, err := cbor.Marshal(body)
	if err != nil {
		t.Fatalf("marshal CBOR %s body: %v", op, err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/service/Kinesis/operation/"+op, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build CBOR %s request: %v", op, err)
	}
	req.Header.Set("Content-Type", "application/cbor")
	req.Header.Set("Smithy-Protocol", "rpc-v2-cbor")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do CBOR %s request: %v", op, err)
	}
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func decodeCBOR(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := cbor.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode CBOR response: %v", err)
	}
}

func TestRPCv2CBOR_ListStreams(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := kinesisCBORCall(t, srv, "CreateStream", map[string]any{
		"StreamName": "cbor-list",
		"ShardCount": 1,
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	resp = kinesisCBORCall(t, srv, "ListStreams", map[string]any{})
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertHeader(t, resp, "Content-Type", "application/cbor")

	var out struct {
		StreamNames []string `cbor:"StreamNames"`
	}
	decodeCBOR(t, resp, &out)
	if len(out.StreamNames) != 1 || out.StreamNames[0] != "cbor-list" {
		t.Fatalf("StreamNames = %#v, want [cbor-list]", out.StreamNames)
	}
}

func TestRPCv2CBOR_RecordRoundTrip(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := kinesisCBORCall(t, srv, "CreateStream", map[string]any{
		"StreamName": "cbor-records",
		"ShardCount": 1,
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	resp = kinesisCBORCall(t, srv, "ListShards", map[string]any{
		"StreamName": "cbor-records",
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var shardsOut struct {
		Shards []struct {
			ShardId string `cbor:"ShardId"`
		} `cbor:"Shards"`
	}
	decodeCBOR(t, resp, &shardsOut)
	if len(shardsOut.Shards) != 1 {
		t.Fatalf("expected one shard, got %d", len(shardsOut.Shards))
	}

	resp = kinesisCBORCall(t, srv, "GetShardIterator", map[string]any{
		"StreamName":        "cbor-records",
		"ShardId":           shardsOut.Shards[0].ShardId,
		"ShardIteratorType": "TRIM_HORIZON",
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var iterOut struct {
		ShardIterator string `cbor:"ShardIterator"`
	}
	decodeCBOR(t, resp, &iterOut)
	if iterOut.ShardIterator == "" {
		t.Fatal("expected ShardIterator")
	}

	resp = kinesisCBORCall(t, srv, "PutRecord", map[string]any{
		"StreamName":   "cbor-records",
		"Data":         []byte("hello"),
		"PartitionKey": "pk",
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	resp = kinesisCBORCall(t, srv, "GetRecords", map[string]any{
		"ShardIterator": iterOut.ShardIterator,
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var recordsOut struct {
		Records []struct {
			Data         []byte `cbor:"Data"`
			PartitionKey string `cbor:"PartitionKey"`
		} `cbor:"Records"`
	}
	decodeCBOR(t, resp, &recordsOut)
	if len(recordsOut.Records) != 1 {
		t.Fatalf("expected one record, got %d", len(recordsOut.Records))
	}
	if string(recordsOut.Records[0].Data) != "hello" {
		t.Fatalf("record Data = %q, want hello", string(recordsOut.Records[0].Data))
	}
	if recordsOut.Records[0].PartitionKey != "pk" {
		t.Fatalf("record PartitionKey = %q, want pk", recordsOut.Records[0].PartitionKey)
	}
}

// ---- PutRecord sequence-number collisions (storage-access-plan.md A1) -----

// TestPutRecord_noSeqNoCollisionAfterDeletion reproduces the A1 hazard: the
// old nextSeqNo derived the next sequence number from len(existing records)
// in the shard. Kinesis has no public per-record delete API, so a record
// can only disappear via retention trim or stream-recreation residue — we
// simulate that here by deleting the stored record directly, the same way
// other services' tests inject storage-layer conditions the wire API can't
// reach (see tests/integration/sqs/sqs_test.go's direct srv.Store.Set
// calls). Before A1: deleting the shard's only record drops its length back
// to 0, so the next PutRecord recomputes sequence number 0 again — the
// exact same key as the deleted record — silently colliding with (and
// masking the loss of) whatever key formatting assumed was unique.
func TestPutRecord_noSeqNoCollisionAfterDeletion(t *testing.T) {
	// Given: a stream with one shard and a single record in it
	srv := helpers.NewTestServer(t)
	const streamName = "seq-collision"

	resp := kinesisCall(t, srv, "CreateStream", map[string]any{
		"StreamName": streamName,
		"ShardCount": 1,
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	resp = kinesisCall(t, srv, "PutRecord", map[string]any{
		"StreamName":   streamName,
		"Data":         []byte("first"),
		"PartitionKey": "pk",
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var first struct {
		ShardId        string `json:"ShardId"`
		SequenceNumber string `json:"SequenceNumber"`
	}
	decodeJSON(t, resp, &first)
	if first.SequenceNumber == "" {
		t.Fatal("expected a non-empty SequenceNumber")
	}

	// When: the record is removed directly from the store (simulating
	// retention trim / stream-recreation residue), and a second record is
	// put afterwards.
	ctx := context.Background()
	recordKey := "us-east-1/" + streamName + "/" + first.ShardId + "/" + first.SequenceNumber
	if err := srv.Store.Delete(ctx, "kinesis:records", recordKey); err != nil {
		t.Fatalf("delete record directly: %v", err)
	}

	resp = kinesisCall(t, srv, "PutRecord", map[string]any{
		"StreamName":   streamName,
		"Data":         []byte("second"),
		"PartitionKey": "pk",
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var second struct {
		ShardId        string `json:"ShardId"`
		SequenceNumber string `json:"SequenceNumber"`
	}
	decodeJSON(t, resp, &second)

	// Then: the second record must get a strictly greater sequence number —
	// never a repeat of the deleted one.
	if second.SequenceNumber == first.SequenceNumber {
		t.Fatalf("sequence number collision: both PutRecord calls got %q", first.SequenceNumber)
	}
	if second.SequenceNumber <= first.SequenceNumber {
		t.Fatalf("sequence numbers must strictly increase: first=%q second=%q", first.SequenceNumber, second.SequenceNumber)
	}
}

// TestPutRecords_noSeqNoCollisionAfterDeletion is PutRecords' counterpart:
// a whole batch must still allocate strictly-increasing sequence numbers
// for a shard after one of its earlier records was removed.
func TestPutRecords_noSeqNoCollisionAfterDeletion(t *testing.T) {
	// Given: a stream with one shard and one record already in it
	srv := helpers.NewTestServer(t)
	const streamName = "seq-collision-batch"

	resp := kinesisCall(t, srv, "CreateStream", map[string]any{
		"StreamName": streamName,
		"ShardCount": 1,
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	resp = kinesisCall(t, srv, "PutRecord", map[string]any{
		"StreamName":   streamName,
		"Data":         []byte("first"),
		"PartitionKey": "pk",
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var first struct {
		ShardId        string `json:"ShardId"`
		SequenceNumber string `json:"SequenceNumber"`
	}
	decodeJSON(t, resp, &first)

	// When: that record is deleted directly, then a 3-record PutRecords
	// batch is submitted to the same shard.
	ctx := context.Background()
	recordKey := "us-east-1/" + streamName + "/" + first.ShardId + "/" + first.SequenceNumber
	if err := srv.Store.Delete(ctx, "kinesis:records", recordKey); err != nil {
		t.Fatalf("delete record directly: %v", err)
	}

	resp = kinesisCall(t, srv, "PutRecords", map[string]any{
		"StreamName": streamName,
		"Records": []map[string]any{
			{"Data": []byte("a"), "PartitionKey": "pk"},
			{"Data": []byte("b"), "PartitionKey": "pk"},
			{"Data": []byte("c"), "PartitionKey": "pk"},
		},
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var batch struct {
		Records []struct {
			SequenceNumber string `json:"SequenceNumber"`
		} `json:"Records"`
	}
	decodeJSON(t, resp, &batch)

	// Then: none of the batch's sequence numbers repeats the deleted
	// record's, and the batch itself is strictly increasing (a contiguous
	// block allocation, not one collision-prone read-modify-write per
	// record).
	if len(batch.Records) != 3 {
		t.Fatalf("expected 3 records in response, got %d", len(batch.Records))
	}
	prev := first.SequenceNumber
	for i, r := range batch.Records {
		if r.SequenceNumber <= prev {
			t.Fatalf("record[%d] SequenceNumber=%q must be strictly greater than previous=%q", i, r.SequenceNumber, prev)
		}
		prev = r.SequenceNumber
	}
}

// ---- GetRecords iterator resume (storage-access-plan.md A2) ---------------

// TestGetRecords_iteratorResumeNoDuplicatesOrGaps walks a shard's full
// record set through repeated small-Limit GetRecords calls, following
// NextShardIterator each time — mirroring internal/state's ScanPage
// no-duplicates/no-gaps suites (see assertScanPagePaginatesFullRange in
// internal/state/memory_test.go), but over the Kinesis wire API.
func TestGetRecords_iteratorResumeNoDuplicatesOrGaps(t *testing.T) {
	// Given: a stream with one shard and many records
	srv := helpers.NewTestServer(t)
	const streamName = "iter-resume"
	const total = 37
	const pageLimit = 5

	resp := kinesisCall(t, srv, "CreateStream", map[string]any{
		"StreamName": streamName,
		"ShardCount": 1,
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	for i := 0; i < total; i++ {
		resp := kinesisCall(t, srv, "PutRecord", map[string]any{
			"StreamName":   streamName,
			"Data":         []byte(fmt.Sprintf("rec-%03d", i)),
			"PartitionKey": "pk",
		})
		helpers.AssertStatus(t, resp, http.StatusOK)
		resp.Body.Close()
	}

	resp = kinesisCall(t, srv, "ListShards", map[string]any{"StreamName": streamName})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var shardsOut struct {
		Shards []struct {
			ShardId string `json:"ShardId"`
		} `json:"Shards"`
	}
	decodeJSON(t, resp, &shardsOut)
	if len(shardsOut.Shards) != 1 {
		t.Fatalf("expected one shard, got %d", len(shardsOut.Shards))
	}

	resp = kinesisCall(t, srv, "GetShardIterator", map[string]any{
		"StreamName":        streamName,
		"ShardId":           shardsOut.Shards[0].ShardId,
		"ShardIteratorType": "TRIM_HORIZON",
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var iterOut struct {
		ShardIterator string `json:"ShardIterator"`
	}
	decodeJSON(t, resp, &iterOut)

	// When: we page through GetRecords with a Limit well below the total
	// record count, following NextShardIterator each time.
	var got []string
	iter := iterOut.ShardIterator
	for pages := 0; len(got) < total; pages++ {
		if pages > total+5 {
			t.Fatalf("GetRecords did not converge after %d pages (want %d records, got %d): %v", pages, total, len(got), got)
		}
		resp := kinesisCall(t, srv, "GetRecords", map[string]any{
			"ShardIterator": iter,
			"Limit":         pageLimit,
		})
		helpers.AssertStatus(t, resp, http.StatusOK)
		var out struct {
			Records []struct {
				Data []byte `json:"Data"`
			} `json:"Records"`
			NextShardIterator string `json:"NextShardIterator"`
		}
		decodeJSON(t, resp, &out)
		if len(out.Records) == 0 && out.NextShardIterator == iter {
			t.Fatalf("iterator stalled (no records, no progress) after %d of %d records", len(got), total)
		}
		for _, r := range out.Records {
			got = append(got, string(r.Data))
		}
		iter = out.NextShardIterator
	}

	// Then: every record was returned exactly once, in write order — no
	// skips, no duplicates.
	if len(got) != total {
		t.Fatalf("collected %d records, want %d", len(got), total)
	}
	for i, d := range got {
		want := fmt.Sprintf("rec-%03d", i)
		if d != want {
			t.Fatalf("record[%d] = %q, want %q (skip, duplicate, or reorder)", i, d, want)
		}
	}
}

// ---- SplitShard -------------------------------------------------------------

// TestSplitShard pins the JSON1.1 wire path's behavior for SplitShard,
// which now delegates to splitShardTyped (typed_logic.go) instead of
// duplicating shard-splitting logic in handler.go. There was no existing
// integration coverage for SplitShard at all before this test.
func TestSplitShard(t *testing.T) {
	srv := helpers.NewTestServer(t)

	// Given: a stream with a single shard
	resp := kinesisCall(t, srv, "CreateStream", map[string]any{
		"StreamName": "split-test",
		"ShardCount": 1,
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	resp = kinesisCall(t, srv, "ListShards", map[string]any{
		"StreamName": "split-test",
	})
	var before struct {
		Shards []struct {
			ShardId      string `json:"ShardId"`
			HashKeyRange struct {
				StartingHashKey string `json:"StartingHashKey"`
				EndingHashKey   string `json:"EndingHashKey"`
			} `json:"HashKeyRange"`
		} `json:"Shards"`
	}
	decodeJSON(t, resp, &before)
	if len(before.Shards) != 1 {
		t.Fatalf("expected 1 open shard before split, got %d", len(before.Shards))
	}
	parentShardID := before.Shards[0].ShardId
	startHash := before.Shards[0].HashKeyRange.StartingHashKey
	endHash := before.Shards[0].HashKeyRange.EndingHashKey

	// Split roughly at the midpoint of the shard's hash key range: the
	// exact math isn't the point of this test (that's covered by the
	// shared implementation's own logic in typed_logic.go/handler.go
	// history), the wire round trip is.
	newStartingHashKey := "1"

	// When: SplitShard is called via the JSON1.1 wire (X-Amz-Target), the
	// same path real AWS SDKs use by default.
	resp = kinesisCall(t, srv, "SplitShard", map[string]any{
		"StreamName":         "split-test",
		"ShardToSplit":       parentShardID,
		"NewStartingHashKey": newStartingHashKey,
	})

	// Then: it succeeds with an empty body (matching this handler's
	// pre-existing void-operation convention — see SplitShard's doc
	// comment in handler.go).
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if body != "" {
		t.Fatalf("expected empty body for SplitShard success, got %q", body)
	}

	// And: ListShards now shows two open child shards covering the
	// original hash key range, with the parent no longer open.
	resp = kinesisCall(t, srv, "ListShards", map[string]any{
		"StreamName": "split-test",
	})
	var after struct {
		Shards []struct {
			ShardId      string `json:"ShardId"`
			HashKeyRange struct {
				StartingHashKey string `json:"StartingHashKey"`
				EndingHashKey   string `json:"EndingHashKey"`
			} `json:"HashKeyRange"`
		} `json:"Shards"`
	}
	decodeJSON(t, resp, &after)
	if len(after.Shards) != 2 {
		t.Fatalf("expected 2 open shards after split, got %d", len(after.Shards))
	}
	for _, s := range after.Shards {
		if s.ShardId == parentShardID {
			t.Fatalf("parent shard %s is still open after split", parentShardID)
		}
	}
	gotStart := after.Shards[0].HashKeyRange.StartingHashKey
	gotEnd := after.Shards[1].HashKeyRange.EndingHashKey
	if gotStart != startHash {
		t.Fatalf("first child StartingHashKey = %q, want parent's %q", gotStart, startHash)
	}
	if gotEnd != endHash {
		t.Fatalf("second child EndingHashKey = %q, want parent's %q", gotEnd, endHash)
	}

	resp = kinesisCall(t, srv, "DescribeStreamSummary", map[string]any{
		"StreamName": "split-test",
	})
	var summary struct {
		StreamDescriptionSummary struct {
			OpenShardCount int `json:"OpenShardCount"`
		} `json:"StreamDescriptionSummary"`
	}
	decodeJSON(t, resp, &summary)
	if summary.StreamDescriptionSummary.OpenShardCount != 2 {
		t.Fatalf("expected OpenShardCount=2, got %d", summary.StreamDescriptionSummary.OpenShardCount)
	}
}

// ---- MergeShards -----------------------------------------------------------

func TestMergeShards(t *testing.T) {
	srv := helpers.NewTestServer(t)

	// Given: a stream with 2 shards
	resp := kinesisCall(t, srv, "CreateStream", map[string]any{
		"StreamName": "merge-test",
		"ShardCount": 2,
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Find two open shards via ListShards
	resp = kinesisCall(t, srv, "ListShards", map[string]any{
		"StreamName": "merge-test",
	})
	var listResp struct {
		Shards []struct {
			ShardId      string `json:"ShardId"`
			HashKeyRange struct {
				StartingHashKey string `json:"StartingHashKey"`
				EndingHashKey   string `json:"EndingHashKey"`
			} `json:"HashKeyRange"`
			SequenceNumberRange struct {
				EndingSequenceNumber string `json:"EndingSequenceNumber"`
			} `json:"SequenceNumberRange"`
		} `json:"Shards"`
	}
	decodeJSON(t, resp, &listResp)

	// Collect open shards
	var openShardIDs []string
	for _, s := range listResp.Shards {
		if s.SequenceNumberRange.EndingSequenceNumber == "" {
			openShardIDs = append(openShardIDs, s.ShardId)
		}
	}
	if len(openShardIDs) < 2 {
		t.Fatalf("expected at least 2 open shards, got %d", len(openShardIDs))
	}

	// When: merge the two shards
	resp = kinesisCall(t, srv, "MergeShards", map[string]any{
		"StreamName":           "merge-test",
		"ShardToMerge":         openShardIDs[0],
		"AdjacentShardToMerge": openShardIDs[1],
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Then: ListShards should show 1 open shard (the merged one)
	resp = kinesisCall(t, srv, "ListShards", map[string]any{
		"StreamName": "merge-test",
	})
	var afterResp struct {
		Shards []struct {
			ShardId string `json:"ShardId"`
		} `json:"Shards"`
	}
	decodeJSON(t, resp, &afterResp)
	if len(afterResp.Shards) != 1 {
		t.Fatalf("expected 1 open shard after merge, got %d", len(afterResp.Shards))
	}

	// Also verify via DescribeStreamSummary that shard count is 1
	resp = kinesisCall(t, srv, "DescribeStreamSummary", map[string]any{
		"StreamName": "merge-test",
	})
	var summResp struct {
		StreamDescriptionSummary struct {
			OpenShardCount int `json:"OpenShardCount"`
		} `json:"StreamDescriptionSummary"`
	}
	decodeJSON(t, resp, &summResp)
	if summResp.StreamDescriptionSummary.OpenShardCount != 1 {
		t.Fatalf("expected OpenShardCount=1, got %d", summResp.StreamDescriptionSummary.OpenShardCount)
	}
}
