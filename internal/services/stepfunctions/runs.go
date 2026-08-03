package stepfunctions

import (
	"context"
	"sync"
	"time"
)

// In-flight execution tracking.
//
// StartExecution accepts and returns while the execution is still RUNNING, as
// AWS does, so the interpreter runs on a goroutine rather than on the request.
// This file is what keeps that honest: every run is registered while it is
// alive, so DescribeExecution and GetExecutionHistory can observe it
// progressing, StopExecution can interrupt it, and shutdown can drain it.

// executionRun tracks one execution while it is running.
type executionRun struct {
	// cancel unwinds the interpreter — used by StopExecution and by shutdown.
	cancel context.CancelFunc
	// hist is the live recorder. GetExecutionHistory snapshots it so a running
	// execution reports the states it has already been through.
	hist *historyRecorder

	mu        sync.Mutex
	stopped   bool
	stoppedAt time.Time
	errName   string
	cause     string
}

// stop asks the execution to unwind as ABORTED, recording the error and cause
// StopExecution supplied and the moment it was asked for.
func (r *executionRun) stop(now time.Time, errName, cause string) {
	r.mu.Lock()
	if !r.stopped {
		r.stopped = true
		r.stoppedAt = now
		r.errName = errName
		r.cause = cause
	}
	r.mu.Unlock()
	r.cancel()
}

// abortReason reports whether this unwind was asked for by StopExecution, and
// with what error and cause. It is how the interpreter tells an abort apart
// from the budget running out.
func (r *executionRun) abortReason() (stopped bool, errName, cause string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopped, r.errName, r.cause
}

// stopTime returns the time StopExecution was called, if it was, so the
// terminal record carries the same stopDate StopExecution already returned.
func (r *executionRun) stopTime() (time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stoppedAt, r.stopped
}

// registerRun records a run as in-flight. Returns the run so the caller can
// hand it to the interpreter.
func (h *Handler) registerRun(execARN string, run *executionRun) {
	h.runsMu.Lock()
	defer h.runsMu.Unlock()
	if h.runs == nil {
		h.runs = make(map[string]*executionRun)
	}
	h.runs[execARN] = run
}

// lookupRun returns the in-flight run for an execution, or nil once it has
// finished and its terminal state is in the store.
func (h *Handler) lookupRun(execARN string) *executionRun {
	h.runsMu.Lock()
	defer h.runsMu.Unlock()
	return h.runs[execARN]
}

// releaseRun forgets a run once its terminal state has been persisted, so
// readers fall through to the store.
func (h *Handler) releaseRun(execARN string) {
	h.runsMu.Lock()
	defer h.runsMu.Unlock()
	delete(h.runs, execARN)
}

// Stop drains in-flight executions. It first cancels them — an execution
// parked in a Wait would otherwise hold shutdown open for its whole budget —
// then waits for the goroutines to finish writing their terminal state, or
// until ctx expires.
//
// Satisfies router.Stopper.
func (h *Handler) Stop(ctx context.Context) {
	if h.shutdownCancel != nil {
		h.shutdownCancel()
	}
	done := make(chan struct{})
	go func() {
		h.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		if h.log != nil {
			h.log.Logger().Warn("stepfunctions: timed out waiting for in-flight executions to finish")
		}
	}
}
