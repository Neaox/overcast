package s3

// lifecycle.go holds the stored model for S3 bucket lifecycle configuration,
// the in-memory index that keeps the object hot paths free of lifecycle work,
// and the clock-driven sweeper that actually applies the rules.
//
// Scope (docs/plans/full-emulation-priority.md §7): expirations really delete;
// transitions stop at a synthetic storage-class marker. There is no Glacier
// retrieval-delay simulation and no replication.
//
// Anything the sweeper below cannot act on is refused by
// handler_lifecycle.go's validation rather than stored and silently ignored
// (§2.1, shape 2). Keep the two files in step: a construct added to the model
// here must either be evaluated here or refused there.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// nsLifecycle stores one LifecycleConfiguration per bucket, keyed by bucket
// name. A bucket with no configuration has no record — which is what makes the
// idle sweep a single scan of an empty namespace.
const nsLifecycle = "s3:lifecycle"

// lifecycleSweepInterval is how often the sweeper wakes. Real S3 applies
// lifecycle actions asynchronously, "typically within 48 hours" of an object
// becoming eligible; an hourly local sweep is well inside that and matches the
// DynamoDB TTL sweeper's cadence. The sweep is driven by the injected clock,
// so tests reach it by advancing a mock rather than waiting.
const lifecycleSweepInterval = time.Hour

// lifecycleSweepPageSize bounds how many objects one sweep step holds in
// memory. The sweeper pages through a bucket rather than materialising it, so
// peak memory is a function of this constant and not of bucket size.
const lifecycleSweepPageSize = 1000

// ---- Stored model ----------------------------------------------------------

// LifecycleConfiguration is a bucket's stored lifecycle rules.
type LifecycleConfiguration struct {
	Rules []LifecycleRule `json:"rules"`
}

// LifecycleRule is one lifecycle rule.
//
// Exactly one of Prefix and Filter is non-nil: Prefix is the deprecated
// rule-level form (still what the CLI's simplest example emits), Filter the
// current one. AWS refuses a rule carrying both, and echoes back whichever
// form was supplied rather than rewriting one into the other — so both are
// modelled rather than normalised.
type LifecycleRule struct {
	ID     string `json:"id"`
	Status string `json:"status"` // "Enabled" or "Disabled"

	Prefix *string          `json:"prefix,omitempty"`
	Filter *LifecycleFilter `json:"filter,omitempty"`

	Expiration                     *LifecycleExpiration  `json:"expiration,omitempty"`
	Transitions                    []LifecycleTransition `json:"transitions,omitempty"`
	AbortIncompleteMultipartUpload *LifecycleAbortMPU    `json:"abort_incomplete_multipart_upload,omitempty"`
}

// LifecycleFilter selects which objects a rule applies to. AWS models it as a
// union: at most one of the fields below is set, with And carrying the
// conjunction of several predicates.
type LifecycleFilter struct {
	Prefix                string        `json:"prefix,omitempty"`
	Tag                   *LifecycleTag `json:"tag,omitempty"`
	ObjectSizeGreaterThan *int64        `json:"object_size_greater_than,omitempty"`
	ObjectSizeLessThan    *int64        `json:"object_size_less_than,omitempty"`
	And                   *LifecycleAnd `json:"and,omitempty"`
}

// LifecycleAnd is the conjunction form of a filter.
type LifecycleAnd struct {
	Prefix                string         `json:"prefix,omitempty"`
	Tags                  []LifecycleTag `json:"tags,omitempty"`
	ObjectSizeGreaterThan *int64         `json:"object_size_greater_than,omitempty"`
	ObjectSizeLessThan    *int64         `json:"object_size_less_than,omitempty"`
}

// LifecycleTag is one object-tag predicate.
type LifecycleTag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// LifecycleExpiration deletes matching objects. Exactly one of Days and Date
// is set — AWS rejects a rule carrying both.
type LifecycleExpiration struct {
	Days int        `json:"days,omitempty"`
	Date *time.Time `json:"date,omitempty"`
}

// LifecycleTransition marks matching objects with a storage class. Exactly one
// of Days and Date is set.
type LifecycleTransition struct {
	Days         int        `json:"days,omitempty"`
	Date         *time.Time `json:"date,omitempty"`
	StorageClass string     `json:"storage_class"`
}

