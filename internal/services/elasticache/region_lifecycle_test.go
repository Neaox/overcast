package elasticache

// Regression test: cache clusters are stored under region-scoped keys, but
// the creating → available transition and the deferred record delete run on
// scheduler callbacks outside any request context. Before the region-scoped
// scheduler API those callbacks resolved the DEFAULT region and clusters
// created elsewhere were stuck in "creating" forever.

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/state"
)

func TestCacheClusterLifecycle_nonDefaultRegion(t *testing.T) {
	// Docker is wired to the package's fake daemon rather than skipped for want
	// of a real one. The subject is the health check's region handling, and only
	// the Docker path schedules a health check: a metadata-only handler reaches
	// "available" by the create path's own transition (readiness.go), which
	// would pass this test without the health check having run at all.
	fd := newFakeDockerDaemon(t)
	fd.release() // nothing here is about the start-vs-delete race the fake can stage

	// Take the port before Overcast allocates it, and make it the base so the
	// allocation lands on the one already held.
	//
	// The order matters and the other way round is a flake: allocatePort only
	// scans its own store, so for a fresh store it returns portBase itself
	// without ever asking the OS whether that port is free. Listening on the
	// allocated port afterwards therefore fails with "address already in use"
	// wherever something else holds it.
	//
	// The stand-in answers `+PONG`, because since the readiness rework a dial
	// that merely connects no longer promotes anything: an accepting port is a
	// port proxy, and only an engine that answers PING is an engine. A bare
	// net.Listen here left the cluster in "creating" — correctly.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port for the cache node: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	cachePort := ln.Addr().(*net.TCPAddr).Port
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close() //nolint:errcheck // test double
				_, _ = bufio.NewReader(c).ReadString('\n')
				_, _ = c.Write([]byte("+PONG\r\n"))
			}(conn)
		}
	}()

	clk := clock.NewMock()
	svc := New(&config.Config{Region: "us-east-1", AccountID: "123456789012", ElastiCachePortBase: cachePort},
		state.NewMemoryStore(), zap.NewNop(), clk)
	h := svc.handler
	h.docker = docker.NewClient("tcp://"+fd.srv.Listener.Addr().String(), zap.NewNop())
	h.puller = docker.NewImagePuller(h.docker)
	h.dockerReady.Store(true)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.scheduler.Stop(ctx)
		h.dockerWg.Wait()
	})
	ctx := middleware.ContextWithRegion(context.Background(), "eu-west-1")
	const id = "eu-redis"

	post := func(fn http.HandlerFunc, params url.Values) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(params.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()
		fn(w, req)
		return w.Code
	}

	if code := post(h.CreateCacheCluster, url.Values{
		"CacheClusterId": []string{id},
		"Engine":         []string{"redis"},
		"CacheNodeType":  []string{"cache.t3.micro"},
	}); code != 200 {
		t.Fatalf("CreateCacheCluster: HTTP %d", code)
	}

	// Invisible under the default region.
	if got, _ := h.store.getCacheCluster(context.Background(), id); got != nil {
		t.Fatal("cluster unexpectedly visible under the default region")
	}

	// The container start runs on a background goroutine, and it is what
	// schedules the health check that promotes the cluster. Wait for it before
	// touching the clock, or there is no health check to fire yet.
	h.dockerWg.Wait()

	started, aerr := h.store.getCacheCluster(ctx, id)
	if aerr != nil || started == nil {
		t.Fatalf("getCacheCluster after start: %v", aerr)
	}
	// The health check promotes the cluster only once something answers PING on
	// the published port — a real protocol exchange, not a metadata flip — and
	// the listener standing in for the cache node is the one reserved above.
	if started.HostPort != cachePort {
		t.Fatalf("cluster published port %d, want the reserved %d: nothing is listening there",
			started.HostPort, cachePort)
	}

	// Fire the 1s health check and wait for it: the mock clock runs the callback
	// on a goroutine of its own, so advancing the clock alone leaves the read on
	// the next line racing it.
	h.scheduler.AdvanceAndSettle(clk, 2*time.Second)
	got, aerr := h.store.getCacheCluster(ctx, id)
	if aerr != nil {
		t.Fatalf("getCacheCluster: %s", aerr.Message)
	}
	if got.CacheClusterStatus != "available" {
		t.Fatalf("after create: status = %q, want %q (creating → available no-oped)", got.CacheClusterStatus, "available")
	}

	if code := post(h.DeleteCacheCluster, url.Values{"CacheClusterId": []string{id}}); code != 200 {
		t.Fatalf("DeleteCacheCluster: HTTP %d", code)
	}
	h.scheduler.AdvanceAndSettle(clk, time.Second)
	if got, _ := h.store.getCacheCluster(ctx, id); got != nil {
		t.Fatalf("after delete: record still present with status %q (deferred delete no-oped)", got.CacheClusterStatus)
	}
}
