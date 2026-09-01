package elasticache

// readiness_test.go — the two defects this file exists to hold closed.
//
//	(a) A replication group whose endpoint never answered was transitioned to
//	    "available" when its retries ran out (#881). Thirty refused connections
//	    were treated as grounds for reporting a working cache.
//	(b) A bare TCP dial was the readiness signal for every cache, so a port that
//	    accepted — which Docker's published port does as soon as the proxy is
//	    up, before the engine has bound anything — was enough to report the
//	    engine ready.

import (
	"bufio"
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/middleware"
	"github.com/overcast-sh/overcast/internal/state"
)

// newReadinessHandler is a metadata-only handler on a mock clock. No Docker: a
// readiness watch is scheduled with an address and settles the record from what
// answers there, which is the whole of what these tests drive.
func newReadinessHandler(t *testing.T) (*Handler, *clock.Mock, context.Context) {
	t.Helper()
	clk := clock.NewMock()
	svc := New(&config.Config{Region: "us-east-1", AccountID: "123456789012"},
		state.NewMemoryStore(), zap.NewNop(), clk)
	h := svc.handler
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.scheduler.Stop(ctx)
	})
	return h, clk, middleware.ContextWithRegion(context.Background(), "us-east-1")
}

// deadAddr returns a loopback host and port that nothing is listening on: the
// listener is opened only to be given a port the OS is not handing to anyone
// else, then closed. A dial there is refused immediately, so a whole budget's
// worth of attempts costs no wall-clock time.
func deadAddr(t *testing.T) (string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("close the reserved listener: %v", err)
	}
	return "127.0.0.1", port
}

// acceptOnly stands in for Docker's published port before the engine behind it
// is serving: the connection is accepted and then dropped without a byte of
// protocol. This is the shape defect (b) is about.
func acceptOnly(t *testing.T) (string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	return "127.0.0.1", ln.Addr().(*net.TCPAddr).Port
}

// acceptAndHold accepts and then says nothing at all, holding the connection
// open — the closest reproduction of a port proxy in front of a process that
// has not started listening. Costs one probe timeout per attempt, so tests
// using it drive a single attempt.
func acceptAndHold(t *testing.T) (string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	held := make(chan net.Conn, 8)
	t.Cleanup(func() {
		_ = ln.Close()
		close(held)
		for c := range held {
			_ = c.Close()
		}
	})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			select {
			case held <- conn:
			default:
				_ = conn.Close()
			}
		}
	}()
	return "127.0.0.1", ln.Addr().(*net.TCPAddr).Port
}

// serveEngine answers one command per connection with reply, which is what a
// running Redis (`+PONG`) or memcached (`VERSION 1.6.0`) does.
func serveEngine(t *testing.T, reply string) (string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close() //nolint:errcheck // test double
				// Read whatever arrives — RESP arrays span lines, memcached's
				// `version` does not — then answer. The probe only reads the
				// first line back, so one write settles it either way.
				_, _ = bufio.NewReader(c).ReadString('\n')
				_, _ = c.Write([]byte(reply))
			}(conn)
		}
	}()
	return "127.0.0.1", ln.Addr().(*net.TCPAddr).Port
}

// runBudget advances the mock clock far enough to exhaust a cacheReadiness
// budget, settling each attempt as it fires. One Interval per step, because
// each attempt schedules the next from inside itself.
func runBudget(t *testing.T, h *Handler, clk *clock.Mock) {
	t.Helper()
	steps := int(cacheReadiness.Budget/cacheReadiness.Interval) + 4
	for range steps {
		h.scheduler.AdvanceAndSettle(clk, cacheReadiness.Interval)
	}
}

// ── (a) the replication-group false-available ────────────────────────────────

func TestReplicationGroupThatNeverAnswers_isNotReportedAvailable(t *testing.T) {
	h, clk, ctx := newReadinessHandler(t)
	const id = "rg-never-answers"
	host, port := deadAddr(t)

	if aerr := h.store.putReplicationGroup(ctx, &ReplicationGroup{
		ReplicationGroupId: id, Status: "creating", Engine: "redis",
		ConfigurationEndpoint: &ClusterEndpoint{Address: host, Port: port},
	}); aerr != nil {
		t.Fatalf("seed replication group: %s", aerr.Message)
	}

	h.scheduleReplicationGroupHealthCheck("us-east-1", id, host, port)
	runBudget(t, h, clk)

	got, aerr := h.store.getReplicationGroup(ctx, id)
	if aerr != nil {
		t.Fatalf("getReplicationGroup: %s", aerr.Message)
	}
	if got.Status == "available" {
		t.Fatalf("Status = %q: the group reports a working cache with nothing listening on %s:%d (#881)",
			got.Status, host, port)
	}
	if got.Status != statusCreateFailed {
		t.Fatalf("Status = %q, want %q — AWS's terminal failure for a replication group",
			got.Status, statusCreateFailed)
	}
	if !strings.Contains(got.StatusReason, "did not become ready") {
		t.Errorf("StatusReason = %q, want it to say the group never became ready", got.StatusReason)
	}
	if !strings.Contains(got.StatusReason, "127.0.0.1") {
		t.Errorf("StatusReason = %q, want it to name the endpoint that never answered", got.StatusReason)
	}
}

