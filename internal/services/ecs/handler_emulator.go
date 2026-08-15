package ecs

// handler_emulator.go — Emulator-only endpoints behind /_overcast/ecs/ prefix.
// These are NOT part of the AWS API surface.

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/docker"
)

// GetTaskContainerLogs returns the last 200 lines of logs for a specific
// container in a task. This is an emulator-only endpoint.
func (h *Handler) GetTaskContainerLogs(w http.ResponseWriter, r *http.Request) {
	taskArn := chi.URLParam(r, "taskArn")
	container := chi.URLParam(r, "container")

	// Find the task across all clusters.
	tasks, aerr := h.store.listAllTasks(r.Context())
	if aerr != nil {
		writeEmulatorError(w, http.StatusInternalServerError, aerr.Message)
		return
	}

	var dockerID, clusterName, taskID, resolvedTaskArn string
	for _, t := range tasks {
		if t.TaskArn != taskArn && extractTaskID(t.TaskArn) != taskArn {
			continue
		}
		for _, c := range t.Containers {
			if c.Name == container {
				dockerID = c.DockerID
				clusterName = extractClusterName(t.ClusterArn)
				taskID = extractTaskID(t.TaskArn)
				resolvedTaskArn = t.TaskArn
				break
			}
		}
		break
	}

	if dockerID == "" {
		writeEmulatorError(w, http.StatusNotFound, "task or container not found")
		return
	}

	logs, found, aerr := h.store.getTaskContainerLogs(r.Context(), clusterName, taskID, container)
	if aerr != nil {
		writeEmulatorError(w, http.StatusInternalServerError, aerr.Message)
		return
	}
	if found {
		writeTaskContainerLogs(w, resolvedTaskArn, container, taskLogsResponse{
			Logs: logs.Logs, LogSource: "retained", CapturedAt: logs.CapturedAt,
		})
		return
	}

	if !h.dockerReady.Load() {
		writeEmulatorError(w, http.StatusServiceUnavailable, "Docker is not available")
		return
	}

	raw, err := h.docker.ContainerLogs(r.Context(), dockerID, "200")
	if err != nil {
		writeEmulatorError(w, http.StatusInternalServerError, "failed to fetch logs: "+err.Error())
		return
	}

	// docker.DemuxStream and not a local stripper: a container started with a
	// TTY writes no frame headers at all, and only DemuxStream tells the two
	// shapes apart. ECS used to carry its own copy that did not, so every TTY
	// task's logs came back with the first eight bytes eaten and the rest cut
	// at whatever offset the ninth through twelfth bytes of its own output
	// happened to spell.
	writeTaskContainerLogs(w, resolvedTaskArn, container, taskLogsResponse{
		Logs: string(docker.DemuxStream(raw)), LogSource: "container",
	})
}

// taskLogsResponse is the emulator-only log payload. It is deliberately not an
// AWS shape: the retained copy and the time it was taken have no place in
// DescribeTasks, which is the whole reason the retention lives in its own
// namespace rather than on the Task record.
type taskLogsResponse struct {
	ContainerName string `json:"containerName"`
	TaskArn       string `json:"taskArn"`
	Logs          string `json:"logs"`
	// LogSource is "container" for a live read and "retained" for the copy kept
	// from a container that has since been removed, so a caller can say which
	// it is showing rather than implying the task is still running.
	LogSource string `json:"logSource,omitempty"`
	// CapturedAt dates a retained copy. Empty on a live read, which is now.
	CapturedAt string `json:"capturedAt,omitempty"`
}

// writeTaskContainerLogs stamps the identity fields onto out and writes it, so
// each caller only has to say where the logs came from.
func writeTaskContainerLogs(w http.ResponseWriter, taskArn, container string, out taskLogsResponse) {
	out.TaskArn = taskArn
	out.ContainerName = container
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(&out)
}

// ListClusterTasks returns all tasks for a given cluster.
// This is an emulator-only endpoint.
func (h *Handler) ListClusterTasks(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")

	tasks, aerr := h.store.listTasks(r.Context(), cluster)
	if aerr != nil {
		writeEmulatorError(w, http.StatusInternalServerError, aerr.Message)
		return
	}

	type containerSummary struct {
		ContainerArn string `json:"containerArn"`
		Name         string `json:"name"`
		LastStatus   string `json:"lastStatus"`
	}
	type taskSummary struct {
		TaskArn    string             `json:"taskArn"`
		LastStatus string             `json:"lastStatus"`
		Containers []containerSummary `json:"containers"`
	}

	out := make([]taskSummary, 0, len(tasks))
	for _, t := range tasks {
		cs := make([]containerSummary, 0, len(t.Containers))
		for _, c := range t.Containers {
			cs = append(cs, containerSummary{
				ContainerArn: c.ContainerArn,
				Name:         c.Name,
				LastStatus:   c.LastStatus,
			})
		}
		out = append(out, taskSummary{
			TaskArn:    t.TaskArn,
			LastStatus: t.LastStatus,
			Containers: cs,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"tasks": out})
}

// writeEmulatorError writes a simple JSON error for emulator-only endpoints.
func writeEmulatorError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
