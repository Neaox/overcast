package scheduler

// Schedule-expression coverage.
//
// The engine evaluates every enabled schedule's expression on every tick, and
// the API evaluates it again on every CreateSchedule and UpdateSchedule, so
// this is the emulator's hottest pure function. These cases pin what each
// expression form resolves to — including the awkward corners: the AWS
// dom/dow interaction, a leap-day-only schedule, and an expression that can
// never come round.

import (
	"testing"
	"time"
)

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return parsed
}

func TestNextFireTime(t *testing.T) {
	// Given: one expression per form the engine accepts, evaluated from a
	// fixed "now" — Sunday 9 August 2026, 12:34:56 UTC unless stated.
	const now = "2026-08-09T12:34:56Z"

	cases := []struct {
		name     string
		expr     string
		lastFire string // RFC3339, or "" for never fired
		now      string // RFC3339, or "" for the default above
		want     string // RFC3339, or "" when a zero time is expected
		wantErr  bool
	}{
		// rate
		{name: "rate fires at once when never fired", expr: "rate(1 minute)", want: now},
		{name: "rate counts from the last firing", expr: "rate(5 minutes)",
			lastFire: "2026-08-09T12:00:00Z", want: "2026-08-09T12:05:00Z"},
		{name: "rate accepts a singular unit", expr: "rate(2 hour)",
			lastFire: "2026-08-09T12:00:00Z", want: "2026-08-09T14:00:00Z"},
		{name: "rate accepts days", expr: "rate(1 day)",
			lastFire: "2026-08-09T12:00:00Z", want: "2026-08-10T12:00:00Z"},
		{name: "rate rejects a zero value", expr: "rate(0 minutes)", wantErr: true},
		{name: "rate rejects an unknown unit", expr: "rate(1 fortnight)", wantErr: true},

		// at
		{name: "at returns its own instant", expr: "at(2026-12-25T09:00:00)",
			want: "2026-12-25T09:00:00Z"},
		{name: "at rejects an unparseable instant", expr: "at(christmas)", wantErr: true},

		// cron — the everyday forms
		{name: "cron daily, later today", expr: "cron(0 12 * * ? *)",
			now: "2026-08-09T06:00:00Z", want: "2026-08-09T12:00:00Z"},
		{name: "cron daily, already past today", expr: "cron(0 12 * * ? *)",
			want: "2026-08-10T12:00:00Z"},
		{name: "cron step minutes", expr: "cron(0/15 * * * ? *)", want: "2026-08-09T12:45:00Z"},
		{name: "cron minute list", expr: "cron(0 0,12 * * ? *)",
			now: "2026-08-09T06:00:00Z", want: "2026-08-09T12:00:00Z"},
		{name: "cron hour range", expr: "cron(0 9-17 * * ? *)",
			now: "2026-08-09T12:30:00Z", want: "2026-08-09T13:00:00Z"},
		// A step over a range is stepped from the range's own start. The
		// minute-by-minute evaluator could not read `9-17/4` and silently
		// treated it as `*/4`, so this fired at 16:00 — off the range's grid.
		{name: "cron range with a step", expr: "cron(0 9-17/4 * * ? *)",
			want: "2026-08-09T13:00:00Z"},
		{name: "cron step from a bare value", expr: "cron(0/20 * * * ? *)",
			want: "2026-08-09T12:40:00Z"},
		{name: "cron continues from the last firing", expr: "cron(0/5 * * * ? *)",
			lastFire: "2026-08-09T12:00:00Z", now: "2026-08-09T12:07:00Z",
			want: "2026-08-09T12:05:00Z"},

		// cron — day-of-month and day-of-week
		{name: "cron day of month", expr: "cron(0 0 15 * ? *)", want: "2026-08-15T00:00:00Z"},
		{name: "cron day of week", expr: "cron(30 9 ? * 1 *)", want: "2026-08-10T09:30:00Z"},
		{name: "cron with both day fields set matches either",
			expr: "cron(0 0 1 * 5 *)", want: "2026-08-14T00:00:00Z"},

		// cron — the sparse ones
		{name: "cron monthly", expr: "cron(0 0 1 * ? *)", want: "2026-09-01T00:00:00Z"},
		{name: "cron yearly", expr: "cron(0 0 1 1 ? *)", want: "2027-01-01T00:00:00Z"},
		{name: "cron leap day only", expr: "cron(0 0 29 2 ? *)", want: "2028-02-29T00:00:00Z"},
		{name: "cron with an explicit year", expr: "cron(0 0 1 1 ? 2028)",
			want: "2028-01-01T00:00:00Z"},

		// cron — refusals
		{name: "cron with a date that never comes round", expr: "cron(0 0 30 2 ? *)", wantErr: true},
		{name: "cron beyond the five-year horizon", expr: "cron(0 0 1 1 ? 2040)", wantErr: true},
		{name: "cron with the wrong field count", expr: "cron(0 12 * * ?)", wantErr: true},
		{name: "unknown expression form", expr: "every 5 minutes", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nowValue := tc.now
			if nowValue == "" {
				nowValue = now
			}
			var lastFire time.Time
			if tc.lastFire != "" {
				lastFire = mustTime(t, tc.lastFire)
			}

			// When: the next fire time is computed
			got, err := nextFireTime(tc.expr, lastFire, mustTime(t, nowValue))

			// Then: it lands on the instant AWS's expression names
			if tc.wantErr {
				if err == nil {
					t.Fatalf("nextFireTime(%q) = %s, want an error", tc.expr, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("nextFireTime(%q): %v", tc.expr, err)
			}
			if want := mustTime(t, tc.want); !got.Equal(want) {
				t.Fatalf("nextFireTime(%q) = %s, want %s", tc.expr, got.Format(time.RFC3339), tc.want)
			}
		})
	}
}
