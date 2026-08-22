package main

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/Neaox/overcast/internal/config"
)

func TestWarnIfExistingDatabaseIgnored(t *testing.T) {
	tests := []struct {
		name        string
		state       config.StateBackend
		writeDBFile string // "" = no file, else filename to create in dataDir
		wantWarn    bool
	}{
		{"memory with no existing database — the fresh-install path, must stay silent", config.StateBackendMemory, "", false},
		{"memory with overcast.db present", config.StateBackendMemory, "overcast.db", true},
		{"memory with overcast.wal present", config.StateBackendMemory, "overcast.wal", true},
		{"hybrid with existing database — not memory mode, nothing to warn about", config.StateBackendHybrid, "overcast.db", false},
		{"wal backend with existing database — not memory mode, nothing to warn about", config.StateBackendWAL, "overcast.db", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.writeDBFile != "" {
				if err := os.WriteFile(filepath.Join(dir, tt.writeDBFile), []byte{}, 0o644); err != nil {
					t.Fatalf("seed %s: %v", tt.writeDBFile, err)
				}
			}
			cfg := &config.Config{
				State:       tt.state,
				StateSource: config.StateSourceExplicit,
				DataDir:     dir,
			}
			core, logs := observer.New(zap.WarnLevel)
			logger := zap.New(core)

			warnIfExistingDatabaseIgnored(cfg, logger)

			gotWarn := logs.Len() > 0
			if gotWarn != tt.wantWarn {
				t.Errorf("warned = %v, want %v (log entries: %v)", gotWarn, tt.wantWarn, logs.All())
			}
		})
	}
}

// TestWarnIfExistingDatabaseIgnored_nilSafe covers the defensive nil guards —
// neither a nil cfg nor a nil logger should ever be reachable from runServe,
// but the guard keeps this a no-op rather than a panic if that ever changes.
func TestWarnIfExistingDatabaseIgnored_nilSafe(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	warnIfExistingDatabaseIgnored(nil, logger)
	warnIfExistingDatabaseIgnored(&config.Config{State: config.StateBackendMemory, DataDir: t.TempDir()}, nil)

	if logs.Len() != 0 {
		t.Errorf("expected no log entries, got %v", logs.All())
	}
}
