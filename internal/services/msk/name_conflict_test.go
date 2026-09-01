package msk

// name_conflict_test.go — the scope of the create-time name guards.
//
// AWS scopes an MSK cluster name, and a configuration name, to one account and
// one region: the same name in two regions is two resources. The guard has to
// agree, or a multi-region caller creating the same stack twice is refused the
// second one for a resource it cannot see.
//
// Overcast serves one account, so the account half of the scope is implicit;
// the region half is not, and is what this file pins.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/middleware"
	"github.com/overcast-sh/overcast/internal/state"
)

// newRegionHandler builds a Docker-less MSK handler. Without Docker there is no
// container to start, so a create is metadata-only — which is all these tests
// exercise.
func newRegionHandler(t *testing.T) *Handler {
	t.Helper()
	svc := New(&config.Config{Region: "us-east-1", AccountID: "123456789012"},
		state.NewMemoryStore(), zap.NewNop(), clock.NewMock())
	t.Cleanup(func() { svc.Stop(context.Background()) })
	return svc.handler
}

// postJSON drives one handler with a JSON body in a named region.
func postJSON(t *testing.T, h http.HandlerFunc, region, path string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.ContextWithRegion(context.Background(), region))
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

func TestCreateCluster_nameConflictIsRegionScoped(t *testing.T) {
	h := newRegionHandler(t)

	if w := postJSON(t, h.createCluster, "eu-west-1", "/v1/clusters",
		map[string]any{"clusterName": "same-name"}); w.Code != http.StatusOK {
		t.Fatalf("first createCluster: HTTP %d: %s", w.Code, w.Body.String())
	}

	// Same name, different region: a different resource, and not a conflict.
	if w := postJSON(t, h.createCluster, "us-west-2", "/v1/clusters",
		map[string]any{"clusterName": "same-name"}); w.Code != http.StatusOK {
		t.Fatalf("createCluster in a second region: HTTP %d: %s — the guard is not region-scoped",
			w.Code, w.Body.String())
	}

	// Same name, same region: a conflict.
	w := postJSON(t, h.createCluster, "eu-west-1", "/v1/clusters",
		map[string]any{"clusterName": "same-name"})
	if w.Code != http.StatusConflict {
		t.Fatalf("createCluster repeating a name in one region: HTTP %d: %s, want 409", w.Code, w.Body.String())
	}
}

func TestCreateConfiguration_nameConflictIsRegionScoped(t *testing.T) {
	h := newRegionHandler(t)

	if w := postJSON(t, h.createConfiguration, "eu-west-1", "/v1/configurations",
		map[string]any{"name": "same-config"}); w.Code != http.StatusCreated {
		t.Fatalf("first createConfiguration: HTTP %d: %s", w.Code, w.Body.String())
	}
	if w := postJSON(t, h.createConfiguration, "us-west-2", "/v1/configurations",
		map[string]any{"name": "same-config"}); w.Code != http.StatusCreated {
		t.Fatalf("createConfiguration in a second region: HTTP %d: %s — the guard is not region-scoped",
			w.Code, w.Body.String())
	}
	w := postJSON(t, h.createConfiguration, "eu-west-1", "/v1/configurations",
		map[string]any{"name": "same-config"})
	if w.Code != http.StatusConflict {
		t.Fatalf("createConfiguration repeating a name in one region: HTTP %d: %s, want 409", w.Code, w.Body.String())
	}
}

// TestCreateCluster_concurrentSameNameYieldsOne is the reason the guard holds a
// lock rather than only reading before it writes. The check and the store write
// are two steps against a shared store, and the two creates that matter here
// mint different ARNs — so the per-ARN record lock the rest of this package
// uses cannot serialise them, and nothing else would.
//
// The failure is a plain assertion rather than a data race, so it shows without
// -race: against a check that does not hold a lock, every attempt reads a free
// name and all of them are answered 200.
func TestCreateCluster_concurrentSameNameYieldsOne(t *testing.T) {
	h := newRegionHandler(t)

	const attempts = 8
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		codes  = make(map[int]int, 2)
		region = "eu-west-1"
	)
	start := make(chan struct{})
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			w := postJSON(t, h.createCluster, region, "/v1/clusters",
				map[string]any{"clusterName": "racing-name"})
			mu.Lock()
			codes[w.Code]++
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()

	if codes[http.StatusOK] != 1 {
		t.Fatalf("%d creates succeeded, want exactly 1 (all codes: %v)", codes[http.StatusOK], codes)
	}
	if codes[http.StatusConflict] != attempts-1 {
		t.Fatalf("%d creates conflicted, want %d (all codes: %v)", codes[http.StatusConflict], attempts-1, codes)
	}

	stored, aerr := h.store.listClusters(middleware.ContextWithRegion(context.Background(), region))
	if aerr != nil {
		t.Fatalf("listClusters: %s", aerr.Message)
	}
	if len(stored) != 1 {
		t.Fatalf("%d clusters stored, want 1", len(stored))
	}
}
