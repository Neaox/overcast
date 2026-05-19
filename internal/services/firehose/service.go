// Package firehose provides a basic emulation of Amazon Data Firehose
// (formerly Kinesis Data Firehose).
//
// Implemented operations: CreateDeliveryStream, DescribeDeliveryStream,
// ListDeliveryStreams, DeleteDeliveryStream, PutRecord, PutRecordBatch.
//
// Records are accepted but silently discarded (no S3 buffering).
package firehose

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "firehose"

// ─── Types ────────────────────────────────────────────────────

// DeliveryStream represents a Firehose delivery stream.
type DeliveryStream struct {
	DeliveryStreamName   string `json:"DeliveryStreamName"`
	DeliveryStreamARN    string `json:"DeliveryStreamARN"`
	DeliveryStreamStatus string `json:"DeliveryStreamStatus"`
	DeliveryStreamType   string `json:"DeliveryStreamType"`
}

// ─── Store ────────────────────────────────────────────────────

type firehoseStore struct {
	store state.Store
	cfg   *config.Config
}

func newFirehoseStore(s state.Store, cfg *config.Config) *firehoseStore {
	return &firehoseStore{store: s, cfg: cfg}
}

const nsStreams = "firehose:streams"

func (s *firehoseStore) putStream(ctx context.Context, ds *DeliveryStream) error {
	raw, err := json.Marshal(ds)
	if err != nil {
		return fmt.Errorf("firehose: marshal delivery stream: %w", err)
	}
	return s.store.Set(ctx, nsStreams, ds.DeliveryStreamName, string(raw))
}

func (s *firehoseStore) getStream(ctx context.Context, name string) (*DeliveryStream, bool) {
	raw, found, err := s.store.Get(ctx, nsStreams, name)
	if err != nil || !found {
		return nil, false
	}
	var ds DeliveryStream
	if json.Unmarshal([]byte(raw), &ds) != nil {
		return nil, false
	}
	return &ds, true
}

func (s *firehoseStore) listStreams(ctx context.Context) ([]*DeliveryStream, error) {
	pairs, err := s.store.Scan(ctx, nsStreams, "")
	if err != nil {
		return nil, err
	}
	out := make([]*DeliveryStream, 0, len(pairs))
	for _, kv := range pairs {
		var ds DeliveryStream
		if json.Unmarshal([]byte(kv.Value), &ds) == nil {
			out = append(out, &ds)
		}
	}
	return out, nil
}

func (s *firehoseStore) deleteStream(ctx context.Context, name string) error {
	return s.store.Delete(ctx, nsStreams, name)
}

// ─── Service ──────────────────────────────────────────────────

// Service implements router.Service and router.TargetDispatcher for Firehose.
type Service struct {
	log     *serviceutil.ServiceLogger
	store   *firehoseStore
	cfg     *config.Config
	ops     map[string]http.HandlerFunc
	typedOp map[string]op.Operation
}

// New returns a configured Firehose Service.
func New(cfg *config.Config, st state.Store, logger *zap.Logger, _ clock.Clock) *Service {
	s := &Service{
		log:   serviceutil.NewServiceLogger(logger, serviceName),
		store: newFirehoseStore(st, cfg),
		cfg:   cfg,
	}
	s.ops = map[string]http.HandlerFunc{
		"CreateDeliveryStream":   s.createDeliveryStream,
		"DescribeDeliveryStream": s.describeDeliveryStream,
		"ListDeliveryStreams":    s.listDeliveryStreams,
		"DeleteDeliveryStream":   s.deleteDeliveryStream,
		"PutRecord":              s.putRecord,
		"PutRecordBatch":         s.putRecordBatch,
	}
	s.typedOp = s.typedOps()
	return s
}

func (s *Service) Name() string                { return serviceName }
func (s *Service) RegisterRoutes(_ chi.Router) {}
func (s *Service) TargetPrefix() string        { return "Firehose_20150804." }

