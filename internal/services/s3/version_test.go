package s3

// Unit tests for the version keyspace and the version-aware half of the
// lifecycle sweeper.
//
// They live here rather than in tests/integration/s3 for the reason the
// lifecycle unit tests do: a mock clock is shared by every service on a test
// server, so advancing it by days there fires every one-second ticker in the
// emulator tens of thousands of times. Day-scale noncurrent arithmetic costs
// milliseconds against a handler that owns its clock.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/state"
)

// ---- Fixtures --------------------------------------------------------------

func seedBucket(t *testing.T, h *Handler, ctx context.Context, name, status string) *Bucket {
	t.Helper()
	b := &Bucket{Name: name, Region: "us-east-1", VersioningStatus: status, VersionHistoryReady: true}
	if aerr := h.store.putBucket(ctx, b); aerr != nil {
		t.Fatalf("seed bucket %s: %v", name, aerr)
	}
	return b
}

// seedVersion writes one version record, and also the current-version record
// when current is true. Bodies are not involved: the sweeper only reads
// metadata.
func seedVersion(t *testing.T, h *Handler, ctx context.Context, obj *Object, current bool) {
	t.Helper()
	if obj.Seq == "" {
		obj.Seq = newVersionStamp(obj.LastModified).sortToken()
	}
	if obj.VersionID == "" {
		obj.VersionID = obj.Seq
	}
	if aerr := h.store.putVersion(ctx, obj); aerr != nil {
		t.Fatalf("seed version %s/%s: %v", obj.Bucket, obj.Key, aerr)
	}
	if current {
		if aerr := h.store.putObjectMeta(ctx, obj); aerr != nil {
			t.Fatalf("seed current %s/%s: %v", obj.Bucket, obj.Key, aerr)
		}
	}
}

// seedHistory writes a key's versions newest-first, mirroring the order the
// scan returns them in, and makes the first one current.
func seedHistory(t *testing.T, h *Handler, ctx context.Context, objs ...*Object) {
	t.Helper()
	for i, obj := range objs {
		seedVersion(t, h, ctx, obj, i == 0)
	}
}

func versionCount(t *testing.T, h *Handler, ctx context.Context, bucket, key string) int {
	t.Helper()
	versions, aerr := h.store.listKeyVersions(ctx, bucket, key)
	if aerr != nil {
		t.Fatalf("list versions %s/%s: %v", bucket, key, aerr)
	}
	return len(versions)
}

// noncurrentRule builds a rule that expires noncurrent versions after days,
// optionally retaining newer ones.
func noncurrentRule(id string, days int, retain *int) LifecycleRule {
	rule := enabledRule(id, "")
	rule.NoncurrentVersionExpiration = &LifecycleNoncurrentVersionExpiration{
		NoncurrentDays:          days,
		NewerNoncurrentVersions: retain,
	}
	return rule
}

// ---- Version ids -----------------------------------------------------------

// TestVersionStamp_sortTokenOrdersNewestFirst is the property the whole
// keyspace rests on: a plain lexicographic scan of s3:versions has to produce
// AWS's "most recently stored first" order without any sorting.
func TestVersionStamp_sortTokenOrdersNewestFirst(t *testing.T) {
	base := mustTime(t, "2026-03-01T00:00:00Z")

	// Given: three versions minted at increasing times
	tokens := []string{
		newVersionStamp(base).sortToken(),
		newVersionStamp(base.Add(time.Second)).sortToken(),
		newVersionStamp(base.Add(2 * time.Second)).sortToken(),
	}

	// Then: the newest sorts first
	if !(tokens[2] < tokens[1] && tokens[1] < tokens[0]) {
		t.Errorf("tokens do not descend with recency: %q", tokens)
	}
	// And: they are the fixed width the storage key layout assumes
	for _, tok := range tokens {
		if len(tok) != 24 {
			t.Errorf("token %q has length %d, want 24", tok, len(tok))
		}
	}
}

