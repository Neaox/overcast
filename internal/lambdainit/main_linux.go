//go:build linux

package lambdainit

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/overcast-sh/overcast/internal/services/lambda/initproto"
)

// options is everything the init needs. Production values come from argv and
// the environment; the tests set them directly, which is what lets the whole
// init — proxy, drain, shipper, supervisor — be exercised in-process against a
// fake Runtime API and real pipes, with no Docker anywhere.
type options struct {
	// hostAddr is the Overcast per-environment Runtime API endpoint,
	// "host:port", from OVERCAST_RUNTIME_API.
	hostAddr string
	// listenAddr is where the init serves the Runtime API. Production is
	// initproto.LambdaRuntimeAPI; tests use 127.0.0.1:0 and read back the port.
	listenAddr string
	// extensionsDir is scanned for external extensions.
	extensionsDir string
	// annotate prefixes teed lines with "req=<id> ".
	annotate bool
	// child is the command to run: the container's ENTRYPOINT + CMD.
	child []string
	// environ is the environment the children inherit.
	environ []string

	// stdout and stderr are the container's own streams — where the child's
	// bytes are teed so `docker logs` is unchanged. diag takes the init's
	// [overcast-init] lines; in production it is the same file as stderr.
	stdout io.Writer
	stderr io.Writer
	diag   io.Writer

	now     func() time.Time
	signals <-chan os.Signal

	backlogFrames    int
	backlogBytes     int
	extensionGrace   time.Duration
	terminationGrace time.Duration
	flushGrace       time.Duration

	// ready, when set, is called with the address the proxy is listening on
	// once the runtime child is running. Tests use it; production does not.
	ready func(addr string)
}

func (o *options) applyDefaults() {
	if o.listenAddr == "" {
		o.listenAddr = initproto.LambdaRuntimeAPI
	}
	if o.extensionsDir == "" {
		o.extensionsDir = initproto.ExtensionsDir
	}
	if o.stdout == nil {
		o.stdout = os.Stdout
	}
	if o.stderr == nil {
		o.stderr = os.Stderr
	}
	if o.diag == nil {
		o.diag = o.stderr
	}
	if o.now == nil {
		o.now = time.Now
	}
	if o.extensionGrace <= 0 {
		o.extensionGrace = 2 * time.Second
	}
	if o.terminationGrace <= 0 {
		o.terminationGrace = 2 * time.Second
	}
	if o.flushGrace <= 0 {
		o.flushGrace = 2 * time.Second
	}
}

// Main is the init's entry point: it runs the container's original command as
// its child and returns the exit code the process should use. argv and environ
// are os.Args and os.Environ().
func Main(argv []string, environ []string) int {
	opts := options{
		hostAddr: lookupEnv(environ, initproto.EnvRuntimeAPI),
		annotate: truthy(lookupEnv(environ, initproto.EnvAnnotate)),
		environ:  environ,
	}
	if len(argv) > 1 {
		opts.child = argv[1:]
	}

	sig := make(chan os.Signal, 4)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sig)
	opts.signals = sig

	return run(context.Background(), opts)
}

func lookupEnv(environ []string, key string) string {
	for _, kv := range environ {
		if hasEnvKey(kv, key) {
			return kv[len(key)+1:]
		}
	}
	return ""
}

// truthy reads an opt-in flag the way a shell would: set to anything except
// "", "0" or "false" turns it on.
func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// run is Main with everything injected. It returns the process exit code.
func run(ctx context.Context, opts options) int {
	opts.applyDefaults()
	diag := &diagLog{w: opts.diag}

	if opts.hostAddr == "" {
		diag.printf("%s is not set: the init has no Overcast Runtime API endpoint to proxy to, so it cannot start", initproto.EnvRuntimeAPI)
		return exitConfig
	}
	if len(opts.child) == 0 {
		diag.printf("no child command: the init is invoked as `%s <entrypoint> [cmd…]`", initproto.InitPath)
		return exitConfig
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	s := &supervisor{
		opts:    opts,
		diag:    diag,
		ship:    newShipper(opts.hostAddr, opts.backlogFrames, opts.backlogBytes, opts.now, diag),
		tracker: &requestTracker{},
		reaper:  newReaper(diag),
		drainer: &drainer{},
	}
	s.phase = &initPhase{now: opts.now, publish: s.ship.publishRecord, diag: diag}
	s.proxy = newProxy(opts.hostAddr, s.tracker, s.drainer.drain, diag)
	s.proxy.initDone = func() (uint64, bool) { return s.phase.complete(initproto.StatusSuccess) }
	s.proxy.invokeDone = func(req string, durationMs float64, producedBytes *int64, spans []initproto.RecSpan) uint64 {
		return s.ship.publishRequestRecord(req, initproto.Record{
			Type:          initproto.RecInvokeDone,
			DurationMs:    durationMs,
			ProducedBytes: producedBytes,
			Spans:         spans,
		})
	}

	s.reaper.start()
	defer s.reaper.shutdown()

	shipDone := make(chan struct{})
	go func() {
		defer close(shipDone)
		s.ship.run(ctx)
	}()

	addr, err := s.proxy.listen(opts.listenAddr)
	if err != nil {
		diag.printf("cannot serve the Runtime API on %s: %v", opts.listenAddr, err)
		cancel()
		<-shipDone
		return exitConfig
	}
	go func() {
		if err := s.proxy.serve(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			diag.printf("runtime API listener stopped: %v", err)
		}
	}()

	// INIT has begun. Published before anything is spawned, so no line the
	// environment produces can precede platform.initStart on the stream — the
	// extensions start first on AWS too, and their output belongs inside the
	// phase, not in front of it.
	s.phase.begin()

	env := childEnv(opts.environ, addr)
	s.startExtensions(env)

	runtime, err := s.startChild("runtime", opts.child, env, initproto.SrcStdout, initproto.SrcStderr)
	if err != nil {
		diag.printf("cannot start the runtime %q: %v", opts.child[0], err)
		s.finish()
		cancel()
		<-shipDone
		return exitCannotExec
	}
	diag.printf("runtime started pid=%d argv=%q", runtime.pid(), opts.child)

	if opts.ready != nil {
		opts.ready(addr)
	}

	code := s.awaitRuntime(ctx, runtime)
	s.finish()
	// finish() flushed and closed the log stream; anything the shipper is
	// still doing is a retry against a host that is not answering, and the
	// container is going away regardless.
	cancel()
	<-shipDone
	return code
}
