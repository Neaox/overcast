// Package lambda is a stub — handlers are implemented test-first.
//
// Architecture uses the Strategy pattern for runtime execution:
//
//	Runtime interface ← NodeRuntime (v1) | PythonRuntime (future) | GoRuntime (future)
//
// The Lambda handler never knows which runtime it's talking to. Adding a new
// runtime means implementing the Runtime interface and registering it in the
// RuntimeRegistry — nothing else changes.
//
// Implementation order (TDD):
//  1. CreateFunction / GetFunction / ListFunctions / DeleteFunction / UpdateFunctionCode
//  2. Invoke (synchronous) — stub response mode
//  3. Invoke (synchronous) — real Node.js execution via NodeRuntime
//  4. InvokeAsync
//  5. Event source mapping (SQS→Lambda)
package lambda

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// Runtime is the Strategy interface for Lambda execution environments.
// It follows a two-level lifecycle: the Runtime manages the pool of warm
// instances; a RuntimeInstance executes a single invocation.
//
// Sequence:
//
//	inst, err := runtime.Acquire(ctx, fn)  // get or start a warm container
//	result, err := inst.Invoke(ctx, event) // run the handler
//	runtime.Release(ctx, inst, err == nil) // return or discard the instance
type Runtime interface {
	// CanHandle returns true if this runtime can execute functions with the
	// given runtime identifier (e.g. "nodejs20.x", "nodejs22.x").
	CanHandle(runtimeID string) bool

	// Acquire returns a warm RuntimeInstance ready to serve one invocation.
	// It may start a new container if no warm instance is available.
	Acquire(ctx context.Context, fn *Function) (RuntimeInstance, error)

	// Release returns the instance to the pool (healthy=true) or destroys it
	// (healthy=false, e.g. after a crash or timeout).
	Release(ctx context.Context, inst RuntimeInstance, healthy bool)
}

// RuntimeInstance represents a single warm Lambda container (or process) that
// can execute exactly one invocation at a time.
type RuntimeInstance interface {
	// Invoke sends the event payload to the function handler and returns the
	// result. The instance is exclusive to the caller for the duration.
	Invoke(ctx context.Context, event []byte) (*InvokeResult, error)

	// LogStreamName returns the CloudWatch Logs stream name for this container
	// instance. The name is assigned when the instance starts and remains fixed
	// for its lifetime. Format: YYYY/MM/DD/[$LATEST]<26-char hex>
	LogStreamName() string

	// Healthy reports whether the instance is usable after the last invocation.
	Healthy() bool

	// FunctionName returns the name of the Lambda function this instance runs.
	// Used by InstancePool.Release to key the pool without requiring *Function.
	FunctionName() string

	// CodeHash returns the SHA-256 of the deployment package this instance was
	// built from. Used by InstancePool to detect stale instances after code updates.
	CodeHash() string

	// Close shuts down and removes the underlying container or process.
	Close() error
}

// InvokeResult holds the outcome of a Lambda invocation.
type InvokeResult struct {
	// StatusCode is the HTTP status code returned by the function handler.
	StatusCode int
	// Payload is the raw JSON response body.
	Payload []byte
	// FunctionError is non-empty if the function returned an error response
	// (i.e. X-Amz-Function-Error: Handled or Unhandled).
	FunctionError string
	// LogResult contains base64-encoded tail log output (last 4KB).
	LogResult string
	// LogGroupName is the CloudWatch log group for this function.
	LogGroupName string
	// LogStreamName is the specific log stream produced by this invocation.
	LogStreamName string

	// acquireFailed is true when rt.Acquire failed (Docker infrastructure
	// issue: image pull, container create/start, IP assignment). Used by
	// invokeSync to retry once. Never set for errors from inside a running
	// container (timeouts, init errors, handler crashes).
	acquireFailed bool
}

// LayerVersionLink is a reference to a specific layer version attached to a function.
// The struct mirrors the AWS FunctionConfiguration.Layers shape so it serialises
// directly into wire responses without a conversion step.
type LayerVersionLink struct {
	ARN                      string `json:"Arn"`
	CodeSize                 int64  `json:"CodeSize"`
	SigningProfileVersionARN string `json:"SigningProfileVersionArn,omitempty"`
	SigningJobARN            string `json:"SigningJobArn,omitempty"`
}

