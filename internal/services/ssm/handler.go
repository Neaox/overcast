package ssm

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
)

const secureStringMasked = "kms:alias/aws/ssm:encrypted"

// Handler holds SSM handler dependencies.
type Handler struct {
	cfg   *config.Config
	store *Store
	log   *serviceutil.ServiceLogger
	clk   clock.Clock
	bus   *events.Bus
	ops   map[string]http.HandlerFunc

	typedOp map[string]op.Operation
}

func newHandler(cfg *config.Config, store *Store, log *serviceutil.ServiceLogger, clk clock.Clock) *Handler {
	h := &Handler{cfg: cfg, store: store, log: log, clk: clk}
	h.initOps()
	return h
}

// initOps registers every known SSM operation to its handler.
// Adding a new operation: add an entry here, implement in handler.go.
func (h *Handler) initOps() {
	h.ops = map[string]http.HandlerFunc{
		"PutParameter":        h.PutParameter,
		"GetParameter":        h.GetParameter,
		"GetParameters":       h.GetParameters,
		"GetParametersByPath": h.GetParametersByPath,
		"DescribeParameters":  h.DescribeParameters,
		"GetParameterHistory": h.GetParameterHistory,
		"AddTagsToResource":   h.AddTagsToResource,
		"ListTagsForResource": h.ListTagsForResource,
		"DeleteParameter":     h.DeleteParameter,
		"DeleteParameters":    h.DeleteParameters,
	}
	h.typedOp = h.typedOps()
}

type parameterWire struct {
	Name             string  `json:"Name" cbor:"Name"`
	Type             string  `json:"Type" cbor:"Type"`
	Value            string  `json:"Value" cbor:"Value"`
	Version          int64   `json:"Version" cbor:"Version"`
	ARN              string  `json:"ARN" cbor:"ARN"`
	LastModifiedDate float64 `json:"LastModifiedDate" cbor:"LastModifiedDate"`
	DataType         string  `json:"DataType" cbor:"DataType"`
}

type describeParameterWire struct {
	Name             string  `json:"Name" cbor:"Name"`
	Type             string  `json:"Type" cbor:"Type"`
	Description      string  `json:"Description,omitempty" cbor:"Description,omitempty"`
	Version          int64   `json:"Version" cbor:"Version"`
	LastModifiedDate float64 `json:"LastModifiedDate" cbor:"LastModifiedDate"`
	Policies         []any   `json:"Policies" cbor:"Policies"`
	Tier             string  `json:"Tier" cbor:"Tier"`
}

type historyParameterWire struct {
	Name             string  `json:"Name" cbor:"Name"`
	Type             string  `json:"Type" cbor:"Type"`
	Value            string  `json:"Value" cbor:"Value"`
	Version          int64   `json:"Version" cbor:"Version"`
	LastModifiedDate float64 `json:"LastModifiedDate" cbor:"LastModifiedDate"`
	Tier             string  `json:"Tier" cbor:"Tier"`
}

// ─── Handlers ─────────────────────────────────────────────────────────────────

