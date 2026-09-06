package logs

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	eventsbus "github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/middleware"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

// logsTagCfg is the CloudWatch Logs dialect of the shared tag validator
// (serviceutil.ValidateTags), used by every operation that accepts a tag map.
//
// Evidence, from the CloudWatch Logs Smithy model: the `Tags` map carries
// @length(min: 1, max: 50); `TagKey` carries @length(min: 1, max: 128) and
// `TagValue` @length(min: 0, max: 256) — so an empty tag VALUE is legal and an
// empty tag KEY is not. Both CreateLogGroup and TagLogGroup model
// InvalidParameterException and neither models TooManyTagsException, so the
// count violation reports InvalidParameterException here too. The reserved
// `aws:` key prefix is not in the model; it comes from the user guide:
// https://docs.aws.amazon.com/AmazonCloudWatch/latest/logs/log-group-tagging.html
// (which also gives the 50-tag limit and the 1–128 key length, and quotes the
// value limit as 255 where the model says 256 — the model wins on the wire).
//
// AWS publishes no message text for any of these, and this service's reference
// policy is docs-only, so the shared validator's wording is used rather than an
// invented AWS-looking string; the exceeded message is the model's own
// TooManyTagsException documentation.
var logsTagCfg = serviceutil.TagValidationConfig{
	ExceededCode:    "InvalidParameterException",
	InvalidCode:     "InvalidParameterException",
	ExceededMessage: "A resource can have no more than 50 tags.",
}

type createLogGroupRequest struct {
	LogGroupName string            `json:"logGroupName" cbor:"logGroupName"`
	Tags         map[string]string `json:"tags,omitempty" cbor:"tags,omitempty"`
}

type describeLogGroupsRequest struct {
	LogGroupNamePrefix string `json:"logGroupNamePrefix,omitempty" cbor:"logGroupNamePrefix,omitempty"`
	Limit              int    `json:"limit,omitempty" cbor:"limit,omitempty"`
	NextToken          string `json:"nextToken,omitempty" cbor:"nextToken,omitempty"`
}

type logGroupResponse struct {
	LogGroupName    string `json:"logGroupName" cbor:"logGroupName"`
	ARN             string `json:"arn" cbor:"arn"`
	CreationTime    int64  `json:"creationTime" cbor:"creationTime"`
	RetentionInDays int    `json:"retentionInDays,omitempty" cbor:"retentionInDays,omitempty"`
}

type describeLogGroupsResponse struct {
	LogGroups []logGroupResponse `json:"logGroups" cbor:"logGroups"`
	NextToken string             `json:"nextToken,omitempty" cbor:"nextToken,omitempty"`
}

type deleteLogGroupRequest struct {
	LogGroupName string `json:"logGroupName" cbor:"logGroupName"`
}

type createLogStreamRequest struct {
	LogGroupName  string `json:"logGroupName" cbor:"logGroupName"`
	LogStreamName string `json:"logStreamName" cbor:"logStreamName"`
}

type describeLogStreamsRequest struct {
	LogGroupName        string `json:"logGroupName" cbor:"logGroupName"`
	LogStreamNamePrefix string `json:"logStreamNamePrefix,omitempty" cbor:"logStreamNamePrefix,omitempty"`
	Limit               int    `json:"limit,omitempty" cbor:"limit,omitempty"`
	NextToken           string `json:"nextToken,omitempty" cbor:"nextToken,omitempty"`
}

type logStreamResponse struct {
	LogStreamName       string `json:"logStreamName" cbor:"logStreamName"`
	ARN                 string `json:"arn" cbor:"arn"`
	CreationTime        int64  `json:"creationTime" cbor:"creationTime"`
	FirstEventTimestamp int64  `json:"firstEventTimestamp,omitempty" cbor:"firstEventTimestamp,omitempty"`
	LastEventTimestamp  int64  `json:"lastEventTimestamp,omitempty" cbor:"lastEventTimestamp,omitempty"`
	LastIngestionTime   int64  `json:"lastIngestionTime,omitempty" cbor:"lastIngestionTime,omitempty"`
	UploadSequenceToken string `json:"uploadSequenceToken,omitempty" cbor:"uploadSequenceToken,omitempty"`
}

type describeLogStreamsResponse struct {
	LogStreams []logStreamResponse `json:"logStreams" cbor:"logStreams"`
	NextToken  string              `json:"nextToken,omitempty" cbor:"nextToken,omitempty"`
}

type deleteLogStreamRequest struct {
	LogGroupName  string `json:"logGroupName" cbor:"logGroupName"`
	LogStreamName string `json:"logStreamName" cbor:"logStreamName"`
}

type putLogEventsRequest struct {
	LogGroupName  string          `json:"logGroupName" cbor:"logGroupName"`
	LogStreamName string          `json:"logStreamName" cbor:"logStreamName"`
	LogEvents     []logEventInput `json:"logEvents" cbor:"logEvents"`
}

type logEventInput struct {
	Timestamp int64  `json:"timestamp" cbor:"timestamp"`
	Message   string `json:"message" cbor:"message"`
}

// rejectedLogEventsInfo reports the events PutLogEvents accepted the call for
// and then discarded. Each field is a pointer because AWS omits an index that
// does not apply, and a client tells "nothing was rejected" from "the batch's
// first event was rejected" by presence rather than by a zero.
//
// The index conventions are AWS's own, and they differ between the fields:
//   - tooOldLogEventEndIndex — "The index of the last log event that is too
//     old. This field is exclusive."
//   - tooNewLogEventStartIndex — "The index of the first log event that is
//     too new. This field is inclusive."
//
// https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_RejectedLogEventsInfo.html
type rejectedLogEventsInfo struct {
	ExpiredLogEventEndIndex  *int `json:"expiredLogEventEndIndex,omitempty" cbor:"expiredLogEventEndIndex,omitempty"`
	TooNewLogEventStartIndex *int `json:"tooNewLogEventStartIndex,omitempty" cbor:"tooNewLogEventStartIndex,omitempty"`
	TooOldLogEventEndIndex   *int `json:"tooOldLogEventEndIndex,omitempty" cbor:"tooOldLogEventEndIndex,omitempty"`
}

