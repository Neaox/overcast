package router

import (
	"testing"
	"time"
)

func TestCollectMetrics_startupAnchors(t *testing.T) {
	// Given: process creation happened well before Go code started
	origStartTime, origGoStartTime, origReadyTime := startTime, goStartTime, readyTime
	t.Cleanup(func() {
		startTime = origStartTime
		goStartTime = origGoStartTime
		readyTime = origReadyTime
	})
	procStart := time.Unix(100, 0)
	goStart := procStart.Add(1500 * time.Millisecond)
	ready := goStart.Add(25 * time.Millisecond)
	startTime = procStart
	goStartTime = goStart
	readyTime = ready

	// When: metrics are collected
	snap := collectMetrics()

	// Then: startup_duration_ms is Go-start to ready, and pre_init_ms is process-start to Go-start
	if snap.StartupDurationMs != 25 {
		t.Fatalf("startup_duration_ms = %v, want 25", snap.StartupDurationMs)
	}
	if snap.PreInitMs != 1500 {
		t.Fatalf("pre_init_ms = %v, want 1500", snap.PreInitMs)
	}
}

func TestEnvironmentPhaseLabel(t *testing.T) {
	// Given/When/Then: Linux PID 1 receives the container-specific label
	if got := environmentPhaseLabel("linux", 1); got != "container init + entrypoint + exec (pre-Go)" {
		t.Fatalf("linux pid 1 label = %q", got)
	}

	// Given/When/Then: non-PID 1 and non-Linux use the generic process-spawn label
	if got := environmentPhaseLabel("linux", 2); got != "OS process spawn: loader / AV / exec (pre-Go)" {
		t.Fatalf("linux pid 2 label = %q", got)
	}
	if got := environmentPhaseLabel("windows", 1); got != "OS process spawn: loader / AV / exec (pre-Go)" {
		t.Fatalf("windows pid 1 label = %q", got)
	}
}

func TestStartupProfilerFinalize_environmentPhase(t *testing.T) {
	// Given: process creation is one second before Go startup, and a Go phase starts at goStart
	origStartTime, origGoStartTime, origSealed := startTime, goStartTime, sealedPhases
	origExternal := externalPhases
	t.Cleanup(func() {
		startTime = origStartTime
		goStartTime = origGoStartTime
		sealedPhases = origSealed
		externalPhases = origExternal
	})
	procStart := time.Unix(100, 0)
	goStart := procStart.Add(time.Second)
	startTime = procStart
	goStartTime = goStart
	externalPhases = nil

	p := &startupProfiler{
		start: goStart,
		prev:  goStart,
		phases: []StartupPhase{{
			Name:       "Go runtime + package init",
			StartMs:    0,
			DurationMs: 10,
		}},
	}

	// When: phases are finalized
	p.finalize()

	// Then: the environment segment is first and flagged, while Go phases stay Go-anchored
	if len(sealedPhases) != 2 {
		t.Fatalf("expected 2 phases, got %d: %#v", len(sealedPhases), sealedPhases)
	}
	if !sealedPhases[0].Environment {
		t.Fatalf("expected first phase to be environment: %#v", sealedPhases[0])
	}
	if sealedPhases[0].DurationMs != 1000 {
		t.Fatalf("environment duration = %v, want 1000", sealedPhases[0].DurationMs)
	}
	if sealedPhases[1].StartMs != 0 {
		t.Fatalf("Go phase start_ms = %v, want 0", sealedPhases[1].StartMs)
	}
}