func TestCacheClusterThatNeverAnswers_isNotReportedAvailable(t *testing.T) {
	h, clk, ctx := newReadinessHandler(t)
	const id = "cluster-never-answers"
	host, port := deadAddr(t)

	if aerr := h.store.putCacheCluster(ctx, &CacheCluster{
		CacheClusterId: id, CacheClusterStatus: "creating", Engine: "redis",
	}); aerr != nil {
		t.Fatalf("seed cache cluster: %s", aerr.Message)
	}

	h.scheduleHealthCheck("us-east-1", id, host, port)
	runBudget(t, h, clk)

	got, aerr := h.store.getCacheCluster(ctx, id)
	if aerr != nil {
		t.Fatalf("getCacheCluster: %s", aerr.Message)
	}
	// AWS documents no create-failed for a cache cluster; incompatible-network
	// is the terminal value the CacheClusterAvailable waiter fails on.
	if got.CacheClusterStatus != statusClusterUnreachable {
		t.Fatalf("CacheClusterStatus = %q, want %q", got.CacheClusterStatus, statusClusterUnreachable)
	}
	if got.StatusReason == "" {
		t.Error("StatusReason is empty: a failed cluster must say why")
	}
}

func TestServerlessCacheThatNeverAnswers_isNotReportedAvailable(t *testing.T) {
	h, clk, ctx := newReadinessHandler(t)
	const name = "serverless-never-answers"
	host, port := deadAddr(t)

	if aerr := h.store.putServerlessCache(ctx, &ServerlessCache{
		ServerlessCacheName: name, Status: "creating", Engine: "valkey",
	}); aerr != nil {
		t.Fatalf("seed serverless cache: %s", aerr.Message)
	}

	h.scheduleServerlessHealthCheck("us-east-1", name, host, port)
	runBudget(t, h, clk)

	got, aerr := h.store.getServerlessCache(ctx, name)
	if aerr != nil {
		t.Fatalf("getServerlessCache: %s", aerr.Message)
	}
	if got.Status != statusCreateFailed {
		t.Fatalf("Status = %q, want %q", got.Status, statusCreateFailed)
	}
	if got.StatusReason == "" {
		t.Error("StatusReason is empty: a failed cache must say why")
	}
}

// ── (b) a port that accepts is not an engine that is ready ───────────────────

func TestCacheClusterWhosePortAcceptsButEngineIsSilent_isNotReportedAvailable(t *testing.T) {
	h, clk, ctx := newReadinessHandler(t)
	const id = "cluster-proxy-only"
	host, port := acceptAndHold(t)

	if aerr := h.store.putCacheCluster(ctx, &CacheCluster{
		CacheClusterId: id, CacheClusterStatus: "creating", Engine: "redis",
	}); aerr != nil {
		t.Fatalf("seed cache cluster: %s", aerr.Message)
	}

	h.scheduleHealthCheck("us-east-1", id, host, port)
	// One attempt is the whole test: a dial-based check promoted the cluster on
	// its first success, and this connection succeeds.
	h.scheduler.AdvanceAndSettle(clk, cacheReadiness.FirstDelay)

	got, aerr := h.store.getCacheCluster(ctx, id)
	if aerr != nil {
		t.Fatalf("getCacheCluster: %s", aerr.Message)
	}
	if got.CacheClusterStatus != "creating" {
		t.Fatalf("CacheClusterStatus = %q, want it still %q: the port accepted but no engine answered PING",
			got.CacheClusterStatus, "creating")
	}
}

func TestReplicationGroupWhosePortOnlyAccepts_endsCreateFailed(t *testing.T) {
	h, clk, ctx := newReadinessHandler(t)
	const id = "rg-proxy-only"
	host, port := acceptOnly(t)

	if aerr := h.store.putReplicationGroup(ctx, &ReplicationGroup{
		ReplicationGroupId: id, Status: "creating", Engine: "redis",
	}); aerr != nil {
		t.Fatalf("seed replication group: %s", aerr.Message)
	}

	h.scheduleReplicationGroupHealthCheck("us-east-1", id, host, port)
	runBudget(t, h, clk)

	got, aerr := h.store.getReplicationGroup(ctx, id)
	if aerr != nil {
		t.Fatalf("getReplicationGroup: %s", aerr.Message)
	}
	if got.Status != statusCreateFailed {
		t.Fatalf("Status = %q, want %q: every connection was accepted and none was answered",
			got.Status, statusCreateFailed)
	}
}

// ── the healthy path still reaches available, on the first attempt ───────────

