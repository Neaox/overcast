package rds

import (
	"context"
	"encoding/json"
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

// handler_lifecycle_test.go — stop/start of a DB instance across the container
// boundary.
//
// RDS containers were created with HostConfig.AutoRemove, which tells Docker to
// delete the container the moment it exits. StopDBInstance stops the container
// deliberately and keeps its ID, so AutoRemove deleted the very container
// StartDBInstance was going to restart: a stopped instance could never be
// started again, and the emulator-only logs endpoint answered a stopped
// instance with the daemon's "No such container" error.

const lifecycleContainerID = "cafecafecafe"

// lifecycleDaemon is an httptest server speaking enough of the Docker Engine
// API for the RDS container lifecycle, and — crucially — modelling AutoRemove
// the way the real daemon does: a container created with it is gone once it
// stops, and every later request for it is a 404.
type lifecycleDaemon struct {
	srv *httptest.Server

	mu         sync.Mutex
	autoRemove bool // what the create request asked for
	exists     bool // whether the daemon still has the container
	running    bool
	starts     int // successful start requests
}

func newLifecycleDaemon(t *testing.T) *lifecycleDaemon {
	t.Helper()
	d := &lifecycleDaemon{}

	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		const cid = lifecycleContainerID

		// Once the daemon has discarded the container, everything addressed at
		// it answers 404 with the body Docker actually sends.
		if strings.Contains(p, "/containers/"+cid) {
			d.mu.Lock()
			gone := !d.exists
			d.mu.Unlock()
			if gone {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"No such container: ` + cid + `"}`))
				return
			}
		}

		switch {
		case strings.HasSuffix(p, "/images/create"), strings.HasSuffix(p, "/images/prune"):
			w.WriteHeader(http.StatusOK)

		// GetContainerByName lookup before create — no existing container.
		case strings.Contains(p, "/containers/overcast-rds-") && strings.HasSuffix(p, "/json"):
			w.WriteHeader(http.StatusNotFound)

		case strings.HasSuffix(p, "/networks/create"):
			_, _ = w.Write([]byte(`{"Id":"net-1"}`))

		case strings.HasSuffix(p, "/connect"):
			w.WriteHeader(http.StatusOK)

		case strings.HasSuffix(p, "/containers/create"):
			var req docker.CreateContainerRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode create container request: %v", err)
			}
			d.mu.Lock()
			if req.HostConfig != nil {
				d.autoRemove = req.HostConfig.AutoRemove
			}
			d.exists = true
			d.mu.Unlock()
			_, _ = w.Write([]byte(`{"Id":"` + cid + `"}`))

		case strings.HasSuffix(p, "/containers/"+cid+"/start"):
			d.mu.Lock()
			if !d.running {
				d.starts++
			}
			d.running = true
			d.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)

		case strings.HasSuffix(p, "/containers/"+cid+"/stop"):
			d.mu.Lock()
			d.running = false
			// The behaviour under test: AutoRemove turns a stop into a delete.
			if d.autoRemove {
				d.exists = false
			}
			d.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)

		// Container remove (DELETE /containers/{id}).
		case r.Method == http.MethodDelete && strings.HasSuffix(p, "/containers/"+cid):
			d.mu.Lock()
			d.exists = false
			d.running = false
			d.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)

		// Inspect by ID (setContainerEndpoint when running inside Docker).
		case strings.HasSuffix(p, "/containers/"+cid+"/json"):
			_, _ = w.Write([]byte(`{"Id":"` + cid + `",` +
				`"State":{"Status":"running","Running":true},` +
				`"NetworkSettings":{"Networks":{"overcast_rds":{"IPAddress":"127.0.0.1"}},"Ports":{}}}`))

		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func (d *lifecycleDaemon) state() (autoRemove, exists bool, starts int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.autoRemove, d.exists, d.starts
}

// containerGone reports whether the daemon has discarded the container, polling
// for up to 10s so an async GC removal has a chance to land.
func (d *lifecycleDaemon) containerGone(within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		if _, exists, _ := d.state(); !exists {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func newLifecycleHandler(t *testing.T, d *lifecycleDaemon) *Handler {
	t.Helper()
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())
	h := s.handler
	h.docker = docker.NewClient("tcp://"+d.srv.Listener.Addr().String(), zap.NewNop())
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

// createRunningInstance creates a DB instance and waits for its container start
// to finish, so the test acts on a settled record.
func createRunningInstance(t *testing.T, h *Handler, id string) {
	t.Helper()
	if _, aerr := h.createDBInstanceTyped(context.Background(), &createDBInstanceReq{
		DBInstanceIdentifier: id,
		Engine:               "mysql",
		MasterUsername:       "admin",
		MasterUserPassword:   "password123",
	}); aerr != nil {
		t.Fatalf("CreateDBInstance: %s: %s", aerr.Code, aerr.Message)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		inst, aerr := h.store.getDBInstance(context.Background(), id)
		if aerr == nil && inst.DockerContainerID != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("container ID was never recorded on the instance")
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.dockerWg.Wait()
}

// A stopped instance must still have a container to start again. AWS's
// stop/start cycle preserves the instance; AutoRemove made the stop
// destructive, so StartDBInstance had nothing to restart and the instance was
// stranded in "stopped" for good.
func TestStopStartDBInstance_containerSurvivesTheStop(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()
	const id = "lifecycle-mysql"

	// Given: a running instance whose container was not created with AutoRemove.
	createRunningInstance(t, h, id)
	if autoRemove, _, _ := d.state(); autoRemove {
		t.Error("RDS container created with HostConfig.AutoRemove — Docker deletes it on stop, " +
			"so StopDBInstance destroys the container StartDBInstance needs")
	}

	// When: the instance is stopped and started again.
	if _, aerr := h.stopDBInstanceTyped(ctx, &stopDBInstanceReq{DBInstanceIdentifier: id}); aerr != nil {
		t.Fatalf("StopDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	if _, exists, _ := d.state(); !exists {
		t.Fatal("the stop removed the container — nothing is left for StartDBInstance to start")
	}
	if _, aerr := h.startDBInstanceTyped(ctx, &startDBInstanceReq{DBInstanceIdentifier: id}); aerr != nil {
		t.Fatalf("StartDBInstance: %s: %s", aerr.Code, aerr.Message)
	}

	// Then: the daemon really did restart the same container.
	_, exists, starts := d.state()
	if !exists {
		t.Fatal("container no longer exists after the stop/start cycle")
	}
	if starts < 2 {
		t.Errorf("container was started %d time(s); want a second start from StartDBInstance", starts)
	}
}

// Dropping AutoRemove puts container removal entirely on the delete path, so
// prove the delete path still removes it — otherwise the fix trades a stranded
// instance for a leaked container.
func TestDeleteDBInstance_removesItsContainer(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)

	gcCtx, cancelGC := context.WithCancel(context.Background())
	t.Cleanup(cancelGC)
	h.gc = docker.NewGC(h.docker, zap.NewNop(), false)
	h.gc.StartRemoveLoop(gcCtx)

	const id = "lifecycle-delete"
	createRunningInstance(t, h, id)

	if _, aerr := h.deleteDBInstanceTyped(context.Background(), &deleteDBInstanceReq{DBInstanceIdentifier: id}); aerr != nil {
		t.Fatalf("DeleteDBInstance: %s: %s", aerr.Code, aerr.Message)
	}

	if !d.containerGone(10 * time.Second) {
		t.Fatal("DeleteDBInstance left the container behind")
	}
}
