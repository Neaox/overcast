//go:build windows

package mdns

// windows.go — Windows Publisher. Shells out to `dns-sd.exe -P`, which
// ships with Apple's "Bonjour Print Services for Windows" and is also
// bundled with iTunes / Xcode command-line tools. If it isn't on PATH the
// publisher returns ErrUnsupported; the host CLI is expected to tell the
// user how to install it.
//
// Command shape mirrors the macOS backend:
//
//	dns-sd.exe -P <name> _overcast._tcp local 80 <hostname> <ip>

import (
	"context"
	"os/exec"

	"go.uber.org/zap"
)

func newPublisher(log *zap.Logger) (Publisher, error) {
	if _, err := exec.LookPath("dns-sd.exe"); err != nil {
		return nil, ErrUnsupported
	}
	return newProcPublisher(log, windowsCmd), nil
}

func windowsCmd(ctx context.Context, r Record) *exec.Cmd {
	return exec.CommandContext(ctx, "dns-sd.exe",
		"-P", r.Hostname, "_overcast._tcp", "local", "80",
		r.Hostname, r.IP.String(),
	)
}
