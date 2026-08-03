package s3

// Unit tests for the lifecycle sweeper and its rule evaluation.
//
// The day-scale time semantics live here rather than in
// tests/integration/s3 on purpose. A mock clock is shared by every service on
// a test server, so advancing it by a day there makes every one-second ticker
// in the emulator fire 86,400 times; the same assertions cost milliseconds
// against a handler that owns its clock. The integration suite keeps the HTTP
// contract and one end-to-end sweep that proves the ticker is wired up.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// ---- Fixtures --------------------------------------------------------------

// newLifecycleTestHandler returns a handler backed by a fresh memory store and
// a mock clock, with the sweeper running against a context the test cancels.
func newLifecycleTestHandler(t *testing.T) (*Handler, *clock.Mock, context.Context) {
	t.Helper()
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000", DataDir: t.TempDir()}
	log := serviceutil.NewServiceLogger(zap.NewNop(), "s3")
	mock := clock.NewMock()
	h := newHandler(cfg, state.NewMemoryStore(), log, mock, events.NewBus())

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return h, mock, ctx
}

// seedObject writes an object's metadata straight to the store. Bodies are not
// involved: the sweeper only ever reads metadata and deletes.
func seedObject(t *testing.T, h *Handler, ctx context.Context, obj *Object) {
	t.Helper()
	raw, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal object %s/%s: %v", obj.Bucket, obj.Key, err)
	}
	if err := h.store.store.Set(ctx, nsObjects, objectStoreKey(obj.Bucket, obj.Key), string(raw)); err != nil {
		t.Fatalf("seed object %s/%s: %v", obj.Bucket, obj.Key, err)
	}
}

func seedLifecycle(t *testing.T, h *Handler, ctx context.Context, bucket string, cfg *LifecycleConfiguration) {
	t.Helper()
	if aerr := h.store.putLifecycle(ctx, bucket, cfg); aerr != nil {
		t.Fatalf("seed lifecycle for %s: %v", bucket, aerr)
	}
}

func objectExists(t *testing.T, h *Handler, ctx context.Context, bucket, key string) bool {
	t.Helper()
	_, aerr := h.store.getObjectMeta(ctx, bucket, key)
	return aerr == nil
}

func enabledRule(id string, prefix string) LifecycleRule {
	return LifecycleRule{ID: id, Status: lifecycleStatusEnabled, Filter: &LifecycleFilter{Prefix: prefix}}
}

// ---- Expiry arithmetic -----------------------------------------------------

func TestLifecycleRule_expiresAt_roundsToNextUTCMidnight(t *testing.T) {
	// Given: objects written at various times of day under a Days rule
	cases := []struct {
		name         string
		lastModified string
		days         int
		want         string
	}{
		{
			name:         "mid-afternoon write",
			lastModified: "2026-03-01T15:04:05Z",
			days:         1,
			// +1 day lands on 2026-03-02T15:04:05Z, which rounds up to the
			// following UTC midnight.
			want: "2026-03-03T00:00:00Z",
		},
		{
			name:         "write exactly at midnight",
			lastModified: "2026-03-01T00:00:00Z",
			days:         1,
			want:         "2026-03-03T00:00:00Z",
		},
		{
			name:         "thirty day rule",
			lastModified: "2026-03-01T23:59:59Z",
			days:         30,
			want:         "2026-04-01T00:00:00Z",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// When: the rule's expiry is computed
			obj := &Object{LastModified: mustTime(t, tc.lastModified)}
			rule := LifecycleRule{Expiration: &LifecycleExpiration{Days: tc.days}}
			got, ok := rule.expiresAt(obj)

			// Then: it matches AWS's next-UTC-midnight rounding
			if !ok {
				t.Fatal("expiresAt reported no expiry for a Days rule")
			}
			if want := mustTime(t, tc.want); !got.Equal(want) {
				t.Errorf("expiresAt = %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
			}
		})
	}
}