// reportsRejection is false for the zero value, which is what keeps the whole
// member off the wire for a batch that lost nothing.
func (r *rejectedLogEventsInfo) reportsRejection() bool {
	return r.ExpiredLogEventEndIndex != nil || r.TooNewLogEventStartIndex != nil || r.TooOldLogEventEndIndex != nil
}

type putLogEventsResponse struct {
	NextSequenceToken     string                 `json:"nextSequenceToken" cbor:"nextSequenceToken"`
	RejectedLogEventsInfo *rejectedLogEventsInfo `json:"rejectedLogEventsInfo,omitempty" cbor:"rejectedLogEventsInfo,omitempty"`
}

type getLogEventsRequest struct {
	LogGroupName  string `json:"logGroupName" cbor:"logGroupName"`
	LogStreamName string `json:"logStreamName" cbor:"logStreamName"`
	StartTime     *int64 `json:"startTime,omitempty" cbor:"startTime,omitempty"`
	EndTime       *int64 `json:"endTime,omitempty" cbor:"endTime,omitempty"`
	Limit         int    `json:"limit,omitempty" cbor:"limit,omitempty"`
	NextToken     string `json:"nextToken,omitempty" cbor:"nextToken,omitempty"`
	StartFromHead *bool  `json:"startFromHead,omitempty" cbor:"startFromHead,omitempty"`
}

type logEventResponse struct {
	Timestamp     int64  `json:"timestamp" cbor:"timestamp"`
	Message       string `json:"message" cbor:"message"`
	IngestionTime int64  `json:"ingestionTime" cbor:"ingestionTime"`
}

type getLogEventsResponse struct {
	Events            []logEventResponse `json:"events" cbor:"events"`
	NextForwardToken  string             `json:"nextForwardToken" cbor:"nextForwardToken"`
	NextBackwardToken string             `json:"nextBackwardToken" cbor:"nextBackwardToken"`
}

type filterLogEventsRequest struct {
	LogGroupName        string   `json:"logGroupName" cbor:"logGroupName"`
	FilterPattern       string   `json:"filterPattern,omitempty" cbor:"filterPattern,omitempty"`
	StartTime           *int64   `json:"startTime,omitempty" cbor:"startTime,omitempty"`
	EndTime             *int64   `json:"endTime,omitempty" cbor:"endTime,omitempty"`
	LogStreamNames      []string `json:"logStreamNames,omitempty" cbor:"logStreamNames,omitempty"`
	LogStreamNamePrefix string   `json:"logStreamNamePrefix,omitempty" cbor:"logStreamNamePrefix,omitempty"`
	Limit               int      `json:"limit,omitempty" cbor:"limit,omitempty"`
	NextToken           string   `json:"nextToken,omitempty" cbor:"nextToken,omitempty"`
}

// filteredEventResponse is AWS's FilteredLogEvent. It carries an eventId
// where GetLogEvents' OutputLogEvent does not — the AWS model gives that shape
// only timestamp, message and ingestionTime, so GetLogEvents is left alone
// here (#1721). See record_pointer.go for what the id is and why.
type filteredEventResponse struct {
	EventID       string `json:"eventId,omitempty" cbor:"eventId,omitempty"`
	Timestamp     int64  `json:"timestamp" cbor:"timestamp"`
	Message       string `json:"message" cbor:"message"`
	IngestionTime int64  `json:"ingestionTime" cbor:"ingestionTime"`
	LogStreamName string `json:"logStreamName" cbor:"logStreamName"`
}

type searchedLogStreamResponse struct {
	LogStreamName      string `json:"logStreamName" cbor:"logStreamName"`
	SearchedCompletely bool   `json:"searchedCompletely" cbor:"searchedCompletely"`
}

type filterLogEventsResponse struct {
	Events             []filteredEventResponse     `json:"events" cbor:"events"`
	SearchedLogStreams []searchedLogStreamResponse `json:"searchedLogStreams" cbor:"searchedLogStreams"`
	NextToken          string                      `json:"nextToken,omitempty" cbor:"nextToken,omitempty"`
}

type putRetentionPolicyRequest struct {
	LogGroupName    string `json:"logGroupName" cbor:"logGroupName"`
	RetentionInDays int    `json:"retentionInDays" cbor:"retentionInDays"`
}

type deleteRetentionPolicyRequest struct {
	LogGroupName string `json:"logGroupName" cbor:"logGroupName"`
}

type tagLogGroupRequest struct {
	LogGroupName string            `json:"logGroupName" cbor:"logGroupName"`
	Tags         map[string]string `json:"tags" cbor:"tags"`
}

type untagLogGroupRequest struct {
	LogGroupName string   `json:"logGroupName" cbor:"logGroupName"`
	Tags         []string `json:"tags" cbor:"tags"`
}

type listTagsLogGroupRequest struct {
	LogGroupName string `json:"logGroupName" cbor:"logGroupName"`
}

type listTagsLogGroupResponse struct {
	Tags map[string]string `json:"tags" cbor:"tags"`
}

// createLogGroupTyped creates a log group, applying any create-time `tags` in
// the same store write as the group itself — on AWS a rejected CreateLogGroup
// leaves nothing behind, so the tags cannot be a follow-up mutation that could
// fail on its own. Tag validation runs before the duplicate-name check, since
// request-shape validation precedes resource resolution.
func (h *Handler) createLogGroupTyped(ctx context.Context, req *createLogGroupRequest) (*struct{}, *protocol.AWSError) {
	if req.LogGroupName == "" {
		return nil, errInvalidParameter("logGroupName is required")
	}
	if aerr := serviceutil.ValidateTags(logsTagCfg, req.Tags); aerr != nil {
		return nil, aerr
	}
	if _, aerr := h.store.getLogGroup(ctx, req.LogGroupName); aerr == nil {
		return nil, errGroupAlreadyExists(req.LogGroupName)
	}
	g := &LogGroup{
		Name:         req.LogGroupName,
		ARN:          protocol.LogGroupARN(middleware.RegionFromContext(ctx, h.cfg.Region), h.cfg.AccountID, req.LogGroupName),
		CreationTime: h.clk.Now().UnixMilli(),
	}
	if len(req.Tags) > 0 {
		g.Tags = make(map[string]string, len(req.Tags))
		for k, v := range req.Tags {
			g.Tags[k] = v
		}
	}
	if aerr := h.store.putLogGroup(ctx, g); aerr != nil {
		return nil, aerr
	}
	if h.bus != nil {
		h.bus.Publish(ctx, eventsbus.Event{
			Type:    eventsbus.LogGroupCreated,
			Time:    h.clk.Now(),
			Source:  "logs",
			Payload: eventsbus.ResourcePayload{Name: req.LogGroupName},
		})
	}
	return &struct{}{}, nil
}

