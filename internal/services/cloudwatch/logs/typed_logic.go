package logs

import (
	"context"
	"fmt"
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
	allEvents, aerr := h.store.getEvents(ctx, req.LogGroupName, req.LogStreamName)
	if aerr != nil {
		return nil, aerr
	}
	filtered := make([]LogEvent, 0, len(allEvents))
	for _, e := range allEvents {
		if req.StartTime != nil && e.Timestamp < *req.StartTime {
			continue
		}
		if req.EndTime != nil && e.Timestamp >= *req.EndTime {
			continue
		}
		filtered = append(filtered, e)
	}
	out := make([]logEventResponse, 0, len(filtered))
	for _, e := range filtered {
		out = append(out, logEventResponse(e))
	}
	return &getLogEventsResponse{
		Events:            out,
		NextForwardToken:  fmt.Sprintf("f/%d", len(allEvents)),
		NextBackwardToken: "b/0",
	}, nil
}

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
	var streams []*LogStream
	if len(req.LogStreamNames) > 0 {
		for _, name := range req.LogStreamNames {
			ls, aerr := h.store.getLogStream(ctx, req.LogGroupName, name)
			if aerr != nil {
				continue
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
	var matched []filteredEventResponse
	searched := make([]searchedLogStreamResponse, 0, len(streams))
	for _, ls := range streams {
		if req.StartTime != nil && ls.LastEventTimestamp > 0 && ls.LastEventTimestamp < *req.StartTime {
			searched = append(searched, searchedLogStreamResponse{LogStreamName: ls.Name, SearchedCompletely: true})
			continue
		}
		if req.EndTime != nil && ls.FirstEventTimestamp > 0 && ls.FirstEventTimestamp > *req.EndTime {
			searched = append(searched, searchedLogStreamResponse{LogStreamName: ls.Name, SearchedCompletely: true})
			continue
		}
		events, aerr := h.store.getEvents(ctx, req.LogGroupName, ls.Name)
		if aerr != nil {
			continue
		}
		startIdx := 0
		if req.StartTime != nil {
			startIdx = sort.Search(len(events), func(i int) bool {
				return events[i].Timestamp >= *req.StartTime
			})
		}
		endIdx := len(events)
		if req.EndTime != nil {
			endIdx = sort.Search(len(events), func(i int) bool {
				return events[i].Timestamp > *req.EndTime
			})
		}
		for _, e := range events[startIdx:endIdx] {
			if !matcher(e.Message) {
				continue
			}
			matched = append(matched, filteredEventResponse{
				Timestamp:     e.Timestamp,
				Message:       e.Message,
				IngestionTime: e.IngestionTime,
				LogStreamName: ls.Name,
			})
		}
		searched = append(searched, searchedLogStreamResponse{LogStreamName: ls.Name, SearchedCompletely: true})
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].Timestamp < matched[j].Timestamp })
	return &filterLogEventsResponse{Events: matched, SearchedLogStreams: searched}, nil
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
