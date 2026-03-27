package helpers

import (
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/your-org/overcast/internal/clock"
	"github.com/your-org/overcast/internal/config"
	"github.com/your-org/overcast/internal/router"
	"github.com/your-org/overcast/internal/state"
)

// TestServer wraps httptest.Server with a pre-configured emulator instance.
// Each test receives a fresh server with empty in-memory state — isolation
// is guaranteed without any setup/teardown ceremony.
type TestServer struct {
	*httptest.Server
	// Store is exposed so tests can inspect or pre-populate state directly
	// when needed. Prefer HTTP setup helpers (createBucket, createQueue etc.)
	// over direct store access wherever possible.
	Store  *state.MemoryStore
	Config *config.Config
	// Clock is the mock clock injected into all services on this server.
	// It is only set when WithMockClock() is passed to NewTestServer;
	// for real-clock servers it is nil.
	// Use Clock.Add(d) to advance time without any real sleep.
	Clock *clock.Mock
}

// serverOptions holds all non-config options for NewTestServer so that Option
// can carry both config mutations and server-level settings.
type serverOptions struct {
	cfg   *config.Config
	mock  *clock.Mock
	store state.Store // nil means use default MemoryStore
}

// NewTestServer creates a started test server with sensible defaults.
// The server is automatically closed when the test ends via t.Cleanup.
//
// Example — basic usage:
//
//	srv := helpers.NewTestServer(t)
//
// Example — with options:
//
//	srv := helpers.NewTestServer(t,
//	    helpers.WithServices("s3"),
//	    helpers.WithRegion("eu-west-1"),
//	    helpers.WithMockClock(),
//	)
//
// Example — advancing time in a test:
//
//	srv := helpers.NewTestServer(t, helpers.WithMockClock())
//	srv.Clock.Add(35 * time.Second) // instant — no real sleep
func NewTestServer(t *testing.T, opts ...Option) *TestServer {
	t.Helper()

	so := &serverOptions{cfg: defaultTestConfig()}
	logger := zap.NewNop() // silent in tests — keep output clean

	for _, opt := range opts {
		opt(so)
	}

	// Ensure a data directory is always available for on-disk state.
	if so.cfg.DataDir == "" {
		so.cfg.DataDir = t.TempDir()
	}

	store := so.store
	if store == nil {
		store = state.NewMemoryStore()
	}

	var clk clock.Clock
	if so.mock != nil {
		clk = so.mock
	} else {
		clk = clock.New()
	}

	handler := router.New(so.cfg, store, logger, clk)
	srv := httptest.NewServer(handler)

	var ms *state.MemoryStore
	if m, ok := store.(*state.MemoryStore); ok {
		ms = m
	}

	ts := &TestServer{
		Server: srv,
		Store:  ms,
		Config: so.cfg,
		Clock:  so.mock,
	}
	t.Cleanup(srv.Close)
	return ts
}

// Reset wipes all state on the server. Useful when a test wants to verify
// behaviour starting from a clean slate mid-test without creating a new server.
func (ts *TestServer) Reset() {
	if ts.Store != nil {
		ts.Store.Reset()
	}
}

// Option is a functional option for configuring the test server.
// Use the With* constructors rather than crafting values directly.
type Option func(*serverOptions)

// WithServices restricts which services are enabled.
// Useful for testing that disabled services return 404/501 correctly.
func WithServices(services ...string) Option {
	return func(so *serverOptions) {
		so.cfg.Services = make(map[string]bool)
		for _, s := range services {
			so.cfg.Services[s] = true
		}
	}
}

// WithRegion overrides the AWS region reported in ARNs and responses.
func WithRegion(region string) Option {
	return func(so *serverOptions) {
		so.cfg.Region = region
	}
}

// WithAccountID overrides the fake AWS account ID used in ARNs.
func WithAccountID(id string) Option {
	return func(so *serverOptions) {
		so.cfg.AccountID = id
	}
}

// WithDebug enables the /_debug/* endpoint namespace on the test server.
func WithDebug(enabled bool) Option {
	return func(so *serverOptions) {
		so.cfg.Debug = enabled
	}
}

// WithMockClock injects a manually-controlled clock into all services on the
// test server. Access srv.Clock to advance time without real sleeps:
//
//	srv := helpers.NewTestServer(t, helpers.WithMockClock())
//	srv.Clock.Add(35 * time.Second) // visibility timeout expires instantly
func WithMockClock() Option {
	return func(so *serverOptions) {
		so.mock = clock.NewMock()
	}
}

// WithStore injects a specific Store implementation (e.g. SQLiteStore).
// By default the server uses an in-memory store.
func WithStore(s state.Store) Option {
	return func(so *serverOptions) {
		so.store = s
	}
}

// WithDataDir sets the data directory for on-disk state (e.g. S3 body files).
// If not set, a temporary directory is used automatically.
func WithDataDir(dir string) Option {
	return func(so *serverOptions) {
		so.cfg.DataDir = dir
	}
}

// defaultTestConfig returns a config suited for test servers.
func defaultTestConfig() *config.Config {
	return &config.Config{
		Host:      "127.0.0.1",
		Port:      0, // httptest assigns the port
		Region:    "us-east-1",
		AccountID: "000000000000",
		State:     config.StateBackendMemory,
		LogLevel:  "error", // suppress info/debug logs in test output
		Services: map[string]bool{
			"s3":       true,
			"sqs":      true,
			"dynamodb": true,
			"sns":      true,
			"lambda":   true,
		},
		LambdaNodeBin:   "node",
		ShutdownTimeout: 0,
		SigV4Validate:   false,
		Debug:           false,
	}
}
