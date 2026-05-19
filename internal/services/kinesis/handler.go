package kinesis

// handler.go — HTTP handlers for the Kinesis emulator.
// Implements stream lifecycle, record put/get, shard management, and tagging.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// Handler holds all dependencies for Kinesis HTTP handlers.
type Handler struct {
	ops     map[string]http.HandlerFunc
	typedOp map[string]op.Operation
	cfg     *config.Config
	store   *kinesisStore
	log     *serviceutil.ServiceLogger
	clk     clock.Clock
	bus     *events.Bus
}

func newHandler(cfg *config.Config, store *kinesisStore, log *serviceutil.ServiceLogger, clk clock.Clock) *Handler {
	h := &Handler{cfg: cfg, store: store, log: log, clk: clk}
	h.initOps()
	return h
}

// initOps registers every known Kinesis operation to its handler.
// Implemented operations point to their handler method.
// Adding a new operation: add an entry here and implement it.
func (h *Handler) initOps() {
	h.ops = map[string]http.HandlerFunc{
		"CreateStream":                  h.CreateStream,
		"DeleteStream":                  h.DeleteStream,
		"DescribeStream":                h.DescribeStream,
		"DescribeStreamSummary":         h.DescribeStreamSummary,
		"ListStreams":                   h.ListStreams,
		"PutRecord":                     h.PutRecord,
		"PutRecords":                    h.PutRecords,
		"GetShardIterator":              h.GetShardIterator,
		"GetRecords":                    h.GetRecords,
		"ListShards":                    h.ListShards,
		"SplitShard":                    h.SplitShard,
		"AddTagsToStream":               h.AddTagsToStream,
		"ListTagsForStream":             h.ListTagsForStream,
		"RemoveTagsFromStream":          h.RemoveTagsFromStream,
		"MergeShards":                   h.MergeShards,
		"IncreaseStreamRetentionPeriod": h.IncreaseStreamRetentionPeriod,
		"DecreaseStreamRetentionPeriod": h.DecreaseStreamRetentionPeriod,
	}
	h.typedOp = h.typedOps()
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	protocol.WriteAWSJSON(w, r, status, v, "application/x-amz-json-1.1")
}

// decodeJSON decodes the request body into v. Returns false and writes an error if it fails.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "SerializationException",
			Message:    "Failed to deserialize the message: " + err.Error(),
			HTTPStatus: http.StatusBadRequest,
		})
		return false
	}
	return true
}

// ---- Stream lifecycle --------------------------------------------------------

// publish emits an event if the bus is wired.
func (h *Handler) publish(r *http.Request, t events.Type, payload any) {
	if h.bus != nil {
		h.bus.Publish(r.Context(), events.Event{Type: t, Payload: payload})
	}
}

