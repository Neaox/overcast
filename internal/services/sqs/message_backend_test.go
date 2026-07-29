package sqs

import (
	"context"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

// newTestMessageBackends returns one messageBackend per implementation
// available in this build, keyed by the label used for its t.Run subtest, so
// tests can run the same assertions against every one of them — the
// memory-mode parity requirement from docs/plans/storage-plan.md's graduation
// rule. Mirrors internal/services/cloudwatch/logs/event_backend_test.go's
// newTestBackends.
//
// "sql" is a real sqlMessageBackend over a temp-dir-rooted *state.HybridStore,
// and is only present when the build has SQLite compiled in. Under
// -tags nosqlite, state.NewHybridStore is a stub that always errors
// (internal/state/sqlite_hybrid_nosqlite.go), so the map degrades to
// memory-only rather than this file being tagged out — the memory half of
// every parity test below still works there and is worth keeping. See
// tests/AGENTS.md § Build-tag-sensitive tests.
func newTestMessageBackends(t *testing.T) map[string]messageBackend {
	t.Helper()
	backends := map[string]messageBackend{"memory": newMemMessageBackend()}
	if !config.SQLiteSupported() {
		return backends
	}

	dir := t.TempDir()
	hybrid, err := state.NewHybridStore(dir, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	t.Cleanup(func() {
		if err := hybrid.Close(); err != nil {
			t.Logf("hybrid.Close: %v", err)
		}
	})
	backendStore := state.Unwrap(hybrid, serviceName)
	sqlBackend := newMessageBackendFor(backendStore)
	if _, ok := sqlBackend.(*sqlMessageBackend); !ok {
		t.Fatalf("expected sqlMessageBackend for a SQLite-backed store, got %T", sqlBackend)
	}
	backends["sql"] = sqlBackend

	return backends
}

func newTestMessage(id, group, seq string, visibleAt time.Time) *Message {
	return &Message{
		MessageID:      id,
		Body:           "body-" + id,
		MD5OfBody:      "d41d8cd98f00b204e9800998ecf8427e",
		VisibleAfter:   visibleAt,
		MessageGroupId: group,
		SequenceNumber: seq,
	}
}

// TestMessageBackend_MemoryAndSQL_Parity runs the same put/get/delete/
// receive/count sequence against both backends and asserts identical
// externally-observable behavior — the "backend parity" test the storage-
// plan 3.10 report calls for.
func TestMessageBackend_MemoryAndSQL_Parity(t *testing.T) {
	for name, b := range newTestMessageBackends(t) {
		b := b
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			const region, queue = "us-east-1", "my-queue"
			now := time.UnixMilli(1_700_000_000_000).UTC()

			// Empty queue reads back empty, non-nil results.
			msgs, err := b.listMessages(ctx, region, queue)
			if err != nil {
				t.Fatalf("listMessages (empty): %v", err)
			}
			if len(msgs) != 0 {
				t.Fatalf("listMessages (empty) = %v, want empty", msgs)
			}
			blocked, err := b.blockedGroups(ctx, region, queue, now)
			if err != nil {
				t.Fatalf("blockedGroups (empty): %v", err)
			}
			if len(blocked) != 0 {
				t.Fatalf("blockedGroups (empty) = %v, want empty", blocked)
			}

			// Put three standard messages, one visible, one not-yet-visible
			// (delayed), one already received (in-flight).
			visible := newTestMessage("m-visible", "", "", now.Add(-time.Second))
			delayed := newTestMessage("m-delayed", "", "", now.Add(time.Hour))
			inflight := newTestMessage("m-inflight", "", "", now.Add(time.Minute))
			for _, m := range []*Message{visible, delayed, inflight} {
				if err := b.putMessage(ctx, region, queue, m); err != nil {
					t.Fatalf("putMessage(%s): %v", m.MessageID, err)
				}
			}

			// getMessage round-trips exactly.
			got, found, err := b.getMessage(ctx, region, queue, "m-visible")
			if err != nil || !found {
				t.Fatalf("getMessage(m-visible): found=%v err=%v", found, err)
			}
			if got.Body != visible.Body {
				t.Fatalf("getMessage(m-visible).Body = %q, want %q", got.Body, visible.Body)
			}

			// getMessage on a missing ID reports not-found, not an error.
			_, found, err = b.getMessage(ctx, region, queue, "does-not-exist")
			if err != nil {
				t.Fatalf("getMessage(missing): unexpected error %v", err)
			}
			if found {
				t.Fatalf("getMessage(missing): found = true, want false")
			}

			// receiveCandidates (standard, unordered) returns only the
			// visible one.
			candidates, err := b.receiveCandidates(ctx, region, queue, now, 10, false)
			if err != nil {
				t.Fatalf("receiveCandidates: %v", err)
			}
			if len(candidates) != 1 || candidates[0].MessageID != "m-visible" {
				t.Fatalf("receiveCandidates = %v, want just m-visible", candidates)
			}

			// countMessages: 1 visible, 3 total.
			visibleCount, total, err := b.countMessages(ctx, region, queue, now)
			if err != nil {
				t.Fatalf("countMessages: %v", err)
			}
			if visibleCount != 1 || total != 3 {
				t.Fatalf("countMessages = (%d, %d), want (1, 3)", visibleCount, total)
			}

			// deleteMessage removes exactly one.
			if err := b.deleteMessage(ctx, region, queue, "m-delayed"); err != nil {
				t.Fatalf("deleteMessage: %v", err)
			}
			if _, found, _ := b.getMessage(ctx, region, queue, "m-delayed"); found {
				t.Fatalf("m-delayed still found after deleteMessage")
			}
			_, total, _ = b.countMessages(ctx, region, queue, now)
			if total != 2 {
				t.Fatalf("countMessages total after delete = %d, want 2", total)
			}

			// deleteQueueMessages clears everything for the queue.
			if err := b.deleteQueueMessages(ctx, region, queue); err != nil {
				t.Fatalf("deleteQueueMessages: %v", err)
			}
			msgs, err = b.listMessages(ctx, region, queue)
			if err != nil {
				t.Fatalf("listMessages after deleteQueueMessages: %v", err)
			}
			if len(msgs) != 0 {
				t.Fatalf("listMessages after deleteQueueMessages = %v, want empty", msgs)
			}

			// debugDeleteAll clears every queue.
			if err := b.putMessage(ctx, region, queue, visible); err != nil {
				t.Fatalf("re-seed for debugDeleteAll: %v", err)
			}
			if err := b.debugDeleteAll(ctx); err != nil {
				t.Fatalf("debugDeleteAll: %v", err)
			}
			msgs, _ = b.listMessages(ctx, region, queue)
			if len(msgs) != 0 {
				t.Fatalf("listMessages after debugDeleteAll = %v, want empty", msgs)
			}
		})
	}
}