// DescribeLogGroups / DescribeLogStreams limit — AWS documents both with the
// same wording: "The maximum number of items returned. If you don't specify a
// value, the default is up to 50 items", "Valid Range: Minimum value of 1.
// Maximum value of 50."
// https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_DescribeLogGroups.html
// https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_DescribeLogStreams.html
const (
	describeDefaultLimit = 50
	describeMaxLimit     = 50
)

// describeLogGroupsTyped honours limit + nextToken (#1721). Both Describes
// materialise their whole, already name-sorted list before paging, so
// serviceutil.Paginate — this codebase's index-cursor pagination helper — is
// the right tool for them. The bespoke (Timestamp, Seq) tokens in tokens.go
// exist only because the *event* operations resume inside an indexed range
// read, which a Describe does not; nothing here needs a second token codec.
func (h *Handler) describeLogGroupsTyped(ctx context.Context, req *describeLogGroupsRequest) (*describeLogGroupsResponse, *protocol.AWSError) {
	groups, aerr := h.store.listLogGroups(ctx, req.LogGroupNamePrefix)
	if aerr != nil {
		return nil, aerr
	}
	page, err := serviceutil.Paginate(groups, req.Limit, req.NextToken, serviceutil.PaginateOptions{
		DefaultLimit: describeDefaultLimit,
		MaxLimit:     describeMaxLimit,
	})
	if err != nil {
		return nil, errInvalidNextToken()
	}
	out := make([]logGroupResponse, 0, len(page.Items))
	for _, g := range page.Items {
		out = append(out, logGroupResponse{
			LogGroupName:    g.Name,
			ARN:             g.ARN,
			CreationTime:    g.CreationTime,
			RetentionInDays: g.RetentionInDays,
		})
	}
	return &describeLogGroupsResponse{LogGroups: out, NextToken: page.NextToken}, nil
}

func (h *Handler) deleteLogGroupTyped(ctx context.Context, req *deleteLogGroupRequest) (*struct{}, *protocol.AWSError) {
	if req.LogGroupName == "" {
		return nil, errInvalidParameter("logGroupName is required")
	}
	if _, aerr := h.store.getLogGroup(ctx, req.LogGroupName); aerr != nil {
		return nil, aerr
	}
	if aerr := h.store.deleteLogGroup(ctx, req.LogGroupName); aerr != nil {
		return nil, aerr
	}
	return &struct{}{}, nil
}

func (h *Handler) createLogStreamTyped(ctx context.Context, req *createLogStreamRequest) (*struct{}, *protocol.AWSError) {
	if req.LogGroupName == "" || req.LogStreamName == "" {
		return nil, errInvalidParameter("logGroupName and logStreamName are required")
	}
	if _, aerr := h.store.getLogGroup(ctx, req.LogGroupName); aerr != nil {
		return nil, aerr
	}
	if _, aerr := h.store.getLogStream(ctx, req.LogGroupName, req.LogStreamName); aerr == nil {
		return nil, errStreamAlreadyExists(req.LogStreamName)
	}
	ls := &LogStream{
		Name:                req.LogStreamName,
		ARN:                 protocol.LogStreamARN(h.cfg.Region, h.cfg.AccountID, req.LogGroupName, req.LogStreamName),
		CreationTime:        h.clk.Now().UnixMilli(),
		UploadSequenceToken: "1",
	}
	if aerr := h.store.putLogStream(ctx, req.LogGroupName, ls); aerr != nil {
		return nil, aerr
	}
	if h.bus != nil {
		h.bus.Publish(ctx, eventsbus.Event{
			Type:    eventsbus.LogStreamCreated,
			Time:    h.clk.Now(),
			Source:  "logs",
			Payload: eventsbus.ResourcePayload{Name: req.LogGroupName + "/" + req.LogStreamName},
		})
	}
	return &struct{}{}, nil
}

func (h *Handler) describeLogStreamsTyped(ctx context.Context, req *describeLogStreamsRequest) (*describeLogStreamsResponse, *protocol.AWSError) {
	if req.LogGroupName == "" {
		return nil, errInvalidParameter("logGroupName is required")
	}
	if _, aerr := h.store.getLogGroup(ctx, req.LogGroupName); aerr != nil {
		return nil, aerr
	}
	streams, aerr := h.store.listLogStreams(ctx, req.LogGroupName, req.LogStreamNamePrefix)
	if aerr != nil {
		return nil, aerr
	}
	page, err := serviceutil.Paginate(streams, req.Limit, req.NextToken, serviceutil.PaginateOptions{
		DefaultLimit: describeDefaultLimit,
		MaxLimit:     describeMaxLimit,
	})
	if err != nil {
		return nil, errInvalidNextToken()
	}
	out := make([]logStreamResponse, 0, len(page.Items))
	for _, s := range page.Items {
		out = append(out, logStreamResponse{
			LogStreamName:       s.Name,
			ARN:                 s.ARN,
			CreationTime:        s.CreationTime,
			FirstEventTimestamp: s.FirstEventTimestamp,
			LastEventTimestamp:  s.LastEventTimestamp,
			LastIngestionTime:   s.LastIngestionTime,
			UploadSequenceToken: s.UploadSequenceToken,
		})
	}
	return &describeLogStreamsResponse{LogStreams: out, NextToken: page.NextToken}, nil
}

func (h *Handler) deleteLogStreamTyped(ctx context.Context, req *deleteLogStreamRequest) (*struct{}, *protocol.AWSError) {
	if req.LogGroupName == "" || req.LogStreamName == "" {
		return nil, errInvalidParameter("logGroupName and logStreamName are required")
	}
	if _, aerr := h.store.getLogGroup(ctx, req.LogGroupName); aerr != nil {
		return nil, aerr
	}
	if _, aerr := h.store.getLogStream(ctx, req.LogGroupName, req.LogStreamName); aerr != nil {
		return nil, aerr
	}
	if aerr := h.store.deleteLogStream(ctx, req.LogGroupName, req.LogStreamName); aerr != nil {
		return nil, aerr
	}
	return &struct{}{}, nil
}

