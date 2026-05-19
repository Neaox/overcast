// Package kinesis_test contains integration tests for the Kinesis emulator.
//
// Run: go test ./tests/integration/kinesis/...
package kinesis_test

import (
	"bytes"
	"encoding/json"
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