// CreateStream handles Kinesis_20131202.CreateStream.
// AWS docs: https://docs.aws.amazon.com/kinesis/latest/APIReference/API_CreateStream.html
func (h *Handler) CreateStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName string `json:"StreamName"`
		ShardCount int    `json:"ShardCount"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.StreamName == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("StreamName"))
		return
	}
	shardCount := req.ShardCount
	if shardCount <= 0 {
		shardCount = 1
	}

	if _, aerr := h.store.getStream(r.Context(), req.StreamName); aerr == nil {
		protocol.WriteJSONError(w, r, errStreamAlreadyExists(req.StreamName))
		return
	}

	st := &Stream{
		StreamName:           req.StreamName,
		StreamARN:            streamARN(h.cfg.AccountID, middleware.RegionFromContext(r.Context(), h.cfg.Region), req.StreamName),
		StreamStatus:         "ACTIVE",
		ShardCount:           shardCount,
		Shards:               buildInitialShards(shardCount),
		Tags:                 map[string]string{},
		CreatedAt:            h.clk.Now().UTC(),
		RetentionPeriodHours: 24,
	}
	if aerr := h.store.putStream(r.Context(), st); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	h.publish(r, events.KinesisStreamCreated, events.ResourcePayload{Name: req.StreamName})
	h.log.Debug("stream created", zap.String("stream", req.StreamName), zap.Int("shards", shardCount))
	w.WriteHeader(http.StatusOK)
}

// DeleteStream handles Kinesis_20131202.DeleteStream.
// AWS docs: https://docs.aws.amazon.com/kinesis/latest/APIReference/API_DeleteStream.html
func (h *Handler) DeleteStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName string `json:"StreamName"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.StreamName == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("StreamName"))
		return
	}
	if _, aerr := h.store.getStream(r.Context(), req.StreamName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if aerr := h.store.deleteStream(r.Context(), req.StreamName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	h.publish(r, events.KinesisStreamDeleted, events.ResourcePayload{Name: req.StreamName})
	w.WriteHeader(http.StatusOK)
}

// DescribeStream handles Kinesis_20131202.DescribeStream.
// AWS docs: https://docs.aws.amazon.com/kinesis/latest/APIReference/API_DescribeStream.html
func (h *Handler) DescribeStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName string `json:"StreamName"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.StreamName == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("StreamName"))
		return
	}
	st, aerr := h.store.getStream(r.Context(), req.StreamName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{
		"StreamDescription": toStreamDescription(st),
	})
}

// DescribeStreamSummary handles Kinesis_20131202.DescribeStreamSummary.
// AWS docs: https://docs.aws.amazon.com/kinesis/latest/APIReference/API_DescribeStreamSummary.html
func (h *Handler) DescribeStreamSummary(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName string `json:"StreamName"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.StreamName == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("StreamName"))
		return
	}
	st, aerr := h.store.getStream(r.Context(), req.StreamName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{
		"StreamDescriptionSummary": map[string]any{
			"StreamName":              st.StreamName,
			"StreamARN":               st.StreamARN,
			"StreamStatus":            st.StreamStatus,
			"OpenShardCount":          activeShardCount(st),
			"RetentionPeriodHours":    st.RetentionPeriodHours,
			"StreamCreationTimestamp": st.CreatedAt.Unix(),
			"EnhancedMonitoring":      []any{},
		},
	})
}

// ListStreams handles Kinesis_20131202.ListStreams.
// AWS docs: https://docs.aws.amazon.com/kinesis/latest/APIReference/API_ListStreams.html
func (h *Handler) ListStreams(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Limit int `json:"Limit"`
	}
	// Body may be empty — ignore decode errors here.
	_ = json.NewDecoder(r.Body).Decode(&req)

	streams, aerr := h.store.listStreams(r.Context())
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	names := make([]string, len(streams))
	for i, st := range streams {
		names[i] = st.StreamName
	}
	writeJSON(w, r, http.StatusOK, map[string]any{
		"StreamNames":    names,
		"HasMoreStreams": false,
	})
}

// ---- Records -----------------------------------------------------------------

