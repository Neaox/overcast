package router

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// StartupPhase records a single timed phase during server initialization.
// Exposed via /_metrics to power the startup timeline visualisation in the web UI.
// StartMs is milliseconds since the OS process started (same reference as startup_duration_ms).
type StartupPhase struct {
	Name       string  `json:"name"`
	StartMs    float64 `json:"start_ms"`    // ms since OS process start
	DurationMs float64 `json:"duration_ms"` // ms this phase took
}

// externalPhases accumulates phases recorded before router.New is called —
// config load, store init, etc., from cmd/overcast. Protected by externalMu.
// Merged into sealedPhases by startupProfiler.finalize().
//
// Pre-sized generously to avoid any reallocation during the handful of
// pre-router phases (config load, store init, hook discovery, etc.).
var (
	externalMu     sync.Mutex
	externalPhases = make([]StartupPhase, 0, 16)
)

// sealedPhases is written once by startupProfiler.finalize() and then
// read-only for the lifetime of the process.
var sealedPhases []StartupPhase

// RecordExternalPhase appends a startup phase from outside the router package
// (e.g. cmd/overcast config load, store init). These are merged into the
// sealed timeline when router.New completes.
// Always records regardless of OVERCAST_PROFILE_STARTUP.
func RecordExternalPhase(name string, start, end time.Time) {
	phase := StartupPhase{
		Name:       name,
		StartMs:    float64(start.Sub(startTime).Microseconds()) / 1000,
		DurationMs: float64(end.Sub(start).Microseconds()) / 1000,
	}
	externalMu.Lock()
	externalPhases = append(externalPhases, phase)
	externalMu.Unlock()
}

// GetStartupPhases returns the sealed startup phase timeline.
// Returns nil until router.New has returned — phases are sealed then.
func GetStartupPhases() []StartupPhase {
	return sealedPhases // written once; read-only thereafter, no lock needed
}

// ProcessStartTime returns the OS process start time that anchors all phase
// measurements. Exported so cmd/overcast can anchor its phase timer to the
// same reference point as the router profiler, producing honest totals.
func ProcessStartTime() time.Time { return startTime }

// startupProfiler emits per-phase timings to stderr when
// OVERCAST_PROFILE_STARTUP=1 is set, and always records phases for the UI.
//
// Output format (tab-separated for easy awk/cut):
//
//	startup-profile  <phase>  +<ms-since-prev>ms  (=<ms-since-process-start>ms)
//
// No mutex: router.New is single-threaded — all mark() and finalize() calls
// happen on the same goroutine. The mutex-free design also means recording
// phases adds no synchronisation overhead to startup.
type startupProfiler struct {
	enabled bool
	start   time.Time      // = startTime (process start) for the (=...) total column
	prev    time.Time      // tracks last mark time
	phases  []StartupPhase // router-internal phases, merged by finalize()
}

var startupProfileEnabled = os.Getenv("OVERCAST_PROFILE_STARTUP") == "1"

// newStartupProfiler creates a profiler for use inside router.New.
// prev is set to time.Now() (not startTime) so each phase's duration reflects
// work done inside router.New, not the entire time from process start.
// The (=...) total column in stderr output still uses startTime as its origin,
// giving an honest process-relative total consistent with phaseTimer output.
//
// phases is pre-sized to 150 to cover all expected marks (~90+ service marks)
// without any allocation during the body of router.New.
func newStartupProfiler() *startupProfiler {
	return &startupProfiler{
		enabled: startupProfileEnabled,
		start:   startTime,
		prev:    time.Now(),                   // anchor to when router.New was called
		phases:  make([]StartupPhase, 0, 150), // pre-sized: eliminates all slice growth
	}
}

// mark records a phase boundary. Always records the phase for the metrics API;
// conditionally writes to stderr when OVERCAST_PROFILE_STARTUP=1.
//
// No mutex: mark is only ever called from the single goroutine running
// router.New. The hot path (profiling disabled) is just two time.Now() calls,
// a subtraction, and a slice append into pre-allocated capacity — adding
// roughly 150 ns per call, or ~15 µs total across all marks.
func (p *startupProfiler) mark(phase string) {
	now := time.Now()
	prev := p.prev
	p.prev = now

	p.phases = append(p.phases, StartupPhase{
		Name:       phase,
		StartMs:    float64(prev.Sub(startTime).Microseconds()) / 1000,
		DurationMs: float64(now.Sub(prev).Microseconds()) / 1000,
	})

	if !p.enabled {
		return
	}
	delta := now.Sub(prev)
	total := now.Sub(p.start)
	fmt.Fprintf(os.Stderr, "startup-profile\t%-40s\t+%6.2fms\t(=%6.2fms)\n",
		phase, float64(delta.Microseconds())/1000, float64(total.Microseconds())/1000)
}

// finalize seals the startup phase timeline into sealedPhases.
// Called at the end of router.New so that /_metrics can expose the full
// phase breakdown. Merges external phases (pre-router) with router phases.
// Must be called from the same goroutine as mark() (i.e. inside router.New).
func (p *startupProfiler) finalize() {
	externalMu.Lock()
	ext := make([]StartupPhase, len(externalPhases))
	copy(ext, externalPhases)
	externalMu.Unlock()

	// p.phases is accessed without a lock — safe because finalize is called
	// from the same single goroutine as all mark() calls.
	all := make([]StartupPhase, 0, len(ext)+len(p.phases))
	all = append(all, ext...)
	all = append(all, p.phases...)
	sealedPhases = all // write once, then read-only
}
