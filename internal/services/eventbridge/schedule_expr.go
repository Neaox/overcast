package eventbridge

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Neaox/overcast/internal/awscron"
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
		return nextCronFire(expr, lastFire, now)
	}
	return time.Time{}, fmt.Errorf("unknown schedule expression: %q", expr)
}

// nextCronFire is the shared AWS cron parser, which both this service and
// EventBridge Scheduler evaluate their expressions with. PutRule calls it to
// decide whether an expression is one Overcast can honour, so an expression it
// cannot read is refused there rather than stored and never fired.
func nextCronFire(expr string, lastFire, now time.Time) (time.Time, error) {
	schedule, err := awscron.Parse(expr)
	if err != nil {
		return time.Time{}, err
	}
	from := now
	if !lastFire.IsZero() {
		from = lastFire.Add(time.Minute)
	}
	next, ok := schedule.Next(from, now.Add(awscron.Horizon))
	if !ok {
		return time.Time{}, fmt.Errorf("cron expression %q has no firing within the next five years", expr)
	}
	return next, nil
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

func regionFromRuleKey(key, fallback string) string {
	region, _, ok := strings.Cut(key, "/")
	if !ok || region == "" {
		return fallback
	}
	return region
}
