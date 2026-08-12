package rds

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

const (
	// maxDockerRecoveryAttempts is deliberately an emulator implementation
	// detail, not an AWS quota. AWS exposes the terminal result — failed means
	// RDS could not recover the instance — but not its internal retry count.
	maxDockerRecoveryAttempts = 5
	dockerRecoveryBaseBackoff = time.Second
	dockerRecoveryMaxBackoff  = 30 * time.Second
	dockerRecoveryStableFor   = 2 * time.Minute
)

func dockerRecoveryBackoff(attempt int) time.Duration {
	if attempt <= 1 {
		return 0
	}
	delay := dockerRecoveryBaseBackoff
	for n := 2; n < attempt && delay < dockerRecoveryMaxBackoff; n++ {
		delay = min(delay*2, dockerRecoveryMaxBackoff)
	}
	return delay
}

// ── Docker container event handlers ──────────────────────────────────────────
//
// These methods keep RDS instance status in sync with Docker container state.
// They handle two scenarios:
//   - Ongoing: the event watcher publishes container started/died/stopped events
//   - Startup: the Supervisor lists existing containers and calls reconcileContainers
//
// The event payload only carries a resource ID — not the region the instance
// was created under — so these handlers locate the instance by scanning all
// regions and then pin the region on the context for subsequent store writes.

// containerIsOurs reports whether the container an event or a reconciliation
// scan is describing is the one this Overcast created for inst.
//
// Resource IDs are user-chosen names, not daemon-unique. Two Overcasts sharing
// a Docker daemon can each hold a DB instance called "mydb", and each one's
// container carries overcast.resource-id=mydb — so matching on that alone lets
// one of them restart, adopt, or stop the other's live database. An RDS
// container keeps the database in its writable layer, which makes that a data
// loss, not a nuisance. See InstanceDomain.ContainerIsOurs for what settles it,
// and why an unlabelled container is referred to the record rather than
// refused.
func (h *Handler) containerIsOurs(ctx context.Context, containerID, owner string, inst *DBInstance) bool {
	return h.instances.ContainerIsOurs(ctx, containerID, owner, inst.DockerContainerID)
}

// ourContainer picks the container in candidates that this Overcast created
// for inst, if any. The candidates all carry inst's resource ID, which on a
// shared daemon means "every Overcast's container for that name", not this
// one's.
func (h *Handler) ourContainer(ctx context.Context, candidates []*docker.ContainerSummary, inst *DBInstance) *docker.ContainerSummary {
	return h.instances.OwnContainer(ctx, candidates, inst.DockerContainerID)
}

// handleContainerEvent recovers an RDS engine that exits outside the RDS API.
// Docker reports the same die event for a process crash and for a deliberate
// Docker Desktop stop, so neither can establish RDS user intent. StopDBInstance
// moves the record to stopping before touching Docker and is therefore left
// alone; every instance still expected to run is restarted and health-checked.
func (h *Handler) handleContainerEvent(_ context.Context, e events.Event) {
	p, ok := e.Payload.(events.DockerContainerPayload)
	if !ok || p.Service != "rds" || e.Type != events.DockerContainerDied || h.shuttingDown.Load() {
		return
	}

	inst, region, found, err := serviceutil.FindRegioned[DBInstance](context.Background(), h.store.store, nsDBInstances, p.ResourceID, h.store.defaultRegion)
	if err != nil || !found {
		return
	}
	ctx := middleware.ContextWithRegion(context.Background(), region)
	log := h.log.WithRecorder(ctx)
	if !h.containerIsOurs(ctx, p.ContainerID, p.Instance, inst) {
		// Another Overcast's database exiting is its business. Recovering from
		// it here would restart a container of ours that never stopped, and
		// spend this instance's recovery budget doing it.
		return
	}

	// The status is re-read under the record's lock rather than written back
	// from the scan above: the event stream is a writer like any other, and the
	// copy it found is already older than whatever an API call is doing now.
	switch inst.DBInstanceStatus {
	case "available", "creating", "starting":
		// Read the container's output while the container still exists. A
		// database that dies on its own leaves its explanation here, and
		// Docker discards it with the container. The read is a Docker call, so
		// it stays outside the record's lock, on the snapshot found above.
		h.captureContainerLogs(ctx, inst)
		if !h.recoverExpectedContainer(ctx, p.ResourceID, inst) {
			return
		}
		log.Info("instance container exited — recovering",
			zap.String("instance", p.ResourceID),
			zap.String("action", p.Action))
	}
}

