package logs

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	eventsbus "github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// Handler holds CloudWatch Logs handler dependencies.
type Handler struct {
	cfg     *config.Config
	store   *logsStore
	log     *serviceutil.ServiceLogger
	clk     clock.Clock
	ops     map[string]http.HandlerFunc
	typedOp map[string]op.Operation
	bus     *eventsbus.Bus
}

// newHandler constructs a Handler from the raw dependencies.
func newHandler(cfg *config.Config, store state.Store, log *serviceutil.ServiceLogger, clk clock.Clock) *Handler {
	h := &Handler{
		cfg:   cfg,
		store: newLogsStore(store, clk, cfg.Region),
		log:   log,
		clk:   clk,
	}
	h.initOps()
	return h
}

// initOps registers every known CloudWatch Logs operation to its handler.
// Implemented operations point to their handler method; stubs live in handler_stubs.go.
func (h *Handler) initOps() {
	h.ops = map[string]http.HandlerFunc{
		// P1 — implemented
		"CreateLogGroup":     h.CreateLogGroup,
		"DescribeLogGroups":  h.DescribeLogGroups,
		"CreateLogStream":    h.CreateLogStream,
		"DescribeLogStreams": h.DescribeLogStreams,
		"PutLogEvents":       h.PutLogEvents,
		"GetLogEvents":       h.GetLogEvents,
		"StartLiveTail":      h.StartLiveTail,
		// P2 — stubs (handler_stubs.go)
		"DeleteLogGroup":  h.DeleteLogGroup,
		"DeleteLogStream": h.DeleteLogStream,
		"FilterLogEvents": h.FilterLogEvents,
		// P3 — implemented
		"PutRetentionPolicy":    h.PutRetentionPolicy,
		"DeleteRetentionPolicy": h.DeleteRetentionPolicy,
		// P3 — stubs (handler_stubs.go)
		"PutSubscriptionFilter": h.PutSubscriptionFilter,
		"StartQuery":            h.StartQuery,
		"GetQueryResults":       h.GetQueryResults,
		"TagLogGroup":           h.TagLogGroup,
		"UntagLogGroup":         h.UntagLogGroup,
		"ListTagsLogGroup":      h.ListTagsLogGroup,
		"TagResource":           h.TagResource,
		"UntagResource":         h.UntagResource,
		"ListTagsForResource":   h.ListTagsForResource,
		"PutMetricFilter":       h.PutMetricFilter,
	}
	h.typedOp = h.typedOps()
}

// ---- P1 handlers -----------------------------------------------------------

// CreateLogGroup creates a new log group, optionally with create-time tags.
// Delegates to createLogGroupTyped (typed_logic.go) so the JSON and
// CBOR/typed-operation paths share one implementation — including tag
// validation and atomic create-time tagging. Keeping two copies is what let
// the typed model silently drop `tags` (#676).
// AWS docs: https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_CreateLogGroup.html
func (h *Handler) CreateLogGroup(w http.ResponseWriter, r *http.Request) {
	var req createLogGroupRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if _, aerr := h.createLogGroupTyped(r.Context(), &req); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, struct{}{})
}

