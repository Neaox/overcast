package awscron

import (
	"strings"
	"testing"
	"time"
)

// base is a Tuesday, 21:20 UTC, so the expected firings below can be read
// against a fixed calendar.
var base = time.Date(2026, 8, 11, 21, 20, 34, 0, time.UTC)

func nextFrom(t *testing.T, expr string, from time.Time) time.Time {
	t.Helper()
	s, err := Parse(expr)
	if err != nil {
		t.Fatalf("Parse(%q): %v", expr, err)
	}
	next, ok := s.Next(from, from.Add(Horizon))
	if !ok {
		t.Fatalf("%q: no firing within the horizon", expr)
	}
	return next
}

func TestNext_everySupportedForm(t *testing.T) {
	// Every one of these is valid AWS cron. The ten marked below were rejected
	// before this package existed: EventBridge stored them and never fired
	// them, and the schedule engine refused them outright.
	for _, tc := range []struct {
		expr string
		want string
	}{
		{"cron(*/5 * * * ? *)", "2026-08-11T21:25:00Z"},
		{"cron(0/5 * * * ? *)", "2026-08-11T21:25:00Z"},
		{"cron(*/5 * ? * * *)", "2026-08-11T21:25:00Z"},
		{"cron(0 3 * * ? *)", "2026-08-12T03:00:00Z"},
		{"cron(0 5 1 * ? *)", "2026-09-01T05:00:00Z"},
		{"cron(0,30 * * * ? *)", "2026-08-11T21:30:00Z"},
		{"cron(0 0 1 * ? 2027)", "2027-01-01T00:00:00Z"},

		// Named weekdays and months.
		{"cron(15 10 ? * MON-FRI *)", "2026-08-12T10:15:00Z"},
		{"cron(0 18 ? * MON *)", "2026-08-17T18:00:00Z"},
		{"cron(0 8 1 JAN ? *)", "2027-01-01T08:00:00Z"},
		{"cron(0 8 1 JAN-MAR ? *)", "2027-01-01T08:00:00Z"},
		{"cron(0 12 ? * fri *)", "2026-08-14T12:00:00Z"}, // names are case-insensitive

		// A range with a step.
		{"cron(0 0-6/2 * * ? *)", "2026-08-12T00:00:00Z"},

		// L, W and #.
		{"cron(0 8 L * ? *)", "2026-08-31T08:00:00Z"},
		{"cron(0 8 LW * ? *)", "2026-08-31T08:00:00Z"},   // 31 Aug 2026 is a Monday
		{"cron(0 8 15W * ? *)", "2026-08-14T08:00:00Z"},  // 15 Aug 2026 is a Saturday, so the Friday
		{"cron(0 12 ? * 6#3 *)", "2026-08-21T12:00:00Z"}, // third Friday
		{"cron(0 12 ? * FRI#3 *)", "2026-08-21T12:00:00Z"},
		{"cron(0 12 ? * 6L *)", "2026-08-28T12:00:00Z"}, // last Friday
	} {
		if got := nextFrom(t, tc.expr, base).Format(time.RFC3339); got != tc.want {
			t.Errorf("%-26s next = %s, want %s", tc.expr, got, tc.want)
		}
	}
}

func TestParse_dayOfWeekIsAWSNumbering(t *testing.T) {
	// AWS numbers the days 1-7 from Sunday, so 2 is Monday — not Tuesday, as
	// Go's own time.Weekday would have it, and not out of range, as 7 was.
	if got := nextFrom(t, "cron(0 12 ? * 2 *)", base).Weekday(); got != time.Monday {
		t.Errorf("day-of-week 2 fired on %s, want Monday", got)
	}
	if got := nextFrom(t, "cron(0 12 ? * 7 *)", base).Weekday(); got != time.Saturday {
		t.Errorf("day-of-week 7 fired on %s, want Saturday", got)
	}
	if got := nextFrom(t, "cron(0 12 ? * SUN *)", base).Weekday(); got != time.Sunday {
		t.Errorf("day-of-week SUN fired on %s, want Sunday", got)
	}
}

func TestParse_rejectsWhatItCannotHonour(t *testing.T) {
	// Each error has to name the expression, and enough of what was wrong with
	// it to fix it without reading this code — the failure a user meets is a
	// deploy that stopped, not a stack trace.
	for _, tc := range []struct {
		expr, wantIn string
	}{
		{"cron(*/5 * * * *)", "has 5 fields"},
		{"cron(*/5 * * * ? * *)", "has 7 fields"},
		{"cron(0 12 ? * NOTADAY *)", "day-of-week"},
		{"cron(0 12 ? * MON-NOPE *)", "day-of-week"},
		{"cron(0 8 1 NOTAMONTH ? *)", "month"},
		{"cron(0 8 32W * ? *)", "day-of-month"},
		{"cron(0 12 ? * 6#9 *)", "occurrence from 1 to 5"},
		{"cron(*/0 * * * ? *)", "invalid step"},
		{"cron(*/5 * * * ? *", "closing parenthesis"},
	} {
		_, err := Parse(tc.expr)
		if err == nil {
			t.Errorf("Parse(%q) succeeded; want an error", tc.expr)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantIn) {
			t.Errorf("Parse(%q) error = %q, want it to mention %q", tc.expr, err, tc.wantIn)
		}
		if !strings.Contains(err.Error(), strings.TrimSuffix(tc.expr, ")")) {
			t.Errorf("Parse(%q) error = %q, want it to quote the expression", tc.expr, err)
		}
	}
}

func TestParse_fieldCountErrorShowsTheWorkingForm(t *testing.T) {
	// The five-field Unix cron is the mistake people actually make, and the
	// error is the only place they will find out what AWS wanted instead.
	_, err := Parse("cron(*/5 * * * *)")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "cron(*/5 * * * ? *)") {
		t.Errorf("error = %q, want it to show the six-field form", err)
	}
}

func TestNext_reportsNoFiringRatherThanSearchingForever(t *testing.T) {
	// 30 February never comes. The walk has to reach the horizon and say so
	// rather than run out the clock.
	s, err := Parse("cron(0 0 30 2 ? *)")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, ok := s.Next(base, base.Add(Horizon)); ok {
		t.Error("30 February reported a firing")
	}
}

func TestNext_isBoundedByFieldsNotByDistance(t *testing.T) {
	// A yearly expression is the case that cost 525,600 iterations when the
	// walk stepped a minute at a time; the field-jumping walk answers it in
	// the same handful of passes as a minutely one.
	if got := nextFrom(t, "cron(0 0 1 1 ? *)", base).Format(time.RFC3339); got != "2027-01-01T00:00:00Z" {
		t.Errorf("yearly next = %s, want 2027-01-01T00:00:00Z", got)
	}
}

func BenchmarkNext(b *testing.B) {
	for _, expr := range []string{"cron(*/5 * * * ? *)", "cron(0 0 1 1 ? *)", "cron(0 12 ? * 6#3 *)"} {
		s, err := Parse(expr)
		if err != nil {
			b.Fatalf("Parse(%q): %v", expr, err)
		}
		limit := base.Add(Horizon)
		b.Run(expr, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, ok := s.Next(base, limit); !ok {
					b.Fatal("no firing")
				}
			}
		})
	}
}

func BenchmarkParse(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Parse("cron(*/5 * ? * MON-FRI *)"); err != nil {
			b.Fatal(err)
		}
	}
}
