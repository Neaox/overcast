package elasticache

// metadata_only_test.go — the three ElastiCache shapes when no container is
// ever going to start.
//
// Overcast runs without a Docker daemon for anyone who has not configured one,
// and on that path every resource here was left in "creating" for as long as
// the record existed. Nothing was coming to change it: "available" is the
// readiness watch's transition to make, and no watch is scheduled when no
// container was started. So `aws elasticache wait cache-cluster-available`
// spun until it gave up with nothing to say, and a CloudFormation stack that
// waited for the cache held open for its whole budget and then rolled back a
// cache that was already as ready as it would ever be.
//
// RDS and Lambda answer this the other way, and have since the create-path
// status work: a metadata-only resource is ready the moment it is recorded,
// because there is no runtime being claimed and nothing will arrive to prove.
// Claiming progress that can never be made is the same dishonesty as claiming
// a cache is available with nothing behind it — these tests are the first half
// of that, and readiness_test.go is the second.
//
// The region is deliberately not the default one: these transitions run on
// scheduler callbacks outside any request context, where a lookup that loses
// the region reads the wrong store key and silently does nothing.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/middleware"
	"github.com/overcast-sh/overcast/internal/state"
)

// newMetadataOnlyHandler builds a handler with no Docker wired at all, which is
// the whole subject: dockerReady stays false, so no create path starts a
// container and no readiness watch is ever scheduled.
func newMetadataOnlyHandler(t *testing.T) (*Handler, *clock.Mock, context.Context) {
	t.Helper()
	clk := clock.NewMock()
	svc := New(&config.Config{Region: "us-east-1", AccountID: "123456789012"},
		state.NewMemoryStore(), zap.NewNop(), clk)
	h := svc.handler
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.scheduler.Stop(ctx)
		h.dockerWg.Wait()
	})
	return h, clk, middleware.ContextWithRegion(context.Background(), "eu-west-1")
}

// settle runs the transitions the create path scheduled. The production clock
// is real, where the scheduler runs a zero-delay transition inline; a mock
// holds it until the clock moves, so the test moves it.
func settle(t *testing.T, h *Handler, clk *clock.Mock) {
	t.Helper()
	h.scheduler.AdvanceAndSettle(clk, time.Millisecond)
}

// createViaForm drives one of the form-encoded create handlers in a region.
// The package's other postForm helper carries no context, and the region is
// half of what these tests are checking.
func createViaForm(t *testing.T, ctx context.Context, fn http.HandlerFunc, params url.Values) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(params.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	fn(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create: HTTP %d: %s", w.Code, w.Body.String())
	}
}

func TestCacheCluster_withoutAContainerRuntime_becomesAvailable(t *testing.T) {
	h, clk, ctx := newMetadataOnlyHandler(t)
	const id = "metadata-cluster"

	createViaForm(t, ctx, h.CreateCacheCluster, url.Values{
		"CacheClusterId": []string{id},
		"Engine":         []string{"redis"},
	})
	settle(t, h, clk)

	got, aerr := h.store.getCacheCluster(ctx, id)
	if aerr != nil {
		t.Fatalf("getCacheCluster: %s", aerr.Message)
	}
	if got.CacheClusterStatus == "creating" {
		t.Fatal(`status is still "creating": no container was started, so no readiness ` +
			`watch exists to move it — the cluster is as ready as it will ever be and ` +
			"every waiter on it spins until it gives up")
	}
	if got.CacheClusterStatus != "available" {
		t.Errorf("status = %q, want %q", got.CacheClusterStatus, "available")
	}
}

// The typed path is a separate implementation of the same create, reached
// whenever the caller's SDK negotiates a wire protocol Overcast has a codec
// for. Fixing one and not the other would make the status depend on which SDK
// asked.
func TestCacheClusterTyped_withoutAContainerRuntime_becomesAvailable(t *testing.T) {
	h, clk, ctx := newMetadataOnlyHandler(t)
	const id = "metadata-cluster-typed"

	if _, aerr := h.createCacheClusterTyped(ctx, &ecCreateCacheClusterReq{
		CacheClusterId: id, Engine: "redis",
	}); aerr != nil {
		t.Fatalf("CreateCacheCluster: %s: %s", aerr.Code, aerr.Message)
	}
	settle(t, h, clk)

	got, aerr := h.store.getCacheCluster(ctx, id)
	if aerr != nil {
		t.Fatalf("getCacheCluster: %s", aerr.Message)
	}
	if got.CacheClusterStatus != "available" {
		t.Errorf("status = %q, want %q", got.CacheClusterStatus, "available")
	}
}

