package msk

// readiness_test.go — a cluster whose broker never comes up must reach a
// terminal state carrying why, and a port that merely accepts is not a broker.

import (
	"context"
	"encoding/binary"
	"io"
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

// deadAddr returns a loopback address nothing is listening on, so a whole
// budget's worth of attempts is refused instantly.
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

// acceptOnly is Docker's published port with nothing behind it: connections are
// accepted and dropped. This is what a bare dial could not tell from a broker.
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

// serveBroker answers ApiVersions the way a running broker does: it echoes the
// correlation ID the request carried and reports error code errorCode. Serving
// the real reply rather than any bytes at all is the point — the probe checks
// both fields.
func serveBroker(t *testing.T, errorCode int16) (string, int) {
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
				size := make([]byte, 4)
				if _, err := io.ReadFull(c, size); err != nil {
					return
				}
				req := make([]byte, binary.BigEndian.Uint32(size))
				if _, err := io.ReadFull(c, req); err != nil {
					return
				}
				// Request header v1: api_key, api_version, correlation_id.
				correlationID := binary.BigEndian.Uint32(req[4:8])

				body := binary.BigEndian.AppendUint32(nil, correlationID)
				body = binary.BigEndian.AppendUint16(body, uint16(errorCode)) //nolint:gosec // fixed-width protocol field
				body = binary.BigEndian.AppendUint32(body, 0)                 // api_keys: empty array
				_, _ = c.Write(append(binary.BigEndian.AppendUint32(nil, uint32(len(body))), body...))
			}(conn)
		}
	}()
	return "127.0.0.1", ln.Addr().(*net.TCPAddr).Port
}

func runBudget(t *testing.T, h *Handler, clk *clock.Mock) {
	t.Helper()
	steps := int(brokerReadiness.Budget/brokerReadiness.Interval) + 4
	for range steps {
		h.scheduler.AdvanceAndSettle(clk, brokerReadiness.Interval)
	}
}

const testClusterARN = "arn:aws:kafka:us-east-1:123456789012:cluster/demo/1111-2222-3333-4"

func TestClusterWhoseBrokerNeverAnswers_reachesFAILEDWithAReason(t *testing.T) {
	h, clk, ctx := newReadinessHandler(t)
	host, port := deadAddr(t)

	if aerr := h.store.putCluster(ctx, &Cluster{
		ClusterArn: testClusterARN, ClusterName: "demo", State: "CREATING",
	}); aerr != nil {
		t.Fatalf("seed cluster: %s", aerr.Message)
	}

	h.scheduleHealthCheck(testClusterARN, host, port)
	runBudget(t, h, clk)

	got, aerr := h.store.getCluster(ctx, testClusterARN)
	if aerr != nil {
		t.Fatalf("getCluster: %s", aerr.Message)
	}
	if got.State == "CREATING" {
		t.Fatal("State is still CREATING: nothing is coming to change it, so " +
			"`aws kafka wait cluster-active` spins until it gives up with nothing to say")
	}
	if got.State != stateFailed {
		t.Fatalf("State = %q, want %q", got.State, stateFailed)
	}
	if got.StateInfo == nil {
		t.Fatal("StateInfo is nil: a FAILED cluster must carry why, the way AWS models it")
	}
	if got.StateInfo.Code != stateInfoUnhealthy {
		t.Errorf("StateInfo.Code = %q, want %q", got.StateInfo.Code, stateInfoUnhealthy)
	}
	if !strings.Contains(got.StateInfo.Message, "127.0.0.1") {
		t.Errorf("StateInfo.Message = %q, want it to name the broker that never answered",
			got.StateInfo.Message)
	}
}

func TestClusterWhosePortOnlyAccepts_isNotReportedActive(t *testing.T) {
	h, clk, ctx := newReadinessHandler(t)
	host, port := acceptOnly(t)

	if aerr := h.store.putCluster(ctx, &Cluster{
		ClusterArn: testClusterARN, ClusterName: "demo", State: "CREATING",
	}); aerr != nil {
		t.Fatalf("seed cluster: %s", aerr.Message)
	}

	h.scheduleHealthCheck(testClusterARN, host, port)
	// One attempt: a dial-based check promoted the cluster on its first
	// success, and this connection succeeds.
	h.scheduler.AdvanceAndSettle(clk, brokerReadiness.FirstDelay)

	got, _ := h.store.getCluster(ctx, testClusterARN)
	if got.State != "CREATING" {
		t.Fatalf("State = %q, want it still %q: the port accepted but no broker answered ApiVersions",
			got.State, "CREATING")
	}
}

func TestClusterWhoseBrokerAnswers_becomesACTIVE(t *testing.T) {
	h, clk, ctx := newReadinessHandler(t)
	host, port := serveBroker(t, 0)

	if aerr := h.store.putCluster(ctx, &Cluster{
		ClusterArn: testClusterARN, ClusterName: "demo", State: "CREATING",
		StateInfo: &StateInfo{Code: stateInfoUnhealthy, Message: "an earlier failure"},
	}); aerr != nil {
		t.Fatalf("seed cluster: %s", aerr.Message)
	}

	h.scheduleHealthCheck(testClusterARN, host, port)
	// The first scheduled attempt and nothing more: the healthy path must not
	// have got slower.
	h.scheduler.AdvanceAndSettle(clk, brokerReadiness.FirstDelay)

	got, _ := h.store.getCluster(ctx, testClusterARN)
	if got.State != "ACTIVE" {
		t.Fatalf("State = %q, want ACTIVE after one attempt against a broker answering ApiVersions", got.State)
	}
	if got.StateInfo != nil {
		t.Errorf("StateInfo = %+v, want it cleared once the cluster is ACTIVE", got.StateInfo)
	}
}

func TestProbeBroker(t *testing.T) {
	t.Run("error code NONE", func(t *testing.T) {
		host, port := serveBroker(t, 0)
		if err := probeBroker(context.Background(), net.JoinHostPort(host, strconv.Itoa(port))); err != nil {
			t.Fatalf("probeBroker = %v, want nil", err)
		}
	})
	t.Run("UNSUPPORTED_VERSION still proves a broker answered", func(t *testing.T) {
		host, port := serveBroker(t, 35)
		if err := probeBroker(context.Background(), net.JoinHostPort(host, strconv.Itoa(port))); err != nil {
			t.Fatalf("probeBroker = %v, want nil: a broker too new for v0 still answered", err)
		}
	})
	t.Run("a refusing broker is not ready", func(t *testing.T) {
		host, port := serveBroker(t, 58) // CLUSTER_AUTHORIZATION_FAILED
		if err := probeBroker(context.Background(), net.JoinHostPort(host, strconv.Itoa(port))); err == nil {
			t.Fatal("probeBroker = nil, want an error for a non-zero Kafka error code")
		}
	})
	t.Run("a port that only accepts is not a broker", func(t *testing.T) {
		host, port := acceptOnly(t)
		if err := probeBroker(context.Background(), net.JoinHostPort(host, strconv.Itoa(port))); err == nil {
			t.Fatal("probeBroker = nil for a port that accepts and answers nothing — " +
				"that is the Docker port-proxy case a bare dial could not tell apart")
		}
	})
	t.Run("nothing listening", func(t *testing.T) {
		host, port := deadAddr(t)
		if err := probeBroker(context.Background(), net.JoinHostPort(host, strconv.Itoa(port))); err == nil {
			t.Fatal("probeBroker = nil for a refused connection")
		}
	})
}