// Function is the domain model for a stored Lambda function definition.
type Function struct {
	Name            string             `json:"name"`
	ARN             string             `json:"arn"`
	Runtime         string             `json:"runtime"`
	Handler         string             `json:"handler"`
	Role            string             `json:"role"`
	Description     string             `json:"description,omitempty"`
	Timeout         int                `json:"timeout"`
	MemorySize      int                `json:"memory_size"`
	Environment     map[string]string  `json:"environment,omitempty"`
	CodeZip         []byte             `json:"code_zip,omitempty"` // base64-decoded zip
	CodeSize        int64              `json:"code_size,omitempty"`
	CodeS3Bucket    string             `json:"code_s3_bucket,omitempty"`
	CodeS3Key       string             `json:"code_s3_key,omitempty"`
	ImageUri        string             `json:"image_uri,omitempty"` // PackageType=Image only
	PackageType     string             `json:"package_type,omitempty"`
	Architectures   []string           `json:"architectures,omitempty"`
	State           string             `json:"state"` // "Active", "Pending", "Inactive", "Failed"
	StateReason     string             `json:"state_reason,omitempty"`
	StateReasonCode string             `json:"state_reason_code,omitempty"` // e.g. "Creating", "Idle", "ImagePullError"
	RevisionId      string             `json:"revision_id,omitempty"`
	LastModified    string             `json:"last_modified,omitempty"`
	LogGroup        string             `json:"log_group,omitempty"` // Custom log group; defaults to /aws/lambda/{name}
	Layers          []LayerVersionLink `json:"layers,omitempty"`    // Attached layer versions (empty until layers are implemented)
	// SourceCode and SourceFilename are emulator-internal: they hold the raw
	// handler source text authored in the web UI. Not exposed in AWS wire responses.
	SourceCode     string `json:"source_code,omitempty"`
	SourceFilename string `json:"source_filename,omitempty"`
	// VpcConfig optionally associates the function with an EC2 VPC. When set,
	// the Lambda container is connected to the VPC's Docker network in addition
	// to the default Lambda network, so the function can communicate with other
	// resources in the VPC.
	VpcConfig *VpcConfig `json:"vpc_config,omitempty"`
	// ImageConfig overrides the container image's EntryPoint, Command, and
	// WorkingDirectory. Only applicable when PackageType=Image.
	ImageConfig *ImageConfig      `json:"image_config,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	// ReservedConcurrency is the reserved concurrency limit. nil = unreserved,
	// 0 = throttled (no executions).
	ReservedConcurrency *int `json:"reserved_concurrency,omitempty"`
}

// ImageConfig overrides for container image Lambda functions.
type ImageConfig struct {
	EntryPoint       []string `json:"entry_point,omitempty"`
	Command          []string `json:"command,omitempty"`
	WorkingDirectory string   `json:"working_directory,omitempty"`
}

// VpcConfig associates a Lambda function with a VPC.
type VpcConfig struct {
	SubnetIds        []string `json:"SubnetIds,omitempty"`
	SecurityGroupIds []string `json:"SecurityGroupIds,omitempty"`
	VpcId            string   `json:"VpcId,omitempty"`
}

// logGroupName returns the CloudWatch Logs log group for this function.
// Uses the custom LogGroup if set, otherwise the AWS default /aws/lambda/{name}.
func (f *Function) logGroupName() string {
	if f.LogGroup != "" {
		return f.LogGroup
	}
	return "/aws/lambda/" + f.Name
}

// runtimeRegistry holds the active set of Runtimes behind an atomic pointer so
// the Docker initialisation goroutine can upgrade from stub→container runtimes
// after startup without locking any callers.
type runtimeRegistry struct {
	p atomic.Pointer[[]Runtime]
}

func newRuntimeRegistry(initial []Runtime) *runtimeRegistry {
	rr := &runtimeRegistry{}
	rr.p.Store(&initial)
	return rr
}

// get returns the current runtime list. The slice must not be modified.
func (rr *runtimeRegistry) get() []Runtime {
	return *rr.p.Load()
}

// set atomically replaces the runtime list.
func (rr *runtimeRegistry) set(runtimes []Runtime) {
	rr.p.Store(&runtimes)
}

// Service implements router.Service for Lambda.
type Service struct {
	cfg         *config.Config
	clk         clock.Clock
	store       state.Store
	ls          *lambdaStore
	log         *serviceutil.ServiceLogger
	handler     *Handler
	invoker     *ServiceInvoker
	logWriter   events.LogWriter
	tracker     *instanceTracker
	esmDelivery *esmDeliveryManager // nil until InitESMDelivery is called
	initWg      sync.WaitGroup      // signals when initDockerRuntime completes
	gc          *docker.GC          // nil until Docker init completes
	// mu protects the fields below, which are written by initDockerRuntime.
	mu               sync.Mutex
	bus              *events.Bus       // set by InitBus; read by initDockerRuntime
	runtimeAPI       *RuntimeAPIServer // nil until Docker init completes
	docker           *docker.Client    // nil until Docker init completes
	containerRuntime *ContainerRuntime // nil until Docker init completes
	pool             *InstancePool     // nil until Docker init completes
}

// WaitReady blocks until the background Docker runtime initialisation has
// completed (successfully or not). Production callers should never need this;
// it exists so integration tests can ensure the ContainerRuntime is wired
// before invoking functions.
func (s *Service) WaitReady() { s.initWg.Wait() }

// InitLogWriter wires the CloudWatch Logs writer so Lambda invocations can
// write START/log/END/REPORT lines without importing the logs package.
// Called by the router after all services are constructed.
func (s *Service) InitLogWriter(lw events.LogWriter) {
	s.mu.Lock()
	s.logWriter = lw
	s.mu.Unlock()
	s.handler.logWriter = lw
	s.invoker.logWriter = lw
	s.invoker.cfg = s.cfg
	// Forward to ContainerRuntime so the log-streaming goroutine can write
	// container stdout/stderr (including Powertools JSON lines) to CloudWatch.
	s.mu.Lock()
	cr := s.containerRuntime
	s.mu.Unlock()
	if cr != nil {
		cr.SetLogWriter(lw)
	}
}

// InitBus wires the event bus so Lambda lifecycle events (FunctionCreated,
// FunctionDeleted, FunctionUpdated) are published for topology and UI consumers.
// Called by the router after all services are constructed.
func (s *Service) InitBus(b *events.Bus) {
	s.handler.bus = b
	s.tracker.SetBus(b)
	s.invoker.InitBus(b, s.clk)
	s.mu.Lock()
	s.bus = b
	cr := s.containerRuntime
	s.mu.Unlock()
	if cr != nil {
		cr.SetBus(b)
	}
}

// InitS3Sync wires S3-reactive code sync. When an S3 object that matches a
// function's CodeS3Bucket/CodeS3Key is uploaded, the function's CodeZip is
// refreshed automatically and the warm pool is invalidated on the next invoke.
//
// Must be called after InitBus; if the bus has not been set this is a no-op.
func (s *Service) InitS3Sync(fetch S3FetchFunc) {
	if fetch == nil {
		return
	}
	// Make the fetcher available to CreateFunction / UpdateFunctionCode for
	// eager retrieval, in addition to the reactive watcher.
	s.handler.s3Fetch = fetch
	s.mu.Lock()
	bus := s.bus
	s.mu.Unlock()
	if bus == nil {
		return
	}
	w := newS3SyncWatcher(s.ls, fetch, s.log.Logger(), s.clk)
	w.register(bus)
}

// SetVPCResolver wires the EC2 VPC resolver so Lambda can look up subnet→VPC
// mappings and connect containers to VPC Docker networks.
func (s *Service) SetVPCResolver(r VPCNetworkResolver) {
	s.handler.setVPCResolver(r)
	// If the container runtime is already initialized, wire it there too.
	s.mu.Lock()
	cr := s.containerRuntime
	s.mu.Unlock()
	if cr != nil {
		cr.SetVPCResolver(r)
	}
}

// New returns a configured Lambda Service with all supported runtimes registered.
// Docker availability is checked in the background — the service starts
// immediately using the stub NodeRuntime and upgrades to ContainerRuntime once
// Docker is confirmed reachable. Other services are never blocked.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, "lambda")
	ls := newLambdaStore(store, cfg.Region, clk)
	tracker := newInstanceTracker(clk, logger)
	rtCache := newRuntimeCache(logger)

	// Start with the stub runtime so the service is immediately available.
	rr := newRuntimeRegistry([]Runtime{newNodeRuntime(clk, logger)})

	esmSt := newESMStore(ls)
	h := newHandler(cfg, log, clk, rr, ls, tracker, rtCache)
	h.esm = esmSt

	s := &Service{
		cfg:     cfg,
		clk:     clk,
		store:   store,
		ls:      ls,
		log:     log,
		tracker: tracker,
		handler: h,
		invoker: newServiceInvoker(ls, rr, logger, tracker),
	}

	// Probe Docker in the background so startup of other services is not delayed.
	s.initWg.Add(1)
	go func() {
		defer s.initWg.Done()
		s.initDockerRuntime(cfg, clk, rr)
	}()

	return s
}

// initDockerRuntime probes the Docker daemon (with retries) and, if available,
// replaces the stub runtime in rr with ContainerRuntime. Called once from a
// goroutine spawned by New.
func (s *Service) initDockerRuntime(cfg *config.Config, clk clock.Clock, rr *runtimeRegistry) {
	log := s.log.Logger()

	// Skip Docker entirely when no socket is configured. This avoids
	// unnecessary probe retries in test servers that don't need containers.
	if cfg.LambdaDockerSocket == "" {
		log.Debug("LambdaDockerSocket is empty — skipping Docker init")
		return
	}

	result, probeErr := docker.Probe(cfg.LambdaDockerSocket, cfg.LambdaNetwork, log)
	if probeErr != nil {
		s.log.Warn("Docker not available — using stub runtime (invocations will return mock responses)",
			zap.String("socket", cfg.LambdaDockerSocket), zap.Error(probeErr))
		return
	}

	dc := result.Client

	// Create Docker GC so Stop() can sweep orphaned Lambda containers.
	s.gc = docker.NewGC(dc, s.log.ZapLogger(), false)
	s.gc.StartRemoveLoop(context.Background())
	// At startup, immediately clean up any orphaned containers from prior
	// crashes that left containers stuck in "created" or "exited" state.
	s.gc.Sweep("lambda")

	s.log.Info("Docker available — initialising container runtime",
		zap.String("socket", cfg.LambdaDockerSocket))

	listenAddr := fmt.Sprintf("0.0.0.0:%d", cfg.LambdaRuntimeAPIPort)

	// Listen first so we know the actual port (important when port is 0).
	ln, lnErr := net.Listen("tcp", listenAddr)
	if lnErr != nil {
		s.log.Warn("failed to listen for Runtime API server — container runtime disabled",
			zap.String("addr", listenAddr), zap.Error(lnErr))
		return
	}
	actualPort := ln.Addr().(*net.TCPAddr).Port

	containerAddr := runtimeAPIContainerAddr(cfg, dc, log, actualPort)

	runtimeAPI, apiErr := NewRuntimeAPIServerFromListener(ln, containerAddr, log, clk)
	if apiErr != nil {
		s.log.Warn("failed to start Runtime API server — container runtime disabled",
			zap.Error(apiErr))
		return
	}

	containerRuntime := NewContainerRuntime(cfg, clk, dc, s.gc, runtimeAPI, log)

	// When a container's RIC issues its first GET /next, transition the
	// instance tracker from "initializing" to "running" and throttle the
	// INIT-burst CPU down to the steady-state proportional allocation.
	runtimeAPI.OnFirstNext = func(functionARN string) {
		name := functionNameFromARN(functionARN)
		s.tracker.RuntimeConnected(name)
		containerRuntime.ThrottleInitBurst(functionARN)
	}

	containerRuntime.SetLayerContentFetcher(func(ctx context.Context, layerVersionARN string) ([]byte, error) {
		lv, aerr := s.ls.getLayerVersionByARN(ctx, layerVersionARN)
		if aerr != nil {
			return nil, fmt.Errorf("get layer %s: %s", layerVersionARN, aerr.Message)
		}
		if lv == nil {
			return nil, fmt.Errorf("layer version not found: %s", layerVersionARN)
		}
		return append([]byte(nil), lv.Content...), nil
	})

	// Always wire the remote layer fetcher — it checks the layer cache
	// dir (LAMBDA_LAYER_CACHE_DIR or {DataDir}/layers) for pre-downloaded
	// zips and optionally fetches from real AWS when credentials are configured.
	{
		fetcher := NewRemoteLayerFetcher(cfg, log, clk)
		containerRuntime.SetRemoteLayerFetcher(fetcher)
		cacheDir := cfg.LambdaLayerCacheDir
		if cacheDir == "" {
			cacheDir = filepath.Join(cfg.DataDir, "layers")
		}
		log.Info("lambda layer cache configured",
			zap.String("cache_dir", cacheDir),
			zap.Bool("remote_fetch", cfg.LambdaFetchRemoteLayers && cfg.LambdaRemoteAWSAccessKeyID != ""))
	}

	// Wire the log writer if InitLogWriter was already called.
	s.mu.Lock()
	lw := s.logWriter
	s.mu.Unlock()
	if lw != nil {
		containerRuntime.SetLogWriter(lw)
	}

	// Wire the VPC resolver if SetVPCResolver was already called.
	if r := s.handler.getVPCResolver(); r != nil {
		containerRuntime.SetVPCResolver(r)
	}

	// Subscribe the exit notifier so per-invocation WaitContainer goroutines
	// are replaced by a single event stream. The Docker event watcher is owned
	// by the router (started once for all services); Lambda just subscribes.
	s.mu.Lock()
	bus := s.bus
	s.mu.Unlock()
	if bus != nil {
		bus.Subscribe(events.DockerContainerDied, containerRuntime.exitNotify.handleContainerDied)
		containerRuntime.SetBus(bus)
	}

	// Atomically upgrade to ContainerRuntime. NodeRuntime stays as fallback.
	pool := NewInstancePool(containerRuntime, log, clk)
	rr.set([]Runtime{pool, newNodeRuntime(clk, log)})

	// Wire the image prewarmer so CreateFunction can kick off image pulls
	// in the background instead of paying the cost on the first Invoke.
	s.handler.prewarmer = containerRuntime.PrewarmFunction

	// Store references for Shutdown/InitLogWriter to use.
	s.mu.Lock()
	s.docker = dc
	s.runtimeAPI = runtimeAPI
	s.containerRuntime = containerRuntime
	s.pool = pool
	s.mu.Unlock()

	// Pre-pull all active runtime images (nodejs, python, java, dotnet,
	// ruby, provided) in parallel so the first cold start of any runtime
	// skips the image pull entirely. This is the single biggest lever for
	// cold-start latency — base images are 200–500 MB and pulling them on
	// the first Invoke path can take minutes.
	// Run in background — do not block the container-runtime transition.
	go containerRuntime.SeedImages()

	// Pre-pull images for any functions that were persisted from a previous
	// session (e.g. after a restart with SQLite store). This ensures their
	// first invocation after restart is warm too.
	go s.seedPersistedFunctionImages(containerRuntime)

	s.log.Info("container runtime enabled")
}

// seedPersistedFunctionImages pre-pulls Docker images for functions that were
// persisted from a previous session (e.g. after a restart with SQLite store).
// This ensures the first invocation after restart is warm for those functions
// regardless of runtime. Active runtime images are already covered by SeedImages;
// this handles custom ImageUri functions where the image is not in activeRuntimes.
//
// It also reconciles functions stuck in "Pending" state from a previous session
// (e.g. the server restarted before the prewarmer callback could transition
// them). Once the image is confirmed available the function is flipped to
// "Active"; if the image pull fails it is marked "Failed".
func (s *Service) seedPersistedFunctionImages(cr *ContainerRuntime) {
	ctx := context.Background()
	fns, aerr := s.ls.listAllFunctions(ctx)
	if aerr != nil {
		s.log.Warn("cannot list persisted functions for image seeding", zap.Error(aerr))
		return
	}
	for _, fn := range fns {
		image, err := imageForFunction(fn)
		if err != nil {
			s.log.Debug("skip seed: cannot resolve image for persisted function",
				zap.String("function", fn.Name),
				zap.Error(err))
			continue
		}
		pullErr := cr.ensureImage(ctx, image, dockerPlatformForLambdaArchitectures(fn.Architectures))
		if pullErr != nil {
			s.log.Warn("seed pull failed for persisted function",
				zap.String("function", fn.Name),
				zap.String("image", image),
				zap.Error(pullErr))
		}

		// Reconcile functions stuck in Pending from a previous session.
		if fn.State == "Pending" {
			if pullErr != nil {
				fn.State = "Failed"
				fn.StateReason = "Failed to pull container image: " + pullErr.Error()
				fn.StateReasonCode = "ImagePullError"
			} else {
				fn.State = "Active"
				fn.StateReason = ""
				fn.StateReasonCode = ""
			}
			// Use the function's own region for the store key.
			fnCtx := middleware.ContextWithRegion(ctx, regionFromFunctionARN(fn.ARN))
			if serr := s.ls.putFunction(fnCtx, fn); serr != nil {
				s.log.Warn("failed to reconcile pending function state",
					zap.String("function", fn.Name),
					zap.String("target_state", fn.State),
					zap.String("error", serr.Message))
			} else {
				s.log.Info("reconciled pending function",
					zap.String("function", fn.Name),
					zap.String("state", fn.State))
			}
		}
	}
}

// InitESMDelivery wires SQS→Lambda and DynamoDB Streams→Lambda event delivery.
// Called by the router after all services are constructed and the event bus is
// available. receiver may be nil when the SQS service is not loaded.
func (s *Service) InitESMDelivery(receiver events.MessageReceiver, enqueuer events.MessageEnqueuer, bus *events.Bus) {
	mgr := newESMDeliveryManager(
		s.handler.esm,
		s.invoker,
		receiver,
		enqueuer,
		bus,
		s.log,
		s.handler.clk,
		s.cfg,
		context.Background(),
	)
	s.esmDelivery = mgr
	s.handler.esmDelivery = mgr
	// Resume delivery for any ESMs that were Enabled before restart.
	// Run asynchronously: with the hybrid store this would block startup
	// until the SQLite seed completes. Tracked via mgr.wg so Stop()'s
	// StopAll drain waits for it; uses mgr.baseCtx so shutdown is
	// observable should that ever be wired to a real cancel.
	mgr.wg.Add(1)
	go func() {
		defer mgr.wg.Done()
		mgr.ReloadAll(mgr.baseCtx)
	}()
}

// Stop shuts down the Runtime API server and any background resources.
func (s *Service) Stop(ctx context.Context) {
	if s.esmDelivery != nil {
		s.esmDelivery.StopAll()
	}
	// Wait for in-flight async invocations to finish.
	s.handler.StopAsync(ctx)
	// Stop background sweepers.
	s.tracker.Stop()
	s.mu.Lock()
	rapi := s.runtimeAPI
	pool := s.pool
	gc := s.gc
	s.mu.Unlock()
	// (Docker watcher is owned by the router; no cancel needed here.)
	if pool != nil {
		pool.Stop()
	}
	if rapi != nil {
		if err := rapi.Stop(ctx); err != nil {
			s.log.Error("lambda: runtime API shutdown error", zap.Error(err))
		}
	}
	// Clean up any Docker containers (warm pool, stuck, or orphaned).
	if gc != nil {
		gc.DrainAndSweep(ctx, "lambda")
	}
}

func (s *Service) Name() string { return "lambda" }

// PathPrefixes implements router.PathPrefixService. When Lambda is disabled,
// the router registers a 503 ServiceDisabled handler at this prefix so requests
// don't fall through to S3's /{bucket}/* wildcard and return XML errors.
func (s *Service) PathPrefixes() []string { return []string{"/2015-03-31"} }

// Invoker returns the FunctionInvoker for this Lambda service.
// Used by other services (e.g. S3 notifications) to invoke Lambda functions
// without creating an import cycle.
func (s *Service) Invoker() *ServiceInvoker { return s.invoker }

// SyncInvoker returns the FunctionSyncInvoker for this Lambda service.
// Used by API Gateway to invoke Lambda functions synchronously and receive
// the response payload.
func (s *Service) SyncInvoker() events.FunctionSyncInvoker { return s.invoker }

// RegisterRoutes mounts Lambda REST endpoints.
// Lambda uses versioned REST paths, not a single-dispatch target header.
func (s *Service) RegisterRoutes(r chi.Router) {
	const apiBase = "/2015-03-31"

	r.Post(apiBase+"/functions", s.handler.CreateFunction)
	r.Post(apiBase+"/functions/", s.handler.CreateFunction)
	r.Get(apiBase+"/functions", s.handler.ListFunctions)
	r.Get(apiBase+"/functions/", s.handler.ListFunctions)
	r.Post(apiBase+"/event-source-mappings", s.handler.CreateEventSourceMapping)
	r.Post(apiBase+"/event-source-mappings/", s.handler.CreateEventSourceMapping)
	r.Get(apiBase+"/event-source-mappings", s.handler.ListEventSourceMappings)
	r.Get(apiBase+"/event-source-mappings/", s.handler.ListEventSourceMappings)
	r.Get(apiBase+"/event-source-mappings/{uuid}", s.handler.GetEventSourceMapping)
	r.Put(apiBase+"/event-source-mappings/{uuid}", s.handler.UpdateEventSourceMapping)
	r.Delete(apiBase+"/event-source-mappings/{uuid}", s.handler.DeleteEventSourceMapping)
	r.Get(apiBase+"/functions/{name}", s.handler.GetFunction)
	r.Get(apiBase+"/functions/{name}/code-signing-config", s.handler.GetFunctionCodeSigningConfig)
	r.Delete(apiBase+"/functions/{name}", s.handler.DeleteFunction)
	r.Put(apiBase+"/functions/{name}/code", s.handler.UpdateFunctionCode)
	r.Get(apiBase+"/functions/{name}/configuration", s.handler.GetFunctionConfiguration)
	r.Put(apiBase+"/functions/{name}/configuration", s.handler.UpdateFunctionConfiguration)
	r.Put(apiBase+"/functions/{name}/concurrency", s.handler.PutFunctionConcurrency)
	r.Get(apiBase+"/functions/{name}/concurrency", s.handler.GetFunctionConcurrency)
	r.Delete(apiBase+"/functions/{name}/concurrency", s.handler.DeleteFunctionConcurrency)
	r.Put(apiBase+"/functions/{name}/provisioned-concurrency", s.handler.PutProvisionedConcurrencyConfig)
	r.Get(apiBase+"/functions/{name}/provisioned-concurrency", s.handler.GetProvisionedConcurrencyConfig)
	r.Post(apiBase+"/functions/{name}/invocations", s.handler.InvokeFunction)
	// InvokeWithResponseStream uses a different API version path.
	const streamBase = "/2021-11-15"
	r.Post(streamBase+"/functions/{name}/response-streaming-invocations", s.handler.InvokeWithResponseStream)
	// Emulator-only: SSE invoke with progress events for the web UI.
	r.Post(apiBase+"/functions/{name}/invoke-with-progress", s.handler.InvokeFunctionSSE)
	// Versions.
	r.Post(apiBase+"/functions/{name}/versions", s.handler.PublishVersion)
	r.Get(apiBase+"/functions/{name}/versions", s.handler.ListVersionsByFunction)
	// Aliases.
	r.Post(apiBase+"/functions/{name}/aliases", s.handler.CreateAlias)
	r.Get(apiBase+"/functions/{name}/aliases", s.handler.ListAliases)
	r.Get(apiBase+"/functions/{name}/aliases/{aliasName}", s.handler.GetAlias)
	r.Put(apiBase+"/functions/{name}/aliases/{aliasName}", s.handler.UpdateAlias)
	r.Delete(apiBase+"/functions/{name}/aliases/{aliasName}", s.handler.DeleteAlias)
	// Emulator-only: plain-text source code storage for the web UI editor.
	r.Get(apiBase+"/functions/{name}/source", s.handler.GetFunctionSource)
	r.Put(apiBase+"/functions/{name}/source", s.handler.PutFunctionSource)
	// Emulator-only: saved test events for the web UI Test tab.
	r.Get(apiBase+"/functions/{name}/test-events", s.handler.ListTestEvents)
	r.Put(apiBase+"/functions/{name}/test-events/{eventName}", s.handler.PutTestEvent)
	r.Delete(apiBase+"/functions/{name}/test-events/{eventName}", s.handler.DeleteTestEvent)
	// Layers — introduced 2018-10-31, separate API base from functions.
	const layerBase = "/2018-10-31"
	r.Get(layerBase+"/layers", s.handler.ListLayers)
	r.Get(layerBase+"/layers/", s.handler.ListLayers)
	r.Post(layerBase+"/layers/{layerName}/versions", s.handler.PublishLayerVersion)
	r.Get(layerBase+"/layers/{layerName}/versions", s.handler.ListLayerVersions)
	r.Get(layerBase+"/layers/{layerName}/versions/{versionNumber}", s.handler.GetLayerVersion)
	r.Delete(layerBase+"/layers/{layerName}/versions/{versionNumber}", s.handler.DeleteLayerVersion)

	// Emulator-specific: list warm/running instances for the topology map UI.
	r.Get("/_lambda/instances", s.handler.ListInstances)
	// Emulator-specific: runtime catalog for the web UI.
	r.Get("/_lambda/runtimes", s.handler.ListRuntimes)
	// Emulator-specific: layer zip metadata for the web UI.
	r.Get("/_lambda/layers/{layerName}/versions/{versionNumber}/metadata", s.handler.GetLayerVersionMetadata)
}

// runtimeAPIContainerAddr determines the host:port that Lambda containers use
// to reach the Runtime API server. The strategy depends on whether Overcast
// itself is running inside a Docker container.
func runtimeAPIContainerAddr(cfg *config.Config, dc *docker.Client, logger *zap.Logger, port int) string {

	// If running inside a Docker container (/.dockerenv exists), find our IP
	// on the Lambda network so sibling containers can reach us.
	if _, err := os.Stat("/.dockerenv"); err == nil {
		// We're inside a container. Get our hostname (= container ID).
		hostname, _ := os.Hostname()
		if hostname != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			// Attach ourselves to the Lambda network if not already.
			_ = dc.ConnectNetwork(ctx, cfg.LambdaNetwork, hostname)

			// Inspect the container to get our IP on the Lambda network.
			info, err := dc.InspectContainer(ctx, hostname)
			if err == nil {
				if net, ok := info.NetworkSettings.Networks[cfg.LambdaNetwork]; ok && net.IPAddress != "" {
					addr := fmt.Sprintf("%s:%d", net.IPAddress, port)
					logger.Info("lambda: Runtime API address (container mode)", zap.String("addr", addr))
					return addr
				}
			}
		}
	}

	// Running on the host — use host.docker.internal (Docker Desktop) or
	// the first non-loopback IP.
	if ip := hostReachableIP(); ip != "" {
		addr := fmt.Sprintf("%s:%d", ip, port)
		logger.Info("lambda: Runtime API address (host mode)", zap.String("addr", addr))
		return addr
	}

	// Last resort.
	return fmt.Sprintf("host.docker.internal:%d", port)
}

// hostReachableIP returns the first non-loopback IPv4 address on the host.
func hostReachableIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
			return ipNet.IP.String()
		}
	}
	return ""
}
