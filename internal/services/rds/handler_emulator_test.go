package rds

// handler_emulator_test.go — the logs endpoint, which exists so that somebody
// staring at a database that will not start can find out why.
//
// It could not do that. It read the live container and nothing else, so once
// the container was gone — precisely the situation a failed start leaves
// behind — it answered with the Docker daemon's "No such container" as an HTTP
// 500 and rendered an empty pane.

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/state"
)

const logsContainerID = "d0ced0ced0ce"

// dockerLogFrame wraps payload in Docker's 8-byte stdout multiplex header.
func dockerLogFrame(payload string) []byte {
	frame := make([]byte, 8, 8+len(payload))
	frame[0] = 1 // stdout
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(payload)))
	return append(frame, payload...)
}

// logsDaemon speaks just enough of the Docker Engine API to serve — or refuse
// — one container's logs.
type logsDaemon struct {
	srv     *httptest.Server
	logs    string
	present bool
}

func newLogsDaemon(t *testing.T, logs string, present bool) *logsDaemon {
	t.Helper()
	d := &logsDaemon{logs: logs, present: present}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/containers/"+logsContainerID+"/logs") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !d.present {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"No such container: ` + logsContainerID + `"}`))
			return
		}
		_, _ = w.Write(dockerLogFrame(d.logs))
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func newLogsHandler(t *testing.T, d *logsDaemon) *Handler {
	t.Helper()
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	clk := clock.NewMock()
	clk.Set(time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC))
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clk)
	h := s.handler
	if d != nil {
		h.docker = docker.NewClient("tcp://"+d.srv.Listener.Addr().String(), zap.NewNop())
		h.dockerReady.Store(true)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.scheduler.Stop(ctx)
	})
	return h
}

func fetchLogs(t *testing.T, h *Handler, id string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("instanceId", id)
	req := httptest.NewRequest(http.MethodGet, "/_overcast/rds/instances/"+id+"/logs", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	h.GetInstanceLogs(rec, req)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode logs response (%d): %v: %s", rec.Code, err, rec.Body.String())
	}
	return rec, body
}

func putInstance(t *testing.T, h *Handler, inst *DBInstance) {
	t.Helper()
	if aerr := h.store.putDBInstance(context.Background(), inst); aerr != nil {
		t.Fatalf("putDBInstance: %s", aerr.Message)
	}
}

