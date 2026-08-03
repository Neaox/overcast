package apigateway

// usage.go — usage-plan throttle and quota accounting for the REST v1 execute
// path.
//
// Two things happen here, and they are deliberately separate:
//
//   - **Evaluation** always runs. Every request that presents an API key
//     attached to a usage plan is measured against that plan's
//     ThrottleSettings (a token bucket) and QuotaSettings (a calendar window),
//     and the result is readable through GetUsage. This costs one map lookup
//     and one short critical section per request, and never touches
//     state.Store.
//   - **Rejection** is opt-in, gated on Config.EnforceAPIGatewayThrottle
//     (OVERCAST_ENFORCE_APIGATEWAY_THROTTLE). With it off — the default — an
//     over-limit request is served exactly as it is today, so a stack that
//     works locally keeps working. See docs/plans/full-emulation-priority.md
//     § 2.1 shape 1.
//
// Scope is REST v1 only. HTTP APIs (v2) have no usage-plan or API-key concept
// on real AWS, so there is nothing to enforce on ExecuteV2API and inventing
// something would be a divergence, not a feature.
//
// State lives in memory and is not persisted: quota counters reset when the
// emulator restarts. That is a deliberate trade for keeping the request path
// free of store round-trips (see docs/dev/performance.md); real AWS persists
// them across the quota period.

import (
	"math"
	"sync"
	"time"
)

// throttleReason names which limit a request fell foul of. It maps onto the
// API Gateway gateway-response type of the same meaning.
type throttleReason string

const (
	// reasonNone means the request was within every configured limit.
	reasonNone throttleReason = ""
	// reasonThrottle is API Gateway's THROTTLED gateway response: the
	// usage plan's token bucket was empty.
	reasonThrottle throttleReason = "THROTTLED"
	// reasonQuota is API Gateway's QUOTA_EXCEEDED gateway response: the
	// usage plan's quota for the current period is spent.
	reasonQuota throttleReason = "QUOTA_EXCEEDED"
)

// maxTrackedDays bounds the per-key daily history a usageEntry keeps for
// GetUsage. Anything older than this is dropped on the next write; a month
// plus a margin covers the longest quota period AWS offers (MONTH).
const maxTrackedDays = 40

// usageDateLayout is the date format GetUsage's startDate/endDate parameters
// and its daily log keys use.
const usageDateLayout = "2006-01-02"

// maxUsageRange bounds the window GetUsage will report on. The response holds
// one entry per day per key, so without a cap a query parameter would size the
// reply. AWS documents no equivalent limit.
const maxUsageRange = 400 * 24 * time.Hour

// usageKey identifies a single (usage plan, API key) accounting bucket. AWS
// throttles per API key within a plan, not per plan, so the pair is the unit.
type usageKey struct {
	planID string
	keyID  string
}

// usageDecision is the outcome of evaluating one request against its plan.
type usageDecision struct {
	// Reason is why the request would be rejected, or reasonNone.
	Reason throttleReason
	// Used is the request count in the current quota period, after this
	// request was counted (or not, when it was rejected).
	Used int64
	// Remaining is the quota left in the current period, or -1 when the plan
	// configures no quota. It can go negative when enforcement is off, which
	// is exactly the "how far past the limit is this stack" signal that mode
	// exists to give.
	Remaining int64
	// Throttles and QuotaRejects are the cumulative counts for this
	// (plan, key) pair since the emulator started, including decisions that
	// were reported but not enforced.
	Throttles    int64
	QuotaRejects int64
}

// usageEntry is the mutable accounting state for one (plan, key) pair. Its own
// mutex is the only lock a request takes once the entry exists, so concurrent
// traffic for different keys never contends.
type usageEntry struct {
	mu sync.Mutex

	// Token bucket.
	tokens     float64
	lastRefill time.Time
	bucketInit bool

	// Quota window.
	periodStart time.Time
	firstPeriod time.Time
	periodUsed  int64
	windowsSeen bool

	// Daily log for GetUsage. dayStart/dayKey memoise the formatted date so
	// the request path does not allocate a string per call.
	daily    map[string]int64
	dayStart time.Time
	dayKey   string

	throttles    int64
	quotaRejects int64

	// lastReport coalesces the event/log notification — see shouldReport.
	lastReport time.Time
}

// reportInterval is the minimum gap between throttle notifications for one
// (plan, key) pair. Being over a limit means the caller is by definition
// hammering the endpoint, so publishing one bus event per rejected request
// would put unbounded work on the request path for no extra information.
const reportInterval = time.Second

