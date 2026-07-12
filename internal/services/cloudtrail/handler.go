package cloudtrail

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// Handler holds CloudTrail handler dependencies.
type Handler struct {
	ops     map[string]http.HandlerFunc
	typedOp map[string]op.Operation
	cfg     *config.Config
	store   state.Store
	log     *serviceutil.ServiceLogger
}

func newHandler(cfg *config.Config, store state.Store, log *serviceutil.ServiceLogger) *Handler {
	h := &Handler{cfg: cfg, store: store, log: log}
	h.initOps()
	return h
}

func (h *Handler) initOps() {
	h.ops = map[string]http.HandlerFunc{
		"CreateTrail":    h.createTrail,
		"DescribeTrails": h.describeTrails,
		"UpdateTrail":    h.updateTrail,
		"DeleteTrail":    h.deleteTrail,
		"ListTrails":     h.listTrails,
		"GetTrailStatus": h.getTrailStatus,
		"StartLogging":   h.startLogging,
		"StopLogging":    h.stopLogging,
		"LookupEvents":   h.lookupEvents,
	}
	h.typedOp = h.typedOps()
}

type trail struct {
	Name                       string `json:"Name"`
	S3BucketName               string `json:"S3BucketName"`
	S3KeyPrefix                string `json:"S3KeyPrefix,omitempty"`
	IncludeGlobalServiceEvents bool   `json:"IncludeGlobalServiceEvents"`
	IsMultiRegionTrail         bool   `json:"IsMultiRegionTrail"`
	HomeRegion                 string `json:"HomeRegion"`
	TrailARN                   string `json:"TrailARN"`
	LogFileValidationEnabled   bool   `json:"LogFileValidationEnabled"`
	CloudWatchLogsLogGroupArn  string `json:"CloudWatchLogsLogGroupArn,omitempty"`
	CloudWatchLogsRoleArn      string `json:"CloudWatchLogsRoleArn,omitempty"`
	KmsKeyId                   string `json:"KmsKeyId,omitempty"`
	IsOrganizationTrail        bool   `json:"IsOrganizationTrail"`
	IsLogging                  bool   `json:"IsLogging"`
}

func (h *Handler) createTrail(w http.ResponseWriter, r *http.Request) {
	var in createTrailInput
	if !decodeJSONBody(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.S3BucketName) == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidTrailNameException",
			Message:    "Name and S3BucketName are required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	ctx := r.Context()
	if _, exists, aerr := h.getTrail(ctx, in.Name); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	} else if exists {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "TrailAlreadyExistsException",
			Message:    "Trail already exists",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	t := trail{
		Name:                       in.Name,
		S3BucketName:               in.S3BucketName,
		S3KeyPrefix:                in.S3KeyPrefix,
		IncludeGlobalServiceEvents: in.IncludeGlobalServiceEvents,
		IsMultiRegionTrail:         in.IsMultiRegionTrail,
		HomeRegion:                 h.region(),
		TrailARN:                   h.trailARN(in.Name),
		LogFileValidationEnabled:   in.EnableLogFileValidation,
		CloudWatchLogsLogGroupArn:  in.CloudWatchLogsLogGroupArn,
		CloudWatchLogsRoleArn:      in.CloudWatchLogsRoleArn,
		KmsKeyId:                   in.KmsKeyId,
		IsOrganizationTrail:        in.IsOrganizationTrail,
		IsLogging:                  false,
	}

	if aerr := h.putTrail(ctx, &t); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, trailToCreateOutput(&t))
}

func (h *Handler) describeTrails(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TrailNameList []string `json:"trailNameList"`
	}
	if !decodeJSONBody(w, r, &in) {
		return
	}

	ctx := r.Context()
	trails, aerr := h.listAllTrails(ctx)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	allow := make(map[string]struct{}, len(in.TrailNameList))
	if len(in.TrailNameList) > 0 {
		for _, name := range in.TrailNameList {
			allow[name] = struct{}{}
		}
	}

	out := make([]map[string]any, 0, len(trails))
	for i := range trails {
		if len(allow) > 0 {
			if _, ok := allow[trails[i].Name]; !ok {
				continue
			}
		}
		out = append(out, trailToDescribeEntry(&trails[i]))
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"trailList": out})
}

