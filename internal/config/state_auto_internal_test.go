package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveAutoState_allSignalCombinations exhaustively covers every one of
// the 8 possible combinations of the three evidence signals (Mountpoint,
// DataDirExplicit, ExistingDatabase), all with SQLiteAvailable: true — the
// documented precedence is Mountpoint > DataDirExplicit > ExistingDatabase >
// (none, memory). resolveAutoState is a pure function of these booleans, so
// this table is a complete specification of the evidence-weighing behavior —
// no filesystem or environment setup needed. The separate SQLiteAvailable
// gate (a hard short-circuit, not evidence to weigh) is covered by
// TestResolveAutoState_sqliteUnavailableOverridesEveryEvidenceSignal below.
func TestResolveAutoState_allSignalCombinations(t *testing.T) {
	tests := []struct {
		name        string
		sig         autoStateSignals
		wantBackend StateBackend
		wantSignal  string
	}{
		{"none", autoStateSignals{SQLiteAvailable: true}, StateBackendMemory, ""},
		{"mountpoint only", autoStateSignals{SQLiteAvailable: true, Mountpoint: true}, StateBackendHybrid, autoSignalMountpoint},
		{"dataDirExplicit only", autoStateSignals{SQLiteAvailable: true, DataDirExplicit: true}, StateBackendHybrid, autoSignalDataDirExplicit},
		{"existingDatabase only", autoStateSignals{SQLiteAvailable: true, ExistingDatabase: true}, StateBackendHybrid, autoSignalExistingDB},
		{"mountpoint + dataDirExplicit", autoStateSignals{SQLiteAvailable: true, Mountpoint: true, DataDirExplicit: true}, StateBackendHybrid, autoSignalMountpoint},
		{"mountpoint + existingDatabase", autoStateSignals{SQLiteAvailable: true, Mountpoint: true, ExistingDatabase: true}, StateBackendHybrid, autoSignalMountpoint},
		{"dataDirExplicit + existingDatabase", autoStateSignals{SQLiteAvailable: true, DataDirExplicit: true, ExistingDatabase: true}, StateBackendHybrid, autoSignalDataDirExplicit},
		{"all three", autoStateSignals{SQLiteAvailable: true, Mountpoint: true, DataDirExplicit: true, ExistingDatabase: true}, StateBackendHybrid, autoSignalMountpoint},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend, signal, reason := resolveAutoState(tt.sig)
			if backend != tt.wantBackend {
				t.Errorf("backend = %q, want %q", backend, tt.wantBackend)
			}
			if signal != tt.wantSignal {
				t.Errorf("signal = %q, want %q", signal, tt.wantSignal)
			}
			if reason == "" {
				t.Error("reason: expected a non-empty explanation in every case")
			}
		})
	}
}

// TestResolveAutoState_sqliteUnavailableOverridesEveryEvidenceSignal verifies
// the capability gate: in a -tags nosqlite build, hybrid cannot be
// constructed at all (NewHybridStore returns a hard error — see
// internal/state/sqlite_hybrid_nosqlite.go), so resolveAutoState must never
// pick hybrid regardless of how much evidence of persistence intent is
// present. Resolving to hybrid there would crash the process at startup
// (confirmed live: `docker run -v vol:/data overcast-slim` — the `nosqlite`
// build — with no OVERCAST_STATE set previously exited 1 with "hybrid
// store: not compiled with SQLite support" before this gate was added),
// which defeats the entire premise that auto's worst case is a safe,
// always-startable memory fallback.
func TestResolveAutoState_sqliteUnavailableOverridesEveryEvidenceSignal(t *testing.T) {
	tests := []struct {
		name string
		sig  autoStateSignals
	}{
		{"no evidence", autoStateSignals{SQLiteAvailable: false}},
		{"mountpoint", autoStateSignals{SQLiteAvailable: false, Mountpoint: true}},
		{"dataDirExplicit", autoStateSignals{SQLiteAvailable: false, DataDirExplicit: true}},
		{"existingDatabase", autoStateSignals{SQLiteAvailable: false, ExistingDatabase: true}},
		{"all evidence signals", autoStateSignals{SQLiteAvailable: false, Mountpoint: true, DataDirExplicit: true, ExistingDatabase: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend, signal, reason := resolveAutoState(tt.sig)
			if backend != StateBackendMemory {
				t.Errorf("backend = %q, want %q (SQLite unavailable must force memory)", backend, StateBackendMemory)
			}
			if signal != "" {
				t.Errorf("signal = %q, want empty", signal)
			}
			if reason == "" {
				t.Error("reason: expected a non-empty explanation")
			}
		})
	}
}

