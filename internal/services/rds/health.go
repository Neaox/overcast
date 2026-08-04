package rds

// health.go — bringing a DB instance up, and being honest when it does not
// come up.
//
// The instance lifecycle used to report success regardless of what happened to
// the container behind it. StartDBInstance logged a warning if the container
// would not start and returned 200; the health check dialled the engine thirty
// times and, having failed every one, transitioned the instance to "available"
// anyway "so the API is usable". An instance with no container at all
// therefore read as AVAILABLE, and the only way to discover otherwise was to
// try to connect to it.
//
// Three things replace that:
//
//   - A recorded container that Docker no longer has is rebuilt rather than
//     reported as started. Containers do go missing — a startup sweep, a
//     `docker prune`, a user tidying up by hand — and the instance record is
//     the thing that says the database should exist.
//   - The container's own state is consulted while waiting. A container that
//     has exited has already decided the outcome; waiting out the rest of the
//     budget only delays saying so, and its exit code is the most useful thing
//     anyone gets to see.
//   - Running out of budget fails the instance. AWS has a "failed" status
//     meaning "has failed and Amazon RDS can't recover it"; that is what this
//     is, and AWS never parks an instance in "starting" forever either.
//
// The reason text is kept on the record but deliberately not added to the
// DescribeDBInstances wire shape: the real DBInstance has no StatusReason
// field, and StatusInfos is documented as read-replica-only. AWS exposes a
// failure reason through RDS events (DescribeEvents), which is where this text
// is destined.

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/middleware"
)

const (
	// healthCheckInterval is how often Overcast dials the engine while waiting.
	healthCheckInterval = 2 * time.Second

	// healthCheckBudget is how long a database gets to start accepting
	// connections before the instance is called failed. A first boot
	// initialises the data directory — on a cold image that is minutes, not
	// seconds — so the budget is generous. It is a deadline rather than an
	// attempt count so that a slow or contended poll cannot silently shorten
	// it, which an attempt count did.
	healthCheckBudget = 5 * time.Minute

	// dialTimeout bounds a single connection attempt.
	dialTimeout = 2 * time.Second
)

// statusesAwaitingStartup are the statuses a health check is still deciding.
// Anything else means something took the instance somewhere on purpose —
// stopping, deleting, modifying — and the check must not write over it.
func awaitingStartup(status string) bool {
	return status == "creating" || status == "starting"
}

// failInstance moves an instance to AWS's "failed" status with the reason that
// put it there, unless something else has already moved it on. Re-reading
// rather than writing a caller's snapshot is what keeps a slow failure from
// resurrecting an instance that was stopped or deleted while it waited.
func (h *Handler) failInstance(ctx context.Context, instanceID, reason string) {
	got, aerr := h.store.getDBInstance(ctx, instanceID)
	if aerr != nil {
		return
	}
	if !awaitingStartup(got.DBInstanceStatus) {
		return
	}
	got.DBInstanceStatus = "failed"
	got.StatusReason = reason
	if aerr := h.store.putDBInstance(ctx, got); aerr != nil {
		h.log.Warn("RDS: persist failed instance",
			zap.String("instance", instanceID), zap.String("error", aerr.Message))
		return
	}
	h.log.Warn("RDS: instance failed to start",
		zap.String("instance", instanceID), zap.String("reason", reason))
}

// markAvailable moves an instance that has answered a connection to
// "available", clearing any earlier failure reason.
func (h *Handler) markAvailable(ctx context.Context, inst *DBInstance) {
	inst.DBInstanceStatus = "available"
	inst.StatusReason = ""
	if aerr := h.store.putDBInstance(ctx, inst); aerr != nil {
		h.log.Warn("RDS: persist available instance",
			zap.String("instance", inst.DBInstanceIdentifier), zap.String("error", aerr.Message))
	}
}

// containerFailureReason reports why the instance's container will not answer,
// or "" while it is still a candidate. A transient Docker error is not an
// answer — the caller keeps waiting — but an exited container is.
func (h *Handler) containerFailureReason(ctx context.Context, inst *DBInstance) string {
	if !h.dockerReady.Load() || inst.DockerContainerID == "" {
		return ""
	}
	info, err := h.docker.InspectContainer(ctx, inst.DockerContainerID)
	if err != nil {
		if docker.IsNotFound(err) {
			return "the database container no longer exists"
		}
		return ""
	}
	if info.State.Running {
		return ""
	}
	switch {
	case info.State.OOMKilled:
		return "the database container was killed by the kernel out-of-memory killer — give Docker more memory"
	case info.State.Error != "":
		return fmt.Sprintf("the database container could not run: %s", info.State.Error)
	case info.State.Status == "created":
		// Created but never started: the start call did not take effect.
		return ""
	default:
		return fmt.Sprintf("the database container exited with code %d — see the instance logs for the engine's own output",
			info.State.ExitCode)
	}
}

