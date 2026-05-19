//go:build linux

package router

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// processStartTime returns the wall-clock time the kernel launched this
// process, derived from /proc/uptime and /proc/self/stat.
//
// /proc/stat's btime field is in whole seconds — it has no sub-second
// precision. Using btime + startTicks/100 introduces a systematic overcount
// equal to the fractional part of the boot epoch, which can be hundreds of
// milliseconds. This method avoids btime entirely:
//
//	process_start = now − (uptime_secs − startTicks/clkTck)
//
// /proc/uptime provides sub-second (centisecond) precision for system uptime,
// so the only remaining error is at most ±10ms from the 100 Hz tick resolution
// of startTicks — acceptable for startup profiling.
//
// Falls back to time.Now() on any parse error.
func processStartTime() time.Time {
	// The Linux kernel ABI guarantees /proc/self/stat starttime is expressed
	// in USER_HZ = 100 jiffies per second, regardless of the kernel's HZ
	// config. This is fixed in the kernel's exported interface (fs/proc/array.c)
	// and safe to hardcode.
	const clkTck = 100

	uptimeSecs, err := readUptime()
	if err != nil {
		return time.Now()
	}

	startTicks, err := readProcStartTicks()
	if err != nil {
		return time.Now()
	}

	// Seconds since the process started (fractional, centisecond resolution).
	secsSinceStart := uptimeSecs - float64(startTicks)/clkTck
	if secsSinceStart < 0 {
		// Shouldn't happen, but guard against clock skew.
		return time.Now()
	}

	elapsed := time.Duration(secsSinceStart * float64(time.Second))
	return time.Now().Add(-elapsed)
}

// readUptime parses the system uptime in fractional seconds from /proc/uptime.
// The kernel writes this with centisecond (10ms) resolution.
func readUptime() (float64, error) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		return 0, fmt.Errorf("empty /proc/uptime")
	}
	return strconv.ParseFloat(fields[0], 64)
}

// readProcStartTicks returns the starttime field (field 22, clock ticks
// since boot) from /proc/self/stat.
func readProcStartTicks() (uint64, error) {
	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0, err
	}
	// The comm field (field 2) can contain spaces and is wrapped in parens.
	// Everything after the last ')' is fields 3–N, space-separated.
	s := string(data)
	idx := strings.LastIndex(s, ")")
	if idx < 0 {
		return 0, fmt.Errorf("malformed /proc/self/stat")
	}
	fields := strings.Fields(s[idx+1:])
	// Field 22 (1-indexed from field 1) = index 19 after the closing paren.
	if len(fields) < 20 {
		return 0, fmt.Errorf("too few fields in /proc/self/stat")
	}
	return strconv.ParseUint(fields[19], 10, 64)
}
