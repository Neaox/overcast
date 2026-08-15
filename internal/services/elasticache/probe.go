package elasticache

// probe.go — proving a cache engine is serving, not merely that a port accepts.
//
// The three health checks here used to be a bare `net.DialTimeout`. That is not
// a readiness signal, for the reason efs/live_nfs.go had already written down:
// Docker's published port accepts connections as soon as the port proxy is up,
// which is before the engine process inside the container has bound anything.
// A cache that reported "available" off a successful dial was reporting the
// proxy, and the first command a client sent went to a socket the engine was
// not on yet.
//
// What replaces it is the smallest command each engine answers — the same bytes
// `redis-cli PING` and `echo version | nc` put on the wire. Speaking the
// protocol directly rather than `docker exec`-ing the engine's CLI (the way
// rds/health.go runs pg_isready) is deliberate here: these health checks also
// run with Docker unavailable or not yet wired, the images Overcast pulls are
// not guaranteed to ship a client binary, and one PING is the whole of what the
// CLI would have done anyway. RDS needs exec because it is proving something a
// connection cannot show — that the init scripts finished — and that is a
// different question from "is the server up".

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// engineProbeTimeout bounds one probe: connect, write the command, read the
// reply. Calibrated against a cache engine on loopback, where every one of
// those is sub-millisecond when the engine is up — the timeout is only ever
// spent on a container that accepted the connection and then said nothing, and
// it is deliberately shorter than the retry interval so a silent engine cannot
// stretch the budget past its wall-clock deadline.
const engineProbeTimeout = 2 * time.Second

// probeEngine reports whether the cache engine at addr is serving. A nil error
// means it answered its own protocol; any error is the evidence for why the
// attempt did not count.
func probeEngine(ctx context.Context, engine, addr string) error {
	if engine == "memcached" {
		return probeMemcached(ctx, addr)
	}
	// redis, valkey, and anything else Overcast maps onto a Redis image: all
	// speak RESP, and valkey answers PING as Redis does.
	return probeRedis(ctx, addr)
}

// probeRedis sends RESP `PING` and requires an answer that only a running
// server produces.
//
// `+PONG` is the ready answer. An error reply is not automatically a failure:
// a server that rejects a command is a server that is up, and `-NOAUTH` is what
// a password-protected engine answers an unauthenticated PING with. The
// exceptions are the errors that mean "up but not yet serving" — `-LOADING`
// while the dataset is read back off disk, `-BUSY` while a script blocks the
// server, `-MASTERDOWN` on a replica with no primary. Those are the whole
// reason a bare dial was wrong, so they are reported as not-ready rather than
// smuggled through as an answer.
func probeRedis(ctx context.Context, addr string) error {
	line, err := engineExchange(ctx, addr, "*1\r\n$4\r\nPING\r\n")
	if err != nil {
		return err
	}
	switch {
	case strings.HasPrefix(line, "+PONG"):
		return nil
	case strings.HasPrefix(line, "-"):
		for _, notReady := range []string{"-LOADING", "-BUSY", "-MASTERDOWN"} {
			if strings.HasPrefix(line, notReady) {
				return fmt.Errorf("the engine answered PING with %q — it is up but not serving yet", line)
			}
		}
		// Any other error reply still took a command and produced a reply, so
		// the server is serving; it simply did not like this one.
		return nil
	default:
		return fmt.Errorf("PING was answered with %q, which is not a Redis reply", line)
	}
}

// probeMemcached sends `version`, which memcached answers with `VERSION x.y.z`.
// Memcached has no PING; `version` is its cheapest command and the one its own
// health checks use.
func probeMemcached(ctx context.Context, addr string) error {
	line, err := engineExchange(ctx, addr, "version\r\n")
	if err != nil {
		return err
	}
	if !strings.HasPrefix(line, "VERSION") {
		return fmt.Errorf("`version` was answered with %q, which is not a memcached reply", line)
	}
	return nil
}

// engineExchange dials addr, writes cmd and returns the first line of the
// reply. Every step is bounded by engineProbeTimeout, including the read: a
// container that accepts and then never speaks is the case this whole file
// exists to catch, and it must not be allowed to block a scheduler callback.
func engineExchange(ctx context.Context, addr, cmd string) (string, error) {
	dialer := net.Dialer{Timeout: engineProbeTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return "", fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close() //nolint:errcheck // read-only probe connection

	// time.Now rather than the injected clock, as efs/live_nfs.go's NFS probe
	// also does: a socket deadline is enforced by the kernel against the wall
	// clock and a mock cannot move it. Nothing about the retry policy is timed
	// here — that all lives on the scheduler's clock, in readiness.Watch.
	if err := conn.SetDeadline(time.Now().Add(engineProbeTimeout)); err != nil {
		return "", fmt.Errorf("set deadline: %w", err)
	}
	if _, err := conn.Write([]byte(cmd)); err != nil {
		return "", fmt.Errorf("write to %s: %w", addr, err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		if errors.Is(err, net.ErrClosed) || line == "" {
			return "", fmt.Errorf("%s accepted the connection but never answered: %w", addr, err)
		}
		return "", fmt.Errorf("read from %s: %w", addr, err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}
