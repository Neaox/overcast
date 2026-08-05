package lambda

import (
	"context"
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
}

func newS3SyncWatcher(ls *lambdaStore, fetch S3FetchFunc, log *zap.Logger, clk clock.Clock) *s3SyncWatcher {
	return &s3SyncWatcher{ls: ls, fetch: fetch, log: log, clk: clk}
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
		w.syncFunctionCode(ctx, fn)
	}
}

// syncFunctionCode fetches the zip from S3, stores it in the function record,
// bumps the revision, and retires the warm instance still running the old code.
func (w *s3SyncWatcher) syncFunctionCode(ctx context.Context, fn *Function) {
	expectedCodeHash := fn.CodeHash
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
	// mutate under the code-source lock. The source binding prevents an inline
	// update from being undone; the code hash fences the exact code generation
	// observed before the fetch, so an older watcher cannot overwrite a newer
	// package fetched from the same S3 location. Unrelated configuration updates
	// deliberately do not invalidate the refresh.
	var previousIdentity string
	fresh, changed, aerr := w.ls.mutateFunction(ctx, fn.Name, func(current *Function) (bool, *protocol.AWSError) {
		if current.CodeS3Bucket != fn.CodeS3Bucket || current.CodeS3Key != fn.CodeS3Key {
			return false, nil
		}
		if current.CodeHash != expectedCodeHash {
			return false, nil
		}
		previousIdentity = functionInstanceIdentity(current)
		current.setCode(zip)
		current.RevisionId = uuid.NewString()
		current.LastModified = w.clk.Now().UTC().Format(time.RFC3339)
		return true, nil
	})
	if aerr != nil {
		w.log.Warn("s3 sync: persist updated function",
			zap.String("function", fn.Name),
			zap.Error(aerr),
		)
		return
	}
	if !changed {
		return
	}

	if w.retire != nil {
		w.retire(fresh, previousIdentity)
	}

	w.log.Info("s3 sync: refreshed function code from S3",
		zap.String("function", fn.Name),
		zap.String("bucket", fn.CodeS3Bucket),
		zap.String("key", fn.CodeS3Key),
		zap.Int("bytes", len(zip)),
	)
}