// LifecycleAbortMPU aborts multipart uploads left in progress too long.
type LifecycleAbortMPU struct {
	DaysAfterInitiation int `json:"days_after_initiation"`
}

// enabled reports whether the rule's Status selects it for action.
func (r *LifecycleRule) enabled() bool { return r.Status == lifecycleStatusEnabled }

// keyPrefix returns the key prefix the rule selects on, in either the legacy
// or the Filter form. An empty string means the whole bucket.
func (r *LifecycleRule) keyPrefix() string {
	switch {
	case r.Prefix != nil:
		return *r.Prefix
	case r.Filter == nil:
		return ""
	case r.Filter.And != nil:
		return r.Filter.And.Prefix
	default:
		return r.Filter.Prefix
	}
}

// matches reports whether obj is selected by the rule's filter.
func (r *LifecycleRule) matches(obj *Object) bool {
	if !strings.HasPrefix(obj.Key, r.keyPrefix()) {
		return false
	}
	if r.Prefix != nil || r.Filter == nil {
		return true
	}
	f := r.Filter
	if f.And != nil {
		for i := range f.And.Tags {
			if !objectHasTag(obj, &f.And.Tags[i]) {
				return false
			}
		}
		return sizeWithin(obj.ContentLength, f.And.ObjectSizeGreaterThan, f.And.ObjectSizeLessThan)
	}
	if f.Tag != nil && !objectHasTag(obj, f.Tag) {
		return false
	}
	return sizeWithin(obj.ContentLength, f.ObjectSizeGreaterThan, f.ObjectSizeLessThan)
}

func objectHasTag(obj *Object, tag *LifecycleTag) bool {
	v, ok := obj.Tags[tag.Key]
	return ok && v == tag.Value
}

// sizeWithin applies AWS's exclusive object-size bounds.
func sizeWithin(size int64, greaterThan, lessThan *int64) bool {
	if greaterThan != nil && size <= *greaterThan {
		return false
	}
	if lessThan != nil && size >= *lessThan {
		return false
	}
	return true
}

// expiresAt returns when AWS would expire obj under this rule.
//
// For a Days rule S3 "adds the number of days to the object's last modified
// date and rounds the resulting time to the next day midnight UTC", so an
// object written at 15:00 under a 1-day rule expires at 00:00 UTC two
// calendar days later, not 24 hours later. A Date rule expires at the date
// itself, which validation has already pinned to UTC midnight.
func (r *LifecycleRule) expiresAt(obj *Object) (time.Time, bool) {
	if r.Expiration == nil {
		return time.Time{}, false
	}
	if r.Expiration.Date != nil {
		return r.Expiration.Date.UTC(), true
	}
	return nextUTCMidnight(obj.LastModified.UTC().AddDate(0, 0, r.Expiration.Days)), true
}

// transitionAt returns when the given transition takes effect for obj, using
// the same midnight rounding as expiresAt.
func transitionAt(t *LifecycleTransition, obj *Object) time.Time {
	if t.Date != nil {
		return t.Date.UTC()
	}
	return nextUTCMidnight(obj.LastModified.UTC().AddDate(0, 0, t.Days))
}

// nextUTCMidnight rounds t up to the next UTC midnight. Truncate works on
// absolute time, which is UTC-aligned, so truncating to 24h yields the UTC
// midnight that starts t's day.
//
// A time that is *already* midnight is returned unchanged: AWS rounds the
// computed expiry to the next midnight, and a value sitting on one needs no
// rounding. Rounding it forward anyway would give an object written at
// 00:00:00 UTC a whole extra day of life. Rare against wall-clock timestamps,
// routine against a mock clock started at a round time.
func nextUTCMidnight(t time.Time) time.Time {
	utc := t.UTC()
	day := utc.Truncate(24 * time.Hour)
	if day.Equal(utc) {
		return day
	}
	return day.Add(24 * time.Hour)
}

// expirationFor returns the earliest expiry the configuration schedules for
// obj, and the rule that schedules it. AWS resolves overlapping expiration
// rules in the object's favour by applying the one that fires first.
func (c *LifecycleConfiguration) expirationFor(obj *Object) (time.Time, *LifecycleRule) {
	var (
		soonest time.Time
		winner  *LifecycleRule
	)
	for i := range c.Rules {
		rule := &c.Rules[i]
		if !rule.enabled() || rule.Expiration == nil || !rule.matches(obj) {
			continue
		}
		at, ok := rule.expiresAt(obj)
		if !ok {
			continue
		}
		if winner == nil || at.Before(soonest) {
			soonest, winner = at, rule
		}
	}
	return soonest, winner
}

