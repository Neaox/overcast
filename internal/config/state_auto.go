package config

import (
	"os"
	"path/filepath"
)

// StateSource identifies whether the effective storage backend (Config.State)
// was chosen explicitly by the user (OVERCAST_STATE set to a concrete
// backend name) or resolved by the OVERCAST_STATE=auto heuristic (also the
// default when OVERCAST_STATE is unset — see resolveAutoState).
//
// This is internal provenance for logging and diagnostics (the startup log
// line, the memory-mode advisory, and /_overcast/health's storage.configured field) —
// not itself a user-facing configuration knob.
type StateSource string

const (
	// StateSourceExplicit means OVERCAST_STATE was set to a concrete backend
	// name (memory, hybrid, persistent, wal, or the sqlite alias).
	StateSourceExplicit StateSource = "explicit"

	// StateSourceAuto means OVERCAST_STATE was unset or set to "auto", and
	// Config.State was resolved by resolveAutoState.
	StateSourceAuto StateSource = "auto"
)

// Auto-detection signal codes — stable identifiers for which
// resolveAutoState rule fired, safe to key tests or logs off of. Empty means
// no signal fired (the resolved backend is memory).
const (
	autoSignalMountpoint      = "mountpoint"
	autoSignalDataDirExplicit = "explicit-data-dir"
	autoSignalExistingDB      = "existing-database"
)

// autoStateSignals bundles the three pieces of evidence resolveAutoState
// weighs when OVERCAST_STATE is "auto" (or unset). Keeping this as a plain
// struct — rather than passing paths/env directly — keeps resolveAutoState a
// pure, table-testable function of fake data; the filesystem/env probing
// that produces real values lives in detectAutoStateSignals below.
type autoStateSignals struct {
	// Mountpoint is true when OVERCAST_DATA_DIR is a filesystem mountpoint
	// (its device differs from its parent directory's) — the signature of a
	// Docker named volume or bind mount. See isMountpoint.
	Mountpoint bool

	// DataDirExplicit is true when OVERCAST_DATA_DIR was set by something
	// other than the Docker image's own baked-in ENV default. The image sets
	// both OVERCAST_DATA_DIR=/data and OVERCAST_DATA_DIR_SOURCE=image; a
	// user (native, or Docker with OVERCAST_DATA_DIR overridden and
	// OVERCAST_DATA_DIR_SOURCE cleared) is the only other way this env var is
	// non-empty, so its presence there is treated as deliberate intent to
	// persist to that directory.
	DataDirExplicit bool

	// ExistingDatabase is true when an overcast.db (or overcast.wal) file
	// already exists in the resolved data directory — someone persisted
	// state there before. This is the regression guard: it ensures existing
	// native users (db at ~/.overcast/data) and existing Docker volume users
	// are never silently stranded in memory mode by this change, even if
	// neither of the other two signals fires for them.
	ExistingDatabase bool

	// SQLiteAvailable is false in a -tags nosqlite build (see
	// sqlite_support.go), where the hybrid backend cannot be constructed at
	// all — NewHybridStore returns a hard error. This is a capability gate,
	// not evidence of intent like the other three fields: when false, it
	// short-circuits resolveAutoState straight to memory regardless of what
	// the other signals say, because resolving to hybrid here would crash
	// the process at startup instead of degrading gracefully.
	SQLiteAvailable bool
}

// resolveAutoState decides the storage backend for OVERCAST_STATE=auto (the
// default when OVERCAST_STATE is unset). It resolves to StateBackendHybrid
// when there is evidence the user wants persistence, else
// StateBackendMemory — persisting into an ephemeral container layer (or a
// native run nobody will look at again) is pointless, so memory is the safe,
// fast default absent any such evidence.
//
// SQLiteAvailable is checked first and, when false, short-circuits straight
// to memory — it's a hard capability gate, not evidence to weigh. The
// remaining three evidence signals are then checked in the order documented
// on autoStateSignals' fields; the first one that fires determines both the
// resolved backend and the returned signal code. signal is empty only when
// the resolved backend is memory (whether because no evidence was found, or
// because SQLite is unavailable); reason is always populated for logging.
func resolveAutoState(sig autoStateSignals) (backend StateBackend, signal string, reason string) {
	switch {
	case !sig.SQLiteAvailable:
		return StateBackendMemory, "",
			"hybrid requires SQLite support, which this build excludes (-tags nosqlite); falling back to memory"
	case sig.Mountpoint:
		return StateBackendHybrid, autoSignalMountpoint,
			"the data directory is a mounted volume or bind mount"
	case sig.DataDirExplicit:
		return StateBackendHybrid, autoSignalDataDirExplicit,
			"OVERCAST_DATA_DIR was explicitly configured"
	case sig.ExistingDatabase:
		return StateBackendHybrid, autoSignalExistingDB,
			"an existing overcast database was found in the data directory"
	default:
		return StateBackendMemory, "",
			"no persistence signal found (no mounted volume, no explicit data directory, no existing database)"
	}
}

// detectAutoStateSignals gathers the real, filesystem/env-backed evidence
// resolveAutoState needs, for the given resolved data directory.
// dataDirEnvRaw is the literal (undefaulted) value of OVERCAST_DATA_DIR, and
// dataDirSource is the literal value of OVERCAST_DATA_DIR_SOURCE — both read
// by the caller (Load) before defaulting, since defaulting itself would
// erase the "was this actually set" information this function needs.
func detectAutoStateSignals(dataDir, dataDirEnvRaw, dataDirSource string) autoStateSignals {
	return autoStateSignals{
		Mountpoint:       isMountpoint(dataDir),
		DataDirExplicit:  dataDirEnvRaw != "" && !isDockerImage(dataDirSource),
		ExistingDatabase: HasExistingDatabase(dataDir),
		SQLiteAvailable:  sqliteBuildSupported,
	}
}

// isDockerImage reports whether dataDirSource — the literal value of
// OVERCAST_DATA_DIR_SOURCE — carries the marker the Dockerfile bakes in
// alongside its own OVERCAST_DATA_DIR=/data default (see the ENV block in
// Dockerfile). This is the same signal storage-mode auto-detection above
// uses to tell "the image's own untouched default" apart from "a user
// actually configured this" (see DataDirExplicit); Load's native-vs-
// containerised bind-address default (#761) reuses it rather than
// duplicating the heuristic, so the two decisions can never disagree about
// what "containerised" means.
func isDockerImage(dataDirSource string) bool {
	return dataDirSource == "image"
}

// HasExistingDatabase reports whether a persistent-backend database file
// already exists in dataDir — overcast.db (hybrid/persistent backends) or
// overcast.wal (the WAL backend's append log). See internal/state/sqlite.go
// and internal/state/wal.go for the filenames this must stay in sync with.
//
// Exported (beyond resolveAutoState's own use of it as an auto-detection
// signal) so a caller can ask the same question *after* OVERCAST_STATE has
// already been resolved to something other than auto — see
// cmd/overcast's ephemeral-state preflight and
// internal/router/advisories.go's checkMemoryModeIgnoresExistingData, both of
// which warn when memory mode (explicit, or auto in a -tags nosqlite build)
// is about to silently ignore a database that already has data in it.
func HasExistingDatabase(dataDir string) bool {
	for _, name := range []string{"overcast.db", "overcast.wal"} {
		if _, err := os.Stat(filepath.Join(dataDir, name)); err == nil {
			return true
		}
	}
	return false
}
