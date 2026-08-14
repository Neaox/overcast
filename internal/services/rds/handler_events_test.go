package rds

// handler_events_test.go — DescribeEvents, and the failure reason finally
// having somewhere to go.
//
// PR 1 gave a DB instance that will not start AWS's "failed" status and
// recorded why on the record. Nothing could read that reason: it is not in the
// DescribeDBInstances wire shape and it must not be, because the real
// DBInstance has no such field. AWS's answer to "why did that happen" is
// DescribeEvents, so that is where the reason has to surface.

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

func newEventsHandler(t *testing.T) (*Handler, *clock.Mock) {
	t.Helper()
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	clk := clock.NewMock()
	clk.Set(time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC))
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clk)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.handler.scheduler.Stop(ctx)
	})
	return s.handler, clk
}

func createInstanceForEvents(t *testing.T, h *Handler, id string) {
	t.Helper()
	if _, aerr := h.createDBInstanceTyped(context.Background(), &createDBInstanceReq{
		DBInstanceIdentifier: id,
		Engine:               "mysql",
		MasterUsername:       "admin",
		MasterUserPassword:   "password123",
	}); aerr != nil {
		t.Fatalf("CreateDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
}

func describeEvents(t *testing.T, h *Handler, req *describeEventsReq) []xmlEvent {
	t.Helper()
	resp, aerr := h.describeEventsTyped(context.Background(), req)
	if aerr != nil {
		t.Fatalf("DescribeEvents: %s: %s", aerr.Code, aerr.Message)
	}
	return resp.Result.Events.Items
}

func messagesOf(events []xmlEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Message)
	}
	return out
}

