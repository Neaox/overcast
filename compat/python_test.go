package compat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The exact text Windows' Microsoft Store app-execution alias prints when
// something runs the `python3` it has planted on PATH. This is the string the
// python-sdk suite died on, so it is the one worth asserting against.
const storeStubOutput = "Python was not found; run without arguments to install from the Microsoft Store, " +
	"or disable this shortcut from Settings > Manage App Execution Aliases.\n"

func notOnPath(name string) error {
	return &exec.Error{Name: name, Err: exec.ErrNotFound}
}

func TestResolvePython(t *testing.T) {
	tests := []struct {
		name   string
		probes map[string]struct {
			out string
			err error
		}
		want       string
		wantErrHas []string
	}{
		{
			name: "prefers python3 when it is a real Python 3",
			probes: map[string]struct {
				out string
				err error
			}{
				"python3": {out: "Python 3.12.4\n"},
				"python":  {out: "Python 2.7.18\n"},
			},
			want: "python3",
		},
		{
			// The Windows case from issue #795's sibling failure: the stub is
			// on PATH and executable, so only the probe tells it apart.
			name: "falls back to python when python3 is the Store alias stub",
			probes: map[string]struct {
				out string
				err error
			}{
				"python3": {out: storeStubOutput, err: &exec.ExitError{}},
				"python":  {out: "Python 3.14.3\n"},
			},
			want: "python",
		},
		{
			name: "falls back to python when python3 is absent",
			probes: map[string]struct {
				out string
				err error
			}{
				"python3": {err: notOnPath("python3")},
				"python":  {out: "Python 3.11.9\n"},
			},
			want: "python",
		},
		{
			name: "rejects a python that is still Python 2",
			probes: map[string]struct {
				out string
				err error
			}{
				"python3": {err: notOnPath("python3")},
				"python":  {out: "Python 2.7.18\n"},
			},
			wantErrHas: []string{"python3 (not on PATH)", "python (Python 2, not Python 3)"},
		},
		{
			name: "reports every candidate when none is usable",
			probes: map[string]struct {
				out string
				err error
			}{
				"python3": {out: storeStubOutput, err: &exec.ExitError{}},
				"python":  {err: notOnPath("python")},
			},
			wantErrHas: []string{
				"python3 (Microsoft Store alias stub, not an interpreter)",
				"python (not on PATH)",
				"install Python 3",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probe := func(name string) (string, error) {
				p, ok := tt.probes[name]
				if !ok {
					t.Fatalf("unexpected probe of %q", name)
				}
				return p.out, p.err
			}

			got, err := resolvePython(pythonCandidates, probe)

			if len(tt.wantErrHas) > 0 {
				if err == nil {
					t.Fatalf("resolvePython() = %q, want error", got)
				}
				for _, want := range tt.wantErrHas {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("resolvePython() error = %q, want it to mention %q", err, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("resolvePython() error = %v, want %q", err, tt.want)
			}
			if got != tt.want {
				t.Errorf("resolvePython() = %q, want %q", got, tt.want)
			}
		})
	}
}

// The interpreter really on this machine must be usable — otherwise the
// python-sdk suite cannot run here, and that is worth failing loudly for
// rather than discovering nineteen tests into a compat run.
func TestPythonInterpreter_resolvesOnThisMachine(t *testing.T) {
	name, err := pythonInterpreter()
	if err != nil {
		t.Skipf("no Python 3 on this machine: %v", err)
	}
	out, err := execPythonProbe(name)
	if !isPython3(out, err) {
		t.Fatalf("pythonInterpreter() = %q, but %q --version gave %q (%v)", name, name, out, err)
	}
}

func TestRunSuite_reportsUnresolvedInterpreter(t *testing.T) {
	// Given: a suite whose interpreter could not be resolved.
	r := &Runner{
		cfg:       RunConfig{Endpoint: "http://localhost:4566", Region: "us-east-1", RunID: "oc-test"},
		logWriter: &bytes.Buffer{},
	}
	suite := SuiteConfig{
		Name:    "python-sdk",
		ArgvErr: errors.New("no Python 3 interpreter found: tried python3 (not on PATH)"),
	}

	// When: the runner tries to execute it.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := r.runSuite(ctx, suite, 1)

	// Then: the reason is the error, not whatever exec would have said about
	// an empty command.
	if err == nil {
		t.Fatal("runSuite() error = nil, want the interpreter resolution failure")
	}
	if !strings.Contains(err.Error(), "no Python 3 interpreter found") {
		t.Fatalf("runSuite() error = %q, want the resolution failure", err)
	}
	if !strings.Contains(err.Error(), `suite "python-sdk"`) {
		t.Fatalf("runSuite() error = %q, want it to name the suite", err)
	}
}

// The dashboard spawns suites through the orchestrator rather than the
// runner, so it needs the same guard: without it an unresolved interpreter
// reaches exec as an empty command name.
func TestOrchestratorStartSuite_reportsUnresolvedInterpreter(t *testing.T) {
	o := &Orchestrator{ctx: context.Background()}
	sp := &SuiteProcess{
		Name: "python-sdk",
		Config: SuiteConfig{
			Name:    "python-sdk",
			ArgvErr: errors.New("no Python 3 interpreter found: tried python3 (not on PATH)"),
		},
	}

	err := o.startSuite(sp)

	if err == nil {
		t.Fatal("startSuite() error = nil, want the interpreter resolution failure")
	}
	if !strings.Contains(err.Error(), "no Python 3 interpreter found") {
		t.Fatalf("startSuite() error = %q, want the resolution failure", err)
	}
}

func TestDescribePythonProbe(t *testing.T) {
	tests := []struct {
		name string
		out  string
		err  error
		want string
	}{
		{name: "missing", err: notOnPath("python3"), want: "not on PATH"},
		{name: "store stub", out: storeStubOutput, err: &exec.ExitError{}, want: "Microsoft Store alias stub, not an interpreter"},
		{name: "python 2", out: "Python 2.7.18\n", want: "Python 2, not Python 3"},
		{name: "unrecognised", out: "banana 1.0\n", want: "unrecognised --version output: banana 1.0"},
		{name: "failed with output", out: "boom\nmore\n", err: fmt.Errorf("exit status 9"), want: "exit status 9: boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := describePythonProbe(tt.out, tt.err); got != tt.want {
				t.Errorf("describePythonProbe() = %q, want %q", got, tt.want)
			}
		})
	}
}