// handleContainerStarted processes DockerContainerStarted events. If the
// container belongs to an RDS instance that was stopped/starting/creating,
// a health check is re-scheduled to verify DB connectivity before marking
// the instance available.
func (h *Handler) handleContainerStarted(_ context.Context, e events.Event) {
	p, ok := e.Payload.(events.DockerContainerPayload)
	if !ok || p.Service != "rds" || h.shuttingDown.Load() {
		return
	}

	inst, region, found, err := serviceutil.FindRegioned[DBInstance](context.Background(), h.store.store, nsDBInstances, p.ResourceID, h.store.defaultRegion)
	if err != nil || !found {
		return
	}

	ctx := middleware.ContextWithRegion(context.Background(), region)
	log := h.log.WithRecorder(ctx)
	if !h.containerIsOurs(ctx, p.ContainerID, p.Instance, inst) {
		// The stopped/StoppedByUser branch below stops the container the event
		// names. Reaching it for a container this instance did not create
		// shuts down another Overcast's live database on the strength of a
		// stop request nobody made against it.
		return
	}
	switch inst.DBInstanceStatus {
	case "stopped":
		if inst.StoppedByUser {
			if err := h.docker.StopContainer(ctx, p.ContainerID, 10); err != nil {
				log.Warn("RDS: stop Docker-started container for explicitly stopped instance",
					zap.String("instance", p.ResourceID), zap.Error(err))
			}
			return
		}
		h.recoverExpectedContainer(ctx, inst.DBInstanceIdentifier, nil)
	case "starting", "creating":
		healthHost, healthPort := dialTarget(inst)
		h.scheduleHealthCheck(region, inst.DBInstanceIdentifier, healthHost, healthPort)
	}
}

// handleDockerDaemonConnected reconciles changes that happened while the
// Docker event stream was unavailable. The watcher publishes this on both its
// initial connection and every reconnection; the pointer check prevents an RDS
// service wired to one daemon from reacting to another configured socket.
func (h *Handler) handleDockerDaemonConnected(ctx context.Context, e events.Event) {
	log := h.log.WithRecorder(ctx)
	p, ok := e.Payload.(docker.DaemonConnectedPayload)
	if !ok || !p.Reconnected || p.Client == nil || p.Client != h.docker || h.shuttingDown.Load() {
		return
	}
	containers, err := h.docker.ListContainers(ctx, serviceName)
	if err != nil {
		log.Warn("RDS: reconcile after Docker reconnect", zap.Error(err))
		return
	}
	h.reconcileContainers(ctx, containers)
}

// instanceOwnsContainer reports whether a DB instance record still claims this
// container's resource ID, in any region. It is the startup sweep's veto: a
// container an instance still owns is not litter, however long it has been
// stopped.
func (h *Handler) instanceOwnsContainer(resourceID string) bool {
	if resourceID == "" {
		return false
	}
	log := h.log.WithRecorder(context.Background())
	_, _, found, err := serviceutil.FindRegioned[DBInstance](
		context.Background(), h.store.store, nsDBInstances, resourceID, h.store.defaultRegion)
	if err != nil {
		// Unknown ownership: keep the container. Removing one that is still
		// owned strands an instance; keeping an orphan costs disk until the
		// next sweep.
		log.Warn("RDS: could not determine container ownership for the startup sweep — keeping it",
			zap.String("resource", resourceID), zap.Error(err))
		return true
	}
	return found
}

