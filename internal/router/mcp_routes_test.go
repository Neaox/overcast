//go:build !slim

package router

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/mcp"
	"github.com/Neaox/overcast/internal/state"
)

// mcpDiscoverBody is a well-formed modern request, and the cheapest one there
// is: `server/discover` takes no parameters of its own and is not a named
// method, so `Mcp-Method` is the only header it obliges the caller to mirror.
//
// It is spelled out rather than built, because what these tests need from it is
// that it is *valid* — a request the server would answer if nothing stopped it.
// That is what makes 401 and 403 attributable to the thing being tested.
const mcpDiscoverBody = `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{` +
	`"io.modelcontextprotocol/protocolVersion":"` + mcp.ProtocolVersion + `",` +
	`"io.modelcontextprotocol/clientCapabilities":{},` +
	`"io.modelcontextprotocol/clientInfo":{"name":"router-test","version":"1.0"}}}}`

func newMCPRouterTestServer(t *testing.T, mutateCfg ...func(*config.Config)) *httptest.Server {
	t.Helper()

	cfg := &config.Config{
		Host:      "127.0.0.1",
		Port:      0,
		Region:    "us-east-1",
		AccountID: "000000000000",
		State:     config.StateBackendMemory,
		LogLevel:  "error",

		ShutdownTimeout: 0,
		SigV4Validate:   false,
		Debug:           false,
	}
	for _, mutate := range mutateCfg {
		if mutate != nil {
			mutate(cfg)
		}
	}

	store := state.NewMemoryStore()
	handler, preShutdown, cleanup, _ := New(cfg, store, zap.NewNop(), clock.New())
	srv := httptest.NewServer(handler)
	t.Cleanup(func() {
		preShutdown()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		cleanup(ctx)
		srv.Close()
	})
	return srv
}

func TestRuntimeMCPRoutes_RemoteExposureRequiresBearerToken(t *testing.T) {
	srv := newMCPRouterTestServer(t, func(cfg *config.Config) {
		cfg.MCPRemoteExposure = true
		cfg.MCPAuthToken = "test-token"
	})

	unauthReq, err := http.NewRequest(http.MethodPost, srv.URL+"/_overcast/mcp", strings.NewReader(mcpDiscoverBody))
	if err != nil {
		t.Fatalf("new unauth request: %v", err)
	}
	unauthReq.Header.Set("Content-Type", "application/json")
	unauthReq.Header.Set("Mcp-Method", "server/discover")
	unauthResp, err := http.DefaultClient.Do(unauthReq)
	if err != nil {
		t.Fatalf("POST /_overcast/mcp unauthenticated: %v", err)
	}
	defer unauthResp.Body.Close()
	if unauthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth status = %d, want 401", unauthResp.StatusCode)
	}
	if got := unauthResp.Header.Get("WWW-Authenticate"); !strings.Contains(strings.ToLower(got), "bearer") {
		t.Fatalf("WWW-Authenticate = %q, want bearer challenge", got)
	}

	authReq, err := http.NewRequest(http.MethodPost, srv.URL+"/_overcast/mcp", strings.NewReader(mcpDiscoverBody))
	if err != nil {
		t.Fatalf("new auth request: %v", err)
	}
	authReq.Header.Set("Content-Type", "application/json")
	authReq.Header.Set("Mcp-Method", "server/discover")
	authReq.Header.Set("Authorization", "Bearer test-token")
	authResp, err := http.DefaultClient.Do(authReq)
	if err != nil {
		t.Fatalf("POST /_overcast/mcp authenticated: %v", err)
	}
	defer authResp.Body.Close()
	if authResp.StatusCode != http.StatusOK {
		t.Fatalf("auth status = %d, want 200", authResp.StatusCode)
	}

	// A 200 alone would also be what a JSON-RPC error looks like, so read far
	// enough to see the request was served rather than merely admitted.
	var answer struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.NewDecoder(authResp.Body).Decode(&answer); err != nil {
		t.Fatalf("decode authenticated response: %v", err)
	}
	if len(answer.Error) > 0 {
		t.Fatalf("authenticated server/discover returned an error: %s", answer.Error)
	}
	if len(answer.Result) == 0 {
		t.Fatal("authenticated server/discover returned no result")
	}
}

