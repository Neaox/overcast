package kinesis

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
)

type createStreamRequest struct {
	StreamName string `json:"StreamName"`
	ShardCount int    `json:"ShardCount"`
}

type deleteStreamRequest struct {
	StreamName string `json:"StreamName"`
}

type describeStreamRequest struct {
	StreamName string `json:"StreamName"`
}

type describeStreamResponse struct {
	StreamDescription map[string]any `json:"StreamDescription"`
}

type describeStreamSummaryRequest struct {
	StreamName string `json:"StreamName"`
}

type describeStreamSummaryResponse struct {
	StreamDescriptionSummary map[string]any `json:"StreamDescriptionSummary"`
}

type listStreamsRequest struct {
	Limit int `json:"Limit"`
}

type listStreamsResponse struct {
	StreamNames    []string `json:"StreamNames"`
	HasMoreStreams bool     `json:"HasMoreStreams"`
}

type putRecordRequest struct {
	StreamName   string `json:"StreamName"`
	Data         []byte `json:"Data"`
	PartitionKey string `json:"PartitionKey"`
}

type putRecordResponse struct {
	ShardId        string `json:"ShardId"`
	SequenceNumber string `json:"SequenceNumber"`
}

type putRecordsRequest struct {
	StreamName string           `json:"StreamName"`
	Records    []putRecordsItem `json:"Records"`
}

type putRecordsItem struct {
	Data         []byte `json:"Data"`
	PartitionKey string `json:"PartitionKey"`
}

type putRecordsEntry struct {
	ShardId        string `json:"ShardId"`
	SequenceNumber string `json:"SequenceNumber"`
}

type putRecordsResponse struct {
	FailedRecordCount int               `json:"FailedRecordCount"`
	Records           []putRecordsEntry `json:"Records"`
}

type getShardIteratorRequest struct {
	StreamName             string `json:"StreamName"`
	ShardId                string `json:"ShardId"`
	ShardIteratorType      string `json:"ShardIteratorType"`
	StartingSequenceNumber string `json:"StartingSequenceNumber"`
}

type getShardIteratorResponse struct {
	ShardIterator string `json:"ShardIterator"`
}

type getRecordsRequest struct {
	ShardIterator string `json:"ShardIterator"`
	Limit         int    `json:"Limit"`
}

type recordResponse struct {
	SequenceNumber              string  `json:"SequenceNumber"`
	ApproximateArrivalTimestamp float64 `json:"ApproximateArrivalTimestamp"`
	Data                        []byte  `json:"Data"`
	PartitionKey                string  `json:"PartitionKey"`
}

type getRecordsResponse struct {
	Records            []recordResponse `json:"Records"`
	NextShardIterator  string           `json:"NextShardIterator"`
	MillisBehindLatest int              `json:"MillisBehindLatest"`
}

type listShardsRequest struct {
	StreamName string `json:"StreamName"`
}

type listShardsResponse struct {
	Shards    []map[string]any `json:"Shards"`
	NextToken any              `json:"NextToken"`
}

type splitShardRequest struct {
	StreamName         string `json:"StreamName"`
	ShardToSplit       string `json:"ShardToSplit"`
	NewStartingHashKey string `json:"NewStartingHashKey"`
}

type mergeShardsRequest struct {
	StreamName           string `json:"StreamName"`
	ShardToMerge         string `json:"ShardToMerge"`
	AdjacentShardToMerge string `json:"AdjacentShardToMerge"`
}

type addTagsToStreamRequest struct {
	StreamName string            `json:"StreamName"`
	Tags       map[string]string `json:"Tags"`
}

type listTagsForStreamRequest struct {
	StreamName string `json:"StreamName"`
}

type tagEntry struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

type listTagsForStreamResponse struct {
	Tags        []tagEntry `json:"Tags"`
	HasMoreTags bool       `json:"HasMoreTags"`
}

type removeTagsFromStreamRequest struct {
	StreamName string   `json:"StreamName"`
	TagKeys    []string `json:"TagKeys"`
}

