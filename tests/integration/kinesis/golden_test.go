// golden_test.go contains wire-byte golden tests for Kinesis — see
// docs/plans/wire-byte-goldens.md for the harness design.
//
// Kinesis is the proven delegation pilot: PR #272 converted PutRecord,
// PutRecords, GetShardIterator, GetRecords, SplitShard, and MergeShards from
// duplicated logic into thin JSON1.1 decode-shims that call the same typed
// implementation the CBOR path uses (see the doc comments on those handlers
// in internal/services/kinesis/handler.go). These goldens guard against a
// regression in that already-completed delegation and give the P2 sweep
// (docs/plans/level2-codegen.md §3 Track 2, §4 phase P2) a worked example of
// "delegate, don't duplicate" staying byte-identical over time.
//
// Coverage — management-plane, deterministic operations only:
//
//	CreateStream, DeleteStream, DescribeStream, DescribeStreamSummary,
//	ListStreams, ListShards, AddTagsToStream, ListTagsForStream,
//	RemoveTagsFromStream, IncreaseStreamRetentionPeriod,
//	DecreaseStreamRetentionPeriod
//
// Excluded (message-plane / non-deterministic or low golden value):
//
//   - PutRecord, PutRecords, GetRecords, GetShardIterator: message-plane
//     operations per docs/plans/wire-byte-goldens.md §3 non-goals. Sequence
//     numbers and shard iterators happen to be deterministic in this
//     emulator's implementation today, but pinning their exact encoding as a
//     byte-level contract is exactly the kind of internal-representation
//     coupling the plan's non-goals warn against — these get behavioral
//     integration tests instead (see kinesis_test.go).
//   - SplitShard, MergeShards: JSON1.1 success response is an unconditional
//     empty 200 body regardless of outcome (see the "empty-body convention"
//     doc comments in handler.go) — a golden here would only pin the status
//     code, which existing integration tests already assert more directly.
//
// Known pre-existing non-determinism (not papered over by this harness):
// ListTagsForStream serializes internal/services/kinesis Stream.Tags — a Go
// map — by ranging over it directly (handler.go), so with two or more tags
// the JSON array order is randomized per process and this golden would be
// flaky. The golden test below therefore uses exactly one tag, where map
// iteration order is moot. A real fix (sort tags by key before
// serialization) is out of scope for this harness and has been flagged
// separately.
//
// Record: go test ./tests/integration/kinesis/ -run TestGolden -record
// Assert: go test ./tests/integration/kinesis/ -run TestGolden
package kinesis_test

