//go:build !nosqlite

package state

// Failing-first coverage for the torn Scan/List read: the pending-write
// overlay has to be snapshotted BEFORE the base source is read, not consulted
// after it.
//
// Reading the base first and merging the live overlay afterwards loses keys.
// A flush that commits between the two halves moves a key out of the overlay
// and into SQLite, but the base result was captured before that commit and
// the overlay no longer holds the key — so a key that exists in the store
// appears in neither half and Scan/List silently omits it. That is not a
// theoretical window: it fires on every TierCached namespace read (s3:objects,
// sqs:messages, lambda:function-code, ecs:tasks, …), which always take the
// SQLite-backed path, and on TierHot namespaces for as long as the startup
// seed is still running. A concurrent reproduction of the pre-fix behavior
// lost tens of thousands of keys in ten seconds.
//
// White-box (package state) because the interleaving has to be exact: the
// tests below drive scanMerged/listMerged with a base-read function that
// commits the flush itself, mid-read, rather than racing a background flush
// ticker and hoping. hybridScanPageMerged already had this ordering right —
// these pin the same invariant for the unpaginated pair.

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// newFlushControlledHybridStore returns a store whose flushes only ever
// happen when a test asks for one: a flush interval far longer than any test
// run, already past the startup seed so flushOnce is no longer a no-op.
func newFlushControlledHybridStore(t *testing.T) *HybridStore {
	t.Helper()
	s, err := NewHybridStore(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	if err := s.WaitReady(context.Background()); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	<-s.loaded // flushOnce defers to the next tick until the seed has finished
	return s
}

func TestHybridStore_ScanMerged_SnapshotsOverlayBeforeBaseRead(t *testing.T) {
	s := newFlushControlledHybridStore(t)
	ctx := context.Background()

	// "s3:objects" is TierCached, so a real Scan of it always takes the
	// SQLite-plus-overlay path this helper implements.
	const namespace, prefix, key = "s3:objects", "b/", "b/obj"
	if err := s.Set(ctx, namespace, key, "v1"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Base read that sees SQLite as it was before the flush, and then lets the
	// flush commit — and empty the overlay — while it is still in flight.
	baseSawEmptySQLite := false
	fetchBase := func(ctx context.Context, ns, p string) ([]KV, error) {
		pairs, err := s.sqliteScan(ctx, ns, p)
		if err != nil {
			return nil, err
		}
		baseSawEmptySQLite = len(pairs) == 0
		if err := s.flush(ctx); err != nil {
			return nil, err
		}
		return pairs, nil
	}

	got, err := s.scanMerged(ctx, namespace, prefix, fetchBase)
	if err != nil {
		t.Fatalf("scanMerged: %v", err)
	}
	if !baseSawEmptySQLite {
		t.Fatal("test setup: the base read already saw the flushed row, so the torn window was never opened")
	}
	if len(got) != 1 || got[0].Key != key || got[0].Value != "v1" {
		t.Fatalf("scanMerged across a mid-read flush = %+v, want exactly {%s: v1} — the key was written, flushed and never deleted", got, key)
	}
}

func TestHybridStore_ListMerged_SnapshotsOverlayBeforeBaseRead(t *testing.T) {
	s := newFlushControlledHybridStore(t)
	ctx := context.Background()

	const namespace, prefix, key = "s3:objects", "b/", "b/obj"
	if err := s.Set(ctx, namespace, key, "v1"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	fetchBase := func(ctx context.Context, ns, p string) ([]string, error) {
		keys, err := s.sqliteList(ctx, ns, p)
		if err != nil {
			return nil, err
		}
		if err := s.flush(ctx); err != nil {
			return nil, err
		}
		return keys, nil
	}

	got, err := s.listMerged(ctx, namespace, prefix, fetchBase)
	if err != nil {
		t.Fatalf("listMerged: %v", err)
	}
	if len(got) != 1 || got[0] != key {
		t.Fatalf("listMerged across a mid-read flush = %v, want exactly [%s]", got, key)
	}
}

// TestHybridStore_ScanAndList_HonourShorterPrefixTombstone guards the
// narrowing in snapshotOverlayLocked: the dirty/flushing entries it copies are
// filtered to namespace+prefix, but prefix tombstones deliberately are not.
// A tombstone shadows the keys UNDER its prefix, so one recorded at "a/"
// covers a scan of "a/b/" — filtering tombstones the same way as keys would
// drop the deletion and resurrect every key beneath it.
func TestHybridStore_ScanAndList_HonourShorterPrefixTombstone(t *testing.T) {
	s := newFlushControlledHybridStore(t)
	ctx := context.Background()

	const namespace = "s3:objects"
	for i := 0; i < 5; i++ {
		if err := s.Set(ctx, namespace, fmt.Sprintf("a/b/k-%d", i), "v"); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}
	if err := s.flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if err := s.DeletePrefix(ctx, namespace, "a/"); err != nil {
		t.Fatalf("DeletePrefix: %v", err)
	}

	pairs, err := s.Scan(ctx, namespace, "a/b/")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(pairs) != 0 {
		t.Fatalf("Scan(%q) after DeletePrefix(%q) = %+v, want empty", "a/b/", "a/", pairs)
	}

	keys, err := s.List(ctx, namespace, "a/b/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("List(%q) after DeletePrefix(%q) = %v, want empty", "a/b/", "a/", keys)
	}
}

// TestHybridStore_ScanMerged_MidReadFlushOfADeleteStaysDeleted is the other
// direction: a key deleted before the read must not reappear when the flush
// of that tombstone lands mid-read. The overlay snapshot no longer carries
// the tombstone by the time the merge runs, but the base read — taken before
// the commit — still returns the row, so the merge has to be able to lose it
// for the right reason as well as keep it for the right reason.
func TestHybridStore_ScanMerged_MidReadFlushOfADeleteStaysDeleted(t *testing.T) {
	s := newFlushControlledHybridStore(t)
	ctx := context.Background()

	const namespace, prefix, key = "s3:objects", "b/", "b/obj"
	if err := s.Set(ctx, namespace, key, "v1"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.flush(ctx); err != nil {
		t.Fatalf("flush the Set: %v", err)
	}
	if err := s.Delete(ctx, namespace, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	fetchBase := func(ctx context.Context, ns, p string) ([]KV, error) {
		pairs, err := s.sqliteScan(ctx, ns, p) // still has the row: the delete is unflushed
		if err != nil {
			return nil, err
		}
		if err := s.flush(ctx); err != nil {
			return nil, err
		}
		return pairs, nil
	}

	got, err := s.scanMerged(ctx, namespace, prefix, fetchBase)
	if err != nil {
		t.Fatalf("scanMerged: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("scanMerged after a deleted key's tombstone flushed mid-read = %+v, want empty", got)
	}
}
