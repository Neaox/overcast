package conformance

import (
	"regexp"
	"sync"
	"time"
)

// testClockTime is the fixed instant §3.5/timestamps freezes the fixture's
// clock at. Any wall-clock value works — what matters is that two separate
// Check runs set the same one and compare what came back.
func testClockTime() time.Time {
	return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
}

var (
	arnPatternsMu sync.Mutex
	arnPatterns   = map[string]*regexp.Regexp{}
)

// arnPattern returns (and caches) the §3.5 ARN template regexp for service:
// arn:aws:<service>:<region>:<account>:<path>.
func arnPattern(service string) *regexp.Regexp {
	arnPatternsMu.Lock()
	defer arnPatternsMu.Unlock()
	if re, ok := arnPatterns[service]; ok {
		return re
	}
	re := regexp.MustCompile(`^arn:aws:` + regexp.QuoteMeta(service) + `:[^:]*:[^:]*:.+$`)
	arnPatterns[service] = re
	return re
}