// TestVersionStamp_sortTokenBreaksClockTies matters because the mock clock does
// not advance between requests: without the counter, two writes in one test
// would be indistinguishable and their order undefined.
func TestVersionStamp_sortTokenBreaksClockTies(t *testing.T) {
	// Given: two versions minted at the very same instant
	at := mustTime(t, "2026-03-01T00:00:00Z")
	first := newVersionStamp(at).sortToken()
	second := newVersionStamp(at).sortToken()

	// Then: the later mint still sorts first
	if second >= first {
		t.Errorf("tie not broken by recency: first=%q second=%q", first, second)
	}
}

// TestVersionStamp_sequencerIncreases is the opposite direction: AWS documents
// the notification sequencer as increasing with event order, which is what a
// consumer compares.
func TestVersionStamp_sequencerIncreases(t *testing.T) {
	base := mustTime(t, "2026-03-01T00:00:00Z")
	first := newVersionStamp(base).sequencer()
	second := newVersionStamp(base.Add(time.Second)).sequencer()

	if len(first) != len(second) {
		t.Fatalf("sequencers have different widths (%d vs %d), which breaks the documented comparison", len(first), len(second))
	}
	if second <= first {
		t.Errorf("sequencer did not increase: %q then %q", first, second)
	}
}

// TestLegacyVersionStamp_isDeterministic is what makes the backfill re-runnable:
// the token an object gets is a pure function of data the migration never
// changes.
func TestLegacyVersionStamp_isDeterministic(t *testing.T) {
	at := mustTime(t, "2026-03-01T12:00:00Z")
	first := legacyVersionStamp(at).sortToken()
	// A second derivation of the same object, as a re-run of the backfill does.
	again := legacyVersionStamp(at.In(time.FixedZone("local", 3600))).sortToken()
	if first != again {
		t.Errorf("legacy stamp is not stable across calls: %q then %q", first, again)
	}
	// And: a minted token for the same instant still sorts ahead of it, because
	// anything minted was stored later than the object being migrated.
	if newVersionStamp(at).sortToken() >= legacyVersionStamp(at).sortToken() {
		t.Error("a minted version does not sort ahead of a legacy one at the same instant")
	}
}

// ---- Backfill --------------------------------------------------------------

// TestEnsureVersionHistory_migratesAndIsRerunSafe covers what happens to objects
// persisted before this bucket had version history: they become the null
// version of their key, and running the migration again changes nothing.
func TestEnsureVersionHistory_migratesAndIsRerunSafe(t *testing.T) {
	h, _, ctx := newLifecycleTestHandler(t)

	// Given: a versioned bucket holding an object written by an older build —
	// no version id, no sort token, no version record
	b := seedBucket(t, h, ctx, "legacy", versioningEnabled)
	b.VersionHistoryReady = false
	if aerr := h.store.putBucket(ctx, b); aerr != nil {
		t.Fatal(aerr)
	}
	seedObject(t, h, ctx, &Object{
		Bucket: "legacy", Key: "a.txt", ContentLength: 3,
		LastModified: mustTime(t, "2026-01-01T09:00:00Z"),
	})

	// When: version history is prepared
	if aerr := h.ensureVersionHistory(ctx, b); aerr != nil {
		t.Fatalf("ensureVersionHistory: %v", aerr)
	}

	// Then: the object is the key's null version
	versions, aerr := h.store.listKeyVersions(ctx, "legacy", "a.txt")
	if aerr != nil {
		t.Fatal(aerr)
	}
	if len(versions) != 1 {
		t.Fatalf("want 1 version, got %d", len(versions))
	}
	if versions[0].wireVersionID() != nullVersionID {
		t.Errorf("migrated version id = %q, want null", versions[0].wireVersionID())
	}
	firstSeq := versions[0].Seq
	if firstSeq == "" {
		t.Error("migrated version has no sort token")
	}

	// And: the current-version record now agrees with it
	current, aerr := h.store.getObjectMeta(ctx, "legacy", "a.txt")
	if aerr != nil {
		t.Fatal(aerr)
	}
	if current.Seq != firstSeq {
		t.Errorf("current record token = %q, want %q", current.Seq, firstSeq)
	}

	// When: it runs again — including against a bucket whose completion flag
	// was lost, which is the interrupted-migration case
	b.VersionHistoryReady = false
	if aerr := h.ensureVersionHistory(ctx, b); aerr != nil {
		t.Fatalf("second ensureVersionHistory: %v", aerr)
	}

	// Then: nothing is duplicated and nothing moves
	versions, aerr = h.store.listKeyVersions(ctx, "legacy", "a.txt")
	if aerr != nil {
		t.Fatal(aerr)
	}
	if len(versions) != 1 || versions[0].Seq != firstSeq {
		t.Errorf("re-run changed the history: %d versions, token %q (was %q)",
			len(versions), versions[0].Seq, firstSeq)
	}
}