// PutParameter creates or overwrites a parameter.
func (h *Handler) PutParameter(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"Name"`
		Value       string `json:"Value"`
		Type        string `json:"Type"`
		Description string `json:"Description"`
		Overwrite   bool   `json:"Overwrite"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("Name"))
		return
	}
	if req.Value == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("Value"))
		return
	}
	if req.Type == "" {
		req.Type = "String"
	}

	ctx := r.Context()
	existing, err := h.store.Get(ctx, req.Name)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if existing != nil && !req.Overwrite {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ParameterAlreadyExists",
			Message:    fmt.Sprintf("Parameter %s already exists.", req.Name),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	var rec *ParameterRecord
	if existing != nil {
		rec = existing
	} else {
		rec = &ParameterRecord{
			Name: req.Name,
			Tags: map[string]string{},
		}
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
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	if h.bus != nil {
		evType := events.SSMParameterCreated
		if existing != nil {
			evType = events.SSMParameterUpdated
		}
		h.bus.Publish(ctx, events.Event{
			Type: evType, Time: h.clk.Now(), Source: "ssm",
			Payload: events.ResourcePayload{Name: req.Name},
		})
	}

	writeJSON(w, r, http.StatusOK, map[string]any{
		"Version": rec.Version(),
		"Tier":    "Standard",
	})
}

// GetParameter returns the latest version of a parameter.
func (h *Handler) GetParameter(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name           string `json:"Name"`
		WithDecryption bool   `json:"WithDecryption"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("Name"))
		return
	}
	ctx := r.Context()
	rec, err := h.store.Get(ctx, req.Name)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if rec == nil || rec.Latest() == nil {
		protocol.WriteJSONError(w, r, errParameterNotFound(req.Name))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{
		"Parameter": h.toWire(rec, rec.Version(), rec.Latest(), req.WithDecryption),
	})
}

// GetParameters returns the latest version of multiple parameters.
func (h *Handler) GetParameters(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Names          []string `json:"Names"`
		WithDecryption bool     `json:"WithDecryption"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
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
	writeJSON(w, r, http.StatusOK, map[string]any{
		"Parameters":        params,
		"InvalidParameters": invalid,
	})
}

// GetParametersByPath returns parameters matching a path prefix.
func (h *Handler) GetParametersByPath(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path           string `json:"Path"`
		Recursive      bool   `json:"Recursive"`
		MaxResults     int    `json:"MaxResults"`
		NextToken      string `json:"NextToken"`
		WithDecryption bool   `json:"WithDecryption"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.Path == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("Path"))
		return
	}
	// Ensure path ends with / for prefix matching.
	prefix := req.Path
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	ctx := r.Context()
	all, err := h.store.Scan(ctx, prefix)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	// Filter: if not recursive, only direct children (no more slashes after prefix).
	var filtered []*ParameterRecord
	for _, p := range all {
		if !req.Recursive {
			suffix := strings.TrimPrefix(p.Name, prefix)
			if strings.Contains(suffix, "/") {
				continue
			}
		}
		filtered = append(filtered, p)
	}

	// AWS: MaxResults valid range is 1-10 for this op (default 10, no
	// higher value is honored): https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_GetParametersByPath.html#API_GetParametersByPath_RequestParameters
	page, err := serviceutil.Paginate(filtered, req.MaxResults, req.NextToken,
		serviceutil.PaginateOptions{DefaultLimit: 10, MaxLimit: 10})
	if err != nil {
		protocol.WriteJSONError(w, r, errInvalidNextToken())
		return
	}

	params := make([]parameterWire, 0, len(page.Items))
	for _, rec := range page.Items {
		params = append(params, h.toWire(rec, rec.Version(), rec.Latest(), req.WithDecryption))
	}

	resp := map[string]any{"Parameters": params}
	if page.NextToken != "" {
		resp["NextToken"] = page.NextToken
	}
	writeJSON(w, r, http.StatusOK, resp)
}

// DescribeParameters returns parameter metadata with optional filters.
func (h *Handler) DescribeParameters(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ParameterFilters []struct {
			Key    string   `json:"Key"`
			Option string   `json:"Option"`
			Values []string `json:"Values"`
		} `json:"ParameterFilters"`
		MaxResults int    `json:"MaxResults"`
		NextToken  string `json:"NextToken"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	all, err := h.store.Scan(ctx, "")
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	// Apply ParameterFilters.
	filtered := make([]*ParameterRecord, 0, len(all))
	for _, rec := range all {
		if matchesFilters(rec, req.ParameterFilters) {
			filtered = append(filtered, rec)
		}
	}

	// AWS: MaxResults valid range is 1-50 for this op: https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_DescribeParameters.html#API_DescribeParameters_RequestParameters
	page, err := serviceutil.Paginate(filtered, req.MaxResults, req.NextToken,
		serviceutil.PaginateOptions{DefaultLimit: 50, MaxLimit: 50})
	if err != nil {
		protocol.WriteJSONError(w, r, errInvalidNextToken())
		return
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
	resp := map[string]any{"Parameters": params}
	if page.NextToken != "" {
		resp["NextToken"] = page.NextToken
	}
	writeJSON(w, r, http.StatusOK, resp)
}

// matchesFilters returns true if the record satisfies all ParameterFilters.
func matchesFilters(rec *ParameterRecord, filters []struct {
	Key    string   `json:"Key"`
	Option string   `json:"Option"`
	Values []string `json:"Values"`
}) bool {
	for _, f := range filters {
		if f.Key == "Name" {
			if f.Option == "BeginsWith" {
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
	}
	return true
}

// GetParameterHistory returns all versions of a parameter.
func (h *Handler) GetParameterHistory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string `json:"Name"`
		NextToken  string `json:"NextToken"`
		MaxResults int    `json:"MaxResults"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("Name"))
		return
	}
	ctx := r.Context()
	rec, err := h.store.Get(ctx, req.Name)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if rec == nil {
		protocol.WriteJSONError(w, r, errParameterNotFound(req.Name))
		return
	}

	type versionedItem struct {
		v       ParameterVersion
		version int64
	}
	items := make([]versionedItem, 0, len(rec.Versions))
	for i, v := range rec.Versions {
		items = append(items, versionedItem{v: v, version: int64(i + 1)})
	}

	// AWS: MaxResults valid range is 1-50 for this op: https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_GetParameterHistory.html#API_GetParameterHistory_RequestParameters
	page, err := serviceutil.Paginate(items, req.MaxResults, req.NextToken,
		serviceutil.PaginateOptions{DefaultLimit: 50, MaxLimit: 50})
	if err != nil {
		protocol.WriteJSONError(w, r, errInvalidNextToken())
		return
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
	resp := map[string]any{"Parameters": params}
	if page.NextToken != "" {
		resp["NextToken"] = page.NextToken
	}
	writeJSON(w, r, http.StatusOK, resp)
}

// AddTagsToResource adds tags to a parameter.
func (h *Handler) AddTagsToResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceType string `json:"ResourceType"`
		ResourceId   string `json:"ResourceId"`
		Tags         []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"Tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.ResourceId == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("ResourceId"))
		return
	}
	ctx := r.Context()
	rec, err := h.store.Get(ctx, req.ResourceId)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if rec == nil {
		protocol.WriteJSONError(w, r, errParameterNotFound(req.ResourceId))
		return
	}
	if rec.Tags == nil {
		rec.Tags = map[string]string{}
	}
	for _, t := range req.Tags {
		rec.Tags[t.Key] = t.Value
	}
	if err := h.store.Put(ctx, rec); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{})
}

// ListTagsForResource returns tags associated with a parameter.
func (h *Handler) ListTagsForResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceType string `json:"ResourceType"`
		ResourceId   string `json:"ResourceId"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.ResourceId == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("ResourceId"))
		return
	}
	ctx := r.Context()
	rec, err := h.store.Get(ctx, req.ResourceId)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if rec == nil {
		protocol.WriteJSONError(w, r, errParameterNotFound(req.ResourceId))
		return
	}
	tags := make([]map[string]string, 0, len(rec.Tags))
	for k, v := range rec.Tags {
		tags = append(tags, map[string]string{"Key": k, "Value": v})
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"TagList": tags})
}