// recoverExpectedContainer brings back the engine for an instance that was
// meant to be live when Overcast last observed it. Reconciliation works from a
// scan, so the status is checked again under the instance lock before any
// Docker work starts: a concurrent StopDBInstance must win rather than have
// startup recovery start its container behind its back.
func (h *Handler) recoverExpectedContainer(ctx context.Context, instanceID string, captured *DBInstance) bool {
	region := h.store.region(ctx)
	log := h.log.WithRecorder(ctx)
	// A new exit before this timer fires proves the previous recovery was not
	// stable. Preserve its attempt count and replace the timer only after the
	// next successful health check.
	h.scheduler.CancelScoped(region, instanceID, "docker-recovery-stable")

	var (
		delay       time.Duration
		exhausted   bool
		priorStatus string
	)
	reason := fmt.Sprintf("the database container repeatedly exited and could not be recovered after %d attempts", maxDockerRecoveryAttempts)
	if _, aerr := h.mutateInstance(ctx, instanceID, func(inst *DBInstance) *protocol.AWSError {
		priorStatus = inst.DBInstanceStatus
		if captured != nil {
			inst.LastLogs = captured.LastLogs
			inst.LastLogsAt = captured.LastLogsAt
		}

		switch inst.DBInstanceStatus {
		case "available":
		case "creating", "starting":
			if inst.DockerRecoveryPending {
				// Docker can deliver duplicate die events. One pending restart is
				// enough; replacing its keyed timer would postpone recovery.
				return errInstanceMovedOn
			}
		case "stopped":
			if inst.StoppedByUser {
				return errInstanceMovedOn
			}
		case "failed":
			if !inst.DockerRecoveryPending {
				return errInstanceMovedOn
			}
		default:
			return errInstanceMovedOn
		}

		if !inst.DockerRecoveryAvailableAt.IsZero() &&
			h.clk.Now().Sub(inst.DockerRecoveryAvailableAt) >= dockerRecoveryStableFor {
			inst.DockerRecoveryAttempts = 0
		}
		inst.DockerRecoveryAvailableAt = time.Time{}
		if inst.DockerRecoveryAttempts >= maxDockerRecoveryAttempts {
			inst.DBInstanceStatus = "failed"
			inst.DockerRecoveryPending = false
			inst.StatusReason = reason
			exhausted = true
			return nil
		}

		inst.DBInstanceStatus = "starting"
		inst.DockerRecoveryAttempts++
		inst.DockerRecoveryPending = true
		delay = dockerRecoveryBackoff(inst.DockerRecoveryAttempts)
		return nil
	}); aerr != nil {
		if aerr != errInstanceMovedOn {
			log.Warn("reconcile: prepare instance recovery",
				zap.String("instance", instanceID), zap.String("error", aerr.Message))
		}
		return false
	}
	if exhausted {
		h.recordInstanceFailureEvent(ctx, instanceID, priorStatus, reason)
		log.Warn("RDS: automatic container recovery exhausted",
			zap.String("instance", instanceID), zap.String("reason", reason))
		return false
	}

	h.scheduler.AfterScoped(region, instanceID, "docker-recovery", delay, func(ctx context.Context) {
		h.startInstanceContainerAsync(ctx, instanceID)
	})
	return true
}