// ---- Malformed persisted records -------------------------------------------

// TestListKeyVersions_isolatesAMalformedRecord is AGENTS.md's
// malformed-persisted-state rule for the new namespace: one unreadable version
// must not take the key's other versions — or the bucket — down with it.
func TestListKeyVersions_isolatesAMalformedRecord(t *testing.T) {
	h, _, ctx := newLifecycleTestHandler(t)
	seedBucket(t, h, ctx, "corrupt", versioningEnabled)

	// Given: a key with two good versions and one record that will not decode
	seedHistory(t, h, ctx,
		&Object{Bucket: "corrupt", Key: "a.txt", LastModified: mustTime(t, "2026-01-02T00:00:00Z")},
		&Object{Bucket: "corrupt", Key: "a.txt", LastModified: mustTime(t, "2026-01-01T00:00:00Z")},
	)
	if err := h.store.store.Set(ctx, nsVersions,
		versionStoreKey("corrupt", "a.txt", "0000000000000000000000ZZ"), "{not json"); err != nil {
		t.Fatal(err)
	}

	// When: the key's versions are listed
	versions, aerr := h.store.listKeyVersions(ctx, "corrupt", "a.txt")

	// Then: the two readable versions come back, with no error
	if aerr != nil {
		t.Fatalf("a malformed record failed the whole list: %v", aerr)
	}
	if len(versions) != 2 {
		t.Errorf("want 2 readable versions, got %d", len(versions))
	}
}

// TestDecodeVersionPairs_recoversATokenlessRecord covers the subtler corruption:
// a record that decodes but has lost the sort token it is addressed by. It is
// recovered from the storage key rather than emitted as an entry no caller
// could page past or delete.
func TestDecodeVersionPairs_recoversATokenlessRecord(t *testing.T) {
	raw, err := json.Marshal(&Object{Bucket: "b", Key: "a.txt"})
	if err != nil {
		t.Fatal(err)
	}

	got := decodeVersionPairs([]state.KV{{Key: versionStoreKey("b", "a.txt", "TOKEN"), Value: string(raw)}})

	if len(got) != 1 {
		t.Fatalf("want 1 record, got %d", len(got))
	}
	if got[0].Seq != "TOKEN" {
		t.Errorf("Seq = %q, want the token from the storage key", got[0].Seq)
	}
}

// ---- Noncurrent expiration -------------------------------------------------

func TestSweepLifecycle_expiresNoncurrentVersions(t *testing.T) {
	h, mock, ctx := newLifecycleTestHandler(t)
	seedBucket(t, h, ctx, "nc", versioningEnabled)

	// Given: a current version written today over one that became noncurrent
	// ten days ago, and a rule that expires noncurrent versions after 3 days
	mock.Set(mustTime(t, "2026-03-11T00:00:00Z"))
	seedHistory(t, h, ctx,
		&Object{Bucket: "nc", Key: "a.txt", ContentLength: 1, LastModified: mustTime(t, "2026-03-01T00:00:00Z")},
		&Object{Bucket: "nc", Key: "a.txt", ContentLength: 1, LastModified: mustTime(t, "2026-02-01T00:00:00Z")},
	)
	seedLifecycle(t, h, ctx, "nc", &LifecycleConfiguration{Rules: []LifecycleRule{noncurrentRule("nc", 3, nil)}})

	// When: the sweeper runs
	h.sweepLifecycle(ctx)

	// Then: only the noncurrent version is gone
	if got := versionCount(t, h, ctx, "nc", "a.txt"); got != 1 {
		t.Errorf("want 1 surviving version, got %d", got)
	}
	if !objectExists(t, h, ctx, "nc", "a.txt") {
		t.Error("the current version was expired by a noncurrent rule")
	}
}

