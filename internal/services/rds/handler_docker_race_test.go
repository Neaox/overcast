package rds

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/state"
)

// fakeDockerDaemon is an httptest server speaking just enough of the Docker
// Engine API for the RDS container-start path. The first container start
// request blocks until release() is called, so tests can interleave API calls
// with an in-flight background container start.
type fakeDockerDaemon struct {
	srv         *httptest.Server
	containerID string
	started     chan struct{} // closed when the first start request arrives
	release     func()        // unblocks the first start request (idempotent)

	// Inspect gate, off unless holdInspect is called: the health check
	// inspects the container between reading the instance record and dialling
	// the engine, so blocking the inspect holds that window open for a test.
	inspectMu       sync.Mutex
	inspectHeld     chan struct{} // closed when an inspect arrives while held
	inspectReleased chan struct{} // closed to let held inspects through
}

// holdInspect blocks the next container inspect until releaseInspect.
func (fd *fakeDockerDaemon) holdInspect() {
	fd.inspectMu.Lock()
	defer fd.inspectMu.Unlock()
	fd.inspectHeld = make(chan struct{})
	fd.inspectReleased = make(chan struct{})
}

// waitInspect blocks until a held inspect has arrived.
func (fd *fakeDockerDaemon) waitInspect(t *testing.T) {
	t.Helper()
	fd.inspectMu.Lock()
	held := fd.inspectHeld
	fd.inspectMu.Unlock()
	select {
	case <-held:
	case <-time.After(10 * time.Second):
		t.Fatal("container inspect never reached the fake Docker daemon")
	}
}

// releaseInspect lets the held inspect return.
func (fd *fakeDockerDaemon) releaseInspect() {
	fd.inspectMu.Lock()
	defer fd.inspectMu.Unlock()
	if fd.inspectReleased != nil {
		select {
		case <-fd.inspectReleased:
		default:
			close(fd.inspectReleased)
		}
	}
}

// gateInspect is called by the inspect route: it reports the arrival and waits
// for the release, or returns straight away when no gate is set.
func (fd *fakeDockerDaemon) gateInspect() {
	fd.inspectMu.Lock()
	held, released := fd.inspectHeld, fd.inspectReleased
	if held != nil {
		select {
		case <-held:
		default:
			close(held)
		}
		fd.inspectHeld = nil
	}
	fd.inspectMu.Unlock()
	if released != nil {
		<-released
	}
}

func newFakeDockerDaemon(t *testing.T) *fakeDockerDaemon {
	t.Helper()
	const containerID = "cafecafecafe"
	started := make(chan struct{})
	releaseCh := make(chan struct{})
	var startMu sync.Mutex
	startCount := 0

	// Built before the server so the routes can close over it.
	fd := &fakeDockerDaemon{containerID: containerID, started: started}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		// ImagePuller.Ensure → pull; best-effort prune afterwards.
		case strings.HasSuffix(p, "/images/create"), strings.HasSuffix(p, "/images/prune"):
			w.WriteHeader(http.StatusOK)

		// GetContainerByName lookup before create — no existing container.
		case strings.Contains(p, "/containers/overcast-rds-") && strings.HasSuffix(p, "/json"):
			w.WriteHeader(http.StatusNotFound)

		case strings.HasSuffix(p, "/networks/create"):
			w.Write([]byte(`{"Id":"net-1"}`)) //nolint:errcheck

		case strings.HasSuffix(p, "/connect"):
			w.WriteHeader(http.StatusOK)

		case strings.HasSuffix(p, "/containers/create"):
			w.Write([]byte(`{"Id":"` + containerID + `"}`)) //nolint:errcheck

		case strings.HasSuffix(p, "/containers/"+containerID+"/start"):
			startMu.Lock()
			startCount++
			first := startCount == 1
			startMu.Unlock()
			if first {
				close(started)
				<-releaseCh
			}
			w.WriteHeader(http.StatusNoContent)

		case strings.HasSuffix(p, "/containers/"+containerID+"/stop"):
			w.WriteHeader(http.StatusNoContent)

		// Inspect by ID (setContainerEndpoint when running inside Docker, and
		// the health check's container-state look).
		case strings.HasSuffix(p, "/containers/"+containerID+"/json"):
			fd.gateInspect()
			w.Write([]byte(`{"Id":"` + containerID + `",` + //nolint:errcheck
				`"State":{"Status":"running","Running":true},` +
				`"NetworkSettings":{"Networks":{"overcast_rds":{"IPAddress":"127.0.0.1"}},"Ports":{}}}`))

		// Container remove, network disconnect, etc. — accept silently.
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))

	var once sync.Once
	fd.srv = srv
	fd.release = func() { once.Do(func() { close(releaseCh) }) }
	// Cleanups run LIFO: srv.Close must run after release, or Close hangs on
	// the blocked start request.
	t.Cleanup(srv.Close)
	t.Cleanup(fd.release)
	return fd
}

