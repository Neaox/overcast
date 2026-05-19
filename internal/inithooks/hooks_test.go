package inithooks

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// writeScript creates an executable .sh file with the given content.
func writeScript(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o755))
	return path
}

func TestRun_alphabeticalOrder(t *testing.T) {
	// Given: a ready.d directory with scripts named out of alphabetical order
	base := t.TempDir()
	readyDir := filepath.Join(base, "ready.d")
	require.NoError(t, os.MkdirAll(readyDir, 0o755))

	outFile := filepath.Join(t.TempDir(), "order.txt")
	writeScript(t, readyDir, "02_second.sh", "#!/bin/sh\necho second >> "+outFile+"\n")
	writeScript(t, readyDir, "01_first.sh", "#!/bin/sh\necho first >> "+outFile+"\n")
	writeScript(t, readyDir, "03_third.sh", "#!/bin/sh\necho third >> "+outFile+"\n")

	runner := NewRunner([]string{base}, nil, 5*time.Second, zaptest.NewLogger(t))

	// When: we run the READY stage
	runner.Run(context.Background(), StageReady)

	// Then: scripts ran in alphabetical order
	data, err := os.ReadFile(outFile)
	require.NoError(t, err)
	assert.Equal(t, "first\nsecond\nthird\n", string(data))
}

func TestRun_subdirectoriesDepthFirst(t *testing.T) {
	// Given: ready.d with a subdirectory — parent scripts first, then subdir
	base := t.TempDir()
	readyDir := filepath.Join(base, "ready.d")
	subDir := filepath.Join(readyDir, "aaa_subdir")
	require.NoError(t, os.MkdirAll(subDir, 0o755))

	outFile := filepath.Join(t.TempDir(), "order.txt")
	writeScript(t, readyDir, "01_parent.sh", "#!/bin/sh\necho parent >> "+outFile+"\n")
	writeScript(t, subDir, "01_child.sh", "#!/bin/sh\necho child >> "+outFile+"\n")
	writeScript(t, readyDir, "zzz_last.sh", "#!/bin/sh\necho last >> "+outFile+"\n")

	runner := NewRunner([]string{base}, nil, 5*time.Second, zaptest.NewLogger(t))

	// When: we run the READY stage
	runner.Run(context.Background(), StageReady)

	// Then: parent script runs first, then subdir, then remaining parent scripts
	// filepath.WalkDir visits in lexical order: 01_parent.sh, aaa_subdir/01_child.sh, zzz_last.sh
	data, err := os.ReadFile(outFile)
	require.NoError(t, err)
	assert.Equal(t, "parent\nchild\nlast\n", string(data))
}

func TestRun_failedScriptDoesNotBlockOthers(t *testing.T) {
	// Given: three scripts where the middle one fails
	base := t.TempDir()
	readyDir := filepath.Join(base, "ready.d")
	require.NoError(t, os.MkdirAll(readyDir, 0o755))

	outFile := filepath.Join(t.TempDir(), "order.txt")
	writeScript(t, readyDir, "01_ok.sh", "#!/bin/sh\necho first >> "+outFile+"\n")
	writeScript(t, readyDir, "02_fail.sh", "#!/bin/sh\nexit 1\n")
	writeScript(t, readyDir, "03_ok.sh", "#!/bin/sh\necho third >> "+outFile+"\n")

	runner := NewRunner([]string{base}, nil, 5*time.Second, zaptest.NewLogger(t))

	// When: we run the READY stage
	runner.Run(context.Background(), StageReady)

	// Then: first and third scripts ran; second is in ERROR state
	data, err := os.ReadFile(outFile)
	require.NoError(t, err)
	assert.Equal(t, "first\nthird\n", string(data))

	status := runner.StageStatus(StageReady)
	assert.True(t, status.Completed)
	require.Len(t, status.Scripts, 3)
	assert.Equal(t, StateSuccessful, status.Scripts[0].State)
	assert.Equal(t, StateError, status.Scripts[1].State)
	assert.Equal(t, StateSuccessful, status.Scripts[2].State)
}

func TestRun_nonShFilesSkipped(t *testing.T) {
	// Given: a ready.d directory with .sh and non-.sh files
	base := t.TempDir()
	readyDir := filepath.Join(base, "ready.d")
	require.NoError(t, os.MkdirAll(readyDir, 0o755))

	outFile := filepath.Join(t.TempDir(), "order.txt")
	writeScript(t, readyDir, "01_run.sh", "#!/bin/sh\necho ran >> "+outFile+"\n")
	writeScript(t, readyDir, "02_skip.txt", "this is not a script\n")
	writeScript(t, readyDir, "03_skip.py", "print('should not run')\n")

	runner := NewRunner([]string{base}, nil, 5*time.Second, zaptest.NewLogger(t))

	// When: we run the READY stage
	runner.Run(context.Background(), StageReady)

	// Then: only the .sh file ran
	data, err := os.ReadFile(outFile)
	require.NoError(t, err)
	assert.Equal(t, "ran\n", string(data))

	status := runner.StageStatus(StageReady)
	require.Len(t, status.Scripts, 1)
}

func TestRun_emptyDirectory(t *testing.T) {
	// Given: a base directory with an empty ready.d
	base := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(base, "ready.d"), 0o755))

	runner := NewRunner([]string{base}, nil, 5*time.Second, zaptest.NewLogger(t))

	// When: we run the READY stage
	runner.Run(context.Background(), StageReady)

	// Then: stage is completed with no scripts
	status := runner.StageStatus(StageReady)
	assert.True(t, status.Completed)
	assert.Empty(t, status.Scripts)
}