// reconcileContainers is called once at startup after Docker becomes available.
// It compares the live container state against stored RDS instances and corrects
// any status drift (e.g. containers that exited while Overcast was not running).
func (h *Handler) reconcileContainers(ctx context.Context, containers []docker.ContainerSummary) {
	log := h.log.WithRecorder(ctx)
	// Index containers by resource ID for fast lookup. One resource ID can name
	// several containers — a DB instance called "mydb" under each Overcast
	// sharing this daemon — so they are collected rather than overwritten, and
	// ourContainer picks this instance's out of them below.
	byResource := make(map[string][]*docker.ContainerSummary, len(containers))
	for i := range containers {
		rid := containers[i].ResourceID()
		if rid != "" {
			byResource[rid] = append(byResource[rid], &containers[i])
		}
	}

	regioned, err := serviceutil.ScanRegions[DBInstance](ctx, h.store.store, nsDBInstances, h.store.defaultRegion)
	if err != nil {
		log.Warn("reconcile: failed to list instances", zap.Error(err))
		return
	}

	for _, ri := range regioned {
		inst := ri.Value
		if inst.DockerContainerID == "" {
			continue // metadata-only instance — no container expected
		}
		rctx := middleware.ContextWithRegion(ctx, ri.Region)

		candidates := byResource[inst.DBInstanceIdentifier]
		c := h.ourContainer(rctx, candidates, inst)
		switch {
		case c == nil:
			// A missing container is runtime drift, not a user-requested stop.
			// Rebuild instances that were expected to be live; an explicitly
			// stopped instance stays stopped.
			if len(candidates) > 0 {
				// Not missing so much as somebody else's. Say so: the rebuild
				// below will collide with it over the container name, and that
				// failure reads as a Docker problem without this line.
				log.Warn("reconcile: the container for this name belongs to another Overcast on this daemon — leaving it alone",
					zap.String("instance", inst.DBInstanceIdentifier))
			}
			if expectsDockerRecovery(inst) {
				if h.recoverExpectedContainer(rctx, inst.DBInstanceIdentifier, nil) {
					log.Info("reconcile: container missing — rebuilding",
						zap.String("instance", inst.DBInstanceIdentifier))
				}
			}

		case c.State == "running":
			if inst.DBInstanceStatus == "stopped" && inst.StoppedByUser {
				// StopDBInstance is the only durable stop signal. If somebody
				// starts that engine directly through Docker, restore the
				// control-plane state instead of adopting the runtime state.
				if err := h.docker.StopContainer(rctx, c.ID, 10); err != nil {
					log.Warn("reconcile: stop container for explicitly stopped instance",
						zap.String("instance", inst.DBInstanceIdentifier), zap.Error(err))
				} else {
					log.Info("reconcile: Docker-started container stopped to preserve StopDBInstance",
						zap.String("instance", inst.DBInstanceIdentifier))
				}
				continue
			}

			// Container is running — refresh the endpoint address (it may have
			// changed if the container was assigned a new IP) and schedule a
			// health check to verify DB connectivity before marking available.
			// The Docker inspect stays outside the record's lock; only the two
			// fields it decides are written back, onto a fresh read, because
			// this sweep runs while the API is already serving.
			ecfg := engineEnvConfig[inst.Engine]
			h.setContainerDialTarget(rctx, inst, ecfg)
			if _, aerr := h.mutateInstance(rctx, inst.DBInstanceIdentifier, func(stored *DBInstance) *protocol.AWSError {
				stored.DialAddress = inst.DialAddress
				stored.DialPort = inst.DialPort
				return nil
			}); aerr != nil {
				log.Warn("reconcile: persist dial target",
					zap.String("instance", inst.DBInstanceIdentifier), zap.String("error", aerr.Message))
			}

			if inst.DBInstanceStatus == "stopped" || (inst.DBInstanceStatus == "failed" && inst.DockerRecoveryPending) {
				if h.recoverExpectedContainer(rctx, inst.DBInstanceIdentifier, nil) {
					log.Info("reconcile: running container recovering from external stop",
						zap.String("instance", inst.DBInstanceIdentifier))
				}
			} else if inst.DBInstanceStatus == "creating" || inst.DBInstanceStatus == "starting" || inst.DBInstanceStatus == "available" {
				healthHost, healthPort := dialTarget(inst)
				h.scheduleHealthCheck(ri.Region, inst.DBInstanceIdentifier, healthHost, healthPort)
				log.Info("reconcile: container running — scheduling health check",
					zap.String("instance", inst.DBInstanceIdentifier),
					zap.String("endpoint", inst.Endpoint.Address),
					zap.Int("port", inst.Endpoint.Port))
			}

		default: // exited, dead, paused, etc.
			if expectsDockerRecovery(inst) {
				// Docker may have restarted while Overcast was away. The stored
				// instance status is the desired state, so restart its engine and
				// health-check it instead of treating drift as a user stop.
				if h.recoverExpectedContainer(rctx, inst.DBInstanceIdentifier, nil) {
					log.Info("reconcile: container not running — restarting",
						zap.String("instance", inst.DBInstanceIdentifier),
						zap.String("containerState", c.State))
				}
			}
		}
	}
}

func expectsDockerRecovery(inst *DBInstance) bool {
	switch inst.DBInstanceStatus {
	case "available", "creating", "starting":
		return true
	case "stopped":
		return !inst.StoppedByUser
	case "failed":
		return inst.DockerRecoveryPending
	default:
		return false
	}
}
