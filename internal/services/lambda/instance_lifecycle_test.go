package lambda

// instance_lifecycle_test.go — execution environments must be retired when a
// function's code or configuration changes, not left warm until the 15-minute
// idle TTL expires.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// ─── functionInstanceIdentity ────────────────────────────────────────────────

func TestFunctionInstanceIdentity_changesWithContainerAffectingConfig(t *testing.T) {
	// Given: a function with one environment variable.
	base := &Function{
		Name:        "fn",
		Runtime:     "nodejs22.x",
		Handler:     "index.handler",
		Timeout:     3,
		MemorySize:  128,
		Environment: map[string]string{"STAGE": "dev"},
	}
	baseID := functionInstanceIdentity(base)

	// When/Then: every field that is baked into the container at creation time
	// produces a different identity.
	cases := []struct {
		name  string
		apply func(fn *Function)
	}{
		{"environment value", func(fn *Function) { fn.Environment = map[string]string{"STAGE": "prod"} }},
		{"environment key", func(fn *Function) { fn.Environment = map[string]string{"STAGE2": "dev"} }},
		{"environment added", func(fn *Function) { fn.Environment = map[string]string{"STAGE": "dev", "DEBUG": "1"} }},
		{"environment cleared", func(fn *Function) { fn.Environment = nil }},
		{"memory size", func(fn *Function) { fn.MemorySize = 512 }},
		{"timeout", func(fn *Function) { fn.Timeout = 30 }},
		{"handler", func(fn *Function) { fn.Handler = "index.other" }},
		{"runtime", func(fn *Function) { fn.Runtime = "nodejs20.x" }},
		{"code", func(fn *Function) { fn.CodeZip = []byte("new-zip") }},
		{"layers", func(fn *Function) {
			fn.Layers = []LayerVersionLink{{ARN: "arn:aws:lambda:us-east-1:000000000000:layer:l:1"}}
		}},
		{"vpc config", func(fn *Function) { fn.VpcConfig = &VpcConfig{VpcId: "vpc-1", SubnetIds: []string{"subnet-1"}} }},
		{"image config", func(fn *Function) { fn.ImageConfig = &ImageConfig{Command: []string{"app.handler"}} }},
		{"architectures", func(fn *Function) { fn.Architectures = []string{"arm64"} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mutated := *base
			tc.apply(&mutated)
			if got := functionInstanceIdentity(&mutated); got == baseID {
				t.Fatalf("identity unchanged after %s change — warm instance would be reused with stale config", tc.name)
			}
		})
	}
}

func TestFunctionInstanceIdentity_stableForNonContainerFields(t *testing.T) {
	// Given: a function.
	base := &Function{
		Name:        "fn",
		Runtime:     "nodejs22.x",
		Handler:     "index.handler",
		Timeout:     3,
		MemorySize:  128,
		Environment: map[string]string{"STAGE": "dev"},
	}
	baseID := functionInstanceIdentity(base)

	// When: only fields that never reach the container change.
	mutated := *base
	mutated.Description = "now with a description"
	mutated.Role = "arn:aws:iam::000000000000:role/other"
	mutated.RevisionId = "a-new-revision"
	mutated.LastModified = "2026-07-27T00:00:00Z"

	// Then: the identity is unchanged, so the warm instance survives.
	if got := functionInstanceIdentity(&mutated); got != baseID {
		t.Fatalf("identity changed for a non-container field — would force a pointless cold start")
	}
}

func TestFunctionInstanceIdentity_environmentOrderIndependent(t *testing.T) {
	// Given: two functions with the same environment written in different order.
	a := &Function{Name: "fn", Environment: map[string]string{"A": "1", "B": "2"}}
	b := &Function{Name: "fn", Environment: map[string]string{"B": "2", "A": "1"}}

	// Then: identities match — Go map iteration order must not leak in.
	if functionInstanceIdentity(a) != functionInstanceIdentity(b) {
		t.Fatal("identity depends on map iteration order")
	}
}

