package ecs

// docker_fake_test.go — a Docker daemon stand-in for the ECS tests whose
// subject only happens behind `dockerReady`.
//
// Since #686 a task is not transitioned to RUNNING unless Docker is wired, so
// tests about the transition itself — region scoping, ordering, deployment
// health — cannot reach their subject on a metadata-only handler. They were
// gated with docker.SkipWithoutDocker instead — a helper that skipped
// unconditionally, because it dialled an empty socket path — so they stopped
// running entirely. That helper has since been deleted; nothing needs a real
// daemon any more.
//
// A real daemon is the wrong dependency for them: none is about containers, and
// requiring one makes the tests unrunnable wherever there is no socket. Point a
// real docker.Client at an httptest server speaking enough of the Engine API
// instead — the same harness elasticache/handler_docker_race_test.go,
// rds/handler_docker_race_test.go and lambda/seed_reconcile_test.go use. It
// keeps the real client, the real HTTP encoding and the real request shapes,
// which a hand-written mock of a Docker interface would all stub out.

import (
	"context"
	"encoding/json"
	"fmt"
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

// fakeECSDockerDaemon answers the container-start path RunTask drives. It
// records the containers it was asked to start so a test can assert that a
// task which reports RUNNING has something behind it.
type fakeECSDockerDaemon struct {
	srv *httptest.Server

	mu      sync.Mutex
	started []string
	created []docker.CreateContainerRequest
	onStart func(string)

	// logs and removed model the one thing the retention paths turn on: a
	// container's output is readable right up to the moment it is removed, and
	// not one call afterwards. A daemon that always answers is no test of an
	// ordering bug.
	logs    map[string][]byte
	removed map[string]bool
}

// startedCount reports how many containers have been started.
func (fd *fakeECSDockerDaemon) startedCount() int {
	fd.mu.Lock()
	defer fd.mu.Unlock()
	return len(fd.started)
}

// createdEnvironments returns one environment snapshot per container create.
func (fd *fakeECSDockerDaemon) createdEnvironments() [][]string {
	fd.mu.Lock()
	defer fd.mu.Unlock()
	envs := make([][]string, len(fd.created))
	for i := range fd.created {
		envs[i] = append([]string(nil), fd.created[i].Env...)
	}
	return envs
}

func (fd *fakeECSDockerDaemon) setOnStart(fn func(string)) {
	fd.mu.Lock()
	defer fd.mu.Unlock()
	fd.onStart = fn
}

// setContainerLogs gives a container the bytes its logs endpoint serves, in
// whatever framing the test is about — multiplexed or the raw TTY stream.
func (fd *fakeECSDockerDaemon) setContainerLogs(containerID string, body []byte) {
	fd.mu.Lock()
	defer fd.mu.Unlock()
	fd.logs[containerID] = body
}

// wasRemoved reports whether the container has been deleted from the daemon,
// which is the point after which its logs are unrecoverable.
func (fd *fakeECSDockerDaemon) wasRemoved(containerID string) bool {
	fd.mu.Lock()
	defer fd.mu.Unlock()
	return fd.removed[containerID]
}

func (fd *fakeECSDockerDaemon) latestResourceID() string {
	fd.mu.Lock()
	defer fd.mu.Unlock()
	if len(fd.created) == 0 || fd.created[len(fd.created)-1].Labels == nil {
		return ""
	}
	return fd.created[len(fd.created)-1].Labels[docker.LabelResourceID]
}

func newFakeECSDockerDaemon(t *testing.T) *fakeECSDockerDaemon {
	t.Helper()
	fd := &fakeECSDockerDaemon{
		logs:    map[string][]byte{},
		removed: map[string]bool{},
	}
	var seq int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		// Image pull, and the inspect the puller does around it.
		case strings.HasSuffix(p, "/images/create"), strings.Contains(p, "/images/"):
			w.WriteHeader(http.StatusOK)

		// GetContainerByName lookup before create — no container of that name.
		case strings.Contains(p, "/containers/overcast-ecs-") && strings.HasSuffix(p, "/json"):
			w.WriteHeader(http.StatusNotFound)

		case strings.HasSuffix(p, "/containers/create"):
			var create docker.CreateContainerRequest
			if err := json.NewDecoder(r.Body).Decode(&create); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			fd.mu.Lock()
			seq++
			id := fmt.Sprintf("ecsfakecontainer%04d", seq)
			fd.created = append(fd.created, create)
			fd.mu.Unlock()
			w.Write([]byte(`{"Id":"` + id + `"}`)) //nolint:errcheck

		// Logs, until the container is removed. Docker answers a removed one
		// with a JSON 404, which the client turns into an error rather than
		// letting the body reach a caller that would read it as output.
		case strings.HasSuffix(p, "/logs"):
			id := containerIDFromPath(p)
			fd.mu.Lock()
			gone, body := fd.removed[id], fd.logs[id]
			fd.mu.Unlock()
			if gone {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"message":"No such container: ` + id + `"}`)) //nolint:errcheck
				return
			}
			w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
			w.Write(body) //nolint:errcheck

		case strings.HasSuffix(p, "/stop"):
			w.WriteHeader(http.StatusNoContent)

		case strings.HasSuffix(p, "/start"):
			fd.mu.Lock()
			containerID := containerIDFromPath(p)
			fd.started = append(fd.started, containerID)
			onStart := fd.onStart
			fd.mu.Unlock()
			if onStart != nil {
				onStart(containerID)
			}
			w.WriteHeader(http.StatusNoContent)

		case r.Method == http.MethodDelete && strings.Contains(p, "/containers/"):
			fd.mu.Lock()
			fd.removed[containerIDFromPath(p)] = true
			fd.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)

		// Container inspect, once one exists.
		case strings.Contains(p, "/containers/") && strings.HasSuffix(p, "/json"):
			w.Write([]byte(`{"Id":"` + containerIDFromPath(p) + `",` + //nolint:errcheck
				`"State":{"Status":"running","Running":true},` +
				`"NetworkSettings":{"Networks":{"bridge":{"IPAddress":"172.17.0.2"}},"Ports":{}}}`))

		case strings.HasSuffix(p, "/networks/create"):
			w.Write([]byte(`{"Id":"net-ecs-fake"}`)) //nolint:errcheck

		case strings.HasSuffix(p, "/networks/prune"):
			w.WriteHeader(http.StatusOK)

		// Network inspect — no containers attached, which is all the data
		// plane needs to decide to connect.
		case strings.Contains(p, "/networks/") && r.Method == http.MethodGet:
			w.Write([]byte(`{"Id":"net-ecs-fake","Name":"bridge","Containers":{}}`)) //nolint:errcheck

		// connect/disconnect, archive (CA bundle), and anything else the path
		// touches but does not read a body from.
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	fd.srv = srv
	t.Cleanup(srv.Close)
	return fd
}

// containerIDFromPath pulls the container ID out of /v1.43/containers/<id>/...
func containerIDFromPath(p string) string {
	const marker = "/containers/"
	i := strings.Index(p, marker)
	if i < 0 {
		return ""
	}
	rest := p[i+len(marker):]
	if j := strings.Index(rest, "/"); j >= 0 {
		return rest[:j]
	}
	return rest
}

// newECSDockerTestHandler is newECSRegionTestHandler with Docker wired to a
// fake daemon, for tests whose subject is gated on dockerReady.
//
// gc stays nil, as in the elasticache harness: nothing here exercises the
// reaper, and starting its loop would only add traffic to the fake.
func newECSDockerTestHandler(t *testing.T) (*Handler, *clock.Mock, *fakeECSDockerDaemon) {
	t.Helper()
	clk := clock.NewMock()
	svc := New(&config.Config{Region: "us-east-1", AccountID: "123456789012"}, state.NewMemoryStore(), zap.NewNop(), clk)
	h := svc.handler
	fd := wireFakeDocker(t, h)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.scheduler.Stop(ctx)
	})
	return h, clk, fd
}

// wireFakeDocker points an already-built handler at a fake daemon, for the
// tests that construct their own — a gated store, a real clock — and so cannot
// take newECSDockerTestHandler wholesale. It wires the same three fields
// Service.SetDocker does, minus the GC.
func wireFakeDocker(t *testing.T, h *Handler) *fakeECSDockerDaemon {
	t.Helper()
	fd := newFakeECSDockerDaemon(t)
	h.docker = docker.NewClient("tcp://"+fd.srv.Listener.Addr().String(), zap.NewNop())
	h.puller = docker.NewImagePuller(h.docker)
	h.dockerReady.Store(true)
	return fd
}