func TestRun_missingDirectory(t *testing.T) {
	// Given: a base directory that does not exist
	runner := NewRunner([]string{"/nonexistent/path"}, nil, 5*time.Second, zaptest.NewLogger(t))

	// When: we run the READY stage
	runner.Run(context.Background(), StageReady)

	// Then: stage is completed with no scripts (no error)
	status := runner.StageStatus(StageReady)
	assert.True(t, status.Completed)
	assert.Empty(t, status.Scripts)
}

func TestRun_multipleDirs(t *testing.T) {
	// Given: two base directories, each with ready.d scripts
	base1 := t.TempDir()
	base2 := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(base1, "ready.d"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(base2, "ready.d"), 0o755))

	outFile := filepath.Join(t.TempDir(), "order.txt")
	writeScript(t, filepath.Join(base1, "ready.d"), "01_dir1.sh", "#!/bin/sh\necho dir1 >> "+outFile+"\n")
	writeScript(t, filepath.Join(base2, "ready.d"), "01_dir2.sh", "#!/bin/sh\necho dir2 >> "+outFile+"\n")

	runner := NewRunner([]string{base1, base2}, nil, 5*time.Second, zaptest.NewLogger(t))

	// When: we run the READY stage
	runner.Run(context.Background(), StageReady)

	// Then: scripts from dir1 run first, then dir2
	data, err := os.ReadFile(outFile)
	require.NoError(t, err)
	assert.Equal(t, "dir1\ndir2\n", string(data))
}

func TestRun_envVarsPassed(t *testing.T) {
	// Given: a script that writes an env var to a file
	base := t.TempDir()
	readyDir := filepath.Join(base, "ready.d")
	require.NoError(t, os.MkdirAll(readyDir, 0o755))

	outFile := filepath.Join(t.TempDir(), "env.txt")
	writeScript(t, readyDir, "01_env.sh", "#!/bin/sh\necho $AWS_ENDPOINT_URL >> "+outFile+"\n")

	runner := NewRunner(
		[]string{base},
		[]string{"AWS_ENDPOINT_URL=http://localhost:4566"},
		5*time.Second,
		zaptest.NewLogger(t),
	)

	// When: we run the READY stage
	runner.Run(context.Background(), StageReady)

	// Then: the env var was available to the script
	data, err := os.ReadFile(outFile)
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:4566\n", string(data))
}

func TestDiscover_populatesAllStages(t *testing.T) {
	// Given: scripts in multiple stage directories
	base := t.TempDir()
	for _, stage := range []string{"boot.d", "start.d", "ready.d", "shutdown.d"} {
		dir := filepath.Join(base, stage)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		writeScript(t, dir, "test.sh", "#!/bin/sh\ntrue\n")
	}

	runner := NewRunner([]string{base}, nil, 5*time.Second, zaptest.NewLogger(t))

	// When: we call Discover
	runner.Discover()

	// Then: all stages have scripts in UNKNOWN state
	status := runner.Status()
	assert.Len(t, status.Scripts, 4)
	for _, s := range status.Scripts {
		assert.Equal(t, StateUnknown, s.State)
	}
	for _, stage := range AllStages {
		assert.False(t, status.Completed[stage])
	}
}

func TestStatus_afterRun(t *testing.T) {
	// Given: a ready.d with one script
	base := t.TempDir()
	readyDir := filepath.Join(base, "ready.d")
	require.NoError(t, os.MkdirAll(readyDir, 0o755))
	writeScript(t, readyDir, "setup.sh", "#!/bin/sh\ntrue\n")

	runner := NewRunner([]string{base}, nil, 5*time.Second, zaptest.NewLogger(t))
	runner.Discover()

	// When: we run the READY stage
	runner.Run(context.Background(), StageReady)

	// Then: full status shows READY completed, script SUCCESSFUL
	status := runner.Status()
	assert.True(t, status.Completed[StageReady])
	assert.False(t, status.Completed[StageBoot])

	var readyScripts []ScriptResult
	for _, s := range status.Scripts {
		if s.Stage == StageReady {
			readyScripts = append(readyScripts, s)
		}
	}
	require.Len(t, readyScripts, 1)
	assert.Equal(t, StateSuccessful, readyScripts[0].State)
	assert.Equal(t, "setup.sh", readyScripts[0].Name)
}

func TestParseStage_valid(t *testing.T) {
	tests := []struct {
		input string
		want  Stage
	}{
		{"boot", StageBoot},
		{"BOOT", StageBoot},
		{"Boot", StageBoot},
		{"start", StageStart},
		{"ready", StageReady},
		{"shutdown", StageShutdown},
	}
	for _, tt := range tests {
		stage, ok := ParseStage(tt.input)
		assert.True(t, ok, "ParseStage(%q) should be valid", tt.input)
		assert.Equal(t, tt.want, stage)
	}
}

func TestParseStage_invalid(t *testing.T) {
	_, ok := ParseStage("invalid")
	assert.False(t, ok)
}

func TestRun_timeout(t *testing.T) {
	// Given: a script that sleeps longer than the timeout, using a trap to
	// ensure the sleep process is cleaned up when the shell is killed.
	base := t.TempDir()
	readyDir := filepath.Join(base, "ready.d")
	require.NoError(t, os.MkdirAll(readyDir, 0o755))

	writeScript(t, readyDir, "01_slow.sh", "#!/bin/sh\ntrap 'exit 0' TERM\nsleep 30 &\nwait\n")

	runner := NewRunner([]string{base}, nil, 100*time.Millisecond, zaptest.NewLogger(t))

	// When: we run the READY stage
	runner.Run(context.Background(), StageReady)

	// Then: the script is in ERROR state (timed out)
	status := runner.StageStatus(StageReady)
	assert.True(t, status.Completed)
	require.Len(t, status.Scripts, 1)
	assert.Equal(t, StateError, status.Scripts[0].State)
}
