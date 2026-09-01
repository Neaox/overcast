package lambda

// report_memory_test.go — pins the REPORT memory model: Max Memory Used is
// the environment's monotonic peak (as on AWS), sampled off the invoke
// response path with a bounded wait.

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/docker"
)

// memStatsInstance returns a containerInstance whose Docker stats endpoint
// reports the given (settable) memory usage.
func memStatsInstance(t *testing.T, usageBytes *atomic.Int64) *containerInstance {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/stats") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"memory_stats":{"usage":` + strconv.FormatInt(usageBytes.Load(), 10) + `}}`))
	}))
	t.Cleanup(srv.Close)
	return &containerInstance{
		id:     "cafebabe1234deadbeef",
		docker: docker.NewClient("tcp://"+strings.TrimPrefix(srv.URL, "http://"), zap.NewNop()),
		logger: zap.NewNop(),
		clk:    clock.New(),
	}
}

func TestReportMemoryMB_isMonotonicPeak(t *testing.T) {
	// Given: an instance whose container currently uses 96 MiB.
	var usage atomic.Int64
	usage.Store(96 * 1024 * 1024)
	ci := memStatsInstance(t, &usage)

	// When: a sample is folded in.
	if got := ci.reportMemoryMB(ci.startMemorySample()); got != 96 {
		t.Fatalf("first report = %d MB, want 96", got)
	}

	// Then: a later, lower sample never lowers the reported peak (AWS's Max
	// Memory Used is the environment's running maximum)…
	usage.Store(32 * 1024 * 1024)
	if got := ci.reportMemoryMB(ci.startMemorySample()); got != 96 {
		t.Fatalf("report after lower sample = %d MB, want 96 (monotonic peak)", got)
	}

	// …and a higher sample raises it.
	usage.Store(128 * 1024 * 1024)
	if got := ci.reportMemoryMB(ci.startMemorySample()); got != 128 {
		t.Fatalf("report after higher sample = %d MB, want 128", got)
	}
}

func TestReportMemoryMB_unresolvedSampleFallsBackToPeak(t *testing.T) {
	// Given: an instance with a recorded peak and a sample that never resolves.
	ci := &containerInstance{clk: clock.New()}
	ci.peakMemMB.Store(64)
	stuck := make(chan struct{}) // never closed

	// When: the REPORT value is read.
	started := time.Now()
	got := ci.reportMemoryMB(stuck)

	// Then: it returns the previous peak after the bounded wait, well before
	// anything resembling a stats stall.
	if got != 64 {
		t.Fatalf("report = %d MB, want 64 (previous peak)", got)
	}
	if waited := time.Since(started); waited > time.Second {
		t.Fatalf("reportMemoryMB waited %v — the wait must be tightly bounded", waited)
	}
}

func TestReportMemoryMB_failedSampleReportsZeroWithoutStalling(t *testing.T) {
	// Given: an instance whose Docker endpoint is unreachable and no peak yet.
	ci := &containerInstance{
		id:     "cafebabe1234deadbeef",
		docker: docker.NewClient("tcp://127.0.0.1:1", zap.NewNop()),
		logger: zap.NewNop(),
		clk:    clock.New(),
	}

	// When/Then: the report is 0 (best-effort, matches previous behavior) and
	// does not hang.
	if got := ci.reportMemoryMB(ci.startMemorySample()); got != 0 {
		t.Fatalf("report = %d MB, want 0 for unreachable daemon", got)
	}
}
