package mdns

// proc.go — a generic Publisher that shells out to a long-lived command per
// record. Darwin and Windows back on to `dns-sd -P`; Linux backs on to
// `avahi-publish -a -R`. The only platform-specific bit is the command
// constructor, which is injected at build time from darwin.go / linux.go /
// windows.go.
//
// Contract of the spawned command:
//
//   - It must block for the lifetime of the advertisement. Exiting before
//     Unpublish is called is treated as a silent failure: the record is
//     dropped from the active map on the next Close.
//   - It must tear the advertisement down on SIGKILL (or, on Windows, on
//     Process.Kill). Both dns-sd and avahi-publish meet this guarantee.
//
// Concurrency: procPublisher is safe for concurrent use. Publish/Unpublish
// take the mutex only while mutating the map — the subprocess itself runs
// outside the lock.

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"

	"go.uber.org/zap"
)

// cmdFactory builds the platform-specific command that advertises r. ctx is
// bound to the subprocess lifetime, so cancelling it terminates the process.
// Factories must not start the command — procPublisher owns the lifecycle.
type cmdFactory func(ctx context.Context, r Record) *exec.Cmd

// procPublisher implements Publisher by spawning one long-lived subprocess
// per advertised Record.
type procPublisher struct {
	log     *zap.Logger
	factory cmdFactory

	mu     sync.Mutex
	active map[string]*procEntry
	closed bool
}

type procEntry struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	record Record
}

func newProcPublisher(log *zap.Logger, factory cmdFactory) *procPublisher {
	return &procPublisher{
		log:     log,
		factory: factory,
		active:  make(map[string]*procEntry),
	}
}

// Publish starts a subprocess that advertises r. If r.Hostname is already
// active, the existing subprocess is replaced atomically (old one killed,
// new one started). Re-publishing a record with the same IP is still a
// replace — callers are expected to dedupe via the bridge.
func (p *procPublisher) Publish(_ context.Context, r Record) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errors.New("mdns: publisher closed")
	}

	if prev, ok := p.active[r.Key()]; ok {
		prev.cancel()
		_ = prev.cmd.Wait()
		delete(p.active, r.Key())
	}

	procCtx, cancel := context.WithCancel(context.Background())
	cmd := p.factory(procCtx, r)
	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("mdns: start %s: %w", cmd.Path, err)
	}
	p.active[r.Key()] = &procEntry{cmd: cmd, cancel: cancel, record: r}
	p.log.Debug("mdns: published", zap.String("hostname", r.Hostname), zap.Stringer("ip", r.IP))
	return nil
}

// Unpublish terminates the subprocess advertising r and removes it from the
// active set. Unpublishing an unknown record is a no-op.
func (p *procPublisher) Unpublish(_ context.Context, r Record) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, ok := p.active[r.Key()]
	if !ok {
		return nil
	}
	delete(p.active, r.Key())
	entry.cancel()
	_ = entry.cmd.Wait()
	p.log.Debug("mdns: unpublished", zap.String("hostname", r.Hostname))
	return nil
}

// Close tears down every still-active subprocess. Safe to call multiple
// times; subsequent calls are no-ops.
func (p *procPublisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	for key, entry := range p.active {
		entry.cancel()
		_ = entry.cmd.Wait()
		delete(p.active, key)
	}
	return nil
}
