package athena

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/protocol"
)

type startQueryExecReq struct {
	QueryString         string `json:"QueryString" cbor:"QueryString"`
	WorkGroup           string `json:"WorkGroup" cbor:"WorkGroup"`
	ResultConfiguration struct {
		OutputLocation string `json:"OutputLocation" cbor:"OutputLocation"`
	} `json:"ResultConfiguration" cbor:"ResultConfiguration"`
}

type queryIDReq struct {
	QueryExecutionId string `json:"QueryExecutionId" cbor:"QueryExecutionId"`
}

type workGroupNameReq struct {
	WorkGroup string `json:"WorkGroup" cbor:"WorkGroup"`
}

type createWorkGroupReq struct {
	Name        string `json:"Name" cbor:"Name"`
	Description string `json:"Description" cbor:"Description"`
}

type startQueryExecResp struct {
	QueryExecutionId string `json:"QueryExecutionId" cbor:"QueryExecutionId"`
}

type getQueryExecResp struct {
	QueryExecution QueryExecution `json:"QueryExecution" cbor:"QueryExecution"`
}

type getQueryResultsResp struct {
	ResultSet resultSetWire `json:"ResultSet" cbor:"ResultSet"`
}

type resultSetWire struct {
	Rows              []any              `json:"Rows" cbor:"Rows"`
	ResultSetMetadata resultSetMetaWire `json:"ResultSetMetadata" cbor:"ResultSetMetadata"`
}

type resultSetMetaWire struct {
	ColumnInfo []any `json:"ColumnInfo" cbor:"ColumnInfo"`
}

type listQueriesResp struct {
	QueryExecutionIds []string `json:"QueryExecutionIds" cbor:"QueryExecutionIds"`
}

type getWorkGroupResp struct {
	WorkGroup WorkGroup `json:"WorkGroup" cbor:"WorkGroup"`
}

type listWorkGroupsResp struct {
	WorkGroups []workGroupSummary `json:"WorkGroups" cbor:"WorkGroups"`
}

type workGroupSummary struct {
	Name  string `json:"Name" cbor:"Name"`
	State string `json:"State" cbor:"State"`
}

func (s *Service) startQueryExecutionTyped(ctx context.Context, req *startQueryExecReq) (*startQueryExecResp, *protocol.AWSError) {
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
	if err := s.store.putQuery(ctx, qe); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &startQueryExecResp{QueryExecutionId: qe.QueryExecutionId}, nil
}

func (s *Service) getQueryExecutionTyped(ctx context.Context, req *queryIDReq) (*getQueryExecResp, *protocol.AWSError) {
	qe, found := s.store.getQuery(ctx, req.QueryExecutionId)
	if !found {
		return nil, &protocol.AWSError{
			Code: "InvalidRequestException", Message: fmt.Sprintf("QueryExecution %s not found", req.QueryExecutionId), HTTPStatus: http.StatusNotFound,
		}
	}
	return &getQueryExecResp{QueryExecution: *qe}, nil
}

func (s *Service) getQueryResultsTyped(ctx context.Context, req *queryIDReq) (*getQueryResultsResp, *protocol.AWSError) {
	if _, found := s.store.getQuery(ctx, req.QueryExecutionId); !found {
		return nil, &protocol.AWSError{
			Code: "InvalidRequestException", Message: fmt.Sprintf("QueryExecution %s not found", req.QueryExecutionId), HTTPStatus: http.StatusNotFound,
		}
	}
	return &getQueryResultsResp{ResultSet: resultSetWire{
		Rows:              []any{},
		ResultSetMetadata: resultSetMetaWire{ColumnInfo: []any{}},
	}}, nil
}

func (s *Service) listQueryExecutionsTyped(ctx context.Context, _ *struct{}) (*listQueriesResp, *protocol.AWSError) {
	queries, err := s.store.listQueries(ctx)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	ids := make([]string, 0, len(queries))
	for _, q := range queries {
		ids = append(ids, q.QueryExecutionId)
	}
	return &listQueriesResp{QueryExecutionIds: ids}, nil
}

func (s *Service) createWorkGroupTyped(ctx context.Context, req *createWorkGroupReq) (*struct{}, *protocol.AWSError) {
	if req.Name == "" {
		return nil, &protocol.AWSError{
			Code: "InvalidRequestException", Message: "Name is required", HTTPStatus: http.StatusBadRequest,
		}
	}
	wg := &WorkGroup{Name: req.Name, State: "ENABLED", Description: req.Description}
	if err := s.store.putWorkGroup(ctx, wg); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &struct{}{}, nil
}

func (s *Service) getWorkGroupTyped(ctx context.Context, req *workGroupNameReq) (*getWorkGroupResp, *protocol.AWSError) {
	wg, found := s.store.getWorkGroup(ctx, req.WorkGroup)
	if !found {
		return nil, &protocol.AWSError{
			Code: "InvalidRequestException", Message: fmt.Sprintf("WorkGroup %s not found", req.WorkGroup), HTTPStatus: http.StatusNotFound,
		}
	}
	return &getWorkGroupResp{WorkGroup: *wg}, nil
}

func (s *Service) listWorkGroupsTyped(ctx context.Context, _ *struct{}) (*listWorkGroupsResp, *protocol.AWSError) {
	workgroups, err := s.store.listWorkGroups(ctx)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	summaries := make([]workGroupSummary, 0, len(workgroups))
	for _, wg := range workgroups {
		summaries = append(summaries, workGroupSummary{Name: wg.Name, State: wg.State})
	}
	return &listWorkGroupsResp{WorkGroups: summaries}, nil
}

func (s *Service) deleteWorkGroupTyped(ctx context.Context, req *workGroupNameReq) (*struct{}, *protocol.AWSError) {
	if _, found := s.store.getWorkGroup(ctx, req.WorkGroup); !found {
		return nil, &protocol.AWSError{
			Code: "InvalidRequestException", Message: fmt.Sprintf("WorkGroup %s not found", req.WorkGroup), HTTPStatus: http.StatusNotFound,
		}
	}
	if err := s.store.deleteWorkGroup(ctx, req.WorkGroup); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &struct{}{}, nil
}
