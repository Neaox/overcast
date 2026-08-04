//go:build !unix

package rds

// held_port_other_test.go — the platforms where a port cannot be held.
//
// Holding a port means keeping a socket bound to it without listening and
// promoting that same socket later (see held_port_unix_test.go). net.FileListener
// does not take a socket handle on Windows, so the promotion is not available
// and the tests fall back to probing a free port and binding it when they need
// it — what every platform did before. That fallback has a window in which
// something else can take the port; CI runs on Linux, where the hold is real.

import "errors"

const heldPortsSupported = false

// holdPort is unreachable: every caller checks heldPortsSupported first. It
// exists so the shared reservation code compiles here.
func holdPort(int) (heldPort, error) {
	return nil, errors.New("a bound socket cannot be promoted to a listener on this platform")
}
