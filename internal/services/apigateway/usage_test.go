package apigateway

// usage_test.go — token-bucket and quota-window behaviour for usage plans.
//
// These exercise the accounting directly against a mock clock, which is the
// only practical way to cross a quota period: advancing a whole test server's
// clock by a day drives every clock-driven sweeper in the emulator through
// 86,400 ticks.

import (
	"testing"
	"time"

	"github.com/overcast-sh/overcast/internal/clock"
)

func planWith(throttle *ThrottleSettings, quota *QuotaSettings) *UsagePlan {
	return &UsagePlan{ID: "plan1", Name: "plan", Throttle: throttle, Quota: quota}
}

var testKey = usageKey{planID: "plan1", keyID: "key1"}

func TestUsageTracker_noLimitsAlwaysAllows(t *testing.T) {
	// Given a plan with neither a throttle nor a quota
	tr := newUsageTracker()
	clk := clock.NewMock()
	plan := planWith(nil, nil)

	// When many requests arrive in the same instant with enforcement on
	for i := range 100 {
		d := tr.record(testKey, plan, clk.Now(), true)
		// Then none is rejected, and usage is still counted
		if d.Reason != reasonNone {
			t.Fatalf("request %d: expected no limit, got %q", i+1, d.Reason)
		}
		if d.Remaining != -1 {
			t.Fatalf("request %d: expected remaining=-1 without a quota, got %d", i+1, d.Remaining)
		}
	}
	if d := tr.record(testKey, plan, clk.Now(), true); d.Used != 101 {
		t.Errorf("expected usage to keep counting without limits, got %d", d.Used)
	}
}

func TestUsageTracker_burstThenRefill(t *testing.T) {
	// Given a bucket of 3 tokens refilling at 2/s
	tr := newUsageTracker()
	clk := clock.NewMock()
	plan := planWith(&ThrottleSettings{BurstLimit: 3, RateLimit: 2}, nil)

	// When the burst is spent in one instant
	for i := range 3 {
		if d := tr.record(testKey, plan, clk.Now(), true); d.Reason != reasonNone {
			t.Fatalf("request %d should be within burst, got %q", i+1, d.Reason)
		}
	}

	// Then the next request is throttled
	if d := tr.record(testKey, plan, clk.Now(), true); d.Reason != reasonThrottle {
		t.Fatalf("expected THROTTLED once the burst is spent, got %q", d.Reason)
	}

	// And half a second refills exactly one token
	clk.Add(500 * time.Millisecond)
	if d := tr.record(testKey, plan, clk.Now(), true); d.Reason != reasonNone {
		t.Fatalf("expected one refilled token after 500ms at 2/s, got %q", d.Reason)
	}
	if d := tr.record(testKey, plan, clk.Now(), true); d.Reason != reasonThrottle {
		t.Fatalf("expected the bucket to be empty again, got %q", d.Reason)
	}
}

func TestUsageTracker_burstCapsRefill(t *testing.T) {
	// Given a long idle period on a small bucket
	tr := newUsageTracker()
	clk := clock.NewMock()
	plan := planWith(&ThrottleSettings{BurstLimit: 2, RateLimit: 100}, nil)
	tr.record(testKey, plan, clk.Now(), true)

	// When an hour passes
	clk.Add(time.Hour)

	// Then the bucket holds burstLimit tokens, not an hour's worth
	for i := range 2 {
		if d := tr.record(testKey, plan, clk.Now(), true); d.Reason != reasonNone {
			t.Fatalf("request %d should be within the capped burst, got %q", i+1, d.Reason)
		}
	}
	if d := tr.record(testKey, plan, clk.Now(), true); d.Reason != reasonThrottle {
		t.Fatalf("expected the bucket to cap at burstLimit, got %q", d.Reason)
	}
}

func TestUsageTracker_quotaResetsOnPeriodRollover(t *testing.T) {
	// Given a daily quota of two requests, already spent
	tr := newUsageTracker()
	clk := clock.NewMock()
	plan := planWith(nil, &QuotaSettings{Limit: 2, Period: "DAY"})
	for range 2 {
		tr.record(testKey, plan, clk.Now(), true)
	}
	if d := tr.record(testKey, plan, clk.Now(), true); d.Reason != reasonQuota {
		t.Fatalf("expected QUOTA_EXCEEDED once the daily limit is spent, got %q", d.Reason)
	}

	// When the clock crosses midnight UTC
	clk.Add(24 * time.Hour)

	// Then the new period starts empty
	d := tr.record(testKey, plan, clk.Now(), true)
	if d.Reason != reasonNone {
		t.Fatalf("expected the quota to reset on the next day, got %q", d.Reason)
	}
	if d.Used != 1 || d.Remaining != 1 {
		t.Errorf("expected used=1 remaining=1 after rollover, got used=%d remaining=%d", d.Used, d.Remaining)
	}
}