func TestFunctionInstanceIdentity_environmentDelimiterInjection(t *testing.T) {
	// Given: environment variables whose values could forge the encoding of a
	// neighbouring pair if the fingerprint were naively concatenated.
	a := &Function{Name: "fn", Environment: map[string]string{"A": "1", "AB": "2"}}
	b := &Function{Name: "fn", Environment: map[string]string{"A": "1\nenv:AB=2", "AB": ""}}

	// Then: the two configurations are distinguishable.
	if functionInstanceIdentity(a) == functionInstanceIdentity(b) {
		t.Fatal("distinct environments collide — fingerprint encoding is ambiguous")
	}
}

// ─── InstancePool ────────────────────────────────────────────────────────────

func TestInstancePoolInvalidateFunction_closesIdleInstance(t *testing.T) {
	// Given: a pool holding an idle instance for a function.
	pool := NewInstancePool(poolTestRuntime{}, zap.NewNop(), clock.NewMock(), PoolLimits{})
	defer pool.Stop()
	inst := newPoolTestInstance("fn")
	pool.Release(context.Background(), inst, true)

	// When: the function's configuration changes.
	retired := pool.InvalidateFunction(&Function{Name: "fn", Environment: map[string]string{"NEW": "1"}})

	// Then: the idle container is destroyed immediately, not left until the
	// 15-minute idle sweep.
	if retired != 1 {
		t.Fatalf("InvalidateFunction retired %d instances, want 1", retired)
	}
	if inst.CloseCalls() != 1 {
		t.Fatalf("close calls = %d, want 1", inst.CloseCalls())
	}
	pool.mu.Lock()
	entries := len(pool.entries)
	pool.mu.Unlock()
	if entries != 0 {
		t.Fatalf("pooled entries = %d, want 0", entries)
	}
}

func TestInstancePoolInvalidateFunction_keepsIdleInstanceWhenIdentityUnchanged(t *testing.T) {
	// Given: a pool holding an idle instance.
	pool := NewInstancePool(poolTestRuntime{}, zap.NewNop(), clock.NewMock(), PoolLimits{})
	defer pool.Stop()
	inst := newPoolTestInstance("fn")
	pool.Release(context.Background(), inst, true)

	// When: an update lands that does not change anything the container sees.
	unchanged := &Function{Name: "fn"}
	inst.configIdentity = functionInstanceIdentity(unchanged)
	pool.mu.Lock()
	pool.entries["fn"][0].identity = inst.configIdentity
	pool.mu.Unlock()
	retired := pool.InvalidateFunction(unchanged)

	// Then: the warm instance is kept — no pointless cold start.
	if retired != 0 {
		t.Fatalf("InvalidateFunction retired %d instances whose configuration is unchanged", retired)
	}
	if inst.CloseCalls() != 0 {
		t.Fatalf("close calls = %d, want 0", inst.CloseCalls())
	}
}

func TestInstancePoolRelease_retiresInstanceInvalidatedWhileRunning(t *testing.T) {
	// Given: an instance checked out of the pool for an in-flight invocation.
	pool := NewInstancePool(poolTestRuntime{}, zap.NewNop(), clock.NewMock(), PoolLimits{})
	defer pool.Stop()
	inst := newPoolTestInstance("fn")
	fn := &Function{Name: "fn", Environment: map[string]string{"STAGE": "dev"}}
	inst.configIdentity = functionInstanceIdentity(fn)
	pool.Release(context.Background(), inst, true)
	if _, err := pool.Acquire(context.Background(), fn); err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// When: the configuration changes mid-invocation, then the invocation ends.
	pool.InvalidateFunction(&Function{Name: "fn", Environment: map[string]string{"STAGE": "prod"}})
	if inst.CloseCalls() != 0 {
		t.Fatal("in-flight instance was closed before its invocation completed")
	}
	pool.Release(context.Background(), inst, true)

	// Then: the instance is destroyed on release rather than returned to the
	// warm pool with stale configuration.
	if inst.CloseCalls() != 1 {
		t.Fatalf("close calls = %d, want 1", inst.CloseCalls())
	}
	pool.mu.Lock()
	entries := len(pool.entries)
	pool.mu.Unlock()
	if entries != 0 {
		t.Fatalf("pooled entries = %d, want 0 — stale instance was re-pooled", entries)
	}
}