// transitionFor returns the storage class obj should carry as of now: the
// class of the latest-firing transition that is already due.
func (c *LifecycleConfiguration) transitionFor(obj *Object, now time.Time) (string, bool) {
	var (
		latest time.Time
		class  string
	)
	for i := range c.Rules {
		rule := &c.Rules[i]
		if !rule.enabled() || len(rule.Transitions) == 0 || !rule.matches(obj) {
			continue
		}
		for j := range rule.Transitions {
			at := transitionAt(&rule.Transitions[j], obj)
			if at.After(now) {
				continue
			}
			if class == "" || at.After(latest) {
				latest, class = at, rule.Transitions[j].StorageClass
			}
		}
	}
	return class, class != ""
}

// abortIncompleteAfter returns the shortest DaysAfterInitiation any enabled
// rule sets. AWS scopes this action by the rule's filter, but an in-progress
// upload has no tags or size yet, so only rules whose prefix the key matches
// are consulted.
func (c *LifecycleConfiguration) abortIncompleteAfter(key string) (int, bool) {
	best := 0
	for i := range c.Rules {
		rule := &c.Rules[i]
		if !rule.enabled() || rule.AbortIncompleteMultipartUpload == nil {
			continue
		}
		if !strings.HasPrefix(key, rule.keyPrefix()) {
			continue
		}
		days := rule.AbortIncompleteMultipartUpload.DaysAfterInitiation
		if best == 0 || days < best {
			best = days
		}
	}
	return best, best > 0
}

// hasObjectActions reports whether the configuration schedules anything the
// object sweep needs to look at. A configuration with only an
// AbortIncompleteMultipartUpload rule never justifies paging the bucket.
func (c *LifecycleConfiguration) hasObjectActions() bool {
	for i := range c.Rules {
		if !c.Rules[i].enabled() {
			continue
		}
		if c.Rules[i].Expiration != nil || len(c.Rules[i].Transitions) > 0 {
			return true
		}
	}
	return false
}

// hasAbortActions reports whether any enabled rule aborts incomplete uploads.
func (c *LifecycleConfiguration) hasAbortActions() bool {
	for i := range c.Rules {
		if c.Rules[i].enabled() && c.Rules[i].AbortIncompleteMultipartUpload != nil {
			return true
		}
	}
	return false
}

// ---- Store access ----------------------------------------------------------

func (s *s3Store) getLifecycle(ctx context.Context, bucket string) (*LifecycleConfiguration, bool, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsLifecycle, bucket)
	if err != nil {
		return nil, false, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, false, nil
	}
	var cfg LifecycleConfiguration
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		// A single unreadable record must not become a 500 for the bucket:
		// report it the way AWS reports an absent configuration, which is the
		// state the caller can actually act on (CONTRIBUTING.md
		// malformed-persisted-state policy).
		return nil, false, nil
	}
	return &cfg, true, nil
}

func (s *s3Store) putLifecycle(ctx context.Context, bucket string, cfg *LifecycleConfiguration) *protocol.AWSError {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsLifecycle, bucket, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *s3Store) deleteLifecycle(ctx context.Context, bucket string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsLifecycle, bucket); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// scanLifecycle returns every stored configuration, keyed by bucket. One store
// round trip; an empty namespace costs a single empty scan.
func (s *s3Store) scanLifecycle(ctx context.Context) (map[string]*LifecycleConfiguration, error) {
	pairs, err := s.store.Scan(ctx, nsLifecycle, "")
	if err != nil {
		return nil, err
	}
	out := make(map[string]*LifecycleConfiguration, len(pairs))
	for _, kv := range pairs {
		var cfg LifecycleConfiguration
		if jsonErr := json.Unmarshal([]byte(kv.Value), &cfg); jsonErr != nil {
			// One malformed persisted record must not fail the whole scan
			// (CONTRIBUTING.md malformed-persisted-state rule).
			continue
		}
		out[kv.Key] = &cfg
	}
	return out, nil
}

// ---- In-memory index -------------------------------------------------------

