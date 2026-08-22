package router

import (
	"fmt"
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

// The Detail copy must match the actual mount type: telling a named-volume
// user (native ext4) to "switch to a named volume" is wrong advice — that
// exact regression shipped in alpha.24 and was reported from the field.
func TestCheckDataDirSlowFilesystem_detailMatchesMountClass(t *testing.T) {
	cases := []struct {
		name       string
		probe      state.DataDirProbeResult
		wantSubstr string
		banSubstr  string
	}{
		{
			name:       "shared mount gets named-volume advice",
			probe:      state.DataDirProbeResult{FsyncMillis: 120, Slow: true, FsType: "fuse.grpcfuse", MountClass: "shared"},
			wantSubstr: "named Docker volume",
			banSubstr:  "native filesystem",
		},
		{
			name:       "native mount gets I/O-pressure advice, not bind-mount advice",
			probe:      state.DataDirProbeResult{FsyncMillis: 106, Slow: true, FsType: "ext4", MountClass: "native"},
			wantSubstr: "native filesystem (ext4)",
			banSubstr:  "Use a named Docker volume",
		},
		{
			name:       "unknown mount stays hedged",
			probe:      state.DataDirProbeResult{FsyncMillis: 90, Slow: true},
			wantSubstr: "If the data directory is a Docker Desktop bind mount",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probe := tc.probe
			a := checkDataDirSlowFilesystem(state.DebugMetrics{Mode: "hybrid", DataDirProbe: &probe})
			if a == nil {
				t.Fatal("expected an advisory, got nil")
			}
			if !containsDebugBody(a.Detail, tc.wantSubstr) {
				t.Errorf("detail missing %q: %q", tc.wantSubstr, a.Detail)
			}
			if tc.banSubstr != "" && containsDebugBody(a.Detail, tc.banSubstr) {
				t.Errorf("detail must not contain %q for this mount class: %q", tc.banSubstr, a.Detail)
			}
		})
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
	a := checkMemoryMode(config.StateBackendMemory, config.StateSourceExplicit, true)

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
	// When: the rule evaluates an auto-resolved memory backend in a build that
	// does have SQLite (so hybrid really is a reachable remediation).
	a := checkMemoryMode(config.StateBackendMemory, config.StateSourceAuto, true)

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

// TestCheckMemoryMode_autoWithoutSQLiteNamesWALNotHybrid covers the third
// variant, added because the plain auto wording is wrong in every particular
// for a -tags nosqlite build (the overcast-slim image, the overcastd
// binaries): mounting a volume there changes nothing (config.resolveAutoState
// short-circuits to memory before weighing any signal), and following its
// "set OVERCAST_STATE=hybrid to persist" advice doesn't degrade — it stops
// the daemon from starting outright. The durable backend that does exist in
// every build is wal, so that is what this variant must name.
func TestCheckMemoryMode_autoWithoutSQLiteNamesWALNotHybrid(t *testing.T) {
	// When: the rule evaluates an auto-resolved memory backend in a build
	// compiled without SQLite.
	a := checkMemoryMode(config.StateBackendMemory, config.StateSourceAuto, false)

	// Then: still an info advisory under the same code (memory mode is not an
	// error in any variant), but with its own wording and a docs deep-link.
	if a == nil {
		t.Fatal("expected an advisory, got nil")
	}
	if a.Severity != advisorySeverityInfo {
		t.Errorf("severity = %q, want %q (a build without SQLite is not an error condition)", a.Severity, advisorySeverityInfo)
	}
	if a.Code != advisoryCodeMemoryMode {
		t.Errorf("code = %q, want %q", a.Code, advisoryCodeMemoryMode)
	}
	if a.DocsPath != noSQLiteDocsPath {
		t.Errorf("docsPath = %q, want %q", a.DocsPath, noSQLiteDocsPath)
	}

	// And: it names wal as the remediation, and does not repeat the
	// SQLite-available variant's advice, which would not work in this build.
	if !strings.Contains(a.Detail, "OVERCAST_STATE=wal") {
		t.Errorf("detail = %q, expected it to name OVERCAST_STATE=wal — the one durable backend in this build", a.Detail)
	}
	if strings.Contains(a.Detail, "OVERCAST_STATE=hybrid to persist") {
		t.Errorf("detail = %q, must not tell a no-SQLite build to set OVERCAST_STATE=hybrid — that exits non-zero at startup", a.Detail)
	}
	withSQLite := checkMemoryMode(config.StateBackendMemory, config.StateSourceAuto, true)
	if withSQLite == nil {
		t.Fatal("expected an advisory for the SQLite-available auto case, got nil")
	}
	if a.Title == withSQLite.Title || a.Detail == withSQLite.Detail {
		t.Error("expected distinct wording from the SQLite-available auto variant")
	}
}

// TestCheckMemoryMode_explicitWordingIgnoresSQLiteAvailability pins the
// deliberate scope of the SQLite gate: it only reworks the auto variant.
// OVERCAST_STATE=memory typed explicitly is the same informed choice in every
// build, so its wording must not drift when SQLite is absent.
func TestCheckMemoryMode_explicitWordingIgnoresSQLiteAvailability(t *testing.T) {
	// When: the rule evaluates an explicit memory backend without SQLite.
	a := checkMemoryMode(config.StateBackendMemory, config.StateSourceExplicit, false)

	// Then: the unchanged explicit-mode wording comes back.
	if a == nil {
		t.Fatal("expected an advisory, got nil")
	}
	if a.Title != "Running in memory-only mode" {
		t.Errorf("title = %q, want the explicit-mode wording", a.Title)
	}
	if a.Detail != "OVERCAST_STATE=memory — state won't survive restarts; expected in this mode." {
		t.Errorf("detail = %q, want the explicit-mode wording", a.Detail)
	}
}

func TestCheckMemoryMode_absentForPersistentBackends(t *testing.T) {
	for _, backend := range []config.StateBackend{
		config.StateBackendHybrid,
		config.StateBackendPersistent,
		config.StateBackendWAL,
	} {
		for _, source := range []config.StateSource{config.StateSourceExplicit, config.StateSourceAuto} {
			for _, sqlite := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s/%s/sqlite=%v", backend, source, sqlite), func(t *testing.T) {
					if a := checkMemoryMode(backend, source, sqlite); a != nil {
						t.Fatalf("expected no advisory for backend %q source %q sqlite %v, got %+v", backend, source, sqlite, a)
					}
				})
			}
		}
	}
}

