package groups

import (
	"context"
	"fmt"
	"time"

	"github.com/Neaox/overcast-compat-cli/internal/awscli"
	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// Kinesis returns the Kinesis service group.
func Kinesis() ServiceGroup {
	g := &kinesisGroup{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			// kinesis-streams
			"CreateStream":          g.CreateStream,
			"DescribeStream":        g.DescribeStream,
			"DescribeStreamSummary": g.DescribeStreamSummary,
			"ListStreams":           g.ListStreams,
			"AddTagsToStream":       g.AddTagsToStream,
			"ListTagsForStream":     g.ListTagsForStream,
			"DeleteStream":          g.DeleteStream,
			// kinesis-records
			"PutRecord":        g.PutRecord,
			"PutRecords":       g.PutRecords,
			"GetShardIterator": g.GetShardIterator,
			"GetRecords":       g.GetRecords,
			// kinesis-shards
			"ListShards":  g.ListShards,
			"SplitShard":  g.SplitShard,
			"MergeShards": g.MergeShards,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"kinesis-streams": g.setupStreams,
			"kinesis-records": g.setupRecords,
			"kinesis-shards":  g.setupShards,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"kinesis-streams": g.teardownStream,
			"kinesis-records": g.teardownStream,
			"kinesis-shards":  g.teardownStream,
		},
	}
}

type kinesisGroup struct{}

func (g *kinesisGroup) streamName(t *harness.TestContext) string {
	return fmt.Sprintf("%s-kinesis", t.RunID)
}

// currentStreamName returns the stream name stored in context (set by each group's
// setup) falling back to the default streamName.
func (g *kinesisGroup) currentStreamName(t *harness.TestContext) string {
	if n := t.GetString("stream_name"); n != "" {
		return n
	}
	return g.streamName(t)
}

func (g *kinesisGroup) waitStreamActive(t *harness.TestContext) error {
	for i := 0; i < 30; i++ {
		out, err := awscli.RunOutput(t.Endpoint, t.Region,
			"kinesis", "describe-stream-summary",
			"--stream-name", g.currentStreamName(t),
		)
		if err != nil {
			return err
		}
		desc, _ := out["StreamDescriptionSummary"].(map[string]any)
		status, _ := desc["StreamStatus"].(string)
		if status == "ACTIVE" {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("kinesis: stream %s did not become ACTIVE", g.currentStreamName(t))
}

// ─── kinesis-streams ─────────────────────────────────────────────────────────

func (g *kinesisGroup) setupStreams(_ context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("%s-kinesis", t.RunID)
	t.Set("stream_name", name)
	// Best-effort pre-delete of any leftover stream from previous runs.
	awscli.Run(t.Endpoint, t.Region, "kinesis", "delete-stream", "--stream-name", name) //nolint:errcheck
	return nil
}

func (g *kinesisGroup) CreateStream(_ context.Context, t *harness.TestContext) error {
	err := awscli.Run(t.Endpoint, t.Region,
		"kinesis", "create-stream",
		"--stream-name", g.currentStreamName(t),
		"--shard-count", "1",
	)
	if err != nil {
		return err
	}
	return g.waitStreamActive(t)
}

func (g *kinesisGroup) DescribeStream(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kinesis", "describe-stream",
		"--stream-name", g.currentStreamName(t),
	)
	if err != nil {
		return err
	}
	desc, _ := out["StreamDescription"].(map[string]any)
	shards, _ := desc["Shards"].([]any)
	if len(shards) == 0 {
		return fmt.Errorf("kinesis DescribeStream: no shards found")
	}
	shard := shards[0].(map[string]any)
	shardID, _ := shard["ShardId"].(string)
	t.Set("shard_id", shardID)

	sr, _ := shard["HashKeyRange"].(map[string]any)
	t.Set("hash_key_start", fmt.Sprintf("%v", sr["StartingHashKey"]))
	t.Set("hash_key_end", fmt.Sprintf("%v", sr["EndingHashKey"]))
	return nil
}

func (g *kinesisGroup) DescribeStreamSummary(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kinesis", "describe-stream-summary",
		"--stream-name", g.currentStreamName(t),
	)
	if err != nil {
		return err
	}
	desc, _ := out["StreamDescriptionSummary"].(map[string]any)
	if desc["StreamStatus"] != "ACTIVE" {
		return fmt.Errorf("kinesis DescribeStreamSummary: expected StreamStatus=ACTIVE, got %v", desc["StreamStatus"])
	}
	return nil
}

