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
//     Failures are retried with backoff until the container is gone — see
//     removeContainer.
//
// DrainAndSweep is called at shutdown: it drains the remove queue and then
// removes this instance's managed containers (Docker-level sweep), catching
// any orphans.
//
// Every sweep here is scoped to the instance that created the containers — see
// InstanceDomainFunc and LabelInstance. Container IDs handed to StopNow and
// ScheduleRemove are not, and need not be: those come from this instance's own
// records, so they are already known to be ours.
//
// Zero value is invalid — use NewGC.
type GC struct {
	client    *Client
	logger    *zap.Logger
	keeps     bool // KeepContainers: skip removes (debugging only)
	domain    InstanceDomainFunc
	removeQ   chan string
	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup

	beforeRemove func(containerID string)
}

// InstanceDomainFunc resolves the identity of the Overcast instance whose
// containers this GC may sweep — the value its service stamps into
// LabelInstance at creation. It is a function rather than a string because
// resolving it reads the state store, which can fail transiently; a GC given a
// string would cache that failure for the life of the process. Implementations
// are expected to memoize success and retry failure
// (serviceutil.InstanceDomain.Resolve does).
//
// Returning "" means ownership cannot be established, and every sweep then
// removes nothing.
type InstanceDomainFunc func(context.Context) string

// NewGC creates a GC tied to a Docker client.
// keepContainers=true means containers are never removed — stop only
// (useful for debugging / post-mortem inspection).
//
// domain scopes the sweeps to one instance's own containers. A nil domain
// disables sweeping entirely rather than restoring the old sweep-everything
// behaviour: the failure mode of sweeping too little is disk held until
// someone prunes, and of sweeping too much is another Overcast's running
// database destroyed.
func NewGC(client *Client, logger *zap.Logger, keepContainers bool, domain InstanceDomainFunc) *GC {
	return &GC{
		client:  client,
		logger:  logger,
		keeps:   keepContainers,
		domain:  domain,
		removeQ: make(chan string, 256),
		done:    make(chan struct{}),
	}
}

// sweepDomain resolves the identity this GC's sweeps are confined to, or ""
// when it cannot be established.
func (g *GC) sweepDomain(ctx context.Context) string {
	if g.domain == nil {
		return ""
	}
	return g.domain(ctx)
}

