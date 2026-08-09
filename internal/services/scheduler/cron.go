package scheduler

// Schedule-expression evaluation — rate(), at() and AWS's six-field cron().
//
// This is the emulator's hottest pure function: the engine evaluates every
// enabled schedule's expression on every tick, and CreateSchedule and
// UpdateSchedule evaluate it again to decide whether the expression is one the
// engine can honour.
//
// cron() is evaluated by advancing field by field — year, then month, then
// day, then hour, then minute — each jump landing on the next value that field
// admits and resetting everything below it. The previous evaluator stepped one
// minute at a time from now until something matched, so it cost one iteration
// per minute between now and the answer: a daily schedule cost up to 1,440, a
// monthly one up to 44,640, and a yearly one up to 525,600 — every second, for
// every such schedule. Advancing by field costs a handful of passes whatever
// the answer is.
//
// A parsed expression is held in fixed-size bitmasks rather than in slices, so
// parsing allocates nothing and the dense expressions — which the old evaluator
// answered in a couple of iterations and so never had to parse in full — do not
// pay for the sparse ones.
//
// Not supported, as before: the L, W and # day specifiers, and the three-letter
// month and day names. An expression using one is refused by CreateSchedule
// rather than stored and never fired.

import (
	"errors"
	"fmt"
	"math/bits"
	"strconv"
	"strings"
	"time"
)

// cronHorizon is how far ahead a cron expression is searched before the
// emulator gives up on it. An expression whose next firing is beyond this —
// `cron(0 0 30 2 ? *)`, say, which names 30 February — is reported as one the
// engine cannot honour, which is what stops it being stored.
const cronHorizon = 5 * 365 * 24 * time.Hour