// PutRecord handles Kinesis_20131202.PutRecord.
// AWS docs: https://docs.aws.amazon.com/kinesis/latest/APIReference/API_PutRecord.html
func (h *Handler) PutRecord(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName   string `json:"StreamName"`
		Data         []byte `json:"Data"`
		PartitionKey string `json:"PartitionKey"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.StreamName == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("StreamName"))
		return
	}
	st, aerr := h.store.getStream(r.Context(), req.StreamName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	shardIdx := pickShard(st.Shards, req.PartitionKey)
	shardID := st.Shards[shardIdx].ShardId

	existing, aerr := h.store.listRecords(r.Context(), req.StreamName, shardID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	seqNo := nextSeqNo(existing, shardIdx)
	rec := &Record{
		SequenceNumber:              seqNo,
		ApproximateArrivalTimestamp: h.clk.Now().UTC(),
		Data:                        req.Data,
		PartitionKey:                req.PartitionKey,
	}
	if aerr := h.store.putRecord(r.Context(), req.StreamName, shardID, rec); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{
		"ShardId":        shardID,
		"SequenceNumber": seqNo,
	})
}

// PutRecords handles Kinesis_20131202.PutRecords.
// AWS docs: https://docs.aws.amazon.com/kinesis/latest/APIReference/API_PutRecords.html
func (h *Handler) PutRecords(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName string `json:"StreamName"`
		Records    []struct {
			Data         []byte `json:"Data"`
			PartitionKey string `json:"PartitionKey"`
		} `json:"Records"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.StreamName == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("StreamName"))
		return
	}
	st, aerr := h.store.getStream(r.Context(), req.StreamName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	type resultEntry struct {
		ShardId        string `json:"ShardId"`
		SequenceNumber string `json:"SequenceNumber"`
	}
	results := make([]resultEntry, 0, len(req.Records))
	now := h.clk.Now().UTC()
	for _, entry := range req.Records {
		shardIdx := pickShard(st.Shards, entry.PartitionKey)
		shardID := st.Shards[shardIdx].ShardId
		existing, _ := h.store.listRecords(r.Context(), req.StreamName, shardID)
		seqNo := nextSeqNo(existing, shardIdx)
		rec := &Record{
			SequenceNumber:              seqNo,
			ApproximateArrivalTimestamp: now,
			Data:                        entry.Data,
			PartitionKey:                entry.PartitionKey,
		}
		_ = h.store.putRecord(r.Context(), req.StreamName, shardID, rec)
		results = append(results, resultEntry{ShardId: shardID, SequenceNumber: seqNo})
	}
	writeJSON(w, r, http.StatusOK, map[string]any{
		"FailedRecordCount": 0,
		"Records":           results,
	})
}

// GetShardIterator handles Kinesis_20131202.GetShardIterator.
// AWS docs: https://docs.aws.amazon.com/kinesis/latest/APIReference/API_GetShardIterator.html
func (h *Handler) GetShardIterator(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName             string `json:"StreamName"`
		ShardId                string `json:"ShardId"`
		ShardIteratorType      string `json:"ShardIteratorType"`
		StartingSequenceNumber string `json:"StartingSequenceNumber"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.StreamName == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("StreamName"))
		return
	}
	if _, aerr := h.store.getStream(r.Context(), req.StreamName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// afterSeqNo: the iterator will return records AFTER this sequence number.
	// "" means start from the very beginning (TRIM_HORIZON).
	var afterSeqNo string
	switch req.ShardIteratorType {
	case "AT_SEQUENCE_NUMBER":
		// return records starting AT this sequence number — so afterSeqNo is one before
		afterSeqNo = "before:" + req.StartingSequenceNumber
	case "AFTER_SEQUENCE_NUMBER":
		afterSeqNo = req.StartingSequenceNumber
	case "LATEST":
		// Encode a magic value so GetRecords returns nothing but a valid iterator.
		afterSeqNo = "LATEST"
	default: // TRIM_HORIZON and anything else: start from beginning
		afterSeqNo = ""
	}

	iter := encodeShardIterator(req.StreamName, req.ShardId, afterSeqNo)
	encoded := base64.StdEncoding.EncodeToString([]byte(iter))
	writeJSON(w, r, http.StatusOK, map[string]any{
		"ShardIterator": encoded,
	})
}

// GetRecords handles Kinesis_20131202.GetRecords.
// AWS docs: https://docs.aws.amazon.com/kinesis/latest/APIReference/API_GetRecords.html
func (h *Handler) GetRecords(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ShardIterator string `json:"ShardIterator"`
		Limit         int    `json:"Limit"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ShardIterator == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("ShardIterator"))
		return
	}

	raw, err := base64.StdEncoding.DecodeString(req.ShardIterator)
	if err != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidArgumentException",
			Message:    "Invalid ShardIterator",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	streamName, shardID, afterSeqNo, ok := decodeShardIterator(string(raw))
	if !ok {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidArgumentException",
			Message:    "Invalid ShardIterator format",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	allRecords, aerr := h.store.listRecords(r.Context(), streamName, shardID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 10000
	}

	// Filter by position.
	var filtered []Record
	if afterSeqNo == "LATEST" {
		// LATEST: no records on first call, advance past all existing.
		// afterSeqNo for next iterator is last known seqNo.
		filtered = nil
		if len(allRecords) > 0 {
			afterSeqNo = allRecords[len(allRecords)-1].SequenceNumber
		}
	} else {
		for _, rec := range allRecords {
			if afterSeqNo == "" || rec.SequenceNumber > afterSeqNo {
				filtered = append(filtered, rec)
			} else if len(afterSeqNo) > 7 && afterSeqNo[:7] == "before:" {
				// AT_SEQUENCE_NUMBER: include the named sequence number itself.
				target := afterSeqNo[7:]
				if rec.SequenceNumber >= target {
					filtered = append(filtered, rec)
				}
			}
		}
	}

	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	type recordJSON struct {
		SequenceNumber              string  `json:"SequenceNumber"`
		ApproximateArrivalTimestamp float64 `json:"ApproximateArrivalTimestamp"`
		Data                        []byte  `json:"Data"`
		PartitionKey                string  `json:"PartitionKey"`
	}
	out := make([]recordJSON, len(filtered))
	var lastSeqNo string
	for i, rec := range filtered {
		out[i] = recordJSON{
			SequenceNumber:              rec.SequenceNumber,
			ApproximateArrivalTimestamp: float64(rec.ApproximateArrivalTimestamp.Unix()),
			Data:                        rec.Data,
			PartitionKey:                rec.PartitionKey,
		}
		lastSeqNo = rec.SequenceNumber
	}
	if lastSeqNo == "" && len(allRecords) > 0 {
		lastSeqNo = allRecords[len(allRecords)-1].SequenceNumber
	}
	if lastSeqNo == "" {
		lastSeqNo = afterSeqNo
	}

	// Build next iterator pointing past the last returned record.
	nextIter := encodeShardIterator(streamName, shardID, lastSeqNo)
	nextEncoded := base64.StdEncoding.EncodeToString([]byte(nextIter))

	writeJSON(w, r, http.StatusOK, map[string]any{
		"Records":            out,
		"NextShardIterator":  nextEncoded,
		"MillisBehindLatest": 0,
	})
}

