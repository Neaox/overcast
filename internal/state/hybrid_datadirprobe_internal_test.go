//go:build !nosqlite

package state

// Failing-first coverage for storage-pressure-handling item 3: the
// startup fsync micro-probe that flags a slow data directory (e.g. a Docker
// Desktop bind mount — see docs/performance.md "Data dir placement") before
// it manifests as flush/read latency. White-box (package state) so tests
// can inject a fake probe via HybridOptions' unexported dataDirFsyncProbe
// field, instead of needing an actually slow filesystem in CI.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// waitForDataDirProbe polls until HybridStore.getDataDirProbeResult is
// non-nil or the deadline passes. The probe runs in a background goroutine
// (storage-pressure-handling item 3 respects the no-blocking-startup rule),
// so tests must wait for it rather than asserting immediately after New*.
func waitForDataDirProbe(t *testing.T, s *HybridStore) *DataDirProbeResult {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if r := s.getDataDirProbeResult(); r != nil {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the data dir fsync probe to complete")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestHybridStore_SlowDataDirProbe_FlagsSlowAndRecordsMetrics injects a fake
// probe reporting a fsync duration well past the configured threshold and
// proves the result is recorded as "slow" and surfaces via DebugMetrics.
func TestHybridStore_SlowDataDirProbe_FlagsSlowAndRecordsMetrics(t *testing.T) {
	const threshold = 50 * time.Millisecond
	const fakeSlowFsync = 600 * time.Millisecond // matches the measured Docker-Desktop-bind-mount order of magnitude

	s, err := NewHybridStoreWithOptions(t.TempDir(), HybridOptions{
		FlushInterval:             time.Hour,
		DataDirSlowFsyncThreshold: threshold,
		dataDirFsyncProbe: func(dataDir string) (time.Duration, error) {
			return fakeSlowFsync, nil
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewHybridStoreWithOptions: %v", err)
	}
	defer s.Close()

	result := waitForDataDirProbe(t, s)
	if !result.Slow {
		t.Fatalf("probe result = %+v, want Slow=true for a %v fsync against a %v threshold", result, fakeSlowFsync, threshold)
	}
	if result.FsyncMillis != fakeSlowFsync.Milliseconds() {
		t.Fatalf("probe result FsyncMillis = %d, want %d", result.FsyncMillis, fakeSlowFsync.Milliseconds())
	}
	if result.ProbedAt.IsZero() {
		t.Fatal("probe result ProbedAt must be set")
	}

	m := s.DebugMetrics(context.Background(), DebugMetricsOptions{})
	if m.DataDirProbe == nil {
		t.Fatal("DebugMetrics.DataDirProbe = nil, want the probe result to be exposed")
	}
	if !m.DataDirProbe.Slow {
		t.Fatalf("DebugMetrics.DataDirProbe = %+v, want Slow=true", m.DataDirProbe)
	}
}

// TestHybridStore_FastDataDirProbe_NotFlaggedSlow is the negative case: a
// fsync duration under the threshold must not be flagged.
func TestHybridStore_FastDataDirProbe_NotFlaggedSlow(t *testing.T) {
	const threshold = 75 * time.Millisecond
	const fakeFastFsync = 2 * time.Millisecond

	s, err := NewHybridStoreWithOptions(t.TempDir(), HybridOptions{
		FlushInterval:             time.Hour,
		DataDirSlowFsyncThreshold: threshold,
		dataDirFsyncProbe: func(dataDir string) (time.Duration, error) {
			return fakeFastFsync, nil
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewHybridStoreWithOptions: %v", err)
	}
	defer s.Close()

	result := waitForDataDirProbe(t, s)
	if result.Slow {
		t.Fatalf("probe result = %+v, want Slow=false for a %v fsync against a %v threshold", result, fakeFastFsync, threshold)
	}
}

// TestHybridStore_DataDirProbe_FailureLeavesResultNilNotSlow proves a probe
// that errors out (e.g. permission denied) is treated as "the check didn't
// run", not as "the check ran and found a slow disk" — DataDirProbe stays
// nil rather than reporting a false Slow=true or Slow=false.
func TestHybridStore_DataDirProbe_FailureLeavesResultNilNotSlow(t *testing.T) {
	s, err := NewHybridStoreWithOptions(t.TempDir(), HybridOptions{
		FlushInterval: time.Hour,
		dataDirFsyncProbe: func(dataDir string) (time.Duration, error) {
			return 0, errors.New("permission denied")
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewHybridStoreWithOptions: %v", err)
	}
	defer s.Close()

	// Give the background probe goroutine a moment to run and fail.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if r := s.getDataDirProbeResult(); r != nil {
		t.Fatalf("getDataDirProbeResult() = %+v, want nil after a failed probe", r)
	}
	m := s.DebugMetrics(context.Background(), DebugMetricsOptions{})
	if m.DataDirProbe != nil {
		t.Fatalf("DebugMetrics.DataDirProbe = %+v, want nil after a failed probe", m.DataDirProbe)
	}
}

// TestProbeDataDirFsync_realProbeSucceeds is a light sanity check on the
// real (non-test) probe implementation against a real temp directory —
// distinct from the fake-probe tests above, which exercise the
// slow/fast/failure classification logic without depending on actual disk
// speed.
func TestProbeDataDirFsync_realProbeSucceeds(t *testing.T) {
	dir := t.TempDir()
	elapsed, err := probeDataDirFsync(dir)
	if err != nil {
		t.Fatalf("probeDataDirFsync: %v", err)
	}
	if elapsed < 0 {
		t.Fatalf("probeDataDirFsync elapsed = %v, want >= 0", elapsed)
	}
}
