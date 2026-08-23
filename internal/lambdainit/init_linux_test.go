//go:build linux

package lambdainit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/Neaox/overcast/internal/services/lambda/initproto"
)

func TestInitRefusesToStartWithoutTheHostEndpoint(t *testing.T) {
	var diag lockedBuffer
	code := run(context.Background(), options{
		child: childCmd(),
		diag:  &diag,
	})

	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(diag.String(), initproto.EnvRuntimeAPI) {
		t.Fatalf("the diagnostic does not name %s: %q", initproto.EnvRuntimeAPI, diag.String())
	}
	if !strings.HasPrefix(diag.String(), "[overcast-init] ") {
		t.Fatalf("the diagnostic is not labelled: %q", diag.String())
	}
}

func TestInitRefusesToStartWithoutAChildCommand(t *testing.T) {
	var diag lockedBuffer
	code := run(context.Background(), options{
		hostAddr: "127.0.0.1:1",
		diag:     &diag,
	})

	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(diag.String(), initproto.InitPath) {
		t.Fatalf("the diagnostic does not show how the init is invoked: %q", diag.String())
	}
}

func TestMainRefusesToStartWithoutTheHostEndpoint(t *testing.T) {
	// The exported entry point, with the environment a misconfigured container
	// would give it.
	if code := Main([]string{"/var/overcast/init", "/lambda-entrypoint.sh", "app.handler"}, []string{"PATH=/usr/bin"}); code != 2 {
		t.Fatalf("Main exit code = %d, want 2", code)
	}
}

func TestInitCannotStartTheRuntime(t *testing.T) {
	h := newFakeHost(t)
	res := runInit(t, h, options{
		child:   []string{"/nonexistent/lambda-entrypoint.sh", "app.handler"},
		environ: childEnviron("exit", "0"),
	})

	if res.code != 127 {
		t.Fatalf("exit code = %d, want 127", res.code)
	}
	if !strings.Contains(res.diag, "/nonexistent/lambda-entrypoint.sh") {
		t.Fatalf("the diagnostic does not name the command: %q", res.diag)
	}
}

func TestInitRunsTheContainerCommandAndTeesItsOutput(t *testing.T) {
	h := newFakeHost(t)
	res := runInit(t, h, options{
		child:   childCmd(),
		environ: childEnviron("print-then-exit", "3"),
	})

	if res.code != 3 {
		t.Fatalf("exit code = %d, want 3 (the child's own)", res.code)
	}

	// The tee is byte-for-byte: `docker logs` must be exactly what the child
	// wrote, with nothing of the init's mixed in.
	if res.stdout != "out-one\nout-two\n" {
		t.Fatalf("teed stdout = %q", res.stdout)
	}
	if res.stderr != "err-one\n" {
		t.Fatalf("teed stderr = %q", res.stderr)
	}

	h.mustAwaitFrameCount(3)
	frames := h.snapshotFrames()
	assertContiguous(t, frames)

	stdout := framesBySource(frames, initproto.SrcStdout)
	if len(stdout) != 2 || stdout[0].Msg != "out-one" || stdout[1].Msg != "out-two" {
		t.Fatalf("stdout frames = %v", frameMessages(stdout))
	}
	stderr := framesBySource(frames, initproto.SrcStderr)
	if len(stderr) != 1 || stderr[0].Msg != "err-one" {
		t.Fatalf("stderr frames = %v", frameMessages(stderr))
	}
	for _, f := range frames {
		if f.Req != "" {
			t.Errorf("frame %d was attributed to %q, but no invocation ever started", f.Seq, f.Req)
		}
		if f.T == 0 {
			t.Errorf("frame %d has no timestamp", f.Seq)
		}
	}
}

func TestInitPassesTheEntrypointArgumentsThrough(t *testing.T) {
	h := newFakeHost(t)
	res := runInit(t, h, options{
		child:   childCmd("/lambda-entrypoint.sh", "app.handler"),
		environ: childEnviron("echo-args", ""),
	})

	if res.code != 0 {
		t.Fatalf("exit code = %d, want 0", res.code)
	}
	if want := "argv=/lambda-entrypoint.sh|app.handler\n"; res.stdout != want {
		t.Fatalf("the child saw %q, want %q", res.stdout, want)
	}
}

