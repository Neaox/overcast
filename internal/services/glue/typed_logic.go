package glue

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
)

type createDatabaseReq struct {
	DatabaseInput *Database `json:"DatabaseInput" cbor:"DatabaseInput"`
	CatalogId     string    `json:"CatalogId" cbor:"CatalogId"`
}

type getDatabaseReq struct {
	Name string `json:"Name" cbor:"Name"`
}

type deleteDatabaseReq struct {
	Name string `json:"Name" cbor:"Name"`
}

type getDatabaseResp struct {
	Database *Database `json:"Database" cbor:"Database"`
}

type getDatabasesResp struct {
	DatabaseList []*Database `json:"DatabaseList" cbor:"DatabaseList"`
}

type createTableReq struct {
	DatabaseName string `json:"DatabaseName" cbor:"DatabaseName"`
	TableInput   *Table `json:"TableInput" cbor:"TableInput"`
}

type getTableReq struct {
	DatabaseName string `json:"DatabaseName" cbor:"DatabaseName"`
	Name         string `json:"Name" cbor:"Name"`
}

type getTablesReq struct {
	DatabaseName string `json:"DatabaseName" cbor:"DatabaseName"`
}

type getTableResp struct {
	Table *Table `json:"Table" cbor:"Table"`
}

type getTablesResp struct {
	TableList []*Table `json:"TableList" cbor:"TableList"`
}

type deleteTableReq struct {
	DatabaseName string `json:"DatabaseName" cbor:"DatabaseName"`
	Name         string `json:"Name" cbor:"Name"`
}

func (s *Service) createDatabaseTyped(ctx context.Context, req *createDatabaseReq) (*struct{}, *protocol.AWSError) {
	if req.DatabaseInput == nil || req.DatabaseInput.Name == "" {
		return nil, &protocol.AWSError{
			Code: "InvalidInputException", Message: "DatabaseInput.Name is required",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	db := req.DatabaseInput
	if db.CatalogId == "" {
		db.CatalogId = s.cfg.AccountID
	}
	if err := s.store.putDatabase(ctx, db); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &struct{}{}, nil
}

func (s *Service) getDatabaseTyped(ctx context.Context, req *getDatabaseReq) (*getDatabaseResp, *protocol.AWSError) {
	db, found := s.store.getDatabase(ctx, req.Name)
	if !found {
		return nil, &protocol.AWSError{
			Code:       "EntityNotFoundException",
			Message:    fmt.Sprintf("Database %s not found", req.Name),
			HTTPStatus: http.StatusNotFound,
		}
	}
	return &getDatabaseResp{Database: db}, nil
}

func (s *Service) getDatabasesTyped(ctx context.Context, _ *struct{}) (*getDatabasesResp, *protocol.AWSError) {
	dbs, err := s.store.listDatabases(ctx)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	return &getDatabasesResp{DatabaseList: dbs}, nil
}

func (s *Service) deleteDatabaseTyped(ctx context.Context, req *deleteDatabaseReq) (*struct{}, *protocol.AWSError) {
	if _, found := s.store.getDatabase(ctx, req.Name); !found {
		return nil, &protocol.AWSError{
			Code:       "EntityNotFoundException",
			Message:    fmt.Sprintf("Database %s not found", req.Name),
			HTTPStatus: http.StatusNotFound,
		}
	}
	if err := s.store.deleteDatabase(ctx, req.Name); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &struct{}{}, nil
}

func (s *Service) createTableTyped(ctx context.Context, req *createTableReq) (*struct{}, *protocol.AWSError) {
	if req.TableInput == nil || req.TableInput.Name == "" {
		return nil, &protocol.AWSError{
			Code: "InvalidInputException", Message: "TableInput.Name is required",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	t := req.TableInput
	t.DatabaseName = req.DatabaseName
	if t.CatalogId == "" {
		t.CatalogId = s.cfg.AccountID
	}
	if err := s.store.putTable(ctx, t); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &struct{}{}, nil
}

func (s *Service) getTableTyped(ctx context.Context, req *getTableReq) (*getTableResp, *protocol.AWSError) {
	t, found := s.store.getTable(ctx, req.DatabaseName, req.Name)
	if !found {
		return nil, &protocol.AWSError{
			Code:       "EntityNotFoundException",
			Message:    fmt.Sprintf("Table %s not found in database %s", req.Name, req.DatabaseName),
			HTTPStatus: http.StatusNotFound,
		}
	}
	return &getTableResp{Table: t}, nil
}

func (s *Service) getTablesTyped(ctx context.Context, req *getTablesReq) (*getTablesResp, *protocol.AWSError) {
	tables, err := s.store.listTables(ctx, req.DatabaseName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	return &getTablesResp{TableList: tables}, nil
}

func (s *Service) deleteTableTyped(ctx context.Context, req *deleteTableReq) (*struct{}, *protocol.AWSError) {
	if _, found := s.store.getTable(ctx, req.DatabaseName, req.Name); !found {
		return nil, &protocol.AWSError{
			Code:       "EntityNotFoundException",
			Message:    fmt.Sprintf("Table %s not found", req.Name),
			HTTPStatus: http.StatusNotFound,
		}
	}
	if err := s.store.deleteTable(ctx, req.DatabaseName, req.Name); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &struct{}{}, nil
}
