// Package awscron parses and evaluates AWS's six-field cron expressions —
// the ones EventBridge rules and EventBridge Scheduler schedules are written
// in, and the only cron dialect Overcast speaks.
//
// It exists because there were two of these, disagreeing. EventBridge matched
// a raw expression field by field on every candidate minute, which accepted
// syntax it did not understand — a rule written `cron(15 10 ? * MON-FRI *)`
// was stored happily and then never fired, because "MON-FRI" matched nothing
// and nothing said so. EventBridge Scheduler parsed up front and refused what
// it could not honour, which is the better failure, but numbered the days of
// the week 0-6 from Go's time.Weekday under a comment asserting AWS does the
// same. It does not: AWS numbers them 1-7 from Sunday, so that service fired
// `cron(0 12 ? * 2 *)` on Tuesday where AWS fires it on Monday, and never
// fired 7 at all, the value being outside its range.
//
// AWS's syntax, all of which is supported here:
//
//	field         values          wildcards
//	minute        0-59            , - * /
//	hour          0-23            , - * /
//	day-of-month  1-31            , - * ? / L W
//	month         1-12, JAN-DEC   , - * /
//	day-of-week   1-7, SUN-SAT    , - * ? / L #
//	year          1970-2199       , - * /
//
// Day-of-month and day-of-week are mutually exclusive: one of them must be
// "?", because a date cannot be selected by both readings at once. AWS rejects
// an expression that sets both to a value; an expression that leaves both
// unrestricted is the common `* * ? *` shape.
//
// # Evaluation
//
// Next advances field by field — year, month, day, hour, minute — each jump
// landing on the next value that field admits and resetting everything below
// it, so the cost is a handful of passes whatever the answer is. Stepping a
// minute at a time instead, as EventBridge did, costs one iteration per minute
// between now and the answer: 525,600 for a yearly expression, and 2.6 million
// before concluding that an expression it had failed to understand would never
// fire.
//
// A parsed expression holds its value sets in fixed-size bitmasks and its day
// specifiers in a fixed-size array, so parsing allocates nothing — which
// matters because the schedule engine parses every enabled expression on every
// tick.
package awscron

import (
	"errors"
	"fmt"
	"math/bits"
	"strconv"
	"strings"
	"time"
)

// Horizon is how far ahead an expression is searched before it is reported as
// one that will not fire. An expression whose next firing is beyond this —
// `cron(0 0 30 2 ? *)`, say, which names 30 February — is refused at the API
// rather than stored and silently never run.
const Horizon = 5 * 365 * 24 * time.Hour

// Field bounds, in the order AWS writes them. Day-of-week is 1-7 from Sunday,
// as AWS documents it, which is one more than Go's time.Weekday.
const (
	minuteMin, minuteMax = 0, 59
	hourMin, hourMax     = 0, 23
	domMin, domMax       = 1, 31
	monthMin, monthMax   = 1, 12
	dowMin, dowMax       = 1, 7
	yearMin, yearMax     = 1970, 2199
)

// awsWeekday converts a Go weekday to AWS's 1-7 numbering.
func awsWeekday(d time.Weekday) int { return int(d) + 1 }

// monthNames and dayNames are the three-letter abbreviations AWS accepts in
// place of a number, in the month and day-of-week fields respectively.
var monthNames = map[string]int{
	"JAN": 1, "FEB": 2, "MAR": 3, "APR": 4, "MAY": 5, "JUN": 6,
	"JUL": 7, "AUG": 8, "SEP": 9, "OCT": 10, "NOV": 11, "DEC": 12,
}

var dayNames = map[string]int{
	"SUN": 1, "MON": 2, "TUE": 3, "WED": 4, "THU": 5, "FRI": 6, "SAT": 7,
}

// Schedule is a parsed six-field AWS cron expression.
type Schedule struct {
	minute, hour, dom, month, dow, year field
	dayMode                             dayMode
	days                                dayRules
}

