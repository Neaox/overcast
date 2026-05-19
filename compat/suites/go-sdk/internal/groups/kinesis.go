package groups

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/Neaox/overcast-compat-go-sdk/internal/clients"
	"github.com/Neaox/overcast-compat-go-sdk/internal/harness"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/aws/aws-sdk-go-v2/service/kinesis/types"
)

func Kinesis(c *clients.Clients) ServiceGroup {
	g := &kinesisGroup{c: c}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"CreateStream":          g.CreateStream,
			"DescribeStream":        g.DescribeStream,
			"DescribeStreamSummary": g.DescribeStreamSummary,
			"ListStreams":           g.ListStreams,
			"AddTagsToStream":       g.AddTagsToStream,
			"ListTagsForStream":     g.ListTagsForStream,
			"DeleteStream":          g.DeleteStream,
			"PutRecord":             g.PutRecord,
			"PutRecords":            g.PutRecords,
			"GetShardIterator":      g.GetShardIterator,
			"GetRecords":            g.GetRecords,
			"ListShards":            g.ListShards,
			"SplitShard":            g.SplitShard,
			"MergeShards":           g.MergeShards,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"kinesis-streams": g.setupStreams,
			"kinesis-records": g.setupRecords,
			"kinesis-shards":  g.setupShards,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"kinesis-streams": g.teardownStreams,
			"kinesis-records": g.teardownRecords,
			"kinesis-shards":  g.teardownShards,
		},
	}
}

type kinesisGroup struct{ c *clients.Clients }

func (g *kinesisGroup) cl() *kinesis.Client { return g.c.Kinesis() }

func (g *kinesisGroup) waitStreamActive(ctx context.Context, streamName string) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := g.cl().DescribeStreamSummary(ctx, &kinesis.DescribeStreamSummaryInput{
			StreamName: aws.String(streamName),
		})
		if err != nil {
			return err
		}
		if resp.StreamDescriptionSummary.StreamStatus == types.StreamStatusActive {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("stream %q did not become ACTIVE within timeout", streamName)
}

// ── kinesis-streams ───────────────────────────────────────────────────────────

func (g *kinesisGroup) setupStreams(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-stream-%s", t.RunID)
	if _, err := g.cl().CreateStream(ctx, &kinesis.CreateStreamInput{
		StreamName: aws.String(name),
		ShardCount: aws.Int32(1),
	}); err != nil {
		return err
	}
	if err := g.waitStreamActive(ctx, name); err != nil {
		return err
	}
	t.Set("kinesis_stream", name)
	return nil
}

func (g *kinesisGroup) teardownStreams(ctx context.Context, t *harness.TestContext) error {
	if name := t.GetString("kinesis_stream"); name != "" {
		g.cl().DeleteStream(ctx, &kinesis.DeleteStreamInput{StreamName: aws.String(name)}) //nolint:errcheck
	}
	return nil
}

func (g *kinesisGroup) CreateStream(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-cs-%s", t.RunID)
	_, err := g.cl().CreateStream(ctx, &kinesis.CreateStreamInput{
		StreamName: aws.String(name),
		ShardCount: aws.Int32(1),
	})
	if err != nil {
		return err
	}
	g.waitStreamActive(ctx, name) //nolint:errcheck
	// Verify stream appears in ListStreams
	list, lErr := g.cl().ListStreams(ctx, &kinesis.ListStreamsInput{})
	if lErr != nil {
		g.cl().DeleteStream(ctx, &kinesis.DeleteStreamInput{StreamName: aws.String(name)}) //nolint:errcheck
		return fmt.Errorf("CreateStream: ListStreams verify failed: %w", lErr)
	}
	found := false
	for _, sn := range list.StreamNames {
		if sn == name {
			found = true
			break
		}
	}
	g.cl().DeleteStream(ctx, &kinesis.DeleteStreamInput{StreamName: aws.String(name)}) //nolint:errcheck
	if !found {
		return fmt.Errorf("CreateStream: stream %q not found in ListStreams", name)
	}
	return nil
}

