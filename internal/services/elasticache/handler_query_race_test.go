package elasticache

// handler_query_race_test.go — the Query handlers place containers too.
//
// CreateCacheCluster exists twice, once per wire protocol, and each spawns its
// own background container start. The typed one learned to merge the container
// it started into a fresh read; the Query one went on persisting the snapshot
// it had taken before the start, which puts a cluster deleted in the meantime
// back to "creating" and leaves its container running with nothing left to
// reclaim it — the leak handler_docker_race_test.go covers on the typed path,
// reachable through the other protocol.
//
// A rule enforced on one wire path is a rule that lapses when a caller switches
// protocol, which is why this is tested on both.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// postQuery drives one Query-protocol handler with form values.
func postQuery(t *testing.T, h *Handler, fn http.HandlerFunc, params url.Values) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(params.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(testCtx())
	w := httptest.NewRecorder()
	fn(w, req)
	return w.Code
}

func TestDeleteCacheCluster_query_midStartDoesNotResurrectOrLeak(t *testing.T) {
	// Given: a cache cluster created over the Query protocol, whose container
	// is still starting — its ID is not on the record yet, so the delete path
	// has nothing to stop
	fd := newFakeDockerDaemon(t)
	h := newDockerTestHandler(t, fd)
	const id = "query-leak"

	if code := postQuery(t, h, h.CreateCacheCluster, url.Values{
		"CacheClusterId": []string{id},
		"Engine":         []string{"redis"},
		"CacheNodeType":  []string{"cache.t3.micro"},
	}); code != http.StatusOK {
		t.Fatalf("CreateCacheCluster: HTTP %d", code)
	}
	waitStarted(t, fd)

	// When: the cluster is deleted while the start is in flight, and the start
	// then completes
	if code := postQuery(t, h, h.DeleteCacheCluster, url.Values{
		"CacheClusterId": []string{id},
	}); code != http.StatusOK {
		t.Fatalf("DeleteCacheCluster: HTTP %d", code)
	}
	fd.release()
	h.dockerWg.Wait()

	// Then: the delete stands — the start goroutine neither writes the cluster
	// back to "creating" nor leaves its container behind
	if got, aerr := h.store.getCacheCluster(testCtx(), id); aerr == nil && got != nil &&
		got.CacheClusterStatus != "deleting" {
		t.Errorf("cluster resurrected by the container start: status = %q, want deleting (or gone)",
			got.CacheClusterStatus)
	}
	waitCondition(t, "container stop/remove after mid-start delete", fd.stoppedOrRemoved)
}