// ---- checkMemoryModeIgnoresExisting ---------------------------------------------

// TestCheckMemoryModeIgnoresExisting_firesOnlyForMemoryWithExistingDatabase
// covers the sharp exception checkMemoryMode itself does not: memory mode
// alone is never remarkable, but memory mode while a database already exists
// in the data directory means real data is being silently left behind.
func TestCheckMemoryModeIgnoresExisting_firesOnlyForMemoryWithExistingDatabase(t *testing.T) {
	a := checkMemoryModeIgnoresExisting(config.StateBackendMemory, true)
	if a == nil {
		t.Fatal("expected an advisory, got nil")
	}
	if a.Severity != advisorySeverityWarning {
		t.Errorf("severity = %q, want %q", a.Severity, advisorySeverityWarning)
	}
	if a.Code != advisoryCodeMemoryModeIgnoresExisting {
		t.Errorf("code = %q, want %q", a.Code, advisoryCodeMemoryModeIgnoresExisting)
	}
	for _, want := range []string{"existing", "OVERCAST_STATE=auto"} {
		if !strings.Contains(a.Detail, want) {
			t.Errorf("detail = %q, expected it to mention %q", a.Detail, want)
		}
	}
}

func TestCheckMemoryModeIgnoresExisting_absentWithoutExistingDatabase(t *testing.T) {
	// The default, healthy fresh-install and CI path: memory mode with no
	// database anywhere yet. Must never fire here — checkMemoryMode alone
	// already covers this case, and it is deliberately not an error.
	if a := checkMemoryModeIgnoresExisting(config.StateBackendMemory, false); a != nil {
		t.Errorf("expected no advisory when there is no existing database, got %+v", a)
	}
}

func TestCheckMemoryModeIgnoresExisting_absentForPersistentBackends(t *testing.T) {
	// An existing database with a persistent backend selected is the normal,
	// intended case (that database is exactly what will be opened) — no
	// mismatch to report regardless of which durable backend it is.
	for _, backend := range []config.StateBackend{
		config.StateBackendHybrid,
		config.StateBackendPersistent,
		config.StateBackendWAL,
	} {
		t.Run(string(backend), func(t *testing.T) {
			if a := checkMemoryModeIgnoresExisting(backend, true); a != nil {
				t.Errorf("expected no advisory for backend %q with an existing database, got %+v", backend, a)
			}
		})
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

// TestComputeAdvisories_threadsSQLiteAvailabilityIntoMemoryMode guards the
// wiring, not the rule: advisoryInput.SQLiteAvailable has to actually reach
// checkMemoryMode. Left unthreaded, the field would sit at its zero value and
// every auto-resolved memory instance — including the overwhelmingly common
// SQLite-capable one — would get the no-SQLite wording, which is the same
// class of wrong-advice bug in the opposite direction.
func TestComputeAdvisories_threadsSQLiteAvailabilityIntoMemoryMode(t *testing.T) {
	for _, tc := range []struct {
		name           string
		sqliteAvail    bool
		wantDocsPath   string
		wantDetailPart string
	}{
		{
			name:           "sqlite build keeps the hybrid remediation",
			sqliteAvail:    true,
			wantDocsPath:   "",
			wantDetailPart: "OVERCAST_STATE=hybrid",
		},
		{
			name:           "nosqlite build gets the wal remediation and a docs link",
			sqliteAvail:    false,
			wantDocsPath:   noSQLiteDocsPath,
			wantDetailPart: "OVERCAST_STATE=wal",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given: an auto-resolved memory backend and nothing else wrong.
			in := advisoryInput{
				StateBackend:    config.StateBackendMemory,
				StateSource:     config.StateSourceAuto,
				SQLiteAvailable: tc.sqliteAvail,
			}

			// When: advisories are computed.
			advisories := computeAdvisories(in)

			// Then: exactly the memory-mode advisory, worded for this build.
			if len(advisories) != 1 {
				t.Fatalf("expected exactly 1 advisory, got %d: %+v", len(advisories), advisories)
			}
			a := advisories[0]
			if a.Code != advisoryCodeMemoryMode {
				t.Fatalf("code = %q, want %q", a.Code, advisoryCodeMemoryMode)
			}
			if a.DocsPath != tc.wantDocsPath {
				t.Errorf("docsPath = %q, want %q", a.DocsPath, tc.wantDocsPath)
			}
			if !strings.Contains(a.Detail, tc.wantDetailPart) {
				t.Errorf("detail = %q, expected it to mention %q", a.Detail, tc.wantDetailPart)
			}
		})
	}
}

// containsDebugBody (defined in debug_test.go) is reused above for substring
// assertions against Advisory.Detail — no need for a second copy of the same
// helper in this file.
