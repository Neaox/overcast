//go:build windows

package inithooks

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// buildScriptCmd constructs the exec.Cmd for running a script on Windows.
// Hook scripts are POSIX shell scripts, so a real sh is preferred: the .sh
// file association that cmd.exe would use typically points at git-bash.exe,
// which opens a terminal window per script and does not propagate exit
// codes, so a failing hook would be reported successful. cmd.exe remains
// the fallback for machines with an association but no findable sh.
//
// Process tree management is done in runScriptCmd via a Job Object rather
// than here: unlike Unix process groups, a job's membership is fixed up
// after the process starts (see runScriptCmd), so there's nothing to attach
// to the Cmd at build time beyond what exec.CommandContext already gives us.
func buildScriptCmd(ctx context.Context, path string) *exec.Cmd {
	if sh := findSh(); sh != "" {
		return exec.CommandContext(ctx, sh, path)
	}
	return exec.CommandContext(ctx, "cmd.exe", "/c", path)
}

// findSh locates a POSIX shell: sh on PATH, or the sh.exe bundled with Git
// for Windows (which installs git.exe on PATH but not its shell).
var findSh = sync.OnceValue(func() string {
	if sh, err := exec.LookPath("sh"); err == nil {
		return sh
	}
	if git, err := exec.LookPath("git"); err == nil {
		sh := filepath.Join(filepath.Dir(filepath.Dir(git)), "bin", "sh.exe")
		if _, err := os.Stat(sh); err == nil {
			return sh
		}
	}
	return ""
})

// runScriptCmd starts and waits for cmd inside a Windows Job Object
// configured with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE, so that a timeout
// takes down the script's entire process tree rather than just the shell.
//
// Without this, exec.CommandContext's cancellation on Windows reduces to
// TerminateProcess on the shell process alone (there is no SIGKILL-to-a-
// process-group equivalent here): any descendant the shell backgrounded with
// `&` is orphaned and keeps running, still holding whatever handles it
// inherited from the shell — including, under `go test`, the test binary's
// own stdout pipe, which is what makes a passing package get reported as
// failed. See https://github.com/Neaox/overcast/issues/947.
func runScriptCmd(cmd *exec.Cmd) error {
	job, err := newKillOnCloseJob()
	if err != nil {
		// No job object available (e.g. a locked-down environment that
		// denies job creation). Fall back to the pre-fix behavior: the shell
		// is still terminated on timeout, its descendants are not.
		return cmd.Run()
	}
	defer func() { _ = job.close() }()

	cmd.Cancel = func() error {
		// Closing the job's last handle triggers KILL_ON_JOB_CLOSE, which
		// terminates every process still assigned to it — the shell and any
		// descendants it spawned. Also kill the shell directly: if it never
		// made it into the job (see the assign call below), this still
		// matches the pre-fix guarantee that the shell itself always dies.
		_ = job.close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return nil
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	// Assign as soon as the process exists. A process that is already a job
	// member has its own children added to the same job automatically (we
	// never request breakaway), so this one assignment covers the whole tree
	// the shell goes on to spawn — provided the shell doesn't outrun it. In
	// practice AssignProcessToJobObject completes long before a freshly
	// started shell has parsed its script and forked anything.
	_ = job.assign(cmd.Process.Pid)

	return cmd.Wait()
}

// killOnCloseJob is a Windows Job Object configured so that terminating it
// (closing its last open handle) kills every process still assigned to it.
type killOnCloseJob struct {
	handle windows.Handle
	once   sync.Once
}

func newKillOnCloseJob() (*killOnCloseJob, error) {
	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		handle,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}

	return &killOnCloseJob{handle: handle}, nil
}

// assign adds the process to the job.
func (j *killOnCloseJob) assign(pid int) error {
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(proc) }()

	return windows.AssignProcessToJobObject(j.handle, proc)
}

// close closes the job's handle. Safe to call more than once.
func (j *killOnCloseJob) close() error {
	var err error
	j.once.Do(func() {
		err = windows.CloseHandle(j.handle)
	})
	return err
}