func TestUsageTracker_rejectedRequestDoesNotConsumeQuota(t *testing.T) {
	// Given a daily quota of one, already spent, under enforcement
	tr := newUsageTracker()
	clk := clock.NewMock()
	plan := planWith(nil, &QuotaSettings{Limit: 1, Period: "DAY"})
	tr.record(testKey, plan, clk.Now(), true)

	// When two more requests are rejected
	for range 2 {
		if d := tr.record(testKey, plan, clk.Now(), true); d.Reason != reasonQuota {
			t.Fatalf("expected QUOTA_EXCEEDED, got %q", d.Reason)
		}
	}

	// Then the used count has not moved — a 429 does not count against the
	// quota on AWS either
	d := tr.record(testKey, plan, clk.Now(), true)
	if d.Used != 1 {
		t.Errorf("expected rejected requests not to be counted, got used=%d", d.Used)
	}
}

func TestUsageTracker_reportOnlyCountsPastTheLimit(t *testing.T) {
	// Given a daily quota of one and enforcement switched off
	tr := newUsageTracker()
	clk := clock.NewMock()
	plan := planWith(nil, &QuotaSettings{Limit: 1, Period: "DAY"})

	// When three requests are served over the limit
	var last usageDecision
	for range 3 {
		last = tr.record(testKey, plan, clk.Now(), false)
	}

	// Then all three are counted and the overshoot is visible
	if last.Used != 3 {
		t.Errorf("expected used=3 in report-only mode, got %d", last.Used)
	}
	if last.Remaining != -2 {
		t.Errorf("expected remaining=-2 to show the overshoot, got %d", last.Remaining)
	}
	if last.Reason != reasonQuota {
		t.Errorf("expected the decision to report QUOTA_EXCEEDED, got %q", last.Reason)
	}
	if last.QuotaRejects != 2 {
		t.Errorf("expected 2 would-be quota rejections, got %d", last.QuotaRejects)
	}
}

func TestUsageTracker_quotaOffsetAppliesToFirstPeriodOnly(t *testing.T) {
	// Given a daily quota of 3 with an offset of 2
	tr := newUsageTracker()
	clk := clock.NewMock()
	plan := planWith(nil, &QuotaSettings{Limit: 3, Offset: 2, Period: "DAY"})

	// When the first period is used
	d := tr.record(testKey, plan, clk.Now(), true)
	// Then only one request fits (3 − 2)
	if d.Remaining != 0 {
		t.Errorf("expected the offset to shrink the first period to 1, got remaining=%d", d.Remaining)
	}
	if d := tr.record(testKey, plan, clk.Now(), true); d.Reason != reasonQuota {
		t.Fatalf("expected the offset period to be exhausted, got %q", d.Reason)
	}

	// And the next period gets the full limit back
	clk.Add(24 * time.Hour)
	if d := tr.record(testKey, plan, clk.Now(), true); d.Remaining != 2 {
		t.Errorf("expected the full limit after the initial period, got remaining=%d", d.Remaining)
	}
}

func TestQuotaPeriodStart(t *testing.T) {
	// Given a Wednesday in the middle of a month
	now := time.Date(2026, 8, 5, 13, 45, 0, 0, time.UTC)

	tests := []struct {
		period string
		want   time.Time
	}{
		{"DAY", time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)},
		{"WEEK", time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)}, // Sunday
		{"MONTH", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)},
		{"", time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)}, // defaults to DAY
	}
	for _, tc := range tests {
		// When the period start is computed
		got := quotaPeriodStart(&QuotaSettings{Period: tc.period}, now)
		// Then it is calendar-aligned in UTC
		if !got.Equal(tc.want) {
			t.Errorf("period %q: expected %s, got %s", tc.period, tc.want, got)
		}
	}
}

func TestUsageTracker_usageForReportsDailyLog(t *testing.T) {
	// Given traffic on two consecutive days against a daily quota of 5
	tr := newUsageTracker()
	clk := clock.NewMock()
	plan := planWith(nil, &QuotaSettings{Limit: 5, Period: "DAY"})
	tr.record(testKey, plan, clk.Now(), true)
	tr.record(testKey, plan, clk.Now(), true)
	clk.Add(24 * time.Hour)
	tr.record(testKey, plan, clk.Now(), true)

	// When the log is read for a three-day window
	from := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	days := tr.usageFor(testKey, plan, from, from.AddDate(0, 0, 2))

	// Then it has one [used, remaining] pair per day
	if len(days) != 3 {
		t.Fatalf("expected 3 daily entries, got %d", len(days))
	}
	if days[0] != [2]int64{2, 3} {
		t.Errorf("day 1: expected [2 3], got %v", days[0])
	}
	if days[1] != [2]int64{1, 4} {
		t.Errorf("day 2: expected [1 4], got %v", days[1])
	}
	if days[2] != [2]int64{0, 5} {
		t.Errorf("day 3 (no traffic): expected [0 5], got %v", days[2])
	}
}

func TestUsageTracker_reportIsCoalesced(t *testing.T) {
	// Given an entry that has just reported
	tr := newUsageTracker()
	clk := clock.NewMock()
	tr.record(testKey, planWith(nil, nil), clk.Now(), false)
	if !tr.shouldReport(testKey, clk.Now()) {
		t.Fatal("expected the first report to be due")
	}

	// When another breach happens within the interval
	clk.Add(reportInterval / 2)

	// Then it is suppressed until the interval elapses
	if tr.shouldReport(testKey, clk.Now()) {
		t.Error("expected reports inside the interval to be coalesced")
	}
	clk.Add(reportInterval)
	if !tr.shouldReport(testKey, clk.Now()) {
		t.Error("expected a report once the interval elapsed")
	}
}
