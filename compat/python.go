package compat

// Finding the Python 3 interpreter that runs the python-sdk suite.
//
// Neither candidate name is right everywhere. `python3` is the correct name on
// Linux and macOS and is frequently absent on Windows; `python` is the usual
// Windows name and is still Python 2 on some older systems. Worse, Windows
// ships a *stub* at `python3.exe` — the Microsoft Store "app execution alias"
// — which is on PATH and is executable, so exec.LookPath finds it and the
// suite dies at launch with "Python was not found; run without arguments to
// install from the Microsoft Store…" instead of running a single test.
//
// So presence on PATH proves nothing. The only reliable discriminator is to
// ask each candidate for its version and require it to answer like a Python 3.

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// pythonCandidates are probed in order; the first that identifies itself as
// Python 3 wins. `python3` is first because where both exist and disagree, it
// is the one guaranteed not to be Python 2.
var pythonCandidates = []string{"python3", "python"}

// pythonProbeTimeout bounds a single `--version` call. A real interpreter
// answers in milliseconds; the bound exists so a wedged stub cannot hang the
// whole run before it starts.
const pythonProbeTimeout = 10 * time.Second

// pythonInterpreter returns the command name that runs Python 3 on this
// machine, or an error naming every candidate tried and why each was rejected.
// The result is computed once — the probe spawns processes, and defaultSuites
// is called for every Runner regardless of whether python-sdk is in the run.
var pythonInterpreter = sync.OnceValues(func() (string, error) {
	return resolvePython(pythonCandidates, execPythonProbe)
})

// pythonSuiteArgv returns the command line that runs the python-sdk suite, or
// the reason no interpreter on this machine can. Argv stays nil in the failure
// case so a caller that forgets to check ArgvErr still cannot spawn anything.
func pythonSuiteArgv() ([]string, error) {
	python, err := pythonInterpreter()
	if err != nil {
		return nil, err
	}
	return []string{python, "runner.py"}, nil
}

// pythonProbe reports what `<name> --version` printed (stdout and stderr
// combined — Python 2 writes its version to stderr) and how it exited.
type pythonProbe func(name string) (output string, err error)

// execPythonProbe is the real probe: look the candidate up on PATH, then ask
// it for its version.
func execPythonProbe(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), pythonProbeTimeout)
	defer cancel()
	//nolint:gosec // path comes from a fixed candidate list, not user input
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	return string(out), err
}

// resolvePython returns the first candidate whose version probe identifies a
// Python 3 interpreter.
func resolvePython(candidates []string, probe pythonProbe) (string, error) {
	rejected := make([]string, 0, len(candidates))
	for _, name := range candidates {
		out, err := probe(name)
		if isPython3(out, err) {
			return name, nil
		}
		rejected = append(rejected, fmt.Sprintf("%s (%s)", name, describePythonProbe(out, err)))
	}
	return "", fmt.Errorf(
		"no Python 3 interpreter found: tried %s; install Python 3 and put it on PATH",
		strings.Join(rejected, ", "),
	)
}

// isPython3 reports whether a version probe came from a working Python 3.
// Both halves matter: the exit status rules out the Store stub, which prints a
// paragraph of prose and exits non-zero, and the prefix rules out a Python 2
// that exits cleanly.
func isPython3(output string, err error) bool {
	return err == nil && strings.HasPrefix(strings.TrimSpace(output), "Python 3")
}

// describePythonProbe explains, in a few words fit for an error message, why a
// candidate was not usable.
func describePythonProbe(output string, err error) string {
	trimmed := strings.TrimSpace(output)
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return "not on PATH"
	case strings.Contains(trimmed, "Microsoft Store"):
		return "Microsoft Store alias stub, not an interpreter"
	case strings.HasPrefix(trimmed, "Python 2"):
		return "Python 2, not Python 3"
	case err != nil && trimmed != "":
		return fmt.Sprintf("%v: %s", err, firstProbeLine(trimmed))
	case err != nil:
		return err.Error()
	default:
		return "unrecognised --version output: " + firstProbeLine(trimmed)
	}
}

// firstProbeLine keeps an error message to one line. Probe output can be a
// multi-line paragraph, and the rest of it never adds anything.
func firstProbeLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