// TestMessageBackend_FIFOOrdering_Parity proves both backends deliver FIFO
// candidates in sequence-number order, identical to each other and to the
// pre-graduation full-scan-then-sort behavior.
func TestMessageBackend_FIFOOrdering_Parity(t *testing.T) {
	for name, b := range newTestMessageBackends(t) {
		b := b
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			const region, queue = "us-east-1", "fifo-queue.fifo"
			now := time.UnixMilli(1_700_000_000_000).UTC()

			// Insert out of sequence order to prove the backend sorts, not
			// just returns insertion order.
			msgs := []*Message{
				newTestMessage("m-3", "group-a", "3", now.Add(-time.Second)),
				newTestMessage("m-1", "group-a", "1", now.Add(-time.Second)),
				newTestMessage("m-2", "group-a", "2", now.Add(-time.Second)),
			}
			for _, m := range msgs {
				if err := b.putMessage(ctx, region, queue, m); err != nil {
					t.Fatalf("putMessage(%s): %v", m.MessageID, err)
				}
			}

			got, err := b.receiveCandidates(ctx, region, queue, now, 10, true)
			if err != nil {
				t.Fatalf("receiveCandidates: %v", err)
			}
			if len(got) != 3 {
				t.Fatalf("receiveCandidates returned %d messages, want 3", len(got))
			}
			wantOrder := []string{"m-1", "m-2", "m-3"}
			for i, want := range wantOrder {
				if got[i].MessageID != want {
					t.Fatalf("candidate %d = %s, want %s (full order: %v)", i, got[i].MessageID, want, candidateIDs(got))
				}
			}
		})
	}
}

func candidateIDs(msgs []*Message) []string {
	ids := make([]string, len(msgs))
	for i, m := range msgs {
		ids[i] = m.MessageID
	}
	return ids
}