// scheduleHealthCheck polls the engine until it answers, its container dies, or
// the budget runs out, and moves the instance to available or failed
// accordingly. It never leaves an instance in creating/starting.
func (h *Handler) scheduleHealthCheck(region, instanceID, host string, port int) {
	if host == "" || port == 0 {
		// Nothing to dial. Without this the poll dials ":0" for the whole
		// budget and then reports a timeout, blaming the database for a
		// missing endpoint.
		h.scheduler.AfterScoped(region, instanceID, "health", 0, func(ctx context.Context) {
			h.failInstance(ctx, instanceID, "the instance has no endpoint to connect to")
		})
		return
	}

	target := net.JoinHostPort(host, strconv.Itoa(port))
	deadline := h.clk.Now().Add(healthCheckBudget)

	var check func(ctx context.Context)
	check = func(ctx context.Context) {
		got, aerr := h.store.getDBInstance(ctx, instanceID)
		if aerr != nil {
			return
		}
		if !awaitingStartup(got.DBInstanceStatus) {
			return // Stopped, deleted or modified while starting — not ours.
		}

		if reason := h.containerFailureReason(ctx, got); reason != "" {
			h.failInstance(ctx, instanceID, reason)
			return
		}

		conn, err := net.DialTimeout("tcp", target, dialTimeout)
		if err == nil {
			conn.Close()
			h.markAvailable(ctx, got)
			return
		}

		if h.clk.Now().Before(deadline) {
			h.scheduler.AfterScoped(region, instanceID, "health", healthCheckInterval, check)
			return
		}
		h.failInstance(ctx, instanceID, fmt.Sprintf(
			"the database did not accept connections on %s within %s", target, healthCheckBudget))
	}
	h.scheduler.AfterScoped(region, instanceID, "health", 1*time.Second, check)
}

// startInstanceContainer brings an instance's container back up, rebuilding it
// when the recorded one is gone. It updates the container-owned fields on inst
// in place so the caller can persist them.
func (h *Handler) startInstanceContainer(ctx context.Context, inst *DBInstance) error {
	err := h.docker.StartContainer(ctx, inst.DockerContainerID)
	if err == nil {
		return nil
	}
	if !docker.IsNotFound(err) {
		return err
	}
	// The container Docker was asked about is gone. Reporting a successful
	// start here is what left instances claiming to be available with nothing
	// behind them; startDBContainer reuses a container of the right name if one
	// survived and otherwise builds a fresh one.
	h.log.Info("RDS: recorded container is gone — rebuilding it",
		zap.String("instance", inst.DBInstanceIdentifier),
		zap.String("container", inst.DockerContainerID))
	return h.startDBContainer(ctx, inst)
}

// startInstanceContainerAsync runs the container start off the request path —
// a rebuild may have to pull an image — and settles the instance's status
// itself: available once the engine answers, failed if it cannot be started at
// all. Mirrors launchDBContainerAsync's merge discipline: the instance may have
// been stopped or deleted while the start was in flight, so only the
// container-owned fields are written back onto a fresh read.
func (h *Handler) startInstanceContainerAsync(ctx context.Context, instanceID string) {
	region := h.store.region(ctx)
	h.dockerWg.Add(1)
	go func() {
		defer h.dockerWg.Done()
		bgCtx := middleware.ContextWithRegion(context.Background(), region)

		got, aerr := h.store.getDBInstance(bgCtx, instanceID)
		if aerr != nil || got == nil {
			return
		}
		if err := h.startInstanceContainer(bgCtx, got); err != nil {
			h.failInstance(bgCtx, instanceID, fmt.Sprintf("the database container could not be started: %v", err))
			return
		}

		fresh, aerr := h.store.getDBInstance(bgCtx, instanceID)
		if aerr != nil || fresh == nil {
			return
		}
		if !awaitingStartup(fresh.DBInstanceStatus) {
			// Stopped or deleted mid-start; leave the transition alone. The
			// container is brought back down by whoever made that transition.
			return
		}
		fresh.DockerContainerID = got.DockerContainerID
		fresh.HostPort = got.HostPort
		fresh.Endpoint = got.Endpoint
		fresh.DialAddress = got.DialAddress
		fresh.DialPort = got.DialPort
		if aerr := h.store.putDBInstance(bgCtx, fresh); aerr != nil {
			h.log.Warn("RDS: persist started instance",
				zap.String("instance", instanceID), zap.String("error", aerr.Message))
			return
		}

		host, port := dialTarget(fresh)
		h.scheduleHealthCheck(region, instanceID, host, port)
	}()
}