func TestLifecycleRule_expiresAt_dateRuleIsNotRounded(t *testing.T) {
	// Given: a Date rule (validation has already pinned it to UTC midnight)
	date := mustTime(t, "2026-06-01T00:00:00Z")
	rule := LifecycleRule{Expiration: &LifecycleExpiration{Date: &date}}

	// When: the expiry is computed for an object written long before
	got, ok := rule.expiresAt(&Object{LastModified: mustTime(t, "2020-01-01T09:00:00Z")})

	// Then: it is the date itself, independent of the object's age
	if !ok || !got.Equal(date) {
		t.Errorf("expiresAt = %s (%v), want %s", got.Format(time.RFC3339), ok, date.Format(time.RFC3339))
	}
}

// ---- Filter evaluation -----------------------------------------------------

func TestLifecycleRule_matches(t *testing.T) {
	obj := &Object{
		Key:           "data/report.csv",
		ContentLength: 100,
		Tags:          map[string]string{"temp": "yes", "team": "core"},
	}
	sizeGT := int64(10)
	sizeLT := int64(50)

	cases := []struct {
		name string
		rule LifecycleRule
		want bool
	}{
		{
			name: "empty filter matches every object",
			rule: LifecycleRule{Filter: &LifecycleFilter{}},
			want: true,
		},
		{
			name: "matching prefix",
			rule: LifecycleRule{Filter: &LifecycleFilter{Prefix: "data/"}},
			want: true,
		},
		{
			name: "non-matching prefix",
			rule: LifecycleRule{Filter: &LifecycleFilter{Prefix: "logs/"}},
			want: false,
		},
		{
			name: "legacy prefix form",
			rule: LifecycleRule{Prefix: strPtr("data/")},
			want: true,
		},
		{
			name: "matching tag",
			rule: LifecycleRule{Filter: &LifecycleFilter{Tag: &LifecycleTag{Key: "temp", Value: "yes"}}},
			want: true,
		},
		{
			name: "tag with the wrong value",
			rule: LifecycleRule{Filter: &LifecycleFilter{Tag: &LifecycleTag{Key: "temp", Value: "no"}}},
			want: false,
		},
		{
			name: "size above the lower bound",
			rule: LifecycleRule{Filter: &LifecycleFilter{ObjectSizeGreaterThan: &sizeGT}},
			want: true,
		},
		{
			name: "size not below the upper bound",
			rule: LifecycleRule{Filter: &LifecycleFilter{ObjectSizeLessThan: &sizeLT}},
			want: false,
		},
		{
			name: "And with every predicate satisfied",
			rule: LifecycleRule{Filter: &LifecycleFilter{And: &LifecycleAnd{
				Prefix:                "data/",
				Tags:                  []LifecycleTag{{Key: "temp", Value: "yes"}, {Key: "team", Value: "core"}},
				ObjectSizeGreaterThan: &sizeGT,
			}}},
			want: true,
		},
		{
			name: "And with one predicate unsatisfied",
			rule: LifecycleRule{Filter: &LifecycleFilter{And: &LifecycleAnd{
				Prefix: "data/",
				Tags:   []LifecycleTag{{Key: "temp", Value: "yes"}, {Key: "missing", Value: "x"}},
			}}},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.rule.matches(obj); got != tc.want {
				t.Errorf("matches = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLifecycleConfiguration_expirationForPicksTheEarliestRule(t *testing.T) {
	// Given: two enabled rules that both select the object
	obj := &Object{Key: "logs/a.txt", LastModified: mustTime(t, "2026-03-01T00:00:00Z")}
	cfg := &LifecycleConfiguration{Rules: []LifecycleRule{
		{ID: "slow", Status: lifecycleStatusEnabled, Filter: &LifecycleFilter{},
			Expiration: &LifecycleExpiration{Days: 30}},
		{ID: "fast", Status: lifecycleStatusEnabled, Filter: &LifecycleFilter{Prefix: "logs/"},
			Expiration: &LifecycleExpiration{Days: 1}},
		{ID: "disabled", Status: lifecycleStatusDisabled, Filter: &LifecycleFilter{},
			Expiration: &LifecycleExpiration{Days: 1}},
	}}

	// When: the object's expiry is resolved
	at, rule := cfg.expirationFor(obj)

	// Then: the soonest enabled rule wins, and the disabled one is ignored
	if rule == nil || rule.ID != "fast" {
		t.Fatalf("rule = %+v, want the 1-day rule", rule)
	}
	if want := mustTime(t, "2026-03-03T00:00:00Z"); !at.Equal(want) {
		t.Errorf("expiry = %s, want %s", at.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

// ---- The sweep -------------------------------------------------------------

func TestSweepLifecycle_expiresOnlyMatchingObjects(t *testing.T) {
	// Given: a bucket whose "tmp/" prefix expires after a day, holding an
	// object that matches, one that does not, and one in another bucket with
	// no configuration at all
	h, mock, ctx := newLifecycleTestHandler(t)
	start := mock.Now().UTC()

	seedObject(t, h, ctx, &Object{Bucket: "b", Key: "tmp/gone.txt", LastModified: start})
	seedObject(t, h, ctx, &Object{Bucket: "b", Key: "keep/stays.txt", LastModified: start})
	seedObject(t, h, ctx, &Object{Bucket: "other", Key: "tmp/safe.txt", LastModified: start})
	seedLifecycle(t, h, ctx, "b", &LifecycleConfiguration{Rules: []LifecycleRule{
		withExpiration(enabledRule("tmp", "tmp/"), 1),
	}})

	// When: the sweep runs after the rounded expiry has passed
	mock.Add(50 * time.Hour)
	h.sweepLifecycle(ctx)

	// Then: only the matching object in the configured bucket is gone
	if objectExists(t, h, ctx, "b", "tmp/gone.txt") {
		t.Error("tmp/gone.txt survived its expiration rule")
	}
	if !objectExists(t, h, ctx, "b", "keep/stays.txt") {
		t.Error("keep/stays.txt was expired by a rule that does not select it")
	}
	if !objectExists(t, h, ctx, "other", "tmp/safe.txt") {
		t.Error("an object in a bucket with no lifecycle configuration was expired")
	}
}

func TestSweepLifecycle_doesNotExpireBeforeTheRoundedExpiry(t *testing.T) {
	// Given: an object one day old under a 1-day rule. AWS rounds the expiry
	// up to the following UTC midnight, so 24 hours is not yet enough.
	h, mock, ctx := newLifecycleTestHandler(t)
	seedObject(t, h, ctx, &Object{Bucket: "b", Key: "a.txt", LastModified: mock.Now().UTC()})
	seedLifecycle(t, h, ctx, "b", &LifecycleConfiguration{Rules: []LifecycleRule{
		withExpiration(enabledRule("all", ""), 1),
	}})

	// When: exactly 24 hours pass
	mock.Add(24 * time.Hour)
	h.sweepLifecycle(ctx)

	// Then: the object is still there
	if !objectExists(t, h, ctx, "b", "a.txt") {
		t.Error("object expired 24h after its write, before the next UTC midnight")
	}
}

func TestSweepLifecycle_disabledRuleIsInert(t *testing.T) {
	// Given: a bucket whose only rule is Disabled
	h, mock, ctx := newLifecycleTestHandler(t)
	seedObject(t, h, ctx, &Object{Bucket: "b", Key: "a.txt", LastModified: mock.Now().UTC()})
	rule := withExpiration(enabledRule("off", ""), 1)
	rule.Status = lifecycleStatusDisabled
	seedLifecycle(t, h, ctx, "b", &LifecycleConfiguration{Rules: []LifecycleRule{rule}})

	// When: well past the expiry
	mock.Add(100 * time.Hour)
	h.sweepLifecycle(ctx)

	// Then: nothing was deleted
	if !objectExists(t, h, ctx, "b", "a.txt") {
		t.Error("a Disabled rule expired an object")
	}
}

func TestSweepLifecycle_transitionMarksStorageClassWithoutDeleting(t *testing.T) {
	// Given: a transition-only rule
	h, mock, ctx := newLifecycleTestHandler(t)
	seedObject(t, h, ctx, &Object{Bucket: "b", Key: "cold.bin", LastModified: mock.Now().UTC()})
	rule := enabledRule("chill", "")
	rule.Transitions = []LifecycleTransition{{Days: 1, StorageClass: "GLACIER"}}
	seedLifecycle(t, h, ctx, "b", &LifecycleConfiguration{Rules: []LifecycleRule{rule}})

	// When: the transition falls due
	mock.Add(50 * time.Hour)
	h.sweepLifecycle(ctx)

	// Then: the object survives and carries the synthetic marker. Transitions
	// stop here — no retrieval delay, no byte movement (§7 non-goals).
	obj, aerr := h.store.getObjectMeta(ctx, "b", "cold.bin")
	if aerr != nil {
		t.Fatalf("transitioned object was deleted: %v", aerr)
	}
	if got := obj.effectiveStorageClass(); got != "GLACIER" {
		t.Errorf("storage class = %q, want GLACIER", got)
	}
}

func TestSweepLifecycle_expirationBeatsTransition(t *testing.T) {
	// Given: a rule that both transitions and expires the same object, with
	// both actions due
	h, mock, ctx := newLifecycleTestHandler(t)
	seedObject(t, h, ctx, &Object{Bucket: "b", Key: "doomed.bin", LastModified: mock.Now().UTC()})
	rule := withExpiration(enabledRule("both", ""), 1)
	rule.Transitions = []LifecycleTransition{{Days: 1, StorageClass: "GLACIER"}}
	seedLifecycle(t, h, ctx, "b", &LifecycleConfiguration{Rules: []LifecycleRule{rule}})

	// When: the sweep runs past both
	mock.Add(50 * time.Hour)
	h.sweepLifecycle(ctx)

	// Then: expiration wins — the object is gone rather than marked
	if objectExists(t, h, ctx, "b", "doomed.bin") {
		t.Error("an object past its expiry was transitioned instead of deleted")
	}
}

func TestSweepLifecycle_abortsIncompleteMultipartUploads(t *testing.T) {
	// Given: an in-progress upload and a rule that abandons it after a day
	h, mock, ctx := newLifecycleTestHandler(t)
	if aerr := h.store.createMultipartUpload(ctx, &MultipartUpload{
		UploadID: "upload-1", Bucket: "b", Key: "big.bin", Initiated: mock.Now().UTC(),
	}); aerr != nil {
		t.Fatalf("seed multipart upload: %v", aerr)
	}
	rule := enabledRule("abort", "")
	rule.AbortIncompleteMultipartUpload = &LifecycleAbortMPU{DaysAfterInitiation: 1}
	seedLifecycle(t, h, ctx, "b", &LifecycleConfiguration{Rules: []LifecycleRule{rule}})

	// When: more than a day passes
	mock.Add(25 * time.Hour)
	h.sweepLifecycle(ctx)

	// Then: the upload is gone
	if _, aerr := h.store.getMultipartUpload(ctx, "upload-1"); aerr == nil {
		t.Error("an abandoned multipart upload survived its AbortIncompleteMultipartUpload rule")
	}
}

func TestSweepLifecycle_keepsRecentMultipartUploads(t *testing.T) {
	// Given: an upload started well inside the abort window
	h, mock, ctx := newLifecycleTestHandler(t)
	if aerr := h.store.createMultipartUpload(ctx, &MultipartUpload{
		UploadID: "upload-2", Bucket: "b", Key: "big.bin", Initiated: mock.Now().UTC(),
	}); aerr != nil {
		t.Fatalf("seed multipart upload: %v", aerr)
	}
	rule := enabledRule("abort", "")
	rule.AbortIncompleteMultipartUpload = &LifecycleAbortMPU{DaysAfterInitiation: 7}
	seedLifecycle(t, h, ctx, "b", &LifecycleConfiguration{Rules: []LifecycleRule{rule}})

	// When: only a day passes
	mock.Add(25 * time.Hour)
	h.sweepLifecycle(ctx)

	// Then: it is untouched
	if _, aerr := h.store.getMultipartUpload(ctx, "upload-2"); aerr != nil {
		t.Errorf("an upload inside its abort window was aborted: %v", aerr)
	}
}

func TestSweepLifecycle_deletedConfigurationStopsExpiring(t *testing.T) {
	// Given: a configuration that is removed before the sweep runs
	h, mock, ctx := newLifecycleTestHandler(t)
	seedObject(t, h, ctx, &Object{Bucket: "b", Key: "a.txt", LastModified: mock.Now().UTC()})
	seedLifecycle(t, h, ctx, "b", &LifecycleConfiguration{Rules: []LifecycleRule{
		withExpiration(enabledRule("all", ""), 1),
	}})
	if aerr := h.store.deleteLifecycle(ctx, "b"); aerr != nil {
		t.Fatalf("delete lifecycle: %v", aerr)
	}

	// When: well past the expiry
	mock.Add(100 * time.Hour)
	h.sweepLifecycle(ctx)

	// Then: nothing was expired
	if !objectExists(t, h, ctx, "b", "a.txt") {
		t.Error("a deleted lifecycle configuration still expired an object")
	}
}

func TestSweepLifecycle_pagesThroughLargeBuckets(t *testing.T) {
	// Given: more objects than one sweep page holds
	h, mock, ctx := newLifecycleTestHandler(t)
	const count = lifecycleSweepPageSize + 25
	for i := 0; i < count; i++ {
		seedObject(t, h, ctx, &Object{
			Bucket:       "b",
			Key:          padKey(i),
			LastModified: mock.Now().UTC(),
		})
	}
	seedLifecycle(t, h, ctx, "b", &LifecycleConfiguration{Rules: []LifecycleRule{
		withExpiration(enabledRule("all", ""), 1),
	}})

	// When: the sweep runs past the expiry
	mock.Add(50 * time.Hour)
	h.sweepLifecycle(ctx)

	// Then: every object was expired, not just the first page
	remaining, aerr := h.store.listObjects(ctx, "b", "")
	if aerr != nil {
		t.Fatalf("list objects: %v", aerr)
	}
	if len(remaining) != 0 {
		t.Errorf("%d objects survived the sweep, want 0 — the sweep stopped at a page boundary", len(remaining))
	}
}

// TestStartLifecycleSweeper_ticksOnTheInjectedClock proves the background loop
// is driven by clock.Clock and stops when its context is cancelled.
func TestStartLifecycleSweeper_ticksOnTheInjectedClock(t *testing.T) {
	// Given: a running sweeper and an already-expired object
	h, mock, ctx := newLifecycleTestHandler(t)
	sweepCtx, cancel := context.WithCancel(ctx)
	h.startLifecycleSweeper(sweepCtx)

	seedObject(t, h, ctx, &Object{Bucket: "b", Key: "a.txt", LastModified: mock.Now().UTC()})
	seedLifecycle(t, h, ctx, "b", &LifecycleConfiguration{Rules: []LifecycleRule{
		withExpiration(enabledRule("all", ""), 1),
	}})

	// When: the clock passes both the expiry and a sweep interval
	mock.Add(50 * time.Hour)

	// Then: the loop expired it without any wall-clock wait
	deadline := time.Now().Add(3 * time.Second)
	for objectExists(t, h, ctx, "b", "a.txt") {
		if time.Now().After(deadline) {
			t.Fatal("the sweeper loop did not expire the object after its ticker fired")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// And: cancelling the context stops it — a later tick changes nothing
	cancel()
	seedObject(t, h, ctx, &Object{Bucket: "b", Key: "b.txt", LastModified: mock.Now().UTC()})
	mock.Add(50 * time.Hour)
	time.Sleep(50 * time.Millisecond)
	if !objectExists(t, h, ctx, "b", "b.txt") {
		t.Error("the sweeper kept running after its context was cancelled")
	}
}

// TestLifecycleConfigFor_isCachedNotRead pins the hot-path contract: once the
// index is loaded, a lookup does not touch the store. S3 is the emulator's
// highest-traffic service, so a per-request store read for a feature almost no
// bucket uses would be a real regression.
func TestLifecycleConfigFor_isCachedNotRead(t *testing.T) {
	// Given: a handler whose index has been loaded once
	h, _, ctx := newLifecycleTestHandler(t)
	seedLifecycle(t, h, ctx, "b", &LifecycleConfiguration{Rules: []LifecycleRule{
		withExpiration(enabledRule("all", ""), 1),
	}})
	if cfg := h.lifecycleConfigFor(ctx, "b"); cfg == nil {
		t.Fatal("configuration not visible after the first lookup")
	}

	// When: the record is deleted behind the cache's back
	if err := h.store.store.Delete(ctx, nsLifecycle, "b"); err != nil {
		t.Fatalf("delete lifecycle record: %v", err)
	}

	// Then: the lookup still answers from the published snapshot, proving it
	// did not re-read the store. Handlers republish the snapshot explicitly
	// after every Put/Delete, and the sweeper refreshes it every tick.
	if cfg := h.lifecycleConfigFor(ctx, "b"); cfg == nil {
		t.Error("lifecycleConfigFor read through to the store instead of the cached snapshot")
	}
}

func TestDeleteBucket_dropsTheLifecycleConfiguration(t *testing.T) {
	// Given: a bucket with a lifecycle configuration
	h, _, ctx := newLifecycleTestHandler(t)
	seedLifecycle(t, h, ctx, "b", &LifecycleConfiguration{Rules: []LifecycleRule{
		withExpiration(enabledRule("all", ""), 1),
	}})

	// When: the bucket is deleted
	if aerr := h.store.deleteBucket(ctx, "b"); aerr != nil {
		t.Fatalf("delete bucket: %v", aerr)
	}

	// Then: the configuration goes with it, so a bucket recreated under the
	// same name does not silently inherit the old rules
	if _, found, aerr := h.store.getLifecycle(ctx, "b"); aerr != nil || found {
		t.Errorf("lifecycle configuration survived DeleteBucket (found=%v, err=%v)", found, aerr)
	}
}

func TestLifecycleConfigFor_unconfiguredBucket(t *testing.T) {
	// Given: a handler with no configurations at all
	h, _, ctx := newLifecycleTestHandler(t)

	// When + Then: an unconfigured bucket resolves to nil, and the object
	// paths therefore do no lifecycle work
	if cfg := h.lifecycleConfigFor(ctx, "anything"); cfg != nil {
		t.Errorf("lifecycleConfigFor = %+v, want nil", cfg)
	}
}

// ---- Local helpers ---------------------------------------------------------

func withExpiration(rule LifecycleRule, days int) LifecycleRule {
	rule.Expiration = &LifecycleExpiration{Days: days}
	return rule
}

func strPtr(s string) *string { return &s }

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return parsed.UTC()
}

// padKey returns a fixed-width key so the store's lexicographic paging order
// matches numeric order.
func padKey(i int) string {
	const digits = "0123456789"
	out := []byte("key-00000")
	for pos := len(out) - 1; pos >= 4 && i > 0; pos-- {
		out[pos] = digits[i%10]
		i /= 10
	}
	return string(out)
}
