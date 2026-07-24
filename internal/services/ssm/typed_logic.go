package ssm

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

type putParameterRequest struct {
	Name        string `json:"Name" cbor:"Name"`
	Value       string `json:"Value" cbor:"Value"`
	Type        string `json:"Type" cbor:"Type"`
	Description string `json:"Description" cbor:"Description"`
	Overwrite   bool   `json:"Overwrite" cbor:"Overwrite"`
}

type putParameterResponse struct {
	Version int64  `json:"Version" cbor:"Version"`
	Tier    string `json:"Tier" cbor:"Tier"`
}

type getParameterRequest struct {
	Name           string `json:"Name" cbor:"Name"`
	WithDecryption bool   `json:"WithDecryption" cbor:"WithDecryption"`
}

type getParameterResponse struct {
	Parameter parameterWire `json:"Parameter" cbor:"Parameter"`
}

type getParametersRequest struct {
	Names          []string `json:"Names" cbor:"Names"`
	WithDecryption bool     `json:"WithDecryption" cbor:"WithDecryption"`
}

type getParametersResponse struct {
	Parameters        []parameterWire `json:"Parameters" cbor:"Parameters"`
	InvalidParameters []string        `json:"InvalidParameters" cbor:"InvalidParameters"`
}

type getParametersByPathRequest struct {
	Path           string `json:"Path" cbor:"Path"`
	Recursive      bool   `json:"Recursive" cbor:"Recursive"`
	MaxResults     int    `json:"MaxResults" cbor:"MaxResults"`
	NextToken      string `json:"NextToken" cbor:"NextToken"`
	WithDecryption bool   `json:"WithDecryption" cbor:"WithDecryption"`
}

type parametersPageResponse struct {
	Parameters []parameterWire `json:"Parameters" cbor:"Parameters"`
	NextToken  string          `json:"NextToken,omitempty" cbor:"NextToken,omitempty"`
}

type parameterFilter struct {
	Key    string   `json:"Key" cbor:"Key"`
	Option string   `json:"Option" cbor:"Option"`
	Values []string `json:"Values" cbor:"Values"`
}

type describeParametersRequest struct {
	ParameterFilters []parameterFilter `json:"ParameterFilters" cbor:"ParameterFilters"`
	MaxResults       int               `json:"MaxResults" cbor:"MaxResults"`
	NextToken        string            `json:"NextToken" cbor:"NextToken"`
}

type describeParametersResponse struct {
	Parameters []describeParameterWire `json:"Parameters" cbor:"Parameters"`
	NextToken  string                  `json:"NextToken,omitempty" cbor:"NextToken,omitempty"`
}

type getParameterHistoryRequest struct {
	Name       string `json:"Name" cbor:"Name"`
	NextToken  string `json:"NextToken" cbor:"NextToken"`
	MaxResults int    `json:"MaxResults" cbor:"MaxResults"`
}

type parameterHistoryResponse struct {
	Parameters []historyParameterWire `json:"Parameters" cbor:"Parameters"`
	NextToken  string                 `json:"NextToken,omitempty" cbor:"NextToken,omitempty"`
}

type resourceTag struct {
	Key   string `json:"Key" cbor:"Key"`
	Value string `json:"Value" cbor:"Value"`
}

type addTagsToResourceRequest struct {
	ResourceType string        `json:"ResourceType" cbor:"ResourceType"`
	ResourceId   string        `json:"ResourceId" cbor:"ResourceId"`
	Tags         []resourceTag `json:"Tags" cbor:"Tags"`
}

type resourceIDRequest struct {
	ResourceType string `json:"ResourceType" cbor:"ResourceType"`
	ResourceId   string `json:"ResourceId" cbor:"ResourceId"`
}

type listTagsForResourceResponse struct {
	TagList []resourceTag `json:"TagList" cbor:"TagList"`
}

type deleteParameterRequest struct {
	Name string `json:"Name" cbor:"Name"`
}

type deleteParametersRequest struct {
	Names []string `json:"Names" cbor:"Names"`
}

type deleteParametersResponse struct {
	DeletedParameters []string `json:"DeletedParameters" cbor:"DeletedParameters"`
	InvalidParameters []string `json:"InvalidParameters" cbor:"InvalidParameters"`
}

