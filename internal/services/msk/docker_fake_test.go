package msk

// docker_fake_test.go — a Docker daemon stand-in for the MSK tests whose
// subject only happens behind `dockerReady`.
//
// Since #686 a cluster is not promoted to ACTIVE unless Docker is wired: the
// promotion is done by the health check, and only the Docker path schedules
// one. Tests about that callback — region scoping in particular — therefore
// cannot reach their subject on a metadata-only handler. They were gated with
// docker.SkipWithoutDocker instead, which skips unconditionally (it dials an
// empty socket path), so they stopped running entirely.
//
// A real daemon is the wrong dependency for them: none is about containers, and
// requiring one makes the tests unrunnable wherever there is no socket. Point a
// real docker.Client at an httptest server speaking enough of the Engine API
// instead — the same harness elasticache/handler_docker_race_test.go,
// rds/handler_docker_race_test.go and lambda/seed_reconcile_test.go use.
//
// Mirrors the harness in ecs/docker_fake_test.go. The two are deliberately not
// shared: each answers the paths its own service drives, and a service test
// package importing another's fixtures would couple them for no gain — the
// same reason elasticache and rds each keep their own fakeDockerDaemon.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeMSKDockerDaemon answers the container-start path createCluster drives.
type fakeMSKDockerDaemon struct {
	srv *httptest.Server

	mu      sync.Mutex
	started []string
}

// startedCount reports how many containers have been started.
func (fd *fakeMSKDockerDaemon) startedCount() int {
	fd.mu.Lock()
	defer fd.mu.Unlock()
	return len(fd.started)
}

func newFakeMSKDockerDaemon(t *testing.T) *fakeMSKDockerDaemon {
	t.Helper()
	fd := &fakeMSKDockerDaemon{}
	var seq int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/images/create"), strings.Contains(p, "/images/"):
			w.WriteHeader(http.StatusOK)

		// GetContainerByName before create — no container of that name, so the
		// fresh-create branch runs rather than the adopt-existing one.
		case strings.Contains(p, "/containers/overcast-msk-") && strings.HasSuffix(p, "/json"):
			w.WriteHeader(http.StatusNotFound)

		case strings.HasSuffix(p, "/containers/create"):
			fd.mu.Lock()
			seq++
			id := fmt.Sprintf("mskfakecontainer%04d", seq)
			fd.mu.Unlock()
			w.Write([]byte(`{"Id":"` + id + `"}`)) //nolint:errcheck

		case strings.HasSuffix(p, "/start"):
			fd.mu.Lock()
			fd.started = append(fd.started, containerIDFromPath(p))
			fd.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)

		// Container inspect. Ports stays empty so the host port is the one
		// Overcast allocated, which is the address the health check dials.
		case strings.Contains(p, "/containers/") && strings.HasSuffix(p, "/json"):
			w.Write([]byte(`{"Id":"` + containerIDFromPath(p) + `",` + //nolint:errcheck
				`"State":{"Status":"running","Running":true},` +
				`"NetworkSettings":{"Networks":{},"Ports":{}}}`))

		case strings.HasSuffix(p, "/networks/create"):
			w.Write([]byte(`{"Id":"net-msk-fake"}`)) //nolint:errcheck

		case strings.Contains(p, "/networks/") && r.Method == http.MethodGet:
			w.Write([]byte(`{"Id":"net-msk-fake","Name":"bridge","Containers":{}}`)) //nolint:errcheck

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