func TestInstancePoolAcquire_evictsInstanceWithStaleEnvironment(t *testing.T) {
	// Given: a pooled instance built with the old environment.
	pool := NewInstancePool(poolTestRuntime{}, zap.NewNop(), clock.NewMock(), PoolLimits{})
	defer pool.Stop()
	oldFn := &Function{Name: "fn", Environment: map[string]string{"STAGE": "dev"}}
	inst := newPoolTestInstance("fn")
	inst.configIdentity = functionInstanceIdentity(oldFn)
	pool.Release(context.Background(), inst, true)

	// When: the function is acquired after its environment changed.
	newFn := &Function{Name: "fn", Environment: map[string]string{"STAGE": "prod"}}
	if _, err := pool.Acquire(context.Background(), newFn); err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// Then: the stale instance is closed and a cold start happens instead.
	if inst.CloseCalls() != 1 {
		t.Fatalf("close calls = %d, want 1", inst.CloseCalls())
	}
}

func TestInstancePoolInvalidateFunction_doesNotStartReplacement(t *testing.T) {
	// Given: a pool over a runtime that records cold starts, holding an idle
	// instance.
	rt := &countingColdStartRuntime{}
	pool := NewInstancePool(rt, zap.NewNop(), clock.NewMock(), PoolLimits{})
	defer pool.Stop()
	pool.Release(context.Background(), newPoolTestInstance("fn"), true)

	// When: the configuration changes.
	pool.InvalidateFunction(&Function{Name: "fn", Environment: map[string]string{"NEW": "1"}})

	// Then: no replacement container is started — the next invocation cold
	// starts one. Only a provisioned concurrency reservation re-warms eagerly.
	if got := rt.count(); got != 0 {
		t.Fatalf("cold starts after invalidate = %d, want 0", got)
	}
}

func TestInstancePoolHandleContainerDied_evictsPooledInstance(t *testing.T) {
	// Given: a pool holding an idle instance whose container is then destroyed
	// out from under it (`docker rm -f`, an OOM kill, a Docker Desktop restart).
	pool := NewInstancePool(poolTestRuntime{}, zap.NewNop(), clock.NewMock(), PoolLimits{})
	defer pool.Stop()
	var lost []string
	pool.observer = observerFunc(func(name string) { lost = append(lost, name) })
	inst := newPoolTestInstance("fn")
	inst.containerID = "c0ffee1234567890"
	pool.Release(context.Background(), inst, true)

	// When: the Docker watcher reports the container died.
	pool.handleContainerDied(context.Background(), events.Event{
		Type: events.DockerContainerDied,
		Payload: events.DockerContainerPayload{
			ContainerID: "c0ffee1234567890",
			Action:      "die",
			Service:     "lambda",
		},
	})

	// Then: the dead instance is dropped from the pool, so the next invocation
	// cold starts instead of submitting to a container that will never answer.
	pool.mu.Lock()
	entries := len(pool.entries)
	pool.mu.Unlock()
	if entries != 0 {
		t.Fatalf("pooled entries = %d, want 0 — dead container still counts as warm", entries)
	}
	if inst.CloseCalls() != 1 {
		t.Fatalf("close calls = %d, want 1", inst.CloseCalls())
	}
	if len(lost) != 1 || lost[0] != "fn" {
		t.Fatalf("onInstanceLost calls = %v, want [fn] — map would keep showing a dead instance", lost)
	}
}

