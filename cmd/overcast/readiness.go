package main

import (
	"fmt"
	"io"
	"net"

	"go.uber.org/zap"
)

// readyMarker is the readiness line Overcast prints once every listener is
// bound and the router is serving on all of them (#1546).
//
// It is LocalStack's line, byte for byte, and that is the whole point: three
// of the five official LocalStack Testcontainers modules block on it rather
// than on an HTTP probe, so a suite that swaps the image and changes nothing
// else hangs until its startup timeout without it.
//
//	Java    Wait.forLogMessage(".*Ready\\.\n", 1)   -> Pattern.matches("(?s).*Ready\\.\n") per log frame
//	Python  wait_for_logs(container, r"Ready\.\n")  -> re.compile(..., re.MULTILINE).search over stdout, then stderr
//	Node    Wait.forLogMessage("Ready", 1)          -> substring match per log line
//
// Java's is the strict one: it applies Pattern.matches to a whole frame, so
// the marker has to be its own line ending in "Ready.\n". A structured line
// carrying the same text ({"msg":"Ready."}) ends in "}" and matches nothing,
// which is why this is a plain write rather than a zap field.
//
// The honesty cost -- an emulator that says another emulator's word in its own
// logs -- is paid by the structured line emitLine writes immediately before
// it, which names the marker and says what it is for. Nothing is gated on
// configuration: one extra line is cheaper than a flag nobody discovers.
const readyMarker = "Ready."

// emitReady announces readiness on out: first a structured line naming the
// bound addresses and the marker about to follow, then the marker itself.
//
// Both go to the same stream (the daemon's stderr, which is also zap's sink),
// so the two lines stay adjacent and in order. Call it once, after every
// listener in lns is bound and serving -- lns is read for its addresses,
// which is also what makes "bound" observable to a test.
func emitReady(logger *zap.Logger, out io.Writer, lns []net.Listener) {
	addrs := make([]string, 0, len(lns))
	for _, ln := range lns {
		addrs = append(addrs, ln.Addr().String())
	}
	logger.Info("overcast ready",
		zap.Strings("addrs", addrs),
		zap.String("marker", readyMarker),
		zap.String("marker_reason", "LocalStack's readiness line, emitted verbatim so LocalStack Testcontainers modules that wait for it can start this image -- see docs/testcontainers.md"),
	)
	fmt.Fprintln(out, readyMarker)
}