func (h *Handler) updateTrail(w http.ResponseWriter, r *http.Request) {
	var in updateTrailInput
	if !decodeJSONBody(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidTrailNameException",
			Message:    "Name is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	ctx := r.Context()
	t, exists, aerr := h.getTrail(ctx, in.Name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if !exists {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "TrailNotFoundException",
			Message:    "Trail not found",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	if in.S3BucketName != nil {
		t.S3BucketName = *in.S3BucketName
	}
	if in.S3KeyPrefix != nil {
		t.S3KeyPrefix = *in.S3KeyPrefix
	}
	if in.IncludeGlobalServiceEvents != nil {
		t.IncludeGlobalServiceEvents = *in.IncludeGlobalServiceEvents
	}
	if in.IsMultiRegionTrail != nil {
		t.IsMultiRegionTrail = *in.IsMultiRegionTrail
	}
	if in.EnableLogFileValidation != nil {
		t.LogFileValidationEnabled = *in.EnableLogFileValidation
	}
	if in.CloudWatchLogsLogGroupArn != nil {
		t.CloudWatchLogsLogGroupArn = *in.CloudWatchLogsLogGroupArn
	}
	if in.CloudWatchLogsRoleArn != nil {
		t.CloudWatchLogsRoleArn = *in.CloudWatchLogsRoleArn
	}
	if in.KmsKeyId != nil {
		t.KmsKeyId = *in.KmsKeyId
	}
	if in.IsOrganizationTrail != nil {
		t.IsOrganizationTrail = *in.IsOrganizationTrail
	}

	if aerr := h.putTrail(ctx, t); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, trailToCreateOutput(t))
}

func (h *Handler) deleteTrail(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"Name"`
	}
	if !decodeJSONBody(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidTrailNameException",
			Message:    "Name is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	ctx := r.Context()
	if _, exists, aerr := h.getTrail(ctx, in.Name); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	} else if !exists {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "TrailNotFoundException",
			Message:    "Trail not found",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	if err := h.store.Delete(ctx, nsTrails, in.Name); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (h *Handler) listTrails(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	trails, aerr := h.listAllTrails(ctx)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	out := make([]map[string]any, 0, len(trails))
	for i := range trails {
		out = append(out, map[string]any{
			"Name":       trails[i].Name,
			"TrailARN":   trails[i].TrailARN,
			"HomeRegion": trails[i].HomeRegion,
		})
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Trails": out})
}

func (h *Handler) getTrailStatus(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"Name"`
	}
	if !decodeJSONBody(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidTrailNameException",
			Message:    "Name is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	t, exists, aerr := h.getTrail(r.Context(), in.Name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if !exists {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "TrailNotFoundException",
			Message:    "Trail not found",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"IsLogging":                 t.IsLogging,
		"LatestDeliveryError":       "",
		"LatestNotificationError":   "",
		"LatestCloudWatchLogsError": "",
		"LatestDigestDeliveryError": "",
	})
}

func (h *Handler) startLogging(w http.ResponseWriter, r *http.Request) {
	h.setLogging(w, r, true)
}

func (h *Handler) stopLogging(w http.ResponseWriter, r *http.Request) {
	h.setLogging(w, r, false)
}

