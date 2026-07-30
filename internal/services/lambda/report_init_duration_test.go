package lambda

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/containerendpoint"
	"github.com/Neaox/overcast/internal/docker"
)

// newRICBackedContainerInstance returns a containerInstance wired to a live
// Runtime API server with its loopback IP registered, so a fake RIC driven by
// serveOneInvocation can answer invocations. The returned addr is the server's
// host:port for the fake RIC to dial.
func newRICBackedContainerInstance(t *testing.T) (*containerInstance, string) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	srv, err := NewRuntimeAPIServerFromListener(ln, addr, zap.NewNop(), clock.New())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })

	arn := "arn:aws:lambda:us-east-1:000000000000:function:demo"
	// The fake RIC dials from loopback, so the server sees it as 127.0.0.1.
	srv.RegisterContainer("127.0.0.1", arn)

	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000", Port: 4566}
	return &containerInstance{
		id:          "cafebabe1234deadbeef",
		containerIP: "127.0.0.1",
		functionARN: arn,
		memorySize:  128,
		// Unroutable endpoint: currentMemoryMB is best-effort and reports 0
		// rather than reaching a real daemon.
		docker:     docker.NewClient("tcp://127.0.0.1:1", zap.NewNop()),
		runtimeAPI: srv,
		logger:     zap.NewNop(),
		clk:        clock.New(),
		exitNotify: newExitNotifier(),
		endpoint:   containerendpoint.New(cfg, "http://172.18.0.1:4566"),
		healthy:    true,
	}, addr
}

// serveOneInvocation acts as the container's RIC for a single invocation:
// long-poll GET /next, then POST the response back.
func serveOneInvocation(t *testing.T, addr string) {
	t.Helper()
	resp, err := http.Get("http://" + addr + "/2018-06-01/runtime/invocation/next")
	if err != nil {
		t.Errorf("fake RIC GET /next: %v", err)
		return
	}
	reqID := resp.Header.Get("Lambda-Runtime-Aws-Request-Id")
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if reqID == "" {
		t.Error("fake RIC: /next response missing Lambda-Runtime-Aws-Request-Id")
		return
	}
	post, err := http.Post("http://"+addr+"/2018-06-01/runtime/invocation/"+reqID+"/response",
		"application/json", bytes.NewReader([]byte(`"ok"`)))
	if err != nil {
		t.Errorf("fake RIC POST /response: %v", err)
		return
	}
	_, _ = io.Copy(io.Discard, post.Body)
	post.Body.Close()
}

// awsReportWithInit pins the exact AWS field order and format of a cold-start
// REPORT line: Duration, Billed Duration, Memory Size, Max Memory Used,
// Init Duration.
var awsReportWithInit = regexp.MustCompile(
	`^REPORT RequestId: [0-9a-f-]+\tDuration: \d+\.\d{2} ms\tBilled Duration: \d+ ms\tMemory Size: 128 MB\tMax Memory Used: \d+ MB\tInit Duration: \d+\.\d{2} ms$`)

// TestContainerInstanceInvoke_reportsInitDurationOnColdStartOnly pins that an
// on-demand environment's first REPORT carries Init Duration in AWS's field
// order, and that a warm reuse of the same environment omits it — exactly the
// cold/warm distinction real Lambda logs.
func TestContainerInstanceInvoke_reportsInitDurationOnColdStartOnly(t *testing.T) {
	// Given: an on-demand environment (init triggered by an invoke).
	ci, addr := newRICBackedContainerInstance(t)
	ci.initStartedAt = ci.clk.Now()

	// When: the cold-start invocation completes.
	go serveOneInvocation(t, addr)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := ci.Invoke(ctx, []byte(`{}`)); err != nil {
		t.Fatalf("first invoke: %v", err)
	}

	// Then: the REPORT line carries Init Duration, last, in AWS format.
	report := reportLine(t, ci)
	if !awsReportWithInit.MatchString(report) {
		t.Errorf("cold-start REPORT does not match AWS format with Init Duration:\n%q", report)
	}

	// When: the same environment serves a warm invocation.
	go serveOneInvocation(t, addr)
	if _, err := ci.Invoke(ctx, []byte(`{}`)); err != nil {
		t.Fatalf("second invoke: %v", err)
	}

	// Then: the warm REPORT omits Init Duration.
	report = reportLine(t, ci)
	if strings.Contains(report, "Init Duration") {
		t.Errorf("warm REPORT should omit Init Duration, got %q", report)
	}
}

// TestContainerInstanceInvoke_provisionedEnvironmentOmitsInitDuration pins that
// a proactively initialised environment (provisioned concurrency — no
// initStartedAt) never reports Init Duration, matching AWS.
func TestContainerInstanceInvoke_provisionedEnvironmentOmitsInitDuration(t *testing.T) {
	// Given: a provisioned environment — initStartedAt stays zero.
	ci, addr := newRICBackedContainerInstance(t)

	// When: its first invocation completes.
	go serveOneInvocation(t, addr)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := ci.Invoke(ctx, []byte(`{}`)); err != nil {
		t.Fatalf("invoke: %v", err)
	}

	// Then: even the first REPORT omits Init Duration.
	report := reportLine(t, ci)
	if strings.Contains(report, "Init Duration") {
		t.Errorf("provisioned REPORT should omit Init Duration, got %q", report)
	}
}

// TestContainerInstanceInvoke_noInitDurationWhenRICNeverPolled pins that a
// cold start whose RIC never issued GET /next (init never finished) reports no
// Init Duration — there is no init end to measure to.
func TestContainerInstanceInvoke_noInitDurationWhenRICNeverPolled(t *testing.T) {
	// Given: an on-demand environment whose container never polls /next.
	ci := newStalledContainerInstance(t)
	ci.initStartedAt = ci.clk.Now()

	// When: the invocation times out.
	invokeCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := ci.Invoke(invokeCtx, []byte(`{}`)); err == nil {
		t.Fatal("expected timeout error")
	}

	// Then: the timeout REPORT has no Init Duration.
	report := reportLine(t, ci)
	if strings.Contains(report, "Init Duration") {
		t.Errorf("REPORT should omit Init Duration when init never completed, got %q", report)
	}
	if !strings.HasSuffix(report, "Status: timeout") {
		t.Errorf("REPORT should still end in Status: timeout, got %q", report)
	}
}