// PutLogEvents' ingestion window. Both boundaries are AWS's own documented
// values rather than numbers inferred from a probe:
//
//   - "Events more than 2 hours in the future are rejected while processing
//     remaining valid events."
//   - "Events older than 14 days or preceding the log group's retention
//     period are rejected while processing remaining valid events."
//
// https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_PutLogEvents.html
//
// "Rejected while processing remaining valid events" is the whole shape of the
// contract, and it is easy to get backwards: an out-of-window event does NOT
// fail the call. AWS answers 200, silently discards those events, and says so
// only through rejectedLogEventsInfo — which is therefore the only signal a
// client ever gets that data was lost (#1721). An out-of-ORDER batch is the
// opposite: the whole batch fails with InvalidParameterException.
const (
	putLogEventsFutureWindow = 2 * time.Hour
	putLogEventsPastWindow   = 14 * 24 * time.Hour
)

// logEventVerdict is why one event in a batch was discarded, or verdictAccept
// when it was kept.
type logEventVerdict int

const (
	verdictAccept logEventVerdict = iota
	verdictTooOld
	verdictExpired
	verdictTooNew
)

// classifyLogEvent applies the ingestion window above to one event's
// timestamp. retentionInDays is the log group's policy, 0 when it has none.
//
// The 14-day rule and the retention-derived rule are two separate AWS report
// fields rather than one boundary: an event outside the absolute 14-day window
// is "too old", one merely older than this group's own retention is "expired".
// Where a retention shorter than 14 days makes both true the absolute rule
// wins, so a group with no retention policy and a group with a long one
// classify a very old event identically.
func classifyLogEvent(timestamp int64, now time.Time, retentionInDays int) logEventVerdict {
	switch {
	case timestamp > now.Add(putLogEventsFutureWindow).UnixMilli():
		return verdictTooNew
	case timestamp < now.Add(-putLogEventsPastWindow).UnixMilli():
		return verdictTooOld
	case retentionInDays > 0 && timestamp < now.Add(-time.Duration(retentionInDays)*24*time.Hour).UnixMilli():
		return verdictExpired
	default:
		return verdictAccept
	}
}