func TestInstancePoolHandleContainerDied_ignoresOtherContainers(t *testing.T) {
	// Given: a pool holding an idle instance.
	pool := NewInstancePool(poolTestRuntime{}, zap.NewNop(), clock.NewMock(), PoolLimits{})
	defer pool.Stop()
	inst := newPoolTestInstance("fn")
	inst.containerID = "c0ffee1234567890"
	pool.Release(context.Background(), inst, true)

	// When: an unrelated container dies — another service's, or another
	// function's.
	for _, payload := range []events.DockerContainerPayload{
		{ContainerID: "deadbeef00000000", Service: "lambda", Action: "die"},
		{ContainerID: "c0ffee1234567890", Service: "rds", Action: "die"},
	} {
		pool.handleContainerDied(context.Background(), events.Event{
			Type:    events.DockerContainerDied,
			Payload: payload,
		})
	}

	// Then: the warm instance is untouched.
	if inst.CloseCalls() != 0 {
		t.Fatalf("close calls = %d, want 0", inst.CloseCalls())
	}
	pool.mu.Lock()
	entries := len(pool.entries)
	pool.mu.Unlock()
	if entries != 1 {
		t.Fatalf("pooled entries = %d, want 1", entries)
	}
}

func TestInstancePoolAcquire_coldStartsAfterPooledContainerDies(t *testing.T) {
	// Given: a pooled instance whose container has died.
	rt := &countingColdStartRuntime{}
	pool := NewInstancePool(rt, zap.NewNop(), clock.NewMock(), PoolLimits{})
	defer pool.Stop()
	fn := &Function{Name: "fn", Environment: map[string]string{"STAGE": "dev"}}
	inst := newPoolTestInstance("fn")
	inst.containerID = "c0ffee1234567890"
	inst.configIdentity = functionInstanceIdentity(fn)
	pool.Release(context.Background(), inst, true)
	pool.handleContainerDied(context.Background(), events.Event{
		Payload: events.DockerContainerPayload{ContainerID: "c0ffee1234567890", Service: "lambda", Action: "die"},
	})

	// When: the function is invoked.
	if _, err := pool.Acquire(context.Background(), fn); err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// Then: it cold starts rather than handing back the dead instance, which
	// would block the invocation until the function's timeout expired.
	if got := rt.count(); got != 1 {
		t.Fatalf("cold starts = %d, want 1", got)
	}
}

// ─── instanceTracker ─────────────────────────────────────────────────────────

func TestInstanceTrackerInvalidate_evictsIdleInstance(t *testing.T) {
	// Given: a tracker with an idle instance.
	tracker := newInstanceTracker(clock.NewMock(), zap.NewNop())
	t.Cleanup(tracker.Stop)
	trackedFor(tracker, "fn", nil).Finish(true, "")

	// When: the function's configuration changes.
	tracker.Invalidate("fn")

	// Then: the instance disappears from the UI snapshot immediately.
	if got := len(tracker.Instances()); got != 0 {
		t.Fatalf("tracked instances = %d, want 0", got)
	}
}

func TestInstanceTrackerInvalidate_evictsRunningInstanceOnRelease(t *testing.T) {
	// Given: a tracker with an instance mid-invocation.
	tracker := newInstanceTracker(clock.NewMock(), zap.NewNop())
	t.Cleanup(tracker.Stop)
	inv := trackedFor(tracker, "fn", nil)
	inv.Ready()
	inv.Running()

	// When: the configuration changes while the invocation is still running.
	tracker.Invalidate("fn")

	// Then: the instance is still reported until the invocation finishes...
	snap := tracker.Instances()
	if len(snap) != 1 {
		t.Fatalf("tracked instances during invocation = %d, want 1", len(snap))
	}
	if snap[0].Status != instanceStatusRunning {
		t.Fatalf("status = %q, want %q", snap[0].Status, instanceStatusRunning)
	}

	// ...and is evicted when it completes rather than going idle.
	inv.Finish(true, "")
	if got := len(tracker.Instances()); got != 0 {
		t.Fatalf("tracked instances after release = %d, want 0", got)
	}
}