type retentionPeriodRequest struct {
	StreamName           string `json:"StreamName"`
	RetentionPeriodHours int    `json:"RetentionPeriodHours"`
}

func (h *Handler) createStreamTyped(ctx context.Context, req *createStreamRequest) (*struct{}, *protocol.AWSError) {
	if req.StreamName == "" {
		return nil, protocol.ErrMissingParameter("StreamName")
	}
	shardCount := req.ShardCount
	if shardCount <= 0 {
		shardCount = 1
	}
	if _, aerr := h.store.getStream(ctx, req.StreamName); aerr == nil {
		return nil, errStreamAlreadyExists(req.StreamName)
	}
	st := &Stream{
		StreamName:           req.StreamName,
		StreamARN:            streamARN(h.cfg.AccountID, h.cfg.Region, req.StreamName),
		StreamStatus:         "ACTIVE",
		ShardCount:           shardCount,
		Shards:               buildInitialShards(shardCount),
		Tags:                 map[string]string{},
		CreatedAt:            h.clk.Now().UTC(),
		RetentionPeriodHours: 24,
	}
	if aerr := h.store.putStream(ctx, st); aerr != nil {
		return nil, aerr
	}
	h.publishCtx(ctx, events.KinesisStreamCreated, events.ResourcePayload{Name: req.StreamName})
	h.log.Debug("stream created", zap.String("stream", req.StreamName), zap.Int("shards", shardCount))
	return &struct{}{}, nil
}

func (h *Handler) deleteStreamTyped(ctx context.Context, req *deleteStreamRequest) (*struct{}, *protocol.AWSError) {
	if req.StreamName == "" {
		return nil, protocol.ErrMissingParameter("StreamName")
	}
	if _, aerr := h.store.getStream(ctx, req.StreamName); aerr != nil {
		return nil, aerr
	}
	if aerr := h.store.deleteStream(ctx, req.StreamName); aerr != nil {
		return nil, aerr
	}
	h.publishCtx(ctx, events.KinesisStreamDeleted, events.ResourcePayload{Name: req.StreamName})
	return &struct{}{}, nil
}

func (h *Handler) describeStreamTyped(ctx context.Context, req *describeStreamRequest) (*describeStreamResponse, *protocol.AWSError) {
	if req.StreamName == "" {
		return nil, protocol.ErrMissingParameter("StreamName")
	}
	st, aerr := h.store.getStream(ctx, req.StreamName)
	if aerr != nil {
		return nil, aerr
	}
	return &describeStreamResponse{StreamDescription: toStreamDescription(st)}, nil
}

func (h *Handler) describeStreamSummaryTyped(ctx context.Context, req *describeStreamSummaryRequest) (*describeStreamSummaryResponse, *protocol.AWSError) {
	if req.StreamName == "" {
		return nil, protocol.ErrMissingParameter("StreamName")
	}
	st, aerr := h.store.getStream(ctx, req.StreamName)
	if aerr != nil {
		return nil, aerr
	}
	return &describeStreamSummaryResponse{StreamDescriptionSummary: map[string]any{
		"StreamName":              st.StreamName,
		"StreamARN":               st.StreamARN,
		"StreamStatus":            st.StreamStatus,
		"OpenShardCount":          activeShardCount(st),
		"RetentionPeriodHours":    st.RetentionPeriodHours,
		"StreamCreationTimestamp": st.CreatedAt.Unix(),
		"EnhancedMonitoring":      []any{},
	}}, nil
}

func (h *Handler) listStreamsTyped(ctx context.Context, _ *listStreamsRequest) (*listStreamsResponse, *protocol.AWSError) {
	streams, aerr := h.store.listStreams(ctx)
	if aerr != nil {
		return nil, aerr
	}
	names := make([]string, len(streams))
	for i, st := range streams {
		names[i] = st.StreamName
	}
	return &listStreamsResponse{StreamNames: names, HasMoreStreams: false}, nil
}