// putLogEventsTyped ingests a batch, enforcing AWS's two very different
// failure modes: an unordered batch is refused whole, while an out-of-window
// event is discarded and reported in rejectedLogEventsInfo behind a 200. Only
// the events that survive classification are stored, since that report is the
// only evidence of the loss a client has to work from.
func (h *Handler) putLogEventsTyped(ctx context.Context, req *putLogEventsRequest) (*putLogEventsResponse, *protocol.AWSError) {
	if req.LogGroupName == "" || req.LogStreamName == "" {
		return nil, errInvalidParameter("logGroupName and logStreamName are required")
	}
	if len(req.LogEvents) == 0 {
		return nil, errInvalidParameter("logEvents must not be empty")
	}
	// "The log events in the batch must be in chronological order by their
	// timestamp." Checked before the group is resolved: it is a request-shape
	// constraint, so the error must not depend on the resources existing —
	// the same ordering this package's other validators use.
	for i := 1; i < len(req.LogEvents); i++ {
		if req.LogEvents[i].Timestamp < req.LogEvents[i-1].Timestamp {
			return nil, errInvalidParameter("The log events in the batch must be in chronological order by their timestamp.")
		}
	}
	g, aerr := h.store.getLogGroup(ctx, req.LogGroupName)
	if aerr != nil {
		return nil, aerr
	}
	ls, aerr := h.store.getLogStream(ctx, req.LogGroupName, req.LogStreamName)
	if aerr != nil {
		return nil, aerr
	}

	clockNow := h.clk.Now()
	now := clockNow.UnixMilli()
	rejected := &rejectedLogEventsInfo{}
	events := make([]LogEvent, 0, len(req.LogEvents))
	for i, e := range req.LogEvents {
		switch classifyLogEvent(e.Timestamp, clockNow, g.RetentionInDays) {
		case verdictTooOld:
			// Exclusive end index. The batch is ascending, so the too-old
			// events are a prefix and the last one seen carries the field.
			rejected.TooOldLogEventEndIndex = ptrTo(i + 1)
		case verdictExpired:
			rejected.ExpiredLogEventEndIndex = ptrTo(i + 1)
		case verdictTooNew:
			// Inclusive start index, and the too-new events are a suffix, so
			// only the first one sets it.
			if rejected.TooNewLogEventStartIndex == nil {
				rejected.TooNewLogEventStartIndex = ptrTo(i)
			}
		case verdictAccept:
			events = append(events, LogEvent{Timestamp: e.Timestamp, Message: e.Message, IngestionTime: now})
		}
	}

	resp := &putLogEventsResponse{NextSequenceToken: ls.UploadSequenceToken}
	if rejected.reportsRejection() {
		resp.RejectedLogEventsInfo = rejected
	}
	if len(events) == 0 {
		// Nothing survived: no write, and the stream's own metadata (its
		// event timestamps and sequence token) must not move for a batch that
		// contributed no events.
		return resp, nil
	}

	// events is ascending because the request was validated as ascending and
	// filtering preserves order — the invariant logsStore.appendEvents' write
	// buffer requires of its input.
	if aerr := h.store.appendEvents(ctx, req.LogGroupName, req.LogStreamName, events); aerr != nil {
		return nil, aerr
	}
	firstTs := events[0].Timestamp
	lastTs := events[len(events)-1].Timestamp
	if ls.FirstEventTimestamp == 0 || firstTs < ls.FirstEventTimestamp {
		ls.FirstEventTimestamp = firstTs
	}
	if lastTs > ls.LastEventTimestamp {
		ls.LastEventTimestamp = lastTs
	}
	ls.LastIngestionTime = now
	seq, _ := strconv.Atoi(ls.UploadSequenceToken)
	ls.UploadSequenceToken = fmt.Sprintf("%d", seq+1)
	if aerr := h.store.putLogStream(ctx, req.LogGroupName, ls); aerr != nil {
		return nil, aerr
	}
	if h.bus != nil {
		items := make([]eventsbus.LogEventItem, 0, len(events))
		for _, e := range events {
			items = append(items, eventsbus.LogEventItem{Timestamp: e.Timestamp, Message: e.Message})
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
	resp.NextSequenceToken = ls.UploadSequenceToken
	return resp, nil
}

// ptrTo is the "address of a literal" the optional-integer members in
// rejectedLogEventsInfo need — presence is meaningful there, so they cannot be
// plain ints with omitempty.
func ptrTo[T any](v T) *T { return &v }

// GetLogEvents Limit — AWS docs: "Minimum value of 1. Maximum value of
// 10000." The default when the client omits Limit is documented as "as many
// log events as can fit in a response size of 1 MB, up to 10,000 log
// events"; this emulator doesn't track response byte size, so it uses the
// 10,000-event cap as the default too.
// https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_GetLogEvents.html
const (
	getLogEventsDefaultLimit = 10000
	getLogEventsMaxLimit     = 10000
)

// getLogEventsTyped implements GetLogEvents' full pagination contract
// (pagination-plan.md G1) against the range+limit pushdown backend
// (storage-access-plan.md A4):
//
//   - Limit is honored (default/cap 10,000, above).
//   - StartFromHead selects direction on a fresh call (default false —
//     "the latest log events are returned first"); once a nextToken is
//     supplied, the token's own "f/"/"b/" prefix determines direction and
//     StartFromHead is ignored, matching real GetLogEvents' documented
//     token-driven paging.
//   - nextForwardToken/nextBackwardToken always encode a real (Timestamp,
//     Seq) resume position (tokens.go) — never the old synthesized
//     f/<count>/b/0 placeholders.
//   - Same-token-when-exhausted: if the client polls with a token and finds
//     nothing new in that direction, this operation echoes the same token
//     back rather than a fresh one — the standard CloudWatch Logs tail-loop
//     termination signal SDK paginators rely on (pagination-plan.md's
//     accept criterion for G1).
func (h *Handler) getLogEventsTyped(ctx context.Context, req *getLogEventsRequest) (*getLogEventsResponse, *protocol.AWSError) {
	if req.LogGroupName == "" || req.LogStreamName == "" {
		return nil, errInvalidParameter("logGroupName and logStreamName are required")
	}
	if _, aerr := h.store.getLogGroup(ctx, req.LogGroupName); aerr != nil {
		return nil, aerr
	}
	if _, aerr := h.store.getLogStream(ctx, req.LogGroupName, req.LogStreamName); aerr != nil {
		return nil, aerr
	}

	limit := req.Limit
	if limit <= 0 {
		limit = getLogEventsDefaultLimit
	}
	if limit > getLogEventsMaxLimit {
		limit = getLogEventsMaxLimit
	}

	// startTime is inclusive; endTime is EXCLUSIVE for GetLogEvents per AWS
	// docs ("events with a timestamp equal to or later than this time are
	// not exported") — distinct from FilterLogEvents' inclusive endTime,
	// see filterLogEventsTyped below. getEventsRange's own window contract
	// is inclusive-both-ends (matching the A5 metrics-range precedent), so
	// the exclusive upper bound is translated to inclusive right here.
	startTs := int64(math.MinInt64)
	if req.StartTime != nil {
		startTs = *req.StartTime
	}
	endTs := int64(math.MaxInt64)
	if req.EndTime != nil {
		endTs = *req.EndTime - 1
	}

	forward := req.StartFromHead != nil && *req.StartFromHead
	var cursor eventCursor
	haveToken := req.NextToken != ""
	if haveToken {
		f, c, ok := decodeLogEventsToken(req.NextToken)
		if !ok {
			return nil, errInvalidNextToken()
		}
		forward = f
		cursor = c
	}

	events, aerr := h.store.getEventsRangeMerged(ctx, req.LogGroupName, req.LogStreamName, startTs, endTs, cursor, limit, forward)
	if aerr != nil {
		return nil, aerr
	}

	out := make([]logEventResponse, 0, len(events))
	for _, e := range events {
		out = append(out, logEventResponse{Timestamp: e.Timestamp, Message: e.Message, IngestionTime: e.IngestionTime})
	}

	var fwdToken, bwdToken string
	switch {
	case len(events) == 0 && haveToken:
		// Same-token-when-exhausted: the direction the client was polling
		// found nothing new — echo its own token back unchanged. The other
		// direction's token still reflects the (unchanged) cursor position
		// so flipping direction from here remains well-defined.
		if forward {
			fwdToken = req.NextToken
			bwdToken = encodeLogEventsToken(false, cursor.Timestamp, cursor.Seq)
		} else {
			bwdToken = req.NextToken
			fwdToken = encodeLogEventsToken(true, cursor.Timestamp, cursor.Seq)
		}
	case len(events) == 0:
		// Fresh call (no input token), nothing in the window at all —
		// anchor both tokens on the window's own edges (seq sentinels
		// chosen so a real event later landing exactly at that edge is
		// still included once ingested) so a subsequent call resumes from
		// the right place instead of a meaningless placeholder.
		fwdToken = encodeLogEventsToken(true, startTs, -1)
		bwdToken = encodeLogEventsToken(false, endTs, math.MaxInt64)
	default:
		first, last := events[0], events[len(events)-1]
		fwdToken = encodeLogEventsToken(true, last.Timestamp, last.Seq)
		bwdToken = encodeLogEventsToken(false, first.Timestamp, first.Seq)
	}

	return &getLogEventsResponse{
		Events:            out,
		NextForwardToken:  fwdToken,
		NextBackwardToken: bwdToken,
	}, nil
}

// FilterLogEvents Limit — AWS docs: "The maximum number of events to
// return. ... Valid Range: Minimum value of 1. Maximum value of 10000." The
// default when omitted is documented as "as many events as can fit in a
// response size of 1MB, up to 10,000" — approximated here (no response byte
// tracking) as the 10,000-event cap.
// https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_FilterLogEvents.html
const (
	filterLogEventsDefaultLimit = 10000
	filterLogEventsMaxLimit     = 10000

	// filterLogEventsRawBatchSize is how many raw (pre-filter-pattern)
	// events are pulled from storage per internal group-range round trip
	// while hunting for `limit` matches — large enough that a typical
	// filter (matching a meaningful fraction of events) fills a page in one
	// or two round trips, small enough to keep any single backend call
	// bounded.
	filterLogEventsRawBatchSize = 1000

	// filterLogEventsScanBudget bounds how many RAW events one
	// FilterLogEvents call reads before returning a resumable nextToken —
	// this emulator's event-count analogue of AWS's documented ~1MB-per-call
	// read budget (no response byte tracking here). Without this, a narrow
	// filter pattern over a huge time window would turn one API call into
	// an unbounded server-side scan; with it, the call returns whatever
	// matches it found within budget plus a nextToken, matching AWS's own
	// documented "doesn't guarantee exactly limit matching events per call"
	// behavior.
	filterLogEventsScanBudget = 50000
)

// filterLogEventsTyped implements FilterLogEvents' limit + nextToken
// contract (pagination-plan.md G6) on top of storage-access-plan.md A4's
// group-range pushdown: ONE group-wide range query (getGroupEventsRangeMerged,
// looped internally only to accumulate enough MATCHED events or exhaust the
// scan budget) replaces the old per-stream full-history reads. Filter
// pattern matching, stream-name-set selection, interleaving, and
// searchedLogStreams shaping stay here (behavioral, per the fidelity
// principle) — only the structural time-window + limit predicate is pushed
// down.
func (h *Handler) filterLogEventsTyped(ctx context.Context, req *filterLogEventsRequest) (*filterLogEventsResponse, *protocol.AWSError) {
	if req.LogGroupName == "" {
		return nil, errInvalidParameter("logGroupName is required")
	}
	matcher, err := CompileFilter(req.FilterPattern)
	if err != nil {
		return nil, errInvalidParameter(err.Error())
	}
	if _, aerr := h.store.getLogGroup(ctx, req.LogGroupName); aerr != nil {
		return nil, aerr
	}

	// Resolve the candidate stream set exactly as before (behavioral,
	// unchanged): explicit LogStreamNames wins, else every stream matching
	// LogStreamNamePrefix.
	var streams []*LogStream
	explicitNames := len(req.LogStreamNames) > 0
	if explicitNames {
		for _, name := range req.LogStreamNames {
			ls, aerr := h.store.getLogStream(ctx, req.LogGroupName, name)
			if aerr != nil {
				continue // skip missing streams (AWS behavior)
			}
			streams = append(streams, ls)
		}
	} else {
		var aerr *protocol.AWSError
		streams, aerr = h.store.listLogStreams(ctx, req.LogGroupName, req.LogStreamNamePrefix)
		if aerr != nil {
			return nil, aerr
		}
	}

	limit := req.Limit
	if limit <= 0 {
		limit = filterLogEventsDefaultLimit
	}
	if limit > filterLogEventsMaxLimit {
		limit = filterLogEventsMaxLimit
	}

	startTs := int64(math.MinInt64)
	if req.StartTime != nil {
		startTs = *req.StartTime
	}
	// endTime is INCLUSIVE for FilterLogEvents (unlike GetLogEvents) —
	// pinned by the existing TestFilterLogEvents_timeRange integration test
	// and preserved unchanged by this range-pushdown rewrite.
	endTs := int64(math.MaxInt64)
	if req.EndTime != nil {
		endTs = *req.EndTime
	}

	var cursor groupCursor
	if req.NextToken != "" {
		c, ok := decodeFilterEventsToken(req.NextToken)
		if !ok {
			return nil, errInvalidNextToken()
		}
		cursor = c
	}

	// A stream whose own [FirstEventTimestamp, LastEventTimestamp] provably
	// can't overlap the query window is fully searched without ever being
	// queried (existing behavior, preserved); every other candidate is
	// "relevant" — its events come back via the group-range query below —
	// and, since that query already covers the whole window in one pass
	// (not a partial per-stream cursor), it too is fully searched by the
	// time this call returns.
	searched := make([]searchedLogStreamResponse, 0, len(streams))
	relevant := make(map[string]bool, len(streams))
	relevantNames := make([]string, 0, len(streams))
	for _, ls := range streams {
		if req.StartTime != nil && ls.LastEventTimestamp > 0 && ls.LastEventTimestamp < *req.StartTime {
			searched = append(searched, searchedLogStreamResponse{LogStreamName: ls.Name, SearchedCompletely: true})
			continue
		}
		if req.EndTime != nil && ls.FirstEventTimestamp > 0 && ls.FirstEventTimestamp > *req.EndTime {
			searched = append(searched, searchedLogStreamResponse{LogStreamName: ls.Name, SearchedCompletely: true})
			continue
		}
		relevant[ls.Name] = true
		relevantNames = append(relevantNames, ls.Name)
		searched = append(searched, searchedLogStreamResponse{LogStreamName: ls.Name, SearchedCompletely: true})
	}

	// An explicit LogStreamNames set of two or more isn't expressible as a
	// single SQL prefix, so that case queries the whole group (streamPrefix
	// "") and filters to the requested names in Go below — less efficient
	// than the prefix/no-filter case when the group has many unrelated
	// streams, but still bounded by the time window (never a full-history
	// read) and still correct. A SINGLE explicit name — the shape the web
	// UI's single-stream viewer issues on every window chunk — IS pushed
	// down, as its own prefix: any prefix-collision streams the bound also
	// matches (name "app" also matching "app-2") are dropped by the
	// `relevant` check below, exactly as before, so this narrows the raw
	// scan set without changing what the call returns.
	// LogStreamNamePrefix, the other common filter shape, pushes all the
	// way down as before (see sqlEventBackend.getGroupEventsRange).
	streamPrefix := ""
	switch {
	case !explicitNames:
		streamPrefix = req.LogStreamNamePrefix
	case len(req.LogStreamNames) == 1:
		streamPrefix = req.LogStreamNames[0]
	}

	var matched []filteredEventResponse
	var lastScanned GroupRangedEvent
	haveLastScanned := false
	rawScanned := 0
	exhausted := false

scanLoop:
	for rawScanned < filterLogEventsScanBudget {
		batch, aerr := h.store.getGroupEventsRangeMerged(ctx, req.LogGroupName, streamPrefix, relevantNames, startTs, endTs, cursor, filterLogEventsRawBatchSize)
		if aerr != nil {
			return nil, aerr
		}
		if len(batch) == 0 {
			exhausted = true
			break
		}
		for _, e := range batch {
			rawScanned++
			lastScanned = e
			haveLastScanned = true
			if explicitNames && !relevant[e.StreamName] {
				continue
			}
			if !matcher(e.Message) {
				continue
			}
			matched = append(matched, filteredEventResponse{
				EventID:       encodeEventID(req.LogGroupName, e.StreamName, e.Timestamp, e.Message),
				Timestamp:     e.Timestamp,
				Message:       e.Message,
				IngestionTime: e.IngestionTime,
				LogStreamName: e.StreamName,
			})
			if len(matched) >= limit {
				break scanLoop
			}
		}
		cursor = groupCursor{Valid: true, Timestamp: lastScanned.Timestamp, StreamName: lastScanned.StreamName, Seq: lastScanned.Seq}
		if len(batch) < filterLogEventsRawBatchSize {
			exhausted = true
			break
		}
	}

	var nextToken string
	if haveLastScanned && !exhausted {
		nextToken = encodeFilterEventsToken(lastScanned.Timestamp, lastScanned.StreamName, lastScanned.Seq)
	}

	return &filterLogEventsResponse{Events: matched, SearchedLogStreams: searched, NextToken: nextToken}, nil
}

type getLogRecordRequest struct {
	LogRecordPointer string `json:"logRecordPointer" cbor:"logRecordPointer"`
	Unmask           bool   `json:"unmask,omitempty" cbor:"unmask,omitempty"`
}

type getLogRecordResponse struct {
	LogRecord map[string]string `json:"logRecord" cbor:"logRecord"`
}

// getLogRecordTyped resolves an eventId minted by FilterLogEvents back to the
// single event it names, which is what makes that field worth returning
// (#1721). `unmask` is accepted and ignored: it selects whether data-protection
// masking is lifted, and Overcast implements no data protection policies, so
// every field is already unmasked.
//
// The record's field names are Logs Insights' own — `@message` holds the full
// unparsed event, per GetLogRecord's documentation — and its two times are
// rendered as epoch milliseconds in a decimal string, matching both the
// numeric string in AWS's own GetLogRecord response example and the
// millisecond timestamps every other operation in this service returns.
// AWS additionally splits a structured message into one entry per parsed
// field; nothing here does that, because no query has selected a field set.
// https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_GetLogRecord.html
func (h *Handler) getLogRecordTyped(ctx context.Context, req *getLogRecordRequest) (*getLogRecordResponse, *protocol.AWSError) {
	if req.LogRecordPointer == "" {
		return nil, errInvalidParameter("logRecordPointer is required")
	}
	ptr, ok := decodeEventID(req.LogRecordPointer)
	if !ok {
		return nil, errInvalidParameter("The specified logRecordPointer is invalid.")
	}
	if _, aerr := h.store.getLogGroup(ctx, ptr.Group); aerr != nil {
		return nil, aerr
	}
	if _, aerr := h.store.getLogStream(ctx, ptr.Group, ptr.Stream); aerr != nil {
		return nil, aerr
	}
	// One millisecond of one stream. The window is a point, so the read is
	// bounded by however many events share that millisecond; GetLogEvents'
	// own page cap is reused as the ceiling rather than inventing a second
	// number for the same "one response worth of events" idea.
	events, aerr := h.store.getEventsRangeMerged(ctx, ptr.Group, ptr.Stream, ptr.Timestamp, ptr.Timestamp, eventCursor{}, getLogEventsMaxLimit, true)
	if aerr != nil {
		return nil, aerr
	}
	for _, e := range events {
		if messageDigest(e.Message) != ptr.MessageSum {
			continue
		}
		return &getLogRecordResponse{LogRecord: map[string]string{
			"@ptr":           req.LogRecordPointer,
			"@timestamp":     strconv.FormatInt(e.Timestamp, 10),
			"@ingestionTime": strconv.FormatInt(e.IngestionTime, 10),
			"@message":       e.Message,
			"@log":           h.cfg.AccountID + ":" + ptr.Group,
			"@logStream":     ptr.Stream,
		}}, nil
	}
	return nil, errLogRecordNotFound()
}

// putRetentionPolicyTyped validates retentionInDays against AWS's fixed value
// set (validRetentionDays, handler.go) BEFORE looking the log group up, so a
// rejected request can never mutate an existing retention policy. Request
// validation ahead of resource resolution matches how AWS reports the two
// errors, and matches this package's other validators.
func (h *Handler) putRetentionPolicyTyped(ctx context.Context, req *putRetentionPolicyRequest) (*struct{}, *protocol.AWSError) {
	if !validRetentionDays[req.RetentionInDays] {
		return nil, errInvalidParameter(
			"Invalid retention value. Valid values are: [1, 3, 5, 7, 14, 30, 60, 90, 120, 150, 180, 365, 400, 545, 731, 1096, 1827, 2192, 2557, 2922, 3288, 3653]")
	}
	g, aerr := h.store.getLogGroup(ctx, req.LogGroupName)
	if aerr != nil {
		return nil, aerr
	}
	g.RetentionInDays = req.RetentionInDays
	if aerr := h.store.putLogGroup(ctx, g); aerr != nil {
		return nil, aerr
	}
	return &struct{}{}, nil
}

func (h *Handler) deleteRetentionPolicyTyped(ctx context.Context, req *deleteRetentionPolicyRequest) (*struct{}, *protocol.AWSError) {
	g, aerr := h.store.getLogGroup(ctx, req.LogGroupName)
	if aerr != nil {
		return nil, aerr
	}
	g.RetentionInDays = 0
	if aerr := h.store.putLogGroup(ctx, g); aerr != nil {
		return nil, aerr
	}
	return &struct{}{}, nil
}

// tagLogGroupTyped merges tags into a log group. The incoming map is validated
// before the group is resolved — it is a request-shape constraint, so the
// error must not depend on whether the group happens to exist — and the merged
// result is validated before it is written, which is what enforces the
// per-resource tag limit across repeated calls. Either rejection leaves the
// existing tag set exactly as it was.
func (h *Handler) tagLogGroupTyped(ctx context.Context, req *tagLogGroupRequest) (*struct{}, *protocol.AWSError) {
	if req.LogGroupName == "" {
		return nil, errInvalidParameter("logGroupName is required")
	}
	if aerr := serviceutil.ValidateTags(logsTagCfg, req.Tags); aerr != nil {
		return nil, aerr
	}
	g, aerr := h.store.getLogGroup(ctx, req.LogGroupName)
	if aerr != nil {
		return nil, aerr
	}
	merged := make(map[string]string, len(g.Tags)+len(req.Tags))
	for k, v := range g.Tags {
		merged[k] = v
	}
	for k, v := range req.Tags {
		merged[k] = v
	}
	if aerr := serviceutil.ValidateTags(logsTagCfg, merged); aerr != nil {
		return nil, aerr
	}
	g.Tags = merged
	if aerr := h.store.putLogGroup(ctx, g); aerr != nil {
		return nil, aerr
	}
	return &struct{}{}, nil
}

func (h *Handler) untagLogGroupTyped(ctx context.Context, req *untagLogGroupRequest) (*struct{}, *protocol.AWSError) {
	if req.LogGroupName == "" {
		return nil, errInvalidParameter("logGroupName is required")
	}
	g, aerr := h.store.getLogGroup(ctx, req.LogGroupName)
	if aerr != nil {
		return nil, aerr
	}
	for _, k := range req.Tags {
		delete(g.Tags, k)
	}
	if aerr := h.store.putLogGroup(ctx, g); aerr != nil {
		return nil, aerr
	}
	return &struct{}{}, nil
}

func (h *Handler) listTagsLogGroupTyped(ctx context.Context, req *listTagsLogGroupRequest) (*listTagsLogGroupResponse, *protocol.AWSError) {
	if req.LogGroupName == "" {
		return nil, errInvalidParameter("logGroupName is required")
	}
	g, aerr := h.store.getLogGroup(ctx, req.LogGroupName)
	if aerr != nil {
		return nil, aerr
	}
	tags := g.Tags
	if tags == nil {
		tags = map[string]string{}
	}
	return &listTagsLogGroupResponse{Tags: tags}, nil
}

// ─── TagResource / UntagResource / ListTagsForResource (#1195) ─────────────
//
// These are the modern, ARN-addressed siblings of TagLogGroup / UntagLogGroup
// / ListTagsLogGroup above. AWS added them without deprecating the legacy
// trio, and both spellings tag the same log group, so each modern operation
// resolves its resourceArn to a log group name and delegates straight to the
// legacy typed function — one validate-merge-store implementation shared by
// both spellings, matching this file's own precedent (CreateLogGroup's doc
// comment) for why a tag-validation contract must not be duplicated.
//
// AWS docs: https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_TagResource.html

type tagResourceRequest struct {
	ResourceArn string            `json:"resourceArn" cbor:"resourceArn"`
	Tags        map[string]string `json:"tags" cbor:"tags"`
}

type untagResourceRequest struct {
	ResourceArn string   `json:"resourceArn" cbor:"resourceArn"`
	TagKeys     []string `json:"tagKeys" cbor:"tagKeys"`
}

type listTagsForResourceRequest struct {
	ResourceArn string `json:"resourceArn" cbor:"resourceArn"`
}

type listTagsForResourceResponse struct {
	Tags map[string]string `json:"tags" cbor:"tags"`
}

// resourceArnToLogGroupName resolves the modern operations' resourceArn
// member to the log group name the legacy operations address directly.
//
// LogGroupName's modeled pattern (`^[.\-_/#A-Za-z0-9]+$`) excludes ':', so
// splitting the ARN on ':' unambiguously separates the fixed
// arn:aws:logs:region:account:log-group: prefix from the name. AWS accepts
// the group ARN with or without its trailing ":*" — this only reads the name
// segment, so both forms resolve identically.
//
// A log group ARN is not the only resourceArn these operations accept on
// real AWS: a destination's ARN (arn:aws:logs:region:account:destination:name)
// is equally valid there. This emulator does not implement CloudWatch Logs
// destinations, so any ARN that does not name a log group is reported
// not-found rather than silently accepted, the same stance taken for
// unimplemented resource types elsewhere in this codebase (see
// docs/plans/resource-tagging-coverage.md's ACM/IAM "out of scope" rows).
func resourceArnToLogGroupName(arn string) (string, *protocol.AWSError) {
	if arn == "" {
		return "", errInvalidParameter("resourceArn is required")
	}
	parts := strings.Split(arn, ":")
	if len(parts) < 7 || parts[0] != "arn" || parts[2] != "logs" || parts[5] != "log-group" || parts[6] == "" {
		return "", errResourceArnNotFound(arn)
	}
	return parts[6], nil
}

func errResourceArnNotFound(arn string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ResourceNotFoundException",
		Message:    fmt.Sprintf("Resource not found: %s", arn),
		HTTPStatus: http.StatusBadRequest,
	}
}

func (h *Handler) tagResourceTyped(ctx context.Context, req *tagResourceRequest) (*struct{}, *protocol.AWSError) {
	name, aerr := resourceArnToLogGroupName(req.ResourceArn)
	if aerr != nil {
		return nil, aerr
	}
	return h.tagLogGroupTyped(ctx, &tagLogGroupRequest{LogGroupName: name, Tags: req.Tags})
}

func (h *Handler) untagResourceTyped(ctx context.Context, req *untagResourceRequest) (*struct{}, *protocol.AWSError) {
	name, aerr := resourceArnToLogGroupName(req.ResourceArn)
	if aerr != nil {
		return nil, aerr
	}
	return h.untagLogGroupTyped(ctx, &untagLogGroupRequest{LogGroupName: name, Tags: req.TagKeys})
}

func (h *Handler) listTagsForResourceTyped(ctx context.Context, req *listTagsForResourceRequest) (*listTagsForResourceResponse, *protocol.AWSError) {
	name, aerr := resourceArnToLogGroupName(req.ResourceArn)
	if aerr != nil {
		return nil, aerr
	}
	resp, aerr := h.listTagsLogGroupTyped(ctx, &listTagsLogGroupRequest{LogGroupName: name})
	if aerr != nil {
		return nil, aerr
	}
	return &listTagsForResourceResponse{Tags: resp.Tags}, nil
}