// TestDetectAutoStateSignals_sqliteAvailableMatchesBuild verifies
// detectAutoStateSignals wires SQLiteAvailable from the sqliteBuildSupported
// build-tag constant rather than leaving it at its zero value (which would
// silently force every auto resolution to memory — the bug this whole gate
// exists to prevent would reappear in the opposite direction).
func TestDetectAutoStateSignals_sqliteAvailableMatchesBuild(t *testing.T) {
	sig := detectAutoStateSignals(t.TempDir(), "", "")
	if sig.SQLiteAvailable != sqliteBuildSupported {
		t.Errorf("SQLiteAvailable = %v, want %v (sqliteBuildSupported)", sig.SQLiteAvailable, sqliteBuildSupported)
	}
}

// TestDetectAutoStateSignals_dataDirExplicit covers the OVERCAST_DATA_DIR_SOURCE
// "image" carve-out: the env var being present is only evidence of explicit
// user intent when it wasn't set by the Docker image's own baked-in default.
func TestDetectAutoStateSignals_dataDirExplicit(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name          string
		dataDirEnvRaw string
		dataDirSource string
		want          bool
	}{
		{"unset", "", "", false},
		{"set, no source marker (native)", dir, "", true},
		{"set, source=image (Docker default)", dir, "image", false},
		{"set, source=something else", dir, "user", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sig := detectAutoStateSignals(dir, tt.dataDirEnvRaw, tt.dataDirSource)
			if sig.DataDirExplicit != tt.want {
				t.Errorf("DataDirExplicit = %v, want %v", sig.DataDirExplicit, tt.want)
			}
		})
	}
}

// TestDetectAutoStateSignals_existingDatabase covers the regression-guard
// signal: an overcast.db or overcast.wal already present in the data
// directory must be detected regardless of the other two signals, so
// existing users (native ~/.overcast/data, or an existing Docker volume) are
// never silently stranded in memory mode by the auto default.
func TestDetectAutoStateSignals_existingDatabase(t *testing.T) {
	t.Run("no database", func(t *testing.T) {
		dir := t.TempDir()
		sig := detectAutoStateSignals(dir, "", "")
		if sig.ExistingDatabase {
			t.Error("ExistingDatabase: expected false for an empty directory")
		}
	})

	t.Run("overcast.db present", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "overcast.db"), []byte{}, 0o644); err != nil {
			t.Fatalf("write overcast.db: %v", err)
		}
		sig := detectAutoStateSignals(dir, "", "")
		if !sig.ExistingDatabase {
			t.Error("ExistingDatabase: expected true when overcast.db exists")
		}
	})

	t.Run("overcast.wal present", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "overcast.wal"), []byte{}, 0o644); err != nil {
			t.Fatalf("write overcast.wal: %v", err)
		}
		sig := detectAutoStateSignals(dir, "", "")
		if !sig.ExistingDatabase {
			t.Error("ExistingDatabase: expected true when overcast.wal exists")
		}
	})

	t.Run("nonexistent directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "does-not-exist")
		sig := detectAutoStateSignals(dir, "", "")
		if sig.ExistingDatabase {
			t.Error("ExistingDatabase: expected false when the directory doesn't exist")
		}
	})
}