func (h *Handler) putRecordTyped(ctx context.Context, req *putRecordRequest) (*putRecordResponse, *protocol.AWSError) {
	if req.StreamName == "" {
		return nil, protocol.ErrMissingParameter("StreamName")
	}
	st, aerr := h.store.getStream(ctx, req.StreamName)
	if aerr != nil {
		return nil, aerr
	}
	shardIdx := pickShard(st.Shards, req.PartitionKey)
	shardID := st.Shards[shardIdx].ShardId
	// Persisted per-shard counter, not len(records) — storage-access-plan.md
	// A1: len() regresses after a deletion and can collide with a
	// surviving record's sequence number.
	base, aerr := h.store.nextShardSeqBlock(ctx, req.StreamName, shardID, 1)
	if aerr != nil {
		return nil, aerr
	}
	seqNo := formatSeqNo(shardIdx, base)
	rec := &Record{
		SequenceNumber:              seqNo,
		ApproximateArrivalTimestamp: h.clk.Now().UTC(),
		Data:                        req.Data,
		PartitionKey:                req.PartitionKey,
	}
	if aerr := h.store.putRecord(ctx, req.StreamName, shardID, rec); aerr != nil {
		return nil, aerr
	}
	return &putRecordResponse{ShardId: shardID, SequenceNumber: seqNo}, nil
}

func (h *Handler) putRecordsTyped(ctx context.Context, req *putRecordsRequest) (*putRecordsResponse, *protocol.AWSError) {
	if req.StreamName == "" {
		return nil, protocol.ErrMissingParameter("StreamName")
	}
	st, aerr := h.store.getStream(ctx, req.StreamName)
	if aerr != nil {
		return nil, aerr
	}

	n := len(req.Records)
	shardIdxs := make([]int, n)
	shardIDs := make([]string, n)
	counts := make(map[string]int, n)
	for i, entry := range req.Records {
		idx := pickShard(st.Shards, entry.PartitionKey)
		shardIdxs[i] = idx
		shardIDs[i] = st.Shards[idx].ShardId
		counts[shardIDs[i]]++
	}

	// One contiguous block reservation per shard touched by this batch —
	// not one read-modify-write per record (storage-access-plan.md A1).
	bases := make(map[string]int64, len(counts))
	for shardID, count := range counts {
		base, aerr := h.store.nextShardSeqBlock(ctx, req.StreamName, shardID, count)
		if aerr != nil {
			return nil, aerr
		}
		bases[shardID] = base
	}

	now := h.clk.Now().UTC()
	results := make([]putRecordsEntry, n)
	for i, entry := range req.Records {
		shardID := shardIDs[i]
		seqNo := formatSeqNo(shardIdxs[i], bases[shardID])
		bases[shardID]++
		rec := &Record{
			SequenceNumber:              seqNo,
			ApproximateArrivalTimestamp: now,
			Data:                        entry.Data,
			PartitionKey:                entry.PartitionKey,
		}
		_ = h.store.putRecord(ctx, req.StreamName, shardID, rec)
		results[i] = putRecordsEntry{ShardId: shardID, SequenceNumber: seqNo}
	}
	return &putRecordsResponse{FailedRecordCount: 0, Records: results}, nil
}

func (h *Handler) getShardIteratorTyped(ctx context.Context, req *getShardIteratorRequest) (*getShardIteratorResponse, *protocol.AWSError) {
	if req.StreamName == "" {
		return nil, protocol.ErrMissingParameter("StreamName")
	}
	if _, aerr := h.store.getStream(ctx, req.StreamName); aerr != nil {
		return nil, aerr
	}
	var afterSeqNo string
	switch req.ShardIteratorType {
	case "AT_SEQUENCE_NUMBER":
		afterSeqNo = "before:" + req.StartingSequenceNumber
	case "AFTER_SEQUENCE_NUMBER":
		afterSeqNo = req.StartingSequenceNumber
	case "LATEST":
		afterSeqNo = "LATEST"
	default:
		afterSeqNo = ""
	}
	iter := encodeShardIterator(req.StreamName, req.ShardId, afterSeqNo)
	return &getShardIteratorResponse{ShardIterator: base64.StdEncoding.EncodeToString([]byte(iter))}, nil
}

