package main

// preflight_ephemeral.go — "my data was here a minute ago".
//
// # The failure this exists for
//
// docs/plans/deploy-failure-diagnosis.md's W4 names memory-only state as a
// known-costly environment trap: a restart silently discards everything the
// developer thinks is still there. The common case of that — OVERCAST_STATE
// unset and nothing indicating a data directory was ever meant to persist —
// is already handled: config.resolveAutoState lands on memory by design, and
// runServe's "storage mode auto-detected" log line (below in cmd_serve.go)
// already says so, every time, unprompted.
//
// The gap is narrower and sharper: memory mode chosen *while an existing
// Overcast database already sits in the data directory*. That only happens
// two ways — OVERCAST_STATE=memory set explicitly (an env file left over
// from CI, a docker-compose override nobody remembers writing), or a
// -tags nosqlite build's auto resolution short-circuiting to memory
// regardless of evidence (see resolveAutoState's SQLiteAvailable gate) — and
// both are worse than the ordinary "auto landed on memory" case: there is
// real data one directory away that this run will neither read nor update.
// A developer who restarts expecting their state back gets an empty
// emulator and no indication that the data still exists, just not here.
//
// # Why it only ever fires on a matched symptom
//
// HasExistingDatabase costs one or two os.Stat calls and is false for every
// fresh install and every ordinary memory-mode CI run — the two cases this
// must never warn on. It only reports true when a database file is actually
// present, which is the one condition that turns "memory mode" from a
// deliberate or harmless default into a trap.
import (
	"fmt"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/config"
)

// warnIfExistingDatabaseIgnored logs one actionable Warn when cfg resolves to
// memory-only storage while an existing Overcast database already sits in
// cfg.DataDir. Called once at startup, immediately alongside the
// "storage mode auto-detected" log line — see runServe in cmd_serve.go.
//
// Deliberately silent for every other combination: a fresh data directory (no
// database yet) or a non-memory backend both mean there is nothing this run
// is about to silently ignore.
func warnIfExistingDatabaseIgnored(cfg *config.Config, logger *zap.Logger) {
	if cfg == nil || logger == nil {
		return
	}
	if cfg.State != config.StateBackendMemory {
		return
	}
	if !config.HasExistingDatabase(cfg.DataDir) {
		return
	}
	logger.Warn(
		fmt.Sprintf(
			"this run is memory-only, but an existing Overcast database was found in %s — "+
				"it will not be read and will not be updated: writes made now are lost on restart, "+
				"while that database is left exactly as it was. If you expected this run to use it, "+
				"check OVERCAST_STATE (currently %q, source %s) and whether this build has SQLite "+
				"support — set OVERCAST_STATE=auto (or hybrid/wal) to pick it back up.",
			cfg.DataDir, string(cfg.State), string(cfg.StateSource),
		),
		zap.String("data_dir", cfg.DataDir),
		zap.String("state", string(cfg.State)),
		zap.String("stateSource", string(cfg.StateSource)),
	)
}
