package cloudformation

// store_legacy_events_test.go — events written by the name-keyed layouts of
// earlier Overcast versions are re-homed under the generation each one
// belongs to, and only that generation reads them.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

// legacyRow seeds one row of the pre-generation per-row layout,
// "<region>/<stackName>/<seq>", carrying an event stamped with stackID.
func legacyRow(t *testing.T, st *cfnStore, name, seq, stackID, eventID string) {
	t.Helper()
	e := testEvent(eventID)
	e.StackName = name
	e.StackID = stackID
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal legacy event: %v", err)
	}
	key := legacyStackEventsPrefix("us-east-1", name) + seq
	if err := st.s.Set(context.Background(), nsEvents, key, string(raw)); err != nil {
		t.Fatalf("seed legacy row %s: %v", key, err)
	}
}

func TestGetStackEvents_legacyRows_reHomedByTheirOwnStackID(t *testing.T) {
	// Given: a name whose per-row legacy history spans two generations — a
	// deleted first stack and the one that replaced it — as an older Overcast
	// wrote them, interleaved under the name alone
	st, _ := newTestCFNStore()
	ctx := context.Background()
	const first = "arn:aws:cloudformation:us-east-1:000000000000:stack/app/gen-1"
	const second = "arn:aws:cloudformation:us-east-1:000000000000:stack/app/gen-2"
	legacyRow(t, st, "app", "100-0000000001", first, "old-create")
	legacyRow(t, st, "app", "100-0000000002", first, "old-fail")
	legacyRow(t, st, "app", "100-0000000003", second, "new-create")

	// When: the current generation's events are read
	got, err := st.getStackEvents(ctx, second)
	if err != nil {
		t.Fatalf("getStackEvents: %v", err)
	}

	// Then: it sees its own event only — the deleted generation's failure
	// does not become its history because they once shared a name
	if ids := eventIDs(got); len(ids) != 1 || ids[0] != "new-create" {
		t.Fatalf("events for the current generation = %v, want [new-create]", ids)
	}

	// And: the deleted generation's events are still reachable by its
	// StackId, in their original order
	old, err := st.getStackEvents(ctx, first)
	if err != nil {
		t.Fatalf("getStackEvents(old): %v", err)
	}
	if ids := eventIDs(old); len(ids) != 2 || ids[0] != "old-create" || ids[1] != "old-fail" {
		t.Fatalf("events for the deleted generation = %v, want [old-create old-fail]", ids)
	}

	// And: nothing is left under the legacy prefix — the read migrated it
	rows, err := st.s.Scan(ctx, nsEvents, legacyStackEventsPrefix("us-east-1", "app"))
	if err != nil {
		t.Fatalf("scan legacy prefix: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("legacy rows still present after migration: %+v", rows)
	}
}

func TestGetStackEvents_legacyRowWithoutAStackID_joinsTheGenerationBeingRead(t *testing.T) {
	// Given: a legacy row whose event carries no usable StackId
	st, _ := newTestCFNStore()
	ctx := context.Background()
	legacyRow(t, st, "app", "100-0000000001", "", "orphan")

	// When: any generation of the name is read
	got, err := st.getStackEvents(ctx, "arn:aws:cloudformation:us-east-1:000000000000:stack/app/gen-9")
	if err != nil {
		t.Fatalf("getStackEvents: %v", err)
	}

	// Then: the row is attributed to the generation asked for rather than
	// dropped — the best attribution there is for an event that names none
	if ids := eventIDs(got); len(ids) != 1 || ids[0] != "orphan" {
		t.Fatalf("events = %v, want [orphan]", ids)
	}
}

func TestGetStackEvents_legacyRows_readTwice_notDuplicated(t *testing.T) {
	// Given: a legacy row, and a store that will persist the migration but
	// refuse to delete the original — the shape of an interrupted migration
	mem := state.NewMemoryStore()
	fs := &failingStore{Store: mem}
	st := newCFNStore(fs, "us-east-1", clock.NewMock())
	ctx := context.Background()
	const id = "arn:aws:cloudformation:us-east-1:000000000000:stack/app/gen-1"
	legacyRow(t, st, "app", "100-0000000001", id, "only")
	fs.failDelete = true

	// When: the generation is read, and then read again with the legacy row
	// still in place beside the migrated one
	for i := 0; i < 2; i++ {
		got, err := st.getStackEvents(ctx, id)
		if err != nil {
			t.Fatalf("read %d: getStackEvents: %v", i, err)
		}

		// Then: the event appears once — the migrated key is derived from
		// the legacy one, so a re-run rewrites the same row rather than
		// adding another
		if ids := eventIDs(got); len(ids) != 1 || ids[0] != "only" {
			t.Fatalf("read %d: events = %v, want [only]", i, ids)
		}
	}
}

func TestGetStackEvents_legacyBlob_eventsReHomedByTheirOwnStackID(t *testing.T) {
	// Given: a single-blob legacy history holding events of two generations
	st, _ := newTestCFNStore()
	ctx := context.Background()
	const first = "arn:aws:cloudformation:us-east-1:000000000000:stack/app/gen-1"
	const second = "arn:aws:cloudformation:us-east-1:000000000000:stack/app/gen-2"
	mk := func(id, stackID string) StackEvent {
		e := testEvent(id)
		e.StackName, e.StackID = "app", stackID
		return e
	}
	raw, err := json.Marshal([]StackEvent{mk("old-1", first), mk("old-2", first), mk("new-1", second)})
	if err != nil {
		t.Fatalf("marshal blob: %v", err)
	}
	blobKey := serviceutil.RegionKey("us-east-1", "app")
	if err := st.s.Set(ctx, nsEvents, blobKey, string(raw)); err != nil {
		t.Fatalf("seed blob: %v", err)
	}

	// When: each generation is read
	got2, err := st.getStackEvents(ctx, second)
	if err != nil {
		t.Fatalf("getStackEvents(second): %v", err)
	}
	got1, err := st.getStackEvents(ctx, first)
	if err != nil {
		t.Fatalf("getStackEvents(first): %v", err)
	}

	// Then: each sees its own events, in blob order
	if ids := eventIDs(got2); len(ids) != 1 || ids[0] != "new-1" {
		t.Errorf("events for the second generation = %v, want [new-1]", ids)
	}
	if ids := eventIDs(got1); len(ids) != 2 || ids[0] != "old-1" || ids[1] != "old-2" {
		t.Errorf("events for the first generation = %v, want [old-1 old-2]", ids)
	}

	// And: the blob is gone
	if _, found, err := st.s.Get(ctx, nsEvents, blobKey); err != nil {
		t.Fatalf("check blob: %v", err)
	} else if found {
		t.Error("legacy blob still present after migration")
	}
}

func TestStackEvents_bareName_isNotAStackID(t *testing.T) {
	// Given: a store
	st, _ := newTestCFNStore()
	ctx := context.Background()

	// When: events are addressed by a bare stack name
	_, getErr := st.getStackEvents(ctx, "mystack")
	appendErr := st.appendStackEvent(ctx, "mystack", testEvent("evt-1"))

	// Then: both refuse — a name identifies whichever stack holds it now,
	// not a history, so a caller must resolve the stack first
	if getErr == nil {
		t.Error("getStackEvents accepted a bare name")
	}
	if appendErr == nil {
		t.Error("appendStackEvent accepted a bare name")
	}
}
