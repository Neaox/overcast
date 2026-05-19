// Package glue provides a basic emulation of AWS Glue Data Catalog.
//
// Implemented operations: CreateDatabase, GetDatabase, GetDatabases,
// DeleteDatabase, CreateTable, GetTable, GetTables, DeleteTable.
//
// Enough for data-layer stacks referencing AWS::Glue::Database and Table.
package glue

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "glue"

// ─── Types ────────────────────────────────────────────────────

// Database represents a Glue database.
type Database struct {
	Name        string `json:"Name"`
	Description string `json:"Description,omitempty"`
	CatalogId   string `json:"CatalogId,omitempty"`
}

// Table represents a Glue table.
type Table struct {
	Name         string `json:"Name"`
	DatabaseName string `json:"DatabaseName"`
	Description  string `json:"Description,omitempty"`
	TableType    string `json:"TableType,omitempty"`
	CatalogId    string `json:"CatalogId,omitempty"`
}

// ─── Store ────────────────────────────────────────────────────

type glueStore struct {
	store state.Store
	cfg   *config.Config
}

func newGlueStore(s state.Store, cfg *config.Config) *glueStore {
	return &glueStore{store: s, cfg: cfg}
}

const (
	nsDatabases = "glue:databases"
	nsTables    = "glue:tables"
)

func (s *glueStore) putDatabase(ctx context.Context, db *Database) error {
	raw, err := json.Marshal(db)
	if err != nil {
		return fmt.Errorf("glue: marshal database: %w", err)
	}
	return s.store.Set(ctx, nsDatabases, db.Name, string(raw))
}

func (s *glueStore) getDatabase(ctx context.Context, name string) (*Database, bool) {
	raw, found, err := s.store.Get(ctx, nsDatabases, name)
	if err != nil || !found {
		return nil, false
	}
	var db Database
	if json.Unmarshal([]byte(raw), &db) != nil {
		return nil, false
	}
	return &db, true
}

func (s *glueStore) listDatabases(ctx context.Context) ([]*Database, error) {
	pairs, err := s.store.Scan(ctx, nsDatabases, "")
	if err != nil {
		return nil, err
	}
	out := make([]*Database, 0, len(pairs))
	for _, kv := range pairs {
		var db Database
		if json.Unmarshal([]byte(kv.Value), &db) == nil {
			out = append(out, &db)
		}
	}
	return out, nil
}

func (s *glueStore) deleteDatabase(ctx context.Context, name string) error {
	return s.store.Delete(ctx, nsDatabases, name)
}

func tableKey(dbName, tableName string) string { return dbName + "/" + tableName }

func (s *glueStore) putTable(ctx context.Context, t *Table) error {
	raw, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("glue: marshal table: %w", err)
	}
	return s.store.Set(ctx, nsTables, tableKey(t.DatabaseName, t.Name), string(raw))
}

func (s *glueStore) getTable(ctx context.Context, dbName, tableName string) (*Table, bool) {
	raw, found, err := s.store.Get(ctx, nsTables, tableKey(dbName, tableName))
	if err != nil || !found {
		return nil, false
	}
	var t Table
	if json.Unmarshal([]byte(raw), &t) != nil {
		return nil, false
	}
	return &t, true
}

func (s *glueStore) listTables(ctx context.Context, dbName string) ([]*Table, error) {
	pairs, err := s.store.Scan(ctx, nsTables, "")
	if err != nil {
		return nil, err
	}
	out := make([]*Table, 0, len(pairs))
	for _, kv := range pairs {
		var t Table
		if json.Unmarshal([]byte(kv.Value), &t) == nil && t.DatabaseName == dbName {
			out = append(out, &t)
		}
	}
	return out, nil
}

func (s *glueStore) deleteTable(ctx context.Context, dbName, tableName string) error {
	return s.store.Delete(ctx, nsTables, tableKey(dbName, tableName))
}

// ─── Service ──────────────────────────────────────────────────

