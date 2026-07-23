package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	defaultWALFileName    = "overcast.wal"
	defaultWALMaxLogBytes = 64 << 20 // 64 MiB
	defaultWALSyncEvery   = 100 * time.Millisecond
)

// WALSyncMode controls how frequently WAL writes are fsync'd to disk.
type WALSyncMode string

const (
	WALSyncAlways   WALSyncMode = "always"
	WALSyncInterval WALSyncMode = "interval"
	WALSyncNever    WALSyncMode = "never"
)

// WALOptions configures WALStore durability and compaction behavior.
type WALOptions struct {
	SyncMode WALSyncMode

	// SyncInterval is used only when SyncMode is WALSyncInterval.
	SyncInterval time.Duration

	// MaxLogBytes triggers compaction when the append log reaches this size.
	MaxLogBytes int64
}

type walOp string

const (
	walSet          walOp = "set"
	walDelete       walOp = "delete"
	walDeletePrefix walOp = "delete_prefix"
)

type walEntry struct {
	Op        walOp  `json:"op"`
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
	Value     string `json:"value,omitempty"`
}

// WALStore is a memory-first store with an append-only write-ahead log.
// Reads are served from memory. Every mutation is appended to disk and can be
// replayed on restart.
type WALStore struct {
	mem *MemoryStore

	path        string
	maxLogBytes int64
	syncMode    WALSyncMode
	syncEvery   time.Duration

	mu      sync.Mutex
	f       *os.File
	logSize int64
	closed  bool

	stopSync chan struct{}
	syncDone chan struct{}
}

// NewWALStore creates or opens the WAL-backed store rooted at dataDir.
func NewWALStore(dataDir string, opts WALOptions) (*WALStore, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("wal store: create data dir %q: %w", dataDir, err)
	}

	if opts.SyncMode == "" {
		opts.SyncMode = WALSyncInterval
	}
	if opts.SyncInterval <= 0 {
		opts.SyncInterval = defaultWALSyncEvery
	}
	if opts.MaxLogBytes <= 0 {
		opts.MaxLogBytes = defaultWALMaxLogBytes
	}
	if err := validateWALSyncMode(opts.SyncMode); err != nil {
		return nil, err
	}

	path := filepath.Join(dataDir, defaultWALFileName)

	mem := NewMemoryStore()
	if err := replayWALFile(path, mem); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("wal store: open %q: %w", path, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("wal store: stat %q: %w", path, err)
	}

	s := &WALStore{
		mem:         mem,
		path:        path,
		maxLogBytes: opts.MaxLogBytes,
		syncMode:    opts.SyncMode,
		syncEvery:   opts.SyncInterval,
		f:           f,
		logSize:     info.Size(),
		stopSync:    make(chan struct{}),
		syncDone:    make(chan struct{}),
	}

	if s.syncMode == WALSyncInterval {
		go s.runPeriodicSync()
	} else {
		close(s.syncDone)
	}

	return s, nil
}

func validateWALSyncMode(mode WALSyncMode) error {
	switch mode {
	case WALSyncAlways, WALSyncInterval, WALSyncNever:
		return nil
	default:
		return fmt.Errorf("wal store: sync mode must be always, interval, or never, got %q", mode)
	}
}

// replayWALFile rebuilds mem from the on-disk append log at path.
//
// Two forms of damage are tolerated rather than treated as fatal, since a
// process kill mid-append (the common case, given the default interval sync
// mode) always produces one of them:
//
//   - A torn final line: the very last line fails to decode AND the file does
//     not end with a trailing newline. This is exactly what a kill mid-Write
//     leaves behind. It is logged and replay stops there — every earlier,
//     complete entry has already been applied.
//   - A corrupt line anywhere else (mid-file garbage, or an unknown op): logged
//     and skipped, replay continues with the remaining lines. A summary count
//     is logged once replay finishes.
//
// Any other error (I/O failure, a decoded entry that MemoryStore itself
// rejects) still aborts replay — those are not the "expected torn write" case
// this function exists to tolerate.
func replayWALFile(path string, mem *MemoryStore) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("wal store: open replay file %q: %w", path, err)
	}

	lines := bytes.Split(data, []byte{'\n'})
	ctx := context.Background()
	var skipped int
	for i, line := range lines {
		if len(line) == 0 {
			continue
		}
		lineNum := i + 1

		var e walEntry
		if err := json.Unmarshal(line, &e); err != nil {
			if i == len(lines)-1 && len(data) > 0 && data[len(data)-1] != '\n' {
				walLogWarnf("wal store: %q has an incomplete final entry (line %d); ignoring: %v", path, lineNum, err)
				break
			}
			walLogWarnf("wal store: %q has a corrupt entry at line %d; skipping: %v", path, lineNum, err)
			skipped++
			continue
		}

		switch e.Op {
		case walSet:
			if err := mem.Set(ctx, e.Namespace, e.Key, e.Value); err != nil {
				return fmt.Errorf("wal store: replay set [%s/%s]: %w", e.Namespace, e.Key, err)
			}
		case walDelete:
			if err := mem.Delete(ctx, e.Namespace, e.Key); err != nil {
				return fmt.Errorf("wal store: replay delete [%s/%s]: %w", e.Namespace, e.Key, err)
			}
		case walDeletePrefix:
			if err := mem.DeletePrefix(ctx, e.Namespace, e.Key); err != nil {
				return fmt.Errorf("wal store: replay delete prefix [%s/%s*]: %w", e.Namespace, e.Key, err)
			}
		default:
			walLogWarnf("wal store: %q has an unknown op %q at line %d; skipping", path, e.Op, lineNum)
			skipped++
		}
	}

	if skipped > 0 {
		walLogWarnf("wal store: replay of %q skipped %d corrupt/unknown entries", path, skipped)
	}
	return nil
}