// TestRuntimeMCPRoutes_ServerWatchesTheShutdownChannel covers the wiring, and
// only the wiring: the channel this package owns is the channel the server it
// builds will watch.
//
// That a stream then ends when the channel closes is a separate property, and
// it belongs to internal/mcp — TestSubscriptions_shutdownEndsTheStreamWithA
// ClosingResult asserts it there, on a server it wires by hand. Which is
// exactly why that test cannot see this: it supplies its own channel, so it
// would pass just as happily against a router that forgot to supply one.
//
// Asserting the identity rather than the effect is what keeps the two apart. A
// router-level test that opened a listen stream and waited for it to end would
// re-run the whole mechanism to observe one assignment, and would have to bound
// the wait with a deadline — the formulation the deleted
// TestRuntimeMCPRoutes_SSEStreamLetsGoOnPreShutdown had, which flaked on CI at
// its 20-second bound. There is nothing to wait for here.
func TestRuntimeMCPRoutes_ServerWatchesTheShutdownChannel(t *testing.T) {
	shutdownCh := make(chan struct{})

	srv := newRuntimeMCPServer(&config.Config{}, shutdownCh)

	got := srv.ShutdownSignal()
	if got == nil {
		t.Fatal("the server was built with no shutdown signal, so nothing but a client " +
			"disconnecting will end its streams and shutdown will wait for that forever")
	}
	// Compared by identity: a server watching *some* channel is not the point,
	// and comparing anything else would pass on a channel the router cannot
	// close.
	if got != (<-chan struct{})(shutdownCh) {
		t.Fatal("the server is watching a channel other than the one it was handed")
	}
}

// TestRuntimeMCPRoutes_PostIsTheOnlyEndpoint pins the shape of the surface the
// router exposes: one endpoint, one method.
//
// This is what is left of the three tests that drove GET and DELETE at this
// path — the streamable GET stream, the session lifecycle and the Accept
// negotiation on GET. Their subjects went with the legacy era in phase 4, but
// what a client that still tries them gets back is worth stating, because the
// answer comes from the MCP handler rather than from chi and nothing else says
// so. 405 with an Allow header tells such a client the path is right and the
// method is not; a 404 would send it looking for a different URL.
func TestRuntimeMCPRoutes_PostIsTheOnlyEndpoint(t *testing.T) {
	srv := newMCPRouterTestServer(t)

	// Both spellings of the base path, since the router routes them separately.
	for _, path := range []string{"/_overcast/mcp", "/_overcast/mcp/"} {
		for _, method := range []string{http.MethodGet, http.MethodDelete} {
			t.Run(method+" "+path, func(t *testing.T) {
				req, err := http.NewRequest(method, srv.URL+path, nil)
				if err != nil {
					t.Fatalf("new request: %v", err)
				}
				// The header the deleted GET stream answered to. It buys
				// nothing now, and that is the point of sending it.
				req.Header.Set("Accept", "text/event-stream")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatalf("%s %s: %v", method, path, err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusMethodNotAllowed {
					t.Fatalf("status = %d, want 405", resp.StatusCode)
				}
				if got := resp.Header.Get("Allow"); got != http.MethodPost {
					t.Fatalf("Allow = %q, want POST", got)
				}
			})
		}
	}
}

// TestRuntimeMCPRoutes_RejectForbiddenOrigin covers the browser-facing
// protection on the surface, which POST now carries alone.
//
// Origin validation survived the removal of the GET stream and the DELETE
// endpoint and did not narrow with them: it is checked in handleRPC before the
// body is read, so a page on another origin cannot reach the emulator through a
// user's browser whatever it asks for. That check having exactly one entry
// point to guard is a simplification, not a reduction in what is guarded.
func TestRuntimeMCPRoutes_RejectForbiddenOrigin(t *testing.T) {
	srv := newMCPRouterTestServer(t)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/_overcast/mcp", strings.NewReader(mcpDiscoverBody))
	if err != nil {
		t.Fatalf("new POST request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Method", "server/discover")
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /_overcast/mcp with forbidden origin: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}

	// Rejected before dispatch, so a request the server would otherwise have
	// answered gets nothing back. Sending a well-formed one above is what makes
	// that assertable: a body the server would have refused anyway could not
	// tell a rejected origin from a rejected request.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if strings.Contains(string(body), `"result"`) {
		t.Fatalf("forbidden origin was answered with a result: %q", body)
	}
}
