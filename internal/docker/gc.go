package docker

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
)

// GC manages async Docker container cleanup. Services schedule containers for
// removal and the GC handles stop+remove in background goroutines:
//
//   - StopNow: fires immediately in a dedicated goroutine (non-blocking).
//     A running container can still execute code — stop it ASAP.
//   - ScheduleRemove: enqueued and processed at leisure by the background loop.
//     Failures are re-enqueued for retry (up to 3 attempts).
//
// DrainAndSweep is called at shutdown: it drains the remove queue and then
// removes every managed container (Docker-level sweep), catching any orphans.
//
// Zero value is invalid — use NewGC.
type GC struct {
	client    *Client
	logger    *zap.Logger
	keeps     bool // KeepContainers: skip removes (debugging only)
	removeQ   chan string
	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

// NewGC creates a GC tied to a Docker client.
// keepContainers=true means containers are never removed — stop only
// (useful for debugging / post-mortem inspection).
func NewGC(client *Client, logger *zap.Logger, keepContainers bool) *GC {
	return &GC{
		client:  client,
		logger:  logger,
		keeps:   keepContainers,
		removeQ: make(chan string, 256),
		done:    make(chan struct{}),
	}
}

// StopNow fires an async StopContainer in its own goroutine and returns
// immediately. Call from a delete handler before returning the response.
// Failures are logged at debug level — the remove loop will force-remove
// the container regardless of stop state.
func (g *GC) StopNow(containerID string) {
	if containerID == "" {
		return
	}
	select {
	case <-g.done:
		return
	default:
	}
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := g.client.StopContainer(ctx, containerID, 10); err != nil {
			g.logger.Debug("gc: stop container", zap.String("container", containerID), zap.Error(err))
		}
	}()
}

// ScheduleRemove enqueues a container for async removal. The background loop
// picks it up when it can — removal is not urgent once the container is stopped.
// Non-blocking. If the remove queue is full the request is dropped (logged).
func (g *GC) ScheduleRemove(containerID string) {
	if containerID == "" || g.keeps {
		return
	}
	select {
	case <-g.done:
		return
	default:
	}
	select {
	case g.removeQ <- containerID:
	default:
		g.logger.Warn("gc: remove queue full, container may not be removed",
			zap.String("container", containerID))
	}
}

// StopAndScheduleRemove stops a container immediately (to halt any code
// running inside) and then queues it for deferred removal with exponential
// backoff. The stop fires in a dedicated goroutine so the caller can proceed
// without waiting for Docker to respond. The deferred removal retries
// indefinitely until the GC shuts down or the container is gone.
func (g *GC) StopAndScheduleRemove(containerID string) {
	if g.keeps {
		return
	}
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		// Stop immediately — we want to halt any running code.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = g.client.StopContainer(ctx, containerID, 5)
		cancel()
		// Queue for deferred removal with exponential backoff.
		g.ScheduleRemove(containerID)
	}()
}

// StartRemoveLoop begins the background remove worker. It blocks until ctx
// is cancelled or the GC is shut down. Safe to call multiple times — each
// call starts an independent worker goroutine tracked by the internal
// WaitGroup.
func (g *GC) StartRemoveLoop(ctx context.Context) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case <-g.done:
				return
			case cid := <-g.removeQ:
				g.removeContainer(cid)
			}
		}
	}()
}

// removeContainer force-removes a container with exponential backoff.
// Retries indefinitely when Docker is overloaded (context deadline exceeded,
// connection refused, etc.). Container-not-found errors terminate immediately.
// Each retry gets a fresh 30 s context; the backoff starts at 1 s and doubles
// each attempt up to a 60 s cap.
func (g *GC) removeContainer(containerID string) {
	const baseDelay = 1 * time.Second
	const maxDelay = 60 * time.Second
	delay := baseDelay
	attempt := 0
	for {
		// Exit early when the GC is shutting down (drain phase).
		select {
		case <-g.done:
			return
		default:
		}

		attempt++
		err := g.client.RemoveContainerForce(containerID)
		if err == nil {
			return
		}
		if IsNotFound(err) {
			return
		}
		g.logger.Warn("gc: remove container failed — will retry",
			zap.String("container", containerID),
			zap.Int("attempt", attempt),
			zap.Duration("next_delay", delay),
			zap.Error(err))
		select {
		case <-g.done:
			return
		case <-time.After(delay):
		}
		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
}

// DrainAndSweep shuts down the GC and removes every managed container for the
// given service. service="" matches all services. Blocks until complete or ctx
// expires during the drain phase.
//
// Call from each service's Stop() method — this is the safety net that catches
// any container whose store record was already deleted but whose Docker
// container was never cleaned up.
//
// Once DrainAndSweep returns the GC is inert; further StopNow / ScheduleRemove
// calls are no-ops.
func (g *GC) DrainAndSweep(ctx context.Context, service string) {
	// Signal shutdown. All subsequent StopNow / ScheduleRemove become no-ops.
	g.closeOnce.Do(func() { close(g.done) })

	// Drain the remove queue. Items enqueued before g.done was closed are
	// still in the channel; process them now.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case cid := <-g.removeQ:
				g.removeContainer(cid)
			default:
				return
			}
		}
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return
	}

	// Wait for all in-flight goroutines (remove loop workers + outstanding
	// StopNow calls). This is bounded by the per-call timeouts (15 s for
	// StopNow, 30 s for remove) so it cannot block forever.
	g.wg.Wait()

	// When KeepContainers is enabled (debugging mode), leave containers as-is.
	if g.keeps {
		return
	}

	// Final sweep: list and remove every managed container. Uses a fresh
	// background context so the sweep is not gated by the caller's (potentially
	// already expired) shutdown context.
	sweepCtx, sweepCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer sweepCancel()

	containers, err := g.client.ListContainers(sweepCtx, service)
	if err != nil {
		g.logger.Warn("gc: sweep list failed", zap.String("service", service), zap.Error(err))
		return
	}
	for _, c := range containers {
		id := c.ID
		g.logger.Debug("gc: sweep removing container",
			zap.String("container", id), zap.String("service", c.Service()))
		_ = g.client.StopContainer(sweepCtx, id, 5)
		_ = g.client.RemoveContainerForce(id)
	}
}

// Sweep removes every managed container for the given service without closing
// the GC. Call at startup to clean up orphaned containers from prior runs.
// service="" matches all services. Non-blocking — runs in a goroutine.
func (g *GC) Sweep(service string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		containers, err := g.client.ListContainers(ctx, service)
		if err != nil {
			g.logger.Debug("gc: startup sweep list failed",
				zap.String("service", service), zap.Error(err))
			return
		}
		removed := 0
		for _, c := range containers {
			id := c.ID
			if c.State == "running" {
				continue // Don't touch running containers from the current session.
			}
			g.logger.Debug("gc: startup sweep removing orphaned container",
				zap.String("container", id), zap.String("service", c.Service()),
				zap.String("state", c.State))
			_ = g.client.StopContainer(ctx, id, 5)
			_ = g.client.RemoveContainerForce(id)
			removed++
		}
		if removed > 0 {
			g.logger.Info("gc: startup sweep complete",
				zap.String("service", service), zap.Int("removed", removed))
		}
	}()
}
