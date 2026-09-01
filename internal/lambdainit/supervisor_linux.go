//go:build linux

package lambdainit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/overcast-sh/overcast/internal/services/lambda/initproto"
)

// diagLog writes the init's own diagnostics — one greppable line each, all
// prefixed [overcast-init] — to the container's stderr and nowhere else. They
// are never published as frames, so they cannot reach CloudWatch, the tail or
// the Telemetry API: those stay AWS-shaped. `docker logs` is where a human
// reads them.
type diagLog struct {
	mu sync.Mutex
	w  io.Writer
}

func (d *diagLog) printf(format string, args ...any) {
	if d == nil || d.w == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	fmt.Fprintf(d.w, "[overcast-init] "+format+"\n", args...)
}

// childProc is one process the init started: the runtime, or an extension.
type childProc struct {
	label  string
	cmd    *exec.Cmd
	status <-chan syscall.WaitStatus
}

func (c *childProc) pid() int {
	if c.cmd == nil || c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}

func (c *childProc) signal(sig os.Signal) {
	if c.cmd == nil || c.cmd.Process == nil {
		return
	}
	// The process may already have been reaped, in which case the kernel says
	// ESRCH and there is nothing to report.
	_ = c.cmd.Process.Signal(sig)
}

// supervisor wires the pieces together: it owns the children, their pipes, the
// log shipper and the Runtime API proxy for the life of the container.
type supervisor struct {
	opts    options
	diag    *diagLog
	ship    *shipper
	tracker *requestTracker
	reaper  *reaper
	drainer *drainer
	proxy   *proxy
	phase   *initPhase

	readerWG   sync.WaitGroup
	readEnds   []*os.File
	extensions []*childProc
}

// publishLine is the single funnel every line goes through: it attributes the
// line to whatever invocation is in flight *now* — which is the moment the init
// read it — and hands it to the shipper, which assigns the seq.
func (s *supervisor) publishLine(src, msg string) uint64 {
	return s.ship.publish(s.tracker.attribute(), src, msg)
}

func (s *supervisor) annotation() string {
	id := s.tracker.current()
	if id == "" {
		return ""
	}
	return "req=" + id + " "
}

// startChild launches one process with pipes the init owns, and starts a reader
// on each. srcOut and srcErr are the frame sources for the two streams: the
// runtime's are "stdout"/"stderr", an extension's are both "ext:<name>".
func (s *supervisor) startChild(label string, argv, env []string, srcOut, srcErr string) (*childProc, error) {
	rOut, wOut, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		rOut.Close()
		wOut.Close()
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	outReader, err := s.newReader(rOut, srcOut, s.opts.stdout)
	if err != nil {
		closeAll(rOut, wOut, rErr, wErr)
		return nil, err
	}
	errReader, err := s.newReader(rErr, srcErr, s.opts.stderr)
	if err != nil {
		closeAll(rOut, wOut, rErr, wErr)
		return nil, err
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Stdout = wOut
	cmd.Stderr = wErr

	status, err := s.reaper.spawn(cmd)
	// The child holds its own duplicates of the write ends; the init must not,
	// or the readers would never see EOF.
	wOut.Close()
	wErr.Close()
	if err != nil {
		rOut.Close()
		rErr.Close()
		return nil, err
	}

	s.readEnds = append(s.readEnds, rOut, rErr)
	s.drainer.add(outReader, errReader)
	s.startReader(outReader)
	s.startReader(errReader)

	return &childProc{label: label, cmd: cmd, status: status}, nil
}

func (s *supervisor) newReader(f *os.File, src string, tee io.Writer) (*pipeReader, error) {
	r, err := newPipeReader(f, src, tee, s.publishLine)
	if err != nil {
		return nil, err
	}
	r.annotate = s.opts.annotate
	r.annotation = s.annotation
	return r, nil
}

func (s *supervisor) startReader(r *pipeReader) {
	s.readerWG.Add(1)
	go func() {
		defer s.readerWG.Done()
		r.run()
	}()
}

func closeAll(files ...*os.File) {
	for _, f := range files {
		f.Close()
	}
}

// startExtensions launches every external extension, before the runtime, as
// AWS does. A directory that is absent or empty is the normal case.
func (s *supervisor) startExtensions(env []string) {
	paths, err := discoverExtensions(s.opts.extensionsDir)
	if err != nil {
		s.diag.printf("cannot read %s: %v", s.opts.extensionsDir, err)
		return
	}
	for _, path := range paths {
		name := filepath.Base(path)
		src := initproto.ExtensionSrc(name)
		child, err := s.startChild(name, []string{path}, env, src, src)
		if err != nil {
			s.diag.printf("extension %s failed to start: %v", name, err)
			continue
		}
		s.extensions = append(s.extensions, child)
		s.diag.printf("extension %s started pid=%d", name, child.pid())
	}
}

// discoverExtensions lists the executables in dir, in name order. Symlinks are
// followed, matching what the shell bootstrap this replaces did with
// `[ -f ] && [ -x ]`.
func discoverExtensions(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			continue
		}
		paths = append(paths, path)
	}
	return paths, nil // os.ReadDir sorts by filename
}