// TestMessageBackend_BlockedGroups_Parity proves both backends identify a
// FIFO group as blocked exactly when it has an invisible message, and never
// for standard (empty-group) messages.
func TestMessageBackend_BlockedGroups_Parity(t *testing.T) {
	for name, b := range newTestMessageBackends(t) {
		b := b
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			const region, queue = "us-east-1", "fifo-queue.fifo"
			now := time.UnixMilli(1_700_000_000_000).UTC()

			visibleA := newTestMessage("a-visible", "group-a", "1", now.Add(-time.Second))
			invisibleA := newTestMessage("a-invisible", "group-a", "2", now.Add(time.Hour))
			visibleB := newTestMessage("b-visible", "group-b", "1", now.Add(-time.Second))
			noGroupInvisible := newTestMessage("no-group", "", "1", now.Add(time.Hour))

			for _, m := range []*Message{visibleA, invisibleA, visibleB, noGroupInvisible} {
				if err := b.putMessage(ctx, region, queue, m); err != nil {
					t.Fatalf("putMessage(%s): %v", m.MessageID, err)
				}
			}

			blocked, err := b.blockedGroups(ctx, region, queue, now)
			if err != nil {
				t.Fatalf("blockedGroups: %v", err)
			}
			if !blocked["group-a"] {
				t.Errorf("expected group-a blocked (has invisible message)")
			}
			if blocked["group-b"] {
				t.Errorf("expected group-b NOT blocked (all visible)")
			}
			if len(blocked) != 1 {
				t.Errorf("blockedGroups = %v, want exactly {group-a}", blocked)
			}
		})
	}
}

// TestMessageBackend_VisibleAtBoundary_Parity pins the exact boundary
// semantics of visibility: a message becomes visible AT its VisibleAfter
// instant (not strictly after) — matching Message.IsVisible's
// !clk.Now().Before(m.VisibleAfter) contract — for both receiveCandidates
// and countMessages, on both backends.
func TestMessageBackend_VisibleAtBoundary_Parity(t *testing.T) {
	for name, b := range newTestMessageBackends(t) {
		b := b
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			const region, queue = "us-east-1", "boundary-queue"
			boundary := time.UnixMilli(1_700_000_000_000).UTC()

			msg := newTestMessage("m-1", "", "", boundary)
			if err := b.putMessage(ctx, region, queue, msg); err != nil {
				t.Fatalf("putMessage: %v", err)
			}

			// One millisecond before the boundary: not yet visible.
			before := boundary.Add(-time.Millisecond)
			candidates, err := b.receiveCandidates(ctx, region, queue, before, 10, false)
			if err != nil {
				t.Fatalf("receiveCandidates(before): %v", err)
			}
			if len(candidates) != 0 {
				t.Fatalf("receiveCandidates(before boundary) = %v, want none visible yet", candidates)
			}
			visibleCount, _, err := b.countMessages(ctx, region, queue, before)
			if err != nil {
				t.Fatalf("countMessages(before): %v", err)
			}
			if visibleCount != 0 {
				t.Fatalf("countMessages(before boundary) visible = %d, want 0", visibleCount)
			}

			// Exactly at the boundary: visible.
			candidates, err = b.receiveCandidates(ctx, region, queue, boundary, 10, false)
			if err != nil {
				t.Fatalf("receiveCandidates(at): %v", err)
			}
			if len(candidates) != 1 {
				t.Fatalf("receiveCandidates(at boundary) = %v, want exactly 1 visible", candidates)
			}
			visibleCount, _, err = b.countMessages(ctx, region, queue, boundary)
			if err != nil {
				t.Fatalf("countMessages(at): %v", err)
			}
			if visibleCount != 1 {
				t.Fatalf("countMessages(at boundary) visible = %d, want 1", visibleCount)
			}

			// One millisecond after: still visible.
			after := boundary.Add(time.Millisecond)
			candidates, err = b.receiveCandidates(ctx, region, queue, after, 10, false)
			if err != nil {
				t.Fatalf("receiveCandidates(after): %v", err)
			}
			if len(candidates) != 1 {
				t.Fatalf("receiveCandidates(after boundary) = %v, want exactly 1 visible", candidates)
			}
		})
	}
}

// The sqlMessageBackend-only tests — corrupt-row isolation and restart
// persistence — live in message_backend_sqlite_test.go, which carries
// //go:build !nosqlite because both reach into the SQLite table directly.