// ownedByThisInstance reports whether a sweep may remove c.
//
// Absence of the label is not permission. A container without one was created
// by an Overcast that predates the label, or by something else entirely;
// either way its owner cannot be established, and an unremovable orphan is a
// far cheaper mistake than deleting a resource another instance is serving.
// The same goes for domain == "": a store that will not answer is not evidence
// that anything on the daemon is litter.
func ownedByThisInstance(c *ContainerSummary, domain string) bool {
	return domain != "" && c.Instance() == domain
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

// SetBeforeRemove registers a hook run once against each container this GC is
// about to remove, while the container is still there to be read.
//
// It exists because a container's removal destroys the only copy of things
// worth keeping — its final output, above all — and every caller that schedules
// one is a caller that could forget to take that copy first. Owning the
// ordering here rather than at each call site is what makes it hold for callers
// not yet written: ScheduleRemove is non-blocking by design, so a caller has no
// point in its own control flow at which the removal has provably not happened
// yet.
//
// Called on the remove loop's goroutine, after the container has been stopped
// and before the first removal attempt — see removeContainer for why the stop
// comes first. Retries do not run it again: it has already had its chance
// against a container that was certainly present, and the failure being retried
// is the removal, not the hook.
//
// Best-effort, and expected to be: it must not panic, and anything it cannot
// do it must swallow, because a container whose hook fails still has to be
// removed. Set before the remove loop starts; not safe to change afterwards.
func (g *GC) SetBeforeRemove(fn func(containerID string)) {
	g.beforeRemove = fn
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
//
// A registered before-remove hook runs first, and the container is stopped
// before it. The stop is not this loop's job — every caller that schedules a
// removal has already asked for one — but it is the only way the hook can be
// given what it is for. StopNow and ScheduleRemove are both non-blocking and
// independent, so this loop routinely reaches a container whose stop is still
// in flight, and a hook run there reads a container mid-shutdown: for the log
// capture that is ECS's hook, that is the difference between keeping why a task
// died and keeping the lines before it started dying. StopContainer on a
// container that has already stopped is a no-op the daemon answers 304 to, so
// the cost is one round trip on the common path, and only for a GC that has a
// hook to order against.
func (g *GC) removeContainer(containerID string) {
	const baseDelay = 1 * time.Second
	const maxDelay = 60 * time.Second

	if g.beforeRemove != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := g.client.StopContainer(ctx, containerID, 10); err != nil {
			g.logger.Debug("gc: stop container before remove hook",
				zap.String("container", containerID), zap.Error(err))
		}
		cancel()
		g.beforeRemove(containerID)
	}

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

// DrainAndSweep shuts down the GC and removes this instance's managed
// containers for the given service. service="" matches all services. Blocks
// until complete or ctx expires during the drain phase.
//
// Call from each service's Stop() method — this is the safety net that catches
// any container whose store record was already deleted but whose Docker
// container was never cleaned up.
//
// The sweep stops and force-removes without consulting container state,
// because at shutdown a running container of ours is exactly what needs
// stopping. That is only defensible once "of ours" is enforced: unscoped, this
// was the most destructive path in the package, since one instance's ordinary
// shutdown tore down another instance's live RDS databases, ECS tasks, Lambda
// runtimes and MSK brokers on the same daemon.
//
// Scoping costs this sweep nothing even under a memory backend, which is what
// separates it from the startup sweeps. Every container it has any business
// removing was created by this process, and so carries the identity this
// process is resolving now — freshly minted or not.
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
	domain := g.sweepDomain(sweepCtx)
	for _, c := range containers {
		id := c.ID
		if !ownedByThisInstance(&c, domain) {
			g.logger.Debug("gc: shutdown sweep leaving container owned elsewhere",
				zap.String("container", id), zap.String("service", c.Service()),
				zap.String("owner", c.Instance()))
			continue
		}
		g.logger.Debug("gc: sweep removing container",
			zap.String("container", id), zap.String("service", c.Service()))
		_ = g.client.StopContainer(sweepCtx, id, 5)
		_ = g.client.RemoveContainerForce(id)
	}
}

// Sweep removes this instance's non-running managed containers for the given
// service without closing the GC. Call at startup to clean up containers this
// instance orphaned in a prior run. service="" matches all services.
// Non-blocking — runs in a goroutine.
func (g *GC) Sweep(service string) {
	g.SweepExcept(service, nil)
}

// SweepExcept is Sweep with a veto. keep is asked about each candidate's
// resource ID and reports whether something live still owns it; those are left
// alone. Pass nil to sweep every non-running container of this instance's own,
// which is Sweep.
//
// A stopped container is not automatically litter. Compute that is recreated on
// demand — an ECS task, a Lambda runtime — has no attachment to the container
// it last ran in, so state alone is a fair test there. A database does: a
// stopped RDS DB instance is a resource the user still owns and expects to
// start again, and its container has to outlive an Overcast restart. Sweeping
// it left the instance record pointing at an ID Docker no longer had, after
// which every start failed, every log fetch 404'd, and the instance still
// claimed to be available.
//
// The veto answers "does anything still own this?" from the sweeping
// instance's own records, which is why it cannot stand alone. Two Overcasts
// share a daemon but not a store, so the second one's veto says "no owner"
// about every container the first one is using. For RDS that was data loss and
// not merely a stranded record: an RDS container carries no volume or bind
// mount, so the database lives in the container's writable layer and is
// destroyed with it. The instance-identity check runs first and is what makes
// the veto safe.
//
// Under a memory backend this sweep removes nothing, because the identity is
// minted fresh each start and so nothing predating the process is in scope.
// That is a deliberate trade and a smaller one than it looks: DrainAndSweep
// already cleans up on an orderly shutdown, so what leaks is the containers of
// a run that crashed. A durable backend still recognises and sweeps them.
// Deleting a container whose owner cannot be established is not a choice worth
// the disk it reclaims; `docker rm $(docker ps -aq --filter
// label=overcast.managed=true)` reclaims it on request.
//
// KeepContainers disables the sweep outright. A container held back for
// post-mortem inspection is usually inspected after a restart, which is exactly
// when this runs.
func (g *GC) SweepExcept(service string, keep func(resourceID string) bool) {
	if g.keeps {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		containers, err := g.client.ListContainers(ctx, service)
		if err != nil {
			g.logger.Debug("gc: startup sweep list failed",
				zap.String("service", service), zap.Error(err))
			return
		}
		domain := g.sweepDomain(ctx)
		removed := 0
		for _, c := range containers {
			id := c.ID
			if c.State == "running" {
				continue // Don't touch running containers from the current session.
			}
			// Before the veto, because the veto reads this instance's records
			// and those say nothing about another instance's containers.
			if !ownedByThisInstance(&c, domain) {
				g.logger.Debug("gc: startup sweep leaving container owned elsewhere",
					zap.String("container", id), zap.String("service", c.Service()),
					zap.String("owner", c.Instance()), zap.String("state", c.State))
				continue
			}
			if keep != nil && keep(c.ResourceID()) {
				g.logger.Debug("gc: startup sweep keeping container owned by a live resource",
					zap.String("container", id), zap.String("service", c.Service()),
					zap.String("resource", c.ResourceID()), zap.String("state", c.State))
				continue
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
