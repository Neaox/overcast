package lambda

// container_runtime_init_burst_test.go — the INIT burst is a property of one
// execution environment, not of the function.
//
// Containers start with burst CPU and are throttled to their memory's
// proportional allocation when their RIC issues its first GET /next. Two
// environments of the same function can be in INIT at the same time — two event
// source mappings on one stream delivering to one function is enough, and the
// CDK compat suite does it on every run — so which container a throttle-down
// belongs to has to be tracked per environment.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/docker"
)

// resourceUpdate is one POST /containers/{id}/update the daemon received.
type resourceUpdate struct {
	containerID string
	nanoCPUs    int64
}

// updatingDaemon is a fake Docker Engine that records container resource
// updates and accepts everything else.
type updatingDaemon struct {
	*httptest.Server

	mu      sync.Mutex
	updates []resourceUpdate
}

func newUpdatingDaemon(t *testing.T) *updatingDaemon {
	t.Helper()
	d := &updatingDaemon{}
	d.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/update") {
			var req docker.UpdateResourcesRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode update body: %v", err)
			}
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1.45/containers/"), "/update")
			d.mu.Lock()
			d.updates = append(d.updates, resourceUpdate{containerID: id, nanoCPUs: req.NanoCPUs})
			d.mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(d.Close)
	return d
}

func (d *updatingDaemon) recorded() []resourceUpdate {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]resourceUpdate(nil), d.updates...)
}

// newBurstContainerRuntime builds the smallest ContainerRuntime that can talk
// to daemon — the INIT-burst bookkeeping needs nothing else.
func newBurstContainerRuntime(daemon *updatingDaemon) *ContainerRuntime {
	return &ContainerRuntime{
		clk:    clock.New(),
		docker: docker.NewClient("tcp://"+daemon.Listener.Addr().String(), zap.NewNop()),
		logger: zap.NewNop(),
	}
}

// TestInitBurst_throttlesTheEnvironmentThatFinishedInit is #1336's part (1):
// two environments of one function cold starting at once shared a single
// registry slot keyed by the function's ARN, so the second registration
// displaced the first. When the environment that came up first reported INIT
// complete, the throttle-down landed on whichever container had registered
// last — cutting a container still in INIT from burst CPU to a fraction of a
// core, which is how a cold start that should take a second sat unfinished for
// 25 seconds. The environment that actually finished, meanwhile, kept burst CPU
// for the rest of its life.
func TestInitBurst_throttlesTheEnvironmentThatFinishedInit(t *testing.T) {
	// Given: two environments of the same 128 MB function, both in INIT.
	daemon := newUpdatingDaemon(t)
	cr := newBurstContainerRuntime(daemon)
	const memoryMB = 128
	cr.registerInitBurst("container-first", memoryMB)
	cr.registerInitBurst("container-second", memoryMB)

	// When: the first environment's RIC issues its first GET /next.
	cr.ThrottleInitBurst("container-first")

	// Then: that environment — and only it — is throttled to its steady-state
	// allocation. The one still in INIT keeps the burst it was started with.
	updates := daemon.recorded()
	if len(updates) != 1 {
		t.Fatalf("resource updates = %d, want 1: %+v", len(updates), updates)
	}
	if updates[0].containerID != "container-first" {
		t.Fatalf("throttled %q, want container-first — the container still in INIT was cut instead",
			updates[0].containerID)
	}
	if want := int64(cpuAllocation(memoryMB) * 1e9); updates[0].nanoCPUs != want {
		t.Fatalf("throttled to %d nanoCPUs, want %d", updates[0].nanoCPUs, want)
	}

	// And: the environment still in INIT is throttled by its own first
	// GET /next, not left holding burst CPU forever.
	cr.ThrottleInitBurst("container-second")
	updates = daemon.recorded()
	if len(updates) != 2 {
		t.Fatalf("resource updates = %d, want 2: %+v", len(updates), updates)
	}
	if updates[1].containerID != "container-second" {
		t.Fatalf("second throttle hit %q, want container-second", updates[1].containerID)
	}
}

// TestInitBurst_forgottenWhenTheEnvironmentDiesBeforeInitCompletes pins the
// other half of keying by container: an environment that never reaches its
// first GET /next never reports INIT complete, so its entry has to be dropped
// when the container is destroyed. Keyed by function ARN this was self-limiting
// — the next cold start of the same function overwrote it — and it no longer
// is.
func TestInitBurst_forgottenWhenTheEnvironmentDiesBeforeInitCompletes(t *testing.T) {
	// Given: an environment registered for throttle-down that is then
	// destroyed mid-INIT (what containerInstance.Close and the cold start's
	// own cleanup path do).
	daemon := newUpdatingDaemon(t)
	cr := newBurstContainerRuntime(daemon)
	cr.registerInitBurst("container-doomed", 128)
	cr.clearInitBurst("container-doomed")

	// Then: nothing is left waiting to throttle a container that is gone...
	cr.initBurstMu.Lock()
	pending := len(cr.initBurst)
	cr.initBurstMu.Unlock()
	if pending != 0 {
		t.Fatalf("pending burst entries = %d, want 0 — one is left per environment that dies in INIT", pending)
	}

	// ...and a late report for that container is a no-op rather than a resource
	// update aimed at a container the daemon no longer has.
	cr.ThrottleInitBurst("container-doomed")
	if got := daemon.recorded(); len(got) != 0 {
		t.Fatalf("resource updates = %d, want 0: %+v", len(got), got)
	}
}
