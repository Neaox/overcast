package rds

import (
	"context"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
)

// ── StopDBInstance ───────────────────────────────────────────────────────────

// StopDBInstance stops a running DB instance.
func (h *Handler) StopDBInstance(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("DBInstanceIdentifier")
	if id == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("DBInstanceIdentifier is required"))
		return
	}

	inst, aerr := h.store.getDBInstance(r.Context(), id)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	if inst.DBInstanceStatus != "available" {
		protocol.WriteQueryXMLError(w, r, errInvalidDBInstanceState(id, "must be available to stop"))
		return
	}

	inst.DBInstanceStatus = "stopping"
	if aerr := h.store.putDBInstance(r.Context(), inst); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	// If Docker container is running, stop it (don't remove).
	if h.dockerReady.Load() && inst.DockerContainerID != "" {
		if h.gc != nil {
			h.gc.StopNow(inst.DockerContainerID)
		} else {
			_ = h.docker.StopContainer(r.Context(), inst.DockerContainerID, 10)
		}
	}

	// Transition stopping → stopped.  With a real clock the scheduler runs
	// 0-delay callbacks synchronously; with a mock clock the transition stays
	// pending until clock.Add is called.
	if inst.DockerContainerID == "" {
		instID := id
		h.scheduler.After(instID+":stopped", 0, func() {
			ctx := context.Background()
			got, aerr := h.store.getDBInstance(ctx, instID)
			if aerr != nil {
				return
			}
			if got.DBInstanceStatus == "stopping" {
				got.DBInstanceStatus = "stopped"
				h.store.putDBInstance(ctx, got) //nolint:errcheck
			}
		})
	} else {
		// Docker: schedule async transition.
		instID := id
		h.scheduler.After(instID+":stopped", 0, func() {
			ctx := context.Background()
			got, aerr := h.store.getDBInstance(ctx, instID)
			if aerr != nil {
				return
			}
			if got.DBInstanceStatus == "stopping" {
				got.DBInstanceStatus = "stopped"
				h.store.putDBInstance(ctx, got) //nolint:errcheck
			}
		})
	}

	h.publish(r, events.RDSInstanceStopped, events.ResourcePayload{Name: id})

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlStopDBInstanceResponse{
		Xmlns:            rdsXMLNS,
		Result:           xmlStopDBInstanceResult{DBInstance: toXMLDBInstance(inst)},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ── StartDBInstance ──────────────────────────────────────────────────────────

// StartDBInstance starts a previously stopped DB instance.
func (h *Handler) StartDBInstance(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("DBInstanceIdentifier")
	if id == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("DBInstanceIdentifier is required"))
		return
	}

	inst, aerr := h.store.getDBInstance(r.Context(), id)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	if inst.DBInstanceStatus != "stopped" {
		protocol.WriteQueryXMLError(w, r, errInvalidDBInstanceState(id, "must be stopped to start"))
		return
	}

	inst.DBInstanceStatus = "starting"
	if aerr := h.store.putDBInstance(r.Context(), inst); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	// If Docker container exists, restart it.
	if h.dockerReady.Load() && inst.DockerContainerID != "" {
		if err := h.docker.StartContainer(r.Context(), inst.DockerContainerID); err != nil {
			h.log.Warn("failed to start RDS container", zap.String("instance", id), zap.Error(err))
		}
		// Schedule health check.
		h.scheduleHealthCheck(id, inst.Endpoint.Address, inst.Endpoint.Port)
	} else {
		// Metadata-only: transition starting → available.  Scheduler runs
		// 0-delay callbacks synchronously with a real clock.
		instID2 := id
		h.scheduler.After(instID2+":available", 0, func() {
			ctx := context.Background()
			got, aerr := h.store.getDBInstance(ctx, instID2)
			if aerr != nil {
				return
			}
			if got.DBInstanceStatus == "starting" {
				got.DBInstanceStatus = "available"
				h.store.putDBInstance(ctx, got) //nolint:errcheck
			}
		})
	}

	h.publish(r, events.RDSInstanceStarted, events.ResourcePayload{Name: id})

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlStartDBInstanceResponse{
		Xmlns:            rdsXMLNS,
		Result:           xmlStartDBInstanceResult{DBInstance: toXMLDBInstance(inst)},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ── ModifyDBInstance ─────────────────────────────────────────────────────────

// ModifyDBInstance modifies metadata properties of an existing DB instance.
func (h *Handler) ModifyDBInstance(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("DBInstanceIdentifier")
	if id == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("DBInstanceIdentifier is required"))
		return
	}

	inst, aerr := h.store.getDBInstance(r.Context(), id)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	// Apply modifications.
	if v := r.FormValue("DBInstanceClass"); v != "" {
		inst.DBInstanceClass = v
	}
	if v := r.FormValue("AllocatedStorage"); v != "" {
		inst.AllocatedStorage = formInt(r, "AllocatedStorage", inst.AllocatedStorage)
	}
	if v := r.FormValue("EngineVersion"); v != "" {
		if v != inst.EngineVersion {
			h.log.Warn("EngineVersion change requested — restart would be needed in production",
				zap.String("instance", id), zap.String("from", inst.EngineVersion), zap.String("to", v))
		}
		inst.EngineVersion = v
	}
	if v := r.FormValue("MultiAZ"); v != "" {
		inst.MultiAZ = v == "true"
	}
	if v := r.FormValue("StorageType"); v != "" {
		inst.StorageType = v
	}

	// Transition through modifying → available.
	prevStatus := inst.DBInstanceStatus
	inst.DBInstanceStatus = "modifying"
	if aerr := h.store.putDBInstance(r.Context(), inst); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	instID := id
	h.scheduler.After(instID+":modified", 500*time.Millisecond, func() {
		ctx := context.Background()
		got, aerr := h.store.getDBInstance(ctx, instID)
		if aerr != nil {
			return
		}
		if got.DBInstanceStatus == "modifying" {
			if prevStatus == "available" || prevStatus == "" {
				got.DBInstanceStatus = "available"
			} else {
				got.DBInstanceStatus = prevStatus
			}
			h.store.putDBInstance(ctx, got) //nolint:errcheck
		}
	})

	h.publish(r, events.RDSInstanceModified, events.ResourcePayload{Name: id})

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlModifyDBInstanceResponse{
		Xmlns:            rdsXMLNS,
		Result:           xmlModifyDBInstanceResult{DBInstance: toXMLDBInstance(inst)},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}