import (
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

const kinesisGoldenDir = "goldens"

func createStreamGolden(t *testing.T, srv *helpers.TestServer, name string, shardCount int) {
	t.Helper()
	resp := kinesisCall(t, srv, "CreateStream", map[string]any{
		"StreamName": name, "ShardCount": shardCount,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestGolden_CreateStream(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())

	resp := kinesisCall(t, srv, "CreateStream", map[string]any{
		"StreamName": "golden-stream", "ShardCount": 1,
	})
	helpers.GoldenTest(t, kinesisGoldenDir, "CreateStream", resp, nil)
}

func TestGolden_DescribeStream(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createStreamGolden(t, srv, "golden-stream", 1)

	resp := kinesisCall(t, srv, "DescribeStream", map[string]any{"StreamName": "golden-stream"})
	helpers.GoldenTest(t, kinesisGoldenDir, "DescribeStream", resp, nil)
}

func TestGolden_DescribeStreamSummary(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createStreamGolden(t, srv, "golden-stream", 1)

	resp := kinesisCall(t, srv, "DescribeStreamSummary", map[string]any{"StreamName": "golden-stream"})
	helpers.GoldenTest(t, kinesisGoldenDir, "DescribeStreamSummary", resp, nil)
}

func TestGolden_ListStreams(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createStreamGolden(t, srv, "golden-stream-a", 1)
	createStreamGolden(t, srv, "golden-stream-b", 1)

	resp := kinesisCall(t, srv, "ListStreams", map[string]any{})
	helpers.GoldenTest(t, kinesisGoldenDir, "ListStreams", resp, nil)
}

func TestGolden_ListShards(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createStreamGolden(t, srv, "golden-stream", 2)

	resp := kinesisCall(t, srv, "ListShards", map[string]any{"StreamName": "golden-stream"})
	helpers.GoldenTest(t, kinesisGoldenDir, "ListShards", resp, nil)
}

func TestGolden_AddTagsToStream(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createStreamGolden(t, srv, "golden-stream", 1)

	resp := kinesisCall(t, srv, "AddTagsToStream", map[string]any{
		"StreamName": "golden-stream",
		"Tags":       map[string]string{"env": "test"},
	})
	helpers.GoldenTest(t, kinesisGoldenDir, "AddTagsToStream", resp, nil)
}

func TestGolden_ListTagsForStream(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createStreamGolden(t, srv, "golden-stream", 1)
	// Exactly one tag — see the file-level doc comment on the pre-existing
	// map-ordering non-determinism in ListTagsForStream's handler.
	tagResp := kinesisCall(t, srv, "AddTagsToStream", map[string]any{
		"StreamName": "golden-stream",
		"Tags":       map[string]string{"env": "test"},
	})
	tagResp.Body.Close()
	helpers.AssertStatus(t, tagResp, http.StatusOK)

	resp := kinesisCall(t, srv, "ListTagsForStream", map[string]any{"StreamName": "golden-stream"})
	helpers.GoldenTest(t, kinesisGoldenDir, "ListTagsForStream", resp, nil)
}

func TestGolden_RemoveTagsFromStream(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createStreamGolden(t, srv, "golden-stream", 1)
	tagResp := kinesisCall(t, srv, "AddTagsToStream", map[string]any{
		"StreamName": "golden-stream",
		"Tags":       map[string]string{"env": "test"},
	})
	tagResp.Body.Close()
	helpers.AssertStatus(t, tagResp, http.StatusOK)

	resp := kinesisCall(t, srv, "RemoveTagsFromStream", map[string]any{
		"StreamName": "golden-stream",
		"TagKeys":    []string{"env"},
	})
	helpers.GoldenTest(t, kinesisGoldenDir, "RemoveTagsFromStream", resp, nil)
}

func TestGolden_IncreaseStreamRetentionPeriod(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createStreamGolden(t, srv, "golden-stream", 1)

	resp := kinesisCall(t, srv, "IncreaseStreamRetentionPeriod", map[string]any{
		"StreamName": "golden-stream", "RetentionPeriodHours": 48,
	})
	helpers.GoldenTest(t, kinesisGoldenDir, "IncreaseStreamRetentionPeriod", resp, nil)
}

func TestGolden_DecreaseStreamRetentionPeriod(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createStreamGolden(t, srv, "golden-stream", 1)
	incResp := kinesisCall(t, srv, "IncreaseStreamRetentionPeriod", map[string]any{
		"StreamName": "golden-stream", "RetentionPeriodHours": 48,
	})
	incResp.Body.Close()
	helpers.AssertStatus(t, incResp, http.StatusOK)

	resp := kinesisCall(t, srv, "DecreaseStreamRetentionPeriod", map[string]any{
		"StreamName": "golden-stream", "RetentionPeriodHours": 24,
	})
	helpers.GoldenTest(t, kinesisGoldenDir, "DecreaseStreamRetentionPeriod", resp, nil)
}

func TestGolden_DeleteStream(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createStreamGolden(t, srv, "golden-stream", 1)

	resp := kinesisCall(t, srv, "DeleteStream", map[string]any{"StreamName": "golden-stream"})
	helpers.GoldenTest(t, kinesisGoldenDir, "DeleteStream", resp, nil)
}