func (h *Handler) putParameterTyped(ctx context.Context, req *putParameterRequest) (*putParameterResponse, *protocol.AWSError) {
	if req.Name == "" {
		return nil, protocol.ErrMissingParameter("Name")
	}
	if req.Value == "" {
		return nil, protocol.ErrMissingParameter("Value")
	}
	if req.Type == "" {
		req.Type = "String"
	}

	existing, err := h.store.Get(ctx, req.Name)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if existing != nil && !req.Overwrite {
		return nil, &protocol.AWSError{
			Code:       "ParameterAlreadyExists",
			Message:    fmt.Sprintf("Parameter %s already exists.", req.Name),
			HTTPStatus: http.StatusBadRequest,
		}
	}

	rec := existing
	if rec == nil {
		rec = &ParameterRecord{Name: req.Name, Tags: map[string]string{}}
	}
	if req.Description != "" {
		rec.Description = req.Description
	}
	rec.Versions = append(rec.Versions, ParameterVersion{
		Value:     req.Value,
		Type:      req.Type,
		CreatedAt: h.clk.Now(),
	})
	if err := h.store.Put(ctx, rec); err != nil {
		return nil, protocol.ErrInternalError
	}

	evType := events.SSMParameterCreated
	if existing != nil {
		evType = events.SSMParameterUpdated
	}
	h.publishCtx(ctx, evType, req.Name)
	return &putParameterResponse{Version: rec.Version(), Tier: "Standard"}, nil
}

func (h *Handler) getParameterTyped(ctx context.Context, req *getParameterRequest) (*getParameterResponse, *protocol.AWSError) {
	if req.Name == "" {
		return nil, protocol.ErrMissingParameter("Name")
	}
	rec, aerr := h.requireParameter(ctx, req.Name)
	if aerr != nil {
		return nil, aerr
	}
	return &getParameterResponse{Parameter: h.toWire(rec, rec.Version(), rec.Latest(), req.WithDecryption)}, nil
}

func (h *Handler) getParametersTyped(ctx context.Context, req *getParametersRequest) (*getParametersResponse, *protocol.AWSError) {
	params := make([]parameterWire, 0, len(req.Names))
	invalid := make([]string, 0)
	for _, name := range req.Names {
		rec, err := h.store.Get(ctx, name)
		if err != nil || rec == nil || rec.Latest() == nil {
			invalid = append(invalid, name)
			continue
		}
		params = append(params, h.toWire(rec, rec.Version(), rec.Latest(), req.WithDecryption))
	}
	return &getParametersResponse{Parameters: params, InvalidParameters: invalid}, nil
}

func (h *Handler) getParametersByPathTyped(ctx context.Context, req *getParametersByPathRequest) (*parametersPageResponse, *protocol.AWSError) {
	if req.Path == "" {
		return nil, protocol.ErrMissingParameter("Path")
	}
	prefix := req.Path
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	all, err := h.store.Scan(ctx, prefix)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	filtered := make([]*ParameterRecord, 0, len(all))
	for _, p := range all {
		if !req.Recursive {
			suffix := strings.TrimPrefix(p.Name, prefix)
			if strings.Contains(suffix, "/") {
				continue
			}
		}
		filtered = append(filtered, p)
	}

	// See handler.go's GetParametersByPath for the AWS doc citation behind
	// this default/cap/error mapping (both call sites dispatch to the same
	// operation depending on wire protocol and must stay in sync).
	page, err := serviceutil.Paginate(filtered, req.MaxResults, req.NextToken,
		serviceutil.PaginateOptions{DefaultLimit: 10, MaxLimit: 10})
	if err != nil {
		return nil, errInvalidNextToken()
	}

	params := make([]parameterWire, 0, len(page.Items))
	for _, rec := range page.Items {
		params = append(params, h.toWire(rec, rec.Version(), rec.Latest(), req.WithDecryption))
	}
	return &parametersPageResponse{Parameters: params, NextToken: page.NextToken}, nil
}

func (h *Handler) describeParametersTyped(ctx context.Context, req *describeParametersRequest) (*describeParametersResponse, *protocol.AWSError) {
	all, err := h.store.Scan(ctx, "")
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	filtered := make([]*ParameterRecord, 0, len(all))
	for _, rec := range all {
		if matchesTypedFilters(rec, req.ParameterFilters) {
			filtered = append(filtered, rec)
		}
	}

	// See handler.go's DescribeParameters for the AWS doc citation behind
	// this default/cap/error mapping.
	page, err := serviceutil.Paginate(filtered, req.MaxResults, req.NextToken,
		serviceutil.PaginateOptions{DefaultLimit: 50, MaxLimit: 50})
	if err != nil {
		return nil, errInvalidNextToken()
	}

	params := make([]describeParameterWire, 0, len(page.Items))
	for _, rec := range page.Items {
		latest := rec.Latest()
		if latest == nil {
			continue
		}
		params = append(params, describeParameterWire{
			Name:             rec.Name,
			Type:             latest.Type,
			Description:      rec.Description,
			Version:          rec.Version(),
			LastModifiedDate: float64(latest.CreatedAt.UnixMilli()) / 1000.0,
			Policies:         []any{},
			Tier:             "Standard",
		})
	}
	return &describeParametersResponse{Parameters: params, NextToken: page.NextToken}, nil
}