// Field bounds, in the order AWS writes them.
const (
	cronMinuteMin, cronMinuteMax = 0, 59
	cronHourMin, cronHourMax     = 0, 23
	cronDayMin, cronDayMax       = 1, 31
	cronMonthMin, cronMonthMax   = 1, 12
	cronDowMin, cronDowMax       = 0, 6 // 0 = Sunday, in Go and in AWS alike
	cronYearMin, cronYearMax     = 1970, 2099
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

// nextCronFire parses a 6-field AWS cron expression and computes the next fire
// time after lastFire (or now if never fired).
func nextCronFire(expr string, lastFire, now time.Time) (time.Time, error) {
	spec, err := parseCron(expr)
	if err != nil {
		return time.Time{}, err
	}
	from := now
	if !lastFire.IsZero() {
		from = lastFire.Add(time.Minute) // search from 1 minute after the last fire
	}
	next, found := spec.next(from.Truncate(time.Minute), now.Add(cronHorizon))
	if !found {
		return time.Time{}, fmt.Errorf("cron expression %q has no next fire within 5 years", expr)
	}
	return next, nil
}

// ─── Parsed form ──────────────────────────────────────────────────────────────

// cronFieldWords is how many 64-bit words a field's value set needs. The widest
// field is the year, whose 1970–2099 range is 130 values.
const cronFieldWords = 3

// cronField is one parsed field: the values, within that field's own range,
// that it admits, as a bitmask offset from min.
type cronField struct {
	min, max int
	any      bool // "*" or "?" — every value in range
	bits     [cronFieldWords]uint64
}

// add admits v, ignoring a value outside the field's range.
func (f *cronField) add(v int) {
	if v < f.min || v > f.max {
		return
	}
	i := v - f.min
	f.bits[i/64] |= 1 << uint(i%64)
}

// matches reports whether the field admits v.
func (f *cronField) matches(v int) bool {
	if v < f.min || v > f.max {
		return false
	}
	if f.any {
		return true
	}
	i := v - f.min
	return f.bits[i/64]&(1<<uint(i%64)) != 0
}

// nextAtOrAfter returns the smallest value the field admits that is at least v,
// reporting false when it admits none — which is the caller's signal to carry
// into the field above.
func (f *cronField) nextAtOrAfter(v int) (int, bool) {
	if v < f.min {
		v = f.min
	}
	if v > f.max {
		return 0, false
	}
	if f.any {
		return v, true
	}
	i := v - f.min
	for word := i / 64; word < cronFieldWords; word++ {
		w := f.bits[word]
		if word == i/64 {
			w &^= 1<<uint(i%64) - 1 // drop the values below v
		}
		if w != 0 {
			return f.min + word*64 + bits.TrailingZeros64(w), true
		}
	}
	return 0, false
}

// dayMatchMode is how a spec decides whether a date's day qualifies. AWS's
// day-of-month and day-of-week fields are mutually exclusive — one of them is
// written "?" — so which field is consulted is settled at parse time.
type dayMatchMode int

const (
	dayAlways     dayMatchMode = iota // both fields wild
	dayByMonthDay                     // day-of-week is "?"
	dayByWeekday                      // day-of-month is "?"
	dayByEither                       // both constrained: either one matching is enough
)

// cronSpec is a parsed six-field AWS cron expression. It holds no slices and no
// pointers, so parsing one costs no allocation.
type cronSpec struct {
	minute, hour, dom, month, dow, year cronField
	dayMode                             dayMatchMode
}

// cronFieldSpecs names each field and its range, in the order AWS writes them.
var cronFieldSpecs = [6]struct {
	name     string
	min, max int
}{
	{"minute", cronMinuteMin, cronMinuteMax},
	{"hour", cronHourMin, cronHourMax},
	{"day-of-month", cronDayMin, cronDayMax},
	{"month", cronMonthMin, cronMonthMax},
	{"day-of-week", cronDowMin, cronDowMax},
	{"year", cronYearMin, cronYearMax},
}

// parseCron parses `cron(min hour dom month dow year)`.
func parseCron(expr string) (cronSpec, error) {
	var spec cronSpec
	inner := strings.TrimSuffix(strings.TrimPrefix(expr, "cron("), ")")
	fields, count := splitCronFields(inner)
	if count != len(cronFieldSpecs) {
		return spec, fmt.Errorf("aws cron must have 6 fields, got %d: %q", count, expr)
	}

	var parsed [6]cronField
	for i, fs := range cronFieldSpecs {
		field, err := parseCronField(fields[i], fs.min, fs.max)
		if err != nil {
			return spec, fmt.Errorf("invalid %s field in %q: %w", fs.name, expr, err)
		}
		parsed[i] = field
	}

	spec = cronSpec{
		minute: parsed[0], hour: parsed[1], dom: parsed[2],
		month: parsed[3], dow: parsed[4], year: parsed[5],
		dayMode: dayMode(fields[2], fields[4]),
	}
	return spec, nil
}

// splitCronFields splits on whitespace into a fixed array, returning how many
// fields were found. Fixed rather than strings.Fields so it allocates nothing;
// a count above six is reported as the count so the error can say so.
func splitCronFields(inner string) (fields [6]string, count int) {
	for i := 0; i < len(inner); {
		for i < len(inner) && isASCIISpace(inner[i]) {
			i++
		}
		if i >= len(inner) {
			break
		}
		start := i
		for i < len(inner) && !isASCIISpace(inner[i]) {
			i++
		}
		if count < len(fields) {
			fields[count] = inner[start:i]
		}
		count++
	}
	return fields, count
}

func isASCIISpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

// dayMode resolves the day-of-month / day-of-week interaction from the two
// fields as written. "?" is AWS's "the other field decides"; the both-set case
// falls back to the OR semantics other cron dialects use.
func dayMode(dom, dow string) dayMatchMode {
	domWild := dom == "*" || dom == "?"
	dowWild := dow == "*" || dow == "?"
	switch {
	case domWild && dowWild:
		return dayAlways
	case dom == "?":
		return dayByWeekday
	case dow == "?":
		return dayByMonthDay
	default:
		return dayByEither
	}
}

// parseCronField parses one field: `*`, `?`, or a comma-separated list of
// values, ranges (`9-17`) and steps (`*/5`, `0/15`, `9-17/2`).
//
// A value outside the field's range is dropped rather than rejected, which is
// in effect what the minute-by-minute evaluator did with one: it never matched,
// and the expression was then reported as having no next firing.
func parseCronField(text string, min, max int) (cronField, error) {
	field := cronField{min: min, max: max}
	text = strings.TrimSpace(text)
	if text == "*" || text == "?" {
		field.any = true
		return field, nil
	}
	if text == "" {
		return field, errors.New("empty field")
	}

	for rest := text; ; {
		part, after, more := strings.Cut(rest, ",")
		first, last, step, err := parseCronPart(part, min, max)
		if err != nil {
			return cronField{}, err
		}
		for v := first; v <= last; v += step {
			field.add(v)
		}
		if !more {
			break
		}
		rest = after
	}
	return field, nil
}

// parseCronPart resolves one comma-separated part to the arithmetic sequence it
// stands for, already trimmed to the field's range so the caller's walk is
// bounded by the range rather than by whatever numbers the expression named.
func parseCronPart(part string, min, max int) (first, last, step int, err error) {
	part = strings.TrimSpace(part)
	step = 1
	if base, stepText, isStep := strings.Cut(part, "/"); isStep {
		step, err = strconv.Atoi(strings.TrimSpace(stepText))
		if err != nil || step <= 0 {
			return 0, 0, 0, fmt.Errorf("invalid step %q", part)
		}
		base = strings.TrimSpace(base)
		switch {
		case base == "*" || base == "?":
			first, last = min, max
		default:
			if lo, hi, isRange := cutRange(base); isRange {
				// A step over a range walks the range: `9-17/4` is 9, 13, 17.
				first, last = lo, hi
			} else {
				// A step over a bare value runs from that value to the end of
				// the field: `0/15` is every quarter hour, not minute zero.
				start, convErr := strconv.Atoi(base)
				if convErr != nil {
					return 0, 0, 0, fmt.Errorf("invalid step base %q", base)
				}
				first, last = start, max
			}
		}
	} else if lo, hi, isRange := cutRange(part); isRange {
		first, last = lo, hi
	} else {
		value, convErr := strconv.Atoi(part)
		if convErr != nil {
			return 0, 0, 0, fmt.Errorf("invalid value %q", part)
		}
		first, last = value, value
	}

	// Keep the sequence on its own grid while bounding the walk: the start
	// advances by whole steps, and the end is simply truncated.
	if first < min {
		first += ((min - first + step - 1) / step) * step
	}
	if last > max {
		last = max
	}
	return first, last, step, nil
}

// cutRange parses `lo-hi`, reporting false when the text is not a range.
func cutRange(part string) (lo, hi int, ok bool) {
	loText, hiText, found := strings.Cut(part, "-")
	if !found {
		return 0, 0, false
	}
	lo, loErr := strconv.Atoi(strings.TrimSpace(loText))
	hi, hiErr := strconv.Atoi(strings.TrimSpace(hiText))
	if loErr != nil || hiErr != nil {
		return 0, 0, false
	}
	return lo, hi, true
}

// ─── Evaluation ───────────────────────────────────────────────────────────────

// next returns the first minute at or after from that the expression matches,
// reporting false when there is none before limit.
//
// Each pass resolves the most significant field that does not yet match and
// jumps straight to that field's next admissible value, zeroing everything
// below it, so the number of passes is bounded by the fields rather than by the
// distance to the answer. Every jump moves strictly forward, so the walk always
// reaches limit.
func (c *cronSpec) next(from, limit time.Time) (time.Time, bool) {
	loc := from.Location()
	for t := from; t.Before(limit); {
		year, ok := c.year.nextAtOrAfter(t.Year())
		if !ok {
			return time.Time{}, false
		}
		if year != t.Year() {
			t = time.Date(year, time.January, 1, 0, 0, 0, 0, loc)
			continue
		}

		month, ok := c.month.nextAtOrAfter(int(t.Month()))
		if !ok {
			t = time.Date(t.Year()+1, time.January, 1, 0, 0, 0, 0, loc)
			continue
		}
		if month != int(t.Month()) {
			t = time.Date(t.Year(), time.Month(month), 1, 0, 0, 0, 0, loc)
			continue
		}

		// The day fields do not decompose into "the next admissible value" the
		// way the others do — the day-of-week reading depends on the calendar,
		// and the month lengths differ — so this one steps a day at a time.
		// That is at most 31 passes, after which AddDate has rolled into the
		// next month and the checks above pick the walk back up.
		if !c.matchesDay(t) {
			t = startOfDay(t).AddDate(0, 0, 1)
			continue
		}

		hour, ok := c.hour.nextAtOrAfter(t.Hour())
		if !ok {
			t = startOfDay(t).AddDate(0, 0, 1)
			continue
		}
		if hour != t.Hour() {
			t = time.Date(t.Year(), t.Month(), t.Day(), hour, 0, 0, 0, loc)
			continue
		}

		minute, ok := c.minute.nextAtOrAfter(t.Minute())
		if !ok {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, loc).Add(time.Hour)
			continue
		}
		if minute != t.Minute() {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), minute, 0, 0, loc)
			continue
		}
		return t, true
	}
	return time.Time{}, false
}

// matchesDay reports whether t's date qualifies under the day fields.
func (c *cronSpec) matchesDay(t time.Time) bool {
	switch c.dayMode {
	case dayAlways:
		return true
	case dayByWeekday:
		return c.dow.matches(int(t.Weekday()))
	case dayByMonthDay:
		return c.dom.matches(t.Day())
	case dayByEither:
		return c.dom.matches(t.Day()) || c.dow.matches(int(t.Weekday()))
	default:
		return true
	}
}

// startOfDay returns midnight on t's own date.
func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}
