package lambda

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
)

// S3FetchFunc retrieves the raw bytes of an S3 object from the emulated S3
// service. Provided by the router as a closure over the S3 service so that the
// lambda package does not import the s3 package directly.
type S3FetchFunc func(ctx context.Context, bucket, key string) ([]byte, *protocol.AWSError)

// s3SyncWatcher subscribes to S3ObjectCreated events on the shared bus and,
// when the updated object matches the code location of a Lambda function,
// fetches the new zip and updates the stored function so the next invoke picks
// up the change without an explicit UpdateFunctionCode call.
//
// This mirrors the Reactive S3 Sync pattern: code lives in S3 and any
// PutObject to the function's code bucket/key triggers an automatic refresh.
// The warm instance running the previous code is retired as soon as the new
// bytes land, exactly as it would be after an explicit UpdateFunctionCode.
type s3SyncWatcher struct {
	ls    *lambdaStore
	fetch S3FetchFunc
	log   *zap.Logger
	clk   clock.Clock
	// retire destroys the warm execution environment for a function whose code
	// just changed. Wired by Service.InitS3Sync; nil in tests that only assert
	// on stored state.
	retire func(fn *Function, previousIdentity string)
	// beforeRetire is a deterministic test seam for the scheduling gap between
	// persistence and retirement arbitration. Production leaves it nil.
	beforeRetire func(fn *Function)
	mu           sync.Mutex
	next         uint64
	state        map[string]s3SyncState
}

type s3SyncState struct {
	creationID      string
	appliedRevision uint64
	retirement      *sync.Mutex
}

func newS3SyncWatcher(ls *lambdaStore, fetch S3FetchFunc, log *zap.Logger, clk clock.Clock) *s3SyncWatcher {
	return &s3SyncWatcher{ls: ls, fetch: fetch, log: log, clk: clk, state: make(map[string]s3SyncState)}
}

func (h *Handler) setS3SyncWatcher(w *s3SyncWatcher) {
	h.s3SyncMu.Lock()
	h.s3SyncWatcher = w
	h.s3SyncMu.Unlock()
}

func (h *Handler) forgetS3SyncFunction(fn *Function) {
	if fn == nil {
		return
	}
	h.s3SyncMu.RLock()
	w := h.s3SyncWatcher
	h.s3SyncMu.RUnlock()
	if w != nil {
		w.forgetFunction(fn.ARN, fn.CreationID)
	}
}

func (w *s3SyncWatcher) forgetFunction(arn, creationID string) {
	retirement := w.retirementLock(arn)
	if retirement == nil {
		return
	}
	// Join this function's retirement ordering so a callback that already
	// passed its generation check completes before DeleteFunction evicts
	// pool/tracker state. Waiting callbacks observe the removed state and skip.
	retirement.Lock()
	defer retirement.Unlock()
	w.mu.Lock()
	state, ok := w.state[arn]
	// Preserve a slot already established by a concurrent recreation. A late
	// event from the deleted creation cannot repopulate its slot because code
	// commits also fence current.CreationID before writing watcher state.
	if ok && state.creationID == creationID {
		delete(w.state, arn)
	}
	w.mu.Unlock()
}

func (w *s3SyncWatcher) retirementLock(arn string) *sync.Mutex {
	w.mu.Lock()
	defer w.mu.Unlock()
	state, ok := w.state[arn]
	if !ok {
		return nil
	}
	if state.retirement == nil {
		state.retirement = &sync.Mutex{}
		w.state[arn] = state
	}
	return state.retirement
}

func (w *s3SyncWatcher) retireFunctionIfCurrent(fn *Function, previousIdentity string, eventRevision uint64) {
	if w.retire == nil {
		return
	}
	// Retirement may close containers and therefore block. Serialize callbacks
	// for this ARN separately from code commits, then re-check the committed
	// generation so a delayed old callback cannot overwrite newer pool expectations.
	retirement := w.retirementLock(fn.ARN)
	if retirement == nil {
		return
	}
	retirement.Lock()
	defer retirement.Unlock()
	w.mu.Lock()
	state, ok := w.state[fn.ARN]
	current := ok && state.creationID == fn.CreationID && state.appliedRevision == eventRevision
	w.mu.Unlock()
	if current {
		w.retire(fn, previousIdentity)
	}
}

