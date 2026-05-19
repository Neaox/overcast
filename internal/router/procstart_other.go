//go:build !linux && !darwin && !windows

package router

import "time"

// processStartTime falls back to time.Now() on non-Linux platforms where
// /proc/self/stat is unavailable. The measurement will reflect Go package
// init time rather than OS exec time, which is still a reasonable proxy
// for application startup on platforms without /proc.
func processStartTime() time.Time {
	return time.Now()
}
