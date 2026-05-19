//go:build darwin

package mdns

// darwin.go — macOS Publisher. Shells out to `dns-sd -P`, the standard
// Bonjour proxy-registration command shipped with every Mac. No CGo, no
// extra dependencies, no entitlements required: anything the user can do
// from Terminal, `overcast dev` can do too.
//
// Command shape:
//
//	dns-sd -P <name> _overcast._tcp local 80 <hostname> <ip>
//
// -P registers a proxy service and, importantly, also publishes an A record
// for <hostname> pointing at <ip>. The service type is arbitrary — we use
// _overcast._tcp so `dns-sd -B _overcast._tcp` from another terminal shows
// overcast's registrations distinct from everything else on the network.

import (
	"context"
	"os/exec"

	"go.uber.org/zap"
)

func newPublisher(log *zap.Logger) (Publisher, error) {
	if _, err := exec.LookPath("dns-sd"); err != nil {
		return nil, ErrUnsupported
	}
	return newProcPublisher(log, darwinCmd), nil
}

func darwinCmd(ctx context.Context, r Record) *exec.Cmd {
	return exec.CommandContext(ctx, "dns-sd",
		"-P", r.Hostname, "_overcast._tcp", "local", "80",
		r.Hostname, r.IP.String(),
	)
}
