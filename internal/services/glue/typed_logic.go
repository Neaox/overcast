package glue

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
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

// ─── Tag operations ─────────────────────────────────────────────

type glueTagResourceReq struct {
	ResourceArn string            `json:"ResourceArn" cbor:"ResourceArn"`
	TagsToAdd   map[string]string `json:"TagsToAdd" cbor:"TagsToAdd"`
}

type glueUntagResourceReq struct {
	ResourceArn  string   `json:"ResourceArn" cbor:"ResourceArn"`
	TagsToRemove []string `json:"TagsToRemove" cbor:"TagsToRemove"`
}

type glueListTagsForResourceReq struct {
	ResourceArn string `json:"ResourceArn" cbor:"ResourceArn"`
}

type glueListTagsForResourceResp struct {
	Tags map[string]string `json:"Tags" cbor:"Tags"`
}

func (s *Service) tagResourceTyped(ctx context.Context, req *glueTagResourceReq) (*struct{}, *protocol.AWSError) {
	dbName, tableName := glueARNToDBAndTable(req.ResourceArn)
	if tableName != "" {
		t, found := s.store.getTable(ctx, dbName, tableName)
		if !found {
			return nil, &protocol.AWSError{
				Code: "EntityNotFoundException", Message: fmt.Sprintf("Table %s not found in database %s", tableName, dbName),
				HTTPStatus: http.StatusNotFound,
			}
		}
		if t.Tags == nil {
			t.Tags = make(map[string]string)
		}
		for k, v := range req.TagsToAdd {
			t.Tags[k] = v
		}
		if aerr := serviceutil.ValidateTags(glueTagCfg, t.Tags); aerr != nil {
			return nil, aerr
		}
		if err := s.store.putTable(ctx, t); err != nil {
			return nil, protocol.ErrInternalError
		}
		return &struct{}{}, nil
	}
	if dbName == "" {
		return nil, &protocol.AWSError{
			Code: "EntityNotFoundException", Message: "Resource not found",
			HTTPStatus: http.StatusNotFound,
		}
	}
	db, found := s.store.getDatabase(ctx, dbName)
	if !found {
		return nil, &protocol.AWSError{
			Code: "EntityNotFoundException", Message: fmt.Sprintf("Database %s not found", dbName),
			HTTPStatus: http.StatusNotFound,
		}
	}
	if db.Tags == nil {
		db.Tags = make(map[string]string)
	}
	for k, v := range req.TagsToAdd {
		db.Tags[k] = v
	}
	if aerr := serviceutil.ValidateTags(glueTagCfg, db.Tags); aerr != nil {
		return nil, aerr
	}
	if err := s.store.putDatabase(ctx, db); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &struct{}{}, nil
}

func (s *Service) untagResourceTyped(ctx context.Context, req *glueUntagResourceReq) (*struct{}, *protocol.AWSError) {
	dbName, tableName := glueARNToDBAndTable(req.ResourceArn)
	if tableName != "" {
		t, found := s.store.getTable(ctx, dbName, tableName)
		if !found {
			return nil, &protocol.AWSError{
				Code: "EntityNotFoundException", Message: fmt.Sprintf("Table %s not found in database %s", tableName, dbName),
				HTTPStatus: http.StatusNotFound,
			}
		}
		for _, k := range req.TagsToRemove {
			delete(t.Tags, k)
		}
		if err := s.store.putTable(ctx, t); err != nil {
			return nil, protocol.ErrInternalError
		}
		return &struct{}{}, nil
	}
	if dbName == "" {
		return nil, &protocol.AWSError{
			Code: "EntityNotFoundException", Message: "Resource not found",
			HTTPStatus: http.StatusNotFound,
		}
	}
	db, found := s.store.getDatabase(ctx, dbName)
	if !found {
		return nil, &protocol.AWSError{
			Code: "EntityNotFoundException", Message: fmt.Sprintf("Database %s not found", dbName),
			HTTPStatus: http.StatusNotFound,
		}
	}
	for _, k := range req.TagsToRemove {
		delete(db.Tags, k)
	}
	if err := s.store.putDatabase(ctx, db); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &struct{}{}, nil
}

func (s *Service) listTagsForResourceTyped(ctx context.Context, req *glueListTagsForResourceReq) (*glueListTagsForResourceResp, *protocol.AWSError) {
	dbName, tableName := glueARNToDBAndTable(req.ResourceArn)
	if tableName != "" {
		t, found := s.store.getTable(ctx, dbName, tableName)
		if !found {
			return nil, &protocol.AWSError{
				Code: "EntityNotFoundException", Message: fmt.Sprintf("Table %s not found in database %s", tableName, dbName),
				HTTPStatus: http.StatusNotFound,
			}
		}
		if t.Tags == nil {
			t.Tags = make(map[string]string)
		}
		return &glueListTagsForResourceResp{Tags: t.Tags}, nil
	}
	if dbName == "" {
		return nil, &protocol.AWSError{
			Code: "EntityNotFoundException", Message: "Resource not found",
			HTTPStatus: http.StatusNotFound,
		}
	}
	db, found := s.store.getDatabase(ctx, dbName)
	if !found {
		return nil, &protocol.AWSError{
			Code: "EntityNotFoundException", Message: fmt.Sprintf("Database %s not found", dbName),
			HTTPStatus: http.StatusNotFound,
		}
	}
	if db.Tags == nil {
		db.Tags = make(map[string]string)
	}
	return &glueListTagsForResourceResp{Tags: db.Tags}, nil
}
