// Package athena provides a basic emulation of Amazon Athena.
//
// Implemented operations: StartQueryExecution, GetQueryExecution,
// GetQueryResults, ListQueryExecutions, CreateWorkGroup, GetWorkGroup,
// ListWorkGroups, DeleteWorkGroup.
//
// Queries are accepted and immediately marked SUCCEEDED with empty results.
package athena

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
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "athena"

// ─── Types ────────────────────────────────────────────────────

// QueryExecution represents an Athena query execution.
type QueryExecution struct {
	QueryExecutionId string `json:"QueryExecutionId"`
	Query            string `json:"Query"`
	WorkGroup        string `json:"WorkGroup,omitempty"`
	Status           struct {
		State              string  `json:"State"`
		SubmissionDateTime float64 `json:"SubmissionDateTime"`
		CompletionDateTime float64 `json:"CompletionDateTime,omitempty"`
	} `json:"Status"`
	ResultConfiguration struct {
		OutputLocation string `json:"OutputLocation,omitempty"`
	} `json:"ResultConfiguration,omitempty"`
}

// WorkGroup represents an Athena workgroup.
type WorkGroup struct {
	Name        string `json:"Name"`
	State       string `json:"State"`
	Description string `json:"Description,omitempty"`
}

// ─── Store ────────────────────────────────────────────────────

type athenaStore struct {
	store state.Store
	cfg   *config.Config
	clk   clock.Clock
}

func newAthenaStore(s state.Store, cfg *config.Config, clk clock.Clock) *athenaStore {
	return &athenaStore{store: s, cfg: cfg, clk: clk}
}

const (
	nsQueries    = "athena:queries"
	nsWorkGroups = "athena:workgroups"
)

func (s *athenaStore) putQuery(ctx context.Context, q *QueryExecution) error {
	raw, err := json.Marshal(q)
	if err != nil {
		return fmt.Errorf("athena: marshal query execution: %w", err)
	}
	return s.store.Set(ctx, nsQueries, q.QueryExecutionId, string(raw))
}

func (s *athenaStore) getQuery(ctx context.Context, id string) (*QueryExecution, bool) {
	raw, found, err := s.store.Get(ctx, nsQueries, id)
	if err != nil || !found {
		return nil, false
	}
	var q QueryExecution
	if json.Unmarshal([]byte(raw), &q) != nil {
		return nil, false
	}
	return &q, true
}

func (s *athenaStore) listQueries(ctx context.Context) ([]*QueryExecution, error) {
	pairs, err := s.store.Scan(ctx, nsQueries, "")
	if err != nil {
		return nil, err
	}
	out := make([]*QueryExecution, 0, len(pairs))
	for _, kv := range pairs {
		var q QueryExecution
		if json.Unmarshal([]byte(kv.Value), &q) == nil {
			out = append(out, &q)
		}
	}
	return out, nil
}

func (s *athenaStore) putWorkGroup(ctx context.Context, wg *WorkGroup) error {
	raw, err := json.Marshal(wg)
	if err != nil {
		return fmt.Errorf("athena: marshal workgroup: %w", err)
	}
	return s.store.Set(ctx, nsWorkGroups, wg.Name, string(raw))
}

func (s *athenaStore) getWorkGroup(ctx context.Context, name string) (*WorkGroup, bool) {
	raw, found, err := s.store.Get(ctx, nsWorkGroups, name)
	if err != nil || !found {
		return nil, false
	}
	var wg WorkGroup
	if json.Unmarshal([]byte(raw), &wg) != nil {
		return nil, false
	}
	return &wg, true
}

func (s *athenaStore) listWorkGroups(ctx context.Context) ([]*WorkGroup, error) {
	pairs, err := s.store.Scan(ctx, nsWorkGroups, "")
	if err != nil {
		return nil, err
	}
	out := make([]*WorkGroup, 0, len(pairs))
	for _, kv := range pairs {
		var wg WorkGroup
		if json.Unmarshal([]byte(kv.Value), &wg) == nil {
			out = append(out, &wg)
		}
	}
	return out, nil
}

func (s *athenaStore) deleteWorkGroup(ctx context.Context, name string) error {
	return s.store.Delete(ctx, nsWorkGroups, name)
}

// ─── Service ──────────────────────────────────────────────────

// Service implements router.Service and router.TargetDispatcher for Athena.
type Service struct {
	log     *serviceutil.ServiceLogger
	store   *athenaStore
	cfg     *config.Config
	clk     clock.Clock
	ops     map[string]http.HandlerFunc
	typedOp map[string]op.Operation
}

