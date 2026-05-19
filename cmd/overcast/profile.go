package main

import (
	"fmt"
	"os"
	"time"

	"github.com/Neaox/overcast/internal/router"
)

// phaseTimer records startup phases from before router.New is called.
// It is anchored to the OS process start time via router.ProcessStartTime(),
// so the (=...) totals in its stderr output are comparable to the router
// profiler's totals — both reference the same process-start origin.
// All phases are always recorded into the router package's phase log
// regardless of OVERCAST_PROFILE_STARTUP, so the startup timeline is always
// available via /_metrics.
type phaseTimer struct {
	enabled bool
	start   time.Time // = OS process start time
	prev    time.Time
}

func newPhaseTimer(enabled bool) *phaseTimer {
	start := router.ProcessStartTime()
	return &phaseTimer{enabled: enabled, start: start, prev: start}
}

func (p *phaseTimer) mark(phase string) {
	now := time.Now()
	prev := p.prev
	p.prev = now

	// Always record for the /_metrics startup timeline, regardless of the
	// OVERCAST_PROFILE_STARTUP flag.
	router.RecordExternalPhase(phase, prev, now)

	if !p.enabled {
		return
	}
	delta := now.Sub(prev)
	total := now.Sub(p.start)
	fmt.Fprintf(os.Stderr, "startup-profile\t%-40s\t+%6.2fms\t(=%6.2fms)\n",
		phase, float64(delta.Microseconds())/1000, float64(total.Microseconds())/1000)
}
