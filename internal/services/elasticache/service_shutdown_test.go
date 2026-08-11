package elasticache

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// service_shutdown_test.go — Stop has to be able to *end* a container start,
// not only wait for one.
//
// Every create path starts its container in a goroutine tracked on dockerWg,
// and Stop waits for that WaitGroup. Each goroutine built its context from
// context.Background(), so bgCancel — the field whose comment says it exists
// "so async goroutines and scheduler callbacks can abandon Docker/store work
// once shutdown begins" — reached none of them; and Stop cancelled it after
// the wait in any case, which is too late to shorten one. A start against a
// daemon that does not answer therefore held shutdown until the Docker
// transport's own ResponseHeaderTimeout expired — 30s, the only thing that
// ended it — out of a budget every service's Stop shares in turn
// (cmd/overcast/cmd_serve.go's cleanup passes one ctx to all of them).
//
// Both wire protocols spawn their own start goroutine, so both are covered
// here for the reason handler_query_race_test.go gives: a rule enforced on one
// wire path is a rule that lapses when a caller switches protocol.

func TestStop_endsAnInFlightContainerStart(t *testing.T) {
	const (
		clusterID = "shutdown-cluster"
		rgID      = "shutdown-rg"
		cacheName = "shutdown-serverless"
	)

	for _, tc := range []struct {
		name   string
		create func(t *testing.T, h *Handler)
	}{
		{"cache cluster, query", func(t *testing.T, h *Handler) {
			if code := postQuery(t, h, h.CreateCacheCluster, url.Values{
				"CacheClusterId": []string{clusterID},
				"Engine":         []string{"redis"},
				"CacheNodeType":  []string{"cache.t3.micro"},
			}); code != http.StatusOK {
				t.Fatalf("CreateCacheCluster: HTTP %d", code)
			}
		}},
		{"cache cluster, typed", func(t *testing.T, h *Handler) {
			if _, aerr := h.createCacheClusterTyped(testCtx(), &ecCreateCacheClusterReq{
				CacheClusterId: clusterID, Engine: "redis",
				CacheNodeType: "cache.t3.micro", NumCacheNodes: 1,
			}); aerr != nil {
				t.Fatalf("createCacheClusterTyped: %v", aerr)
			}
		}},
		{"replication group, query", func(t *testing.T, h *Handler) {
			if code := postQuery(t, h, h.CreateReplicationGroup, url.Values{
				"ReplicationGroupId":          []string{rgID},
				"ReplicationGroupDescription": []string{"shutdown test"},
				"Engine":                      []string{"redis"},
			}); code != http.StatusOK {
				t.Fatalf("CreateReplicationGroup: HTTP %d", code)
			}
		}},
		{"replication group, typed", func(t *testing.T, h *Handler) {
			if _, aerr := h.createReplicationGroupTyped(testCtx(), &ecCreateReplicationGroupReq{
				ReplicationGroupId: rgID, ReplicationGroupDescription: "shutdown test", Engine: "redis",
			}); aerr != nil {
				t.Fatalf("createReplicationGroupTyped: %v", aerr)
			}
		}},
		{"serverless cache, query", func(t *testing.T, h *Handler) {
			if code := postQuery(t, h, h.CreateServerlessCache, url.Values{
				"ServerlessCacheName": []string{cacheName},
				"Engine":              []string{"redis"},
			}); code != http.StatusOK {
				t.Fatalf("CreateServerlessCache: HTTP %d", code)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a container start in flight — the fake daemon holds the
			// first start request open until it is released.
			fd := newFakeDockerDaemon(t)
			svc, h := newDockerTestService(t, fd)
			// Cleanups run LIFO, so this one runs before the harness waits on
			// dockerWg: a failure here unwinds instead of hanging on it.
			t.Cleanup(fd.release)

			tc.create(t, h)
			waitStarted(t, fd)

			// When: the service is stopped. The context is deliberately
			// unbounded — a Stop that can only end the start by outliving its
			// own deadline is the bug, so a deadline here would pass either way.
			stopped := make(chan struct{})
			go func() {
				svc.Stop(context.Background())
				close(stopped)
			}()

			// Then: it returns, because it ended the start rather than waiting
			// on a daemon that never answers. A Stop that reaches the start
			// returns in milliseconds; one that cannot is freed only by the
			// Docker transport's 30s ResponseHeaderTimeout, so this threshold
			// sits well clear of both and races neither.
			select {
			case <-stopped:
			case <-time.After(5 * time.Second):
				t.Fatal("Stop did not return within 5s — it has no way to end an in-flight " +
					"container start, and is waiting out the Docker client's own timeout")
			}
		})
	}
}