// lifecycleIndex is a snapshot of every bucket's lifecycle configuration,
// published as one immutable map behind an atomic pointer.
//
// It exists so that the object hot paths — GetObject, HeadObject, PutObject,
// which are the emulator's highest-traffic routes — pay one atomic load and
// one map lookup for a feature almost no bucket uses, instead of a store read
// per request. The snapshot is replaced wholesale on every configuration
// change and at the top of every sweep, never mutated in place.
type lifecycleIndex struct {
	init serviceutil.LazyInit
	snap atomic.Pointer[map[string]*LifecycleConfiguration]
}

// refreshLifecycleIndex rebuilds and publishes the snapshot. Returns the new
// snapshot so the sweeper can use it without a second load.
func (h *Handler) refreshLifecycleIndex(ctx context.Context) (map[string]*LifecycleConfiguration, error) {
	configs, err := h.store.scanLifecycle(ctx)
	if err != nil {
		return nil, err
	}
	h.lifecycle.snap.Store(&configs)
	return configs, nil
}

// lifecycleConfigFor returns the cached configuration for bucket, or nil when
// it has none. The first call loads the snapshot; every later call is an
// atomic load and a map lookup.
//
// Lazy-init only: no store read happens in New() (AGENTS.md startup budget).
func (h *Handler) lifecycleConfigFor(ctx context.Context, bucket string) *LifecycleConfiguration {
	if err := h.lifecycle.init.Do(func() error {
		_, err := h.refreshLifecycleIndex(ctx)
		return err
	}); err != nil {
		h.log.Warn("lifecycle: load configuration index", zap.Error(err))
		return nil
	}
	snap := h.lifecycle.snap.Load()
	if snap == nil {
		return nil
	}
	return (*snap)[bucket]
}

// ---- Object-path hooks -----------------------------------------------------

// expirationHeader returns the value of the x-amz-expiration header AWS sets
// on an object a lifecycle rule will delete, or "" when no rule expires it.
//
// Shape: expiry-date="Fri, 21 Dec 2012 00:00:00 GMT", rule-id="rule-1".
func (h *Handler) expirationHeader(ctx context.Context, obj *Object) string {
	cfg := h.lifecycleConfigFor(ctx, obj.Bucket)
	if cfg == nil {
		return ""
	}
	at, rule := cfg.expirationFor(obj)
	if rule == nil {
		return ""
	}
	return `expiry-date="` + at.Format(lifecycleExpiryDateFormat) + `", rule-id="` + rule.ID + `"`
}

// lifecycleExpiryDateFormat is RFC 1123 with a literal GMT zone, which is what
// S3 puts in x-amz-expiration.
const lifecycleExpiryDateFormat = "Mon, 02 Jan 2006 15:04:05 GMT"

// setExpirationHeader writes x-amz-expiration when a rule expires obj, and
// nothing at all when none does.
func (h *Handler) setExpirationHeader(ctx context.Context, w http.ResponseWriter, obj *Object) {
	if v := h.expirationHeader(ctx, obj); v != "" {
		w.Header().Set("x-amz-expiration", v)
	}
}

// ---- Sweeper ---------------------------------------------------------------

// startLifecycleSweeper starts the single background loop that applies every
// bucket's lifecycle rules. One goroutine and one ticker for the whole
// service — never one per bucket or per object — cancelled by ctx on shutdown.
//
// The returned channel is closed once the loop has exited, so a caller can
// wait for an in-flight sweep to finish rather than returning from shutdown
// while the sweeper is still deleting objects. See Service.Stop.
func (h *Handler) startLifecycleSweeper(ctx context.Context) <-chan struct{} {
	ticker := h.clk.Ticker(lifecycleSweepInterval)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.sweepLifecycle(ctx)
			}
		}
	}()
	return done
}

// sweepLifecycle applies every stored configuration once.
//
// The scan of the (usually empty) lifecycle namespace is the whole idle cost:
// no configurations means one empty scan an hour and no object reads at all.
// Buckets without a configuration are never paged.
func (h *Handler) sweepLifecycle(ctx context.Context) {
	configs, err := h.refreshLifecycleIndex(ctx)
	if err != nil {
		h.log.Error("lifecycle: scan configurations", zap.Error(err))
		return
	}
	// The snapshot the object paths read is current as of this tick, so mark
	// the lazy load satisfied — a later object request must not re-scan.
	_ = h.lifecycle.init.Do(func() error { return nil })

	if len(configs) == 0 {
		return
	}

	now := h.clk.Now().UTC()
	for bucket, cfg := range configs {
		if ctx.Err() != nil {
			return
		}
		h.sweepBucketObjects(ctx, bucket, cfg, now)
		h.sweepBucketUploads(ctx, bucket, cfg, now)
	}
}