// TestSweepLifecycle_noncurrentClockStartsAtTheSuccessor pins the AWS
// definition: a version's noncurrent age is measured from when the version that
// replaced it was written, not from when the version itself was.
func TestSweepLifecycle_noncurrentClockStartsAtTheSuccessor(t *testing.T) {
	h, mock, ctx := newLifecycleTestHandler(t)
	seedBucket(t, h, ctx, "nc-clock", versioningEnabled)

	// Given: an ancient version that only became noncurrent yesterday
	mock.Set(mustTime(t, "2026-03-11T00:00:00Z"))
	seedHistory(t, h, ctx,
		&Object{Bucket: "nc-clock", Key: "a.txt", ContentLength: 1, LastModified: mustTime(t, "2026-03-10T00:00:00Z")},
		&Object{Bucket: "nc-clock", Key: "a.txt", ContentLength: 1, LastModified: mustTime(t, "2020-01-01T00:00:00Z")},
	)
	seedLifecycle(t, h, ctx, "nc-clock", &LifecycleConfiguration{Rules: []LifecycleRule{noncurrentRule("nc", 3, nil)}})

	// When: the sweeper runs
	h.sweepLifecycle(ctx)

	// Then: it survives — its own age is irrelevant
	if got := versionCount(t, h, ctx, "nc-clock", "a.txt"); got != 2 {
		t.Errorf("want both versions, got %d — the noncurrent clock used the wrong start", got)
	}
}

func TestSweepLifecycle_newerNoncurrentVersionsRetainsThatMany(t *testing.T) {
	h, mock, ctx := newLifecycleTestHandler(t)
	seedBucket(t, h, ctx, "nc-retain", versioningEnabled)

	// Given: a current version and three long-noncurrent ones, with a rule
	// retaining the two newest noncurrent versions
	mock.Set(mustTime(t, "2026-03-11T00:00:00Z"))
	seedHistory(t, h, ctx,
		&Object{Bucket: "nc-retain", Key: "a.txt", ContentLength: 1, LastModified: mustTime(t, "2026-01-04T00:00:00Z")},
		&Object{Bucket: "nc-retain", Key: "a.txt", ContentLength: 1, LastModified: mustTime(t, "2026-01-03T00:00:00Z")},
		&Object{Bucket: "nc-retain", Key: "a.txt", ContentLength: 1, LastModified: mustTime(t, "2026-01-02T00:00:00Z")},
		&Object{Bucket: "nc-retain", Key: "a.txt", ContentLength: 1, LastModified: mustTime(t, "2026-01-01T00:00:00Z")},
	)
	retain := 2
	rule := noncurrentRule("nc", 3, &retain)
	rule.Filter = &LifecycleFilter{Prefix: ""}
	seedLifecycle(t, h, ctx, "nc-retain", &LifecycleConfiguration{Rules: []LifecycleRule{rule}})

	// When: the sweeper runs
	h.sweepLifecycle(ctx)

	// Then: the current version plus the two retained noncurrent ones remain
	if got := versionCount(t, h, ctx, "nc-retain", "a.txt"); got != 3 {
		t.Errorf("want 3 versions (current + 2 retained), got %d", got)
	}
}

// ---- Noncurrent transition -------------------------------------------------

func TestSweepLifecycle_transitionsNoncurrentVersions(t *testing.T) {
	h, mock, ctx := newLifecycleTestHandler(t)
	seedBucket(t, h, ctx, "nc-trans", versioningEnabled)

	mock.Set(mustTime(t, "2026-03-11T00:00:00Z"))
	seedHistory(t, h, ctx,
		&Object{Bucket: "nc-trans", Key: "a.txt", ContentLength: 1 << 20, LastModified: mustTime(t, "2026-03-01T00:00:00Z")},
		&Object{Bucket: "nc-trans", Key: "a.txt", ContentLength: 1 << 20, LastModified: mustTime(t, "2026-02-01T00:00:00Z")},
	)
	rule := enabledRule("nc", "")
	rule.NoncurrentVersionTransitions = []LifecycleNoncurrentVersionTransition{
		{NoncurrentDays: 3, StorageClass: "GLACIER"},
	}
	seedLifecycle(t, h, ctx, "nc-trans", &LifecycleConfiguration{Rules: []LifecycleRule{rule}})

	// When: the sweeper runs
	h.sweepLifecycle(ctx)

	// Then: the noncurrent version carries the class and is still there; the
	// current one is untouched
	versions, aerr := h.store.listKeyVersions(ctx, "nc-trans", "a.txt")
	if aerr != nil {
		t.Fatal(aerr)
	}
	if len(versions) != 2 {
		t.Fatalf("want 2 versions, got %d", len(versions))
	}
	if versions[1].effectiveStorageClass() != "GLACIER" {
		t.Errorf("noncurrent storage class = %q, want GLACIER", versions[1].effectiveStorageClass())
	}
	if versions[0].effectiveStorageClass() != storageClassStandard {
		t.Errorf("current storage class = %q, want STANDARD", versions[0].effectiveStorageClass())
	}
}

