package rds

import (
	"context"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
)

// ── Docker container event handlers ──────────────────────────────────────────
//
// These methods keep RDS instance status in sync with Docker container state.
// They handle two scenarios:
//   - Ongoing: the event watcher publishes container started/died/stopped events
//   - Startup: the Supervisor lists existing containers and calls reconcileContainers

// handleContainerEvent processes DockerContainerDied and DockerContainerStopped
// events. If the container belongs to an RDS instance that is "available" or
// "starting", the instance status is transitioned to "stopped".
func (h *Handler) handleContainerEvent(_ context.Context, e events.Event) {
	p, ok := e.Payload.(events.DockerContainerPayload)
	if !ok || p.Service != "rds" {
		return
	}

	ctx := context.Background()
	inst, aerr := h.store.getDBInstance(ctx, p.ResourceID)
	if aerr != nil || inst == nil {
		return
	}

	switch inst.DBInstanceStatus {
	case "available", "starting":
		inst.DBInstanceStatus = "stopped"
		h.store.putDBInstance(ctx, inst) //nolint:errcheck
		h.log.Info("instance container stopped",
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
	if !ok || p.Service != "rds" {
		return
	}

	ctx := context.Background()
	inst, aerr := h.store.getDBInstance(ctx, p.ResourceID)
	if aerr != nil || inst == nil {
		return
	}

	switch inst.DBInstanceStatus {
	case "stopped", "starting", "creating":
		h.scheduleHealthCheck(inst.DBInstanceIdentifier, inst.Endpoint.Address, inst.Endpoint.Port)
	}
}

// reconcileContainers is called once at startup after Docker becomes available.
// It compares the live container state against stored RDS instances and corrects
// any status drift (e.g. containers that exited while Overcast was not running).
func (h *Handler) reconcileContainers(ctx context.Context, containers []docker.ContainerSummary) {
	// Index containers by resource ID for fast lookup.
	byResource := make(map[string]*docker.ContainerSummary, len(containers))
	for i := range containers {
		rid := containers[i].ResourceID()
		if rid != "" {
			byResource[rid] = &containers[i]
		}
	}

	instances, aerr := h.store.listDBInstances(ctx)
	if aerr != nil {
		h.log.Warn("reconcile: failed to list instances", zap.Error(aerr))
		return
	}

	for _, inst := range instances {
		if inst.DockerContainerID == "" {
			continue // metadata-only instance — no container expected
		}

		c := byResource[inst.DBInstanceIdentifier]
		switch {
		case c == nil:
			// Container gone — mark stopped if it was supposed to be live.
			if inst.DBInstanceStatus == "available" || inst.DBInstanceStatus == "starting" || inst.DBInstanceStatus == "creating" {
				inst.DBInstanceStatus = "stopped"
				h.store.putDBInstance(ctx, inst) //nolint:errcheck
				h.log.Info("reconcile: container missing — marked stopped",
					zap.String("instance", inst.DBInstanceIdentifier))
			}

		case c.State == "running":
			// Container is running — refresh the endpoint address (it may have
			// changed if the container was assigned a new IP) and schedule a
			// health check to verify DB connectivity before marking available.
			ecfg := engineEnvConfig[inst.Engine]
			h.setContainerEndpoint(ctx, inst, ecfg)
			h.store.putDBInstance(ctx, inst) //nolint:errcheck

			if inst.DBInstanceStatus == "creating" || inst.DBInstanceStatus == "starting" || inst.DBInstanceStatus == "stopped" || inst.DBInstanceStatus == "available" {
				h.scheduleHealthCheck(inst.DBInstanceIdentifier, inst.Endpoint.Address, inst.Endpoint.Port)
				h.log.Info("reconcile: container running — scheduling health check",
					zap.String("instance", inst.DBInstanceIdentifier),
					zap.String("endpoint", inst.Endpoint.Address),
					zap.Int("port", inst.Endpoint.Port))
			}

		default: // exited, dead, paused, etc.
			if inst.DBInstanceStatus == "available" || inst.DBInstanceStatus == "starting" {
				inst.DBInstanceStatus = "stopped"
				h.store.putDBInstance(ctx, inst) //nolint:errcheck
				h.log.Info("reconcile: container not running — marked stopped",
					zap.String("instance", inst.DBInstanceIdentifier),
					zap.String("containerState", c.State))
			}
		}
	}
}
