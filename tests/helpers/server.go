package helpers

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/inithooks"
	"github.com/Neaox/overcast/internal/router"
	"github.com/Neaox/overcast/internal/state"
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

	// shutdownOnce makes Shutdown and the registered t.Cleanup interchangeable:
	// whichever runs first does the work, the other is a no-op.
	shutdownOnce sync.Once
	shutdown     func(context.Context)
}

// Shutdown stops the server now rather than at the end of the test: service
// cleanup (which is what tears down Docker containers), then the HTTP listener.
// It is idempotent and the registered t.Cleanup still runs, so a test that
// calls it needs no other bookkeeping.
//
// Reach for it only when the *restart* is the subject — a second server has to
// claim a resource the first one holds, and asserting on what survived means
// the first must be gone while the test is still running. Everything else
// should let t.Cleanup do this.
func (ts *TestServer) Shutdown() {
	ts.shutdownOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		ts.shutdown(ctx)
		ts.Server.Close()
	})
}

// ExternalBase returns the base URL this server embeds in client-facing
// responses: the configured hostname (OVERCAST_HOSTNAME — "localhost" by
// default, see defaultTestConfig) on the port httptest actually bound.
//
// Assert against this, not Server.URL, whenever a test checks a resource URL a
// service handed back. Server.URL is the dial address (127.0.0.1), which is
// deliberately NOT what clients are told: an IP base matches no virtual-host
// rule, so a test pinned to it silently exercises a host shape no real client
// ever sends. That is exactly how the S3/host-route addressing collision
// survived — the Lambda function-URL round-trip test minted
// "{urlId}.lambda-url.us-east-1.127.0.0.1:PORT" and never hit the bug. See
// docs/plans/host-routing-precedence.md.
func (ts *TestServer) ExternalBase() string {
	if ts.Config == nil || ts.Config.Hostname == "" {
		return ts.URL
	}
	u, err := url.Parse(ts.URL)
	if err != nil {
		return ts.URL
	}
	return u.Scheme + "://" + net.JoinHostPort(ts.Config.Hostname, u.Port())
}

