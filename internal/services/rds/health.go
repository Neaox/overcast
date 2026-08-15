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
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil/readiness"
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
	// Take the engine's own output before anything removes the container. It
	// is the only place the actual cause is written down — "exited with code
	// 1" says nothing, and the line above it usually says everything.
	//
	// Reading it is a Docker call, so it happens outside the record's lock, on
	// a snapshot; only the two fields it fills in travel into the write below.
	snapshot, aerr := h.store.getDBInstance(ctx, instanceID)
	if aerr != nil {
		return
	}
	if !awaitingStartup(snapshot.DBInstanceStatus) {
		return
	}
	h.captureContainerLogs(ctx, snapshot)

	priorStatus := ""
	if _, aerr := h.mutateInstance(ctx, instanceID, func(inst *DBInstance) *protocol.AWSError {
		if !awaitingStartup(inst.DBInstanceStatus) {
			return errInstanceMovedOn
		}
		priorStatus = inst.DBInstanceStatus
		inst.DBInstanceStatus = "failed"
		inst.StatusReason = reason
		inst.LastLogs = snapshot.LastLogs
		inst.LastLogsAt = snapshot.LastLogsAt
		return nil
	}); aerr != nil {
		if aerr != errInstanceMovedOn {
			h.log.Warn("RDS: persist failed instance",
				zap.String("instance", instanceID), zap.String("error", aerr.Message))
		}
		return
	}
	h.recordInstanceFailureEvent(ctx, instanceID, priorStatus, reason)
	h.log.Warn("RDS: instance failed to start",
		zap.String("instance", instanceID), zap.String("reason", reason))
}

// markAvailable moves an instance that has answered a connection to
// "available", clearing any earlier failure reason.
//
// inst is the snapshot the caller was holding while it dialled, and is used for
// nothing but the identifier: the record is re-read inside the lock and only
// the status is written. Persisting the snapshot instead is what silently
// reverted every edit an API call made while the dial was in flight — a
// ModifyDBInstance's new instance class, most visibly. Same discipline as
// failInstance, for the same reason.
func (h *Handler) markAvailable(ctx context.Context, inst *DBInstance) {
	instanceID := inst.DBInstanceIdentifier
	fresh, aerr := h.mutateInstance(ctx, instanceID, func(got *DBInstance) *protocol.AWSError {
		if !awaitingStartup(got.DBInstanceStatus) {
			return errInstanceMovedOn
		}
		got.DBInstanceStatus = "available"
		got.StatusReason = ""
		if got.DockerRecoveryAttempts > 0 {
			got.DockerRecoveryAvailableAt = h.clk.Now()
		}
		return nil
	})
	if aerr != nil && aerr != errInstanceMovedOn {
		h.log.Warn("RDS: persist available instance",
			zap.String("instance", instanceID), zap.String("error", aerr.Message))
		return
	}
	if aerr != nil || fresh.DockerRecoveryAttempts == 0 {
		return
	}

	region := h.store.region(ctx)
	h.scheduler.AfterScoped(region, instanceID, "docker-recovery-stable", dockerRecoveryStableFor, func(ctx context.Context) {
		if _, aerr := h.mutateInstance(ctx, instanceID, func(got *DBInstance) *protocol.AWSError {
			if got.DBInstanceStatus != "available" {
				return errInstanceMovedOn
			}
			got.DockerRecoveryAttempts = 0
			got.DockerRecoveryAvailableAt = time.Time{}
			return nil
		}); aerr != nil && aerr != errInstanceMovedOn {
			h.log.Warn("RDS: reset stable container recovery budget",
				zap.String("instance", instanceID), zap.String("error", aerr.Message))
		}
	})
}

// errInstanceMovedOn abandons a mutation whose guard no longer holds — the
// instance was stopped, deleted or modified while the caller was away. It is
// never returned to a caller: every use pairs with a check for it.
var errInstanceMovedOn = &protocol.AWSError{Code: "InstanceMovedOn"}

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

// credentialBootstrapComplete proves the engine finished every init script,
// not merely opened the temporary server used during image initialization.
// It deliberately does not authenticate as the master: AWS permits that user
// to change its password with SQL, which must not make lifecycle recovery fail.
func (h *Handler) credentialBootstrapComplete(ctx context.Context, inst *DBInstance) bool {
	if !h.dockerReady.Load() || inst.DockerContainerID == "" {
		return true
	}

	probeCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	var engineProbe string
	switch inst.Engine {
	case "postgres", "aurora-postgresql":
		engineProbe = "pg_isready --host=127.0.0.1 --port=" + strconv.Itoa(inst.Port) + " --timeout=2"
	default:
		engineProbe = "mysqladmin --protocol=tcp --host=127.0.0.1 --port=" + strconv.Itoa(inst.Port) + " --connect-timeout=2 ping"
	}
	result, err := h.docker.Exec(probeCtx, inst.DockerContainerID,
		[]string{"sh", "-c", "test -f " + credentialBootstrapMarker + " && " + engineProbe}, nil)
	return err == nil && result.ExitCode == 0
}

// engineReadiness is the timing the instance health check runs on. Its budget
// is healthCheckBudget for the reason set out there: a first boot initialises
// the data directory, which on a cold image is minutes.
var engineReadiness = readiness.Policy{
	FirstDelay: 1 * time.Second,
	Interval:   healthCheckInterval,
	Budget:     healthCheckBudget,
}