// TestSweepLifecycle_noncurrentTransitionRespectsTheDefaultMinimumSize is the
// composition test with the bucket's transition default minimum object size:
// the gate applies to noncurrent transitions exactly as it does to current
// ones, rather than being bypassed by the new code path.
func TestSweepLifecycle_noncurrentTransitionRespectsTheDefaultMinimumSize(t *testing.T) {
	cases := []struct {
		name      string
		minimum   string
		class     string
		sizeFiler bool
		want      string
	}{
		{name: "small object blocked by default", minimum: "", class: "STANDARD_IA", want: storageClassStandard},
		{name: "varies_by_storage_class lets Glacier through", minimum: transitionDefaultMinimumVaries, class: "GLACIER", want: "GLACIER"},
		{name: "varies_by_storage_class still blocks other classes", minimum: transitionDefaultMinimumVaries, class: "STANDARD_IA", want: storageClassStandard},
		{name: "an explicit size filter overrides the minimum", minimum: "", class: "STANDARD_IA", sizeFiler: true, want: "STANDARD_IA"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, mock, ctx := newLifecycleTestHandler(t)
			seedBucket(t, h, ctx, "nc-size", versioningEnabled)

			// Given: a noncurrent version well under the 128 KB default minimum
			mock.Set(mustTime(t, "2026-03-11T00:00:00Z"))
			seedHistory(t, h, ctx,
				&Object{Bucket: "nc-size", Key: "a.txt", ContentLength: 10, LastModified: mustTime(t, "2026-03-01T00:00:00Z")},
				&Object{Bucket: "nc-size", Key: "a.txt", ContentLength: 10, LastModified: mustTime(t, "2026-02-01T00:00:00Z")},
			)
			rule := enabledRule("nc", "")
			if tc.sizeFiler {
				greater := int64(1)
				rule.Filter = &LifecycleFilter{ObjectSizeGreaterThan: &greater}
			}
			rule.NoncurrentVersionTransitions = []LifecycleNoncurrentVersionTransition{
				{NoncurrentDays: 3, StorageClass: tc.class},
			}
			seedLifecycle(t, h, ctx, "nc-size", &LifecycleConfiguration{
				Rules:                              []LifecycleRule{rule},
				TransitionDefaultMinimumObjectSize: tc.minimum,
			})

			// When: the sweeper runs
			h.sweepLifecycle(ctx)

			// Then: the size gate decides, exactly as it does for a current
			// version
			versions, aerr := h.store.listKeyVersions(ctx, "nc-size", "a.txt")
			if aerr != nil {
				t.Fatal(aerr)
			}
			if got := versions[1].effectiveStorageClass(); got != tc.want {
				t.Errorf("noncurrent storage class = %q, want %q", got, tc.want)
			}
		})
	}
}

// ---- Current-version expiry on a versioned bucket --------------------------

