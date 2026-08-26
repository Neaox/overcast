package lambda

// esm_concurrency_test.go — pins how many execution environments a single
// function can have at once, and which trigger paths can actually drive that.
//
// These tests use a real clock because pollSQS ticks on a wall-clock ticker.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// ─── pool ────────────────────────────────────────────────────────────────────

// countingColdStartRuntime hands out a fresh instance per Acquire and records
// how many it created.
type countingColdStartRuntime struct {
	mu      sync.Mutex
	created []*poolTestInstance
}

func (r *countingColdStartRuntime) CanHandle(string) bool { return true }

func (r *countingColdStartRuntime) Acquire(_ context.Context, fn *Function) (RuntimeInstance, error) {
	inst := newPoolTestInstance(fn.Name)
	inst.configIdentity = functionInstanceIdentity(fn)
	r.mu.Lock()
	r.created = append(r.created, inst)
	r.mu.Unlock()
	return inst, nil
}

func (r *countingColdStartRuntime) Release(context.Context, RuntimeInstance, bool) {}

func (r *countingColdStartRuntime) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.created)
}

func TestInstancePool_concurrentInvocationsGetSeparateInstances(t *testing.T) {
	// Given: a pool with one warm instance for a function.
	rt := &countingColdStartRuntime{}
	pool := NewInstancePool(rt, zap.NewNop(), clock.NewMock(), PoolLimits{})
	defer pool.Stop()
	fn := &Function{Name: "fn", Environment: map[string]string{"STAGE": "dev"}}
	warm := newPoolTestInstance("fn")
	warm.configIdentity = functionInstanceIdentity(fn)
	pool.Release(context.Background(), warm, true)

	// When: five invocations of the same function overlap — none releases
	// before the others acquire.
	const concurrency = 5
	held := make([]RuntimeInstance, concurrency)
	var wg sync.WaitGroup
	for i := range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			inst, err := pool.Acquire(context.Background(), fn)
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			held[i] = inst
		}()
	}
	wg.Wait()

	// Then: each invocation holds its own execution environment — the warm one
	// plus four cold starts. The pool serialises nothing.
	if got := rt.count(); got != concurrency-1 {
		t.Fatalf("cold starts = %d, want %d (warm instance reused for the first)", got, concurrency-1)
	}
	seen := make(map[RuntimeInstance]bool, concurrency)
	for i, inst := range held {
		if inst == nil {
			t.Fatalf("invocation %d got no instance", i)
		}
		if seen[inst] {
			t.Fatalf("invocation %d was handed an instance already in use", i)
		}
		seen[inst] = true
	}
}

func TestInstancePool_burstStaysWarmForReuse(t *testing.T) {
	// Given: three concurrent invocations of one function, each with its own
	// instance.
	rt := &countingColdStartRuntime{}
	pool := NewInstancePool(rt, zap.NewNop(), clock.NewMock(), PoolLimits{})
	defer pool.Stop()
	fn := &Function{Name: "fn"}
	insts := make([]RuntimeInstance, 3)
	for i := range insts {
		inst, err := pool.Acquire(context.Background(), fn)
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		insts[i] = inst
	}

	// When: all three finish.
	for _, inst := range insts {
		pool.Release(context.Background(), inst, true)
	}

	// Then: all three stay warm and the next burst reuses them instead of cold
	// starting again.
	pool.mu.Lock()
	warm := len(pool.entries["fn"])
	pool.mu.Unlock()
	if warm != 3 {
		t.Fatalf("warm instances = %d, want 3", warm)
	}
	for i, inst := range insts {
		if got := inst.(*poolTestInstance).CloseCalls(); got != 0 {
			t.Fatalf("instance %d close calls = %d, want 0", i, got)
		}
	}
	for range insts {
		if _, err := pool.Acquire(context.Background(), fn); err != nil {
			t.Fatalf("acquire: %v", err)
		}
	}
	if got := rt.count(); got != 3 {
		t.Fatalf("total cold starts = %d, want 3 — the second burst should have reused the warm set", got)
	}
}

// ─── SQS event source mapping ────────────────────────────────────────────────

// blockingInvoker holds every invocation open until release is closed, so a
// test can observe how many the poller has in flight at once.
type blockingInvoker struct {
	inFlight atomic.Int32
	peak     atomic.Int32
	release  chan struct{}
}

func newBlockingInvoker() *blockingInvoker {
	return &blockingInvoker{release: make(chan struct{})}
}