// walLogWarnf reports replay warnings to stderr. WALStore has no logger of
// its own to inject (unlike HybridStore's NewHybridStoreWithLogger) — this
// mirrors the no-logger-available fallback already used elsewhere in this
// package, e.g. sqlite.go's migration-failure path.
func walLogWarnf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...) //nolint:errcheck // best-effort diagnostic write.
}

func (s *WALStore) runPeriodicSync() {
	defer close(s.syncDone)
	ticker := time.NewTicker(s.syncEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				return
			}
			_ = s.f.Sync()
			s.mu.Unlock()
		case <-s.stopSync:
			return
		}
	}
}

func (s *WALStore) Get(ctx context.Context, namespace, key string) (string, bool, error) {
	return s.mem.Get(ctx, namespace, key)
}

func (s *WALStore) Set(ctx context.Context, namespace, key, value string) error {
	if err := s.mem.Set(ctx, namespace, key, value); err != nil {
		return err
	}
	e := walEntry{Op: walSet, Namespace: namespace, Key: key, Value: value}
	if err := s.appendEntryLocked(e); err != nil {
		return err
	}
	return s.maybeCompact()
}

func (s *WALStore) Delete(ctx context.Context, namespace, key string) error {
	if err := s.mem.Delete(ctx, namespace, key); err != nil {
		return err
	}
	e := walEntry{Op: walDelete, Namespace: namespace, Key: key}
	if err := s.appendEntryLocked(e); err != nil {
		return err
	}
	return s.maybeCompact()
}

func (s *WALStore) DeletePrefix(ctx context.Context, namespace, prefix string) error {
	if err := s.mem.DeletePrefix(ctx, namespace, prefix); err != nil {
		return err
	}
	e := walEntry{Op: walDeletePrefix, Namespace: namespace, Key: prefix}
	if err := s.appendEntryLocked(e); err != nil {
		return err
	}
	return s.maybeCompact()
}

func (s *WALStore) List(ctx context.Context, namespace, prefix string) ([]string, error) {
	return s.mem.List(ctx, namespace, prefix)
}

func (s *WALStore) ListNamespaces(ctx context.Context) ([]string, error) {
	return s.mem.ListNamespaces(ctx)
}

func (s *WALStore) Scan(ctx context.Context, namespace, prefix string) ([]KV, error) {
	return s.mem.Scan(ctx, namespace, prefix)
}

// ScanPage delegates to the underlying MemoryStore exactly like Get/List/
// Scan above — WALStore always reads from memory; only writes touch the
// append-only log.
func (s *WALStore) ScanPage(ctx context.Context, namespace, prefix, startAfter string, limit int) ([]KV, string, error) {
	return s.mem.ScanPage(ctx, namespace, prefix, startAfter, limit)
}

func (s *WALStore) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	if s.syncMode == WALSyncInterval {
		close(s.stopSync)
	}
	f := s.f
	s.mu.Unlock()

	if s.syncMode == WALSyncInterval {
		<-s.syncDone
	}

	if f == nil {
		return nil
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("wal store: final sync: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("wal store: close: %w", err)
	}
	return nil
}

func (s *WALStore) appendEntryLocked(entry walEntry) error {
	b, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("wal store: encode entry: %w", err)
	}
	b = append(b, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return io.ErrClosedPipe
	}
	if _, err := s.f.Write(b); err != nil {
		return fmt.Errorf("wal store: append: %w", err)
	}
	s.logSize += int64(len(b))

	if s.syncMode == WALSyncAlways {
		if err := s.f.Sync(); err != nil {
			return fmt.Errorf("wal store: sync: %w", err)
		}
	}

	return nil
}

func (s *WALStore) maybeCompact() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.logSize < s.maxLogBytes {
		return nil
	}
	return s.compactLocked()
}

func (s *WALStore) compactLocked() error {
	tmpPath := s.path + ".new"
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("wal store: compact create tmp: %w", err)
	}

	if err := s.writeSnapshot(tmp); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("wal store: compact sync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("wal store: compact close tmp: %w", err)
	}

	if err := s.f.Close(); err != nil {
		return fmt.Errorf("wal store: compact close active: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("wal store: compact rename: %w", err)
	}

	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("wal store: compact reopen: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("wal store: compact stat: %w", err)
	}

	s.f = f
	s.logSize = info.Size()
	return nil
}

func (s *WALStore) writeSnapshot(w io.Writer) error {
	s.mem.mu.RLock()
	defer s.mem.mu.RUnlock()

	enc := json.NewEncoder(w)
	var firstErr error
	for namespace, tree := range s.mem.data {
		tree.Scan(func(key, value string) bool {
			if err := enc.Encode(walEntry{Op: walSet, Namespace: namespace, Key: key, Value: value}); err != nil {
				firstErr = err
				return false
			}
			return true
		})
		if firstErr != nil {
			return fmt.Errorf("wal store: compact encode snapshot: %w", firstErr)
		}
	}

	return nil
}
