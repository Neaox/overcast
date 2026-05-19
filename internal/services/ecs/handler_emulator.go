package ecs

// handler_emulator.go — Emulator-only endpoints behind /_ecs/ prefix.
// These are NOT part of the AWS API surface.

import (
	"encoding/binary"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// GetTaskContainerLogs returns the last 200 lines of logs for a specific
// container in a task. This is an emulator-only endpoint.
func (h *Handler) GetTaskContainerLogs(w http.ResponseWriter, r *http.Request) {
	taskArn := chi.URLParam(r, "taskArn")
	container := chi.URLParam(r, "container")

	if !h.dockerReady.Load() {
		writeEmulatorError(w, http.StatusServiceUnavailable, "Docker is not available")
		return
	}

	// Find the task across all clusters.
	tasks, aerr := h.store.listAllTasks(r.Context())
	if aerr != nil {
		writeEmulatorError(w, http.StatusInternalServerError, aerr.Message)
		return
	}

	var dockerID string
	for _, t := range tasks {
		if t.TaskArn != taskArn {
			continue
		}
		for _, c := range t.Containers {
			if c.Name == container {
				dockerID = c.DockerID
				break
			}
		}
		break
	}

	if dockerID == "" {
		writeEmulatorError(w, http.StatusNotFound, "task or container not found")
		return
	}

	raw, err := h.docker.ContainerLogs(r.Context(), dockerID, "200")
	if err != nil {
		writeEmulatorError(w, http.StatusInternalServerError, "failed to fetch logs: "+err.Error())
		return
	}

	logs := stripDockerLogHeaders(raw)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"containerName": container,
		"taskArn":       taskArn,
		"logs":          string(logs),
	})
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

// stripDockerLogHeaders removes the 8-byte Docker multiplex frame headers
// from raw container log output, returning only the payload bytes.
func stripDockerLogHeaders(raw []byte) []byte {
	out := make([]byte, 0, len(raw))
	for len(raw) >= 8 {
		size := binary.BigEndian.Uint32(raw[4:8])
		raw = raw[8:]
		if uint32(len(raw)) < size {
			// Truncated frame — take what's left.
			out = append(out, raw...)
			break
		}
		out = append(out, raw[:size]...)
		raw = raw[size:]
	}
	return out
}
