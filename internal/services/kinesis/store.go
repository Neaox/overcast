package kinesis

// store.go — state access for the Kinesis emulator.
// All domain types and serialisation live here.

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	nsStreams = "kinesis:streams"
	nsRecords = "kinesis:records" // key: "<streamName>/<shardId>/<seqNo>"
)

// Stream represents a Kinesis Data Stream.
type Stream struct {
	StreamName           string            `json:"StreamName"`
	StreamARN            string            `json:"StreamARN"`
	StreamStatus         string            `json:"StreamStatus"` // CREATING, ACTIVE, UPDATING, DELETING
	ShardCount           int               `json:"ShardCount"`
	Shards               []Shard           `json:"Shards"`
	Tags                 map[string]string `json:"Tags,omitempty"`
	CreatedAt            time.Time         `json:"CreatedAt"`
	RetentionPeriodHours int               `json:"RetentionPeriodHours"`
}

// Shard represents a Kinesis shard.
type Shard struct {
	ShardId             string              `json:"ShardId"`
	HashKeyRange        HashKeyRange        `json:"HashKeyRange"`
	SequenceNumberRange SequenceNumberRange `json:"SequenceNumberRange"`
}

// HashKeyRange defines the hash key range of a shard.
type HashKeyRange struct {
	StartingHashKey string `json:"StartingHashKey"`
	EndingHashKey   string `json:"EndingHashKey"`
}

// SequenceNumberRange holds sequence numbers for a shard.
type SequenceNumberRange struct {
	StartingSequenceNumber string `json:"StartingSequenceNumber"`
	EndingSequenceNumber   string `json:"EndingSequenceNumber,omitempty"`
}

// Record is a stored Kinesis record.
type Record struct {
	SequenceNumber              string    `json:"SequenceNumber"`
	ApproximateArrivalTimestamp time.Time `json:"ApproximateArrivalTimestamp"`
	Data                        []byte    `json:"Data"`
	PartitionKey                string    `json:"PartitionKey"`
}

// kinesisStore wraps state.Store with Kinesis-specific helpers.
type kinesisStore struct {
	store         state.Store
	defaultRegion string
}

func newKinesisStore(store state.Store, defaultRegion string) *kinesisStore {
	return &kinesisStore{store: store, defaultRegion: defaultRegion}
}

// region extracts the per-request region from context, falling back to the default.
func (s *kinesisStore) region(ctx context.Context) string {
	return middleware.RegionFromContext(ctx, s.defaultRegion)
}

// ---- Stream operations -------------------------------------------------------

func (s *kinesisStore) getStream(ctx context.Context, name string) (*Stream, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsStreams, serviceutil.RegionKey(s.region(ctx), name))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, errNoSuchStream(name)
	}
	var st Stream
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &st, nil
}

func (s *kinesisStore) putStream(ctx context.Context, st *Stream) *protocol.AWSError {
	raw, err := json.Marshal(st)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsStreams, serviceutil.RegionKey(s.region(ctx), st.StreamName), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *kinesisStore) deleteStream(ctx context.Context, name string) *protocol.AWSError {
	// Delete all records for each shard
	region := s.region(ctx)
	keys, err := s.store.List(ctx, nsRecords, serviceutil.RegionKey(region, name+"/"))
	if err == nil {
		for _, k := range keys {
			_ = s.store.Delete(ctx, nsRecords, k)
		}
	}
	if err := s.store.Delete(ctx, nsStreams, serviceutil.RegionKey(region, name)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *kinesisStore) listStreams(ctx context.Context) ([]Stream, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsStreams, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	streams := make([]Stream, 0, len(pairs))
	for _, p := range pairs {
		var st Stream
		if err := json.Unmarshal([]byte(p.Value), &st); err != nil {
			continue
		}
		streams = append(streams, st)
	}
	sort.Slice(streams, func(i, j int) bool { return streams[i].StreamName < streams[j].StreamName })
	return streams, nil
}

// ---- Record operations -------------------------------------------------------

func recordStoreKey(streamName, shardID, seqNo string) string {
	return streamName + "/" + shardID + "/" + seqNo
}

func (s *kinesisStore) putRecord(ctx context.Context, streamName, shardID string, rec *Record) *protocol.AWSError {
	raw, err := json.Marshal(rec)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	key := serviceutil.RegionKey(s.region(ctx), recordStoreKey(streamName, shardID, rec.SequenceNumber))
	if err := s.store.Set(ctx, nsRecords, key, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *kinesisStore) listRecords(ctx context.Context, streamName, shardID string) ([]Record, *protocol.AWSError) {
	prefix := serviceutil.RegionKey(s.region(ctx), streamName+"/"+shardID+"/")
	keys, err := s.store.List(ctx, nsRecords, prefix)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	records := make([]Record, 0, len(keys))
	for _, k := range keys {
		raw, found, err := s.store.Get(ctx, nsRecords, k)
		if err != nil || !found {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			return nil, protocol.Wrap(protocol.ErrInternalError, err)
		}
		records = append(records, rec)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].SequenceNumber < records[j].SequenceNumber
	})
	return records, nil
}