// sweepBucketObjects expires and transitions the objects of one bucket. It
// pages the bucket rather than listing it whole, so memory stays bounded by
// lifecycleSweepPageSize regardless of how many objects the bucket holds.
func (h *Handler) sweepBucketObjects(ctx context.Context, bucket string, cfg *LifecycleConfiguration, now time.Time) {
	if !cfg.hasObjectActions() {
		return
	}
	startAfter := ""
	for {
		objects, next, aerr := h.store.listObjectsPage(ctx, bucket, "", startAfter, lifecycleSweepPageSize)
		if aerr != nil {
			h.log.Error("lifecycle: list objects",
				zap.String("bucket", bucket), zap.Error(aerr))
			return
		}
		for _, obj := range objects {
			if ctx.Err() != nil {
				return
			}
			h.applyLifecycleToObject(ctx, cfg, obj, now)
		}
		if next == "" {
			return
		}
		startAfter = next
	}
}

// applyLifecycleToObject expires obj if any rule's expiry has passed, and
// otherwise marks it with the storage class of the latest due transition.
//
// Expiration wins: an object past its expiry is deleted rather than
// transitioned, which is also the order AWS applies the two actions in.
func (h *Handler) applyLifecycleToObject(ctx context.Context, cfg *LifecycleConfiguration, obj *Object, now time.Time) {
	if at, rule := cfg.expirationFor(obj); rule != nil && !at.After(now) {
		if aerr := h.store.deleteObject(ctx, obj.Bucket, obj.Key); aerr != nil {
			h.log.Error("lifecycle: expire object",
				zap.String("bucket", obj.Bucket), zap.String("key", obj.Key), zap.Error(aerr))
			return
		}
		// A per-tick sweep outcome on the sweeper's own schedule, not a
		// response to a client request — TRACE per CONTRIBUTING § Log levels.
		h.log.Trace("lifecycle: object expired",
			zap.String("bucket", obj.Bucket), zap.String("key", obj.Key),
			zap.String("rule", rule.ID))
		return
	}

	class, ok := cfg.transitionFor(obj, now)
	if !ok || class == obj.effectiveStorageClass() {
		return
	}
	obj.StorageClass = class
	if aerr := h.store.putObjectMeta(ctx, obj); aerr != nil {
		h.log.Error("lifecycle: transition object",
			zap.String("bucket", obj.Bucket), zap.String("key", obj.Key), zap.Error(aerr))
		return
	}
	h.log.Trace("lifecycle: object transitioned",
		zap.String("bucket", obj.Bucket), zap.String("key", obj.Key),
		zap.String("storage_class", class))
}

// sweepBucketUploads aborts multipart uploads left in progress longer than an
// AbortIncompleteMultipartUpload rule allows.
func (h *Handler) sweepBucketUploads(ctx context.Context, bucket string, cfg *LifecycleConfiguration, now time.Time) {
	if !cfg.hasAbortActions() {
		return
	}
	uploads, aerr := h.store.listMultipartUploads(ctx, bucket)
	if aerr != nil {
		h.log.Error("lifecycle: list multipart uploads",
			zap.String("bucket", bucket), zap.Error(aerr))
		return
	}
	for _, u := range uploads {
		days, ok := cfg.abortIncompleteAfter(u.Key)
		if !ok {
			continue
		}
		if u.Initiated.UTC().AddDate(0, 0, days).After(now) {
			continue
		}
		if delErr := h.store.deleteAllParts(ctx, u.UploadID); delErr != nil {
			h.log.Error("lifecycle: delete parts of abandoned upload",
				zap.String("bucket", bucket), zap.String("upload_id", u.UploadID), zap.Error(delErr))
			continue
		}
		if delErr := h.store.deleteMultipartUpload(ctx, u.UploadID); delErr != nil {
			h.log.Error("lifecycle: abort abandoned upload",
				zap.String("bucket", bucket), zap.String("upload_id", u.UploadID), zap.Error(delErr))
			continue
		}
		h.log.Trace("lifecycle: incomplete multipart upload aborted",
			zap.String("bucket", bucket), zap.String("key", u.Key))
	}
}
