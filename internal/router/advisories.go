package router

import (
	"fmt"
	"time"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

// Advisory severities. The web UI (see web/src/features/metrics) renders
// these generically by severity — icon/color only — so adding a value here
// requires a matching UI mapping, unlike adding a new *rule*, which requires
// no UI change at all (see computeAdvisories's doc comment).
const (
	advisorySeverityInfo     = "info"
	advisorySeverityWarning  = "warning"
	advisorySeverityCritical = "critical"
)

// Advisory codes — stable identifiers a caller (or a future test) can key
// off of without parsing Title/Detail text.
const (
	advisoryCodeJournalModeNotWAL       = "journal-mode-not-wal"
	advisoryCodeStoreDegradedMemoryOnly = "store-degraded-memory-only"
	advisoryCodeStoreUnhealthy          = "store-unhealthy"
	advisoryCodeDataDirSlowFilesystem   = "data-dir-slow-filesystem"
	advisoryCodeReadPressureObserved    = "read-pressure-observed"
	advisoryCodeMemoryMode              = "memory-mode"
)

// performanceDocsPath is the docs-browser-relative path (see
// web/src/routes/docs.tsx's `path` search param, and internal/bff/bff.go's
// handleDocsPage, both of which resolve paths relative to the embedded docs/
// directory — cmd/overcast/embed_web.go roots docsFS at "docs") for the
// storage tuning guide. Every advisory whose remediation is "move your data
// dir" or "your storage backend needs attention" points here, per the
// project's docs-reorganization guidance that user-facing tuning content
// stays at docs/performance.md.
const performanceDocsPath = "performance.md"

// dataDirDocsPath deep-links the slow-filesystem advisory straight to the
// relevant section. The fragment is the docs browser's heading slug (see
// web/src/routes/docs.tsx `slug`: lowercase, non-alphanumeric runs → "-")
// for "Data dir placement — avoid host bind mounts on Docker Desktop" in
// docs/performance.md; the UI splits DocsPath on "#" into path + hash.
const dataDirDocsPath = performanceDocsPath + "#data-dir-placement-avoid-host-bind-mounts-on-docker-desktop"

// storageDocsPath is the docs-browser-relative path (see performanceDocsPath
// for how these resolve) for the storage backends guide — the page that
// compares the four backends by durability and explains what OVERCAST_STATE
// accepts.
const storageDocsPath = "storage.md"

// noSQLiteDocsPath deep-links the no-SQLite memory-mode advisory to the
// section spelling out the whole story: which published artifacts are built
// -tags nosqlite, why auto can only ever land on memory there, and the
// volume-plus-OVERCAST_STATE=wal recipe for durability. The fragment is the
// docs browser's heading slug for "Builds without SQLite" — see
// dataDirDocsPath for how that slug is derived.
const noSQLiteDocsPath = storageDocsPath + "#builds-without-sqlite"

// Advisory is one actionable diagnostic surfaced alongside the storage
// diagnostics in GET /_overcast/debug/metrics (see debugMetricsResponse.Advisories).
// The web UI's Metrics & Health page renders these generically — icon/color
// from Severity, then Title/Detail/optional DocsPath link — so adding a
// future rule to computeAdvisories never requires a web UI change.
type Advisory struct {
	// Severity is one of "info", "warning", "critical".
	Severity string `json:"severity"`
	// Code is a stable, machine-readable identifier for this advisory rule
	// (e.g. "journal-mode-not-wal") — safe to key UI behavior or tests off
	// of, unlike Title/Detail, which are free text that may be reworded.
	Code string `json:"code"`
	// Title is a short, human-readable summary (fits in a single UI line).
	Title string `json:"title"`
	// Detail is a longer explanation, including any concrete numbers/values
	// relevant to this specific occurrence (e.g. the live journal mode
	// actually observed, or how many reads were affected).
	Detail string `json:"detail"`
	// DocsPath, when non-empty, is a docs-browser-relative path (see
	// performanceDocsPath) the web UI can open for remediation guidance.
	DocsPath string `json:"docsPath,omitempty"`
}

// advisoryInput bundles exactly the data computeAdvisories needs — all of it
// already computed by the debugMetrics handler for the rest of the payload,
// nothing fetched specially for advisories. Keeping this as a plain struct
// (rather than passing cfg/store directly) keeps every rule function a pure,
// table-testable function of fake data — no real Store or Config required.
type advisoryInput struct {
	// StateBackend is cfg.State, the globally configured storage backend —
	// drives the memory-mode rule. Deliberately the global setting only, not
	// cfg.ServiceStates per-service overrides: a mixed-backend fleet (most of
	// it hybrid, one service forced to memory via
	// OVERCAST_STATE_<SERVICE>=memory) is an intentional, informed choice a
	// per-service override implies, not the "did you mean to do this"
	// signal this rule exists to catch.
	StateBackend config.StateBackend

	// StateSource reports whether StateBackend was chosen explicitly by the
	// user or resolved by the OVERCAST_STATE=auto heuristic — drives which
	// wording variant checkMemoryMode uses when StateBackend is memory. See
	// config.StateSource's doc comment.
	StateSource config.StateSource

	// SQLiteAvailable is config.SQLiteSupported() — false in a -tags nosqlite
	// build (the overcast-slim image and the overcastd binaries). It selects
	// checkMemoryMode's third wording variant, because in such a build every
	// remediation the ordinary auto wording suggests is wrong: mounting a
	// volume changes nothing (config.resolveAutoState short-circuits to memory
	// before weighing any signal) and OVERCAST_STATE=hybrid stops the daemon
	// from starting at all (state.NewHybridStore is stubbed out to return an
	// error — see internal/state/sqlite_hybrid_nosqlite.go).
	SQLiteAvailable bool

	// Stores is one entry per distinct underlying store, exactly as
	// state.DebugMetricsSnapshot returns it — drives every per-store rule
	// (journal-mode-not-wal, store-degraded-memory-only,
	// data-dir-slow-filesystem, read-pressure-observed). A NamespacedStore
	// with mixed backends naturally yields one advisory per affected store,
	// same as the underlying DebugMetrics payload.
	Stores []state.DebugMetrics

	// Health is the aggregated persistent-backend health across every
	// underlying store (state.PersistentHealthSnapshot) — drives
	// store-unhealthy. HasHealth false means no store reports health at all
	// (e.g. every backend is memory-only), matching
	// PersistentHealthSnapshot's own contract exactly.
	Health    state.PersistentHealth
	HasHealth bool
}

// computeAdvisories is the single generator function behind the
// GET /_overcast/debug/metrics payload's `advisories` array. It is a pure function of
// advisoryInput — no I/O, no clock reads beyond formatting a timestamp
// that's already in the input — so every rule is independently unit-tested
// with fake input (see advisories_test.go) rather than requiring a real
// store.
//
// Adding a future rule means adding one more check*(...) *Advisory function
// and one more call below. It must never require a web UI change: the
// Metrics & Health page renders whatever this returns generically, keyed
// only on Severity/Title/Detail/DocsPath.
func computeAdvisories(in advisoryInput) []Advisory {
	var advisories []Advisory
	for _, m := range in.Stores {
		if a := checkJournalModeNotWAL(m); a != nil {
			advisories = append(advisories, *a)
		}
		if a := checkStoreDegraded(m); a != nil {
			advisories = append(advisories, *a)
		}
		if a := checkDataDirSlowFilesystem(m); a != nil {
			advisories = append(advisories, *a)
		}
		if a := checkReadPressure(m); a != nil {
			advisories = append(advisories, *a)
		}
	}
	if a := checkStoreUnhealthy(in.Health, in.HasHealth); a != nil {
		advisories = append(advisories, *a)
	}
	if a := checkMemoryMode(in.StateBackend, in.StateSource, in.SQLiteAvailable); a != nil {
		advisories = append(advisories, *a)
	}
	return advisories
}

// checkJournalModeNotWAL is critical: this project shipped with WAL mode
// silently disabled for its entire history (a driver DSN-spelling bug — see
// hybrid_journalmode_internal_test.go and state.DebugMetrics.JournalMode's
// doc comment), and rollback-journal mode's exclusive commit lock directly
// causes the read starvation storage-pressure-handling item 0 was written to
// fix. An empty JournalMode means the readback hasn't happened yet (still
// starting up) or the store already degraded (see checkStoreDegraded, which
// covers that case on its own) — neither is this rule's concern, so it only
// fires on an actual, confirmed non-"wal" reading.
func checkJournalModeNotWAL(m state.DebugMetrics) *Advisory {
	if m.JournalMode == "" || m.JournalMode == "wal" {
		return nil
	}
	if m.Mode != "hybrid" && m.Mode != "persistent" {
		return nil
	}
	return &Advisory{
		Severity: advisorySeverityCritical,
		Code:     advisoryCodeJournalModeNotWAL,
		Title:    "SQLite is not running in WAL mode",
		Detail: fmt.Sprintf(
			"The live PRAGMA journal_mode readback for the %q backend is %q, not \"wal\". "+
				"Rollback-journal mode takes a brief but real exclusive lock on every commit, which "+
				"can block concurrent readers under write pressure. Restart the emulator; if this "+
				"persists, the SQLite driver DSN may not be applying the journal_mode pragma.",
			m.Mode, m.JournalMode,
		),
		DocsPath: performanceDocsPath,
	}
}

// checkStoreDegraded is critical: once HybridStore.degradeToMemoryOnly has
// run, every write for the rest of the process's life is memory-only and
// silently lost on restart — the single worst outcome this page can report.
func checkStoreDegraded(m state.DebugMetrics) *Advisory {
	if !m.Degraded {
		return nil
	}
	return &Advisory{
		Severity: advisorySeverityCritical,
		Code:     advisoryCodeStoreDegradedMemoryOnly,
		Title:    "Storage degraded to memory-only",
		Detail: fmt.Sprintf(
			"The %q backend permanently fell back to memory-only for this process's lifetime — "+
				"data is no longer being persisted and will be lost on restart. Restart the emulator "+
				"to retry opening the persistent backend; check the startup log for the underlying error.",
			m.Mode,
		),
	}
}

// checkStoreUnhealthy is a warning, not critical: state.PersistentHealth's
// Healthy flag is a transient, self-clearing signal (markPersistentSuccess
// flips it back on the next successful flush — see hybrid.go), unlike
// checkStoreDegraded's permanent, un-recoverable-without-a-restart
// condition. A store can be Healthy=false for a moment under a burst of
// write pressure and recover on its own.
func checkStoreUnhealthy(health state.PersistentHealth, hasHealth bool) *Advisory {
	if !hasHealth || health.Healthy {
		return nil
	}
	detail := "The persistent backend reported a write/flush failure and has not yet recovered."
	if health.LastError != "" {
		detail += fmt.Sprintf(" Last error: %s.", health.LastError)
	}
	if !health.LastErrorAt.IsZero() {
		detail += fmt.Sprintf(" Occurred at %s.", health.LastErrorAt.UTC().Format(time.RFC3339))
	}
	return &Advisory{
		Severity: advisorySeverityWarning,
		Code:     advisoryCodeStoreUnhealthy,
		Title:    "Storage backend reported an error",
		Detail:   detail,
	}
}

// checkDataDirSlowFilesystem surfaces HybridStore's one-time startup fsync
// probe (runDataDirProbe) — the same condition that already logs a WARN at
// startup, now visible in the metrics payload too instead of requiring
// someone to have been watching the log at the right moment.
func checkDataDirSlowFilesystem(m state.DebugMetrics) *Advisory {
	if m.DataDirProbe == nil || !m.DataDirProbe.Slow {
		return nil
	}
	var detail string
	switch m.DataDirProbe.MountClass {
	case "shared":
		detail = fmt.Sprintf(
			"A startup fsync probe against the data directory took %dms (median of 3). The data "+
				"directory is on a Docker Desktop file-sharing mount (%s), which adds latency to "+
				"every flush and read. Use a named Docker volume for the data directory instead.",
			m.DataDirProbe.FsyncMillis, m.DataDirProbe.FsType,
		)
	case "native":
		detail = fmt.Sprintf(
			"A startup fsync probe against the data directory took %dms (median of 3). The data "+
				"directory is already on a native filesystem (%s), so the mount type is not the "+
				"problem — the underlying disk or Docker VM is under I/O pressure. Check host disk "+
				"load, antivirus scanning of the Docker VM disk image, and VM CPU/memory resources. "+
				"The probe runs once at startup, so a transient boot-time stall can also trigger "+
				"this; restart to re-measure.",
			m.DataDirProbe.FsyncMillis, m.DataDirProbe.FsType,
		)
	default:
		detail = fmt.Sprintf(
			"A startup fsync probe against the data directory took %dms (median of 3). If the data "+
				"directory is a Docker Desktop bind mount on macOS/Windows, use a named Docker "+
				"volume instead. If it is already on a native filesystem or named volume, the disk "+
				"or VM is under I/O pressure — check host disk load, antivirus scanning of the "+
				"Docker VM disk image, and VM resources.",
			m.DataDirProbe.FsyncMillis,
		)
	}
	return &Advisory{
		Severity: advisorySeverityWarning,
		Code:     advisoryCodeDataDirSlowFilesystem,
		Title:    "Data directory filesystem is slow",
		Detail:   detail,
		DocsPath: dataDirDocsPath,
	}
}

// checkReadPressure fires on ReadTimeoutCount, deliberately NOT on
// ReadRetryCount alone — not even as an info-level advisory.
//
// Justification: retries are the hybrid SQLite read-retry path (see
// state.ErrStorePressure's doc comment) working exactly as designed —
// individual retries happen routinely under perfectly ordinary, brief SQLite
// busy/locked contention (e.g. a flush landing at the same moment as a
// point read) and resolve within the retry window essentially always. A
// non-zero ReadRetryCount on a healthy, moderately-loaded emulator is
// unremarkable and would make this section noisy on installations that have
// nothing wrong. A timeout, in contrast, means the ENTIRE retry window
// (hybridSQLiteReadRetryTimeout) was exhausted without success — that read
// was actually throttled and surfaced to its caller as a throttling error
// (see state.ErrStorePressure) instead of the data it asked for. That is the
// first user-visible symptom of real, sustained write pressure, which is
// exactly the class of condition an advisory should exist to catch.
func checkReadPressure(m state.DebugMetrics) *Advisory {
	if m.ReadTimeoutCount <= 0 {
		return nil
	}
	return &Advisory{
		Severity: advisorySeverityWarning,
		Code:     advisoryCodeReadPressureObserved,
		Title:    "Storage reads are under write pressure",
		Detail: fmt.Sprintf(
			"%d read(s) exhausted their retry window under sustained SQLite write pressure and were "+
				"throttled instead of served. This means writes are arriving faster than the persistent "+
				"backend can flush. Consider reducing write volume, or moving the data directory to "+
				"faster storage.",
			m.ReadTimeoutCount,
		),
		DocsPath: performanceDocsPath,
	}
}

// checkMemoryMode is informational only in all three variants below — memory
// mode is never itself an error condition, just something worth surfacing so
// nobody is surprised data didn't survive a restart.
//
// Three wordings:
//
//   - Explicit (StateSource == "explicit"): OVERCAST_STATE=memory is a
//     deliberate, common choice for tests and CI — this exists purely so a
//     developer who forgot they set it (or inherited an env file that sets
//     it) isn't surprised the next time they restart.
//   - Auto without SQLite (StateSource == "auto", sqliteAvailable false): a
//     -tags nosqlite build (the overcast-slim image, the overcastd binaries).
//     This variant exists because every remediation the plain auto wording
//     offers is actively wrong here: config.resolveAutoState short-circuits
//     to memory before it weighs the mountpoint/data-dir/existing-database
//     signals, so mounting a volume or setting OVERCAST_DATA_DIR changes
//     nothing, and OVERCAST_STATE=hybrid doesn't degrade — it stops the
//     daemon from starting ("init state backend: hybrid store: not compiled
//     with SQLite support"). The one durable backend compiled into every
//     build is wal (internal/state/wal.go carries no build tag), so that is
//     what this variant names.
//   - Auto (StateSource == "auto"): the OVERCAST_STATE=auto resolver (see
//     config.resolveAutoState) found no evidence of persistence intent — no
//     mounted volume, no explicit data directory, no existing database — and
//     landed on memory by design (e.g. a fresh `docker run` with no volume
//     mounted). Severity stays info (this is working as designed, most
//     visibly for CI), but the wording is the actionable variant: it tells
//     the reader what would change the outcome, since "auto" means nobody
//     deliberately typed OVERCAST_STATE=memory to get here.
func checkMemoryMode(backend config.StateBackend, source config.StateSource, sqliteAvailable bool) *Advisory {
	if backend != config.StateBackendMemory {
		return nil
	}
	if source == config.StateSourceAuto && !sqliteAvailable {
		return &Advisory{
			Severity: advisorySeverityInfo,
			Code:     advisoryCodeMemoryMode,
			Title:    "Running in memory-only mode (this build has no SQLite)",
			Detail: "This build was compiled without SQLite, so the hybrid and persistent backends " +
				"don't exist in it — OVERCAST_STATE=auto resolves to memory no matter what is " +
				"mounted at the data directory, and state won't survive restarts. Mounting a volume " +
				"or setting OVERCAST_DATA_DIR alone will not change this, and OVERCAST_STATE=hybrid " +
				"will stop the emulator from starting. Set OVERCAST_STATE=wal — the durable backend " +
				"compiled into every build — with a volume mounted at the data directory, or use the " +
				"full overcast image or overcast binary if you need hybrid.",
			DocsPath: noSQLiteDocsPath,
		}
	}
	if source == config.StateSourceAuto {
		return &Advisory{
			Severity: advisorySeverityInfo,
			Code:     advisoryCodeMemoryMode,
			Title:    "Running in memory-only mode (auto-detected)",
			Detail: "OVERCAST_STATE was not set and no volume is mounted at the data directory — " +
				"state won't survive restarts. Mount a volume there, set OVERCAST_DATA_DIR, or set " +
				"OVERCAST_STATE=hybrid to persist.",
		}
	}
	return &Advisory{
		Severity: advisorySeverityInfo,
		Code:     advisoryCodeMemoryMode,
		Title:    "Running in memory-only mode",
		Detail:   "OVERCAST_STATE=memory — state won't survive restarts; expected in this mode.",
	}
}