// Service implements router.Service and router.TargetDispatcher for Glue.
type Service struct {
	log     *serviceutil.ServiceLogger
	store   *glueStore
	cfg     *config.Config
	ops     map[string]http.HandlerFunc
	typedOp map[string]op.Operation
}

// New returns a configured Glue Service.
func New(cfg *config.Config, st state.Store, logger *zap.Logger, _ clock.Clock) *Service {
	s := &Service{
		log:   serviceutil.NewServiceLogger(logger, serviceName),
		store: newGlueStore(st, cfg),
		cfg:   cfg,
	}
	s.ops = map[string]http.HandlerFunc{
		"CreateDatabase": s.createDatabase,
		"GetDatabase":    s.getDatabase,
		"GetDatabases":   s.getDatabases,
		"DeleteDatabase": s.deleteDatabase,
		"CreateTable":    s.createTable,
		"GetTable":       s.getTable,
		"GetTables":      s.getTables,
		"DeleteTable":    s.deleteTable,
	}
	s.typedOp = s.typedOps()
	return s
}

func (s *Service) Name() string                { return serviceName }
func (s *Service) RegisterRoutes(_ chi.Router) {}
func (s *Service) TargetPrefix() string        { return "AWSGlue." }

func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			w.Header().Set("x-emulator-unsupported-protocol", c.Name())
			c.WriteError(w, r, &protocol.AWSError{
				Code: "UnsupportedProtocol", Message: "Glue does not support wire protocol " + c.Name() + ".",
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

func (s *Service) createDatabase(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DatabaseInput *Database `json:"DatabaseInput"`
		CatalogId     string    `json:"CatalogId"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.DatabaseInput == nil || req.DatabaseInput.Name == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "InvalidInputException", Message: "DatabaseInput.Name is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	db := req.DatabaseInput
	if db.CatalogId == "" {
		db.CatalogId = s.cfg.AccountID
	}
	if err := s.store.putDatabase(r.Context(), db); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (s *Service) getDatabase(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"Name"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	db, found := s.store.getDatabase(r.Context(), req.Name)
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "EntityNotFoundException",
			Message:    fmt.Sprintf("Database %s not found", req.Name),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Database": db})
}

func (s *Service) getDatabases(w http.ResponseWriter, r *http.Request) {
	dbs, err := s.store.listDatabases(r.Context())
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"DatabaseList": dbs})
}

func (s *Service) deleteDatabase(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"Name"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if _, found := s.store.getDatabase(r.Context(), req.Name); !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "EntityNotFoundException",
			Message:    fmt.Sprintf("Database %s not found", req.Name),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	if err := s.store.deleteDatabase(r.Context(), req.Name); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (s *Service) createTable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DatabaseName string `json:"DatabaseName"`
		TableInput   *Table `json:"TableInput"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.TableInput == nil || req.TableInput.Name == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "InvalidInputException", Message: "TableInput.Name is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	t := req.TableInput
	t.DatabaseName = req.DatabaseName
	if t.CatalogId == "" {
		t.CatalogId = s.cfg.AccountID
	}
	if err := s.store.putTable(r.Context(), t); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (s *Service) getTable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DatabaseName string `json:"DatabaseName"`
		Name         string `json:"Name"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	t, found := s.store.getTable(r.Context(), req.DatabaseName, req.Name)
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "EntityNotFoundException",
			Message:    fmt.Sprintf("Table %s not found in database %s", req.Name, req.DatabaseName),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Table": t})
}

func (s *Service) getTables(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DatabaseName string `json:"DatabaseName"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	tables, err := s.store.listTables(r.Context(), req.DatabaseName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"TableList": tables})
}

func (s *Service) deleteTable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DatabaseName string `json:"DatabaseName"`
		Name         string `json:"Name"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if _, found := s.store.getTable(r.Context(), req.DatabaseName, req.Name); !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "EntityNotFoundException",
			Message:    fmt.Sprintf("Table %s not found", req.Name),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	if err := s.store.deleteTable(r.Context(), req.DatabaseName, req.Name); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}