// stopExtensions asks the extensions to go away and gives them a bounded
// moment. AWS gives an extension a SHUTDOWN event through the Runtime API —
// that is the host's job, over the proxy — so all the init owes them is a
// signal and the guarantee that it will not hang waiting.
func (s *supervisor) stopExtensions() {
	if len(s.extensions) == 0 {
		return
	}
	for _, e := range s.extensions {
		e.signal(syscall.SIGTERM)
	}
	// One grace period for all of them, not one each.
	deadline := time.Now().Add(s.opts.extensionGrace)
	for _, e := range s.extensions {
		select {
		case ws := <-e.status:
			s.diag.printf("extension %s exited %s", e.label, describeStatus(ws))
		case <-time.After(time.Until(deadline)):
			s.diag.printf("extension %s did not exit within %s; killing it", e.label, s.opts.extensionGrace)
			e.signal(syscall.SIGKILL)
		}
	}
}

func (s *supervisor) signalAll(sig os.Signal, runtime *childProc) {
	if runtime != nil {
		runtime.signal(sig)
	}
	for _, e := range s.extensions {
		e.signal(sig)
	}
}

// awaitRuntime blocks until the runtime child is gone, forwarding signals to
// the whole process group's worth of children in the meantime, and returns the
// exit code the init will use.
func (s *supervisor) awaitRuntime(ctx context.Context, runtime *childProc) int {
	for {
		select {
		case ws := <-runtime.status:
			s.diag.printf("child exited pid=%d %s", runtime.pid(), describeStatus(ws))
			return exitCodeFor(ws)

		case sig := <-s.opts.signals:
			s.diag.printf("received %v; forwarding to the runtime and %d extension(s)", sig, len(s.extensions))
			s.signalAll(sig, runtime)

		case <-ctx.Done():
			s.diag.printf("shutting down: %v", ctx.Err())
			s.signalAll(syscall.SIGTERM, runtime)
			select {
			case ws := <-runtime.status:
				s.diag.printf("child exited pid=%d %s", runtime.pid(), describeStatus(ws))
				return exitCodeFor(ws)
			case <-time.After(s.opts.terminationGrace):
				s.diag.printf("child did not exit within %s; killing it", s.opts.terminationGrace)
				s.signalAll(syscall.SIGKILL, runtime)
				select {
				case ws := <-runtime.status:
					return exitCodeFor(ws)
				case <-time.After(s.opts.terminationGrace):
					return 128 + int(syscall.SIGKILL)
				}
			}
		}
	}
}

// finish drains what the children wrote, stops them, and gets the log stream
// safely to the host before the container dies.
func (s *supervisor) finish() {
	drainCtx, cancel := context.WithTimeout(context.Background(), drainMax)
	s.drainer.drain(drainCtx)
	cancel()

	// A runtime that is gone without ever having polled for work never
	// finished INIT. Closing the phase here — after the drain, so the records
	// follow whatever it managed to print on its way out — is what turns a
	// cold start that died into two WARN-level platform records instead of
	// silence. It is a no-op on every ordinary exit, where the first /next
	// closed the phase long ago.
	s.phase.complete(initproto.StatusError)

	s.stopExtensions()

	drainCtx, cancel = context.WithTimeout(context.Background(), drainMax)
	s.drainer.drain(drainCtx)
	cancel()

	// Closing the read ends unblocks the readers, which flush any line that
	// never got its newline.
	closeAll(s.readEnds...)
	s.readerWG.Wait()

	flushCtx, cancel := context.WithTimeout(context.Background(), s.opts.flushGrace)
	if err := s.ship.flush(flushCtx); err != nil {
		s.diag.printf("log channel: %v", err)
	}
	cancel()

	closeCtx, cancel := context.WithTimeout(context.Background(), s.opts.flushGrace)
	s.ship.close(closeCtx)
	cancel()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	s.proxy.shutdown(shutdownCtx)
	cancel()
}

// childEnv is the environment the runtime and the extensions run in: the
// init's own, with AWS_LAMBDA_RUNTIME_API pointed at the init's proxy and the
// init's own configuration removed, so the child's environment is the one AWS
// would have given it.
func childEnv(environ []string, runtimeAPI string) []string {
	const awsRuntimeAPIKey = "AWS_LAMBDA_RUNTIME_API"
	out := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		switch {
		case hasEnvKey(kv, awsRuntimeAPIKey),
			hasEnvKey(kv, initproto.EnvRuntimeAPI),
			hasEnvKey(kv, initproto.EnvAnnotate):
			continue
		}
		out = append(out, kv)
	}
	return append(out, awsRuntimeAPIKey+"="+runtimeAPI)
}

func hasEnvKey(kv, key string) bool {
	return len(kv) > len(key) && kv[:len(key)] == key && kv[len(key)] == '='
}