func (h *Handler) getRecordsTyped(ctx context.Context, req *getRecordsRequest) (*getRecordsResponse, *protocol.AWSError) {
	if req.ShardIterator == "" {
		return nil, protocol.ErrMissingParameter("ShardIterator")
	}
	raw, err := base64.StdEncoding.DecodeString(req.ShardIterator)
	if err != nil {
		return nil, invalidShardIterator("Invalid ShardIterator")
	}
	streamName, shardID, afterSeqNo, ok := decodeShardIterator(string(raw))
	if !ok {
		return nil, invalidShardIterator("Invalid ShardIterator format")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 10000
	}

	var (
		records   []Record
		lastSeqNo string
	)

	switch {
	case afterSeqNo == "LATEST", strings.HasPrefix(afterSeqNo, "before:"):
		// LATEST and AT_SEQUENCE_NUMBER ("before:") iterators only need a
		// full-shard read once, to resolve their starting point on first
		// use — the NextShardIterator this call returns carries a concrete
		// sequence number, so every later poll takes the ScanPage range
		// read below instead. That one-time resolution isn't the hot path
		// A2 targets (repeated polling with a TRIM_HORIZON/
		// AFTER_SEQUENCE_NUMBER-style cursor), so it's deliberately left
		// as a full-shard read (storage-access-plan.md A2).
		allRecords, aerr := h.store.listRecords(ctx, streamName, shardID)
		if aerr != nil {
			return nil, aerr
		}
		if afterSeqNo == "LATEST" {
			if len(allRecords) > 0 {
				lastSeqNo = allRecords[len(allRecords)-1].SequenceNumber
			}
		} else {
			target := afterSeqNo[len("before:"):]
			for _, rec := range allRecords {
				if rec.SequenceNumber >= target {
					records = append(records, rec)
				}
			}
			if len(records) > limit {
				records = records[:limit]
			}
			switch {
			case len(records) > 0:
				lastSeqNo = records[len(records)-1].SequenceNumber
			case len(allRecords) > 0:
				lastSeqNo = allRecords[len(allRecords)-1].SequenceNumber
			default:
				lastSeqNo = afterSeqNo
			}
		}
	default:
		// TRIM_HORIZON ("") or a resolved/resumed cursor: range read via
		// ScanPage instead of a full-shard scan-and-slice
		// (storage-access-plan.md A2, the read-side twin of A1).
		page, aerr := h.store.listRecordsPage(ctx, streamName, shardID, afterSeqNo, limit)
		if aerr != nil {
			return nil, aerr
		}
		records = page
		// No new records past the cursor: keep the cursor exactly where it
		// was (a stable no-op iterator) rather than rewinding to the
		// shard's tail — matches real Kinesis iterators, which never
		// regress or re-deliver already-consumed records.
		lastSeqNo = afterSeqNo
		if len(records) > 0 {
			lastSeqNo = records[len(records)-1].SequenceNumber
		}
	}

	out := make([]recordResponse, len(records))
	for i, rec := range records {
		out[i] = recordResponse{
			SequenceNumber:              rec.SequenceNumber,
			ApproximateArrivalTimestamp: float64(rec.ApproximateArrivalTimestamp.Unix()),
			Data:                        rec.Data,
			PartitionKey:                rec.PartitionKey,
		}
	}
	nextIter := encodeShardIterator(streamName, shardID, lastSeqNo)
	return &getRecordsResponse{
		Records:            out,
		NextShardIterator:  base64.StdEncoding.EncodeToString([]byte(nextIter)),
		MillisBehindLatest: 0,
	}, nil
}

func (h *Handler) listShardsTyped(ctx context.Context, req *listShardsRequest) (*listShardsResponse, *protocol.AWSError) {
	if req.StreamName == "" {
		return nil, protocol.ErrMissingParameter("StreamName")
	}
	st, aerr := h.store.getStream(ctx, req.StreamName)
	if aerr != nil {
		return nil, aerr
	}
	shards := make([]map[string]any, 0, len(st.Shards))
	for _, shard := range st.Shards {
		if shard.SequenceNumberRange.EndingSequenceNumber != "" {
			continue
		}
		shards = append(shards, shardToMap(shard))
	}
	return &listShardsResponse{Shards: shards, NextToken: nil}, nil
}

