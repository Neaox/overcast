// Package dynamodbstreams implements the AWS DynamoDB Streams API emulator.
// It reads stream data produced by the dynamodb service and exposes it via
// the DynamoDBStreams_20120810 target prefix on the shared POST / endpoint.
package dynamodbstreams

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/services/dynamodb"
	"github.com/Neaox/overcast/internal/serviceutil"
)

const serviceName = "dynamodbstreams"

// DynamoDBService is the narrow interface the streams service needs from the
// DynamoDB service. *dynamodb.Service satisfies this automatically.
type DynamoDBService interface {
	ListStreamEnabledTables(ctx context.Context) ([]*dynamodb.Table, error)
	GetStreamTable(ctx context.Context, streamArn string) (*dynamodb.Table, error)
	GetStreamRecordsSince(ctx context.Context, tableName string, afterSeq int64, limit int) ([]*dynamodb.StreamRecord, int64, error)
}

// Service implements router.Service for DynamoDB Streams.
type Service struct {
	ddb     DynamoDBService
	log     *serviceutil.ServiceLogger
	handler *handler
}

// New returns a configured DynamoDB Streams Service.
func New(ddb DynamoDBService, logger *zap.Logger) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	return &Service{
		ddb:     ddb,
		log:     log,
		handler: newHandler(ddb, log),
	}
}

// Name returns the service identifier.
func (s *Service) Name() string { return serviceName }

// TargetPrefix returns the X-Amz-Target prefix for DynamoDB Streams dispatch.
func (s *Service) TargetPrefix() string { return "DynamoDBStreams_20120810." }

// RegisterRoutes is a no-op - DynamoDB Streams uses POST / via target dispatch.
func (s *Service) RegisterRoutes(r chi.Router) {}

// Dispatch routes to the correct handler based on X-Amz-Target.
func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			w.Header().Set("x-emulator-unsupported-protocol", c.Name())
			c.WriteError(w, r, &protocol.AWSError{
				Code:       "UnsupportedProtocol",
				Message:    "DynamoDB Streams does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
		if typed, ok := s.handler.typedOp[opName]; ok {
			typed.Invoke(w, r, c)
			return
		}
		c.WriteError(w, r, &protocol.AWSError{
			Code:       "UnknownOperationException",
			Message:    "Unknown DynamoDB Streams operation: " + opName,
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	target := r.Header.Get("X-Amz-Target")
	const prefix = "DynamoDBStreams_20120810."
	if len(target) > len(prefix) {
		target = target[len(prefix):]
	}
	if fn, ok := s.handler.ops[target]; ok {
		fn(w, r)
		return
	}
	protocol.WriteJSONError(w, r, &protocol.AWSError{
		Code:       "UnknownOperationException",
		Message:    "Unknown DynamoDB Streams operation: " + target,
		HTTPStatus: http.StatusBadRequest,
	})
}

// ---- internal handler ------------------------------------------------------

type handler struct {
	ddb     DynamoDBService
	log     *serviceutil.ServiceLogger
	ops     map[string]http.HandlerFunc
	typedOp map[string]op.Operation
}

func newHandler(ddb DynamoDBService, log *serviceutil.ServiceLogger) *handler {
	h := &handler{ddb: ddb, log: log}
	h.ops = map[string]http.HandlerFunc{
		"ListStreams":      h.ListStreams,
		"DescribeStream":   h.DescribeStream,
		"GetShardIterator": h.GetShardIterator,
		"GetRecords":       h.GetRecords,
	}
	h.typedOp = h.typedOps()
	return h
}