// shouldReport reports whether a throttle notification is due for key, and
// records that it is being sent. Cumulative counts stay exact regardless;
// only the notification is coalesced.
func (t *usageTracker) shouldReport(key usageKey, now time.Time) bool {
	t.mu.RLock()
	e := t.entries[key]
	t.mu.RUnlock()
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.lastReport.IsZero() && now.Sub(e.lastReport) < reportInterval {
		return false
	}
	e.lastReport = now
	return true
}

// usageTracker holds every (plan, key) accounting entry. The map is guarded by
// a RWMutex taken only long enough to find or create an entry; all the
// per-request work happens under the entry's own lock.
type usageTracker struct {
	mu      sync.RWMutex
	entries map[usageKey]*usageEntry
}

func newUsageTracker() *usageTracker {
	return &usageTracker{entries: make(map[usageKey]*usageEntry)}
}

// entry returns the accounting entry for key, creating it on first use.
func (t *usageTracker) entry(key usageKey) *usageEntry {
	t.mu.RLock()
	e := t.entries[key]
	t.mu.RUnlock()
	if e != nil {
		return e
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if e = t.entries[key]; e != nil {
		return e
	}
	e = &usageEntry{daily: make(map[string]int64, 2)}
	t.entries[key] = e
	return e
}

// record evaluates one request against plan's limits and accounts for it.
//
// When enforce is false the request is always counted and the returned
// decision reports what *would* have happened. When enforce is true a rejected
// request consumes neither quota nor a token, matching AWS: a 429 does not
// count against the usage plan quota.
//
// Quota is evaluated before the rate limit so that an exhausted quota is
// reported as QUOTA_EXCEEDED rather than as whichever limit happened to be
// checked first.
func (t *usageTracker) record(key usageKey, plan *UsagePlan, now time.Time, enforce bool) usageDecision {
	e := t.entry(key)
	e.mu.Lock()
	defer e.mu.Unlock()

	e.rollQuotaWindow(plan.Quota, now)

	limit := effectiveQuotaLimit(plan.Quota, e.periodStart, e.firstPeriod)
	remaining := int64(-1)
	if limit >= 0 {
		remaining = limit - e.periodUsed
	}

	// Quota first: a spent quota is a longer-lived condition than an empty
	// bucket, and reporting it as a rate throttle would send a client into a
	// retry loop that cannot succeed.
	if limit >= 0 && e.periodUsed >= limit {
		e.quotaRejects++
		if enforce {
			return usageDecision{
				Reason: reasonQuota, Used: e.periodUsed, Remaining: remaining,
				Throttles: e.throttles, QuotaRejects: e.quotaRejects,
			}
		}
	}

	allowed := e.takeToken(plan.Throttle, now)
	if !allowed {
		e.throttles++
		if enforce {
			return usageDecision{
				Reason: reasonThrottle, Used: e.periodUsed, Remaining: remaining,
				Throttles: e.throttles, QuotaRejects: e.quotaRejects,
			}
		}
	}

	// The request is being served, so it counts.
	e.periodUsed++
	e.countDay(now)
	if limit >= 0 {
		remaining = limit - e.periodUsed
	}

	reason := reasonNone
	switch {
	case limit >= 0 && remaining < 0:
		reason = reasonQuota
	case !allowed:
		reason = reasonThrottle
	}
	return usageDecision{
		Reason: reason, Used: e.periodUsed, Remaining: remaining,
		Throttles: e.throttles, QuotaRejects: e.quotaRejects,
	}
}

// usageFor reports the recorded daily counts for key over the inclusive day
// range [from, to], alongside the quota limit in force. Days with no traffic
// report zero, matching AWS's fixed-length daily log.
func (t *usageTracker) usageFor(key usageKey, plan *UsagePlan, from, to time.Time) [][2]int64 {
	t.mu.RLock()
	e := t.entries[key]
	t.mu.RUnlock()

	days := int(to.Sub(from).Hours()/24) + 1
	if days < 1 {
		days = 1
	}
	out := make([][2]int64, 0, days)

	var daily map[string]int64
	var firstPeriod time.Time
	if e != nil {
		e.mu.Lock()
		daily = make(map[string]int64, len(e.daily))
		for k, v := range e.daily {
			daily[k] = v
		}
		firstPeriod = e.firstPeriod
		e.mu.Unlock()
	}

	q := quotaOf(plan)
	var cumulative int64
	var currentPeriod time.Time
	for i := range days {
		day := from.AddDate(0, 0, i)
		used := daily[day.Format(usageDateLayout)]

		// Every day inside one quota period draws on the same allowance, so
		// the running total resets when the period does. With the default DAY
		// period that is every row; with WEEK/MONTH it spans several.
		period := quotaPeriodStart(q, day)
		if !period.Equal(currentPeriod) {
			currentPeriod = period
			cumulative = 0
		}
		cumulative += used

		remaining := int64(-1)
		if limit := effectiveQuotaLimit(q, period, firstPeriod); limit >= 0 {
			remaining = limit - cumulative
		}
		out = append(out, [2]int64{used, remaining})
	}
	return out
}

// rollQuotaWindow resets the period counter when now has moved into a new
// quota window. Called with e.mu held.
func (e *usageEntry) rollQuotaWindow(q *QuotaSettings, now time.Time) {
	start := quotaPeriodStart(q, now)
	if !e.windowsSeen {
		e.windowsSeen = true
		e.periodStart = start
		e.firstPeriod = start
		return
	}
	if !start.Equal(e.periodStart) {
		e.periodStart = start
		e.periodUsed = 0
	}
}

// takeToken applies the plan's ThrottleSettings as a token bucket, where
// rateLimit is the tokens-per-second refill and burstLimit is the bucket
// capacity — the model API Gateway documents. Returns true when a token was
// available. Called with e.mu held.
func (e *usageEntry) takeToken(th *ThrottleSettings, now time.Time) bool {
	if th == nil || (th.RateLimit <= 0 && th.BurstLimit <= 0) {
		return true // no rate limit configured
	}

	burst := float64(th.BurstLimit)
	if burst <= 0 {
		// AWS falls back to the account-level burst when a plan sets only a
		// rate. One second of refill is the closest analogue that keeps the
		// configured rate meaningful without inventing an account limit.
		burst = math.Max(th.RateLimit, 1)
	}

	if !e.bucketInit {
		e.bucketInit = true
		e.tokens = burst
		e.lastRefill = now
	} else if elapsed := now.Sub(e.lastRefill).Seconds(); elapsed > 0 {
		e.tokens = math.Min(burst, e.tokens+elapsed*th.RateLimit)
		e.lastRefill = now
	}

	if e.tokens >= 1 {
		e.tokens--
		return true
	}
	return false
}

// countDay increments the daily log used by GetUsage, pruning entries that
// have aged out so the map stays bounded. The date string is formatted once
// per calendar day rather than once per request. Called with e.mu held.
func (e *usageEntry) countDay(now time.Time) {
	utc := now.UTC()
	dayStart := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
	if e.dayKey == "" || !dayStart.Equal(e.dayStart) {
		e.dayStart = dayStart
		e.dayKey = dayStart.Format(usageDateLayout)
		if len(e.daily) > maxTrackedDays {
			cutoff := dayStart.AddDate(0, 0, -maxTrackedDays).Format(usageDateLayout)
			for k := range e.daily {
				if k < cutoff { // ISO dates sort lexicographically
					delete(e.daily, k)
				}
			}
		}
	}
	e.daily[e.dayKey]++
}

// quotaOf returns plan's quota settings, tolerating a nil plan.
func quotaOf(plan *UsagePlan) *QuotaSettings {
	if plan == nil {
		return nil
	}
	return plan.Quota
}

// effectiveQuotaLimit returns the request allowance for the period starting at
// periodStart, or -1 when the plan configures no quota.
//
// AWS documents QuotaSettings.offset as "the number of requests subtracted
// from the given limit in the initial time period", so the offset applies only
// to the first window a key is seen in.
func effectiveQuotaLimit(q *QuotaSettings, periodStart, firstPeriod time.Time) int64 {
	if q == nil || q.Limit <= 0 {
		return -1
	}
	limit := int64(q.Limit)
	if q.Offset > 0 && !periodStart.IsZero() && periodStart.Equal(firstPeriod) {
		limit -= int64(q.Offset)
		if limit < 0 {
			limit = 0
		}
	}
	return limit
}

// quotaPeriodStart returns the UTC start of the quota window containing now.
// Periods are calendar-aligned the way API Gateway aligns them: DAY at
// midnight, WEEK at Sunday midnight, MONTH on the first of the month. An
// unset or unrecognised period is treated as DAY, matching the AWS default.
func quotaPeriodStart(q *QuotaSettings, now time.Time) time.Time {
	utc := now.UTC()
	day := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
	if q == nil {
		return day
	}
	switch q.Period {
	case "WEEK":
		return day.AddDate(0, 0, -int(day.Weekday()))
	case "MONTH":
		return time.Date(utc.Year(), utc.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		return day
	}
}