// New returns a configured Athena Service.
func New(cfg *config.Config, st state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	s := &Service{
		log:   serviceutil.NewServiceLogger(logger, serviceName),
		store: newAthenaStore(st, cfg, clk),
		cfg:   cfg,
		clk:   clk,
	}
	s.ops = map[string]http.HandlerFunc{
		"StartQueryExecution": s.startQueryExecution,
		"GetQueryExecution":   s.getQueryExecution,
		"GetQueryResults":     s.getQueryResults,
		"ListQueryExecutions": s.listQueryExecutions,
		"CreateWorkGroup":     s.createWorkGroup,
		"GetWorkGroup":        s.getWorkGroup,
		"ListWorkGroups":      s.listWorkGroups,
		"DeleteWorkGroup":     s.deleteWorkGroup,
	}
	s.typedOp = s.typedOps()
	return s
}

func (s *Service) Name() string                { return serviceName }
func (s *Service) RegisterRoutes(_ chi.Router) {}
func (s *Service) TargetPrefix() string        { return "AmazonAthena." }

func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			w.Header().Set("x-emulator-unsupported-protocol", c.Name())
			c.WriteError(w, r, &protocol.AWSError{
				Code: "UnsupportedProtocol", Message: "Athena does not support wire protocol " + c.Name() + ".",
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

func (s *Service) startQueryExecution(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QueryString         string `json:"QueryString"`
		WorkGroup           string `json:"WorkGroup"`
		ResultConfiguration struct {
			OutputLocation string `json:"OutputLocation"`
		} `json:"ResultConfiguration"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	now := float64(s.clk.Now().Unix())
	qe := &QueryExecution{
		QueryExecutionId: uuid.NewString(),
		Query:            req.QueryString,
		WorkGroup:        req.WorkGroup,
	}
	qe.Status.State = "SUCCEEDED"
	qe.Status.SubmissionDateTime = now
	qe.Status.CompletionDateTime = now
	qe.ResultConfiguration.OutputLocation = req.ResultConfiguration.OutputLocation

	if err := s.store.putQuery(r.Context(), qe); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"QueryExecutionId": qe.QueryExecutionId,
	})
}

func (s *Service) getQueryExecution(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QueryExecutionId string `json:"QueryExecutionId"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	qe, found := s.store.getQuery(r.Context(), req.QueryExecutionId)
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidRequestException",
			Message:    fmt.Sprintf("QueryExecution %s not found", req.QueryExecutionId),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"QueryExecution": qe})
}

func (s *Service) getQueryResults(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QueryExecutionId string `json:"QueryExecutionId"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if _, found := s.store.getQuery(r.Context(), req.QueryExecutionId); !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidRequestException",
			Message:    fmt.Sprintf("QueryExecution %s not found", req.QueryExecutionId),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	// Return empty result set.
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"ResultSet": map[string]any{
			"Rows":              []any{},
			"ResultSetMetadata": map[string]any{"ColumnInfo": []any{}},
		},
	})
}

func (s *Service) listQueryExecutions(w http.ResponseWriter, r *http.Request) {
	queries, err := s.store.listQueries(r.Context())
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	ids := make([]string, 0, len(queries))
	for _, q := range queries {
		ids = append(ids, q.QueryExecutionId)
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"QueryExecutionIds": ids})
}

func (s *Service) createWorkGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"Name"`
		Description string `json:"Description"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "InvalidRequestException", Message: "Name is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	wg := &WorkGroup{Name: req.Name, State: "ENABLED", Description: req.Description}
	if err := s.store.putWorkGroup(r.Context(), wg); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (s *Service) getWorkGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkGroup string `json:"WorkGroup"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	wg, found := s.store.getWorkGroup(r.Context(), req.WorkGroup)
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidRequestException",
			Message:    fmt.Sprintf("WorkGroup %s not found", req.WorkGroup),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"WorkGroup": wg})
}

func (s *Service) listWorkGroups(w http.ResponseWriter, r *http.Request) {
	workgroups, err := s.store.listWorkGroups(r.Context())
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	summaries := make([]map[string]any, 0, len(workgroups))
	for _, wg := range workgroups {
		summaries = append(summaries, map[string]any{
			"Name":  wg.Name,
			"State": wg.State,
		})
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"WorkGroups": summaries})
}

func (s *Service) deleteWorkGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkGroup string `json:"WorkGroup"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if _, found := s.store.getWorkGroup(r.Context(), req.WorkGroup); !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidRequestException",
			Message:    fmt.Sprintf("WorkGroup %s not found", req.WorkGroup),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	if err := s.store.deleteWorkGroup(r.Context(), req.WorkGroup); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}
