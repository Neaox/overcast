package rds

// handler_emulator.go — Emulator-only endpoints behind /_rds/ prefix.
// These are NOT part of the AWS API surface.

import (
	"encoding/binary"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// GetInstanceLogs returns the last 200 lines of logs for an RDS instance's
// Docker container. This is an emulator-only endpoint.
func (h *Handler) GetInstanceLogs(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "instanceId")

	if !h.dockerReady.Load() {
		writeRDSEmulatorError(w, http.StatusServiceUnavailable, "Docker is not available")
		return
	}

	inst, aerr := h.store.getDBInstance(r.Context(), instanceID)
	if aerr != nil {
		writeRDSEmulatorError(w, http.StatusNotFound, "instance not found")
		return
	}

	if inst.DockerContainerID == "" {
		writeRDSEmulatorError(w, http.StatusNotFound, "instance has no associated container")
		return
	}

	raw, err := h.docker.ContainerLogs(r.Context(), inst.DockerContainerID, "200")
	if err != nil {
		writeRDSEmulatorError(w, http.StatusInternalServerError, "failed to fetch logs: "+err.Error())
		return
	}

	logs := stripRDSDockerLogHeaders(raw)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"instanceId": instanceID,
		"engine":     inst.Engine,
		"logs":       string(logs),
	})
}

// writeRDSEmulatorError writes a simple JSON error for emulator-only endpoints.
func writeRDSEmulatorError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// stripRDSDockerLogHeaders removes the 8-byte Docker multiplex frame headers
// from raw container log output, returning only the payload bytes.
func stripRDSDockerLogHeaders(raw []byte) []byte {
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
