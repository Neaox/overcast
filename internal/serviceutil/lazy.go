package serviceutil

import (
	"sync"
	"sync/atomic"
)

// LazyInit provides a generic lazy-initialisation wrapper using sync.Once.
//
// In Go, "lazy loading" of a service means: register routes immediately
// (so the router is ready), but defer any expensive initialisation — store
// setup, background goroutines, connection pools — until the first actual
// request arrives.
//
// This is important for:
//   - Lambda: don't start Node.js process supervisors until the first Invoke
//   - DynamoDB: don't start the expression evaluator goroutines until first use
//   - Any service with expensive startup: only pay the cost if the service is called
//
// TypeScript analogy: a module-level singleton initialised on first import,
// but with explicit control over when "first import" happens.
//
// Usage in a service handler:
//
//	type Handler struct {
//	    init   serviceutil.LazyInit
//	    // ... other fields
//	}
//
//	func (h *Handler) ensureInitialised(cfg *config.Config) error {
//	    return h.init.Do(func() error {
//	        // expensive setup: extract bootstrap.js, start supervisor, etc.
//	        return h.startNodeSupervisor(cfg)
//	    })
//	}
//
//	func (h *Handler) Invoke(w http.ResponseWriter, r *http.Request) {
//	    if err := h.ensureInitialised(h.cfg); err != nil {
//	        protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
//	        return
//	    }
//	    // ... handle request
//	}
type LazyInit struct {
	mu   sync.Mutex
	done atomic.Bool
}

// Do runs fn exactly once, the first time Do is called. Subsequent calls
// return the same error (or nil) without calling fn again.
//
// If fn returns an error, the LazyInit is NOT marked as successfully done —
// the next call to Do will attempt fn again. This allows transient failures
// (e.g. "node not in PATH") to be retried without restarting the server.
//
// If fn succeeds (returns nil), all future calls return nil immediately
// without calling fn again.
func (l *LazyInit) Do(fn func() error) error {
	if l.done.Load() {
		return nil // fast path: already initialised successfully
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// Double-check after acquiring the lock.
	if l.done.Load() {
		return nil
	}

	if err := fn(); err != nil {
		return err
	}
	l.done.Store(true)
	return nil
}

// Done reports whether the lazy initialisation has completed successfully.
func (l *LazyInit) Done() bool {
	return l.done.Load()
}

// Reset forces re-initialisation on the next Do call.
// Primarily useful in tests that need a clean state between runs.
func (l *LazyInit) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.done.Store(false)
}
