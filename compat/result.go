// Package compat provides shared Go types for the NDJSON wire format emitted
// by all per-language test suite runners.
//
// Every runner (Node.js, Python, Go, CLI, …) writes one JSON line per event
// to stdout. The Go runner in runner.go reads these lines, aggregates them,
// and builds a RunReport for display or further processing.
package compat

import "time"

// EventType identifies the kind of NDJSON event.
type EventType string

const (
	EventRunStart      EventType = "run_start"
	EventSuiteStarting EventType = "suite_starting" // emitted by the runner before a suite subprocess starts
	EventSuiteError    EventType = "suite_error"    // emitted when a suite subprocess fails to start or crashes
	EventTestStart     EventType = "test_start"
	EventTestResult    EventType = "test_result"
	EventRunEnd        EventType = "run_end"
)

// Status is the outcome of a single test.
type Status string

const (
	StatusPass Status = "pass"
	StatusFail Status = "fail"
	StatusSkip Status = "skip"
	// StatusUnimplemented indicates the endpoint returned HTTP 501.
	// The feature gap is known and expected; this is distinct from a real
	// failure (wrong response, assertion error, SDK crash).
	StatusUnimplemented Status = "unimplemented"
	// StatusNA indicates the AWS SDK client used by this suite does not yet
	// expose this operation.  It is NOT an Overcast gap and NOT a suite
	// authoring gap — simply that the SDK library has no API for it yet.
	// NA results are excluded from all pass-rate calculations.
	StatusNA Status = "na"
)

// RunStartEvent is the first line emitted by a suite runner.
type RunStartEvent struct {
	Event      EventType `json:"event"`
	Suite      string    `json:"suite"`
	StartedAt  time.Time `json:"started_at"`
	Endpoint   string    `json:"endpoint"`
	Version    string    `json:"version"`
	TotalTests int       `json:"total_tests,omitempty"`
}

// TestStartEvent is emitted once per test, immediately before it begins executing.
// Consumers use this to show a test as "running" while awaiting the result.
type TestStartEvent struct {
	Event   EventType `json:"event"`
	Suite   string    `json:"suite"`
	Service string    `json:"service"`
	Group   string    `json:"group"`
	Test    string    `json:"test"`
}

// TestResultEvent is emitted once per test, immediately after it completes.
type TestResultEvent struct {
	Event   EventType `json:"event"`
	Suite   string    `json:"suite"`
	Service string    `json:"service"`
	Group   string    `json:"group"`
	Test    string    `json:"test"`
	// Op is the AWS API operation name used for documentation links.
	// Empty string disables the doc link. When absent, Test is used.
	Op         string `json:"op,omitempty"`
	Status     Status `json:"status"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

// RunEndEvent is the last line emitted by a suite runner.
type RunEndEvent struct {
	Event         EventType `json:"event"`
	Suite         string    `json:"suite"`
	Passed        int       `json:"passed"`
	Failed        int       `json:"failed"`
	Skipped       int       `json:"skipped"`
	Unimplemented int       `json:"unimplemented"`
	DurationMS    int64     `json:"duration_ms"`
}

// RawEvent is used to peek at the "event" field before full unmarshalling.
type RawEvent struct {
	Event EventType `json:"event"`
}

// RunReport is the aggregated result of one or more suite runs.
// Built by runner.go from the streamed NDJSON events.
type RunReport struct {
	Endpoint   string
	StartedAt  time.Time
	FinishedAt time.Time
	Suites     []*SuiteReport
}

// SuiteReport is the aggregated result of a single suite (e.g. node-js-sdk).
type SuiteReport struct {
	Suite         string
	Groups        []*GroupReport
	Passed        int
	Failed        int
	Skipped       int
	Unimplemented int
}

// GroupReport is the aggregated result of one test group within a suite.
type GroupReport struct {
	Suite         string
	Service       string
	Name          string
	Tests         []TestResultEvent
	Passed        int
	Failed        int
	Skipped       int
	Unimplemented int
}

// Services returns a deduplicated list of service names tested in this suite.
func (s *SuiteReport) Services() []string {
	seen := make(map[string]struct{})
	var out []string
	for _, g := range s.Groups {
		if _, ok := seen[g.Service]; !ok {
			seen[g.Service] = struct{}{}
			out = append(out, g.Service)
		}
	}
	return out
}

// Total returns the total number of tests in this suite.
func (s *SuiteReport) Total() int { return s.Passed + s.Failed + s.Skipped + s.Unimplemented }

// PassRate returns the pass rate as a value in [0, 1]. Returns 0 for empty suites.
// Unimplemented tests are excluded from both numerator and denominator —
// they represent known gaps, not implementation quality.
func (s *SuiteReport) PassRate() float64 {
	t := s.Passed + s.Failed
	if t == 0 {
		return 0
	}
	return float64(s.Passed) / float64(t)
}
