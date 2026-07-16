package eventbridge

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Neaox/overcast/internal/protocol"
)

func scheduleValidationError(err error) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ValidationException",
		Message:    err.Error(),
		HTTPStatus: http.StatusBadRequest,
	}
}

func nextRuleFire(expr string, lastFire, now time.Time) (time.Time, error) {
	expr = strings.TrimSpace(expr)
	if strings.HasPrefix(expr, "rate(") {
		return nextRateFire(expr, lastFire, now)
	}
	if strings.HasPrefix(expr, "cron(") {
		if lastFire.IsZero() {
			lastFire = now.Add(-time.Minute)
		}
		return nextCronFire(expr, lastFire, now)
	}
	return time.Time{}, fmt.Errorf("unknown schedule expression: %q", expr)
}

func nextRateFire(expr string, lastFire, now time.Time) (time.Time, error) {
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(expr, "rate("), ")"))
	parts := strings.Fields(inner)
	if len(parts) != 2 {
		return time.Time{}, fmt.Errorf("invalid rate expression: %q", expr)
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil || n <= 0 {
		return time.Time{}, fmt.Errorf("invalid rate value: %q", parts[0])
	}
	unit := strings.ToLower(strings.TrimSuffix(parts[1], "s"))
	var period time.Duration
	switch unit {
	case "minute":
		period = time.Duration(n) * time.Minute
	case "hour":
		period = time.Duration(n) * time.Hour
	case "day":
		period = time.Duration(n) * 24 * time.Hour
	default:
		return time.Time{}, fmt.Errorf("invalid rate unit: %q", parts[1])
	}
	if lastFire.IsZero() {
		return now, nil
	}
	return lastFire.Add(period), nil
}

func nextCronFire(expr string, lastFire, now time.Time) (time.Time, error) {
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(expr, "cron("), ")"))
	fields := strings.Fields(inner)
	if len(fields) != 6 {
		return time.Time{}, fmt.Errorf("aws cron must have 6 fields")
	}
	limit := now.Add(5 * 365 * 24 * time.Hour)
	for t := lastFire.Add(time.Minute).Truncate(time.Minute); t.Before(limit); t = t.Add(time.Minute) {
		if matchCronField(fields[5], t.Year(), 1970, 2199) && matchCronField(fields[3], int(t.Month()), 1, 12) && matchCronDay(fields[2], fields[4], t) && matchCronField(fields[1], t.Hour(), 0, 23) && matchCronField(fields[0], t.Minute(), 0, 59) {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cron expression has no next fire within search window")
}

func matchCronDay(dom, dow string, t time.Time) bool {
	domOK := matchCronField(dom, t.Day(), 1, 31)
	dowVal := int(t.Weekday()) + 1 // AWS EventBridge uses 1-7 as SUN-SAT.
	dowOK := matchCronField(dow, dowVal, 1, 7)
	if dom != "?" && dow != "?" {
		return domOK || dowOK
	}
	return domOK && dowOK
}

func regionFromRuleKey(key, fallback string) string {
	region, _, ok := strings.Cut(key, "/")
	if !ok || region == "" {
		return fallback
	}
	return region
}

func matchCronField(spec string, value, min, max int) bool {
	if spec == "*" || spec == "?" {
		return true
	}
	for _, part := range strings.Split(spec, ",") {
		if matchCronPart(part, value, min, max) {
			return true
		}
	}
	return false
}

func matchCronPart(part string, value, min, max int) bool {
	if strings.Contains(part, "/") {
		pieces := strings.SplitN(part, "/", 2)
		step, err := strconv.Atoi(pieces[1])
		if err != nil || step <= 0 {
			return false
		}
		start := min
		if pieces[0] != "*" && pieces[0] != "?" {
			parsed, err := strconv.Atoi(pieces[0])
			if err != nil {
				return false
			}
			start = parsed
		}
		return value >= start && (value-start)%step == 0
	}
	if strings.Contains(part, "-") {
		pieces := strings.SplitN(part, "-", 2)
		lo, err1 := strconv.Atoi(pieces[0])
		hi, err2 := strconv.Atoi(pieces[1])
		return err1 == nil && err2 == nil && value >= lo && value <= hi
	}
	n, err := strconv.Atoi(part)
	return err == nil && n >= min && n <= max && value == n
}