func TestInitDiagnosticsGoToStderrOnlyAndNeverIntoFrames(t *testing.T) {
	h := newFakeHost(t)
	res := runInit(t, h, options{
		child:   childCmd(),
		environ: childEnviron("print-then-exit", "0"),
	})

	for _, want := range []string{"runtime started pid=", "child exited pid=", "log channel connected"} {
		if !strings.Contains(res.diag, "[overcast-init] "+want) {
			t.Errorf("no %q line in the diagnostics:\n%s", want, res.diag)
		}
	}
	if strings.Contains(res.stdout, "[overcast-init]") {
		t.Errorf("an [overcast-init] line reached the container's stdout: %q", res.stdout)
	}

	h.mustAwaitFrameCount(3)
	for _, f := range h.snapshotFrames() {
		if strings.Contains(f.Msg, "[overcast-init]") {
			t.Errorf("an [overcast-init] line was shipped as a frame: %q", f.Msg)
		}
	}
}

func TestInitForwardsTerminationSignalsToTheChild(t *testing.T) {
	h := newFakeHost(t)
	signals := make(chan os.Signal, 1)

	res := runInit(t, h, options{
		child:   childCmd(),
		environ: childEnviron("sleep", ""),
		signals: signals,
		// The child is running by the time ready fires, so this is a signal
		// delivered mid-life, not a race with startup.
		ready: func(string) { signals <- syscall.SIGTERM },
	})

	if res.code != 143 {
		t.Fatalf("exit code = %d, want 143 (128+SIGTERM)", res.code)
	}
	if !strings.Contains(res.diag, "forwarding to the runtime") {
		t.Fatalf("the diagnostics do not record the forward:\n%s", res.diag)
	}
	if !strings.Contains(res.diag, "signal=terminated") {
		t.Fatalf("the diagnostics do not record how the child died:\n%s", res.diag)
	}
}

func TestInitAttributesEachLineToTheInvocationInFlight(t *testing.T) {
	h := newFakeHost(t)

	// The barriers are the point: invocation 1 does not start until the host
	// has the INIT-phase line, and invocation 2 does not start until it has the
	// line the child wrote between them. Attribution is then a fact about the
	// protocol, not about timing.
	h.enqueue(invocation{id: "req-1", payload: `{"n":1}`, before: func() { h.awaitMessage("init-line") }})
	h.enqueue(invocation{id: "req-2", payload: `{"n":2}`, before: func() { h.awaitMessage("between-1") }})

	res := runInit(t, h, options{
		child:   childCmd(),
		environ: childEnviron("runtime-attribution", ""),
	})
	if res.code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", res.code, res.diag)
	}

	h.mustAwaitFrameCount(7)
	frames := h.snapshotFrames()
	assertContiguous(t, frames)

	want := []struct {
		msg string
		req string
		src string
	}{
		{msg: "init-line", req: "", src: initproto.SrcStdout},
		{msg: "inv-1-out", req: "req-1", src: initproto.SrcStdout},
		{msg: "inv-1-err", req: "req-1", src: initproto.SrcStderr},
		{msg: "between-1", req: "", src: initproto.SrcStdout},
		{msg: "inv-2-out", req: "req-2", src: initproto.SrcStdout},
		{msg: "inv-2-err", req: "req-2", src: initproto.SrcStderr},
		{msg: "between-2", req: "", src: initproto.SrcStdout},
	}
	for _, w := range want {
		f := findFrame(t, frames, w.msg)
		if f.Req != w.req {
			t.Errorf("%q was attributed to %q, want %q", w.msg, f.Req, w.req)
		}
		if f.Src != w.src {
			t.Errorf("%q came from %q, want %q", w.msg, f.Src, w.src)
		}
	}

	calls := h.snapshotCalls()
	if len(calls) != 2 {
		t.Fatalf("host saw %d results, want 2", len(calls))
	}
	for i, c := range calls {
		if c.kind != "response" || c.id != "req-"+strconv.Itoa(i+1) {
			t.Errorf("call %d = %+v", i, c)
		}
		if c.body != "result-"+strconv.Itoa(i+1) {
			t.Errorf("call %d body = %q", i, c.body)
		}
	}

	// Each response reports a seq that covers its own invocation's output.
	for i, c := range calls {
		last := findFrame(t, frames, "inv-"+strconv.Itoa(i+1)+"-err")
		if seqOf(t, c.logSeq) < last.Seq {
			t.Errorf("%s reported seq %s, but its last line is seq %d", c.id, c.logSeq, last.Seq)
		}
	}
}