func (b *blockingInvoker) Invoke(ctx context.Context, _ string, _ []byte) (*events.InvokeOutcome, error) {
	n := b.inFlight.Add(1)
	for {
		peak := b.peak.Load()
		if n <= peak || b.peak.CompareAndSwap(peak, n) {
			break
		}
	}
	defer b.inFlight.Add(-1)
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	return &events.InvokeOutcome{}, nil
}

// alwaysMessagesReceiver returns one distinct message on every receive, so the
// poller always has work to hand to a Lambda.
type alwaysMessagesReceiver struct{ n atomic.Int64 }

func (r *alwaysMessagesReceiver) ReceiveMessages(_ context.Context, _ string, _, _ int) ([]events.ReceivedMessage, error) {
	id := r.n.Add(1)
	return []events.ReceivedMessage{{
		MessageID:     "msg-" + string(rune('a'+id%26)),
		ReceiptHandle: "rh",
		Body:          "body",
	}}, nil
}

func (r *alwaysMessagesReceiver) DeleteMessages(context.Context, string, []string) error {
	return nil
}

func startSQSPoller(t *testing.T, esm *EventSourceMapping, inv lambdaInvoker, recv events.MessageReceiver) *esmDeliveryManager {
	t.Helper()
	ls := newLambdaStore(state.NewMemoryStore(), "us-east-1", clock.New())
	es := newESMStore(ls)
	if aerr := es.putESM(context.Background(), esm); aerr != nil {
		t.Fatalf("putESM: %v", aerr)
	}
	mgr := newESMDeliveryManager(
		es, inv, recv, nil, nil,
		serviceutil.NewServiceLogger(zap.NewNop(), "lambda"),
		clock.New(), &config.Config{Region: "us-east-1"},
		context.Background(),
	)
	mgr.Start(esm)
	t.Cleanup(func() { stopAll(t, mgr) })
	return mgr
}

func awaitPeak(t *testing.T, inv *blockingInvoker, want int32, within time.Duration) int32 {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if inv.peak.Load() >= want {
			return inv.peak.Load()
		}
		time.Sleep(20 * time.Millisecond)
	}
	return inv.peak.Load()
}

func TestPollSQS_runsInvocationsConcurrently(t *testing.T) {
	// Given: an SQS event source mapping with no ScalingConfig, and a function
	// whose invocations do not return.
	inv := newBlockingInvoker()
	defer close(inv.release)
	startSQSPoller(t, &EventSourceMapping{
		UUID:           "concurrency-uuid",
		FunctionArn:    "arn:aws:lambda:us-east-1:000000000000:function:fn",
		EventSourceArn: "arn:aws:sqs:us-east-1:000000000000:q",
		State:          esmStateEnabled,
		BatchSize:      1,
	}, inv, &alwaysMessagesReceiver{})

	// When: the poller ticks repeatedly while earlier batches are still running.
	peak := awaitPeak(t, inv, 3, 6*time.Second)

	// Then: batches overlap, so one function ends up with several execution
	// environments at once. Note the ceiling is a side effect of the poller's
	// one-batch-per-second cadence, not AWS's poller scaling.
	if peak < 3 {
		t.Fatalf("peak concurrent invocations = %d, want >= 3", peak)
	}
}

func TestPollSQS_scalingConfigCapsConcurrency(t *testing.T) {
	// Given: the same mapping with MaximumConcurrency = 2.
	inv := newBlockingInvoker()
	defer close(inv.release)
	startSQSPoller(t, &EventSourceMapping{
		UUID:           "capped-uuid",
		FunctionArn:    "arn:aws:lambda:us-east-1:000000000000:function:fn",
		EventSourceArn: "arn:aws:sqs:us-east-1:000000000000:q",
		State:          esmStateEnabled,
		BatchSize:      1,
		ScalingConfig:  &ScalingConfig{MaximumConcurrency: 2},
	}, inv, &alwaysMessagesReceiver{})

	// When: the poller runs long enough to have exceeded the cap without it.
	awaitPeak(t, inv, 3, 5*time.Second)

	// Then: concurrency never exceeds MaximumConcurrency; further batches are
	// throttled and their messages return to the queue on visibility timeout.
	if peak := inv.peak.Load(); peak > 2 {
		t.Fatalf("peak concurrent invocations = %d, want <= 2 (MaximumConcurrency)", peak)
	}
}

