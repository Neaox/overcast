//go:build ignore

// Script: bench-startup
// Measures overcast serve cold-start time across all storage backends.
// Works identically on Mac, Linux, and Windows.
//
// Usage:
//
//	go run ./scripts/bench-startup.go              # 5 iterations, all backends
//	go run ./scripts/bench-startup.go -n 10        # 10 iterations
//	go run ./scripts/bench-startup.go -backend wal # single backend
//	go run ./scripts/bench-startup.go -threshold 50 # fail if p50 > 50ms
//
// Output: a summary table with p50, p95, max, and mean for each backend,
// plus the internal startup_duration_ms from /_metrics.
//
// Exit code 1 if any backend's p50 wall time exceeds -threshold (default 80ms).
// This is designed to be called from `make bench-startup` or CI.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"
)

var allBackends = []string{"memory", "hybrid", "wal", "persistent"}

func main() {
	n := flag.Int("n", 5, "Number of cold-start iterations per backend")
	backendCSV := flag.String("backend", "", "Comma-separated backends to test (default: all)")
	threshold := flag.Float64("threshold", 80, "p50 wall-time threshold in ms (exit 1 if exceeded)")
	verbose := flag.Bool("v", false, "Print per-iteration details")
	flag.Parse()

	backends := allBackends
	if *backendCSV != "" {
		backends = strings.Split(*backendCSV, ",")
	}

	binary, err := buildBinary()
	if err != nil {
		fatalf("build: %v", err)
	}

	type result struct {
		Backend    string
		WallTimes  []float64 // ms
		InternalMs []float64 // startup_duration_ms from /_metrics
		HeapBytes  []uint64
		SysBytes   []uint64
	}

	var results []result
	failed := false

	for _, backend := range backends {
		fmt.Printf("\n── %s (%d iterations) ──\n", backend, *n)
		r := result{Backend: backend}

		for i := 0; i < *n; i++ {
			w, m, err := measure(binary, backend, *verbose)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  iteration %d FAILED: %v\n", i+1, err)
				continue
			}
			r.WallTimes = append(r.WallTimes, w)
			r.InternalMs = append(r.InternalMs, m.StartupDurationMs)
			r.HeapBytes = append(r.HeapBytes, m.HeapAllocBytes)
			r.SysBytes = append(r.SysBytes, m.SysBytes)
			if *verbose {
				fmt.Printf("  #%d  wall=%.1fms  internal=%.1fms  heap=%s  sys=%s\n",
					i+1, w, m.StartupDurationMs, fmtBytes(m.HeapAllocBytes), fmtBytes(m.SysBytes))
			}
		}
		results = append(results, r)
	}

	// Summary table
	fmt.Printf("\n%-12s  %8s  %8s  %8s  %8s  │  %8s  %8s  %8s\n",
		"Backend", "p50", "p95", "max", "mean", "int-p50", "heap-p50", "sys-p50")
	fmt.Println(strings.Repeat("─", 92))

	for _, r := range results {
		if len(r.WallTimes) == 0 {
			fmt.Printf("%-12s  %8s\n", r.Backend, "FAILED")
			failed = true
			continue
		}
		wp50, wp95, wmax, wmean := stats(r.WallTimes)
		ip50 := percentile(r.InternalMs, 50)
		hp50 := percentileU64(r.HeapBytes, 50)
		sp50 := percentileU64(r.SysBytes, 50)
		fmt.Printf("%-12s  %7.1fms  %7.1fms  %7.1fms  %7.1fms  │  %7.1fms  %8s  %8s\n",
			r.Backend, wp50, wp95, wmax, wmean, ip50, fmtBytes(hp50), fmtBytes(sp50))
		if wp50 > *threshold {
			fmt.Fprintf(os.Stderr, "FAIL: %s p50 wall time %.1fms exceeds threshold %.0fms\n",
				r.Backend, wp50, *threshold)
			failed = true
		}
	}
	fmt.Println()

	if failed {
		os.Exit(1)
	}
}

// ---- metrics struct (mirrors internal/router/metrics.go) -------------------

type metricsSnapshot struct {
	StartupDurationMs float64 `json:"startup_duration_ms"`
	HeapAllocBytes    uint64  `json:"heap_alloc_bytes"`
	SysBytes          uint64  `json:"sys_bytes"`
}

// ---- build -----------------------------------------------------------------

func buildBinary() (string, error) {
	root, err := os.Getwd()
	if err != nil {
		return "", err
	}
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", err
	}
	binary := filepath.Join(binDir, "overcast")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	fmt.Println("→ building overcast…")
	start := time.Now()
	build := exec.Command("go", "build", "-o", binary, "./cmd/overcast")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return "", err
	}
	fmt.Printf("→ build complete (%.2fs)\n", time.Since(start).Seconds())
	return binary, nil
}

// ---- measure one cold start ------------------------------------------------

func measure(binary, backend string, verbose bool) (wallMs float64, m metricsSnapshot, err error) {
	tmpDir, err := os.MkdirTemp("", "overcast-bench-*")
	if err != nil {
		return 0, m, err
	}
	defer os.RemoveAll(tmpDir)

	port, err := freePort()
	if err != nil {
		return 0, m, fmt.Errorf("find free port: %w", err)
	}

	cmd := exec.Command(binary, "serve")
	cmd.Env = append(os.Environ(),
		"OVERCAST_PORT="+fmt.Sprint(port),
		"OVERCAST_STATE="+backend,
		"OVERCAST_DATA_DIR="+tmpDir,
		"OVERCAST_LOG_LEVEL=error",
		"OVERCAST_DEBUG=false",
	)
	if verbose {
		cmd.Stderr = os.Stderr
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return 0, m, fmt.Errorf("start: %w", err)
	}
	defer func() {
		cmd.Process.Signal(syscall.SIGTERM)
		cmd.Wait()
	}()

	// Poll /_metrics until it responds or timeout.
	metricsURL := fmt.Sprintf("http://127.0.0.1:%d/_metrics", port)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.After(10 * time.Second)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			return 0, m, fmt.Errorf("timeout waiting for /_metrics on port %d", port)
		case <-ticker.C:
			resp, rerr := client.Get(metricsURL)
			if rerr != nil {
				continue
			}
			wall := time.Since(start)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				continue
			}
			if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
				return 0, m, fmt.Errorf("decode /_metrics: %w", err)
			}
			return float64(wall.Microseconds()) / 1000.0, m, nil
		}
	}
}

// ---- stats helpers ---------------------------------------------------------

func stats(vals []float64) (p50, p95, max, mean float64) {
	return percentile(vals, 50), percentile(vals, 95), percentile(vals, 100), avg(vals)
}

func percentile(vals []float64, pct float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := make([]float64, len(vals))
	copy(sorted, vals)
	sort.Float64s(sorted)
	if pct >= 100 {
		return sorted[len(sorted)-1]
	}
	idx := pct / 100.0 * float64(len(sorted)-1)
	lower := int(math.Floor(idx))
	upper := int(math.Ceil(idx))
	if lower == upper {
		return sorted[lower]
	}
	frac := idx - float64(lower)
	return sorted[lower]*(1-frac) + sorted[upper]*frac
}

func percentileU64(vals []uint64, pct float64) uint64 {
	if len(vals) == 0 {
		return 0
	}
	floats := make([]float64, len(vals))
	for i, v := range vals {
		floats[i] = float64(v)
	}
	return uint64(percentile(floats, pct))
}

func avg(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

func fmtBytes(b uint64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port, nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "bench-startup: "+format+"\n", args...)
	os.Exit(1)
}