// Parse parses `cron(minute hour day-of-month month day-of-week year)`, with
// or without the surrounding `cron(...)`. Every error names the expression and
// the field that could not be read, because an expression is usually written
// by hand and the field is the only part worth pointing at.
func Parse(expr string) (Schedule, error) {
	var s Schedule
	inner := strings.TrimSpace(expr)
	if after, ok := strings.CutPrefix(inner, "cron("); ok {
		trimmed, closed := strings.CutSuffix(after, ")")
		if !closed {
			return s, fmt.Errorf("cron expression %q is missing its closing parenthesis", expr)
		}
		inner = trimmed
	}

	fields, count := splitFields(inner)
	if count != len(fieldSpecs) {
		return s, fmt.Errorf(
			"cron expression %q has %d fields; AWS cron takes 6: minute hour day-of-month month day-of-week year (for example, every five minutes is `cron(*/5 * * * ? *)`)",
			expr, count)
	}

	var parsed [6]field
	for i, fs := range fieldSpecs {
		f, err := parseField(fields[i], fs)
		if err != nil {
			return s, fmt.Errorf("invalid %s field in cron expression %q: %w", fs.name, expr, err)
		}
		parsed[i] = f
	}

	rules, err := parseDayRules(fields[2], fields[4])
	if err != nil {
		return s, fmt.Errorf("invalid %s field in cron expression %q: %w", err.field, expr, err.err)
	}

	return Schedule{
		minute: parsed[0], hour: parsed[1], dom: parsed[2],
		month: parsed[3], dow: parsed[4], year: parsed[5],
		dayMode: resolveDayMode(fields[2], fields[4]),
		days:    rules,
	}, nil
}

