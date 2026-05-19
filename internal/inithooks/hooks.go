// Package inithooks discovers and executes user-provided shell scripts at
// well-known lifecycle stages, compatible with LocalStack's init hook system.
//
// Scripts are placed in stage subdirectories (boot.d/, start.d/, ready.d/,
// shutdown.d/) under one or more base directories (e.g. /etc/localstack/init,
// /etc/overcast/init). They are executed in alphabetical order; subdirectories
// are traversed depth-first. A failing script does not block subsequent ones.
package inithooks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Stage is a lifecycle phase where init hooks can run.
type Stage string

const (
	StageBoot     Stage = "BOOT"
	StageStart    Stage = "START"
	StageReady    Stage = "READY"
	StageShutdown Stage = "SHUTDOWN"
)

// AllStages lists every stage in lifecycle order.
var AllStages = []Stage{StageBoot, StageStart, StageReady, StageShutdown}

// ScriptState tracks the execution state of a single init script.
type ScriptState string

const (
	StateUnknown    ScriptState = "UNKNOWN"
	StateRunning    ScriptState = "RUNNING"
	StateSuccessful ScriptState = "SUCCESSFUL"
	StateError      ScriptState = "ERROR"
)

// ScriptResult holds the state and metadata of a single init script.
type ScriptResult struct {
	Stage Stage       `json:"stage"`
	Name  string      `json:"name"`
	State ScriptState `json:"state"`
}

// Runner discovers and executes init hook scripts across configured directories.
type Runner struct {
	dirs    []string
	env     []string
	timeout time.Duration
	logger  *zap.Logger

	mu        sync.RWMutex
	scripts   []ScriptResult // all discovered scripts, in order
	completed map[Stage]bool // whether each stage has finished running
}

// NewRunner creates a Runner that will look for hook scripts in the given base
// directories. Each directory is expected to contain stage subdirectories
// (boot.d/, start.d/, ready.d/, shutdown.d/). The env slice is appended to the
// current process environment when executing scripts.
func NewRunner(dirs []string, env []string, timeout time.Duration, logger *zap.Logger) *Runner {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Runner{
		dirs:      dirs,
		env:       env,
		timeout:   timeout,
		logger:    logger,
		completed: make(map[Stage]bool),
	}
}

// Discover scans all configured directories for hook scripts across all stages
// and populates the initial script list with UNKNOWN state. This should be
// called once after construction so the status endpoint can report discovered
// scripts before they run.
func (r *Runner) Discover() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, stage := range AllStages {
		scripts := r.findScripts(stage)
		for _, path := range scripts {
			r.scripts = append(r.scripts, ScriptResult{
				Stage: stage,
				Name:  filepath.Base(path),
				State: StateUnknown,
			})
		}
	}

	if len(r.scripts) > 0 {
		r.logger.Info("init scripts discovered", zap.Int("count", len(r.scripts)))
	}
}

// Run executes all hook scripts for the given stage synchronously. Scripts are
// run in alphabetical order with subdirectories traversed depth-first. If a
// script fails, the error is logged but execution continues with the next script.
func (r *Runner) Run(ctx context.Context, stage Stage) {
	scripts := r.findScripts(stage)

	if len(scripts) == 0 {
		r.mu.Lock()
		r.completed[stage] = true
		r.mu.Unlock()
		return
	}

	r.logger.Info("running init hooks",
		zap.String("stage", string(stage)),
		zap.Int("scripts", len(scripts)),
	)

	for _, path := range scripts {
		name := filepath.Base(path)
		r.setScriptState(stage, name, StateRunning)

		err := r.execScript(ctx, path)
		if err != nil {
			r.logger.Warn("init hook failed",
				zap.String("stage", string(stage)),
				zap.String("script", path),
				zap.Error(err),
			)
			r.setScriptState(stage, name, StateError)
		} else {
			r.logger.Info("init hook completed",
				zap.String("stage", string(stage)),
				zap.String("script", path),
			)
			r.setScriptState(stage, name, StateSuccessful)
		}
	}

	r.mu.Lock()
	r.completed[stage] = true
	r.mu.Unlock()
}

// Status returns the current state of all discovered scripts and stage
// completion flags. This is the payload for GET /_overcast/init.
func (r *Runner) Status() InitStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()

	completed := make(map[Stage]bool, len(AllStages))
	for _, s := range AllStages {
		completed[s] = r.completed[s]
	}

	scripts := make([]ScriptResult, len(r.scripts))
	copy(scripts, r.scripts)

	return InitStatus{
		Completed: completed,
		Scripts:   scripts,
	}
}

// StageStatus returns the completion flag and scripts for a single stage.
// This is the payload for GET /_overcast/init/{stage}.
func (r *Runner) StageStatus(stage Stage) StageInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var scripts []ScriptResult
	for _, s := range r.scripts {
		if s.Stage == stage {
			scripts = append(scripts, s)
		}
	}

	return StageInfo{
		Completed: r.completed[stage],
		Scripts:   scripts,
	}
}

// findScripts returns all .sh files for the given stage across all configured
// directories, sorted alphabetically with subdirectories traversed depth-first.
func (r *Runner) findScripts(stage Stage) []string {
	dirName := strings.ToLower(string(stage)) + ".d"
	var all []string

	for _, base := range r.dirs {
		stageDir := filepath.Join(base, dirName)
		info, err := os.Stat(stageDir)
		if err != nil || !info.IsDir() {
			continue
		}

		var scripts []string
		if err := filepath.WalkDir(stageDir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // skip errors, continue walking
			}
			if d.IsDir() {
				return nil
			}
			if strings.HasSuffix(d.Name(), ".sh") {
				scripts = append(scripts, path)
			}
			return nil
		}); err != nil {
			continue
		}

		// Sort within each base dir to ensure alphabetical order.
		// filepath.WalkDir already visits in lexical order, but sort
		// explicitly to guarantee the contract.
		sort.Strings(scripts)
		all = append(all, scripts...)
	}

	return all
}

// execScript runs a single shell script with a per-script timeout.
// The platform-specific part (process group management, kill signal) is
// implemented in hooks_unix.go and hooks_windows.go.
func (r *Runner) execScript(ctx context.Context, path string) error {
	scriptCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	cmd := buildScriptCmd(scriptCtx, path)
	cmd.Env = append(os.Environ(), r.env...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.WaitDelay = 500 * time.Millisecond

	if err := cmd.Run(); err != nil {
		if scriptCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("timed out after %s", r.timeout)
		}
		return err
	}
	return nil
}

// setScriptState updates the state of a script by stage and name. If the
// script was not previously discovered (e.g. Discover was not called), it
// is appended to the list.
func (r *Runner) setScriptState(stage Stage, name string, state ScriptState) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i := range r.scripts {
		if r.scripts[i].Stage == stage && r.scripts[i].Name == name {
			r.scripts[i].State = state
			return
		}
	}
	// Not found — append (Discover may not have been called, or dir changed).
	r.scripts = append(r.scripts, ScriptResult{
		Stage: stage,
		Name:  name,
		State: state,
	})
}

// ParseStage converts a string to a Stage, case-insensitively.
// Returns the stage and true if valid, or zero value and false if not.
func ParseStage(s string) (Stage, bool) {
	switch strings.ToUpper(s) {
	case "BOOT":
		return StageBoot, true
	case "START":
		return StageStart, true
	case "READY":
		return StageReady, true
	case "SHUTDOWN":
		return StageShutdown, true
	default:
		return "", false
	}
}