// DeleteParameter deletes a single parameter.
func (h *Handler) DeleteParameter(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"Name"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("Name"))
		return
	}
	ctx := r.Context()
	rec, err := h.store.Get(ctx, req.Name)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if rec == nil {
		protocol.WriteJSONError(w, r, errParameterNotFound(req.Name))
		return
	}
	if err := h.store.Delete(ctx, req.Name); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{
			Type: events.SSMParameterDeleted, Time: h.clk.Now(), Source: "ssm",
			Payload: events.ResourcePayload{Name: req.Name},
		})
	}
	writeJSON(w, r, http.StatusOK, map[string]any{})
}

// DeleteParameters deletes multiple parameters.
func (h *Handler) DeleteParameters(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Names []string `json:"Names"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
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
	if h.bus != nil {
		for _, name := range deleted {
			h.bus.Publish(ctx, events.Event{
				Type: events.SSMParameterDeleted, Time: h.clk.Now(), Source: "ssm",
				Payload: events.ResourcePayload{Name: name},
			})
		}
	}
	writeJSON(w, r, http.StatusOK, map[string]any{
		"DeletedParameters": deleted,
		"InvalidParameters": invalid,
	})
}

// ─── Helper functions ─────────────────────────────────────────────────────────

func (h *Handler) paramARN(name string) string {
	return fmt.Sprintf("arn:aws:ssm:us-east-1:%s:parameter%s", h.cfg.AccountID, name)
}

func (h *Handler) toWire(rec *ParameterRecord, version int64, pv *ParameterVersion, withDecryption bool) parameterWire {
	value := pv.Value
	if pv.Type == "SecureString" && !withDecryption {
		value = secureStringMasked
	}
	return parameterWire{
		Name:             rec.Name,
		Type:             pv.Type,
		Value:            value,
		Version:          version,
		ARN:              h.paramARN(rec.Name),
		LastModifiedDate: float64(pv.CreatedAt.UnixMilli()) / 1000.0,
		DataType:         "text",
	}
}

func errParameterNotFound(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ParameterNotFound",
		Message:    fmt.Sprintf("Parameter %s not found.", name),
		HTTPStatus: http.StatusBadRequest,
	}
}

// errInvalidNextToken maps a garbled/out-of-range pagination NextToken to
// SSM's documented error. A silent restart from page 1 (this codebase's
// most common pagination divergence, see docs/plans/pagination-plan.md G3)
// causes duplicate delivery to any client polling with a stale token.
// Verified against every List/Describe/GetHistory op that uses
// serviceutil.Paginate in this package — all three document InvalidNextToken,
// HTTP 400, "The specified token isn't valid.":
//   - https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_DescribeParameters.html#API_DescribeParameters_Errors
//   - https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_GetParametersByPath.html#API_GetParametersByPath_Errors
//   - https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_GetParameterHistory.html#API_GetParameterHistory_Errors
func errInvalidNextToken() *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "InvalidNextToken",
		Message:    "The specified token isn't valid.",
		HTTPStatus: http.StatusBadRequest,
	}
}

func writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	protocol.WriteAWSJSON(w, r, status, v, "application/x-amz-json-1.1")
}

// Ensure time package is used.
var _ = time.Now
