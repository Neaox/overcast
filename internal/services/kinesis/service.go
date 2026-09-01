// Package kinesis provides emulation of Amazon Kinesis Data Streams.
// See docs/services/kinesis.md for the support matrix.
//
// Wire protocol: JSON 1.1 (X-Amz-Target: Kinesis_20131202.*) and RPC v2 CBOR.
package kinesis

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/protocol/codec"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

const serviceName = "kinesis"

// targetPrefix is the X-Amz-Target prefix for Kinesis Data Streams.
const targetPrefix = "Kinesis_20131202."

// Service implements router.Service and router.TargetDispatcher for Kinesis.
type Service struct {
	handler *Handler
}

// New returns a configured Kinesis Service.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	s := newKinesisStore(store, cfg.Region)
	return &Service{
		handler: newHandler(cfg, s, log, clk),
	}
}

// InitBus wires the event bus for stream lifecycle events.
func (s *Service) InitBus(bus *events.Bus) {
	s.handler.bus = bus
}

// StreamReceiver returns an events.StreamRecordReceiver backed by this
// service's store, so a consumer inside the emulator — the EventBridge Pipes
// Kinesis source poller — can read a stream without an HTTP round trip or an
// import cycle. It allocates only; no store access happens until a call.
func (s *Service) StreamReceiver() events.StreamRecordReceiver {
	return &streamReceiver{store: s.handler.store}
}

// streamReceiver satisfies events.StreamRecordReceiver.
type streamReceiver struct {
	store *kinesisStore
}

// ListStreamShards returns the stream's shard IDs.
func (r *streamReceiver) ListStreamShards(ctx context.Context, streamName string) ([]string, error) {
	stream, aerr := r.store.getStream(ctx, streamName)
	if aerr != nil {
		return nil, aerr
	}
	ids := make([]string, 0, len(stream.Shards))
	for _, shard := range stream.Shards {
		ids = append(ids, shard.ShardId)
	}
	return ids, nil
}

// ReceiveStreamRecords reads one page of a shard and returns the cursor to
// resume from. The cursor is the last sequence number returned, so a caller
// that sees no records keeps the cursor it already had.
func (r *streamReceiver) ReceiveStreamRecords(ctx context.Context, streamName, shardID, afterSeqNo string, limit int) ([]events.StreamRecord, string, error) {
	if limit <= 0 {
		limit = defaultStreamPageSize
	}
	page, aerr := r.store.listRecordsPage(ctx, streamName, shardID, afterSeqNo, limit)
	if aerr != nil {
		return nil, afterSeqNo, aerr
	}
	out := make([]events.StreamRecord, 0, len(page))
	next := afterSeqNo
	for _, rec := range page {
		out = append(out, events.StreamRecord{
			ShardID:                     shardID,
			SequenceNumber:              rec.SequenceNumber,
			PartitionKey:                rec.PartitionKey,
			Data:                        rec.Data,
			ApproximateArrivalTimestamp: rec.ApproximateArrivalTimestamp,
		})
		next = rec.SequenceNumber
	}
	return out, next, nil
}

// defaultStreamPageSize caps a ReceiveStreamRecords call that asks for no limit.
const defaultStreamPageSize = 100

// Name satisfies router.Service.
func (s *Service) Name() string { return serviceName }

// RegisterRoutes satisfies router.Service. Kinesis has no path-routed endpoints.
func (s *Service) RegisterRoutes(_ chi.Router) {}

// TargetPrefix satisfies router.TargetDispatcher.
func (s *Service) TargetPrefix() string { return targetPrefix }

// Dispatch satisfies router.TargetDispatcher.
func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			w.Header().Set("x-emulator-unsupported-protocol", c.Name())
			c.WriteError(w, r, &protocol.AWSError{
				Code:       "UnsupportedProtocol",
				Message:    "Kinesis does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
		// Preserve Kinesis' legacy JSON 1.1 wire shape, including empty
		// bodies for successful void operations. CBOR uses the typed path.
		if c.Name() != codec.NameRPCv2CBOR {
			if fn, ok := s.handler.ops[opName]; ok {
				fn(w, r)
				return
			}
		}
		if typed, ok := s.handler.typedOp[opName]; ok {
			typed.Invoke(w, r, c)
			return
		}
		c.WriteError(w, r, &protocol.AWSError{
			Code:       "UnknownOperationException",
			Message:    "Unknown Kinesis operation: " + opName,
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	target := r.Header.Get("X-Amz-Target")
	suffix := target
	if strings.HasPrefix(target, targetPrefix) {
		suffix = target[len(targetPrefix):]
	}
	if fn, ok := s.handler.ops[suffix]; ok {
		fn(w, r)
		return
	}
	protocol.NotImplementedJSON(w, r)
}