func (h *Handler) setLogging(w http.ResponseWriter, r *http.Request, logging bool) {
	var in struct {
		Name string `json:"Name"`
	}
	if !decodeJSONBody(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidTrailNameException",
			Message:    "Name is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	ctx := r.Context()
	t, exists, aerr := h.getTrail(ctx, in.Name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if !exists {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "TrailNotFoundException",
			Message:    "Trail not found",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	if t.IsLogging == logging {
		protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
		return
	}
	t.IsLogging = logging
	if aerr := h.putTrail(ctx, t); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (h *Handler) lookupEvents(w http.ResponseWriter, r *http.Request) {
	var in map[string]any
	if !decodeJSONBody(w, r, &in) {
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"Events":    []any{},
		"NextToken": nil,
	})
}

func (h *Handler) getTrail(ctx context.Context, name string) (*trail, bool, *protocol.AWSError) {
	raw, ok, err := h.store.Get(ctx, nsTrails, name)
	if err != nil {
		return nil, false, protocol.ErrInternalError
	}
	if !ok {
		return nil, false, nil
	}
	var t trail
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		return nil, false, protocol.ErrInternalError
	}
	return &t, true, nil
}

func (h *Handler) putTrail(ctx context.Context, t *trail) *protocol.AWSError {
	b, err := json.Marshal(t)
	if err != nil {
		return protocol.ErrInternalError
	}
	if err := h.store.Set(ctx, nsTrails, t.Name, string(b)); err != nil {
		return protocol.ErrInternalError
	}
	return nil
}

func (h *Handler) listAllTrails(ctx context.Context) ([]trail, *protocol.AWSError) {
	kvs, err := h.store.Scan(ctx, nsTrails, "")
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	out := make([]trail, 0, len(kvs))
	for _, kv := range kvs {
		var t trail
		if err := json.Unmarshal([]byte(kv.Value), &t); err != nil {
			return nil, protocol.ErrInternalError
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (h *Handler) region() string {
	if h.cfg != nil && h.cfg.Region != "" {
		return h.cfg.Region
	}
	return "us-east-1"
}

func (h *Handler) accountID() string {
	if h.cfg != nil && h.cfg.AccountID != "" {
		return h.cfg.AccountID
	}
	return "000000000000"
}

func (h *Handler) trailARN(name string) string {
	return fmt.Sprintf("arn:aws:cloudtrail:%s:%s:trail/%s", h.region(), h.accountID(), name)
}

func trailToCreateOutput(t *trail) map[string]any {
	return map[string]any{
		"Name":                       t.Name,
		"S3BucketName":               t.S3BucketName,
		"S3KeyPrefix":                t.S3KeyPrefix,
		"IncludeGlobalServiceEvents": t.IncludeGlobalServiceEvents,
		"IsMultiRegionTrail":         t.IsMultiRegionTrail,
		"TrailARN":                   t.TrailARN,
		"LogFileValidationEnabled":   t.LogFileValidationEnabled,
		"CloudWatchLogsLogGroupArn":  t.CloudWatchLogsLogGroupArn,
		"CloudWatchLogsRoleArn":      t.CloudWatchLogsRoleArn,
		"KmsKeyId":                   t.KmsKeyId,
		"IsOrganizationTrail":        t.IsOrganizationTrail,
	}
}

func trailToDescribeEntry(t *trail) map[string]any {
	return map[string]any{
		"Name":                       t.Name,
		"S3BucketName":               t.S3BucketName,
		"S3KeyPrefix":                t.S3KeyPrefix,
		"IncludeGlobalServiceEvents": t.IncludeGlobalServiceEvents,
		"IsMultiRegionTrail":         t.IsMultiRegionTrail,
		"HomeRegion":                 t.HomeRegion,
		"TrailARN":                   t.TrailARN,
		"LogFileValidationEnabled":   t.LogFileValidationEnabled,
		"CloudWatchLogsLogGroupArn":  t.CloudWatchLogsLogGroupArn,
		"CloudWatchLogsRoleArn":      t.CloudWatchLogsRoleArn,
		"KmsKeyId":                   t.KmsKeyId,
		"HasCustomEventSelectors":    false,
		"HasInsightSelectors":        false,
		"IsOrganizationTrail":        t.IsOrganizationTrail,
	}
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, out any) bool {
	if r.Body == nil {
		return true
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(out); err != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "SerializationException",
			Message:    "Invalid JSON request body",
			HTTPStatus: http.StatusBadRequest,
		})
		return false
	}
	return true
}
