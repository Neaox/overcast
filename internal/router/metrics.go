package router

import (
	"encoding/json"
	"net/http"
	"runtime"
	"time"
)

// metricsSnapshot is the JSON body returned by GET /_metrics.
// Exposes safe, read-only runtime statistics — no debug gate required.
type metricsSnapshot struct {
	// timing
	Timestamp         string         `json:"timestamp"`
	Uptime            string         `json:"uptime"`
	UptimeSecs        float64        `json:"uptime_secs"`
	StartTime         string         `json:"start_time"`
	StartupDurationMs float64        `json:"startup_duration_ms"`
	PreInitMs         float64        `json:"pre_init_ms"`
	StartupPhases     []StartupPhase `json:"startup_phases,omitempty"`
	// memory (bytes)
	HeapAllocBytes uint64 `json:"heap_alloc_bytes"`
	HeapSysBytes   uint64 `json:"heap_sys_bytes"`
	HeapInuseBytes uint64 `json:"heap_inuse_bytes"`
	SysBytes       uint64 `json:"sys_bytes"`
	StackInuse     uint64 `json:"stack_inuse_bytes"`
	// GC
	NumGC          uint32  `json:"num_gc"`
	GCPauseLastMs  float64 `json:"gc_pause_last_ms"`
	GCPauseTotalMs float64 `json:"gc_pause_total_ms"`
	NextGCBytes    uint64  `json:"next_gc_bytes"`
	// runtime
	Goroutines int    `json:"goroutines"`
	GoVersion  string `json:"go_version"`
	NumCPU     int    `json:"num_cpu"`
}

// readyTime records when the server finished initialization.
// Set by main after routes are registered; stays zero if not set.
var readyTime time.Time

// collectMetrics reads runtime.MemStats and other runtime counters.
// startTime is declared in debug.go (same package).
func collectMetrics() metricsSnapshot {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	var gcPauseLastMs float64
	if ms.NumGC > 0 {
		lastPause := ms.PauseNs[(ms.NumGC+255)%256]
		gcPauseLastMs = float64(lastPause) / 1e6
	}

	var startupMs float64
	if !readyTime.IsZero() {
		startupMs = float64(readyTime.Sub(goStartTime).Milliseconds())
		if startupMs < 0 {
			startupMs = 0
		}
	}
	preInitMs := float64(goStartTime.Sub(startTime).Milliseconds())
	if preInitMs < 0 {
		preInitMs = 0
	}

	return metricsSnapshot{
		Timestamp:         time.Now().UTC().Format(time.RFC3339),
		Uptime:            time.Since(startTime).Round(time.Second).String(),
		UptimeSecs:        time.Since(startTime).Seconds(),
		StartTime:         startTime.UTC().Format(time.RFC3339),
		StartupDurationMs: startupMs,
		PreInitMs:         preInitMs,
		StartupPhases:     GetStartupPhases(),
		HeapAllocBytes:    ms.HeapAlloc,
		HeapSysBytes:      ms.HeapSys,
		HeapInuseBytes:    ms.HeapInuse,
		SysBytes:          ms.Sys,
		StackInuse:        ms.StackInuse,
		NumGC:             ms.NumGC,
		GCPauseLastMs:     gcPauseLastMs,
		GCPauseTotalMs:    float64(ms.PauseTotalNs) / 1e6,
		NextGCBytes:       ms.NextGC,
		Goroutines:        runtime.NumGoroutine(),
		GoVersion:         runtime.Version(),
		NumCPU:            runtime.NumCPU(),
	}
}

// metricsHandler handles GET /_metrics.
// Always available — not gated by the debug flag.
func metricsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snap := collectMetrics()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(snap) //nolint:errcheck
	}
}