// scheduleHealthCheck polls the engine until it answers, its container dies, or
// the budget runs out, and moves the instance to available or failed
// accordingly. It never leaves an instance in creating/starting.
//
// The retry policy and the exhaustion decision live in readiness.Watch, which
// every container-starting service now shares; what stays here is the evidence,
// and it is the most careful probe in the repo: the container's own state, a
// connection, and an exec of the engine's client proving initialization
// finished. None of that is generic and none of it moved.
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
	readiness.Watch{
		Scheduler:  h.scheduler,
		Clock:      h.clk,
		Region:     region,
		ResourceID: instanceID,
		Transition: "health",
		Subject:    "the database at " + target,
		Policy:     engineReadiness,
		Probe: func(ctx context.Context) readiness.Result {
			got, aerr := h.store.getDBInstance(ctx, instanceID)
			if aerr != nil {
				return readiness.Abandoned()
			}
			if !awaitingStartup(got.DBInstanceStatus) {
				// Stopped, deleted or modified while starting — not ours.
				return readiness.Abandoned()
			}

			// A container that has exited has already decided the outcome;
			// waiting out the rest of the budget only delays saying so.
			if reason := h.containerFailureReason(ctx, got); reason != "" {
				return readiness.Doomed(reason)
			}

			conn, err := net.DialTimeout("tcp", target, dialTimeout)
			if err != nil {
				return readiness.Retry(err)
			}
			conn.Close() //nolint:errcheck // liveness probe connection
			if !h.credentialBootstrapComplete(ctx, got) {
				return readiness.Retry(errors.New(
					"the engine is accepting connections but has not finished initializing"))
			}
			return readiness.Answered()
		},
		OnReady: func(ctx context.Context) {
			got, aerr := h.store.getDBInstance(ctx, instanceID)
			if aerr != nil || got == nil {
				return
			}
			h.markAvailable(ctx, got)
		},
		OnFailed: func(ctx context.Context, reason string) {
			h.failInstance(ctx, instanceID, reason)
		},
	}.Start()
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

// stopInstanceContainerAsync keeps the AWS-visible status at stopping until
// the engine has actually stopped. Marking it stopped first creates a race in
// which an immediate StartDBInstance targets the still-shutting-down process.
func (h *Handler) stopInstanceContainerAsync(ctx context.Context, instanceID, containerID string) {
	region := h.store.region(ctx)
	h.dockerLifecycleMu.Lock()
	if h.shuttingDown.Load() {
		h.dockerLifecycleMu.Unlock()
		return
	}
	h.dockerWg.Add(1)
	h.dockerLifecycleMu.Unlock()
	go func() {
		defer h.dockerWg.Done()
		stopCtx, cancel := context.WithTimeout(middleware.ContextWithRegion(context.Background(), region), 15*time.Second)
		defer cancel()
		if err := h.docker.StopContainer(stopCtx, containerID, 10); err != nil {
			h.log.Warn("RDS: stop database container", zap.String("instance", instanceID), zap.Error(err))
			if _, aerr := h.mutateInstance(stopCtx, instanceID, func(inst *DBInstance) *protocol.AWSError {
				if inst.DBInstanceStatus != "stopping" {
					return errInstanceMovedOn
				}
				inst.DBInstanceStatus = "available"
				inst.StoppedByUser = false
				return nil
			}); aerr != nil && aerr != errInstanceMovedOn {
				h.log.Warn("RDS: restore status after failed stop", zap.String("instance", instanceID), zap.String("error", aerr.Message))
			}
			return
		}
		h.completeInstanceStop(stopCtx, instanceID)
	}()
}

// startInstanceContainerAsync runs the container start off the request path —
// a rebuild may have to pull an image — and settles the instance's status
// itself: available once engine initialization completes, failed if it cannot
// be started at all. Mirrors launchDBContainerAsync's merge
// discipline: the instance may have been stopped or deleted while the start was
// in flight, so only the container-owned fields are written back onto a fresh
// read.
func (h *Handler) startInstanceContainerAsync(ctx context.Context, instanceID string) {
	region := h.store.region(ctx)
	h.dockerLifecycleMu.Lock()
	if h.shuttingDown.Load() {
		h.dockerLifecycleMu.Unlock()
		return
	}
	h.dockerWg.Add(1)
	h.dockerLifecycleMu.Unlock()
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

		fresh, aerr := h.mutateInstance(bgCtx, instanceID, func(inst *DBInstance) *protocol.AWSError {
			if !awaitingStartup(inst.DBInstanceStatus) {
				// Stopped or deleted mid-start; leave the transition alone. The
				// container is brought back down by whoever made that transition.
				return errInstanceMovedOn
			}
			inst.DockerContainerID = got.DockerContainerID
			inst.HostPort = got.HostPort
			inst.Endpoint = got.Endpoint
			inst.DialAddress = got.DialAddress
			inst.DialPort = got.DialPort
			inst.DockerRecoveryPending = false
			return nil
		})
		if aerr != nil {
			if aerr != errInstanceMovedOn {
				h.log.Warn("RDS: persist started instance",
					zap.String("instance", instanceID), zap.String("error", aerr.Message))
			}
			return
		}

		host, port := dialTarget(fresh)
		h.scheduleHealthCheck(region, instanceID, host, port)
	}()
}