func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			w.Header().Set("x-emulator-unsupported-protocol", c.Name())
			c.WriteError(w, r, &protocol.AWSError{
				Code: "UnsupportedProtocol", Message: "Firehose does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
		if c.Name() != codec.NameRPCv2CBOR {
			s.dispatchLegacy(w, r, opName)
			return
		}
		if typed, ok := s.typedOp[opName]; ok {
			typed.Invoke(w, r, c)
			return
		}
		c.WriteError(w, r, protocol.ErrNotImplemented)
		return
	}
	target := r.Header.Get("X-Amz-Target")
	opName := target
	if idx := strings.LastIndex(target, "."); idx >= 0 {
		opName = target[idx+1:]
	}
	s.dispatchLegacy(w, r, opName)
}

func (s *Service) dispatchLegacy(w http.ResponseWriter, r *http.Request, opName string) {
	if fn, ok := s.ops[opName]; ok {
		fn(w, r)
		return
	}
	protocol.NotImplementedJSON(w, r)
}

// ─── Handlers ─────────────────────────────────────────────────

func (s *Service) createDeliveryStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeliveryStreamName string `json:"DeliveryStreamName"`
		DeliveryStreamType string `json:"DeliveryStreamType"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.DeliveryStreamName == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "InvalidArgumentException", Message: "DeliveryStreamName is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	region := middleware.RegionFromContext(r.Context(), s.cfg.Region)
	arn := fmt.Sprintf("arn:aws:firehose:%s:%s:deliverystream/%s", region, s.cfg.AccountID, req.DeliveryStreamName)
	dsType := req.DeliveryStreamType
	if dsType == "" {
		dsType = "DirectPut"
	}
	ds := &DeliveryStream{
		DeliveryStreamName:   req.DeliveryStreamName,
		DeliveryStreamARN:    arn,
		DeliveryStreamStatus: "ACTIVE",
		DeliveryStreamType:   dsType,
	}
	if err := s.store.putStream(r.Context(), ds); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"DeliveryStreamARN": arn})
}

func (s *Service) describeDeliveryStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeliveryStreamName string `json:"DeliveryStreamName"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	ds, found := s.store.getStream(r.Context(), req.DeliveryStreamName)
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Delivery stream %s not found", req.DeliveryStreamName),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"DeliveryStreamDescription": map[string]any{
			"DeliveryStreamName":   ds.DeliveryStreamName,
			"DeliveryStreamARN":    ds.DeliveryStreamARN,
			"DeliveryStreamStatus": ds.DeliveryStreamStatus,
			"DeliveryStreamType":   ds.DeliveryStreamType,
			"HasMoreDestinations":  false,
			"Destinations":         []any{},
		},
	})
}

func (s *Service) listDeliveryStreams(w http.ResponseWriter, r *http.Request) {
	streams, err := s.store.listStreams(r.Context())
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	names := make([]string, 0, len(streams))
	for _, ds := range streams {
		names = append(names, ds.DeliveryStreamName)
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"DeliveryStreamNames":    names,
		"HasMoreDeliveryStreams": false,
	})
}

func (s *Service) deleteDeliveryStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeliveryStreamName string `json:"DeliveryStreamName"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if _, found := s.store.getStream(r.Context(), req.DeliveryStreamName); !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Delivery stream %s not found", req.DeliveryStreamName),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	if err := s.store.deleteStream(r.Context(), req.DeliveryStreamName); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (s *Service) putRecord(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeliveryStreamName string `json:"DeliveryStreamName"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if _, found := s.store.getStream(r.Context(), req.DeliveryStreamName); !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Delivery stream %s not found", req.DeliveryStreamName),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	// Accept and discard.
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"RecordId":  uuid.NewString(),
		"Encrypted": false,
	})
}

func (s *Service) putRecordBatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeliveryStreamName string          `json:"DeliveryStreamName"`
		Records            json.RawMessage `json:"Records"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if _, found := s.store.getStream(r.Context(), req.DeliveryStreamName); !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Delivery stream %s not found", req.DeliveryStreamName),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	// Count records for the response.
	var records []json.RawMessage
	_ = json.Unmarshal(req.Records, &records)
	results := make([]map[string]any, 0, len(records))
	for range records {
		results = append(results, map[string]any{
			"RecordId": uuid.NewString(),
		})
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"FailedPutCount":   0,
		"Encrypted":        false,
		"RequestResponses": results,
	})
}
