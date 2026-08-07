package elasticache

// Regression test: cache clusters are stored under region-scoped keys, but
// the creating → available transition and the deferred record delete run on
// scheduler callbacks outside any request context. Before the region-scoped
// scheduler API those callbacks resolved the DEFAULT region and clusters
// created elsewhere were stuck in "creating" forever.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/state"
	"github.com/Neaox/overcast/tests/helpers"
)

func TestCacheClusterLifecycle_nonDefaultRegion(t *testing.T) {
	helpers.SkipWithoutDocker(t)
	clk := clock.NewMock()
	svc := New(&config.Config{Region: "us-east-1", AccountID: "123456789012"}, state.NewMemoryStore(), zap.NewNop(), clk)
	h := svc.handler
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.scheduler.Stop(ctx)
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

	// Fire the 0-delay creating → available transition and wait for it: the
	// mock clock runs the callback on a goroutine of its own, so advancing the
	// clock alone leaves the read on the next line racing it.
	h.scheduler.AdvanceAndSettle(clk, time.Millisecond)
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
