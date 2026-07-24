// Package dynamodb implements the AWS DynamoDB API emulator.
// See docs/services/dynamodb.md for the support matrix.
package dynamodb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "dynamodb"

// Service implements router.Service for DynamoDB.
type Service struct {
	cfg       *config.Config
	store     state.Store
	log       *serviceutil.ServiceLogger
	handler   *Handler
	ttlCancel context.CancelFunc
}

// New returns a configured DynamoDB Service.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock, bus *events.Bus) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	// Resolve past any state.NamespacedStore wrapping before probing for
	// SQLiteDBProvider — an unrelated OVERCAST_STATE_<SVC> override on some
	// other service would otherwise wrap store in a type that satisfies
	// neither SQLiteDBProvider nor ReadyAwaiter, silently downgrading
	// DynamoDB items/streams to the in-memory-only backend even though
	// DynamoDB itself was never routed away from SQLite.
	backendStore := state.Unwrap(store, serviceName)
	items := newItemBackendFor(backendStore)
	streams := newStreamBackendFor(backendStore)

	ttlCtx, ttlCancel := context.WithCancel(context.Background())

	svc := &Service{
		cfg:       cfg,
		store:     store,
		log:       log,
		handler:   newHandler(cfg, store, items, streams, bus, log, clk, cfg.Region),
		ttlCancel: ttlCancel,
	}

	svc.handler.startTTLSweeper(ttlCtx)
	return svc
}

// newItemBackendFor selects the right itemBackend based on the store type:
//   - SQLiteDBProvider → sqlItemBackend (dedicated indexed table in the same DB file)
//   - anything else    → memItemBackend (nested maps, zero JSON overhead)
//
// Callers must pass a store already resolved with state.Unwrap (see New) —
// a *state.NamespacedStore never implements SQLiteDBProvider itself, so
// passing one through unresolved always falls back to the memory backend.
func newItemBackendFor(store state.Store) itemBackend {
	if provider, ok := store.(state.SQLiteDBProvider); ok {
		return newSQLItemBackend(provider.DB)
	}
	return newMemItemBackend()
}

// newStreamBackendFor selects the right streamBackend based on the store
// type. Callers must pass a store already resolved with state.Unwrap — see
// newItemBackendFor.
func newStreamBackendFor(store state.Store) streamBackend {
	if provider, ok := store.(state.SQLiteDBProvider); ok {
		return newSQLStreamBackend(provider.DB)
	}
	return newMemStreamBackend()
}

// debugItemsNamespace is the virtual raw-state namespace name for DynamoDB
// items (DebugNamespace below). Defined here, not in internal/router, since
// internal/router imports this package (a reverse import would cycle) — see
// router.DebugStateProvider for the generalized interface this satisfies.
const debugItemsNamespace = "dynamodb:items"

// DebugNamespace returns the virtual raw-state namespace name for DynamoDB
// items, implementing router.DebugStateProvider.
func (s *Service) DebugNamespace() string { return debugItemsNamespace }

// DebugStateKeys returns the virtual raw-state keys for DynamoDB items.
func (s *Service) DebugStateKeys(ctx context.Context) ([]string, error) {
	records, err := s.handler.store.items.debugScan(ctx)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(records))
	for _, record := range records {
		keys = append(keys, dynamoDebugItemKey(record))
	}
	sort.Strings(keys)
	return keys, nil
}

// DebugStateValues returns raw DynamoDB item values keyed by table/hash/sort.
func (s *Service) DebugStateValues(ctx context.Context) (map[string]string, error) {
	records, err := s.handler.store.items.debugScan(ctx)
	if err != nil {
		return nil, err
	}
	values := make(map[string]string, len(records))
	for _, record := range records {
		raw, err := json.Marshal(record.Item)
		if err != nil {
			return nil, err
		}
		values[dynamoDebugItemKey(record)] = string(raw)
	}
	return values, nil
}

// DebugResetState deletes all DynamoDB item rows for debug reset operations.
func (s *Service) DebugResetState(ctx context.Context) error {
	return s.handler.store.items.debugDeleteAll(ctx)
}

func dynamoDebugItemKey(record debugItemRecord) string {
	if record.SortKey == "" {
		return record.TableName + "/" + record.HashKey
	}
	return record.TableName + "/" + record.HashKey + "/" + record.SortKey
}

// ---- Exported methods for the dynamodbstreams service ----------------------

// ListStreamEnabledTables returns all tables that have streaming enabled.
func (s *Service) ListStreamEnabledTables(ctx context.Context) ([]*Table, error) {
	pairs, err := s.handler.store.scanAllTables(ctx)
	if err != nil {
		return nil, err
	}
	var result []*Table
	for _, kv := range pairs {
		var t Table
		if err := json.Unmarshal([]byte(kv.Value), &t); err != nil {
			continue
		}
		if t.streamEnabled() {
			result = append(result, &t)
		}
	}
	return result, nil
}

