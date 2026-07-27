package containerendpoint

// published.go derives the port a *host* caller reaches Overcast on.
//
// This is the mirror image of resolve.go. That file answers "what address do
// sibling containers dial to reach Overcast"; this one answers "what port does
// a browser on the host dial", which the web UI needs so the SPA it serves can
// be told where the API actually is.
//
// A container cannot see its own port mapping from the inside. Started with
// `-p 4580:4566`, the process listens on 4566 and nothing in its environment
// records that host callers arrive on 4580. Handing the SPA the listen port
// therefore points the browser at a port that is usually closed, which is why
// the web UI only worked when the published ports happened to match the
// internal ones. Asking Docker for our own container's bindings is the only
// way to recover the mapping.

import (
	"context"
	"os"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/docker"
)

// publishedPortTimeout bounds the single Docker inspect this makes. It runs on
// the startup path, so it must fail fast when the socket is absent rather than
// hold the server back.
const publishedPortTimeout = 3 * time.Second

// inspectClient is the slice of *docker.Client that PublishedPort needs.
type inspectClient interface {
	InspectContainer(ctx context.Context, id string) (*docker.ContainerInspect, error)
}

// PublishedPort returns the host port that internalPort is published on when
// Overcast itself runs in a container, or 0 when the mapping cannot be
// determined: the process is not containerised, the Docker socket is not
// mounted, or the port is not published at all.
//
// Returning 0 means "no better answer than the one you already have" — callers
// keep their configured port. A native binary is never containerised, so it
// never reaches the Docker call and its behaviour is unchanged.
func PublishedPort(ctx context.Context, dc inspectClient, internalPort int, logger *zap.Logger) int {
	return publishedPort(ctx, dc, internalPort, logger, runningInContainer)
}

// publishedPort is PublishedPort with its environment probe injected, so the
// containerised branch is testable without a Docker daemon.
func publishedPort(ctx context.Context, dc inspectClient, internalPort int, logger *zap.Logger,
	inContainer func() bool) int {
	if dc == nil || internalPort <= 0 || !inContainer() {
		return 0
	}

	// Docker defaults a container's hostname to its own short ID, which the
	// inspect endpoint accepts. resolve.go identifies us the same way.
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		return 0
	}

	ctx, cancel := context.WithTimeout(ctx, publishedPortTimeout)
	defer cancel()

	info, err := dc.InspectContainer(ctx, hostname)
	if err != nil || info == nil {
		logDebug(logger, "published port: inspect failed; keeping the configured port",
			zap.String("container", hostname), zap.Error(err))
		return 0
	}

	key := strconv.Itoa(internalPort) + "/tcp"
	for _, b := range info.NetworkSettings.Ports[key] {
		// Docker commonly lists an IPv4 and an IPv6 binding for one -p flag,
		// both carrying the same host port; either answers the question.
		p, err := strconv.Atoi(b.HostPort)
		if err != nil || p <= 0 {
			continue
		}
		if p != internalPort {
			logDebug(logger, "published port resolved",
				zap.Int("internal_port", internalPort), zap.Int("published_port", p))
		}
		return p
	}

	// Port not published — e.g. `--network host`, or the UI reached over a
	// container network rather than a host mapping.
	return 0
}

func logDebug(logger *zap.Logger, msg string, fields ...zap.Field) {
	if logger == nil {
		return
	}
	logger.Debug(msg, fields...)
}
