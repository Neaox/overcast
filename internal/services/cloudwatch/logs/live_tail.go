package logs

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	eventsbus "github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/eventstream"
	"github.com/Neaox/overcast/internal/serviceutil"
)

const (
	liveTailQueueSize     = 10
	liveTailMaxUpdateSize = 500
)

type startLiveTailRequest struct {
	LogGroupIdentifiers   []string `json:"logGroupIdentifiers"`
	LogStreamNames        []string `json:"logStreamNames,omitempty"`
	LogStreamNamePrefixes []string `json:"logStreamNamePrefixes,omitempty"`
	LogEventFilterPattern string   `json:"logEventFilterPattern,omitempty"`
}

type liveTailEvent struct {
	LogGroupIdentifier string `json:"logGroupIdentifier"`
	LogStreamName      string `json:"logStreamName"`
	Message            string `json:"message"`
	Timestamp          int64  `json:"timestamp"`
	IngestionTime      int64  `json:"ingestionTime"`
}

type liveTailSessionStart struct {
	RequestID             string   `json:"requestId"`
	SessionID             string   `json:"sessionId"`
	LogGroupIdentifiers   []string `json:"logGroupIdentifiers"`
	LogStreamNames        []string `json:"logStreamNames,omitempty"`
	LogStreamNamePrefixes []string `json:"logStreamNamePrefixes,omitempty"`
	LogEventFilterPattern string   `json:"logEventFilterPattern,omitempty"`
}

// StartLiveTail streams CloudWatch Logs Live Tail sessionStart/sessionUpdate
// events using the AWS event-stream binary format. Per AWS docs, callers pass
// one or more logGroupIdentifiers and may scope by stream names/prefixes and a
// standard CloudWatch Logs filter pattern.
func (h *Handler) StartLiveTail(w http.ResponseWriter, r *http.Request) {
	var req startLiveTailRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if len(req.LogGroupIdentifiers) == 0 {
		protocol.WriteJSONError(w, r, errInvalidParameter("logGroupIdentifiers is required"))
		return
	}
	if len(req.LogGroupIdentifiers) > 10 {
		protocol.WriteJSONError(w, r, errInvalidParameter("logGroupIdentifiers can include up to 10 log groups"))
		return
	}
	if len(req.LogStreamNames) > 0 && len(req.LogStreamNamePrefixes) > 0 {
		protocol.WriteJSONError(w, r, errInvalidParameter("logStreamNames and logStreamNamePrefixes are mutually exclusive"))
		return
	}
	if (len(req.LogStreamNames) > 0 || len(req.LogStreamNamePrefixes) > 0) && len(req.LogGroupIdentifiers) != 1 {
		protocol.WriteJSONError(w, r, errInvalidParameter("logStreamNames and logStreamNamePrefixes require exactly one log group"))
		return
	}
	matcher, err := CompileFilter(req.LogEventFilterPattern)
	if err != nil {
		protocol.WriteJSONError(w, r, errInvalidParameter(err.Error()))
		return
	}

	groups := make(map[string]string, len(req.LogGroupIdentifiers))
	for _, identifier := range req.LogGroupIdentifiers {
		if !strings.HasPrefix(identifier, "arn:") {
			protocol.WriteJSONError(w, r, errInvalidParameter("logGroupIdentifiers must contain log group ARNs"))
			return
		}
		groupName := logGroupNameFromIdentifier(identifier)
		if groupName == "" {
			protocol.WriteJSONError(w, r, errInvalidParameter("logGroupIdentifiers must contain log group names or ARNs"))
			return
		}
		if _, aerr := h.store.getLogGroup(r.Context(), groupName); aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		groups[groupName] = identifier
	}

	w.Header().Set("Content-Type", eventstream.ContentType)
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	sessionID := "overcast-" + h.clk.Now().Format("20060102150405.000000000")
	h.writeLiveTailEvent(w, flusher, "sessionStart", liveTailSessionStart{
		RequestID:             protocol.RequestIDFromContext(r.Context()),
		SessionID:             sessionID,
		LogGroupIdentifiers:   req.LogGroupIdentifiers,
		LogStreamNames:        req.LogStreamNames,
		LogStreamNamePrefixes: req.LogStreamNamePrefixes,
		LogEventFilterPattern: req.LogEventFilterPattern,
	})

	session := newLiveTailSession(r.Context(), h.bus, req, groups, matcher, h.clk.Now)
	defer session.Close()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			batch := session.Drain(liveTailMaxUpdateSize)
			h.writeLiveTailEvent(w, flusher, "sessionUpdate", map[string]any{
				"sessionMetadata": map[string]any{"sampled": false},
				"sessionResults":  batch,
			})
		}
	}
}