// TestSweepLifecycle_expiringACurrentVersionAddsADeleteMarker is the behaviour
// that makes an Expiration rule mean something different on a versioned bucket:
// nothing is removed.
func TestSweepLifecycle_expiringACurrentVersionAddsADeleteMarker(t *testing.T) {
	h, mock, ctx := newLifecycleTestHandler(t)
	seedBucket(t, h, ctx, "cur-exp", versioningEnabled)

	mock.Set(mustTime(t, "2026-03-11T00:00:00Z"))
	seedHistory(t, h, ctx, &Object{
		Bucket: "cur-exp", Key: "a.txt", ContentLength: 1,
		LastModified: mustTime(t, "2026-03-01T00:00:00Z"),
	})
	rule := enabledRule("exp", "")
	rule.Expiration = &LifecycleExpiration{Days: 1}
	seedLifecycle(t, h, ctx, "cur-exp", &LifecycleConfiguration{Rules: []LifecycleRule{rule}})

	// When: the sweeper runs
	h.sweepLifecycle(ctx)

	// Then: a delete marker is now current, and the object is still stored
	// beneath it
	versions, aerr := h.store.listKeyVersions(ctx, "cur-exp", "a.txt")
	if aerr != nil {
		t.Fatal(aerr)
	}
	if len(versions) != 2 {
		t.Fatalf("want 2 versions (marker + original), got %d", len(versions))
	}
	if !versions[0].DeleteMarker {
		t.Error("the newest version is not a delete marker")
	}
	if versions[1].DeleteMarker {
		t.Error("the original version was replaced rather than hidden")
	}
	current, aerr := h.store.getObjectMeta(ctx, "cur-exp", "a.txt")
	if aerr != nil {
		t.Fatalf("the key's current record was removed: %v", aerr)
	}
	if !current.DeleteMarker {
		t.Error("the current record is not the delete marker")
	}
}

// TestSweepLifecycle_unversionedExpiryStillDeletes is the regression guard for
// the split: the bucket-less and never-versioned paths must keep deleting
// outright rather than growing tombstones.
func TestSweepLifecycle_unversionedExpiryStillDeletes(t *testing.T) {
	h, mock, ctx := newLifecycleTestHandler(t)
	seedBucket(t, h, ctx, "plain", "")

	mock.Set(mustTime(t, "2026-03-11T00:00:00Z"))
	seedObject(t, h, ctx, &Object{
		Bucket: "plain", Key: "a.txt", ContentLength: 1,
		LastModified: mustTime(t, "2026-03-01T00:00:00Z"),
	})
	rule := enabledRule("exp", "")
	rule.Expiration = &LifecycleExpiration{Days: 1}
	seedLifecycle(t, h, ctx, "plain", &LifecycleConfiguration{Rules: []LifecycleRule{rule}})

	h.sweepLifecycle(ctx)

	if objectExists(t, h, ctx, "plain", "a.txt") {
		t.Error("an unversioned object was not deleted by its expiration rule")
	}
	if got := versionCount(t, h, ctx, "plain", "a.txt"); got != 0 {
		t.Errorf("an unversioned bucket grew %d version records", got)
	}
}

// ---- ExpiredObjectDeleteMarker ---------------------------------------------

func TestSweepLifecycle_expiredObjectDeleteMarkerRemovesALoneMarker(t *testing.T) {
	h, mock, ctx := newLifecycleTestHandler(t)
	seedBucket(t, h, ctx, "eodm", versioningEnabled)

	// Given: a key whose only version is a delete marker
	mock.Set(mustTime(t, "2026-03-11T00:00:00Z"))
	seedHistory(t, h, ctx, &Object{
		Bucket: "eodm", Key: "a.txt", DeleteMarker: true,
		LastModified: mustTime(t, "2026-03-01T00:00:00Z"),
	})
	rule := enabledRule("tidy", "")
	rule.Expiration = &LifecycleExpiration{ExpiredObjectDeleteMarker: true}
	seedLifecycle(t, h, ctx, "eodm", &LifecycleConfiguration{Rules: []LifecycleRule{rule}})

	// When: the sweeper runs
	h.sweepLifecycle(ctx)

	// Then: the key is gone entirely
	if got := versionCount(t, h, ctx, "eodm", "a.txt"); got != 0 {
		t.Errorf("want the marker removed, got %d versions", got)
	}
	if objectExists(t, h, ctx, "eodm", "a.txt") {
		t.Error("the key's current record outlived its only version")
	}
}