// GetStreamTable returns the table descriptor for a given stream ARN.
func (s *Service) GetStreamTable(ctx context.Context, streamArn string) (*Table, error) {
	pairs, err := s.handler.store.scanAllTables(ctx)
	if err != nil {
		return nil, err
	}
	for _, kv := range pairs {
		var t Table
		if err := json.Unmarshal([]byte(kv.Value), &t); err != nil {
			continue
		}
		if t.LatestStreamArn == streamArn {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("stream not found: %s", streamArn)
}

// GetStreamRecordsSince returns stream records for a table with seq > afterSeq.
// Returns (records, latestSeqInResult, error).
func (s *Service) GetStreamRecordsSince(ctx context.Context, tableName string, afterSeq int64, limit int) ([]*StreamRecord, int64, error) {
	recs, aerr := s.handler.store.getStreamRecordsSince(ctx, tableName, afterSeq, limit)
	if aerr != nil {
		return nil, 0, aerr
	}
	var latestSeq int64
	if len(recs) > 0 {
		latestSeq = recs[len(recs)-1].SequenceNumber
	} else {
		seq, aerr := s.handler.store.latestStreamSeq(ctx, tableName)
		if aerr != nil {
			return nil, 0, aerr
		}
		latestSeq = seq
	}
	return recs, latestSeq, nil
}

// Name returns the service identifier.
func (s *Service) Name() string { return serviceName }

// Stop cancels the TTL sweeper goroutine.
func (s *Service) Stop(_ context.Context) {
	if s.ttlCancel != nil {
		s.ttlCancel()
	}
}

// DynamoDBInvoker returns an events.DynamoDBInvoker that dispatches operations
// through the handler's operation map using httptest, so we don't need to
// export any handler internals.
func (s *Service) DynamoDBInvoker() events.DynamoDBInvoker {
	return &localDynamoDBInvoker{handler: s.handler}
}

// TargetPrefix returns the X-Amz-Target prefix for DynamoDB dispatch.
func (s *Service) TargetPrefix() string { return "DynamoDB_20120810." }

// RegisterRoutes is a no-op — DynamoDB uses POST / which is handled by the
// router's target dispatcher shared with SQS and SNS.
func (s *Service) RegisterRoutes(r chi.Router) {}

// Dispatch routes to the correct DynamoDB handler based on X-Amz-Target.
func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	// Wrap the response writer to compute the X-Amz-Crc32 header.
	// The AWS Go SDK v2 validates this checksum on every DynamoDB response;
	// without it, resp.Body.Close() returns a checksum mismatch error and
	// the SDK logs "failed to close HTTP response body".
	cw := wrapCRC32(w)

	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			cw.Header().Set("x-emulator-unsupported-protocol", c.Name())
			c.WriteError(cw, r, &protocol.AWSError{
				Code:       "UnsupportedProtocol",
				Message:    "DynamoDB does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
		if raw, ok := s.handler.rawOp[opName]; ok {
			raw.Invoke(cw, r, c)
			return
		}
	}

	target := r.Header.Get("X-Amz-Target")
	// Strip prefix: "DynamoDB_20120810.PutItem" → "PutItem"
	const prefix = "DynamoDB_20120810."
	if len(target) > len(prefix) {
		target = target[len(prefix):]
	}

	if fn, ok := s.handler.ops[target]; ok {
		fn(cw, r)
		return
	}
	protocol.WriteJSONError(cw, r, &protocol.AWSError{
		Code:       "UnknownOperationException",
		Message:    "Unknown operation: " + target,
		HTTPStatus: http.StatusBadRequest,
	})
}

// crc32ResponseWriter intercepts Write calls to compute a running CRC32
// checksum and set the X-Amz-Crc32 header before the response is flushed.
// This works because the DrainBody middleware buffers the response and defers
// header flush until after the handler returns.
type crc32ResponseWriter struct {
	http.ResponseWriter
	hash hash.Hash32
}

func (w *crc32ResponseWriter) Write(b []byte) (int, error) {
	w.hash.Write(b)
	w.Header().Set("X-Amz-Crc32", strconv.FormatUint(uint64(w.hash.Sum32()), 10))
	return w.ResponseWriter.Write(b)
}

func (w *crc32ResponseWriter) WriteHeader(status int) {
	w.ResponseWriter.WriteHeader(status)
}

// Unwrap lets middleware (chi, http.ResponseController) reach the underlying writer.
func (w *crc32ResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// ---- localDynamoDBInvoker -------------------------------------------------

// localDynamoDBInvoker implements events.DynamoDBInvoker by dispatching
// operations through the handler's operation map via httptest. This avoids
// exporting DynamoDB handler internals while giving AppSync (and future
// consumers) a clean invoke path.
type localDynamoDBInvoker struct {
	handler *Handler
}

func (inv *localDynamoDBInvoker) Invoke(ctx context.Context, operation string, input []byte) ([]byte, error) {
	fn, ok := inv.handler.ops[operation]
	if !ok {
		return nil, fmt.Errorf("dynamodb: unknown operation %q", operation)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/", bytes.NewReader(input))
	if err != nil {
		return nil, fmt.Errorf("dynamodb: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "DynamoDB_20120810."+operation)

	rec := httptest.NewRecorder()
	fn(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("dynamodb: read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("dynamodb: %s returned HTTP %d: %s", operation, resp.StatusCode, string(body))
	}

	return body, nil
}
