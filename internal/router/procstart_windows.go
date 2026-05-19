//go:build windows

package router

import (
	"syscall"
	"time"
)

// processStartTime returns the wall-clock time the kernel launched this
// process via GetProcessTimes, which is available on Windows.
// Falls back to time.Now() on any error.
func processStartTime() time.Time {
	h, err := syscall.GetCurrentProcess()
	if err != nil {
		return time.Now()
	}
	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return time.Now()
	}
	// Filetime.Nanoseconds() converts from 100-ns intervals since 1601 to Unix nanoseconds.
	return time.Unix(0, creation.Nanoseconds())
}
