//go:build linux

package mdns

// linux.go — Linux Publisher. Shells out to `avahi-publish -a -R`, which is
// part of the avahi-utils package and ships on most desktop distros. We
// deliberately avoid a direct D-Bus dependency (godbus) — the extra binary
// size and the risk of protocol drift aren't worth it for a dev tool.
//
// Command shape:
//
//	avahi-publish -a -R <hostname> <ip>
//
// -a publishes an A record and -R makes the command block until killed,
// matching the procPublisher lifecycle contract.

import (
	"context"
	"os/exec"

	"go.uber.org/zap"
)

func newPublisher(log *zap.Logger) (Publisher, error) {
	if _, err := exec.LookPath("avahi-publish"); err != nil {
		return nil, ErrUnsupported
	}
	return newProcPublisher(log, linuxCmd), nil
}

func linuxCmd(ctx context.Context, r Record) *exec.Cmd {
	return exec.CommandContext(ctx, "avahi-publish",
		"-a", "-R", r.Hostname, r.IP.String(),
	)
}