func (g *kinesisGroup) ListStreams(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "kinesis", "list-streams")
	if err != nil {
		return err
	}
	names, _ := out["StreamNames"].([]any)
	want := g.currentStreamName(t)
	for _, v := range names {
		if v == want {
			return nil
		}
	}
	return fmt.Errorf("kinesis ListStreams: stream %q not found", want)
}

func (g *kinesisGroup) AddTagsToStream(_ context.Context, t *harness.TestContext) error {
	if err := awscli.Run(t.Endpoint, t.Region,
		"kinesis", "add-tags-to-stream",
		"--stream-name", g.currentStreamName(t),
		"--tags", `{"env":"test"}`,
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kinesis", "list-tags-for-stream",
		"--stream-name", g.currentStreamName(t),
	)
	if err != nil {
		return fmt.Errorf("kinesis AddTagsToStream: list-tags-for-stream failed: %w", err)
	}
	tags, _ := out["Tags"].([]any)
	for _, raw := range tags {
		if m, ok := raw.(map[string]any); ok && m["Key"] == "env" && m["Value"] == "test" {
			return nil
		}
	}
	return fmt.Errorf("kinesis AddTagsToStream: tag env=test not found")
}

func (g *kinesisGroup) ListTagsForStream(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kinesis", "list-tags-for-stream",
		"--stream-name", g.currentStreamName(t),
	)
	if err != nil {
		return err
	}
	tags, _ := out["Tags"].([]any)
	if len(tags) == 0 {
		return fmt.Errorf("kinesis ListTagsForStream: no tags returned")
	}
	return nil
}

func (g *kinesisGroup) DeleteStream(_ context.Context, t *harness.TestContext) error {
	name := g.currentStreamName(t)
	if err := awscli.Run(t.Endpoint, t.Region,
		"kinesis", "delete-stream",
		"--stream-name", name,
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "kinesis", "list-streams")
	if err != nil {
		return fmt.Errorf("kinesis DeleteStream: list-streams failed: %w", err)
	}
	names, _ := out["StreamNames"].([]any)
	for _, v := range names {
		if v == name {
			return fmt.Errorf("kinesis DeleteStream: stream %q still present after delete", name)
		}
	}
	return nil
}

func (g *kinesisGroup) teardownStream(_ context.Context, t *harness.TestContext) error {
	awscli.Run(t.Endpoint, t.Region, "kinesis", "delete-stream", "--stream-name", g.currentStreamName(t)) //nolint:errcheck
	return nil
}

// ─── kinesis-records ─────────────────────────────────────────────────────────

func (g *kinesisGroup) setupRecords(_ context.Context, t *harness.TestContext) error {
	t.Set("stream_name", fmt.Sprintf("%s-kinesis-r", t.RunID))
	if err := awscli.Run(t.Endpoint, t.Region,
		"kinesis", "create-stream",
		"--stream-name", g.currentStreamName(t),
		"--shard-count", "1",
	); err != nil {
		return err
	}
	return g.waitStreamActive(t)
}

func (g *kinesisGroup) PutRecord(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kinesis", "put-record",
		"--stream-name", g.currentStreamName(t),
		"--data", encodeBase64([]byte("record-1")),
		"--partition-key", t.RunID,
	)
	if err != nil {
		return err
	}
	shardID, _ := out["ShardId"].(string)
	t.Set("shard_id", shardID)
	return nil
}

func (g *kinesisGroup) PutRecords(_ context.Context, t *harness.TestContext) error {
	records := fmt.Sprintf(
		`[{"Data":"%s","PartitionKey":"pk1"},{"Data":"%s","PartitionKey":"pk2"}]`,
		encodeBase64([]byte("r1")),
		encodeBase64([]byte("r2")),
	)
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kinesis", "put-records",
		"--stream-name", g.currentStreamName(t),
		"--records", records,
	)
	if err != nil {
		return err
	}
	if failed, _ := out["FailedRecordCount"].(float64); failed != 0 {
		return fmt.Errorf("kinesis PutRecords: FailedRecordCount=%v, expected 0", failed)
	}
	return nil
}