func TestInstanceTrackerInvalidate_nextAcquireStartsFreshInstance(t *testing.T) {
	// Given: an instance that was retired by a configuration change.
	tracker := newInstanceTracker(clock.NewMock(), zap.NewNop())
	t.Cleanup(tracker.Stop)
	firstInv := trackedFor(tracker, "fn", nil)
	firstInv.Finish(true, "")
	firstID := tracker.Instances()[0].InstanceID
	tracker.Invalidate("fn")

	// When: the function is invoked again.
	tracker.Begin("fn", nil)

	// Then: it is reported as a new instance in a cold start.
	if got := tracker.Instances()[0].InstanceID; got == firstID {
		t.Fatal("instance ID reused after the environment was retired")
	}
	snap := tracker.Instances()
	if len(snap) != 1 || snap[0].Status != instanceStatusStarting {
		t.Fatalf("snapshot after re-acquire = %+v, want one starting instance", snap)
	}
}

// ─── UpdateFunctionConfiguration / UpdateFunctionCode ────────────────────────

func TestUpdateFunctionConfiguration_retiresWarmInstance(t *testing.T) {
	// Given: a function with a warm, idle instance holding the old environment.
	h, pool := lifecycleTestHandler(t)
	fn := seedLifecycleFunction(t, h, map[string]string{"STAGE": "dev"})
	inst := newPoolTestInstance(fn.Name)
	inst.configIdentity = functionInstanceIdentity(fn)
	pool.Release(context.Background(), inst, true)
	trackedFor(h.tracker, fn.Name, nil).Finish(true, "")

	// When: the environment is changed through the API the web UI uses.
	rec := updateFunctionConfiguration(t, h, fn.Name, map[string]any{
		"Environment": map[string]any{"Variables": map[string]string{"STAGE": "prod"}},
	})

	// Then: the request succeeds and the idle container is destroyed straight
	// away instead of surviving until the 15-minute idle TTL.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if inst.CloseCalls() != 1 {
		t.Fatalf("close calls = %d, want 1 — stale container left running", inst.CloseCalls())
	}
	if got := len(h.tracker.Instances()); got != 0 {
		t.Fatalf("tracked instances = %d, want 0", got)
	}
}

func TestUpdateFunctionConfiguration_keepsWarmInstanceWhenContainerUnaffected(t *testing.T) {
	// Given: a function with a warm, idle instance.
	h, pool := lifecycleTestHandler(t)
	fn := seedLifecycleFunction(t, h, map[string]string{"STAGE": "dev"})
	inst := newPoolTestInstance(fn.Name)
	inst.configIdentity = functionInstanceIdentity(fn)
	pool.Release(context.Background(), inst, true)

	// When: only the description changes.
	rec := updateFunctionConfiguration(t, h, fn.Name, map[string]any{"Description": "just a note"})

	// Then: the warm instance is kept — no gratuitous cold start.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if inst.CloseCalls() != 0 {
		t.Fatalf("close calls = %d, want 0", inst.CloseCalls())
	}
}

func TestUpdateFunctionCode_retiresWarmInstance(t *testing.T) {
	// Given: a function with a warm, idle instance.
	h, pool := lifecycleTestHandler(t)
	fn := seedLifecycleFunction(t, h, nil)
	inst := newPoolTestInstance(fn.Name)
	inst.configIdentity = functionInstanceIdentity(fn)
	pool.Release(context.Background(), inst, true)

	// When: new code is uploaded.
	body, _ := json.Marshal(map[string]any{"ZipFile": []byte("brand-new-zip")})
	req := httptest.NewRequest(http.MethodPut, "/2015-03-31/functions/"+fn.Name+"/code", bytes.NewReader(body))
	req = withFunctionNameParam(req, fn.Name)
	rec := httptest.NewRecorder()
	h.UpdateFunctionCode(rec, req)

	// Then: the container running the old code is destroyed immediately.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if inst.CloseCalls() != 1 {
		t.Fatalf("close calls = %d, want 1 — container with old code left running", inst.CloseCalls())
	}
}

