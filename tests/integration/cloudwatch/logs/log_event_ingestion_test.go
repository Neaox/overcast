package logs_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// putLogEventsResult is PutLogEvents' full response envelope, including the
// rejectedLogEventsInfo AWS uses to report events it accepted the call for but
// silently discarded. The index fields are pointers so a test can tell "not
// reported" from "reported as 0".
type putLogEventsResult struct {
	NextSequenceToken     string `json:"nextSequenceToken"`
	RejectedLogEventsInfo *struct {
		ExpiredLogEventEndIndex  *int `json:"expiredLogEventEndIndex"`
		TooNewLogEventStartIndex *int `json:"tooNewLogEventStartIndex"`
		TooOldLogEventEndIndex   *int `json:"tooOldLogEventEndIndex"`
	} `json:"rejectedLogEventsInfo"`
}

// putLogEventsRaw issues PutLogEvents and decodes the whole response envelope,
// asserting only that the call succeeded — the point of these tests is that an
// out-of-window event does NOT turn PutLogEvents into an error.
func putLogEventsRaw(t *testing.T, srv *helpers.TestServer, group, stream string, events []logEvent) putLogEventsResult {
	t.Helper()
	resp := logsCall(t, srv, "PutLogEvents", map[string]any{
		"logGroupName":  group,
		"logStreamName": stream,
		"logEvents":     events,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result putLogEventsResult
	helpers.DecodeJSON(t, resp, &result)
	return result
}

// storedMessages returns every message GetLogEvents reports for a stream, in
// order — what actually made it into storage.
func storedMessages(t *testing.T, srv *helpers.TestServer, group, stream string) []string {
	t.Helper()
	resp := logsCall(t, srv, "GetLogEvents", map[string]any{
		"logGroupName":  group,
		"logStreamName": stream,
		"startFromHead": true,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Events []struct {
			Message string `json:"message"`
		} `json:"events"`
	}
	helpers.DecodeJSON(t, resp, &result)
	out := make([]string, 0, len(result.Events))
	for _, e := range result.Events {
		out = append(out, e.Message)
	}
	return out
}

func TestPutLogEvents_tooOldEventsAreReportedAndDiscarded(t *testing.T) {
	// Given: a log group with no retention policy, so the only "too old" rule
	// in play is AWS's documented 14-day one
	srv := helpers.NewTestServer(t)
	createLogGroup(t, srv, "/aws/lambda/too-old")
	createLogStream(t, srv, "/aws/lambda/too-old", "stream-1")
	now := time.Now()

	// When: a batch mixes a 30-day-old event with a current one
	result := putLogEventsRaw(t, srv, "/aws/lambda/too-old", "stream-1", []logEvent{
		{Timestamp: now.Add(-30 * 24 * time.Hour).UnixMilli(), Message: "ancient"},
		{Timestamp: now.UnixMilli(), Message: "current"},
	})

	// Then: the call still succeeds — AWS does not raise here — and reports
	// the discard through rejectedLogEventsInfo. tooOldLogEventEndIndex is
	// documented as the index of the last too-old event, exclusive, so one
	// leading rejected event reports 1.
	if result.RejectedLogEventsInfo == nil {
		t.Fatal("a 30-day-old event was accepted with no rejectedLogEventsInfo")
	}
	if result.RejectedLogEventsInfo.TooOldLogEventEndIndex == nil {
		t.Fatalf("rejectedLogEventsInfo has no tooOldLogEventEndIndex: %+v", result.RejectedLogEventsInfo)
	}
	if got := *result.RejectedLogEventsInfo.TooOldLogEventEndIndex; got != 1 {
		t.Errorf("tooOldLogEventEndIndex = %d, want 1", got)
	}

	// And: the rejected event is not stored — the field is the only signal a
	// client gets that the data was dropped, so it has to be true.
	got := storedMessages(t, srv, "/aws/lambda/too-old", "stream-1")
	if len(got) != 1 || got[0] != "current" {
		t.Errorf("stored messages = %v, want only [current]", got)
	}
}

func TestPutLogEvents_tooNewEventsAreReportedAndDiscarded(t *testing.T) {
	// Given: a log group and stream
	srv := helpers.NewTestServer(t)
	createLogGroup(t, srv, "/aws/lambda/too-new")
	createLogStream(t, srv, "/aws/lambda/too-new", "stream-1")
	now := time.Now()

	// When: a batch mixes a current event with one six hours in the future
	result := putLogEventsRaw(t, srv, "/aws/lambda/too-new", "stream-1", []logEvent{
		{Timestamp: now.UnixMilli(), Message: "current"},
		{Timestamp: now.Add(6 * time.Hour).UnixMilli(), Message: "from the future"},
	})

	// Then: 200, and the trailing event is reported. tooNewLogEventStartIndex
	// is documented as the index of the first too-new event, inclusive.
	if result.RejectedLogEventsInfo == nil {
		t.Fatal("an event 6h in the future was accepted with no rejectedLogEventsInfo")
	}
	if result.RejectedLogEventsInfo.TooNewLogEventStartIndex == nil {
		t.Fatalf("rejectedLogEventsInfo has no tooNewLogEventStartIndex: %+v", result.RejectedLogEventsInfo)
	}
	if got := *result.RejectedLogEventsInfo.TooNewLogEventStartIndex; got != 1 {
		t.Errorf("tooNewLogEventStartIndex = %d, want 1", got)
	}

	// And: only the in-window event is stored
	got := storedMessages(t, srv, "/aws/lambda/too-new", "stream-1")
	if len(got) != 1 || got[0] != "current" {
		t.Errorf("stored messages = %v, want only [current]", got)
	}
}

func TestPutLogEvents_eventsPastRetentionAreReportedAsExpired(t *testing.T) {
	// Given: a log group whose retention is one day — inside the 14-day
	// absolute rule, so only the retention-derived rule can reject
	srv := helpers.NewTestServer(t)
	createLogGroup(t, srv, "/aws/lambda/expired")
	createLogStream(t, srv, "/aws/lambda/expired", "stream-1")
	resp := logsCall(t, srv, "PutRetentionPolicy", map[string]any{
		"logGroupName":    "/aws/lambda/expired",
		"retentionInDays": 1,
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	now := time.Now()

	// When: a batch mixes a two-day-old event with a current one
	result := putLogEventsRaw(t, srv, "/aws/lambda/expired", "stream-1", []logEvent{
		{Timestamp: now.Add(-48 * time.Hour).UnixMilli(), Message: "past retention"},
		{Timestamp: now.UnixMilli(), Message: "current"},
	})

	// Then: it is reported as expired rather than too old, and discarded
	if result.RejectedLogEventsInfo == nil {
		t.Fatal("an event older than the retention period was accepted with no rejectedLogEventsInfo")
	}
	if result.RejectedLogEventsInfo.ExpiredLogEventEndIndex == nil {
		t.Fatalf("rejectedLogEventsInfo has no expiredLogEventEndIndex: %+v", result.RejectedLogEventsInfo)
	}
	if got := *result.RejectedLogEventsInfo.ExpiredLogEventEndIndex; got != 1 {
		t.Errorf("expiredLogEventEndIndex = %d, want 1", got)
	}
	if got := storedMessages(t, srv, "/aws/lambda/expired", "stream-1"); len(got) != 1 || got[0] != "current" {
		t.Errorf("stored messages = %v, want only [current]", got)
	}
}

func TestPutLogEvents_beyondBothOldRulesReportsTooOld(t *testing.T) {
	// Given: a log group with a one-day retention, so an ancient event breaks
	// the absolute 14-day rule and the retention-derived rule at once
	srv := helpers.NewTestServer(t)
	createLogGroup(t, srv, "/aws/lambda/both-rules")
	createLogStream(t, srv, "/aws/lambda/both-rules", "stream-1")
	resp := logsCall(t, srv, "PutRetentionPolicy", map[string]any{
		"logGroupName":    "/aws/lambda/both-rules",
		"retentionInDays": 1,
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// When: a 30-day-old event is sent
	result := putLogEventsRaw(t, srv, "/aws/lambda/both-rules", "stream-1", []logEvent{
		{Timestamp: time.Now().Add(-30 * 24 * time.Hour).UnixMilli(), Message: "ancient"},
	})

	// Then: the absolute rule wins, so the group's retention does not change
	// how a very old event is classified
	if result.RejectedLogEventsInfo == nil || result.RejectedLogEventsInfo.TooOldLogEventEndIndex == nil {
		t.Fatalf("expected tooOldLogEventEndIndex, got %+v", result.RejectedLogEventsInfo)
	}
	if result.RejectedLogEventsInfo.ExpiredLogEventEndIndex != nil {
		t.Errorf("an event outside the 14-day window was also reported as expired: %+v", result.RejectedLogEventsInfo)
	}
}

func TestPutLogEvents_inWindowBatchReportsNoRejections(t *testing.T) {
	// Given: a log group and stream
	srv := helpers.NewTestServer(t)
	createLogGroup(t, srv, "/aws/lambda/in-window")
	createLogStream(t, srv, "/aws/lambda/in-window", "stream-1")
	now := time.Now()

	// When: every event sits inside the accepted window
	result := putLogEventsRaw(t, srv, "/aws/lambda/in-window", "stream-1", []logEvent{
		{Timestamp: now.Add(-13 * 24 * time.Hour).UnixMilli(), Message: "old but valid"},
		{Timestamp: now.Add(time.Hour).UnixMilli(), Message: "slightly ahead but valid"},
	})

	// Then: the field is absent entirely — a client uses its presence to
	// detect loss, so an always-present empty object would be a false alarm.
	if result.RejectedLogEventsInfo != nil {
		t.Errorf("in-window batch reported rejectedLogEventsInfo: %+v", result.RejectedLogEventsInfo)
	}
	if got := storedMessages(t, srv, "/aws/lambda/in-window", "stream-1"); len(got) != 2 {
		t.Errorf("stored %d events, want 2: %v", len(got), got)
	}
}

func TestPutLogEvents_outOfOrderBatchIsRejected(t *testing.T) {
	// Given: a log group and stream
	srv := helpers.NewTestServer(t)
	createLogGroup(t, srv, "/aws/lambda/out-of-order")
	createLogStream(t, srv, "/aws/lambda/out-of-order", "stream-1")
	now := time.Now().UnixMilli()

	// When: the batch is not in ascending timestamp order
	resp := logsCall(t, srv, "PutLogEvents", map[string]any{
		"logGroupName":  "/aws/lambda/out-of-order",
		"logStreamName": "stream-1",
		"logEvents": []logEvent{
			{Timestamp: now, Message: "second"},
			{Timestamp: now - 1000, Message: "first"},
		},
	})
	defer resp.Body.Close()

	// Then: unlike an out-of-window event, AWS rejects the whole batch
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidParameterException")

	// And: nothing from it is stored
	if got := storedMessages(t, srv, "/aws/lambda/out-of-order", "stream-1"); len(got) != 0 {
		t.Errorf("a rejected batch stored %v", got)
	}
}

func TestPutLogEvents_equalTimestampsAreInOrder(t *testing.T) {
	// Given: a log group and stream
	srv := helpers.NewTestServer(t)
	createLogGroup(t, srv, "/aws/lambda/same-ms")
	createLogStream(t, srv, "/aws/lambda/same-ms", "stream-1")
	now := time.Now().UnixMilli()

	// When: two events share a millisecond — bursty writers do this constantly
	result := putLogEventsRaw(t, srv, "/aws/lambda/same-ms", "stream-1", []logEvent{
		{Timestamp: now, Message: "first"},
		{Timestamp: now, Message: "second"},
	})

	// Then: the batch is chronological, so it is accepted whole
	if result.RejectedLogEventsInfo != nil {
		t.Errorf("same-millisecond batch reported rejectedLogEventsInfo: %+v", result.RejectedLogEventsInfo)
	}
	if got := storedMessages(t, srv, "/aws/lambda/same-ms", "stream-1"); len(got) != 2 {
		t.Errorf("stored %d events, want 2: %v", len(got), got)
	}
}

func TestFilterLogEvents_eventIdResolvesThroughGetLogRecord(t *testing.T) {
	// Given: a stream carrying two events
	srv := helpers.NewTestServer(t)
	createLogGroup(t, srv, "/aws/lambda/records")
	createLogStream(t, srv, "/aws/lambda/records", "stream-1")
	now := time.Now().UnixMilli()
	putLogEvents(t, srv, "/aws/lambda/records", "stream-1", []logEvent{
		{Timestamp: now, Message: "first record"},
		{Timestamp: now + 1000, Message: "second record"},
	})

	// When: FilterLogEvents returns them
	resp := logsCall(t, srv, "FilterLogEvents", map[string]any{
		"logGroupName": "/aws/lambda/records",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var filtered struct {
		Events []struct {
			EventID       string `json:"eventId"`
			Message       string `json:"message"`
			Timestamp     int64  `json:"timestamp"`
			LogStreamName string `json:"logStreamName"`
		} `json:"events"`
	}
	helpers.DecodeJSON(t, resp, &filtered)

	// Then: every event carries an eventId, and they are distinct
	if len(filtered.Events) != 2 {
		t.Fatalf("FilterLogEvents returned %d events, want 2", len(filtered.Events))
	}
	seen := make(map[string]bool, 2)
	for i, e := range filtered.Events {
		if e.EventID == "" {
			t.Fatalf("event %d has no eventId: %+v", i, e)
		}
		if seen[e.EventID] {
			t.Fatalf("event %d reuses eventId %q", i, e.EventID)
		}
		seen[e.EventID] = true
	}

	// And: an eventId resolves through GetLogRecord to that same event
	record := logsCall(t, srv, "GetLogRecord", map[string]any{
		"logRecordPointer": filtered.Events[1].EventID,
	})
	defer record.Body.Close()
	helpers.AssertStatus(t, record, http.StatusOK)
	var got struct {
		LogRecord map[string]string `json:"logRecord"`
	}
	helpers.DecodeJSON(t, record, &got)
	if got.LogRecord["@message"] != "second record" {
		t.Errorf("logRecord[@message] = %q, want %q", got.LogRecord["@message"], "second record")
	}
	if got.LogRecord["@logStream"] != "stream-1" {
		t.Errorf("logRecord[@logStream] = %q, want %q", got.LogRecord["@logStream"], "stream-1")
	}
}

func TestGetLogRecord_malformedPointer(t *testing.T) {
	// Given: a running server
	srv := helpers.NewTestServer(t)

	// When: GetLogRecord is given something that is not one of our pointers
	resp := logsCall(t, srv, "GetLogRecord", map[string]any{"logRecordPointer": "nonsense"})
	defer resp.Body.Close()

	// Then: it is a parameter error, not a not-found
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidParameterException")
}

func TestGetLogRecord_pointerToADeletedEvent(t *testing.T) {
	// Given: an event whose eventId has been handed out, then its stream
	// deleted
	srv := helpers.NewTestServer(t)
	createLogGroup(t, srv, "/aws/lambda/gone")
	createLogStream(t, srv, "/aws/lambda/gone", "stream-1")
	putLogEvents(t, srv, "/aws/lambda/gone", "stream-1", []logEvent{
		{Timestamp: time.Now().UnixMilli(), Message: "doomed"},
	})
	filter := logsCall(t, srv, "FilterLogEvents", map[string]any{"logGroupName": "/aws/lambda/gone"})
	var filtered struct {
		Events []struct {
			EventID string `json:"eventId"`
		} `json:"events"`
	}
	helpers.DecodeJSON(t, filter, &filtered)
	filter.Body.Close()
	if len(filtered.Events) != 1 {
		t.Fatalf("setup: FilterLogEvents returned %d events, want 1", len(filtered.Events))
	}
	deleted := logsCall(t, srv, "DeleteLogStream", map[string]any{
		"logGroupName":  "/aws/lambda/gone",
		"logStreamName": "stream-1",
	})
	deleted.Body.Close()

	// When: the pointer is resolved
	resp := logsCall(t, srv, "GetLogRecord", map[string]any{
		"logRecordPointer": filtered.Events[0].EventID,
	})
	defer resp.Body.Close()

	// Then: the record is reported missing
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
}
