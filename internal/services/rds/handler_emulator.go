package rds

// handler_emulator.go — Emulator-only endpoints behind /_overcast/rds/ prefix.
// These are NOT part of the AWS API surface.
//
// The logs endpoint exists to answer one question: why will this database not
// start? It used to be unable to. It read the container's live logs and
// nothing else, so the moment the container was gone — which, for a database
// that failed to start, is exactly when someone opens this tab — it answered
// with the Docker daemon's "No such container" as an HTTP 500 and left the
// pane empty.
//
// Three things fix that: a container Docker no longer has is a 404 and not a
// server error; the instance's own status and failure reason travel with the
// response, because "failed: the database container exited with code 1" is
// most of the diagnosis; and the container's last output is kept on the
// instance record when it dies, so there is something left to read.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

// maxRetainedLogBytes bounds what is copied off a dying container onto its
// instance record. Enough for a stack trace and the lines around it, small
// enough that the record stays a record. The tail is kept, not the head — the
// last thing an engine says before it gives up is the useful part.
const maxRetainedLogBytes = 16 * 1024

// logsResponse is the emulator-only logs payload. Every field is best-effort
// except InstanceID and Status: the point is to say as much as is known.
//
// The same shape is sent on the 404 — a 404 here means "no logs", not "no
// instance", and the status and failure reason are the whole reason the caller
// asked — with Error set so an HTTP-aware client still sees a message.
type logsResponse struct {
	Error        string `json:"error,omitempty"`
	InstanceID   string `json:"instanceId"`
	Engine       string `json:"engine"`
	Status       string `json:"status"`
	StatusReason string `json:"statusReason,omitempty"`
	Logs         string `json:"logs"`
	// LogSource is "container" for a live read and "retained" for the copy
	// kept from a container that has since died or been removed, so the UI can
	// say which it is showing rather than implying the database is still there.
	LogSource  string `json:"logSource,omitempty"`
	CapturedAt string `json:"capturedAt,omitempty"`
	// Message explains the situation in one sentence when the logs alone do
	// not, which is most of the time something has gone wrong.
	Message string `json:"message,omitempty"`
}

// GetInstanceLogs returns the last 200 lines of logs for an RDS instance's
// Docker container, falling back to the tail retained when that container
// died. This is an emulator-only endpoint.
func (h *Handler) GetInstanceLogs(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "instanceId")

	inst, aerr := h.store.getDBInstance(r.Context(), instanceID)
	if aerr != nil {
		writeRDSEmulatorError(w, http.StatusNotFound, "instance not found")
		return
	}

	out := logsResponse{
		InstanceID:   instanceID,
		Engine:       inst.Engine,
		Status:       inst.DBInstanceStatus,
		StatusReason: inst.StatusReason,
	}

	// Live logs, when there is a container to read them from.
	var liveErr error
	dockerReady := h.dockerReady.Load()
	if dockerReady && inst.DockerContainerID != "" {
		raw, err := h.docker.ContainerLogs(r.Context(), inst.DockerContainerID, "200")
		if err != nil {
			liveErr = err
		} else {
			out.Logs = string(docker.DemuxStream(raw))
			out.LogSource = "container"
		}
	}

	if out.Logs == "" && inst.LastLogs != "" {
		out.Logs = inst.LastLogs
		out.LogSource = "retained"
		out.CapturedAt = inst.LastLogsAt
	}

	out.Message = logsExplanation(inst, &out, dockerReady, liveErr)

	// Nothing at all to show, and no reason to offer: that is a genuine
	// not-found, not a server error. A container Docker has forgotten is the
	// commonest way to get here and it used to be a 500.
	if out.Logs == "" && out.StatusReason == "" {
		out.Error = out.Message
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(&out)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(&out)
}

// logsExplanation writes the one sentence that turns an empty pane into a
// diagnosis.
//
// The retained cases come first on purpose: an instance whose container died
// before Docker itself went away still has logs worth reading, and telling
// that caller it is "metadata-only" would be false as well as useless.
func logsExplanation(inst *DBInstance, out *logsResponse, dockerReady bool, liveErr error) string {
	switch {
	case out.LogSource == "retained" && liveErr != nil && docker.IsNotFound(liveErr):
		return "The database container no longer exists. These are the last logs Overcast captured before it went away."
	case out.LogSource == "retained":
		return "The database container is not readable. These are the last logs Overcast captured from it."
	case !dockerReady:
		return "Docker is not available, so this DB instance is metadata-only and has no database container to produce logs."
	case inst.DockerContainerID == "":
		return "No database container was ever started for this DB instance — it is metadata-only."
	case liveErr != nil && docker.IsNotFound(liveErr):
		return "The database container no longer exists and no logs were captured before it went away."
	case liveErr != nil:
		return "Could not read the container's logs: " + liveErr.Error()
	case out.Logs == "":
		return "The database container has not produced any output yet."
	case inst.DBInstanceStatus == "failed":
		return "This DB instance failed to start; the container's own output below is the engine's account of why."
	default:
		return ""
	}
}

// captureContainerLogs copies a bounded tail of the container's output onto
// the instance record, so it survives the container. Mutates inst in place;
// the caller persists. Best-effort by design — a database that will not start
// must still reach "failed" when its logs cannot be read.
func (h *Handler) captureContainerLogs(ctx context.Context, inst *DBInstance) {
	log := h.log.WithRecorder(ctx)
	if inst == nil || !h.dockerReady.Load() || inst.DockerContainerID == "" || h.docker == nil {
		return
	}
	raw, err := h.docker.ContainerLogs(ctx, inst.DockerContainerID, "200")
	if err != nil {
		log.Debug("RDS: could not capture container logs",
			zap.String("instance", inst.DBInstanceIdentifier), zap.Error(err))
		return
	}
	logs := strings.TrimSpace(string(docker.DemuxStream(raw)))
	if logs == "" {
		return
	}
	inst.LastLogs = serviceutil.TailBytes(logs, maxRetainedLogBytes)
	inst.LastLogsAt = h.clk.Now().UTC().Format("2006-01-02T15:04:05Z")
}

// writeRDSEmulatorError writes a simple JSON error for emulator-only endpoints.
func writeRDSEmulatorError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