// DescribeLogGroups returns a list of log groups.
// AWS docs: https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_DescribeLogGroups.html
func (h *Handler) DescribeLogGroups(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LogGroupNamePrefix string `json:"logGroupNamePrefix,omitempty"`
		Limit              int    `json:"limit,omitempty"`
		NextToken          string `json:"nextToken,omitempty"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	groups, aerr := h.store.listLogGroups(r.Context(), req.LogGroupNamePrefix)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	type logGroupResp struct {
		LogGroupName    string `json:"logGroupName"`
		ARN             string `json:"arn"`
		CreationTime    int64  `json:"creationTime"`
		RetentionInDays int    `json:"retentionInDays,omitempty"`
	}
	out := make([]logGroupResp, 0, len(groups))
	for _, g := range groups {
		out = append(out, logGroupResp{
			LogGroupName:    g.Name,
			ARN:             g.ARN,
			CreationTime:    g.CreationTime,
			RetentionInDays: g.RetentionInDays,
		})
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"logGroups": out,
	})
}

// CreateLogStream creates a new log stream within a group.
// AWS docs: https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_CreateLogStream.html
// DeleteLogGroup deletes a log group and all associated streams and events.
func (h *Handler) DeleteLogGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LogGroupName string `json:"logGroupName"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.LogGroupName == "" {
		protocol.WriteJSONError(w, r, errInvalidParameter("logGroupName is required"))
		return
	}
	// Verify the group exists.
	if _, aerr := h.store.getLogGroup(r.Context(), req.LogGroupName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if aerr := h.store.deleteLogGroup(r.Context(), req.LogGroupName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, struct{}{})
}

// DeleteLogStream deletes a log stream and all its events.
func (h *Handler) DeleteLogStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LogGroupName  string `json:"logGroupName"`
		LogStreamName string `json:"logStreamName"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.LogGroupName == "" || req.LogStreamName == "" {
		protocol.WriteJSONError(w, r, errInvalidParameter("logGroupName and logStreamName are required"))
		return
	}
	// Verify the group exists.
	if _, aerr := h.store.getLogGroup(r.Context(), req.LogGroupName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	// Verify the stream exists.
	if _, aerr := h.store.getLogStream(r.Context(), req.LogGroupName, req.LogStreamName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if aerr := h.store.deleteLogStream(r.Context(), req.LogGroupName, req.LogStreamName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, struct{}{})
}

func (h *Handler) CreateLogStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LogGroupName  string `json:"logGroupName"`
		LogStreamName string `json:"logStreamName"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.LogGroupName == "" || req.LogStreamName == "" {
		protocol.WriteJSONError(w, r, errInvalidParameter("logGroupName and logStreamName are required"))
		return
	}

	ctx := r.Context()

	// Group must exist.
	if _, aerr := h.store.getLogGroup(ctx, req.LogGroupName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Check for duplicate stream.
	if _, aerr := h.store.getLogStream(ctx, req.LogGroupName, req.LogStreamName); aerr == nil {
		protocol.WriteJSONError(w, r, errStreamAlreadyExists(req.LogStreamName))
		return
	}

	ls := &LogStream{
		Name:                req.LogStreamName,
		ARN:                 protocol.LogStreamARN(middleware.RegionFromContext(r.Context(), h.cfg.Region), h.cfg.AccountID, req.LogGroupName, req.LogStreamName),
		CreationTime:        h.clk.Now().UnixMilli(),
		UploadSequenceToken: "1",
	}
	if aerr := h.store.putLogStream(ctx, req.LogGroupName, ls); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if h.bus != nil {
		h.bus.Publish(ctx, eventsbus.Event{
			Type:    eventsbus.LogStreamCreated,
			Time:    h.clk.Now(),
			Source:  "logs",
			Payload: eventsbus.ResourcePayload{Name: req.LogGroupName + "/" + req.LogStreamName},
		})
	}

	protocol.WriteJSON(w, r, http.StatusOK, struct{}{})
}

// DescribeLogStreams lists log streams within a group.
// AWS docs: https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_DescribeLogStreams.html
func (h *Handler) DescribeLogStreams(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LogGroupName        string `json:"logGroupName"`
		LogStreamNamePrefix string `json:"logStreamNamePrefix,omitempty"`
		Limit               int    `json:"limit,omitempty"`
		NextToken           string `json:"nextToken,omitempty"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.LogGroupName == "" {
		protocol.WriteJSONError(w, r, errInvalidParameter("logGroupName is required"))
		return
	}

	ctx := r.Context()

	// Group must exist.
	if _, aerr := h.store.getLogGroup(ctx, req.LogGroupName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	streams, aerr := h.store.listLogStreams(ctx, req.LogGroupName, req.LogStreamNamePrefix)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	type logStreamResp struct {
		LogStreamName       string `json:"logStreamName"`
		ARN                 string `json:"arn"`
		CreationTime        int64  `json:"creationTime"`
		FirstEventTimestamp int64  `json:"firstEventTimestamp,omitempty"`
		LastEventTimestamp  int64  `json:"lastEventTimestamp,omitempty"`
		LastIngestionTime   int64  `json:"lastIngestionTime,omitempty"`
		UploadSequenceToken string `json:"uploadSequenceToken,omitempty"`
	}
	out := make([]logStreamResp, 0, len(streams))
	for _, s := range streams {
		out = append(out, logStreamResp{
			LogStreamName:       s.Name,
			ARN:                 s.ARN,
			CreationTime:        s.CreationTime,
			FirstEventTimestamp: s.FirstEventTimestamp,
			LastEventTimestamp:  s.LastEventTimestamp,
			LastIngestionTime:   s.LastIngestionTime,
			UploadSequenceToken: s.UploadSequenceToken,
		})
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"logStreams": out,
	})
}

// PutLogEvents appends log events to a stream.
// AWS docs: https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_PutLogEvents.html
func (h *Handler) PutLogEvents(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LogGroupName  string `json:"logGroupName"`
		LogStreamName string `json:"logStreamName"`
		LogEvents     []struct {
			Timestamp int64  `json:"timestamp"`
			Message   string `json:"message"`
		} `json:"logEvents"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.LogGroupName == "" || req.LogStreamName == "" {
		protocol.WriteJSONError(w, r, errInvalidParameter("logGroupName and logStreamName are required"))
		return
	}
	if len(req.LogEvents) == 0 {
		protocol.WriteJSONError(w, r, errInvalidParameter("logEvents must not be empty"))
		return
	}

	ctx := r.Context()

	// Group must exist.
	if _, aerr := h.store.getLogGroup(ctx, req.LogGroupName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Stream must exist.
	ls, aerr := h.store.getLogStream(ctx, req.LogGroupName, req.LogStreamName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	now := h.clk.Now().UnixMilli()
	events := make([]LogEvent, 0, len(req.LogEvents))
	for _, e := range req.LogEvents {
		events = append(events, LogEvent{
			Timestamp:     e.Timestamp,
			Message:       e.Message,
			IngestionTime: now,
		})
	}

	if aerr := h.store.appendEvents(ctx, req.LogGroupName, req.LogStreamName, events); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Update stream metadata.
	sort.Slice(events, func(i, j int) bool { return events[i].Timestamp < events[j].Timestamp })
	firstTs := events[0].Timestamp
	lastTs := events[len(events)-1].Timestamp
	if ls.FirstEventTimestamp == 0 || firstTs < ls.FirstEventTimestamp {
		ls.FirstEventTimestamp = firstTs
	}
	if lastTs > ls.LastEventTimestamp {
		ls.LastEventTimestamp = lastTs
	}
	ls.LastIngestionTime = now

	// Increment sequence token.
	seq, _ := strconv.Atoi(ls.UploadSequenceToken)
	ls.UploadSequenceToken = fmt.Sprintf("%d", seq+1)

	if aerr := h.store.putLogStream(ctx, req.LogGroupName, ls); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"nextSequenceToken": ls.UploadSequenceToken,
	})

	// Publish to the event bus so connected clients can tail this stream in real time.
	if h.bus != nil {
		items := make([]eventsbus.LogEventItem, 0, len(events))
		for _, e := range events {
			items = append(items, eventsbus.LogEventItem{
				Timestamp: e.Timestamp,
				Message:   e.Message,
			})
		}
		h.bus.Publish(ctx, eventsbus.Event{
			Type:   eventsbus.LogEventsWritten,
			Source: "logs",
			Payload: eventsbus.LogEventsWrittenPayload{
				LogGroupName:  req.LogGroupName,
				LogStreamName: req.LogStreamName,
				Events:        items,
			},
		})
	}
}

// AWS docs: https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_GetLogEvents.html
//
// Delegates to getLogEventsTyped (typed_logic.go) so the JSON 1.1 wire path
// (registered here in h.ops, still preferred over the typed-operation path
// for JSON per Service.Dispatch's doc comment) and the CBOR/typed-operation
// path share one implementation of GetLogEvents' pagination contract
// (pagination-plan.md G1) instead of two copies drifting apart.
func (h *Handler) GetLogEvents(w http.ResponseWriter, r *http.Request) {
	var req getLogEventsRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	resp, aerr := h.getLogEventsTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, resp)
}

