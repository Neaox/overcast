package scheduler

// Schedule-expression evaluation — rate(), at() and AWS's six-field cron().
//
// This is the emulator's hottest pure function: the engine evaluates every
// enabled schedule's expression on every tick, and CreateSchedule and
// UpdateSchedule evaluate it again to decide whether the expression is one the
// engine can honour.
//
// cron() is parsed and evaluated by internal/awscron, which EventBridge rules
// share. It used to be parsed here, by a copy that numbered the days of the
// week 0-6 from Go's time.Weekday rather than 1-7 from Sunday as AWS does — so
// `cron(0 12 ? * 2 *)` fired on Tuesday where AWS fires it on Monday, and 7
// never fired at all — and that understood neither the three-letter month and
// day names nor the L, W and # day specifiers.

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Neaox/overcast/internal/awscron"
)

// nextFireTime computes the next time that expr should fire, given the last fire
// time and the current time. Returns zero time when no future firing applies
// (e.g. a one-shot "at" expression that has already fired).
func nextFireTime(expr string, lastFire, now time.Time) (time.Time, error) {
	expr = strings.TrimSpace(expr)
	switch {
	case strings.HasPrefix(expr, "rate("):
		return nextRateFire(expr, lastFire, now)
	case strings.HasPrefix(expr, "at("):
		return nextAtFire(expr)
	case strings.HasPrefix(expr, "cron("):
		return nextCronFire(expr, lastFire, now)
	default:
		return time.Time{}, fmt.Errorf("unknown expression type: %q", expr)
	}
}

// nextRateFire parses a rate expression and returns the next fire time.
func nextRateFire(expr string, lastFire, now time.Time) (time.Time, error) {
	// rate(N unit)
	inner := strings.TrimSuffix(strings.TrimPrefix(expr, "rate("), ")")
	inner = strings.TrimSpace(inner)
	parts := strings.Fields(inner)
	if len(parts) != 2 {
		return time.Time{}, fmt.Errorf("invalid rate expression: %q", expr)
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil || n <= 0 {
		return time.Time{}, fmt.Errorf("invalid rate value: %q", parts[0])
	}
	// AWS writes the unit singular for a value of 1 and plural otherwise, and
	// accepts either, so the trailing "s" is dropped before matching.
	unit := strings.TrimSuffix(strings.ToLower(parts[1]), "s")
	var period time.Duration
	switch unit {
	case "minute":
		period = time.Duration(n) * time.Minute
	case "hour":
		period = time.Duration(n) * time.Hour
	case "day":
		period = time.Duration(n) * 24 * time.Hour
	default:
		return time.Time{}, fmt.Errorf("unknown rate unit: %q", parts[1])
	}

	if lastFire.IsZero() {
		// Never fired: fire immediately on first tick after creation.
		return now, nil
	}
	return lastFire.Add(period), nil
}

// nextAtFire parses an at expression and returns the fire time (or zero if past).
func nextAtFire(expr string) (time.Time, error) {
	// at(yyyy-mm-ddThh:mm:ss)
	inner := strings.TrimSuffix(strings.TrimPrefix(expr, "at("), ")")
	inner = strings.TrimSpace(inner)
	t, err := time.Parse("2006-01-02T15:04:05", inner)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid at expression: %q: %w", expr, err)
	}
	return t, nil
}

// nextCronFire computes the next fire time after lastFire (or now if never
// fired). An expression the parser cannot honour is an error here, which is
// what stops CreateSchedule storing a schedule that would never run.
func nextCronFire(expr string, lastFire, now time.Time) (time.Time, error) {
	schedule, err := awscron.Parse(expr)
	if err != nil {
		return time.Time{}, err
	}
	from := now
	if !lastFire.IsZero() {
		from = lastFire.Add(time.Minute) // search from 1 minute after the last fire
	}
	next, ok := schedule.Next(from, now.Add(awscron.Horizon))
	if !ok {
		return time.Time{}, fmt.Errorf("cron expression %q has no firing within the next five years", expr)
	}
	return next, nil
}
