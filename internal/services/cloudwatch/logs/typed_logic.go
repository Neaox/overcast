package logs

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"

	eventsbus "github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
)

type createLogGroupRequest struct {
	LogGroupName string `json:"logGroupName" cbor:"logGroupName"`
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

type putLogEventsResponse struct {
	NextSequenceToken string `json:"nextSequenceToken" cbor:"nextSequenceToken"`
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

type filteredEventResponse struct {
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

func (h *Handler) createLogGroupTyped(ctx context.Context, req *createLogGroupRequest) (*struct{}, *protocol.AWSError) {
	if req.LogGroupName == "" {
		return nil, errInvalidParameter("logGroupName is required")
	}
	if _, aerr := h.store.getLogGroup(ctx, req.LogGroupName); aerr == nil {
		return nil, errGroupAlreadyExists(req.LogGroupName)
	}
	g := &LogGroup{
		Name:         req.LogGroupName,
		ARN:          protocol.LogGroupARN(middleware.RegionFromContext(ctx, h.cfg.Region), h.cfg.AccountID, req.LogGroupName),
		CreationTime: h.clk.Now().UnixMilli(),
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

func (h *Handler) describeLogGroupsTyped(ctx context.Context, req *describeLogGroupsRequest) (*describeLogGroupsResponse, *protocol.AWSError) {
	groups, aerr := h.store.listLogGroups(ctx, req.LogGroupNamePrefix)
	if aerr != nil {
		return nil, aerr
	}
	out := make([]logGroupResponse, 0, len(groups))
	for _, g := range groups {
		out = append(out, logGroupResponse{
			LogGroupName:    g.Name,
			ARN:             g.ARN,
			CreationTime:    g.CreationTime,
			RetentionInDays: g.RetentionInDays,
		})
	}
	return &describeLogGroupsResponse{LogGroups: out}, nil
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
	out := make([]logStreamResponse, 0, len(streams))
	for _, s := range streams {
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
	return &describeLogStreamsResponse{LogStreams: out}, nil
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

func (h *Handler) putLogEventsTyped(ctx context.Context, req *putLogEventsRequest) (*putLogEventsResponse, *protocol.AWSError) {
	if req.LogGroupName == "" || req.LogStreamName == "" {
		return nil, errInvalidParameter("logGroupName and logStreamName are required")
	}
	if len(req.LogEvents) == 0 {
		return nil, errInvalidParameter("logEvents must not be empty")
	}
	if _, aerr := h.store.getLogGroup(ctx, req.LogGroupName); aerr != nil {
		return nil, aerr
	}
	ls, aerr := h.store.getLogStream(ctx, req.LogGroupName, req.LogStreamName)
	if aerr != nil {
		return nil, aerr
	}
	now := h.clk.Now().UnixMilli()
	events := make([]LogEvent, 0, len(req.LogEvents))
	for _, e := range req.LogEvents {
		events = append(events, LogEvent{Timestamp: e.Timestamp, Message: e.Message, IngestionTime: now})
	}
	if aerr := h.store.appendEvents(ctx, req.LogGroupName, req.LogStreamName, events); aerr != nil {
		return nil, aerr
	}
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
	return &putLogEventsResponse{NextSequenceToken: ls.UploadSequenceToken}, nil
}

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
			return nil, errInvalidParameter("The specified nextToken is invalid.")
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
			return nil, errInvalidParameter("The specified nextToken is invalid.")
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

	// An explicit LogStreamNames set isn't expressible as a single SQL
	// prefix, so that case queries the whole group (streamPrefix "") and
	// filters to the requested names in Go below — less efficient than the
	// prefix/no-filter case when the group has many unrelated streams, but
	// still bounded by the time window (never a full-history read) and
	// still correct. LogStreamNamePrefix, the far more common filter shape,
	// pushes all the way down to SQL (see sqlEventBackend.getGroupEventsRange).
	streamPrefix := ""
	if !explicitNames {
		streamPrefix = req.LogStreamNamePrefix
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

func (h *Handler) putRetentionPolicyTyped(ctx context.Context, req *putRetentionPolicyRequest) (*struct{}, *protocol.AWSError) {
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

func (h *Handler) tagLogGroupTyped(ctx context.Context, req *tagLogGroupRequest) (*struct{}, *protocol.AWSError) {
	if req.LogGroupName == "" {
		return nil, errInvalidParameter("logGroupName is required")
	}
	g, aerr := h.store.getLogGroup(ctx, req.LogGroupName)
	if aerr != nil {
		return nil, aerr
	}
	if g.Tags == nil {
		g.Tags = make(map[string]string)
	}
	for k, v := range req.Tags {
		g.Tags[k] = v
	}
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