func (g *kinesisGroup) DescribeStream(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().DescribeStream(ctx, &kinesis.DescribeStreamInput{
		StreamName: aws.String(t.GetString("kinesis_stream")),
	})
	if err != nil {
		return err
	}
	if resp.StreamDescription == nil {
		return fmt.Errorf("DescribeStream: nil description")
	}
	return nil
}

func (g *kinesisGroup) DescribeStreamSummary(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().DescribeStreamSummary(ctx, &kinesis.DescribeStreamSummaryInput{
		StreamName: aws.String(t.GetString("kinesis_stream")),
	})
	if err != nil {
		return err
	}
	if resp.StreamDescriptionSummary == nil || aws.ToString(resp.StreamDescriptionSummary.StreamName) == "" {
		return fmt.Errorf("DescribeStreamSummary: missing stream name in response")
	}
	return nil
}

func (g *kinesisGroup) ListStreams(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().ListStreams(ctx, &kinesis.ListStreamsInput{})
	if err != nil {
		return err
	}
	found := false
	for _, s := range resp.StreamNames {
		if s == t.GetString("kinesis_stream") {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("ListStreams: stream %q not found", t.GetString("kinesis_stream"))
	}
	return nil
}

func (g *kinesisGroup) AddTagsToStream(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().AddTagsToStream(ctx, &kinesis.AddTagsToStreamInput{
		StreamName: aws.String(t.GetString("kinesis_stream")),
		Tags:       map[string]string{"env": "test"},
	})
	if err != nil {
		return err
	}
	resp, err := g.cl().ListTagsForStream(ctx, &kinesis.ListTagsForStreamInput{
		StreamName: aws.String(t.GetString("kinesis_stream")),
	})
	if err != nil {
		return fmt.Errorf("AddTagsToStream: ListTagsForStream verify failed: %w", err)
	}
	for _, tag := range resp.Tags {
		if aws.ToString(tag.Key) == "env" && aws.ToString(tag.Value) == "test" {
			return nil
		}
	}
	return fmt.Errorf("AddTagsToStream: env=test tag not found")
}

func (g *kinesisGroup) ListTagsForStream(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().ListTagsForStream(ctx, &kinesis.ListTagsForStreamInput{
		StreamName: aws.String(t.GetString("kinesis_stream")),
	})
	if err != nil {
		return err
	}
	if len(resp.Tags) == 0 {
		return fmt.Errorf("ListTagsForStream: expected ≥1 tag")
	}
	return nil
}

func (g *kinesisGroup) RemoveTagsFromStream(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().RemoveTagsFromStream(ctx, &kinesis.RemoveTagsFromStreamInput{
		StreamName: aws.String(t.GetString("kinesis_stream")),
		TagKeys:    []string{"env"},
	})
	if err != nil {
		return err
	}
	resp, err := g.cl().ListTagsForStream(ctx, &kinesis.ListTagsForStreamInput{
		StreamName: aws.String(t.GetString("kinesis_stream")),
	})
	if err != nil {
		return fmt.Errorf("RemoveTagsFromStream: ListTagsForStream verify failed: %w", err)
	}
	for _, tag := range resp.Tags {
		if aws.ToString(tag.Key) == "env" {
			return fmt.Errorf("RemoveTagsFromStream: env tag still present")
		}
	}
	return nil
}

func (g *kinesisGroup) DeleteStream(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-ds-%s", t.RunID)
	g.cl().CreateStream(ctx, &kinesis.CreateStreamInput{StreamName: aws.String(name), ShardCount: aws.Int32(1)}) //nolint:errcheck
	g.waitStreamActive(ctx, name)                                                                                //nolint:errcheck
	_, err := g.cl().DeleteStream(ctx, &kinesis.DeleteStreamInput{StreamName: aws.String(name)})
	if err != nil {
		return err
	}
	list, lErr := g.cl().ListStreams(ctx, &kinesis.ListStreamsInput{})
	if lErr != nil {
		return nil
	}
	for _, sn := range list.StreamNames {
		if sn == name {
			return fmt.Errorf("DeleteStream: stream %q still present", name)
		}
	}
	return nil
}

// ── kinesis-records ───────────────────────────────────────────────────────────

func (g *kinesisGroup) setupRecords(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-rec-%s", t.RunID)
	if _, err := g.cl().CreateStream(ctx, &kinesis.CreateStreamInput{
		StreamName: aws.String(name), ShardCount: aws.Int32(1),
	}); err != nil {
		return err
	}
	if err := g.waitStreamActive(ctx, name); err != nil {
		return err
	}
	t.Set("kinesis_rec_stream", name)
	return nil
}

func (g *kinesisGroup) teardownRecords(ctx context.Context, t *harness.TestContext) error {
	if name := t.GetString("kinesis_rec_stream"); name != "" {
		g.cl().DeleteStream(ctx, &kinesis.DeleteStreamInput{StreamName: aws.String(name)}) //nolint:errcheck
	}
	return nil
}

func (g *kinesisGroup) PutRecord(ctx context.Context, t *harness.TestContext) error {
	stream := t.GetString("kinesis_rec_stream")
	resp, err := g.cl().PutRecord(ctx, &kinesis.PutRecordInput{
		StreamName:   aws.String(stream),
		Data:         []byte("record-data"),
		PartitionKey: aws.String("pk1"),
	})
	if err != nil {
		return err
	}
	if aws.ToString(resp.ShardId) == "" {
		return fmt.Errorf("PutRecord: missing ShardId in response")
	}
	return nil
}

func (g *kinesisGroup) PutRecords(ctx context.Context, t *harness.TestContext) error {
	stream := t.GetString("kinesis_rec_stream")
	resp, err := g.cl().PutRecords(ctx, &kinesis.PutRecordsInput{
		StreamName: aws.String(stream),
		Records: []types.PutRecordsRequestEntry{
			{Data: []byte("r1"), PartitionKey: aws.String("pk1")},
			{Data: []byte("r2"), PartitionKey: aws.String("pk2")},
		},
	})
	if err != nil {
		return err
	}
	if aws.ToInt32(resp.FailedRecordCount) > 0 {
		return fmt.Errorf("PutRecords: %d records failed", aws.ToInt32(resp.FailedRecordCount))
	}
	return nil
}

func (g *kinesisGroup) GetShardIterator(ctx context.Context, t *harness.TestContext) error {
	stream := t.GetString("kinesis_rec_stream")
	// Get first shard ID
	desc, err := g.cl().DescribeStream(ctx, &kinesis.DescribeStreamInput{StreamName: aws.String(stream)})
	if err != nil {
		return err
	}
	if len(desc.StreamDescription.Shards) == 0 {
		return fmt.Errorf("GetShardIterator: no shards")
	}
	shardID := aws.ToString(desc.StreamDescription.Shards[0].ShardId)
	resp, err := g.cl().GetShardIterator(ctx, &kinesis.GetShardIteratorInput{
		StreamName:        aws.String(stream),
		ShardId:           aws.String(shardID),
		ShardIteratorType: types.ShardIteratorTypeTrimHorizon,
	})
	if err != nil {
		return err
	}
	t.Set("kinesis_shard_iter", aws.ToString(resp.ShardIterator))
	return nil
}

func (g *kinesisGroup) GetRecords(ctx context.Context, t *harness.TestContext) error {
	iter := t.GetString("kinesis_shard_iter")
	if iter == "" {
		return nil
	}
	resp, err := g.cl().GetRecords(ctx, &kinesis.GetRecordsInput{
		ShardIterator: aws.String(iter),
		Limit:         aws.Int32(10),
	})
	if err != nil {
		return err
	}
	if len(resp.Records) == 0 {
		return fmt.Errorf("GetRecords: expected ≥1 record")
	}
	return nil
}

// ── kinesis-shards ────────────────────────────────────────────────────────────

func (g *kinesisGroup) setupShards(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-shd-%s", t.RunID)
	if _, err := g.cl().CreateStream(ctx, &kinesis.CreateStreamInput{
		StreamName: aws.String(name), ShardCount: aws.Int32(2),
	}); err != nil {
		return err
	}
	if err := g.waitStreamActive(ctx, name); err != nil {
		return err
	}
	t.Set("kinesis_shd_stream", name)
	return nil
}

func (g *kinesisGroup) teardownShards(ctx context.Context, t *harness.TestContext) error {
	if name := t.GetString("kinesis_shd_stream"); name != "" {
		g.cl().DeleteStream(ctx, &kinesis.DeleteStreamInput{StreamName: aws.String(name)}) //nolint:errcheck
	}
	return nil
}

func (g *kinesisGroup) ListShards(ctx context.Context, t *harness.TestContext) error {
	stream := t.GetString("kinesis_shd_stream")
	resp, err := g.cl().ListShards(ctx, &kinesis.ListShardsInput{StreamName: aws.String(stream)})
	if err != nil {
		return err
	}
	if len(resp.Shards) < 2 {
		return fmt.Errorf("ListShards: expected ≥2 shards, got %d", len(resp.Shards))
	}
	return nil
}

func (g *kinesisGroup) SplitShard(ctx context.Context, t *harness.TestContext) error {
	stream := t.GetString("kinesis_shd_stream")
	desc, err := g.cl().DescribeStream(ctx, &kinesis.DescribeStreamInput{StreamName: aws.String(stream)})
	if err != nil {
		return err
	}
	if len(desc.StreamDescription.Shards) == 0 {
		return fmt.Errorf("SplitShard: no shards")
	}
	shard := desc.StreamDescription.Shards[0]
	startKey := aws.ToString(shard.HashKeyRange.StartingHashKey)
	endKey := aws.ToString(shard.HashKeyRange.EndingHashKey)

	// compute midpoint as string
	midKey := hashMidpoint(startKey, endKey)
	_, err = g.cl().SplitShard(ctx, &kinesis.SplitShardInput{
		StreamName:         aws.String(stream),
		ShardToSplit:       shard.ShardId,
		NewStartingHashKey: aws.String(midKey),
	})
	if err != nil {
		if harness.IsUnimplemented(err) {
			return nil
		}
		return err
	}
	return g.waitStreamActive(ctx, stream)
}

func (g *kinesisGroup) MergeShards(ctx context.Context, t *harness.TestContext) error {
	stream := t.GetString("kinesis_shd_stream")
	desc, err := g.cl().DescribeStream(ctx, &kinesis.DescribeStreamInput{StreamName: aws.String(stream)})
	if err != nil {
		return err
	}
	shards := desc.StreamDescription.Shards
	// find two adjacent open shards
	openShards := make([]types.Shard, 0, len(shards))
	for _, s := range shards {
		if s.SequenceNumberRange.EndingSequenceNumber == nil {
			openShards = append(openShards, s)
		}
	}
	if len(openShards) < 2 {
		return nil // not enough open shards to merge, skip
	}
	_, err = g.cl().MergeShards(ctx, &kinesis.MergeShardsInput{
		StreamName:           aws.String(stream),
		ShardToMerge:         openShards[0].ShardId,
		AdjacentShardToMerge: openShards[1].ShardId,
	})
	if err != nil {
		if harness.IsUnimplemented(err) {
			return nil
		}
		return err
	}
	return g.waitStreamActive(ctx, stream)
}

// hashMidpoint computes the decimal midpoint between two decimal 128-bit hash key strings.
func hashMidpoint(start, end string) string {
	s := new(big.Int)
	e := new(big.Int)
	two := big.NewInt(2)
	s.SetString(start, 10)
	e.SetString(end, 10)
	mid := new(big.Int).Add(s, e)
	mid.Div(mid, two)
	return mid.String()
}
