package athena

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (s *Service) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"StartQueryExecution": op.NewTyped[startQueryExecReq, startQueryExecResp]("StartQueryExecution", s.startQueryExecutionTyped),
		"GetQueryExecution":   op.NewTyped[queryIDReq, getQueryExecResp]("GetQueryExecution", s.getQueryExecutionTyped),
		"GetQueryResults":     op.NewTyped[queryIDReq, getQueryResultsResp]("GetQueryResults", s.getQueryResultsTyped),
		"ListQueryExecutions": op.NewTyped[struct{}, listQueriesResp]("ListQueryExecutions", s.listQueryExecutionsTyped),
		"CreateWorkGroup":     op.NewTyped[createWorkGroupReq, struct{}]("CreateWorkGroup", s.createWorkGroupTyped),
		"GetWorkGroup":        op.NewTyped[workGroupNameReq, getWorkGroupResp]("GetWorkGroup", s.getWorkGroupTyped),
		"ListWorkGroups":      op.NewTyped[struct{}, listWorkGroupsResp]("ListWorkGroups", s.listWorkGroupsTyped),
		"DeleteWorkGroup":     op.NewTyped[workGroupNameReq, struct{}]("DeleteWorkGroup", s.deleteWorkGroupTyped),
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
