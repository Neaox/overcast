package router

import (
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

// goroutineMonitor samples goroutine states on a regular interval. A warning
// is emitted when a significant number of goroutines have been blocked in the
// same state for longer than ageThreshold — a reliable signal of a real leak.
//
// Why age-based rather than count-based?
// A raw count threshold creates false positives during normal activity: Lambda
// warm containers hold ~4 goroutines each, Docker log streams hold 2, and HTTP
// keep-alive connections hold 1. These are all legitimate and transient. The Go
// runtime appends the blocking duration to the goroutine state label once a
// goroutine has been blocked for > 1 minute (e.g. "[IO wait, 20 minutes]").
// Leaked goroutines accumulate age; transient ones clear before the threshold.
//
// Strategy:
//  1. Cheap: runtime.NumGoroutine() every checkInterval — no STW, skip everything else
//     if count is clearly fine.
//  2. Gate: require count to exceed countGate on stwGate consecutive checks before
//     doing the expensive stack snap — prevents a transient burst from triggering
//     a stop-the-world pause.
//  3. Expensive (stop-the-world): runtime.Stack() only when both gates pass, then
//     count goroutines with blocking duration > ageThreshold.
//  4. Fire when "old" goroutine count >= oldCountThreshold.
//
// The monitor is only started in debug mode (cfg.Debug == true).
type goroutineMonitor struct {
	logger            *zap.Logger
	checkInterval     time.Duration // how often to sample; 1 min in production
	countGate         int           // skip STW stack snap when total count is below this
	stwGate           int           // consecutive elevated checks required before STW snap
	ageThreshold      time.Duration // goroutines blocked longer than this are "old"; 0 = count all
	oldCountThreshold int           // fire when this many "old" goroutines are detected
	cooldown          time.Duration // silence period after firing; summary log still fires
	stopCh            chan struct{}
}

// newGoroutineMonitor returns a monitor with production defaults.
//
//   - checkInterval:     	1 min  	— leaks are persistent; no need to sample rapidly
//   - countGate:        	150     — skip everything when goroutine count is clearly fine
//   - stwGate:            	2     	— require 2 consecutive elevated checks before STW snap; prevents a single transient burst from causing a stop-the-world pause
//   - ageThreshold:     	15 min  — matches Lambda pool idle TTL; legitimate goroutines (warm containers, Docker streams) all clear before this
//   - oldCountThreshold: 	50     	— 50 goroutines stuck for 15+ min is unambiguously a leak
//   - cooldown:          	5 min  	— suppress repeated full dumps; summary log still fires
func newGoroutineMonitor(logger *zap.Logger) *goroutineMonitor {
	return &goroutineMonitor{
		logger:            logger,
		checkInterval:     time.Minute,
		countGate:         150,
		stwGate:           2,
		ageThreshold:      15 * time.Minute,
		oldCountThreshold: 50,
		cooldown:          5 * time.Minute,
		stopCh:            make(chan struct{}),
	}
}

// Start launches the background sampling goroutine.
func (m *goroutineMonitor) Start() {
	go m.run()
}

// Stop signals the background goroutine to exit.
func (m *goroutineMonitor) Stop() {
	close(m.stopCh)
}

func (m *goroutineMonitor) run() {
	ticker := time.NewTicker(m.checkInterval)
	defer ticker.Stop()

	var consecutiveElevated int // resets to 0 whenever count drops below countGate
	var lastDump time.Time      // zero = never dumped

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			total := runtime.NumGoroutine()

			// Stage 1: fast gate — free atomic read, no STW.
			if total < m.countGate {
				consecutiveElevated = 0
				continue
			}
			consecutiveElevated++

			// Stage 2: require stwGate consecutive elevated checks before paying
			// the stop-the-world cost. A single burst (e.g. a batch of Lambda
			// cold starts) clears before the next tick and never triggers a snap.
			if consecutiveElevated < m.stwGate {
				continue
			}

			// Stage 3: during cooldown skip the STW snap; just log a summary.
			if !lastDump.IsZero() && time.Since(lastDump) < m.cooldown {
				m.logger.Warn("goroutine leak still present",
					zap.Int("total_goroutines", total),
					zap.String("pprof_tip", "curl 'http://localhost:4566/_debug/pprof/goroutine?debug=2'"),
				)
				continue
			}

			// Stage 4: stop-the-world stack capture + age analysis.
			// Each goroutine header includes its blocking duration when it has
			// been stuck for more than a minute, e.g. "[IO wait, 20 minutes]".
			buf := make([]byte, 4<<20) // 4 MiB
			n := runtime.Stack(buf, true)
			stacks := string(buf[:n])

			old := countOldGoroutines(stacks, m.ageThreshold)
			if old < m.oldCountThreshold {
				continue
			}

			m.logger.Warn("possible goroutine leak: goroutines blocked past age threshold",
				zap.Int("old_goroutines", old),
				zap.Int("total_goroutines", total),
				zap.Duration("age_threshold", m.ageThreshold),
				zap.String("pprof_tip", "curl 'http://localhost:4566/_debug/pprof/goroutine?debug=2'"),
				zap.String("stacks", stacks),
			)
			lastDump = time.Now()
		}
	}
}

// goroutineAgeRe matches the optional wait-duration in a goroutine header line.
// The Go runtime appends it once a goroutine has been blocked for over a minute.
// Examples:
//
//	goroutine 42 [IO wait, 20 minutes]:
//	goroutine 7  [chan receive, 1 minute]:
//	goroutine 99 [select, 2 hours]:
var goroutineAgeRe = regexp.MustCompile(`\[.*,\s*(\d+)\s*(minute|hour)s?\]`)

// countOldGoroutines returns the number of goroutines in the stack dump whose
// blocking duration meets or exceeds minAge. When minAge is zero all goroutine
// headers are counted (age filtering disabled — used in tests).
func countOldGoroutines(stacks string, minAge time.Duration) int {
	minMins := int(minAge.Minutes())
	count := 0
	for _, line := range strings.Split(stacks, "\n") {
		if !strings.HasPrefix(line, "goroutine ") {
			continue
		}
		if minMins == 0 {
			// Age filter disabled — count every goroutine header.
			count++
			continue
		}
		m := goroutineAgeRe.FindStringSubmatch(line)
		if m == nil {
			continue // no duration → fresh or actively running goroutine
		}
		n, _ := strconv.Atoi(m[1])
		if m[2] == "hour" {
			n *= 60
		}
		if n >= minMins {
			count++
		}
	}
	return count
}
