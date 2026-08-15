//go:build !slim

package router

// mcp_shutdown_test.go — the MCP SSE stream must let go when the emulator is
// shutting down.
//
// Every other long-lived handler in this router takes the pre-shutdown channel
// and selects on it: see eventsHandler and domainsWatchHandler, both handed
// shutdownCh in New. The MCP SSE stream was the one that did not. Its loop
// selected on the request context alone, so the only thing that could end it
// was the client going away — and a client that has simply gone quiet, which is
// the normal state of an attached MCP session, never does.
//
// What that costs in production: `overcast` shutting down with an MCP client
// attached holds the process open, because http.Server.Shutdown waits for
// in-flight handlers and this one is not coming back.
//
// What it cost in CI: TestRuntimeMCPRoutes_StreamableGetAndLegacySSE_Connect
// hung in httptest.Server.Close — which waits on outstanding requests the same
// way — until the 20-minute test binary timeout killed the whole `internal/router`
// package. It hit two unrelated pull requests in a row (a docs-only change
// among them) before it was chased down, and each failure cost a full CI job.
//
// The connection here is deliberately left open. Closing it would prove only
// that a disconnect ends the stream, which was never in doubt; the case that
// broke is the one where nothing disconnects and shutdown has to end it on its
// own.

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRuntimeMCPRoutes_SSEStreamLetsGoOnPreShutdown(t *testing.T) {
	// The parts variant, not newMCPRouterTestServer: this test drives the
	// shutdown itself and times it, so it cannot leave the hooks to a cleanup.
	srv, preShutdown, cleanup := newMCPRouterTestParts(t)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/_overcast/mcp", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /_overcast/mcp: %v", err)
	}
	// Closed only at the very end, and only so the test leaves no socket
	// behind. The shutdown under test has to work without it.
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// Read the prelude, so the stream is provably established and parked in the
	// handler's select rather than still being set up.
	buf := make([]byte, 64)
	n, readErr := resp.Body.Read(buf)
	if readErr != nil {
		t.Fatalf("read SSE prelude: %v", readErr)
	}
	if !strings.Contains(string(buf[:n]), ": connected") {
		t.Fatalf("SSE prelude = %q, want : connected", string(buf[:n]))
	}

	preShutdown()
	cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelCleanup()
	cleanup(cleanupCtx)

	// Close waits for outstanding requests, so it returns only once the stream
	// handler has returned. Ten seconds is far longer than a released handler
	// needs and far shorter than the 20-minute binary timeout a stuck one costs.
	closed := make(chan struct{})
	go func() {
		srv.Close()
		close(closed)
	}()

	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("the server did not shut down: an MCP SSE stream is still running after " +
			"pre-shutdown, so nothing short of the client disconnecting will end it — " +
			"in production this holds the process open, and in CI it hangs the package " +
			"until the test binary times out")
	}
}
