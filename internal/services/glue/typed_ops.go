package glue

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (s *Service) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"CreateDatabase": op.NewTyped[createDatabaseReq, struct{}]("CreateDatabase", s.createDatabaseTyped),
		"GetDatabase":    op.NewTyped[getDatabaseReq, getDatabaseResp]("GetDatabase", s.getDatabaseTyped),
		"GetDatabases":   op.NewTyped[struct{}, getDatabasesResp]("GetDatabases", s.getDatabasesTyped),
		"DeleteDatabase": op.NewTyped[deleteDatabaseReq, struct{}]("DeleteDatabase", s.deleteDatabaseTyped),
		"CreateTable":    op.NewTyped[createTableReq, struct{}]("CreateTable", s.createTableTyped),
		"GetTable":       op.NewTyped[getTableReq, getTableResp]("GetTable", s.getTableTyped),
		"GetTables":      op.NewTyped[getTablesReq, getTablesResp]("GetTables", s.getTablesTyped),
		"DeleteTable":    op.NewTyped[deleteTableReq, struct{}]("DeleteTable", s.deleteTableTyped),
	}
}

func (s *Service) Operations() []op.Operation {
	ops := s.typedOp
	out := make([]op.Operation, 0, len(ops))
	for _, operation := range ops {
		out = append(out, operation)
	}
	return out
}

func (s *Service) SupportedProtocols() []codec.Codec {
	return []codec.Codec{codec.JSON10, codec.JSON11, codec.RPCv2CBOR}
}