// newDockerTestHandler wires a Handler against the fake daemon. gc stays nil —
// the code paths under test fall back to direct docker calls.
func newDockerTestHandler(t *testing.T, fd *fakeDockerDaemon) *Handler {
	t.Helper()
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())
	h := s.handler
	h.docker = docker.NewClient("tcp://"+fd.srv.Listener.Addr().String(), zap.NewNop())
	h.puller = docker.NewImagePuller(h.docker)
	h.dockerReady.Store(true)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.scheduler.Stop(ctx)
		h.dockerWg.Wait()
	})
	return h
}

func waitStarted(t *testing.T, fd *fakeDockerDaemon) {
	t.Helper()
	select {
	case <-fd.started:
	case <-time.After(10 * time.Second):
		t.Fatal("container start request never reached the fake Docker daemon")
	}
}

// TestCreateDBInstance_containerStartDoesNotClobberStop reproduces the
// cli/rds-instances/StartDBInstance compat flake: the background goroutine
// spawned by CreateDBInstance reads the instance record, spends real time
// starting the DB container, and must not write that stale snapshot back over
// a Stop transition that landed in the meantime — otherwise the subsequent
// StartDBInstance sees a status other than "stopped" and returns
// InvalidDBInstanceState.
func TestCreateDBInstance_containerStartDoesNotClobberStop(t *testing.T) {
	fd := newFakeDockerDaemon(t)
	h := newDockerTestHandler(t, fd)
	ctx := context.Background()
	const id = "race-stop"

	if _, aerr := h.createDBInstanceTyped(ctx, &createDBInstanceReq{
		DBInstanceIdentifier: id,
		Engine:               "mysql",
		MasterUsername:       "admin",
		MasterUserPassword:   "password123",
	}); aerr != nil {
		t.Fatalf("CreateDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	waitStarted(t, fd)

	// The instance is "available" (zero-delay transition runs inline); stop it
	// while the container start is still in flight.
	if _, aerr := h.stopDBInstanceTyped(ctx, &stopDBInstanceReq{DBInstanceIdentifier: id}); aerr != nil {
		t.Fatalf("StopDBInstance: %s: %s", aerr.Code, aerr.Message)
	}

	// Let the container start finish and its result land in the store.
	fd.release()
	h.dockerWg.Wait()

	inst, aerr := h.store.getDBInstance(ctx, id)
	if aerr != nil {
		t.Fatalf("getDBInstance after container start: %s", aerr.Message)
	}
	if inst.DBInstanceStatus != "stopped" {
		t.Fatalf("container-start goroutine clobbered the stop transition: status = %q, want %q",
			inst.DBInstanceStatus, "stopped")
	}
	if inst.DockerContainerID != fd.containerID {
		t.Fatalf("container identity lost in merge: DockerContainerID = %q, want %q",
			inst.DockerContainerID, fd.containerID)
	}

	// The compat suite's next step must succeed.
	if _, aerr := h.startDBInstanceTyped(ctx, &startDBInstanceReq{DBInstanceIdentifier: id}); aerr != nil {
		t.Fatalf("StartDBInstance after stop: %s: %s", aerr.Code, aerr.Message)
	}
}

// TestCreateDBInstance_containerStartDoesNotResurrectDeleted covers the same
// stale-snapshot write against DeleteDBInstance: when the instance is deleted
// while its container is still starting, the background goroutine must not
// write the old record back into the store.
func TestCreateDBInstance_containerStartDoesNotResurrectDeleted(t *testing.T) {
	fd := newFakeDockerDaemon(t)
	h := newDockerTestHandler(t, fd)
	ctx := context.Background()
	const id = "race-delete"

	if _, aerr := h.createDBInstanceTyped(ctx, &createDBInstanceReq{
		DBInstanceIdentifier: id,
		Engine:               "mysql",
		MasterUsername:       "admin",
		MasterUserPassword:   "password123",
	}); aerr != nil {
		t.Fatalf("CreateDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	waitStarted(t, fd)

	if _, aerr := h.deleteDBInstanceTyped(ctx, &deleteDBInstanceReq{DBInstanceIdentifier: id}); aerr != nil {
		t.Fatalf("DeleteDBInstance: %s: %s", aerr.Code, aerr.Message)
	}

	// Wait for the deferred record removal (50ms scheduler timer) to land.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, aerr := h.store.getDBInstance(ctx, id); aerr != nil {
			break // record gone
		}
		if time.Now().After(deadline) {
			t.Fatal("instance record was never removed after DeleteDBInstance")
		}
		time.Sleep(10 * time.Millisecond)
	}

	fd.release()
	h.dockerWg.Wait()

	if inst, aerr := h.store.getDBInstance(ctx, id); aerr == nil {
		t.Fatalf("deleted instance resurrected by container-start goroutine (status %q)",
			inst.DBInstanceStatus)
	}
}