// Next returns the first minute at or after from that the expression matches,
// reporting false when there is none before limit.
//
// Each pass resolves the most significant field that does not yet match and
// jumps straight to that field's next admissible value, zeroing everything
// below it, so the number of passes is bounded by the fields rather than by
// the distance to the answer. Every jump moves strictly forward, so the walk
// always reaches limit.
func (s *Schedule) Next(from, limit time.Time) (time.Time, bool) {
	loc := from.Location()
	// Round up rather than down: a cron fires on whole minutes, and truncating
	// 21:20:34 to 21:20:00 would answer a "next" firing with a time that has
	// already passed.
	start := from.Truncate(time.Minute)
	if start.Before(from) {
		start = start.Add(time.Minute)
	}
	for t := start; t.Before(limit); {
		year, ok := s.year.nextAtOrAfter(t.Year())
		if !ok {
			return time.Time{}, false
		}
		if year != t.Year() {
			t = time.Date(year, time.January, 1, 0, 0, 0, 0, loc)
			continue
		}

		month, ok := s.month.nextAtOrAfter(int(t.Month()))
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
		// month lengths differ, and L/W/# are properties of a date rather than
		// of a number — so this one steps a day at a time. That is at most 31
		// passes, after which AddDate has rolled into the next month and the
		// checks above pick the walk back up.
		if !s.matchesDay(t) {
			t = startOfDay(t).AddDate(0, 0, 1)
			continue
		}

		hour, ok := s.hour.nextAtOrAfter(t.Hour())
		if !ok {
			t = startOfDay(t).AddDate(0, 0, 1)
			continue
		}
		if hour != t.Hour() {
			t = time.Date(t.Year(), t.Month(), t.Day(), hour, 0, 0, 0, loc)
			continue
		}

		minute, ok := s.minute.nextAtOrAfter(t.Minute())
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

// matchesDay reports whether t's date qualifies, by whichever of the two day
// fields the expression put in charge.
func (s *Schedule) matchesDay(t time.Time) bool {
	switch s.dayMode {
	case dayAlways:
		return true
	case dayByWeekday:
		return s.matchesWeekday(t)
	case dayByMonthDay:
		return s.matchesMonthDay(t)
	case dayByEither:
		// Both fields constrained: either one matching is enough, which is the
		// reading every other cron dialect gives it. AWS refuses the shape
		// outright; accepting it costs nothing and refusing it would fail a
		// template AWS would have failed first, at less useful a moment.
		return s.matchesMonthDay(t) || s.matchesWeekday(t)
	}
	return true
}

func (s *Schedule) matchesMonthDay(t time.Time) bool {
	return s.dom.matches(t.Day()) || s.days.matchesMonthDay(t)
}

func (s *Schedule) matchesWeekday(t time.Time) bool {
	return s.dow.matches(awsWeekday(t.Weekday())) || s.days.matchesWeekday(t)
}

// startOfDay returns midnight on t's own date.
func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// ─── Fields ───────────────────────────────────────────────────────────────────

type fieldSpec struct {
	name     string
	min, max int
	names    map[string]int
}

var fieldSpecs = [6]fieldSpec{
	{name: "minute", min: minuteMin, max: minuteMax},
	{name: "hour", min: hourMin, max: hourMax},
	{name: "day-of-month", min: domMin, max: domMax},
	{name: "month", min: monthMin, max: monthMax, names: monthNames},
	{name: "day-of-week", min: dowMin, max: dowMax, names: dayNames},
	{name: "year", min: yearMin, max: yearMax},
}

// fieldWords is how many 64-bit words a field's value set needs. The widest
// field is the year, whose 1970–2199 range is 230 values.
const fieldWords = 4

// field is one parsed field: the values, within that field's own range, that
// it admits, as a bitmask offset from min.
type field struct {
	min, max int
	any      bool // "*" or "?" — every value in range
	bits     [fieldWords]uint64
}

func (f *field) add(v int) {
	if v < f.min || v > f.max {
		return
	}
	i := v - f.min
	f.bits[i/64] |= 1 << uint(i%64)
}

func (f *field) matches(v int) bool {
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
// reporting false when it admits none — the caller's signal to carry into the
// field above.
func (f *field) nextAtOrAfter(v int) (int, bool) {
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
	for word := i / 64; word < fieldWords; word++ {
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

// splitFields splits on whitespace into a fixed array, returning how many
// fields were found. Fixed rather than strings.Fields so it allocates nothing;
// a count above six is reported as the count so the error can say so.
func splitFields(inner string) (fields [6]string, count int) {
	for i := 0; i < len(inner); {
		for i < len(inner) && isSpace(inner[i]) {
			i++
		}
		if i >= len(inner) {
			break
		}
		start := i
		for i < len(inner) && !isSpace(inner[i]) {
			i++
		}
		if count < len(fields) {
			fields[count] = inner[start:i]
		}
		count++
	}
	return fields, count
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

// parseField parses one field: `*`, `?`, or a comma-separated list of values,
// ranges (`9-17`, `MON-FRI`) and steps (`*/5`, `0/15`, `9-17/2`).
//
// The day specifiers L, W and # are not values, so they are skipped here and
// read by parseDayRules; a part made only of them contributes nothing to the
// value set, and a field made only of them admits no plain value at all.
func parseField(text string, spec fieldSpec) (field, error) {
	f := field{min: spec.min, max: spec.max}
	text = strings.TrimSpace(text)
	if text == "*" || text == "?" {
		f.any = true
		return f, nil
	}
	if text == "" {
		return f, errors.New("empty field")
	}

	for rest := text; ; {
		part, after, more := strings.Cut(rest, ",")
		if isDaySpecifier(part) {
			// Read as a rule rather than a value; see parseDayRules.
			if !isDayField(spec) {
				return field{}, fmt.Errorf("%q is only valid in the day-of-month and day-of-week fields", strings.TrimSpace(part))
			}
		} else {
			first, last, step, err := parsePart(part, spec)
			if err != nil {
				return field{}, err
			}
			for v := first; v <= last; v += step {
				f.add(v)
			}
		}
		if !more {
			break
		}
		rest = after
	}
	return f, nil
}

func isDayField(spec fieldSpec) bool {
	return spec.name == "day-of-month" || spec.name == "day-of-week"
}

// parsePart resolves one comma-separated part to the arithmetic sequence it
// stands for, already trimmed to the field's range so the caller's walk is
// bounded by the range rather than by whatever numbers the expression named.
func parsePart(part string, spec fieldSpec) (first, last, step int, err error) {
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
			first, last = spec.min, spec.max
		default:
			if lo, hi, isRange, rangeErr := cutRange(base, spec); rangeErr != nil {
				return 0, 0, 0, rangeErr
			} else if isRange {
				// A step over a range walks the range: `9-17/4` is 9, 13, 17.
				first, last = lo, hi
			} else {
				// A step over a bare value runs from that value to the end of
				// the field: `0/15` is every quarter hour, not minute zero.
				start, valueErr := parseValue(base, spec)
				if valueErr != nil {
					return 0, 0, 0, valueErr
				}
				first, last = start, spec.max
			}
		}
	} else if lo, hi, isRange, rangeErr := cutRange(part, spec); rangeErr != nil {
		return 0, 0, 0, rangeErr
	} else if isRange {
		first, last = lo, hi
	} else {
		value, valueErr := parseValue(part, spec)
		if valueErr != nil {
			return 0, 0, 0, valueErr
		}
		first, last = value, value
	}

	// Keep the sequence on its own grid while bounding the walk: the start
	// advances by whole steps, and the end is simply truncated.
	if first < spec.min {
		first += ((spec.min - first + step - 1) / step) * step
	}
	if last > spec.max {
		last = spec.max
	}
	return first, last, step, nil
}

// cutRange parses `lo-hi`, in numbers or in names, reporting false when the
// text is not a range at all.
func cutRange(part string, spec fieldSpec) (lo, hi int, ok bool, err error) {
	loText, hiText, found := strings.Cut(part, "-")
	if !found {
		return 0, 0, false, nil
	}
	lo, err = parseValue(loText, spec)
	if err != nil {
		return 0, 0, false, err
	}
	hi, err = parseValue(hiText, spec)
	if err != nil {
		return 0, 0, false, err
	}
	return lo, hi, true, nil
}

// parseValue reads one value: a number, or one of the field's three-letter
// names. AWS accepts the names case-insensitively.
func parseValue(text string, spec fieldSpec) (int, error) {
	text = strings.TrimSpace(text)
	if spec.names != nil {
		if v, ok := spec.names[strings.ToUpper(text)]; ok {
			return v, nil
		}
	}
	v, err := strconv.Atoi(text)
	if err != nil {
		if spec.names != nil {
			return 0, fmt.Errorf("%q is not a number or one of %s", text, nameList(spec.names))
		}
		return 0, fmt.Errorf("%q is not a number", text)
	}
	return v, nil
}

// nameList renders a field's accepted names for an error message, in value
// order so the reader sees SUN..SAT rather than map order.
func nameList(names map[string]int) string {
	ordered := make([]string, len(names))
	for name, v := range names {
		ordered[v-1] = name
	}
	return strings.Join(ordered, ", ")
}

// ─── Day specifiers: L, W and # ───────────────────────────────────────────────

// dayMode is how a schedule decides whether a date's day qualifies. AWS's
// day-of-month and day-of-week fields are mutually exclusive — one of them is
// written "?" — so which field is consulted is settled at parse time.
type dayMode int

const (
	dayAlways     dayMode = iota // both fields wild
	dayByMonthDay                // day-of-week is "?"
	dayByWeekday                 // day-of-month is "?"
	dayByEither                  // both constrained: either one matching is enough
)

func resolveDayMode(dom, dow string) dayMode {
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

// dayRuleKind is which of AWS's date-shaped day specifiers a rule is.
type dayRuleKind uint8

const (
	ruleLastDayOfMonth     dayRuleKind = iota + 1 // L in day-of-month
	ruleLastWeekdayOfMonth                        // LW in day-of-month
	ruleNearestWeekday                            // <day>W in day-of-month
	ruleLastWeekdayInMonth                        // <dow>L in day-of-week
	ruleNthWeekday                                // <dow>#<n> in day-of-week
)

// dayRule is one parsed day specifier. It is a property of a date rather than
// of a number, so it is evaluated per candidate day rather than folded into a
// value set.
type dayRule struct {
	kind dayRuleKind
	// day is the day-of-month for ruleNearestWeekday; weekday is AWS's 1-7 for
	// the two day-of-week rules; nth is which occurrence ruleNthWeekday wants.
	day, weekday, nth int
}

// maxDayRules bounds a list like `1W,15W,LW`. Four is past anything AWS
// documents as useful and keeps the parsed form allocation-free.
const maxDayRules = 4

type dayRules struct {
	rules [maxDayRules]dayRule
	count int
}

func (d *dayRules) add(r dayRule) error {
	if d.count >= maxDayRules {
		return fmt.Errorf("more than %d day specifiers", maxDayRules)
	}
	d.rules[d.count] = r
	d.count++
	return nil
}

// isDaySpecifier reports whether a part is one of AWS's date-shaped day
// specifiers rather than a plain value, range or step.
func isDaySpecifier(part string) bool {
	part = strings.ToUpper(strings.TrimSpace(part))
	return strings.ContainsAny(part, "LW#")
}

// fieldError carries which field a day specifier came from, so Parse can name
// it the way it names every other field's errors.
type fieldError struct {
	field string
	err   error
}

// parseDayRules reads the L, W and # specifiers out of the two day fields.
func parseDayRules(dom, dow string) (dayRules, *fieldError) {
	var rules dayRules
	for _, f := range [...]struct {
		name  string
		text  string
		parse func(string) (dayRule, error)
	}{
		{fieldSpecs[2].name, dom, parseMonthDayRule},
		{fieldSpecs[4].name, dow, parseWeekdayRule},
	} {
		for rest := strings.TrimSpace(f.text); rest != ""; {
			part, after, more := strings.Cut(rest, ",")
			if isDaySpecifier(part) {
				rule, err := f.parse(part)
				if err != nil {
					return rules, &fieldError{field: f.name, err: err}
				}
				if err := rules.add(rule); err != nil {
					return rules, &fieldError{field: f.name, err: err}
				}
			}
			if !more {
				break
			}
			rest = after
		}
	}
	return rules, nil
}

// parseMonthDayRule reads a day-of-month specifier: `L`, `LW`, or `<day>W`.
func parseMonthDayRule(part string) (dayRule, error) {
	text := strings.ToUpper(strings.TrimSpace(part))
	switch text {
	case "L":
		return dayRule{kind: ruleLastDayOfMonth}, nil
	case "LW":
		return dayRule{kind: ruleLastWeekdayOfMonth}, nil
	}
	if base, ok := strings.CutSuffix(text, "W"); ok {
		day, err := strconv.Atoi(base)
		if err != nil || day < domMin || day > domMax {
			return dayRule{}, fmt.Errorf("%q is not a day of the month followed by W", part)
		}
		return dayRule{kind: ruleNearestWeekday, day: day}, nil
	}
	return dayRule{}, fmt.Errorf("%q is not one of L, LW or <day>W", part)
}

// parseWeekdayRule reads a day-of-week specifier: `L`, `<dow>L` or `<dow>#<n>`.
func parseWeekdayRule(part string) (dayRule, error) {
	text := strings.ToUpper(strings.TrimSpace(part))
	// A bare L in day-of-week is the last day of the week, which AWS numbers 7.
	if text == "L" {
		return dayRule{kind: ruleLastWeekdayInMonth, weekday: dowMax}, nil
	}
	if base, ok := strings.CutSuffix(text, "L"); ok {
		weekday, err := parseWeekdayValue(base)
		if err != nil {
			return dayRule{}, err
		}
		return dayRule{kind: ruleLastWeekdayInMonth, weekday: weekday}, nil
	}
	if base, nth, ok := strings.Cut(text, "#"); ok {
		weekday, err := parseWeekdayValue(base)
		if err != nil {
			return dayRule{}, err
		}
		n, err := strconv.Atoi(strings.TrimSpace(nth))
		if err != nil || n < 1 || n > 5 {
			return dayRule{}, fmt.Errorf("%q must name an occurrence from 1 to 5 after #", part)
		}
		return dayRule{kind: ruleNthWeekday, weekday: weekday, nth: n}, nil
	}
	return dayRule{}, fmt.Errorf("%q is not one of L, <day>L or <day>#<n>", part)
}

func parseWeekdayValue(text string) (int, error) {
	return parseValue(text, fieldSpecs[4])
}

// matchesMonthDay reports whether any day-of-month rule admits t's date.
func (d *dayRules) matchesMonthDay(t time.Time) bool {
	for i := 0; i < d.count; i++ {
		r := d.rules[i]
		switch r.kind {
		case ruleLastDayOfMonth:
			if t.Day() == lastDayOfMonth(t) {
				return true
			}
		case ruleLastWeekdayOfMonth:
			if t.Day() == lastWeekdayOfMonth(t) {
				return true
			}
		case ruleNearestWeekday:
			if t.Day() == nearestWeekday(t, r.day) {
				return true
			}
		case ruleLastWeekdayInMonth, ruleNthWeekday:
			// Day-of-week rules; matchesWeekday answers those.
		}
	}
	return false
}

// matchesWeekday reports whether any day-of-week rule admits t's date.
func (d *dayRules) matchesWeekday(t time.Time) bool {
	for i := 0; i < d.count; i++ {
		r := d.rules[i]
		if awsWeekday(t.Weekday()) != r.weekday {
			continue
		}
		switch r.kind {
		case ruleLastWeekdayInMonth:
			if t.Day()+7 > lastDayOfMonth(t) {
				return true
			}
		case ruleNthWeekday:
			if (t.Day()-1)/7+1 == r.nth {
				return true
			}
		case ruleLastDayOfMonth, ruleLastWeekdayOfMonth, ruleNearestWeekday:
			// Day-of-month rules; matchesMonthDay answers those.
		}
	}
	return false
}

// lastDayOfMonth returns the number of days in t's month.
func lastDayOfMonth(t time.Time) int {
	return time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, t.Location()).Day()
}

// lastWeekdayOfMonth returns the last Monday-to-Friday day of t's month.
func lastWeekdayOfMonth(t time.Time) int {
	day := lastDayOfMonth(t)
	for {
		d := time.Date(t.Year(), t.Month(), day, 0, 0, 0, 0, t.Location())
		if isWeekday(d.Weekday()) {
			return day
		}
		day--
	}
}

// nearestWeekday returns the Monday-to-Friday day nearest to the given day of
// t's month, without crossing into another month — AWS's `<day>W`.
func nearestWeekday(t time.Time, day int) int {
	last := lastDayOfMonth(t)
	if day > last {
		day = last
	}
	target := time.Date(t.Year(), t.Month(), day, 0, 0, 0, 0, t.Location())
	switch target.Weekday() {
	case time.Saturday:
		if day > 1 {
			return day - 1
		}
		return day + 2 // Saturday the 1st: the following Monday
	case time.Sunday:
		if day < last {
			return day + 1
		}
		return day - 2 // Sunday the last: the preceding Friday
	case time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday:
		return day // already a weekday
	}
	return day
}

func isWeekday(d time.Weekday) bool {
	return d != time.Saturday && d != time.Sunday
}