func TestSweepLifecycle_expiredObjectDeleteMarkerKeepsAMarkerHidingVersions(t *testing.T) {
	h, mock, ctx := newLifecycleTestHandler(t)
	seedBucket(t, h, ctx, "eodm-keep", versioningEnabled)

	// Given: a delete marker with a version still under it
	mock.Set(mustTime(t, "2026-03-11T00:00:00Z"))
	seedHistory(t, h, ctx,
		&Object{Bucket: "eodm-keep", Key: "a.txt", DeleteMarker: true, LastModified: mustTime(t, "2026-03-01T00:00:00Z")},
		&Object{Bucket: "eodm-keep", Key: "a.txt", ContentLength: 1, LastModified: mustTime(t, "2026-02-01T00:00:00Z")},
	)
	rule := enabledRule("tidy", "")
	rule.Expiration = &LifecycleExpiration{ExpiredObjectDeleteMarker: true}
	seedLifecycle(t, h, ctx, "eodm-keep", &LifecycleConfiguration{Rules: []LifecycleRule{rule}})

	// When: the sweeper runs
	h.sweepLifecycle(ctx)

	// Then: nothing is removed — the action only clears a marker that hides
	// nothing
	if got := versionCount(t, h, ctx, "eodm-keep", "a.txt"); got != 2 {
		t.Errorf("want both versions kept, got %d", got)
	}
}

// TestSweepLifecycle_expiredObjectDeleteMarkerAfterNoncurrentExpiry is the
// combination AWS documents the action for: the versions under a marker expire
// away, and the marker is cleared on a later pass.
func TestSweepLifecycle_expiredObjectDeleteMarkerAfterNoncurrentExpiry(t *testing.T) {
	h, mock, ctx := newLifecycleTestHandler(t)
	seedBucket(t, h, ctx, "eodm-combo", versioningEnabled)

	mock.Set(mustTime(t, "2026-03-11T00:00:00Z"))
	seedHistory(t, h, ctx,
		&Object{Bucket: "eodm-combo", Key: "a.txt", DeleteMarker: true, LastModified: mustTime(t, "2026-03-01T00:00:00Z")},
		&Object{Bucket: "eodm-combo", Key: "a.txt", ContentLength: 1, LastModified: mustTime(t, "2026-02-01T00:00:00Z")},
	)
	tidy := enabledRule("tidy", "")
	tidy.Expiration = &LifecycleExpiration{ExpiredObjectDeleteMarker: true}
	seedLifecycle(t, h, ctx, "eodm-combo", &LifecycleConfiguration{
		Rules: []LifecycleRule{noncurrentRule("nc", 3, nil), tidy},
	})

	// When: the sweeper runs twice — the first pass expires the hidden version,
	// the second finds a marker hiding nothing
	h.sweepLifecycle(ctx)
	if got := versionCount(t, h, ctx, "eodm-combo", "a.txt"); got != 1 {
		t.Fatalf("after the first sweep want just the marker, got %d versions", got)
	}
	h.sweepLifecycle(ctx)

	// Then: the key is gone
	if got := versionCount(t, h, ctx, "eodm-combo", "a.txt"); got != 0 {
		t.Errorf("want the key fully removed, got %d versions", got)
	}
}

// ---- Paging ----------------------------------------------------------------

// TestSweepBucketVersions_groupsKeysAcrossPages guards the carry-over: a key
// whose versions straddle an internal page boundary must still be evaluated as
// one history, or its noncurrent versions look current.
func TestSweepBucketVersions_groupsKeysAcrossPages(t *testing.T) {
	h, mock, ctx := newLifecycleTestHandler(t)
	b := seedBucket(t, h, ctx, "paged", versioningEnabled)

	// Given: more versions of one key than a single sweep page holds
	mock.Set(mustTime(t, "2026-03-11T00:00:00Z"))
	total := lifecycleSweepPageSize + 5
	for i := 0; i < total; i++ {
		seedVersion(t, h, ctx, &Object{
			Bucket: "paged", Key: "a.txt", ContentLength: 1,
			LastModified: mustTime(t, "2026-03-01T00:00:00Z").Add(-time.Duration(i) * time.Hour),
		}, i == 0)
	}
	cfg := &LifecycleConfiguration{Rules: []LifecycleRule{noncurrentRule("nc", 3, nil)}}

	// When: the versioned sweep runs
	h.sweepBucketVersions(ctx, b, cfg, mock.Now().UTC())

	// Then: every noncurrent version went, and the current one stayed
	if got := versionCount(t, h, ctx, "paged", "a.txt"); got != 1 {
		t.Errorf("want 1 surviving version, got %d", got)
	}
}
