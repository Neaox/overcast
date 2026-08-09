package scheduler

// Benchmark for the schedule-expression evaluator.
//
// This runs once per enabled schedule per engine tick — once a second — and
// again on every CreateSchedule and UpdateSchedule, so its cost is paid
// continuously rather than per request. The cases are ordered by how far ahead
// the next firing is, because that is what the cost used to scale with: a
// minute-by-minute search paid one iteration per minute between now and the
// answer, so a yearly cron cost most of a million iterations a second and a
// rate expression cost none.
//
//	go test -run '^$' -bench BenchmarkNextFireTime -benchmem -count 3 ./internal/services/scheduler/

import (
	"testing"
	"time"
)

func BenchmarkNextFireTime(b *testing.B) {
	cases := []struct {
		name string
		expr string
		now  time.Time
	}{
		{name: "rate_1_minute", expr: "rate(1 minute)"},
		{name: "cron_every_5_minutes", expr: "cron(0/5 * * * ? *)"},
		{name: "cron_daily", expr: "cron(0 12 * * ? *)"},
		{name: "cron_monthly", expr: "cron(0 0 1 * ? *)"},
		{name: "cron_yearly", expr: "cron(0 0 1 1 ? *)"},
		// The worst case for a minute-by-minute search: a yearly schedule
		// evaluated just after it last came round, so the answer is a full
		// year — 525,599 minutes — away.
		{
			name: "cron_yearly_just_missed",
			expr: "cron(0 0 1 1 ? *)",
			now:  time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC),
		},
	}

	defaultNow := time.Date(2026, 8, 9, 12, 34, 56, 0, time.UTC)
	for _, tc := range cases {
		now := tc.now
		if now.IsZero() {
			now = defaultNow
		}
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				if _, err := nextFireTime(tc.expr, time.Time{}, now); err != nil {
					b.Fatalf("nextFireTime(%q): %v", tc.expr, err)
				}
			}
		})
	}
}