// ---- Helpers ----------------------------------------------------------------

// buildInitialShards returns evenly distributed shards for a new stream.
// The 128-bit hash space (0 to 2^128-1) is divided equally among shardCount shards.
func buildInitialShards(shardCount int) []Shard {
	maxHash := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
	shards := make([]Shard, shardCount)
	for i := 0; i < shardCount; i++ {
		start := new(big.Int).Div(new(big.Int).Mul(maxHash, big.NewInt(int64(i))), big.NewInt(int64(shardCount)))
		var end *big.Int
		if i == shardCount-1 {
			end = new(big.Int).Set(maxHash)
		} else {
			end = new(big.Int).Div(new(big.Int).Mul(maxHash, big.NewInt(int64(i+1))), big.NewInt(int64(shardCount)))
			end.Sub(end, big.NewInt(1))
		}
		shards[i] = Shard{
			ShardId: fmt.Sprintf("shardId-%012d", i),
			HashKeyRange: HashKeyRange{
				StartingHashKey: start.String(),
				EndingHashKey:   end.String(),
			},
			SequenceNumberRange: SequenceNumberRange{
				StartingSequenceNumber: fmt.Sprintf("49%019d", i),
			},
		}
	}
	return shards
}

// pickShard selects the shard for a given partition key using MD5 hash (same as AWS).
func pickShard(shards []Shard, partitionKey string) int {
	// Simple deterministic mapping: sum of bytes mod shard count.
	var sum int
	for _, b := range []byte(partitionKey) {
		sum += int(b)
	}
	for i, shard := range shards {
		if shard.SequenceNumberRange.EndingSequenceNumber == "" {
			// Only pick active shards.
			_ = i
		}
	}
	// Filter only active shards (no EndingSequenceNumber).
	var active []int
	for i, shard := range shards {
		if shard.SequenceNumberRange.EndingSequenceNumber == "" {
			active = append(active, i)
		}
	}
	if len(active) == 0 {
		return 0
	}
	return active[sum%len(active)]
}

// nextSeqNo generates a monotonically increasing sequence number for a shard.
func nextSeqNo(existing []Record, shardIdx int) string {
	n := len(existing)
	return fmt.Sprintf("49%019d%010d", shardIdx, n)
}

func streamARN(accountID, region, name string) string {
	return fmt.Sprintf("arn:aws:kinesis:%s:%s:stream/%s", region, accountID, name)
}

// errNoSuchStream returns an AWS-flavoured error for a missing stream.
func errNoSuchStream(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ResourceNotFoundException",
		Message:    fmt.Sprintf("Stream %s under account not found.", name),
		HTTPStatus: http.StatusNotFound,
	}
}

// errStreamAlreadyExists returns an AWS-flavoured error for a duplicate stream.
func errStreamAlreadyExists(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ResourceInUseException",
		Message:    fmt.Sprintf("Stream %s under account already exists.", name),
		HTTPStatus: http.StatusBadRequest,
	}
}

// iteratorKey encodes a shard iterator as a stable, opaque string.
// Format: "<streamName>|<shardId>|<nextSeqNo>".
func encodeShardIterator(streamName, shardID, afterSeqNo string) string {
	return streamName + "|" + shardID + "|" + afterSeqNo
}

func decodeShardIterator(iter string) (streamName, shardID, afterSeqNo string, ok bool) {
	parts := strings.SplitN(iter, "|", 3)
	if len(parts) != 3 {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}
