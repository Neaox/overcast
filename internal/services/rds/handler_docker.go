package rds

import (
	"context"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/serviceutil"
)

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

// handleContainerEvent processes DockerContainerDied and DockerContainerStopped
// events. If the container belongs to an RDS instance that is "available" or
// "starting", the instance status is transitioned to "stopped".
func (h *Handler) handleContainerEvent(_ context.Context, e events.Event) {
	p, ok := e.Payload.(events.DockerContainerPayload)
	if !ok || p.Service != "rds" {
		return
	}

	inst, region, found, err := serviceutil.FindRegioned[DBInstance](context.Background(), h.store.store, nsDBInstances, p.ResourceID, h.store.defaultRegion)
	if err != nil || !found {
		return
	}
	ctx := middleware.ContextWithRegion(context.Background(), region)

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

	inst, region, found, err := serviceutil.FindRegioned[DBInstance](context.Background(), h.store.store, nsDBInstances, p.ResourceID, h.store.defaultRegion)
	if err != nil || !found {
		return
	}

	switch inst.DBInstanceStatus {
	case "stopped", "starting", "creating":
		healthHost, healthPort := dialTarget(inst)
		h.scheduleHealthCheck(region, inst.DBInstanceIdentifier, healthHost, healthPort)
	}
}

// instanceOwnsContainer reports whether a DB instance record still claims this
// container's resource ID, in any region. It is the startup sweep's veto: a
// container an instance still owns is not litter, however long it has been
// stopped.
func (h *Handler) instanceOwnsContainer(resourceID string) bool {
	if resourceID == "" {
		return false
	}
	_, _, found, err := serviceutil.FindRegioned[DBInstance](
		context.Background(), h.store.store, nsDBInstances, resourceID, h.store.defaultRegion)
	if err != nil {
		// Unknown ownership: keep the container. Removing one that is still
		// owned strands an instance; keeping an orphan costs disk until the
		// next sweep.
		h.log.Warn("RDS: could not determine container ownership for the startup sweep — keeping it",
			zap.String("resource", resourceID), zap.Error(err))
		return true
	}
	return found
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

	regioned, err := serviceutil.ScanRegions[DBInstance](ctx, h.store.store, nsDBInstances, h.store.defaultRegion)
	if err != nil {
		h.log.Warn("reconcile: failed to list instances", zap.Error(err))
		return
	}

	for _, ri := range regioned {
		inst := ri.Value
		if inst.DockerContainerID == "" {
			continue // metadata-only instance — no container expected
		}
		rctx := middleware.ContextWithRegion(ctx, ri.Region)

		c := byResource[inst.DBInstanceIdentifier]
		switch {
		case c == nil:
			// Container gone — mark stopped if it was supposed to be live.
			if inst.DBInstanceStatus == "available" || inst.DBInstanceStatus == "starting" || inst.DBInstanceStatus == "creating" {
				inst.DBInstanceStatus = "stopped"
				h.store.putDBInstance(rctx, inst) //nolint:errcheck
				h.log.Info("reconcile: container missing — marked stopped",
					zap.String("instance", inst.DBInstanceIdentifier))
			}

		case c.State == "running":
			// Container is running — refresh the endpoint address (it may have
			// changed if the container was assigned a new IP) and schedule a
			// health check to verify DB connectivity before marking available.
			ecfg := engineEnvConfig[inst.Engine]
			h.setContainerDialTarget(rctx, inst, ecfg)
			h.store.putDBInstance(rctx, inst) //nolint:errcheck

			if inst.DBInstanceStatus == "creating" || inst.DBInstanceStatus == "starting" || inst.DBInstanceStatus == "stopped" || inst.DBInstanceStatus == "available" {
				healthHost, healthPort := dialTarget(inst)
				h.scheduleHealthCheck(ri.Region, inst.DBInstanceIdentifier, healthHost, healthPort)
				h.log.Info("reconcile: container running — scheduling health check",
					zap.String("instance", inst.DBInstanceIdentifier),
					zap.String("endpoint", inst.Endpoint.Address),
					zap.Int("port", inst.Endpoint.Port))
			}

		default: // exited, dead, paused, etc.
			if inst.DBInstanceStatus == "available" || inst.DBInstanceStatus == "starting" {
				inst.DBInstanceStatus = "stopped"
				h.store.putDBInstance(rctx, inst) //nolint:errcheck
				h.log.Info("reconcile: container not running — marked stopped",
					zap.String("instance", inst.DBInstanceIdentifier),
					zap.String("containerState", c.State))
			}
		}
	}
}