// ---- Shards ------------------------------------------------------------------

// ListShards handles Kinesis_20131202.ListShards.
// AWS docs: https://docs.aws.amazon.com/kinesis/latest/APIReference/API_ListShards.html
func (h *Handler) ListShards(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName string `json:"StreamName"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.StreamName == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("StreamName"))
		return
	}
	st, aerr := h.store.getStream(r.Context(), req.StreamName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	shards := make([]map[string]any, 0, len(st.Shards))
	for _, shard := range st.Shards {
		if shard.SequenceNumberRange.EndingSequenceNumber != "" {
			continue // closed shard — omit from active listing
		}
		shards = append(shards, shardToMap(shard))
	}
	writeJSON(w, r, http.StatusOK, map[string]any{
		"Shards":    shards,
		"NextToken": nil,
	})
}

// SplitShard handles Kinesis_20131202.SplitShard.
// AWS docs: https://docs.aws.amazon.com/kinesis/latest/APIReference/API_SplitShard.html
func (h *Handler) SplitShard(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName         string `json:"StreamName"`
		ShardToSplit       string `json:"ShardToSplit"`
		NewStartingHashKey string `json:"NewStartingHashKey"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.StreamName == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("StreamName"))
		return
	}
	st, aerr := h.store.getStream(r.Context(), req.StreamName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Find the shard to split.
	idx := -1
	for i, shard := range st.Shards {
		if shard.ShardId == req.ShardToSplit && shard.SequenceNumberRange.EndingSequenceNumber == "" {
			idx = i
			break
		}
	}
	if idx < 0 {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Could not find shard %s in stream %s", req.ShardToSplit, req.StreamName),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	orig := st.Shards[idx]
	nowSeq := fmt.Sprintf("49%019d", len(st.Shards))

	// Close the original shard.
	st.Shards[idx].SequenceNumberRange.EndingSequenceNumber = nowSeq

	// Create two child shards.
	child1 := Shard{
		ShardId: fmt.Sprintf("shardId-%012d", len(st.Shards)),
		HashKeyRange: HashKeyRange{
			StartingHashKey: orig.HashKeyRange.StartingHashKey,
			EndingHashKey:   req.NewStartingHashKey,
		},
		SequenceNumberRange: SequenceNumberRange{
			StartingSequenceNumber: nowSeq,
		},
	}
	child2 := Shard{
		ShardId: fmt.Sprintf("shardId-%012d", len(st.Shards)+1),
		HashKeyRange: HashKeyRange{
			StartingHashKey: req.NewStartingHashKey,
			EndingHashKey:   orig.HashKeyRange.EndingHashKey,
		},
		SequenceNumberRange: SequenceNumberRange{
			StartingSequenceNumber: nowSeq,
		},
	}
	st.Shards = append(st.Shards, child1, child2)
	st.ShardCount = activeShardCount(st)

	if aerr := h.store.putStream(r.Context(), st); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// MergeShards handles Kinesis_20131202.MergeShards (basic implementation).
func (h *Handler) MergeShards(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName           string `json:"StreamName"`
		ShardToMerge         string `json:"ShardToMerge"`
		AdjacentShardToMerge string `json:"AdjacentShardToMerge"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.StreamName == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("StreamName"))
		return
	}
	st, aerr := h.store.getStream(r.Context(), req.StreamName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Find the two shards to merge — both must be open (no EndingSequenceNumber).
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
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Could not find shard %s in stream %s", req.ShardToMerge, req.StreamName),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	if adjIdx < 0 {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Could not find shard %s in stream %s", req.AdjacentShardToMerge, req.StreamName),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	nowSeq := fmt.Sprintf("49%019d", len(st.Shards))

	// Close both parent shards.
	st.Shards[mergeIdx].SequenceNumberRange.EndingSequenceNumber = nowSeq
	st.Shards[adjIdx].SequenceNumberRange.EndingSequenceNumber = nowSeq

	// Create a merged child shard covering the combined hash key range.
	startHash := st.Shards[mergeIdx].HashKeyRange.StartingHashKey
	endHash := st.Shards[mergeIdx].HashKeyRange.EndingHashKey
	adjStart := st.Shards[adjIdx].HashKeyRange.StartingHashKey
	adjEnd := st.Shards[adjIdx].HashKeyRange.EndingHashKey

	// Use the smallest starting and largest ending hash key.
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
		SequenceNumberRange: SequenceNumberRange{
			StartingSequenceNumber: nowSeq,
		},
	}
	st.Shards = append(st.Shards, merged)
	st.ShardCount = activeShardCount(st)

	if aerr := h.store.putStream(r.Context(), st); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ---- Tags --------------------------------------------------------------------

// AddTagsToStream handles Kinesis_20131202.AddTagsToStream.
// AWS docs: https://docs.aws.amazon.com/kinesis/latest/APIReference/API_AddTagsToStream.html
func (h *Handler) AddTagsToStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName string            `json:"StreamName"`
		Tags       map[string]string `json:"Tags"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.StreamName == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("StreamName"))
		return
	}
	st, aerr := h.store.getStream(r.Context(), req.StreamName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if st.Tags == nil {
		st.Tags = map[string]string{}
	}
	for k, v := range req.Tags {
		st.Tags[k] = v
	}
	if aerr := h.store.putStream(r.Context(), st); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ListTagsForStream handles Kinesis_20131202.ListTagsForStream.
// AWS docs: https://docs.aws.amazon.com/kinesis/latest/APIReference/API_ListTagsForStream.html
func (h *Handler) ListTagsForStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName string `json:"StreamName"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.StreamName == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("StreamName"))
		return
	}
	st, aerr := h.store.getStream(r.Context(), req.StreamName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	type tagEntry struct {
		Key   string `json:"Key"`
		Value string `json:"Value"`
	}
	tags := make([]tagEntry, 0, len(st.Tags))
	for k, v := range st.Tags {
		tags = append(tags, tagEntry{Key: k, Value: v})
	}
	writeJSON(w, r, http.StatusOK, map[string]any{
		"Tags":        tags,
		"HasMoreTags": false,
	})
}

// RemoveTagsFromStream handles Kinesis_20131202.RemoveTagsFromStream.
func (h *Handler) RemoveTagsFromStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName string   `json:"StreamName"`
		TagKeys    []string `json:"TagKeys"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.StreamName == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("StreamName"))
		return
	}
	st, aerr := h.store.getStream(r.Context(), req.StreamName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	for _, k := range req.TagKeys {
		delete(st.Tags, k)
	}
	if aerr := h.store.putStream(r.Context(), st); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ---- Retention ---------------------------------------------------------------

// IncreaseStreamRetentionPeriod handles Kinesis_20131202.IncreaseStreamRetentionPeriod.
func (h *Handler) IncreaseStreamRetentionPeriod(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName           string `json:"StreamName"`
		RetentionPeriodHours int    `json:"RetentionPeriodHours"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.StreamName == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("StreamName"))
		return
	}
	st, aerr := h.store.getStream(r.Context(), req.StreamName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	st.RetentionPeriodHours = req.RetentionPeriodHours
	if aerr := h.store.putStream(r.Context(), st); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// DecreaseStreamRetentionPeriod handles Kinesis_20131202.DecreaseStreamRetentionPeriod.
func (h *Handler) DecreaseStreamRetentionPeriod(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName           string `json:"StreamName"`
		RetentionPeriodHours int    `json:"RetentionPeriodHours"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.StreamName == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("StreamName"))
		return
	}
	st, aerr := h.store.getStream(r.Context(), req.StreamName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	st.RetentionPeriodHours = req.RetentionPeriodHours
	if aerr := h.store.putStream(r.Context(), st); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ---- Response helpers --------------------------------------------------------

func toStreamDescription(st *Stream) map[string]any {
	shards := make([]map[string]any, 0, len(st.Shards))
	for _, shard := range st.Shards {
		shards = append(shards, shardToMap(shard))
	}
	return map[string]any{
		"StreamName":              st.StreamName,
		"StreamARN":               st.StreamARN,
		"StreamStatus":            st.StreamStatus,
		"Shards":                  shards,
		"HasMoreShards":           false,
		"RetentionPeriodHours":    st.RetentionPeriodHours,
		"StreamCreationTimestamp": st.CreatedAt.Unix(),
		"EnhancedMonitoring":      []any{},
	}
}

func shardToMap(shard Shard) map[string]any {
	m := map[string]any{
		"ShardId": shard.ShardId,
		"HashKeyRange": map[string]any{
			"StartingHashKey": shard.HashKeyRange.StartingHashKey,
			"EndingHashKey":   shard.HashKeyRange.EndingHashKey,
		},
		"SequenceNumberRange": map[string]any{
			"StartingSequenceNumber": shard.SequenceNumberRange.StartingSequenceNumber,
		},
	}
	if shard.SequenceNumberRange.EndingSequenceNumber != "" {
		m["SequenceNumberRange"].(map[string]any)["EndingSequenceNumber"] = shard.SequenceNumberRange.EndingSequenceNumber
	}
	return m
}

func activeShardCount(st *Stream) int {
	count := 0
	for _, shard := range st.Shards {
		if shard.SequenceNumberRange.EndingSequenceNumber == "" {
			count++
		}
	}
	return count
}

// cmpHashKey compares two decimal hash key strings as big integers.
// Returns -1 if a < b, 0 if a == b, +1 if a > b.
func cmpHashKey(a, b string) int {
	ai, _ := new(big.Int).SetString(a, 10)
	bi, _ := new(big.Int).SetString(b, 10)
	if ai == nil || bi == nil {
		return 0
	}
	return ai.Cmp(bi)
}