func (g *kinesisGroup) GetShardIterator(_ context.Context, t *harness.TestContext) error {
	shardID := t.GetString("shard_id")
	if shardID == "" {
		shardID = "shardId-000000000000"
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kinesis", "get-shard-iterator",
		"--stream-name", g.currentStreamName(t),
		"--shard-id", shardID,
		"--shard-iterator-type", "TRIM_HORIZON",
	)
	if err != nil {
		return err
	}
	iter, _ := out["ShardIterator"].(string)
	if iter == "" {
		return fmt.Errorf("kinesis GetShardIterator: missing ShardIterator")
	}
	t.Set("shard_iterator", iter)
	return nil
}

func (g *kinesisGroup) GetRecords(_ context.Context, t *harness.TestContext) error {
	iter := t.GetString("shard_iterator")
	if iter == "" {
		return fmt.Errorf("kinesis GetRecords: missing shard_iterator")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kinesis", "get-records",
		"--shard-iterator", iter,
		"--limit", "10",
	)
	if err != nil {
		return err
	}
	records, _ := out["Records"].([]any)
	if len(records) == 0 {
		return fmt.Errorf("kinesis GetRecords: no records returned")
	}
	return nil
}

// ─── kinesis-shards ──────────────────────────────────────────────────────────

func (g *kinesisGroup) setupShards(_ context.Context, t *harness.TestContext) error {
	t.Set("stream_name", fmt.Sprintf("%s-kinesis-s", t.RunID))
	if err := awscli.Run(t.Endpoint, t.Region,
		"kinesis", "create-stream",
		"--stream-name", g.currentStreamName(t),
		"--shard-count", "2",
	); err != nil {
		return err
	}
	return g.waitStreamActive(t)
}

func (g *kinesisGroup) ListShards(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kinesis", "list-shards",
		"--stream-name", g.currentStreamName(t),
	)
	if err != nil {
		return err
	}
	shards, _ := out["Shards"].([]any)
	if len(shards) == 0 {
		return fmt.Errorf("kinesis ListShards: no shards")
	}
	shard := shards[0].(map[string]any)
	shardID, _ := shard["ShardId"].(string)
	t.Set("split_shard_id", shardID)

	hr, _ := shard["HashKeyRange"].(map[string]any)
	start, _ := hr["StartingHashKey"].(string)
	end, _ := hr["EndingHashKey"].(string)
	t.Set("hash_key_start", start)
	t.Set("hash_key_end", end)
	return nil
}

func (g *kinesisGroup) SplitShard(_ context.Context, t *harness.TestContext) error {
	shardID := t.GetString("split_shard_id")
	start := t.GetString("hash_key_start")
	end := t.GetString("hash_key_end")
	if shardID == "" {
		return fmt.Errorf("kinesis SplitShard: missing split_shard_id")
	}
	mid := hashMidpointStr(start, end)
	if err := awscli.Run(t.Endpoint, t.Region,
		"kinesis", "split-shard",
		"--stream-name", g.currentStreamName(t),
		"--shard-to-split", shardID,
		"--new-starting-hash-key", mid,
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kinesis", "list-shards",
		"--stream-name", g.currentStreamName(t),
	)
	if err != nil {
		return fmt.Errorf("kinesis SplitShard: list-shards failed: %w", err)
	}
	shards, _ := out["Shards"].([]any)
	if len(shards) < 2 {
		return fmt.Errorf("kinesis SplitShard: expected ≥2 shards after split, got %d", len(shards))
	}
	return nil
}

func (g *kinesisGroup) MergeShards(_ context.Context, t *harness.TestContext) error {
	// List open shards.
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kinesis", "list-shards",
		"--stream-name", g.currentStreamName(t),
	)
	if err != nil {
		return err
	}
	shards, _ := out["Shards"].([]any)
	var openIDs []string
	for _, raw := range shards {
		s, _ := raw.(map[string]any)
		seqRange, _ := s["SequenceNumberRange"].(map[string]any)
		if _, closed := seqRange["EndingSequenceNumber"]; !closed {
			id, _ := s["ShardId"].(string)
			openIDs = append(openIDs, id)
		}
	}
	if len(openIDs) < 2 {
		return nil // not enough open shards
	}
	if err := awscli.Run(t.Endpoint, t.Region,
		"kinesis", "merge-shards",
		"--stream-name", g.currentStreamName(t),
		"--shard-to-merge", openIDs[0],
		"--adjacent-shard-to-merge", openIDs[1],
	); err != nil {
		return err
	}
	return nil
}