func TestInitDrainsBeforeForwardingTheResponse(t *testing.T) {
	h := newFakeHost(t)
	h.enqueue(invocation{id: "req-big", payload: "{}"})

	res := runInit(t, h, options{
		child:   childCmd(),
		environ: childEnviron("runtime-bigline", ""),
	})
	if res.code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", res.code, res.diag)
	}

	h.mustAwaitFrameCount(2)
	frames := h.snapshotFrames()
	assertContiguous(t, frames)

	// The 64 KiB with no newline is one line, assembled across many reads, and
	// it only becomes a frame when its newline arrives. It is the first line
	// the runtime wrote, not the first frame on the stream: the INIT phase's
	// platform records are ahead of it.
	stdout := framesBySource(frames, initproto.SrcStdout)
	if len(stdout) == 0 {
		t.Fatalf("no stdout frames: %v", frameMessages(frames))
	}
	big := stdout[0]
	if len(big.Msg) != bigLineBytes || strings.Trim(big.Msg, "x") != "" {
		t.Fatalf("first stdout frame is %d bytes of %q…, want %d bytes of x", len(big.Msg), truncate(big.Msg, 8), bigLineBytes)
	}
	final := findFrame(t, frames, "final-line")
	if big.Req != "req-big" || final.Req != "req-big" {
		t.Fatalf("lines written during the invocation were attributed to %q/%q", big.Req, final.Req)
	}

	calls := h.snapshotCalls()
	if len(calls) != 1 {
		t.Fatalf("host saw %d results, want 1", len(calls))
	}
	if got := seqOf(t, calls[0].logSeq); got < final.Seq {
		t.Fatalf("the response carried %s, but the invocation's last line is seq %d — it was forwarded before the pipe was drained", calls[0].logSeq, final.Seq)
	}
}

func TestInitDrainsBothStreamsBeforeForwarding(t *testing.T) {
	const pairs = 200

	h := newFakeHost(t)
	h.enqueue(invocation{id: "req-mix", payload: "{}"})

	res := runInit(t, h, options{
		child:   childCmd(),
		environ: childEnviron("runtime-interleave", strconv.Itoa(pairs)),
	})
	if res.code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", res.code, res.diag)
	}

	h.mustAwaitFrameCount(2 * pairs)
	frames := h.snapshotFrames()
	assertContiguous(t, frames)

	out := framesBySource(frames, initproto.SrcStdout)
	errs := framesBySource(frames, initproto.SrcStderr)
	if len(out) != pairs || len(errs) != pairs {
		t.Fatalf("got %d stdout and %d stderr frames, want %d of each", len(out), len(errs), pairs)
	}
	// Order within a stream is the order the process wrote it.
	for i := range pairs {
		if want := "out-" + strconv.Itoa(i+1); out[i].Msg != want {
			t.Fatalf("stdout frame %d = %q, want %q", i, out[i].Msg, want)
		}
		if want := "err-" + strconv.Itoa(i+1); errs[i].Msg != want {
			t.Fatalf("stderr frame %d = %q, want %q", i, errs[i].Msg, want)
		}
	}

	calls := h.snapshotCalls()
	if len(calls) != 1 {
		t.Fatalf("host saw %d results, want 1", len(calls))
	}
	last := frames[len(frames)-1]
	if got := seqOf(t, calls[0].logSeq); got < last.Seq {
		t.Fatalf("the response carried seq %d, but %d lines were published for the invocation", got, last.Seq)
	}
}

func TestInitDrainsBeforeForwardingAnInvocationError(t *testing.T) {
	h := newFakeHost(t)
	h.enqueue(invocation{id: "req-err", payload: "{}"})

	res := runInit(t, h, options{
		child:   childCmd(),
		environ: childEnviron("runtime-error", ""),
	})
	if res.code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", res.code, res.diag)
	}

	h.mustAwaitFrameCount(1)
	frames := h.snapshotFrames()
	boom := findFrame(t, frames, "handler blew up")
	if boom.Req != "req-err" || boom.Src != initproto.SrcStderr {
		t.Fatalf("the error line was recorded as %+v", boom)
	}

	calls := h.snapshotCalls()
	if len(calls) != 1 || calls[0].kind != "error" {
		t.Fatalf("host saw %+v, want one /error call", calls)
	}
	if got := seqOf(t, calls[0].logSeq); got < boom.Seq {
		t.Fatalf("the error carried seq %d, but the handler's last line is seq %d", got, boom.Seq)
	}
}