func TestS3SyncWatcher_retiresWarmInstanceOnCodeRefresh(t *testing.T) {
	// Given: a function whose code lives in S3, with a warm idle instance.
	h, pool := lifecycleTestHandler(t)
	fn := seedLifecycleFunction(t, h, nil)
	fn.CodeS3Bucket = "assets"
	fn.CodeS3Key = "fn.zip"
	if aerr := h.ls.putFunction(context.Background(), fn); aerr != nil {
		t.Fatalf("seed function: %s", aerr.Message)
	}
	inst := newPoolTestInstance(fn.Name)
	inst.configIdentity = functionInstanceIdentity(fn)
	pool.Release(context.Background(), inst, true)

	watcher := newS3SyncWatcher(h.ls, testFetch([]byte("refreshed-zip")), zap.NewNop(), h.clk)
	watcher.retire = h.retireExecutionEnvironment

	// When: a new object lands on the function's code key.
	watcher.syncFunctionCode(context.Background(), fn)

	// Then: the container running the old code is destroyed immediately.
	if inst.CloseCalls() != 1 {
		t.Fatalf("close calls = %d, want 1 — container with old code left running", inst.CloseCalls())
	}
}

func TestDeleteFunction_dropsTrackedInstance(t *testing.T) {
	// Given: a deleted function that had a tracked instance.
	h, _ := lifecycleTestHandler(t)
	fn := seedLifecycleFunction(t, h, nil)
	trackedFor(h.tracker, fn.Name, nil).Finish(true, "")

	// When: the function is deleted.
	req := httptest.NewRequest(http.MethodDelete, "/2015-03-31/functions/"+fn.Name, nil)
	req = withFunctionNameParam(req, fn.Name)
	rec := httptest.NewRecorder()
	h.DeleteFunction(rec, req)

	// Then: the instance stops being reported instead of lingering for 15
	// minutes on a function that no longer exists.
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if got := len(h.tracker.Instances()); got != 0 {
		t.Fatalf("tracked instances = %d, want 0", got)
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func lifecycleTestHandler(t *testing.T) (*Handler, *InstancePool) {
	t.Helper()
	clk := clock.NewMock()
	ls := newLambdaStore(state.NewMemoryStore(), "us-east-1", clk)
	pool := NewInstancePool(poolTestRuntime{}, zap.NewNop(), clk, PoolLimits{})
	t.Cleanup(pool.Stop)
	tracker := newInstanceTracker(clk, zap.NewNop())
	t.Cleanup(tracker.Stop)
	h := &Handler{
		cfg:      &config.Config{Region: "us-east-1"},
		log:      serviceutil.NewServiceLogger(zap.NewNop(), "lambda"),
		clk:      clk,
		ls:       ls,
		tracker:  tracker,
		runtimes: newRuntimeRegistry([]Runtime{pool}),
	}
	return h, pool
}

func seedLifecycleFunction(t *testing.T, h *Handler, env map[string]string) *Function {
	t.Helper()
	fn := &Function{
		Name:        "lifecycle-fn",
		ARN:         "arn:aws:lambda:us-east-1:000000000000:function:lifecycle-fn",
		Runtime:     "nodejs22.x",
		Handler:     "index.handler",
		Role:        "arn:aws:iam::000000000000:role/lambda-role",
		Timeout:     3,
		MemorySize:  128,
		Environment: env,
		CodeZip:     []byte("original-zip"),
		State:       "Active",
	}
	if aerr := h.ls.putFunction(context.Background(), fn); aerr != nil {
		t.Fatalf("seed function: %s", aerr.Message)
	}
	return fn
}

func updateFunctionConfiguration(t *testing.T, h *Handler, name string, patch map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(patch)
	if err != nil {
		t.Fatalf("marshal patch: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/2015-03-31/functions/"+name+"/configuration", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withFunctionNameParam(req, name)
	rec := httptest.NewRecorder()
	h.UpdateFunctionConfiguration(rec, req)
	return rec
}

func withFunctionNameParam(req *http.Request, name string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", name)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}