// A container Docker no longer has is a missing resource, not a server fault.
func TestGetInstanceLogs_missingContainerIsNotFoundNotServerError(t *testing.T) {
	d := newLogsDaemon(t, "", false)
	h := newLogsHandler(t, d)
	const id = "logs-gone"

	putInstance(t, h, &DBInstance{
		DBInstanceIdentifier: id,
		Engine:               "mysql",
		DBInstanceStatus:     "stopped",
		DockerContainerID:    logsContainerID,
	})

	rec, body := fetchLogs(t, h, id)
	if rec.Code == http.StatusInternalServerError {
		t.Fatalf("a container Docker has forgotten answered 500: %s", rec.Body.String())
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if body["status"] != "stopped" {
		t.Errorf("404 body does not carry the instance status: %v", body)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "no longer exists") {
		t.Errorf("404 message = %q, want it to say the container is gone", msg)
	}
}

// The whole point: a failed instance's tab explains the failure, with the
// engine's own last words, long after Docker has thrown the container away.
func TestGetInstanceLogs_explainsAFailedInstanceFromRetainedLogs(t *testing.T) {
	d := newLogsDaemon(t, "", false) // container gone
	h := newLogsHandler(t, d)
	const id = "logs-failed"
	const reason = "the database container exited with code 1 — see the instance logs for the engine's own output"
	const retained = "[ERROR] --initialize specified but the data directory has files in it. Aborting.\n"

	putInstance(t, h, &DBInstance{
		DBInstanceIdentifier: id,
		Engine:               "mysql",
		DBInstanceStatus:     "failed",
		StatusReason:         reason,
		DockerContainerID:    logsContainerID,
		LastLogs:             retained,
		LastLogsAt:           "2026-08-04T12:00:00Z",
	})

	rec, body := fetchLogs(t, h, id)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got, _ := body["logs"].(string); !strings.Contains(got, "data directory has files in it") {
		t.Errorf("logs = %q, want the retained tail", got)
	}
	if body["logSource"] != "retained" {
		t.Errorf("logSource = %v, want retained so the UI does not imply a live container", body["logSource"])
	}
	if body["status"] != "failed" {
		t.Errorf("status = %v, want failed", body["status"])
	}
	if body["statusReason"] != reason {
		t.Errorf("statusReason = %v, want the recorded reason", body["statusReason"])
	}
	if msg, _ := body["message"].(string); msg == "" {
		t.Error("no message explaining the situation")
	}
	if body["capturedAt"] != "2026-08-04T12:00:00Z" {
		t.Errorf("capturedAt = %v, want the capture time", body["capturedAt"])
	}
}

// A live container is still read live, and labelled as such.
func TestGetInstanceLogs_readsTheLiveContainerWhenThereIsOne(t *testing.T) {
	d := newLogsDaemon(t, "ready for connections\n", true)
	h := newLogsHandler(t, d)
	const id = "logs-live"

	putInstance(t, h, &DBInstance{
		DBInstanceIdentifier: id,
		Engine:               "mysql",
		DBInstanceStatus:     "available",
		DockerContainerID:    logsContainerID,
	})

	rec, body := fetchLogs(t, h, id)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got, _ := body["logs"].(string); !strings.Contains(got, "ready for connections") {
		t.Errorf("logs = %q, want the container's output", got)
	}
	if body["logSource"] != "container" {
		t.Errorf("logSource = %v, want container", body["logSource"])
	}
}

// Failing an instance is the last moment its container's output is readable.
// If it is not copied then, it is gone.
func TestFailInstance_retainsTheContainersLastLogs(t *testing.T) {
	const output = "[ERROR] InnoDB: Cannot allocate memory for the buffer pool\n"
	d := newLogsDaemon(t, output, true)
	h := newLogsHandler(t, d)
	ctx := context.Background()
	const id = "logs-capture"

	putInstance(t, h, &DBInstance{
		DBInstanceIdentifier: id,
		Engine:               "mysql",
		DBInstanceStatus:     "creating",
		DockerContainerID:    logsContainerID,
	})

	h.failInstance(ctx, id, "the database container exited with code 1")

	inst, aerr := h.store.getDBInstance(ctx, id)
	if aerr != nil {
		t.Fatalf("getDBInstance: %s", aerr.Message)
	}
	if !strings.Contains(inst.LastLogs, "Cannot allocate memory") {
		t.Fatalf("LastLogs = %q, want the container's output kept before it went away", inst.LastLogs)
	}
	if inst.LastLogsAt == "" {
		t.Error("LastLogsAt is empty, so nothing says how old the retained logs are")
	}

	// And the endpoint serves it once the container is gone.
	d.present = false
	rec, body := fetchLogs(t, h, id)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got, _ := body["logs"].(string); !strings.Contains(got, "Cannot allocate memory") {
		t.Errorf("logs = %q, want the retained output", got)
	}
}

// Retained logs outlive Docker itself. An instance whose container died before
// the daemon went away still has something to show, and telling that caller it
// is "metadata-only" would be false as well as useless.
func TestGetInstanceLogs_retainedLogsSurviveDockerBeingUnavailable(t *testing.T) {
	h := newLogsHandler(t, nil) // no Docker at all
	const id = "logs-no-docker"

	putInstance(t, h, &DBInstance{
		DBInstanceIdentifier: id,
		Engine:               "mysql",
		DBInstanceStatus:     "failed",
		StatusReason:         "the database container exited with code 1",
		DockerContainerID:    logsContainerID,
		LastLogs:             "[ERROR] InnoDB: Cannot allocate memory\n",
		LastLogsAt:           "2026-08-04T12:00:00Z",
	})

	rec, body := fetchLogs(t, h, id)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got, _ := body["logs"].(string); !strings.Contains(got, "Cannot allocate memory") {
		t.Errorf("logs = %q, want the retained tail", got)
	}
	if msg, _ := body["message"].(string); strings.Contains(msg, "metadata-only") {
		t.Errorf("message = %q, but this instance had a container and kept its logs", msg)
	}
}

// A metadata-only instance has no container and never did; say so rather than
// reporting a Docker failure.
func TestGetInstanceLogs_metadataOnlyInstanceIsExplained(t *testing.T) {
	h := newLogsHandler(t, nil)
	const id = "logs-metadata"

	putInstance(t, h, &DBInstance{
		DBInstanceIdentifier: id,
		Engine:               "postgres",
		DBInstanceStatus:     "available",
	})

	rec, body := fetchLogs(t, h, id)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "metadata-only") {
		t.Errorf("message = %q, want it to explain there is no container", msg)
	}
}

func TestGetInstanceLogs_unknownInstanceIsNotFound(t *testing.T) {
	h := newLogsHandler(t, nil)
	rec, body := fetchLogs(t, h, "no-such-instance")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if body["error"] != "instance not found" {
		t.Errorf("error = %v, want \"instance not found\"", body["error"])
	}
}