func TestInitAnnotatesTeedLinesOnlyWhenAskedTo(t *testing.T) {
	h := newFakeHost(t)
	h.enqueue(invocation{id: "req-1", payload: "{}"})
	h.enqueue(invocation{id: "req-2", payload: "{}"})

	res := runInit(t, h, options{
		child:    childCmd(),
		environ:  childEnviron("runtime-attribution", ""),
		annotate: true,
	})
	if res.code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", res.code, res.diag)
	}

	if !strings.Contains(res.stdout, "req=req-1 inv-1-out\n") {
		t.Errorf("annotated stdout does not carry the request id:\n%s", res.stdout)
	}
	if !strings.Contains(res.stderr, "req=req-1 inv-1-err\n") {
		t.Errorf("annotated stderr does not carry the request id:\n%s", res.stderr)
	}
	// A line outside an invocation has nothing to annotate with and is teed
	// unchanged.
	if !strings.Contains(res.stdout, "\ninit-line\n") && !strings.HasPrefix(res.stdout, "init-line\n") {
		t.Errorf("the INIT-phase line was annotated or lost:\n%s", res.stdout)
	}

	// The frames are unaffected either way: annotation is a property of the
	// container's own stdout, not of the protocol.
	h.mustAwaitFrameCount(7)
	if f := findFrame(t, h.snapshotFrames(), "inv-1-out"); f.Req != "req-1" {
		t.Fatalf("frame = %+v", f)
	}
}

func TestInitStartsExtensionsAndTagsTheirOutput(t *testing.T) {
	dir := t.TempDir()
	writeExtension(t, dir, "alpha", "alpha is up")
	writeExtension(t, dir, "beta", "beta is up")
	// Not executable: not an extension.
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("#!/bin/sh\necho nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := newFakeHost(t)
	res := runInit(t, h, options{
		child:         childCmd(),
		environ:       childEnviron("print-then-exit", "0"),
		extensionsDir: dir,
	})
	if res.code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", res.code, res.diag)
	}

	h.mustAwaitFrameCount(5) // 3 from the runtime, one per extension
	frames := h.snapshotFrames()

	for _, name := range []string{"alpha", "beta"} {
		src := initproto.ExtensionSrc(name)
		got := framesBySource(frames, src)
		if len(got) != 1 || got[0].Msg != name+" is up" {
			t.Errorf("frames for %s = %v", src, frameMessages(got))
		}
		if !strings.Contains(res.diag, "extension "+name+" started pid=") {
			t.Errorf("the diagnostics do not record %s starting:\n%s", name, res.diag)
		}
	}
	for _, f := range frames {
		if strings.Contains(f.Msg, "nope") {
			t.Errorf("a non-executable file in the extensions directory was run: %q", f.Msg)
		}
	}
	// The extension's own output is teed like everything else.
	if !strings.Contains(res.stdout, "alpha is up\n") {
		t.Errorf("the extension's output did not reach the container's stdout:\n%s", res.stdout)
	}
}

// writeExtension drops a minimal external extension into dir: it announces
// itself and then waits to be told to stop, which is the shape of a real one.
func writeExtension(t *testing.T, dir, name, line string) {
	t.Helper()
	script := "#!/bin/sh\necho '" + line + "'\nexec sleep 5\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func seqOf(t *testing.T, header string) uint64 {
	t.Helper()
	if header == "" {
		t.Fatal("the forwarded request carried no " + initproto.HeaderLogSeq + " header")
	}
	n, err := strconv.ParseUint(header, 10, 64)
	if err != nil {
		t.Fatalf("%s = %q: %v", initproto.HeaderLogSeq, header, err)
	}
	return n
}

// ─── the INIT phase's platform records ───────────────────────────────────────