func (h *Handler) splitShardTyped(ctx context.Context, req *splitShardRequest) (*struct{}, *protocol.AWSError) {
	if req.StreamName == "" {
		return nil, protocol.ErrMissingParameter("StreamName")
	}
	st, aerr := h.store.getStream(ctx, req.StreamName)
	if aerr != nil {
		return nil, aerr
	}
	idx := -1
	for i, shard := range st.Shards {
		if shard.ShardId == req.ShardToSplit && shard.SequenceNumberRange.EndingSequenceNumber == "" {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, shardNotFound(req.ShardToSplit, req.StreamName)
	}
	orig := st.Shards[idx]
	nowSeq := fmt.Sprintf("49%019d", len(st.Shards))
	st.Shards[idx].SequenceNumberRange.EndingSequenceNumber = nowSeq
	child1 := Shard{
		ShardId: fmt.Sprintf("shardId-%012d", len(st.Shards)),
		HashKeyRange: HashKeyRange{
			StartingHashKey: orig.HashKeyRange.StartingHashKey,
			EndingHashKey:   req.NewStartingHashKey,
		},
		SequenceNumberRange: SequenceNumberRange{StartingSequenceNumber: nowSeq},
	}
	child2 := Shard{
		ShardId: fmt.Sprintf("shardId-%012d", len(st.Shards)+1),
		HashKeyRange: HashKeyRange{
			StartingHashKey: req.NewStartingHashKey,
			EndingHashKey:   orig.HashKeyRange.EndingHashKey,
		},
		SequenceNumberRange: SequenceNumberRange{StartingSequenceNumber: nowSeq},
	}
	st.Shards = append(st.Shards, child1, child2)
	st.ShardCount = activeShardCount(st)
	if aerr := h.store.putStream(ctx, st); aerr != nil {
		return nil, aerr
	}
	return &struct{}{}, nil
}

func (h *Handler) mergeShardsTyped(ctx context.Context, req *mergeShardsRequest) (*struct{}, *protocol.AWSError) {
	if req.StreamName == "" {
		return nil, protocol.ErrMissingParameter("StreamName")
	}
	st, aerr := h.store.getStream(ctx, req.StreamName)
	if aerr != nil {
		return nil, aerr
	}
	mergeIdx, adjIdx := -1, -1
	for i, shard := range st.Shards {
		if shard.SequenceNumberRange.EndingSequenceNumber != "" {
			continue
		}
		if shard.ShardId == req.ShardToMerge {
			mergeIdx = i
		}
		if shard.ShardId == req.AdjacentShardToMerge {
			adjIdx = i
		}
	}
	if mergeIdx < 0 {
		return nil, shardNotFound(req.ShardToMerge, req.StreamName)
	}
	if adjIdx < 0 {
		return nil, shardNotFound(req.AdjacentShardToMerge, req.StreamName)
	}
	nowSeq := fmt.Sprintf("49%019d", len(st.Shards))
	st.Shards[mergeIdx].SequenceNumberRange.EndingSequenceNumber = nowSeq
	st.Shards[adjIdx].SequenceNumberRange.EndingSequenceNumber = nowSeq
	startHash := st.Shards[mergeIdx].HashKeyRange.StartingHashKey
	endHash := st.Shards[mergeIdx].HashKeyRange.EndingHashKey
	adjStart := st.Shards[adjIdx].HashKeyRange.StartingHashKey
	adjEnd := st.Shards[adjIdx].HashKeyRange.EndingHashKey
	if cmpHashKey(adjStart, startHash) < 0 {
		startHash = adjStart
	}
	if cmpHashKey(adjEnd, endHash) > 0 {
		endHash = adjEnd
	}
	merged := Shard{
		ShardId: fmt.Sprintf("shardId-%012d", len(st.Shards)),
		HashKeyRange: HashKeyRange{
			StartingHashKey: startHash,
			EndingHashKey:   endHash,
		},
		SequenceNumberRange: SequenceNumberRange{StartingSequenceNumber: nowSeq},
	}
	st.Shards = append(st.Shards, merged)
	st.ShardCount = activeShardCount(st)
	if aerr := h.store.putStream(ctx, st); aerr != nil {
		return nil, aerr
	}
	return &struct{}{}, nil
}

func (h *Handler) addTagsToStreamTyped(ctx context.Context, req *addTagsToStreamRequest) (*struct{}, *protocol.AWSError) {
	if req.StreamName == "" {
		return nil, protocol.ErrMissingParameter("StreamName")
	}
	st, aerr := h.store.getStream(ctx, req.StreamName)
	if aerr != nil {
		return nil, aerr
	}
	if st.Tags == nil {
		st.Tags = map[string]string{}
	}
	for k, v := range req.Tags {
		st.Tags[k] = v
	}
	if aerr := h.store.putStream(ctx, st); aerr != nil {
		return nil, aerr
	}
	return &struct{}{}, nil
}

func (h *Handler) listTagsForStreamTyped(ctx context.Context, req *listTagsForStreamRequest) (*listTagsForStreamResponse, *protocol.AWSError) {
	if req.StreamName == "" {
		return nil, protocol.ErrMissingParameter("StreamName")
	}
	st, aerr := h.store.getStream(ctx, req.StreamName)
	if aerr != nil {
		return nil, aerr
	}
	tags := make([]tagEntry, 0, len(st.Tags))
	for k, v := range st.Tags {
		tags = append(tags, tagEntry{Key: k, Value: v})
	}
	return &listTagsForStreamResponse{Tags: tags, HasMoreTags: false}, nil
}

func (h *Handler) removeTagsFromStreamTyped(ctx context.Context, req *removeTagsFromStreamRequest) (*struct{}, *protocol.AWSError) {
	if req.StreamName == "" {
		return nil, protocol.ErrMissingParameter("StreamName")
	}
	st, aerr := h.store.getStream(ctx, req.StreamName)
	if aerr != nil {
		return nil, aerr
	}
	for _, k := range req.TagKeys {
		delete(st.Tags, k)
	}
	if aerr := h.store.putStream(ctx, st); aerr != nil {
		return nil, aerr
	}
	return &struct{}{}, nil
}

func (h *Handler) increaseStreamRetentionPeriodTyped(ctx context.Context, req *retentionPeriodRequest) (*struct{}, *protocol.AWSError) {
	return h.updateRetentionPeriod(ctx, req)
}

func (h *Handler) decreaseStreamRetentionPeriodTyped(ctx context.Context, req *retentionPeriodRequest) (*struct{}, *protocol.AWSError) {
	return h.updateRetentionPeriod(ctx, req)
}

func (h *Handler) updateRetentionPeriod(ctx context.Context, req *retentionPeriodRequest) (*struct{}, *protocol.AWSError) {
	if req.StreamName == "" {
		return nil, protocol.ErrMissingParameter("StreamName")
	}
	st, aerr := h.store.getStream(ctx, req.StreamName)
	if aerr != nil {
		return nil, aerr
	}
	st.RetentionPeriodHours = req.RetentionPeriodHours
	if aerr := h.store.putStream(ctx, st); aerr != nil {
		return nil, aerr
	}
	return &struct{}{}, nil
}

func (h *Handler) publishCtx(ctx context.Context, t events.Type, payload any) {
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: t, Payload: payload})
	}
}

func invalidShardIterator(message string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "InvalidArgumentException",
		Message:    message,
		HTTPStatus: http.StatusBadRequest,
	}
}

func shardNotFound(shardID, streamName string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ResourceNotFoundException",
		Message:    fmt.Sprintf("Could not find shard %s in stream %s", shardID, streamName),
		HTTPStatus: http.StatusNotFound,
	}
}