// The lifecycle transitions Overcast already knows about have to be visible
// somewhere. An event feed with nothing in it is not a diagnosis.
func TestDescribeEvents_recordsTheInstanceLifecycle(t *testing.T) {
	h, clk := newEventsHandler(t)
	ctx := context.Background()
	const id = "events-lifecycle"

	createInstanceForEvents(t, h, id)
	h.scheduler.AdvanceAndSettle(clk, time.Second) // creating → available

	if _, aerr := h.stopDBInstanceTyped(ctx, &stopDBInstanceReq{DBInstanceIdentifier: id}); aerr != nil {
		t.Fatalf("StopDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	h.scheduler.AdvanceAndSettle(clk, time.Second) // stopping → stopped
	if _, aerr := h.startDBInstanceTyped(ctx, &startDBInstanceReq{DBInstanceIdentifier: id}); aerr != nil {
		t.Fatalf("StartDBInstance: %s: %s", aerr.Code, aerr.Message)
	}

	got := describeEvents(t, h, &describeEventsReq{})
	want := []string{"DB instance created.", "DB instance stopped.", "DB instance started."}
	if len(got) != len(want) {
		t.Fatalf("DescribeEvents returned %d events (%v), want %v", len(got), messagesOf(got), want)
	}
	for i, msg := range want {
		if got[i].Message != msg {
			t.Errorf("event %d message = %q, want %q", i, got[i].Message, msg)
		}
		if got[i].SourceIdentifier != id {
			t.Errorf("event %d SourceIdentifier = %q, want %q", i, got[i].SourceIdentifier, id)
		}
		if got[i].SourceType != "db-instance" {
			t.Errorf("event %d SourceType = %q, want db-instance", i, got[i].SourceType)
		}
		if got[i].SourceArn == "" {
			t.Errorf("event %d has no SourceArn", i)
		}
		if got[i].Date == "" {
			t.Errorf("event %d has no Date", i)
		}
	}
	if cats := got[0].EventCategories.Items; len(cats) != 1 || cats[0] != "creation" {
		t.Errorf("creation event categories = %v, want [creation]", cats)
	}
	if cats := got[1].EventCategories.Items; len(cats) != 1 || cats[0] != "notification" {
		t.Errorf("stop event categories = %v, want [notification]", cats)
	}
}

// The point of the whole exercise: an instance that will not start says so,
// with the reason, through the one channel AWS provides for it.
func TestDescribeEvents_carriesTheFailureReason(t *testing.T) {
	h, _ := newEventsHandler(t)
	ctx := context.Background()
	const id = "events-failed"
	const reason = "the database container exited with code 1 — see the instance logs for the engine's own output"

	createInstanceForEvents(t, h, id) // stays "creating" under a mock clock
	h.failInstance(ctx, id, reason)

	got := describeEvents(t, h, &describeEventsReq{EventCategories: []string{"failure"}})
	if len(got) != 1 {
		t.Fatalf("DescribeEvents(failure) returned %v, want one failure event", messagesOf(got))
	}
	// A failure while still creating is RDS-EVENT-0278's shape.
	want := "The DB instance creation failed. " + reason + "."
	if got[0].Message != want {
		t.Errorf("failure message = %q, want %q", got[0].Message, want)
	}

	// And a failure on the way back up is RDS-EVENT-0035's.
	inst, aerr := h.store.getDBInstance(ctx, id)
	if aerr != nil {
		t.Fatalf("getDBInstance: %s", aerr.Message)
	}
	inst.DBInstanceStatus = "starting"
	if aerr := h.store.putDBInstance(ctx, inst); aerr != nil {
		t.Fatalf("putDBInstance: %s", aerr.Message)
	}
	h.failInstance(ctx, id, reason)

	got = describeEvents(t, h, &describeEventsReq{EventCategories: []string{"failure"}})
	if len(got) != 2 {
		t.Fatalf("DescribeEvents(failure) returned %v, want two failure events", messagesOf(got))
	}
	want = "Database instance put into failed state. " + reason + "."
	if got[1].Message != want {
		t.Errorf("restart failure message = %q, want %q", got[1].Message, want)
	}
}

// A caller asking about one instance must not be handed another's history, and
// a category filter has to actually filter.
func TestDescribeEvents_filtersBySourceAndCategory(t *testing.T) {
	h, clk := newEventsHandler(t)
	ctx := context.Background()

	createInstanceForEvents(t, h, "events-a")
	createInstanceForEvents(t, h, "events-b")
	h.scheduler.AdvanceAndSettle(clk, time.Second)
	if _, aerr := h.stopDBInstanceTyped(ctx, &stopDBInstanceReq{DBInstanceIdentifier: "events-a"}); aerr != nil {
		t.Fatalf("StopDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	h.scheduler.AdvanceAndSettle(clk, 0)

	got := describeEvents(t, h, &describeEventsReq{SourceType: "db-instance", SourceIdentifier: "events-a"})
	if len(got) != 2 {
		t.Fatalf("events for events-a = %v, want create + stop", messagesOf(got))
	}
	for _, e := range got {
		if e.SourceIdentifier != "events-a" {
			t.Errorf("filter leaked an event for %q", e.SourceIdentifier)
		}
	}

	got = describeEvents(t, h, &describeEventsReq{EventCategories: []string{"creation"}})
	if len(got) != 2 {
		t.Fatalf("creation events = %v, want one per instance", messagesOf(got))
	}

	// A source type Overcast records nothing for is an empty list, not an error.
	if got := describeEvents(t, h, &describeEventsReq{SourceType: "db-cluster"}); len(got) != 0 {
		t.Errorf("db-cluster events = %v, want none", messagesOf(got))
	}
	// A source type that is not an RDS source type at all is a parameter error.
	if _, aerr := h.describeEventsTyped(ctx, &describeEventsReq{SourceType: "not-a-thing"}); aerr == nil {
		t.Error("DescribeEvents accepted an invalid SourceType")
	}
}

// With no StartTime and no Duration AWS returns the past 60 minutes; asking
// for a longer window is how you see anything older.
func TestDescribeEvents_defaultWindowIsThePastHour(t *testing.T) {
	h, clk := newEventsHandler(t)
	const id = "events-window"

	createInstanceForEvents(t, h, id)
	clk.Add(90 * time.Minute)

	if got := describeEvents(t, h, &describeEventsReq{}); len(got) != 0 {
		t.Errorf("default window returned %v, want nothing older than an hour", messagesOf(got))
	}
	if got := describeEvents(t, h, &describeEventsReq{Duration: 180}); len(got) != 1 {
		t.Errorf("Duration=180 returned %v, want the creation event", messagesOf(got))
	}
	start := clk.Now().Add(-3 * time.Hour).UTC().Format(time.RFC3339)
	if got := describeEvents(t, h, &describeEventsReq{StartTime: start}); len(got) != 1 {
		t.Errorf("StartTime=%s returned %v, want the creation event", start, messagesOf(got))
	}
}

// The store must not be a place events accumulate forever. AWS keeps 14 days;
// so does this, plus a hard count cap for a busy day.
func TestDescribeEvents_retentionIsBounded(t *testing.T) {
	h, clk := newEventsHandler(t)
	ctx := context.Background()

	h.recordInstanceEvent(ctx, "old-instance", "DB instance created.", "creation")
	clk.Add(15 * 24 * time.Hour)
	h.recordInstanceEvent(ctx, "new-instance", "DB instance created.", "creation")

	all, aerr := h.store.listEvents(ctx)
	if aerr != nil {
		t.Fatalf("listEvents: %s", aerr.Message)
	}
	if len(all) != 1 || all[0].SourceIdentifier != "new-instance" {
		t.Fatalf("stored events = %d, want the 15-day-old one pruned", len(all))
	}

	for i := 0; i < maxStoredEventsPerRegion+50; i++ {
		h.recordInstanceEvent(ctx, "busy-instance", "DB instance created.", "creation")
	}
	all, aerr = h.store.listEvents(ctx)
	if aerr != nil {
		t.Fatalf("listEvents: %s", aerr.Message)
	}
	if len(all) > maxStoredEventsPerRegion {
		t.Errorf("stored events = %d, want no more than %d", len(all), maxStoredEventsPerRegion)
	}
}

// Pagination has to be real pagination: a marker that continues where the last
// page stopped, and no marker on the last page.
func TestDescribeEvents_paginates(t *testing.T) {
	h, _ := newEventsHandler(t)
	ctx := context.Background()

	for i := 0; i < 45; i++ {
		h.recordInstanceEvent(ctx, "paged", "DB instance created.", "creation")
	}

	first, aerr := h.describeEventsTyped(ctx, &describeEventsReq{MaxRecords: 20})
	if aerr != nil {
		t.Fatalf("DescribeEvents: %s: %s", aerr.Code, aerr.Message)
	}
	if len(first.Result.Events.Items) != 20 {
		t.Fatalf("first page = %d events, want 20", len(first.Result.Events.Items))
	}
	if first.Result.Marker == "" {
		t.Fatal("first page carries no Marker, so the remaining events are unreachable")
	}

	seen := len(first.Result.Events.Items)
	marker := first.Result.Marker
	for marker != "" {
		page, aerr := h.describeEventsTyped(ctx, &describeEventsReq{MaxRecords: 20, Marker: marker})
		if aerr != nil {
			t.Fatalf("DescribeEvents(marker): %s: %s", aerr.Code, aerr.Message)
		}
		seen += len(page.Result.Events.Items)
		marker = page.Result.Marker
	}
	if seen != 45 {
		t.Errorf("paged through %d events, want 45", seen)
	}

	// AWS documents MaxRecords as 20-100 and rejects anything outside it.
	if _, aerr := h.describeEventsTyped(ctx, &describeEventsReq{MaxRecords: 5}); aerr == nil {
		t.Error("DescribeEvents accepted MaxRecords=5, below the documented minimum of 20")
	}
	if _, aerr := h.describeEventsTyped(ctx, &describeEventsReq{Marker: "not-a-marker"}); aerr == nil {
		t.Error("DescribeEvents accepted a garbage Marker instead of rejecting it")
	}
}