type liveTailSession struct {
	cancel  context.CancelFunc
	unsub   func()
	events  chan []liveTailEvent
	req     startLiveTailRequest
	groups  map[string]string
	matcher Matcher
	now     func() time.Time
}

func newLiveTailSession(parent context.Context, bus *eventsbus.Bus, req startLiveTailRequest, groups map[string]string, matcher Matcher, now func() time.Time) *liveTailSession {
	ctx, cancel := context.WithCancel(parent)
	s := &liveTailSession{
		cancel:  cancel,
		events:  make(chan []liveTailEvent, liveTailQueueSize),
		req:     req,
		groups:  groups,
		matcher: matcher,
		now:     now,
	}
	if bus == nil {
		return s
	}
	s.unsub = bus.Subscribe(eventsbus.LogEventsWritten, func(_ context.Context, event eventsbus.Event) {
		select {
		case <-ctx.Done():
			return
		default:
		}
		batch := s.match(event)
		if len(batch) == 0 {
			return
		}
		s.enqueue(ctx, batch)
	})
	return s
}

func (s *liveTailSession) Close() {
	s.cancel()
	if s.unsub != nil {
		s.unsub()
	}
}

func (s *liveTailSession) enqueue(ctx context.Context, batch []liveTailEvent) {
	select {
	case s.events <- batch:
		return
	case <-ctx.Done():
		return
	default:
	}

	select {
	case <-s.events:
	default:
	}
	select {
	case s.events <- batch:
	case <-ctx.Done():
	}
}

func (s *liveTailSession) Drain(limit int) []liveTailEvent {
	out := make([]liveTailEvent, 0)
	for len(out) < limit {
		select {
		case batch := <-s.events:
			remaining := limit - len(out)
			if len(batch) > remaining {
				out = append(out, batch[:remaining]...)
				return out
			}
			out = append(out, batch...)
		default:
			return out
		}
	}
	return out
}

func (s *liveTailSession) match(event eventsbus.Event) []liveTailEvent {
	payload, ok := event.Payload.(eventsbus.LogEventsWrittenPayload)
	if !ok {
		return nil
	}
	identifier, ok := s.groups[payload.LogGroupName]
	if !ok || !s.streamAllowed(payload.LogStreamName) {
		return nil
	}
	ingestionTime := s.now().UnixMilli()
	out := make([]liveTailEvent, 0, len(payload.Events))
	for _, item := range payload.Events {
		if !s.matcher(item.Message) {
			continue
		}
		out = append(out, liveTailEvent{
			LogGroupIdentifier: identifier,
			LogStreamName:      payload.LogStreamName,
			Message:            item.Message,
			Timestamp:          item.Timestamp,
			IngestionTime:      ingestionTime,
		})
	}
	return out
}

func (s *liveTailSession) streamAllowed(name string) bool {
	if len(s.req.LogStreamNames) == 0 && len(s.req.LogStreamNamePrefixes) == 0 {
		return true
	}
	for _, allowed := range s.req.LogStreamNames {
		if name == allowed {
			return true
		}
	}
	for _, prefix := range s.req.LogStreamNamePrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func (h *Handler) writeLiveTailEvent(w http.ResponseWriter, flusher http.Flusher, eventType string, payload any) {
	data, _ := json.Marshal(payload)
	_ = eventstream.WriteMessage(w, []eventstream.Header{
		{Name: ":message-type", Value: "event"},
		{Name: ":event-type", Value: eventType},
		{Name: ":content-type", Value: "application/json"},
	}, data)
	if flusher != nil {
		flusher.Flush()
	}
}

func logGroupNameFromIdentifier(identifier string) string {
	if !strings.HasPrefix(identifier, "arn:") {
		return identifier
	}
	const marker = ":log-group:"
	idx := strings.Index(identifier, marker)
	if idx < 0 {
		return ""
	}
	name := identifier[idx+len(marker):]
	if streamIdx := strings.Index(name, ":log-stream:"); streamIdx >= 0 {
		name = name[:streamIdx]
	}
	return name
}
