//go:build darwin

package router

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// processStartTime returns the wall-clock time the kernel launched this
// process via sysctl kern.proc.pid, which is available on Darwin/macOS.
// Falls back to time.Now() on any error.
func processStartTime() time.Time {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", os.Getpid())
	if err != nil {
		return time.Now()
	}
	sec := int64(kp.Proc.P_starttime.Sec)
	usec := int64(kp.Proc.P_starttime.Usec)
	return time.Unix(sec, usec*1000)
}