func TestDynamoDBStreamESM_deliversOneBatchAtATime(t *testing.T) {
	// Given: a DynamoDB Streams mapping — deliveries run on a single
	// per-mapping worker (processStreamRecords calls deliverStreamBatch
	// synchronously) rather than a goroutine per batch.
	//
	// This documents the asymmetry with SQS: a stream mapping never produces
	// concurrent execution environments for one function, by design, to avoid a
	// cold-start storm on bulk writes.
	inv := newBlockingInvoker()
	defer close(inv.release)
	ls := newLambdaStore(state.NewMemoryStore(), "us-east-1", clock.New())
	es := newESMStore(ls)
	esm := &EventSourceMapping{
		UUID:           "stream-uuid",
		FunctionArn:    "arn:aws:lambda:us-east-1:000000000000:function:fn",
		EventSourceArn: "arn:aws:dynamodb:us-east-1:000000000000:table/T/stream/2026",
		State:          esmStateEnabled,
		BatchSize:      1,
	}
	if aerr := es.putESM(context.Background(), esm); aerr != nil {
		t.Fatalf("putESM: %v", aerr)
	}
	bus := events.NewBus()
	defer bus.Stop()
	mgr := newESMDeliveryManager(
		es, inv, nil, nil, bus,
		serviceutil.NewServiceLogger(zap.NewNop(), "lambda"),
		clock.New(), &config.Config{Region: "us-east-1"},
		context.Background(),
	)
	mgr.Start(esm)
	defer stopAll(t, mgr)

	// When: several stream records arrive while the first invocation is still
	// running.
	for i := range 5 {
		bus.Publish(context.Background(), events.Event{
			Type:   events.DynamoDBStreamInsert,
			Source: "dynamodb",
			Payload: events.DynamoDBStreamPayload{
				Table:          "T",
				EventName:      "INSERT",
				SequenceNumber: int64(i + 1),
			},
		})
	}

	// Then: only one invocation is ever in flight.
	peak := awaitPeak(t, inv, 2, 2*time.Second)
	if peak > 1 {
		t.Fatalf("peak concurrent invocations = %d, want 1 (stream mappings are serial)", peak)
	}
}

// ─── instance tracker ────────────────────────────────────────────────────────

func TestInstanceTracker_reportsEveryConcurrentInstance(t *testing.T) {
	// Given: a function with three concurrent invocations in flight — three
	// real containers.
	tracker := newInstanceTracker(clock.NewMock(), zap.NewNop())
	t.Cleanup(tracker.Stop)
	for range 3 {
		trackedFor(tracker, "fn", nil).Running()
	}

	// Then: all three are reported, so GET /_overcast/lambda/instances and the system
	// map show the concurrency that is actually running.
	snap := tracker.Instances()
	if len(snap) != 3 {
		t.Fatalf("tracked instances = %d, want 3", len(snap))
	}
	ids := make(map[string]bool, 3)
	for _, inst := range snap {
		if inst.InstanceID == "" {
			t.Fatal("instance reported with no ID")
		}
		if ids[inst.InstanceID] {
			t.Fatalf("duplicate instance ID %s", inst.InstanceID)
		}
		ids[inst.InstanceID] = true
	}
}

func TestInstanceTracker_warmReuseKeepsTheSameInstanceID(t *testing.T) {
	// Given: an instance that has served one invocation and gone idle.
	tracker := newInstanceTracker(clock.NewMock(), zap.NewNop())
	t.Cleanup(tracker.Stop)
	inst := newPoolTestInstance("fn")
	first := tracker.Begin("fn", nil)
	first.Bind(inst)
	first.Finish(true, "")
	firstID := tracker.Instances()[0].InstanceID

	// When: the same environment serves a second invocation.
	second := tracker.Begin("fn", nil)
	second.Bind(inst)

	// Then: it is still one instance with the same ID — warm reuse is not a
	// new environment.
	snap := tracker.Instances()
	if len(snap) != 1 {
		t.Fatalf("tracked instances = %d, want 1", len(snap))
	}
	if snap[0].InstanceID != firstID {
		t.Fatalf("instance ID changed on warm reuse: %s → %s", firstID, snap[0].InstanceID)
	}
	if snap[0].Status != instanceStatusRunning {
		t.Fatalf("status = %q, want %q", snap[0].Status, instanceStatusRunning)
	}
}

// observerFunc adapts a plain callback to InstanceObserver for tests that only
// care about lost instances.
type observerFunc func(functionName string)

func (f observerFunc) InstanceWarmed(string, string, string, string) {}
func (f observerFunc) InstanceLost(functionName, _ string, _ string) { f(functionName) }