func TestReplicationGroup_withoutAContainerRuntime_becomesAvailable(t *testing.T) {
	h, clk, ctx := newMetadataOnlyHandler(t)
	const id = "metadata-rg"

	createViaForm(t, ctx, h.CreateReplicationGroup, url.Values{
		"ReplicationGroupId":          []string{id},
		"ReplicationGroupDescription": []string{"no runtime behind it"},
		"Engine":                      []string{"redis"},
	})
	settle(t, h, clk)

	got, aerr := h.store.getReplicationGroup(ctx, id)
	if aerr != nil {
		t.Fatalf("getReplicationGroup: %s", aerr.Message)
	}
	if got.Status != "available" {
		t.Errorf("status = %q, want %q", got.Status, "available")
	}
}

func TestReplicationGroupTyped_withoutAContainerRuntime_becomesAvailable(t *testing.T) {
	h, clk, ctx := newMetadataOnlyHandler(t)
	const id = "metadata-rg-typed"

	if _, aerr := h.createReplicationGroupTyped(ctx, &ecCreateReplicationGroupReq{
		ReplicationGroupId:          id,
		ReplicationGroupDescription: "no runtime behind it",
		Engine:                      "redis",
	}); aerr != nil {
		t.Fatalf("CreateReplicationGroup: %s: %s", aerr.Code, aerr.Message)
	}
	settle(t, h, clk)

	got, aerr := h.store.getReplicationGroup(ctx, id)
	if aerr != nil {
		t.Fatalf("getReplicationGroup: %s", aerr.Message)
	}
	if got.Status != "available" {
		t.Errorf("status = %q, want %q", got.Status, "available")
	}
}

func TestServerlessCache_withoutAContainerRuntime_becomesAvailable(t *testing.T) {
	h, clk, ctx := newMetadataOnlyHandler(t)
	const name = "metadata-serverless"

	createViaForm(t, ctx, h.CreateServerlessCache, url.Values{
		"ServerlessCacheName": []string{name},
		"Engine":              []string{"redis"},
	})
	settle(t, h, clk)

	got, aerr := h.store.getServerlessCache(ctx, name)
	if aerr != nil {
		t.Fatalf("getServerlessCache: %s", aerr.Message)
	}
	if got.Status != "available" {
		t.Errorf("status = %q, want %q", got.Status, "available")
	}
}

// The other half of the rule: where a container *is* being started, the create
// path must keep its hands off the status. Promoting there would make the
// readiness watch a no-op — it only ever moves a record out of creating — and
// that is exactly how a cache whose engine never came up used to report
// "available" forever.
func TestCacheCluster_withAContainerComing_staysCreating(t *testing.T) {
	fd := newFakeDockerDaemon(t)
	fd.release()

	h, _, ctx := newMetadataOnlyHandler(t)
	h.docker = docker.NewClient("tcp://"+fd.srv.Listener.Addr().String(), zap.NewNop())
	h.puller = docker.NewImagePuller(h.docker)
	h.dockerReady.Store(true)
	const id = "container-cluster"

	createViaForm(t, ctx, h.CreateCacheCluster, url.Values{
		"CacheClusterId": []string{id},
		"Engine":         []string{"redis"},
	})
	h.dockerWg.Wait()

	got, aerr := h.store.getCacheCluster(ctx, id)
	if aerr != nil {
		t.Fatalf("getCacheCluster: %s", aerr.Message)
	}
	if got.CacheClusterStatus != "creating" {
		t.Errorf("status = %q, want %q: nothing has confirmed the engine answers yet",
			got.CacheClusterStatus, "creating")
	}
}