// The init publishes the three platform-telemetry records that describe the
// INIT phase, and it publishes them *on the frame stream*, which is what makes
// their position exact rather than raced: platform.initStart before the
// environment could have printed anything, platform.initRuntimeDone and
// platform.initReport after all of it and before the GET /next whose
// X-Overcast-Log-Seq the host waits for. The host therefore has every one of
// them in hand before it writes the first invocation's START, with no
// synchronisation beyond the sequence the log lines already rely on.
func TestInitPublishesTheInitPhaseRecords(t *testing.T) {
	h := newFakeHost(t)
	h.enqueue(invocation{id: "req-1", payload: `{"n":1}`, before: func() { h.awaitMessage("init-line") }})
	h.enqueue(invocation{id: "req-2", payload: `{"n":2}`, before: func() { h.awaitMessage("between-1") }})

	res := runInit(t, h, options{
		child:   childCmd(),
		environ: childEnviron("runtime-attribution", ""),
	})
	if res.code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", res.code, res.diag)
	}

	h.mustAwaitFrameCount(10) // 7 lines + 3 records
	frames := h.snapshotFrames()
	assertContiguous(t, frames)

	records := recordFrames(frames)
	if len(records) != 3 {
		t.Fatalf("got %d records, want 3: %v", len(records), recordSummaries(records))
	}
	wantTypes := []string{initproto.RecInitStart, initproto.RecInitRuntimeDone, initproto.RecInitReport}
	for i, want := range wantTypes {
		if records[i].Rec.Type != want {
			t.Fatalf("record %d is %q, want %q: %v", i, records[i].Rec.Type, want, recordSummaries(records))
		}
		if records[i].Msg != "" || records[i].Src != "" || records[i].Req != "" {
			t.Errorf("record %d is not a bare record frame: %+v", i, records[i])
		}
		if records[i].T == 0 {
			t.Errorf("record %d has no timestamp", i)
		}
	}
	start, runtimeDone, report := records[0], records[1], records[2]

	if got := runtimeDone.Rec.Status; got != initproto.StatusSuccess {
		t.Errorf("initRuntimeDone status = %q, want %q", got, initproto.StatusSuccess)
	}
	if got := report.Rec.Status; got != initproto.StatusSuccess {
		t.Errorf("initReport status = %q, want %q", got, initproto.StatusSuccess)
	}
	if report.Rec.DurationMs <= 0 {
		t.Errorf("initReport durationMs = %v, want a positive measurement", report.Rec.DurationMs)
	}
	if runtimeDone.Rec.DurationMs != 0 {
		t.Errorf("initRuntimeDone carries a duration (%v); only initReport does", runtimeDone.Rec.DurationMs)
	}

	// Position, which is the whole point of shipping these as frames.
	initLine := findFrame(t, frames, "init-line")
	firstInvocationLine := findFrame(t, frames, "inv-1-out")
	if start.Seq >= initLine.Seq {
		t.Errorf("initStart is seq %d and the INIT phase's own output seq %d — initStart must come first", start.Seq, initLine.Seq)
	}
	if runtimeDone.Seq <= initLine.Seq {
		t.Errorf("initRuntimeDone is seq %d and the INIT phase's output seq %d — the phase closes after its output", runtimeDone.Seq, initLine.Seq)
	}
	if report.Seq >= firstInvocationLine.Seq {
		t.Errorf("initReport is seq %d and the first invocation's output seq %d", report.Seq, firstInvocationLine.Seq)
	}

	// And the seq the host is told to wait for before it writes the first
	// START covers all three of them.
	nextSeqs := h.snapshotNextSeqs()
	if len(nextSeqs) == 0 {
		t.Fatal("the host never saw a GET /next")
	}
	if got := seqOf(t, nextSeqs[0]); got < report.Seq {
		t.Errorf("the first /next carried seq %d, but initReport is seq %d — the host would write START before it", got, report.Seq)
	}
}

// A runtime that dies before it ever asks for work never finished INIT, and
// the init says exactly that: the phase is closed with an error status, after
// whatever the runtime managed to print on its way out. Both records are WARN
// at AWS's system-log-level mapping, so this is the one init-phase record a
// default log stream shows.
func TestInitReportsAFailedInitPhaseWhenTheRuntimeNeverPolls(t *testing.T) {
	h := newFakeHost(t)
	res := runInit(t, h, options{
		child:   childCmd(),
		environ: childEnviron("print-then-exit", "1"),
	})
	if res.code != 1 {
		t.Fatalf("exit code = %d, want 1 (the child's own)\n%s", res.code, res.diag)
	}

	h.mustAwaitFrameCount(6) // 3 lines + 3 records
	frames := h.snapshotFrames()
	assertContiguous(t, frames)

	records := recordFrames(frames)
	if len(records) != 3 {
		t.Fatalf("got %d records, want 3: %v", len(records), recordSummaries(records))
	}
	if records[0].Rec.Type != initproto.RecInitStart {
		t.Fatalf("the first record is %q, want %q", records[0].Rec.Type, initproto.RecInitStart)
	}
	for _, f := range records[1:] {
		if f.Rec.Status != initproto.StatusError {
			t.Errorf("%s status = %q, want %q", f.Rec.Type, f.Rec.Status, initproto.StatusError)
		}
	}

	// The failure is reported after the runtime's dying words, not before.
	last := findFrame(t, frames, "err-one")
	if records[1].Seq <= last.Seq {
		t.Errorf("initRuntimeDone is seq %d and the runtime's last line seq %d", records[1].Seq, last.Seq)
	}
}

// recordFrames returns the platform-record frames, in sequence order.
func recordFrames(frames []initproto.Frame) []initproto.Frame {
	var out []initproto.Frame
	for _, f := range frames {
		if f.Rec.Type != "" {
			out = append(out, f)
		}
	}
	return out
}

func recordSummaries(frames []initproto.Frame) []string {
	out := make([]string, 0, len(frames))
	for _, f := range frames {
		out = append(out, fmt.Sprintf("%d:%s:%s:%.3fms", f.Seq, f.Rec.Type, f.Rec.Status, f.Rec.DurationMs))
	}
	return out
}
