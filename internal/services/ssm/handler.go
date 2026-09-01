package ssm

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/protocol/op"
	"github.com/overcast-sh/overcast/internal/serviceutil"
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
		"PutParameter":           h.PutParameter,
		"GetParameter":           h.GetParameter,
		"GetParameters":          h.GetParameters,
		"GetParametersByPath":    h.GetParametersByPath,
		"DescribeParameters":     h.DescribeParameters,
		"GetParameterHistory":    h.GetParameterHistory,
		"AddTagsToResource":      h.AddTagsToResource,
		"RemoveTagsFromResource": h.RemoveTagsFromResource,
		"ListTagsForResource":    h.ListTagsForResource,
		"DeleteParameter":        h.DeleteParameter,
		"DeleteParameters":       h.DeleteParameters,
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
	DataType         string  `json:"DataType,omitempty" cbor:"DataType,omitempty"`
	AllowedPattern   string  `json:"AllowedPattern,omitempty" cbor:"AllowedPattern,omitempty"`
}

type historyParameterWire struct {
	Name             string  `json:"Name" cbor:"Name"`
	Type             string  `json:"Type" cbor:"Type"`
	Value            string  `json:"Value" cbor:"Value"`
	Version          int64   `json:"Version" cbor:"Version"`
	LastModifiedDate float64 `json:"LastModifiedDate" cbor:"LastModifiedDate"`
	Tier             string  `json:"Tier" cbor:"Tier"`
}

// toDescribeWire renders rec's metadata in DescribeParameters' shape. Shared
// by the legacy JSON handler and the typed CBOR handler so the two protocols
// cannot answer the same record differently.
func (h *Handler) toDescribeWire(rec *ParameterRecord, latest *ParameterVersion) describeParameterWire {
	return describeParameterWire{
		Name:             rec.Name,
		Type:             latest.Type,
		Description:      rec.Description,
		Version:          rec.Version(),
		LastModifiedDate: float64(latest.CreatedAt.UnixMilli()) / 1000.0,
		Policies:         policiesWire(rec.Policies),
		Tier:             rec.Tier,
		DataType:         rec.DataType,
		AllowedPattern:   rec.AllowedPattern,
	}
}

// toHistoryWire renders one version of rec in GetParameterHistory's shape.
// Shared for the same reason as toDescribeWire.
func (h *Handler) toHistoryWire(rec *ParameterRecord, v ParameterVersion, version int64) historyParameterWire {
	return historyParameterWire{
		Name:             rec.Name,
		Type:             v.Type,
		Value:            v.Value,
		Version:          version,
		LastModifiedDate: float64(v.CreatedAt.UnixMilli()) / 1000.0,
		Tier:             rec.Tier,
	}
}

// ─── Handlers ─────────────────────────────────────────────────────────────────

// PutParameter creates or overwrites a parameter.
func (h *Handler) PutParameter(w http.ResponseWriter, r *http.Request) {
	// Delegates to putParameterTyped (typed_logic.go) so the legacy
	// JSON1.0/1.1 path and the CBOR typed path share one implementation —
	// the legacy copy previously re-implemented this inline and silently
	// ignored Tags (#1196).
	var req putParameterRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	resp, aerr := h.putParameterTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{
		"Version": resp.Version,
		"Tier":    resp.Tier,
	}, "application/x-amz-json-1.1")
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

	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{
		"Parameter": h.toWire(rec, rec.Version(), rec.Latest(), req.WithDecryption),
	}, "application/x-amz-json-1.1")
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
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{
		"Parameters":        params,
		"InvalidParameters": invalid,
	}, "application/x-amz-json-1.1")
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
	protocol.WriteAWSJSON(w, r, http.StatusOK, resp, "application/x-amz-json-1.1")
}