// FilterLogEvents searches log events across one or more streams in a log group.
// Supports the full CloudWatch Logs filter pattern syntax: text patterns (AND,
// quoted phrases, ?OR), JSON patterns ({ $.field op value } with &&/||), time
// range, logStreamNames, and logStreamNamePrefix.
// AWS docs: https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_FilterLogEvents.html
//
// Delegates to filterLogEventsTyped (typed_logic.go) — see GetLogEvents'
// doc comment above for why the JSON and CBOR/typed-operation paths share
// one implementation of FilterLogEvents' limit + nextToken contract
// (pagination-plan.md G6).
func (h *Handler) FilterLogEvents(w http.ResponseWriter, r *http.Request) {
	var req filterLogEventsRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	resp, aerr := h.filterLogEventsTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, resp)
}

// ---- Retention policy -------------------------------------------------------

// validRetentionDays is the fixed set of retentionInDays values AWS accepts
// for PutRetentionPolicy; anything else is an InvalidParameterException
// (the only parameter error PutRetentionPolicy models).
//
// Evidence: the CloudWatch Logs Smithy model's `Days` shape — the target of
// PutRetentionPolicyRequest$retentionInDays — is a plain integer whose
// documentation enumerates the accepted values, so the restriction is
// server-side rather than a constraint trait the SDK could enforce. The list
// below is that enumeration, in model order.
// https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_PutRetentionPolicy.html
var validRetentionDays = map[int]bool{
	1: true, 3: true, 5: true, 7: true, 14: true, 30: true, 60: true, 90: true,
	120: true, 150: true, 180: true, 365: true, 400: true, 545: true, 731: true,
	1096: true, 1827: true, 2192: true, 2557: true, 2922: true, 3288: true, 3653: true,
}