func (h *Handler) getParameterHistoryTyped(ctx context.Context, req *getParameterHistoryRequest) (*parameterHistoryResponse, *protocol.AWSError) {
	if req.Name == "" {
		return nil, protocol.ErrMissingParameter("Name")
	}
	rec, err := h.store.Get(ctx, req.Name)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if rec == nil {
		return nil, errParameterNotFound(req.Name)
	}

	type versionedItem struct {
		v       ParameterVersion
		version int64
	}
	items := make([]versionedItem, 0, len(rec.Versions))
	for i, v := range rec.Versions {
		items = append(items, versionedItem{v: v, version: int64(i + 1)})
	}

	// See handler.go's GetParameterHistory for the AWS doc citation behind
	// this default/cap/error mapping.
	page, err := serviceutil.Paginate(items, req.MaxResults, req.NextToken,
		serviceutil.PaginateOptions{DefaultLimit: 50, MaxLimit: 50})
	if err != nil {
		return nil, errInvalidNextToken()
	}

	params := make([]historyParameterWire, 0, len(page.Items))
	for _, item := range page.Items {
		params = append(params, historyParameterWire{
			Name:             rec.Name,
			Type:             item.v.Type,
			Value:            item.v.Value,
			Version:          item.version,
			LastModifiedDate: float64(item.v.CreatedAt.UnixMilli()) / 1000.0,
			Tier:             "Standard",
		})
	}
	return &parameterHistoryResponse{Parameters: params, NextToken: page.NextToken}, nil
}

func (h *Handler) addTagsToResourceTyped(ctx context.Context, req *addTagsToResourceRequest) (*struct{}, *protocol.AWSError) {
	if req.ResourceId == "" {
		return nil, protocol.ErrMissingParameter("ResourceId")
	}
	rec, aerr := h.requireParameter(ctx, req.ResourceId)
	if aerr != nil {
		return nil, aerr
	}
	if rec.Tags == nil {
		rec.Tags = map[string]string{}
	}
	for _, t := range req.Tags {
		rec.Tags[t.Key] = t.Value
	}
	if err := h.store.Put(ctx, rec); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &struct{}{}, nil
}

func (h *Handler) listTagsForResourceTyped(ctx context.Context, req *resourceIDRequest) (*listTagsForResourceResponse, *protocol.AWSError) {
	if req.ResourceId == "" {
		return nil, protocol.ErrMissingParameter("ResourceId")
	}
	rec, aerr := h.requireParameter(ctx, req.ResourceId)
	if aerr != nil {
		return nil, aerr
	}
	tags := make([]resourceTag, 0, len(rec.Tags))
	for k, v := range rec.Tags {
		tags = append(tags, resourceTag{Key: k, Value: v})
	}
	return &listTagsForResourceResponse{TagList: tags}, nil
}

func (h *Handler) deleteParameterTyped(ctx context.Context, req *deleteParameterRequest) (*struct{}, *protocol.AWSError) {
	if req.Name == "" {
		return nil, protocol.ErrMissingParameter("Name")
	}
	rec, aerr := h.requireParameter(ctx, req.Name)
	if aerr != nil {
		return nil, aerr
	}
	if err := h.store.Delete(ctx, rec.Name); err != nil {
		return nil, protocol.ErrInternalError
	}
	h.publishCtx(ctx, events.SSMParameterDeleted, req.Name)
	return &struct{}{}, nil
}

func (h *Handler) deleteParametersTyped(ctx context.Context, req *deleteParametersRequest) (*deleteParametersResponse, *protocol.AWSError) {
	deleted := make([]string, 0, len(req.Names))
	invalid := make([]string, 0)
	for _, name := range req.Names {
		rec, err := h.store.Get(ctx, name)
		if err != nil || rec == nil {
			invalid = append(invalid, name)
			continue
		}
		if err := h.store.Delete(ctx, name); err != nil {
			invalid = append(invalid, name)
			continue
		}
		deleted = append(deleted, name)
	}
	for _, name := range deleted {
		h.publishCtx(ctx, events.SSMParameterDeleted, name)
	}
	return &deleteParametersResponse{DeletedParameters: deleted, InvalidParameters: invalid}, nil
}

func (h *Handler) requireParameter(ctx context.Context, name string) (*ParameterRecord, *protocol.AWSError) {
	rec, err := h.store.Get(ctx, name)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if rec == nil || rec.Latest() == nil {
		return nil, errParameterNotFound(name)
	}
	return rec, nil
}

func (h *Handler) publishCtx(ctx context.Context, t events.Type, name string) {
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{
			Type: t, Time: h.clk.Now(), Source: "ssm",
			Payload: events.ResourcePayload{Name: name},
		})
	}
}

func matchesTypedFilters(rec *ParameterRecord, filters []parameterFilter) bool {
	for _, f := range filters {
		if f.Key == "Name" && f.Option == "BeginsWith" {
			matched := false
			for _, v := range f.Values {
				if strings.HasPrefix(rec.Name, v) {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		}
	}
	return true
}