// DescribeParameters returns parameter metadata with optional filters.
func (h *Handler) DescribeParameters(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ParameterFilters []parameterFilter `json:"ParameterFilters"`
		MaxResults       int               `json:"MaxResults"`
		NextToken        string            `json:"NextToken"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	// Refused before the scan, so an account with no parameters cannot answer
	// an unanswerable filter with an empty page that reads as "no match".
	if aerr := validateParameterFilters(req.ParameterFilters); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
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
		params = append(params, h.toDescribeWire(rec, latest))
	}
	resp := map[string]any{"Parameters": params}
	if page.NextToken != "" {
		resp["NextToken"] = page.NextToken
	}
	protocol.WriteAWSJSON(w, r, http.StatusOK, resp, "application/x-amz-json-1.1")
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
		params = append(params, h.toHistoryWire(rec, item.v, item.version))
	}
	resp := map[string]any{"Parameters": params}
	if page.NextToken != "" {
		resp["NextToken"] = page.NextToken
	}
	protocol.WriteAWSJSON(w, r, http.StatusOK, resp, "application/x-amz-json-1.1")
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
		protocol.WriteJSONError(w, r, errInvalidResourceId(req.ResourceId))
		return
	}
	tags := rec.GetTags()
	if tags == nil {
		tags = map[string]string{}
	}
	for _, t := range req.Tags {
		tags[t.Key] = t.Value
	}
	if aerr := serviceutil.ValidateTags(ssmTagCfg, tags); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	rec.SetTags(tags)
	if err := h.store.Put(ctx, rec); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{}, "application/x-amz-json-1.1")
}

// RemoveTagsFromResource removes tags from a parameter.
func (h *Handler) RemoveTagsFromResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceType string   `json:"ResourceType"`
		ResourceId   string   `json:"ResourceId"`
		TagKeys      []string `json:"TagKeys"`
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
		protocol.WriteJSONError(w, r, errInvalidResourceId(req.ResourceId))
		return
	}
	tags := rec.GetTags()
	for _, k := range req.TagKeys {
		delete(tags, k)
	}
	rec.SetTags(tags)
	if err := h.store.Put(ctx, rec); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{}, "application/x-amz-json-1.1")
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
		protocol.WriteJSONError(w, r, errInvalidResourceId(req.ResourceId))
		return
	}
	tags := make([]map[string]string, 0, len(rec.GetTags()))
	for k, v := range rec.GetTags() {
		tags = append(tags, map[string]string{"Key": k, "Value": v})
	}
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{"TagList": tags}, "application/x-amz-json-1.1")
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
			Payload: events.ResourcePayload{Name: req.Name, ARN: h.paramARN(req.Name)},
		})
	}
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{}, "application/x-amz-json-1.1")
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
				Payload: events.ResourcePayload{Name: name, ARN: h.paramARN(name)},
			})
		}
	}
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{
		"DeletedParameters": deleted,
		"InvalidParameters": invalid,
	}, "application/x-amz-json-1.1")
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
	dataType := rec.DataType
	if dataType == "" {
		dataType = "text"
	}
	return parameterWire{
		Name:             rec.Name,
		Type:             pv.Type,
		Value:            value,
		Version:          version,
		ARN:              h.paramARN(rec.Name),
		LastModifiedDate: float64(pv.CreatedAt.UnixMilli()) / 1000.0,
		DataType:         dataType,
	}
}

func errParameterNotFound(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ParameterNotFound",
		Message:    fmt.Sprintf("Parameter %s not found.", name),
		HTTPStatus: http.StatusBadRequest,
	}
}

// errInvalidResourceId is the error the tag operations answer for a missing
// resource — real SSM uses InvalidResourceId there, not ParameterNotFound.
func errInvalidResourceId(id string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "InvalidResourceId",
		Message:    fmt.Sprintf("Invalid resource ID: %s", id),
		HTTPStatus: http.StatusBadRequest,
	}
}

// ssmTagCfg tunes shared tag validation to SSM's error shape.
var ssmTagCfg = serviceutil.TagValidationConfig{
	ExceededCode:    "TooManyTagsError",
	InvalidCode:     "ValidationException",
	ExceededMessage: "The request exceeds the maximum number of tags allowed for the resource.",
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

// Ensure time package is used.
var _ = time.Now