func TestCacheClusterWhoseEngineAnswers_becomesAvailable(t *testing.T) {
	h, clk, ctx := newReadinessHandler(t)
	const id = "cluster-healthy"
	host, port := serveEngine(t, "+PONG\r\n")

	if aerr := h.store.putCacheCluster(ctx, &CacheCluster{
		CacheClusterId: id, CacheClusterStatus: "creating", Engine: "redis",
		StatusReason: "an earlier failure that must not survive coming up",
	}); aerr != nil {
		t.Fatalf("seed cache cluster: %s", aerr.Message)
	}

	h.scheduleHealthCheck("us-east-1", id, host, port)
	// The first scheduled attempt and nothing more: the healthy path must not
	// have got slower.
	h.scheduler.AdvanceAndSettle(clk, cacheReadiness.FirstDelay)

	got, aerr := h.store.getCacheCluster(ctx, id)
	if aerr != nil {
		t.Fatalf("getCacheCluster: %s", aerr.Message)
	}
	if got.CacheClusterStatus != "available" {
		t.Fatalf("CacheClusterStatus = %q, want %q after one attempt against an engine answering PING",
			got.CacheClusterStatus, "available")
	}
	if got.StatusReason != "" {
		t.Errorf("StatusReason = %q, want it cleared once the cluster is available", got.StatusReason)
	}
}

func TestMemcachedClusterWhoseEngineAnswers_becomesAvailable(t *testing.T) {
	h, clk, ctx := newReadinessHandler(t)
	const id = "cluster-memcached"
	host, port := serveEngine(t, "VERSION 1.6.21\r\n")

	if aerr := h.store.putCacheCluster(ctx, &CacheCluster{
		CacheClusterId: id, CacheClusterStatus: "creating", Engine: "memcached",
	}); aerr != nil {
		t.Fatalf("seed cache cluster: %s", aerr.Message)
	}

	h.scheduleHealthCheck("us-east-1", id, host, port)
	h.scheduler.AdvanceAndSettle(clk, cacheReadiness.FirstDelay)

	got, _ := h.store.getCacheCluster(ctx, id)
	if got.CacheClusterStatus != "available" {
		t.Fatalf("CacheClusterStatus = %q, want %q: memcached answered `version`",
			got.CacheClusterStatus, "available")
	}
}

// ── a record that moved on is never written over ─────────────────────────────

func TestDeletedReplicationGroupIsNotFailedByAStragglingWatch(t *testing.T) {
	h, clk, ctx := newReadinessHandler(t)
	const id = "rg-deleted-midflight"
	host, port := deadAddr(t)

	if aerr := h.store.putReplicationGroup(ctx, &ReplicationGroup{
		ReplicationGroupId: id, Status: "deleting", Engine: "redis",
	}); aerr != nil {
		t.Fatalf("seed replication group: %s", aerr.Message)
	}

	h.scheduleReplicationGroupHealthCheck("us-east-1", id, host, port)
	runBudget(t, h, clk)

	got, aerr := h.store.getReplicationGroup(ctx, id)
	if aerr != nil {
		t.Fatalf("getReplicationGroup: %s", aerr.Message)
	}
	if got.Status != "deleting" {
		t.Fatalf("Status = %q, want it left at %q: the delete decided the outcome",
			got.Status, "deleting")
	}
}

// ── the probe itself ─────────────────────────────────────────────────────────

func TestProbeEngine(t *testing.T) {
	tests := []struct {
		name    string
		engine  string
		reply   string
		wantErr bool
	}{
		{name: "redis PONG", engine: "redis", reply: "+PONG\r\n"},
		{name: "valkey PONG", engine: "valkey", reply: "+PONG\r\n"},
		{name: "redis NOAUTH is still a serving engine", engine: "redis",
			reply: "-NOAUTH Authentication required.\r\n"},
		{name: "redis LOADING is not ready", engine: "redis",
			reply: "-LOADING Redis is loading the dataset in memory\r\n", wantErr: true},
		{name: "redis BUSY is not ready", engine: "redis",
			reply: "-BUSY Redis is busy running a script\r\n", wantErr: true},
		{name: "not a Redis reply at all", engine: "redis", reply: "HTTP/1.1 200 OK\r\n", wantErr: true},
		{name: "memcached VERSION", engine: "memcached", reply: "VERSION 1.6.21\r\n"},
		{name: "memcached ERROR", engine: "memcached", reply: "ERROR\r\n", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			host, port := serveEngine(t, tc.reply)
			err := probeEngine(context.Background(), tc.engine, net.JoinHostPort(host, strconv.Itoa(port)))
			if tc.wantErr && err == nil {
				t.Fatalf("probeEngine(%q) = nil, want an error for reply %q", tc.engine, tc.reply)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("probeEngine(%q) = %v, want nil for reply %q", tc.engine, err, tc.reply)
			}
		})
	}
}

func TestProbeEngineRejectsAPortThatOnlyAccepts(t *testing.T) {
	host, port := acceptOnly(t)
	if err := probeEngine(context.Background(), "redis", net.JoinHostPort(host, strconv.Itoa(port))); err == nil {
		t.Fatal("probeEngine = nil for a port that accepts and answers nothing — " +
			"that is the Docker port-proxy case a bare dial could not tell apart")
	}
}
