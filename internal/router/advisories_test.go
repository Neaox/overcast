package router

import (
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

// ---- journal-mode-not-wal ---------------------------------------------------

func TestCheckJournalModeNotWAL_firesWhenLiveModeIsNotWAL(t *testing.T) {
	// Given: a hybrid store whose live journal_mode readback is "delete"
	// (the historical bug this rule exists to catch — see
	// hybrid_journalmode_internal_test.go).
	m := state.DebugMetrics{Mode: "hybrid", JournalMode: "delete"}

	// When: the rule evaluates it.
	a := checkJournalModeNotWAL(m)

	// Then: a critical advisory fires, naming the actual observed mode.
	if a == nil {
		t.Fatal("expected an advisory, got nil")
	}
	if a.Severity != advisorySeverityCritical {
		t.Errorf("severity = %q, want %q", a.Severity, advisorySeverityCritical)
	}
	if a.Code != advisoryCodeJournalModeNotWAL {
		t.Errorf("code = %q, want %q", a.Code, advisoryCodeJournalModeNotWAL)
	}
	if a.DocsPath == "" {
		t.Error("expected a docs path pointing at the performance tuning guide")
	}
}

func TestCheckJournalModeNotWAL_absentWhenModeIsWAL(t *testing.T) {
	// Given: a healthy store correctly running in WAL mode.
	m := state.DebugMetrics{Mode: "hybrid", JournalMode: "wal"}

	// When/Then: no advisory fires.
	if a := checkJournalModeNotWAL(m); a != nil {
		t.Fatalf("expected no advisory for wal mode, got %+v", a)
	}
}

func TestCheckJournalModeNotWAL_absentWhenModeUnknown(t *testing.T) {
	// Given: a store whose journal mode readback hasn't happened yet (still
	// starting up) or the store already degraded — either way, an empty
	// JournalMode is not this rule's concern.
	m := state.DebugMetrics{Mode: "hybrid", JournalMode: ""}

	// When/Then: no advisory fires (an empty readback must not be treated as
	// "confirmed not wal").
	if a := checkJournalModeNotWAL(m); a != nil {
		t.Fatalf("expected no advisory for an empty (not-yet-known) journal mode, got %+v", a)
	}
}

func TestCheckJournalModeNotWAL_absentForNonSQLiteBackedMode(t *testing.T) {
	// Given: a DebugMetrics entry whose Mode isn't a real SQLite-backed
	// backend (defensive: JournalMode should never be set for these, but the
	// rule must not misfire even if it somehow were).
	m := state.DebugMetrics{Mode: "memory", JournalMode: "delete"}

	// When/Then: no advisory fires.
	if a := checkJournalModeNotWAL(m); a != nil {
		t.Fatalf("expected no advisory for a non-SQLite-backed mode, got %+v", a)
	}
}

// ---- store-degraded-memory-only ---------------------------------------------

func TestCheckStoreDegraded_firesWhenDegraded(t *testing.T) {
	// Given: a store that has permanently fallen back to memory-only.
	m := state.DebugMetrics{Mode: "hybrid", Degraded: true}

	// When: the rule evaluates it.
	a := checkStoreDegraded(m)

	// Then: a critical advisory fires.
	if a == nil {
		t.Fatal("expected an advisory, got nil")
	}
	if a.Severity != advisorySeverityCritical {
		t.Errorf("severity = %q, want %q", a.Severity, advisorySeverityCritical)
	}
	if a.Code != advisoryCodeStoreDegradedMemoryOnly {
		t.Errorf("code = %q, want %q", a.Code, advisoryCodeStoreDegradedMemoryOnly)
	}
}

func TestCheckStoreDegraded_absentWhenHealthy(t *testing.T) {
	// Given: a store that never degraded.
	m := state.DebugMetrics{Mode: "hybrid", Degraded: false}

	// When/Then: no advisory fires.
	if a := checkStoreDegraded(m); a != nil {
		t.Fatalf("expected no advisory, got %+v", a)
	}
}

// ---- store-unhealthy ---------------------------------------------------------

func TestCheckStoreUnhealthy_firesWithLastErrorAndTimestamp(t *testing.T) {
	// Given: an aggregated health snapshot reporting a transient failure.
	errAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	health := state.PersistentHealth{
		Mode:        "hybrid",
		Healthy:     false,
		LastError:   "disk full",
		LastErrorAt: errAt,
	}

	// When: the rule evaluates it.
	a := checkStoreUnhealthy(health, true)

	// Then: a warning advisory fires, including the last error and when it happened.
	if a == nil {
		t.Fatal("expected an advisory, got nil")
	}
	if a.Severity != advisorySeverityWarning {
		t.Errorf("severity = %q, want %q", a.Severity, advisorySeverityWarning)
	}
	if a.Code != advisoryCodeStoreUnhealthy {
		t.Errorf("code = %q, want %q", a.Code, advisoryCodeStoreUnhealthy)
	}
	if !containsDebugBody(a.Detail, "disk full") {
		t.Errorf("expected detail to include the last error, got %q", a.Detail)
	}
	if !containsDebugBody(a.Detail, "2026-01-02T03:04:05Z") {
		t.Errorf("expected detail to include the error timestamp, got %q", a.Detail)
	}
}

func TestCheckStoreUnhealthy_absentWhenHealthy(t *testing.T) {
	// Given: a healthy aggregated snapshot.
	health := state.PersistentHealth{Mode: "hybrid", Healthy: true}

	// When/Then: no advisory fires.
	if a := checkStoreUnhealthy(health, true); a != nil {
		t.Fatalf("expected no advisory, got %+v", a)
	}
}

func TestCheckStoreUnhealthy_absentWhenNoStoreReportsHealth(t *testing.T) {
	// Given: no store implements PersistentHealthReporter at all (e.g. every
	// backend is memory-only) — hasHealth is false, and the zero-value health
	// (Healthy: false) must not be misread as "the store is unhealthy".
	health := state.PersistentHealth{}

	// When/Then: no advisory fires.
	if a := checkStoreUnhealthy(health, false); a != nil {
		t.Fatalf("expected no advisory when hasHealth is false, got %+v", a)
	}
}

// ---- data-dir-slow-filesystem -------------------------------------------------

func TestCheckDataDirSlowFilesystem_firesWhenProbeIsSlow(t *testing.T) {
	// Given: a store whose startup fsync probe found a slow data directory.
	m := state.DebugMetrics{
		Mode:         "hybrid",
		DataDirProbe: &state.DataDirProbeResult{FsyncMillis: 120, Slow: true},
	}

	// When: the rule evaluates it.
	a := checkDataDirSlowFilesystem(m)

	// Then: a warning advisory fires with a docs link.
	if a == nil {
		t.Fatal("expected an advisory, got nil")
	}
	if a.Severity != advisorySeverityWarning {
		t.Errorf("severity = %q, want %q", a.Severity, advisorySeverityWarning)
	}
	if a.Code != advisoryCodeDataDirSlowFilesystem {
		t.Errorf("code = %q, want %q", a.Code, advisoryCodeDataDirSlowFilesystem)
	}
	if a.DocsPath == "" {
		t.Error("expected a docs path pointing at the performance tuning guide")
	}
	if !containsDebugBody(a.Detail, "120ms") {
		t.Errorf("expected detail to include the observed fsync latency, got %q", a.Detail)
	}
}

func TestCheckDataDirSlowFilesystem_absentWhenProbeIsFast(t *testing.T) {
	// Given: a store whose probe found a normal-speed filesystem.
	m := state.DebugMetrics{
		Mode:         "hybrid",
		DataDirProbe: &state.DataDirProbeResult{FsyncMillis: 2, Slow: false},
	}

	// When/Then: no advisory fires.
	if a := checkDataDirSlowFilesystem(m); a != nil {
		t.Fatalf("expected no advisory, got %+v", a)
	}
}

func TestCheckDataDirSlowFilesystem_absentWhenProbeNeverRan(t *testing.T) {
	// Given: a store whose probe hasn't completed (or failed to run) — nil,
	// not a zero-value struct.
	m := state.DebugMetrics{Mode: "hybrid", DataDirProbe: nil}

	// When/Then: no advisory fires — an absent probe is not the same as a
	// confirmed-fast one, but it's also not evidence of a problem.
	if a := checkDataDirSlowFilesystem(m); a != nil {
		t.Fatalf("expected no advisory, got %+v", a)
	}
}

// ---- read-pressure-observed ---------------------------------------------------

func TestCheckReadPressure_firesOnTimeouts(t *testing.T) {
	// Given: a store that had to throttle reads after exhausting their
	// retry window under sustained write pressure.
	m := state.DebugMetrics{Mode: "hybrid", ReadTimeoutCount: 3}

	// When: the rule evaluates it.
	a := checkReadPressure(m)

	// Then: a warning advisory fires, naming the affected read count.
	if a == nil {
		t.Fatal("expected an advisory, got nil")
	}
	if a.Severity != advisorySeverityWarning {
		t.Errorf("severity = %q, want %q", a.Severity, advisorySeverityWarning)
	}
	if a.Code != advisoryCodeReadPressureObserved {
		t.Errorf("code = %q, want %q", a.Code, advisoryCodeReadPressureObserved)
	}
	if !containsDebugBody(a.Detail, "3 read") {
		t.Errorf("expected detail to include the timeout count, got %q", a.Detail)
	}
}

// TestCheckReadPressure_absentOnRetriesAlone is the deliberate
// retry-vs-timeout decision this rule is built around: individual retries
// are the read-retry path succeeding as designed under ordinary, brief
// SQLite busy/locked contention — common on a healthy, moderately-loaded
// emulator and not, on their own, evidence of a problem worth surfacing (see
// checkReadPressure's doc comment for the full justification). Only a
// timeout — the retry window fully exhausted — is the actionable signal.
func TestCheckReadPressure_absentOnRetriesAlone(t *testing.T) {
	// Given: a store with many retries but zero timeouts — every one of
	// those retries eventually succeeded within budget.
	m := state.DebugMetrics{Mode: "hybrid", ReadRetryCount: 500, ReadTimeoutCount: 0}

	// When/Then: no advisory fires, not even at info level.
	if a := checkReadPressure(m); a != nil {
		t.Fatalf("expected no advisory for retries alone, got %+v", a)
	}
}

// ---- memory-mode ---------------------------------------------------------------

func TestCheckMemoryMode_firesWhenBackendIsMemory(t *testing.T) {
	// When: the rule evaluates an explicitly-set OVERCAST_STATE=memory.
	a := checkMemoryMode(config.StateBackendMemory, config.StateSourceExplicit)

	// Then: an info advisory fires with the explicit-mode wording.
	if a == nil {
		t.Fatal("expected an advisory, got nil")
	}
	if a.Severity != advisorySeverityInfo {
		t.Errorf("severity = %q, want %q", a.Severity, advisorySeverityInfo)
	}
	if a.Code != advisoryCodeMemoryMode {
		t.Errorf("code = %q, want %q", a.Code, advisoryCodeMemoryMode)
	}
	if a.Title != "Running in memory-only mode" {
		t.Errorf("title = %q, want the explicit-mode wording", a.Title)
	}
	if a.Detail != "OVERCAST_STATE=memory — state won't survive restarts; expected in this mode." {
		t.Errorf("detail = %q, want the explicit-mode wording", a.Detail)
	}
}

// TestCheckMemoryMode_actionableWhenAutoResolvedToMemory covers the variant
// introduced for OVERCAST_STATE=auto (also the default when unset): when the
// auto resolver lands on memory because it found no evidence of persistence
// intent, the advisory must use the actionable wording — nobody deliberately
// typed OVERCAST_STATE=memory to get here, so the message should say what to
// do about it instead of just describing the current mode.
func TestCheckMemoryMode_actionableWhenAutoResolvedToMemory(t *testing.T) {
	// When: the rule evaluates an auto-resolved memory backend.
	a := checkMemoryMode(config.StateBackendMemory, config.StateSourceAuto)

	// Then: severity stays info, but Title/Detail switch to the actionable
	// variant naming the remediation options.
	if a == nil {
		t.Fatal("expected an advisory, got nil")
	}
	if a.Severity != advisorySeverityInfo {
		t.Errorf("severity = %q, want %q (auto-resolved memory is working as designed)", a.Severity, advisorySeverityInfo)
	}
	if a.Code != advisoryCodeMemoryMode {
		t.Errorf("code = %q, want %q", a.Code, advisoryCodeMemoryMode)
	}
	if a.Title == "Running in memory-only mode" {
		t.Error("title: expected the actionable auto-detected variant, got the explicit-mode wording")
	}
	for _, want := range []string{"volume", "OVERCAST_DATA_DIR", "OVERCAST_STATE"} {
		if !strings.Contains(a.Detail, want) {
			t.Errorf("detail = %q, expected it to mention %q", a.Detail, want)
		}
	}
}

func TestCheckMemoryMode_absentForPersistentBackends(t *testing.T) {
	for _, backend := range []config.StateBackend{
		config.StateBackendHybrid,
		config.StateBackendPersistent,
		config.StateBackendWAL,
	} {
		for _, source := range []config.StateSource{config.StateSourceExplicit, config.StateSourceAuto} {
			t.Run(string(backend)+"/"+string(source), func(t *testing.T) {
				if a := checkMemoryMode(backend, source); a != nil {
					t.Fatalf("expected no advisory for backend %q source %q, got %+v", backend, source, a)
				}
			})
		}
	}
}

// ---- computeAdvisories aggregation ---------------------------------------------

func TestComputeAdvisories_emptyWhenNothingWrong(t *testing.T) {
	// Given: a fully healthy hybrid store and no memory-mode override.
	in := advisoryInput{
		StateBackend: config.StateBackendHybrid,
		Stores: []state.DebugMetrics{
			{Mode: "hybrid", JournalMode: "wal", Degraded: false},
		},
		Health:    state.PersistentHealth{Mode: "hybrid", Healthy: true},
		HasHealth: true,
	}

	// When: advisories are computed.
	advisories := computeAdvisories(in)

	// Then: the list is empty (quiet, not merely non-nil — the web UI's
	// empty state distinguishes "no data yet" from "definitely nothing to
	// report", so this asserts len==0 rather than just "not panicking").
	if len(advisories) != 0 {
		t.Fatalf("expected no advisories, got %+v", advisories)
	}
}

func TestComputeAdvisories_combinesRulesAcrossStoresAndGlobalState(t *testing.T) {
	// Given: a NamespacedStore-shaped input with one problem per source —
	// one store with a bad journal mode, one degraded, plus an unhealthy
	// aggregate and a global memory-mode override.
	in := advisoryInput{
		StateBackend: config.StateBackendMemory,
		Stores: []state.DebugMetrics{
			{Mode: "hybrid", JournalMode: "delete"},
			{Mode: "hybrid", Degraded: true},
		},
		Health:    state.PersistentHealth{Healthy: false, LastError: "boom"},
		HasHealth: true,
	}

	// When: advisories are computed.
	advisories := computeAdvisories(in)

	// Then: every independent rule that matched contributes its own entry —
	// journal-mode-not-wal, store-degraded-memory-only, store-unhealthy, and
	// memory-mode (4 total; none of these rules overlap in what they detect).
	codes := make(map[string]bool, len(advisories))
	for _, a := range advisories {
		codes[a.Code] = true
	}
	for _, want := range []string{
		advisoryCodeJournalModeNotWAL,
		advisoryCodeStoreDegradedMemoryOnly,
		advisoryCodeStoreUnhealthy,
		advisoryCodeMemoryMode,
	} {
		if !codes[want] {
			t.Errorf("expected advisory code %q in %+v", want, advisories)
		}
	}
	if len(advisories) != 4 {
		t.Fatalf("expected exactly 4 advisories, got %d: %+v", len(advisories), advisories)
	}
}

// containsDebugBody (defined in debug_test.go) is reused above for substring
// assertions against Advisory.Detail — no need for a second copy of the same
// helper in this file.