// PutRetentionPolicy sets the retention period for the specified log group.
// Delegates to putRetentionPolicyTyped (typed_logic.go) so the JSON and
// CBOR/typed-operation paths share one implementation, including the
// retention value-set validation.
// AWS docs: https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_PutRetentionPolicy.html
func (h *Handler) PutRetentionPolicy(w http.ResponseWriter, r *http.Request) {
	var req putRetentionPolicyRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	if _, aerr := h.putRetentionPolicyTyped(r.Context(), &req); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, struct{}{})
}

// DeleteRetentionPolicy removes a retention policy from the specified log group.
// AWS docs: https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_DeleteRetentionPolicy.html
func (h *Handler) DeleteRetentionPolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LogGroupName string `json:"logGroupName"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	ctx := r.Context()
	g, aerr := h.store.getLogGroup(ctx, req.LogGroupName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	g.RetentionInDays = 0
	if aerr := h.store.putLogGroup(ctx, g); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, struct{}{})
}

// TagLogGroup adds tags to the specified log group.
// Delegates to tagLogGroupTyped (typed_logic.go) — see CreateLogGroup's doc
// comment for why the JSON and CBOR/typed-operation paths share one
// implementation of the tag-validation contract.
// AWS docs: https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_TagLogGroup.html
func (h *Handler) TagLogGroup(w http.ResponseWriter, r *http.Request) {
	var req tagLogGroupRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if _, aerr := h.tagLogGroupTyped(r.Context(), &req); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, struct{}{})
}

// UntagLogGroup removes tags from the specified log group.
// AWS docs: https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_UntagLogGroup.html
func (h *Handler) UntagLogGroup(w http.ResponseWriter, r *http.Request) {
	var req untagLogGroupRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if _, aerr := h.untagLogGroupTyped(r.Context(), &req); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, struct{}{})
}

// ListTagsLogGroup returns the tags for a log group.
// AWS docs: https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_ListTagsLogGroup.html
func (h *Handler) ListTagsLogGroup(w http.ResponseWriter, r *http.Request) {
	var req listTagsLogGroupRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	resp, aerr := h.listTagsLogGroupTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, resp)
}

// TagResource is the modern, ARN-addressed sibling of TagLogGroup (#1195).
// Delegates to tagResourceTyped (typed_logic.go), which resolves the ARN and
// then reuses tagLogGroupTyped so both spellings share one implementation.
// AWS docs: https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_TagResource.html
func (h *Handler) TagResource(w http.ResponseWriter, r *http.Request) {
	var req tagResourceRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if _, aerr := h.tagResourceTyped(r.Context(), &req); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, struct{}{})
}

// UntagResource is the modern, ARN-addressed sibling of UntagLogGroup (#1195).
// AWS docs: https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_UntagResource.html
func (h *Handler) UntagResource(w http.ResponseWriter, r *http.Request) {
	var req untagResourceRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if _, aerr := h.untagResourceTyped(r.Context(), &req); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, struct{}{})
}

// ListTagsForResource is the modern, ARN-addressed sibling of
// ListTagsLogGroup (#1195).
// AWS docs: https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_ListTagsForResource.html
func (h *Handler) ListTagsForResource(w http.ResponseWriter, r *http.Request) {
	var req listTagsForResourceRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	resp, aerr := h.listTagsForResourceTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, resp)
}