// serverOptions holds all non-config options for NewTestServer so that Option
// can carry both config mutations and server-level settings.
type serverOptions struct {
	cfg        *config.Config
	mock       *clock.Mock
	store      state.Store       // nil means use default MemoryStore
	initRunner *inithooks.Runner // nil means no init hooks
	logger     *zap.Logger       // nil means silent (zap.NewNop)
	// ownsNetworks marks a server whose Docker networks were minted for this
	// test alone, so they are removed with it. See WithECSDocker.
	ownsNetworks bool
	// ownsRegistryVolume marks a server whose ECR registry claimed a port this
	// test reserved, so the volume behind it is removed with the test. See
	// WithECRRegistryPort.
	ownsRegistryVolume bool
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
//	    helpers.WithRegion("eu-west-1"),
//	    helpers.WithMockClock(),
//	)
//
// Example — advancing time in a test:
//
//	srv := helpers.NewTestServer(t, helpers.WithMockClock())
//	srv.Clock.Add(35 * time.Second) // instant — no real sleep
func NewTestServer(t *testing.T, opts ...Option) *TestServer {
	if t == nil {
		panic("helpers.NewTestServer: t must not be nil — a *testing.T is required for cleanup registration")
	}
	t.Helper()

	so := &serverOptions{cfg: defaultTestConfig()}

	for _, opt := range opts {
		opt(so)
	}

	logger := so.logger
	if logger == nil {
		logger = zap.NewNop() // silent in tests — keep output clean
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

	// Bind the listener before building the router so the real port is known up
	// front. Config.ExternalBaseURL() formats cfg.Port verbatim, and the harness
	// would otherwise leave it at 0 — services that mint resource URLs through
	// it (SQS, the CloudFormation provisioner, SNS, ECR, AppSync) would hand
	// back an undialable "http://<hostname>:0" base, and any assertion built
	// from ExternalBaseURL() would agree with them because both sides evaluate
	// the same call. See docs/plans/harness-representativeness-audit.md.
	//
	// Writing cfg.Port here rather than after httptest.NewServer keeps it
	// race-free: router.New starts background init goroutines that may read the
	// config, so the value has to be final before that call, not after it.
	srv := httptest.NewUnstartedServer(nil)
	if _, port, err := net.SplitHostPort(srv.Listener.Addr().String()); err == nil {
		if p, convErr := strconv.Atoi(port); convErr == nil {
			so.cfg.Port = p
		}
	}

	handler, _, cleanup, waitReady := router.New(so.cfg, store, logger, clk, so.initRunner)
	srv.Config.Handler = handler
	srv.Start()

	// Block until all services with background init (e.g. Lambda Docker
	// probing) have completed, so tests can invoke immediately.
	waitReady()

	var ms *state.MemoryStore
	if m, ok := store.(*state.MemoryStore); ok {
		ms = m
	}

	ts := &TestServer{
		Server:   srv,
		Store:    ms,
		Config:   so.cfg,
		Clock:    so.mock,
		shutdown: cleanup,
	}
	// Registered first so they run last: neither the networks nor the registry
	// volume can go until the containers holding them have, which the server's
	// own cleanup does.
	if so.ownsNetworks {
		networks := []string{so.cfg.Network, so.cfg.ControlNetwork()}
		t.Cleanup(func() { removeTestNetworks(networks) })
	}
	if so.ownsRegistryVolume {
		port := so.cfg.ECRRegistryPort
		t.Cleanup(func() { removeTestRegistryVolume(port) })
	}
	// t.Cleanup runs in LIFO order: close the server first, then drain
	// any in-flight async work (e.g. SNS fan-out goroutines).
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ts.shutdownOnce.Do(func() { cleanup(ctx) })
	})
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

// WithServiceSubset registers only the named services on the test server.
//
// This is not a general-purpose knob and should not be reached for to "focus" a
// test: every service is always on in real runs, so a test that narrows the set
// is exercising a shape no user ever gets. It exists for the router tests that
// cannot be written any other way — proving no modeled operation falls through
// to S3's broad bucket/object routes requires a server where nothing else is
// registered to claim the path first. See config.TestOnlyServiceSubset.
func WithServiceSubset(services ...string) Option {
	return func(so *serverOptions) {
		subset := make(map[string]bool, len(services))
		for _, s := range services {
			if !slices.Contains(config.AllServices(), s) {
				panic("helpers.WithServiceSubset: unknown service " + s)
			}
			subset[s] = true
		}
		so.cfg.TestOnlyServiceSubset = subset
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

// WithDebug enables the /_overcast/debug/* endpoint namespace on the test server.
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

// WithLambdaDocker enables Docker-backed Lambda execution on the test server.
// By default, test servers skip the Docker probe entirely (stub runtime only)
// to avoid 1000+ unnecessary Docker daemon round-trips across the test suite.
// Use this option for tests that invoke real Lambda container runtimes.
//
// TODO(perf): For Approach B — share a single Docker client, RuntimeAPI server,
// and InstancePool across all test servers in a package via a package-level
// sync.Once. This would enable warm-container reuse across tests and further
// reduce Docker daemon pressure. Wire shared runtime via a new
// lambda.WithSharedRuntime(...) service option instead of each server probing
// independently.
func WithLambdaDocker() Option {
	return func(so *serverOptions) {
		so.cfg.LambdaDockerSocket = TestDockerSocket()
	}
}

// TestDockerSocket is the Docker endpoint a test server should manage
// containers through: LAMBDA_DOCKER_SOCKET when set, otherwise the platform
// default — the same resolution config.Load performs, so a test server talks to
// the daemon a real one would.
//
// It is not "/var/run/docker.sock". That path does not exist on Windows, where
// Docker Desktop listens on a named pipe, so every Docker-dependent test used
// to build a server whose Docker client could never connect. The tests did not
// fail: their own gate found the daemon (it resolves the endpoint correctly),
// the server's did not, and the test skipped or timed out against an emulator
// with no Docker at all. Container behaviour was therefore verified only on
// Linux, which is how a broken ECR-to-ECS image pull reached users from a
// Windows workstation with a green suite.
func TestDockerSocket() string {
	if socket := os.Getenv("LAMBDA_DOCKER_SOCKET"); socket != "" {
		return socket
	}
	return config.DefaultDockerSocket()
}

// WithECSDocker enables Docker-backed ECS task placement on the test server,
// so RunTask starts real containers instead of leaving tasks at PROVISIONING.
// Off by default for the same reason Lambda's is: most ECS tests are about the
// API surface and would pay a daemon round-trip for nothing.
//
// The network is named per test run because the containers are: two packages
// running in parallel must not share, or race to remove, one another's. Being
// per-run, they are also this server's to remove — see the cleanup in
// NewTestServer. A Docker daemon has a finite address pool (Docker Desktop
// subnets roughly thirty networks out of its default pools), so a suite that
// mints a pair per test server and never removes them exhausts it after a few
// dozen runs, and every later `docker network create` fails — which the
// emulator reports as "Docker not available", leaving ECS metadata-only and
// every container test failing for a reason that has nothing to do with the
// code under test.
func WithECSDocker() Option {
	return func(so *serverOptions) {
		so.cfg.ECSDockerSocket = TestDockerSocket()
		so.cfg.Network = fmt.Sprintf("overcast_ecs_test_%d", time.Now().UnixNano())
		so.ownsNetworks = true
	}
}

// WithECRRegistryPort makes the ECR registry claim a fixed host port instead of
// an ephemeral one — the binding shape production uses, since
// OVERCAST_ECR_REGISTRY_PORT defaults to 4510 while the harness defaults to 0.
//
// The difference is not cosmetic, and it decides whether a Docker-dependent
// registry test runs at all. Measured on Docker Desktop 29.6.2 for Windows,
// where the daemon runs in a VM: a fixed publish is reachable from the daemon
// (`docker login localhost:5099` → Login Succeeded) and an ephemeral one is
// not (`docker login localhost:62154` → context deadline exceeded on the
// probe), though `docker port` reports both as dual-stack. So on Desktop every
// registry test the harness left ephemeral skipped itself as "the daemon will
// not talk plain HTTP to …", and the shape real users run was covered only on
// native Linux CI.
//
// Pass a port nothing else will claim — ReserveTCPPort gets one. Do not share
// 4510 between tests: a fixed-port claim replaces whatever container already
// holds its name, which is correct for a predecessor and fatal for a sibling
// test package's live registry.
//
// The claim also brings a named storage volume, so the registry's images
// survive its container — see docs/services/ecr.md § Persistence. That is the
// production shape and the reason to test it, but a per-test port means the
// volume would never be found again, so this server removes it on cleanup. A
// test that wants a *restart* to find its images (one server down, a second
// claiming the same port) gets that within its own lifetime; nothing survives
// the test that reserved the port.
func WithECRRegistryPort(port int) Option {
	return func(so *serverOptions) {
		so.cfg.ECRRegistryPort = port
		so.ownsRegistryVolume = true
	}
}

// WithLambdaHotReload enables bind-mount-based Lambda hot reload.
// Functions must still opt in via the overcast:hot-reload-path tag.
func WithLambdaHotReload() Option {
	return func(so *serverOptions) {
		so.cfg.LambdaHotReload = true
	}
}

// WithSMTPMock enables the built-in SMTP capture server on a random port.
// Emails delivered to SNS email/email-json subscribers are captured and
// accessible via GET /_overcast/ses/inbox/messages on the test server.
func WithSMTPMock() Option {
	return func(so *serverOptions) {
		so.cfg.SMTPMock = true
		so.cfg.SMTPPort = 0 // random port
	}
}

// WithInitRunner injects an init hook runner into the test server so the
// /_overcast/init status endpoint reports its state.
func WithInitRunner(r *inithooks.Runner) Option {
	return func(so *serverOptions) {
		so.initRunner = r
	}
}

// WithServiceStates sets per-service storage backend overrides.
func WithServiceStates(states map[string]config.StateBackend) Option {
	return func(so *serverOptions) {
		so.cfg.ServiceStates = states
	}
}

// WithLogger routes the server's logs to the supplied zap.Logger instead of
// discarding them. Pair it with zaptest/observer to assert on a diagnostic the
// emulator emits but cannot surface in a response — AWS wire formats are fixed,
// so a log line is sometimes the only place a divergence can be reported.
//
//	core, logs := observer.New(zap.WarnLevel)
//	srv := helpers.NewTestServer(t, helpers.WithLogger(zap.New(core)))
func WithLogger(logger *zap.Logger) Option {
	return func(so *serverOptions) {
		so.logger = logger
	}
}

// WithHostname sets the external hostname used in client-facing URLs.
func WithHostname(hostname string) Option {
	return func(so *serverOptions) {
		so.cfg.Hostname = hostname
	}
}

// WithTLS marks the server as TLS-enabled for the purposes of config
// (cfg.TLSEnabled()), without actually serving HTTPS.
//
// Handlers that must not advertise an https:// URL the emulator cannot answer
// — CloudFront's ViewerProtocolPolicy is the case this exists for — branch on
// cfg.TLSEnabled(), which only checks that both paths are set. Serving real TLS
// would mean generating a certificate and re-dialling every helper in this
// package for one boolean, so this sets the paths and nothing else.
func WithTLS() Option {
	return func(so *serverOptions) {
		so.cfg.TLSCertFile = "testdata/unused.crt"
		so.cfg.TLSKeyFile = "testdata/unused.key"
	}
}

// WithEKSMode sets the EKS service mode used by the test server.
func WithEKSMode(mode config.EKSMode) Option {
	return func(so *serverOptions) {
		so.cfg.EKSMode = mode
	}
}

// WithEnforceIAM enables opt-in IAM authorization enforcement middleware.
func WithEnforceIAM(enabled bool) Option {
	return func(so *serverOptions) {
		so.cfg.EnforceIAM = enabled
	}
}

// WithEnforceAPIGatewayThrottle enables opt-in rejection of API Gateway
// requests that exceed their usage plan's throttle or quota limits. Usage is
// measured either way; this only decides whether an over-limit request is
// answered 429 instead of being served.
func WithEnforceAPIGatewayThrottle(enabled bool) Option {
	return func(so *serverOptions) {
		so.cfg.EnforceAPIGatewayThrottle = enabled
	}
}

// WithEC2VPCStrategy sets the VPC network strategy used by the EC2 service.
// Valid values: "shared" (default), "strict", "remapped". See
// docs/plans/ec2-vpc-network-strategies.md for details.
func WithEC2VPCStrategy(strategy string) Option {
	return func(so *serverOptions) {
		so.cfg.EC2VPCNetworkStrategy = strategy
	}
}

// WithSigV4Validate enables or disables SigV4 signature validation for the
// test server. When enabled, unsigned requests and requests with invalid
// signatures are rejected with a 403. Default is false.
func WithSigV4Validate(enabled bool) Option {
	return func(so *serverOptions) {
		so.cfg.SigV4Validate = enabled
	}
}

// defaultTestConfig returns a config suited for test servers.
func defaultTestConfig() *config.Config {
	return &config.Config{
		Host:                 "127.0.0.1",
		Hostname:             "localhost",
		Port:                 0, // httptest assigns the port
		Region:               "us-east-1",
		AccountID:            "000000000000",
		EKSMode:              config.EKSModeMock,
		State:                config.StateBackendMemory,
		ServiceStates:        make(map[string]config.StateBackend),
		HybridFlushInterval:  5 * time.Second,
		CFNSyncWait:          time.Second,
		LogLevel:             "error", // suppress info/debug logs in test output
		LambdaDockerSocket:   "",      // empty = skip Docker probe; use WithLambdaDocker() for container tests
		Network:              "overcast",
		LambdaRuntimeAPIPort: 0, // OS-assigned port — avoids conflicts when test packages run in parallel
		// Mirrors config.Load's default. It only bites once a test also asks for
		// a fixed registry port, but leaving it false there would exercise a
		// storage shape no user gets.
		ECRRegistryPersist: true,
		ShutdownTimeout:    0,
		SigV4Validate:      false,
		Debug:              false,
		SMTPMock:           false, // disabled by default; use WithSMTPMock() to enable
		SMTPPort:           0,     // random when mock is enabled
		SMTPFrom:           "overcast@localhost",
		SMTPInboxMax:       500,
		// Step Functions runaway-execution guard. Executions run on their own
		// goroutines, so this bounds the run, not the request. Kept short so a
		// non-terminating definition fails a test fast rather than hanging.
		StepFunctionsExecutionTimeout: 10 * time.Second,
	}
}

// NewHTTPBackend starts a throwaway HTTP server with the given handler.
// The server is closed automatically when the test ends.
func NewHTTPBackend(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}