// register subscribes the watcher to the event bus and returns a cancel func
// that removes the subscription. Call cancel during service shutdown.
func (w *s3SyncWatcher) register(bus *events.Bus) (cancel func()) {
	return bus.Subscribe(events.S3ObjectCreated, w.onS3ObjectCreated)
}

func (w *s3SyncWatcher) onS3ObjectCreated(ctx context.Context, e events.Event) {
	payload, ok := e.Payload.(events.S3ObjectPayload)
	if !ok {
		return
	}

	fns, aerr := w.ls.listFunctions(ctx)
	if aerr != nil {
		w.log.Warn("s3 sync: list functions", zap.Error(aerr))
		return
	}

	for _, fn := range fns {
		if fn.CodeS3Bucket != payload.Bucket || fn.CodeS3Key != payload.Key {
			continue
		}
		w.syncFunctionCodeForEvent(ctx, fn, e.Seq)
	}
}

// syncFunctionCode fetches the zip from S3, stores it in the function record,
// bumps the revision, and retires the warm instance still running the old code.
func (w *s3SyncWatcher) syncFunctionCode(ctx context.Context, fn *Function) {
	w.syncFunctionCodeForEvent(ctx, fn, 0)
}

func (w *s3SyncWatcher) syncFunctionCodeForEvent(ctx context.Context, fn *Function, busRevision uint64) {
	expectedCodeGeneration := fn.CodeGeneration
	stateKey := fn.ARN
	w.mu.Lock()
	eventRevision := busRevision
	if eventRevision == 0 {
		w.next++
		eventRevision = w.next
	} else if eventRevision > w.next {
		w.next = eventRevision
	}
	w.mu.Unlock()
	zip, err := w.fetch(ctx, fn.CodeS3Bucket, fn.CodeS3Key)
	if err != nil {
		w.log.Warn("s3 sync: fetch zip failed",
			zap.String("function", fn.Name),
			zap.String("bucket", fn.CodeS3Bucket),
			zap.String("key", fn.CodeS3Key),
			zap.Error(err),
		)
		return
	}

	// The fetch may be slow, and fn was read before it started. Re-read and
	// mutate under the code-source lock. The source binding and CodeGeneration
	// prevent an explicit inline/S3 update from being undone. Unrelated
	// configuration updates deliberately do not invalidate the refresh.
	// Serialize only the commits, not the downloads. Event revisions are
	// assigned before fetching so the newest event wins regardless of fetch
	// completion order. Reactive refreshes preserve CodeGeneration, whereas
	// every explicit source write rotates it—even when the bytes are identical.
	// RevisionId is not the fence because unrelated configuration updates rotate it.
	w.mu.Lock()
	state := w.state[stateKey]
	if state.creationID == fn.CreationID && eventRevision <= state.appliedRevision {
		w.mu.Unlock()
		return
	}
	var previousIdentity string
	fresh, changed, aerr := w.ls.mutateFunction(ctx, fn.Name, func(current *Function) (bool, *protocol.AWSError) {
		if current.CreationID != fn.CreationID {
			return false, nil
		}
		if current.CodeS3Bucket != fn.CodeS3Bucket || current.CodeS3Key != fn.CodeS3Key {
			return false, nil
		}
		if current.CodeGeneration != expectedCodeGeneration {
			return false, nil
		}
		previousIdentity = functionInstanceIdentity(current)
		current.setSyncedCode(zip)
		current.RevisionId = uuid.NewString()
		current.LastModified = w.clk.Now().UTC().Format(time.RFC3339)
		return true, nil
	})
	if aerr != nil {
		w.mu.Unlock()
		w.log.Warn("s3 sync: persist updated function",
			zap.String("function", fn.Name),
			zap.Error(aerr),
		)
		return
	}
	if !changed {
		w.mu.Unlock()
		return
	}
	retirement := state.retirement
	if retirement == nil {
		retirement = &sync.Mutex{}
	}
	w.state[stateKey] = s3SyncState{
		creationID:      fn.CreationID,
		appliedRevision: eventRevision,
		retirement:      retirement,
	}
	w.mu.Unlock()

	if w.beforeRetire != nil {
		w.beforeRetire(fresh)
	}
	w.retireFunctionIfCurrent(fresh, previousIdentity, eventRevision)

	w.log.Info("s3 sync: refreshed function code from S3",
		zap.String("function", fn.Name),
		zap.String("bucket", fn.CodeS3Bucket),
		zap.String("key", fn.CodeS3Key),
		zap.Int("bytes", len(zip)),
	)
}
