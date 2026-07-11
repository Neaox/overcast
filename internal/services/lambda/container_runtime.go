package lambda

// container_runtime.go — ContainerRuntime implements the Runtime interface using
// Docker containers with official AWS Lambda ECR images.
//
// Each Lambda function is executed inside a container from
// public.ecr.aws/lambda/{runtime}:{version}. The container communicates with
// Overcast's RuntimeAPIServer to receive invocations and return results.
//
// Warm instance reuse: after responding to an invocation, the container's built-in
// RIC loops back to GET /next and blocks — this IS warm reuse without restarting
// the container. The InstancePool manages the warm/cold lifecycle.

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
)

// ContainerRuntime implements Runtime by running Lambda functions in Docker
// containers using official AWS Lambda base images.
type ContainerRuntime struct {
	cfg              *config.Config
	clk              clock.Clock
	docker           *docker.Client
	gc               *docker.GC // async container cleanup with retries
	runtimeAPI       *RuntimeAPIServer
	logger           *zap.Logger
	network          string                             // Docker network name
	overcastEndpoint string                             // http://host:port — AWS_ENDPOINT_URL for containers
	pullOnce         sync.Map                           // image name → *sync.Once — ensures each image is pulled only once
	logWriter        events.LogWriter                   // nil until InitLogWriter is called
	bus              atomic.Pointer[events.Bus]         // nil until SetBus is called
	exitNotify       *exitNotifier                      // routes Docker watcher die events to per-container channels
	vpcResolver      atomic.Pointer[VPCNetworkResolver] // resolves subnet → VPC → Docker network
	layerFetcher     LayerContentFetcher                // resolves a layer ARN to zip bytes for /opt injection
	remoteFetcher    *RemoteLayerFetcher                // optional — fetches layers from real AWS
	coldStartSem     chan struct{}                      // bounds concurrent container creation/INIT bursts

	// initBurst tracks containers that are still in the INIT phase with burst
	// CPU. Keyed by function ARN → {containerID, steadyStateCPUs}.
	initBurstMu sync.Mutex
	initBurst   map[string]initBurstEntry
}

// SetLogWriter wires the CloudWatch Logs writer so container stdout/stderr is
// forwarded to CloudWatch. Safe to call at any time; the writer is picked up
// by the next Acquire call.
func (cr *ContainerRuntime) SetLogWriter(lw events.LogWriter) { cr.logWriter = lw }

// SetBus wires the event bus so image pull progress events are published.
// Safe to call at any time; picked up by the next ensureImage call.
func (cr *ContainerRuntime) SetBus(b *events.Bus) { cr.bus.Store(b) }

// SetVPCResolver wires the EC2 VPC resolver for connecting Lambda containers
// to VPC Docker networks.
func (cr *ContainerRuntime) SetVPCResolver(r VPCNetworkResolver) {
	cr.vpcResolver.Store(&r)
}

// LayerContentFetcher returns layer zip bytes for a layer version ARN.
// The returned bytes should be an immutable copy owned by the caller.
type LayerContentFetcher func(ctx context.Context, layerVersionARN string) ([]byte, error)

// SetLayerContentFetcher wires layer content retrieval for runtime injection.
func (cr *ContainerRuntime) SetLayerContentFetcher(fetcher LayerContentFetcher) {
	cr.layerFetcher = fetcher
}

// SetRemoteLayerFetcher wires the optional remote layer fetcher that downloads
// layers from real AWS when not available locally.
func (cr *ContainerRuntime) SetRemoteLayerFetcher(fetcher *RemoteLayerFetcher) {
	cr.remoteFetcher = fetcher
}

// connectVPCNetworks connects a running container to the VPC's Docker network
// if the function has a VpcConfig. This is called after the container starts
// on the default Lambda network, giving it connectivity to both the Runtime API
// and the VPC resources.
func (cr *ContainerRuntime) connectVPCNetworks(ctx context.Context, containerID string, fn *Function) error {
	if fn.VpcConfig == nil || fn.VpcConfig.VpcId == "" {
		return nil
	}
	rp := cr.vpcResolver.Load()
	if rp == nil {
		return nil
	}
	resolver := *rp
	status := resolver.VPCNetworkStatus(ctx, fn.VpcConfig.VpcId)
	switch status {
	case "", "ok", "shared", "remapped":
		// launchable
	case "conflict", "unbacked":
		return fmt.Errorf("lambda VPC %s is not launchable (network status=%s)", fn.VpcConfig.VpcId, status)
	default:
		return fmt.Errorf("lambda VPC %s is not launchable (network status=%s)", fn.VpcConfig.VpcId, status)
	}
	netID := resolver.DockerNetworkForVpc(ctx, fn.VpcConfig.VpcId)
	if netID == "" {
		return fmt.Errorf("lambda VPC %s has no Docker network", fn.VpcConfig.VpcId)
	}
	if err := cr.docker.ConnectNetwork(ctx, netID, containerID); err != nil {
		return fmt.Errorf("connect Lambda container to VPC network %s: %w", netID, err)
	}
	cr.logger.Info("connected Lambda container to VPC network",
		zap.String("function", fn.Name),
		zap.String("vpc", fn.VpcConfig.VpcId),
		zap.String("network", netID))
	return nil
}

// NewContainerRuntime creates a ContainerRuntime.
// The Docker client and RuntimeAPIServer must already be initialised.
func NewContainerRuntime(
	cfg *config.Config,
	clk clock.Clock,
	docker *docker.Client,
	gc *docker.GC,
	runtimeAPI *RuntimeAPIServer,
	logger *zap.Logger,
) *ContainerRuntime {
	// Derive the Overcast emulator endpoint from the Runtime API address.
	// The Runtime API host is the IP that Lambda containers can route to on
	// the overcast_lambda Docker network — the same IP serves the main HTTP
	// API on cfg.Port. Setting AWS_ENDPOINT_URL lets function code call S3,
	// SQS, DynamoDB, etc. back into Overcast without any SDK configuration.
	runtimeHost, _, _ := net.SplitHostPort(runtimeAPI.Addr())
	overcastEndpoint := fmt.Sprintf("http://%s:%d", runtimeHost, cfg.Port)

	limit := cfg.LambdaDockerMaxConcurrentStarts
	if limit < 1 {
		limit = 1
	}

	return &ContainerRuntime{
		cfg:              cfg,
		clk:              clk,
		docker:           docker,
		gc:               gc,
		runtimeAPI:       runtimeAPI,
		logger:           logger,
		network:          cfg.LambdaNetwork,
		overcastEndpoint: overcastEndpoint,
		exitNotify:       newExitNotifier(),
		coldStartSem:     make(chan struct{}, limit),
	}
}

func (cr *ContainerRuntime) acquireColdStartSlot(ctx context.Context) (func(), error) {
	if cr.coldStartSem == nil {
		return func() {}, nil
	}
	select {
	case cr.coldStartSem <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() { <-cr.coldStartSem })
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ProgressFunc is called by AcquireWithProgress to report lifecycle steps to
// the caller (e.g. an SSE endpoint streaming progress to the UI).
type ProgressFunc func(step string)

// activeRuntimes lists runtime IDs that ContainerRuntime supports.
// These have official images on public.ecr.aws/lambda/.
var activeRuntimes = map[string]string{
	// Node.js
	"nodejs20.x": "public.ecr.aws/lambda/nodejs:20",
	"nodejs22.x": "public.ecr.aws/lambda/nodejs:22",
	"nodejs24.x": "public.ecr.aws/lambda/nodejs:24",
	// Python
	"python3.9":  "public.ecr.aws/lambda/python:3.9",
	"python3.10": "public.ecr.aws/lambda/python:3.10",
	"python3.11": "public.ecr.aws/lambda/python:3.11",
	"python3.12": "public.ecr.aws/lambda/python:3.12",
	"python3.13": "public.ecr.aws/lambda/python:3.13",
	// Java
	"java11": "public.ecr.aws/lambda/java:11",
	"java17": "public.ecr.aws/lambda/java:17",
	"java21": "public.ecr.aws/lambda/java:21",
	// .NET
	"dotnet8": "public.ecr.aws/lambda/dotnet:8",
	// Ruby
	"ruby3.2": "public.ecr.aws/lambda/ruby:3.2",
	"ruby3.3": "public.ecr.aws/lambda/ruby:3.3",
	// Custom runtime
	"provided.al2023": "public.ecr.aws/lambda/provided:al2023",
}

// CanHandle returns true for all active (non-deprecated) runtime IDs that have
// official ECR images, and for PackageType=Image functions (runtimeID "image").
func (cr *ContainerRuntime) CanHandle(runtimeID string) bool {
	if runtimeID == "image" {
		return true
	}
	_, ok := activeRuntimes[runtimeID]
	return ok
}

// imageForFunction resolves the Docker image that backs fn. Returns an
// error if PackageType=Image has no ImageUri, or if the zip runtime is
// unknown.
func imageForFunction(fn *Function) (string, error) {
	if fn.PackageType == "Image" {
		if fn.ImageUri == "" {
			return "", fmt.Errorf("PackageType=Image but no ImageUri set for %q", fn.Name)
		}
		return fn.ImageUri, nil
	}
	image, ok := activeRuntimes[fn.Runtime]
	if !ok {
		return "", fmt.Errorf("no image for runtime %q", fn.Runtime)
	}
	return image, nil
}

// PrewarmFunction starts a background pull of fn's Docker image so the
// first Invoke doesn't pay the cold-pull cost on the request path. Safe to
// call from CreateFunction — if the image is already cached or in flight,
// the sync.Once inside ensureImage coalesces the work. onReady is invoked
// (on the background goroutine) after the pull completes; it can be nil.
// err passed to onReady is the pull result.
func (cr *ContainerRuntime) PrewarmFunction(fn *Function, onReady func(err error)) {
	image, err := imageForFunction(fn)
	if err != nil {
		if onReady != nil {
			onReady(err)
		}
		return
	}
	go func() {
		pullErr := cr.ensureImage(context.Background(), image)
		if onReady != nil {
			onReady(pullErr)
		}
	}()
}

// Acquire creates and starts a Docker container for fn, then returns a
// containerInstance that can invoke the function via the Runtime API.
func (cr *ContainerRuntime) Acquire(ctx context.Context, fn *Function) (RuntimeInstance, error) {
	return cr.acquireContainer(ctx, fn, func(string) {})
}

// Release is a no-op for ContainerRuntime itself — InstancePool wraps it and
// handles warm-instance storage and eviction.
func (cr *ContainerRuntime) Release(_ context.Context, _ RuntimeInstance, _ bool) {}

// AcquireWithProgress is like Acquire but calls progress at each lifecycle step
// so callers (e.g. the SSE invoke endpoint) can stream status to the UI.
func (cr *ContainerRuntime) AcquireWithProgress(ctx context.Context, fn *Function, progress ProgressFunc) (RuntimeInstance, error) {
	return cr.acquireContainer(ctx, fn, progress)
}

// acquireContainer is the single implementation behind Acquire and
// AcquireWithProgress. The progress callback is called at each lifecycle step;
// callers that don't need progress pass a no-op.
func (cr *ContainerRuntime) acquireContainer(ctx context.Context, fn *Function, progress ProgressFunc) (RuntimeInstance, error) {
	isImage := fn.PackageType == "Image"

	image, err := imageForFunction(fn)
	if err != nil {
		return nil, err
	}

	// Ensure the image is pulled (lazy, once per image).
	exists, _ := cr.docker.ImageExists(ctx, image)
	if !exists {
		progress("Pulling image " + image)
	}
	if err := cr.ensureImage(ctx, image); err != nil {
		return nil, fmt.Errorf("pull image: %w", err)
	}
	if !exists {
		progress("Image ready")
	}

	progress("Waiting for cold-start capacity")
	releaseColdStart, err := cr.acquireColdStartSlot(ctx)
	if err != nil {
		return nil, fmt.Errorf("wait for lambda cold-start slot: %w", err)
	}
	defer releaseColdStart()

	hotReloadPath, err := hotReloadBindPath(fn, cr.cfg.LambdaHotReload)
	if err != nil {
		return nil, err
	}
	if hotReloadPath != "" {
		if msg := typeScriptSourceDiagnostic(hotReloadPath, fn.Runtime); msg != "" {
			cr.logger.Warn(msg)
		}
	}

	// For zip deployments, build a tar archive from the code zip.
	var codeTar []byte
	if !isImage && hotReloadPath == "" {
		progress("Preparing function code")
		codeTar, err = zipToTar(fn.CodeZip)
		if err != nil {
			return nil, fmt.Errorf("build code tar: %w", err)
		}
	}

	logStream := lambdaLogStreamName(cr.clk)
	env := cr.buildEnv(fn, logStream)
	containerName := fmt.Sprintf("overcast-lambda-%s-%d", sanitizeName(fn.Name), cr.clk.Now().UnixNano())

	progress("Creating container")
	ccfg := &docker.ContainerConfig{
		Image:  image,
		Env:    env,
		Labels: docker.ManagedLabels("lambda", fn.Name),
	}
	// For zip functions, CMD is the handler. For image functions, the image's
	// built-in ENTRYPOINT+CMD are used unless ImageConfig provides overrides.
	if !isImage && fn.Handler != "" {
		ccfg.Cmd = []string{fn.Handler}
	} else if isImage && fn.ImageConfig != nil {
		ccfg.Entrypoint = fn.ImageConfig.EntryPoint
		ccfg.Cmd = fn.ImageConfig.Command
		ccfg.WorkingDir = fn.ImageConfig.WorkingDirectory
	}

	// Start with burst CPU for fast INIT, throttled to steady state once the
	// RIC issues its first GET /next (see ThrottleInitBurst).
	req := &docker.CreateContainerRequest{
		ContainerConfig: ccfg,
		HostConfig: &docker.HostConfig{
			Binds:       bindMountTaskDir(hotReloadPath),
			NetworkMode: cr.network,
			Memory:      int64(fn.MemorySize) * 1024 * 1024,
			MemorySwap:  int64(fn.MemorySize) * 1024 * 1024, // disable swap
			NanoCPUs:    int64(initBurstCPUs * 1e9),
		},
	}

	id, err := cr.docker.CreateContainer(ctx, containerName, req)
	if err != nil {
		return nil, fmt.Errorf("create container: %w", decorateHotReloadMountError(err, hotReloadPath))
	}

	var containerIP string
	var registeredIP string
	if inspect, inspectErr := cr.docker.InspectContainer(ctx, id); inspectErr == nil {
		containerIP = cr.extractContainerIP(inspect)
		if containerIP != "" {
			cr.runtimeAPI.RegisterContainer(containerIP, fn.ARN)
			registeredIP = containerIP
		}
	} else {
		cr.logger.Debug("inspect lambda container before start", zap.String("container", id[:12]), zap.Error(inspectErr))
	}

	// cleanup removes the container on any error after creation.
	cleanup := func() {
		if registeredIP != "" {
			cr.runtimeAPI.UnregisterContainer(registeredIP)
		}
		_ = cr.docker.RemoveContainerForce(id)
	}

	// Copy code into the container before starting it (zip deployments only).
	if !isImage && hotReloadPath == "" {
		progress("Copying code to container")
		if err := cr.docker.CopyToContainer(ctx, id, "/var/task", bytes.NewReader(codeTar)); err != nil {
			cleanup()
			return nil, fmt.Errorf("copy code to container: %w", err)
		}
	}

	// Copy attached layer contents into /opt before starting the container.
	if len(fn.Layers) > 0 {
		progress("Injecting layer content")
	}
	if err := cr.copyLayersToContainer(ctx, id, fn); err != nil {
		cleanup()
		return nil, fmt.Errorf("copy layers to container: %w", err)
	}

	progress("Starting container")
	if err := cr.docker.StartContainer(ctx, id); err != nil {
		cleanup()
		return nil, fmt.Errorf("start container: %w", decorateHotReloadMountError(err, hotReloadPath))
	}

	// Connect to VPC Docker network if the function has a VpcConfig.
	if err := cr.connectVPCNetworks(ctx, id, fn); err != nil {
		cleanup()
		return nil, err
	}

	// Register for INIT-burst throttle-down when the RIC first polls /next.
	cr.registerInitBurst(fn.ARN, id, fn.MemorySize)

	progress("Waiting for runtime to initialize")
	if containerIP == "" {
		containerIP, err = cr.awaitContainerIP(ctx, id)
		if err != nil {
			cr.clearInitBurst(fn.ARN)
			cleanup()
			return nil, err
		}
		cr.runtimeAPI.RegisterContainer(containerIP, fn.ARN)
		registeredIP = containerIP
	}

	cr.logger.Info("lambda container started",
		zap.String("function", fn.Name),
		zap.String("container", id[:12]),
		zap.String("image", image),
		zap.String("container_ip", containerIP),
		zap.String("log_stream", logStream),
	)

	return cr.newContainerInstance(id, containerIP, fn, logStream), nil
}

// awaitContainerIP polls the Docker daemon for the container's IP address on
// the Lambda network. Uses exponential backoff starting at 25ms.
func (cr *ContainerRuntime) awaitContainerIP(ctx context.Context, containerID string) (string, error) {
	retryDelay := 25 * time.Millisecond
	for attempt := 0; attempt < 20; attempt++ {
		inspect, err := cr.docker.InspectContainer(ctx, containerID)
		if err != nil {
			return "", fmt.Errorf("inspect container: %w", err)
		}

		// Bail out immediately if the container already died (e.g. image
		// entrypoint crashed on boot).
		if !inspect.State.Running && inspect.State.Status != "created" {
			logs, _ := cr.docker.ContainerLogs(ctx, containerID, "50")
			if len(logs) > 0 {
				return "", fmt.Errorf("container %s exited during startup (status=%s exit=%d): %s",
					containerID[:12], inspect.State.Status, inspect.State.ExitCode, strings.TrimSpace(string(logs)))
			}
			return "", fmt.Errorf("container %s exited during startup (status=%s exit=%d)",
				containerID[:12], inspect.State.Status, inspect.State.ExitCode)
		}

		if ip := cr.extractContainerIP(inspect); ip != "" {
			return ip, nil
		}

		cr.logger.Debug("waiting for container IP assignment",
			zap.String("container", containerID[:12]),
			zap.Int("attempt", attempt+1))
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-cr.clk.After(retryDelay):
		}
		if retryDelay < 250*time.Millisecond {
			retryDelay *= 2
		}
	}
	return "", fmt.Errorf("could not determine container IP for %s", containerID[:12])
}

// extractContainerIP returns the container's IP on the Lambda network, or
// falls back to the first available network IP.
func (cr *ContainerRuntime) extractContainerIP(inspect *docker.ContainerInspect) string {
	if nw, ok := inspect.NetworkSettings.Networks[cr.network]; ok && nw.IPAddress != "" {
		return nw.IPAddress
	}
	for _, nw := range inspect.NetworkSettings.Networks {
		if nw.IPAddress != "" {
			return nw.IPAddress
		}
	}
	return ""
}

// newContainerInstance builds a containerInstance from the created container.
func (cr *ContainerRuntime) newContainerInstance(id, containerIP string, fn *Function, logStream string) *containerInstance {
	logRegion := regionFromFunctionARN(fn.ARN)
	if logRegion == "" {
		logRegion = cr.cfg.Region
	}
	logCtx, logCancel := context.WithCancel(middleware.ContextWithRegion(context.Background(), logRegion))

	ci := &containerInstance{
		id:             id,
		containerIP:    containerIP,
		functionName:   fn.Name,
		functionARN:    fn.ARN,
		codeHash:       functionCodeIdentity(fn),
		memorySize:     fn.MemorySize,
		logStream:      logStream,
		logGroupName:   fn.logGroupName(),
		docker:         cr.docker,
		gc:             cr.gc,
		runtimeAPI:     cr.runtimeAPI,
		logger:         cr.logger,
		clk:            cr.clk,
		logWriter:      cr.logWriter,
		logCtx:         logCtx,
		logCancel:      logCancel,
		exitNotify:     cr.exitNotify,
		logDone:        make(chan struct{}),
		healthy:        true,
		keepContainers: cr.cfg.LambdaKeepContainers,
		readyCh:        cr.runtimeAPI.ReadyChan(containerIP),
	}

	if cr.logWriter != nil {
		go ci.streamLogs()
	} else {
		close(ci.logDone)
	}
	return ci
}

// codeHashOf returns the SHA-256 hex digest of a zip payload.
// Used to detect when UpdateFunctionCode has replaced the deployment package.
func codeHashOf(zip []byte) string {
	h := sha256.Sum256(zip)
	return hex.EncodeToString(h[:])
}

// imageHash returns a SHA-256 hex digest of an image URI string.
// Used by InstancePool to detect stale instances after an image URI change.
func imageHash(uri string) string {
	h := sha256.Sum256([]byte(uri))
	return hex.EncodeToString(h[:])
}

func bindMountTaskDir(hostPath string) []string {
	if hostPath == "" {
		return nil
	}
	return []string{hostPath + ":/var/task:ro"}
}

// ─── Image management ──────────────────────────────────────────────────────

// SeedImages pre-pulls Docker images for all active runtimes (nodejs, python,
// java, dotnet, ruby, provided) in parallel so the first cold start of any
// runtime skips the image pull entirely. The seed runs in a background goroutine
// with a detached context — it does not block startup or callers. Call after
// the ContainerRuntime is fully wired (i.e. after initDockerRuntime).
//
// Pre-pulling at startup is the single biggest lever for cold-start latency:
// the base images are 200–500 MB and pulling them on the first Invoke path can
// take minutes. By the time the user creates a function and invokes it the
// images are already cached locally.
func (cr *ContainerRuntime) SeedImages() {
	// Deduplicate images — activeRuntimes maps distinct runtimes to the same
	// underlying image (e.g. python3.9 and python3.10 both resolve to
	// public.ecr.aws/lambda/python:3.9 and 3.10). Pull only unique images.
	uniq := make(map[string]struct{}, len(activeRuntimes))
	for _, image := range activeRuntimes {
		uniq[image] = struct{}{}
	}
	images := make([]string, 0, len(uniq))
	for image := range uniq {
		images = append(images, image)
	}

	// Concurrent pulls with a bounded worker pool so we don't flood the
	// Docker daemon or the user's network on startup. Return immediately —
	// workers drain the channel in background goroutines.
	const seedWorkers = 4
	jobs := make(chan string, len(images))

	for w := 0; w < seedWorkers && w < len(images); w++ {
		go func() {
			for image := range jobs {
				start := cr.clk.Now()
				err := cr.ensureImage(context.Background(), image)
				if err != nil {
					cr.logger.Warn("seed pull failed",
						zap.String("image", image),
						zap.Duration("elapsed", cr.clk.Since(start)),
						zap.Error(err))
				} else {
					cr.logger.Info("seed pull complete",
						zap.String("image", image),
						zap.Duration("elapsed", cr.clk.Since(start)))
				}
			}
		}()
	}

	for _, img := range images {
		jobs <- img
	}
	close(jobs)
}

func (cr *ContainerRuntime) ensureImage(ctx context.Context, image string) error {
	// Check if already pulled.
	exists, err := cr.docker.ImageExists(ctx, image)
	if err == nil && exists {
		return nil
	}

	// Use sync.Once per image to avoid concurrent pulls.
	once, _ := cr.pullOnce.LoadOrStore(image, &sync.Once{})
	var pullErr error
	once.(*sync.Once).Do(func() {
		cr.logger.Info("pulling Lambda image (first use)", zap.String("image", image))
		// Capture bus and clock once; either may be nil in tests or before wiring.
		bus := cr.bus.Load()
		clk := cr.clk
		if bus != nil && clk != nil {
			bus.Publish(context.Background(), events.Event{
				Type:    events.LambdaImagePulling,
				Time:    clk.Now(),
				Source:  "lambda",
				Payload: events.LambdaImagePullPayload{Image: image},
			})
		}
		var startTime time.Time
		if clk != nil {
			startTime = clk.Now()
		}
		pullErr = cr.docker.PullImage(ctx, image)
		if pullErr != nil {
			// Reset so we retry on next call.
			cr.pullOnce.Delete(image)
		}
		if bus != nil && clk != nil {
			var errStr string
			if pullErr != nil {
				errStr = pullErr.Error()
			}
			bus.Publish(context.Background(), events.Event{
				Type:   events.LambdaImagePullComplete,
				Time:   clk.Now(),
				Source: "lambda",
				Payload: events.LambdaImagePullPayload{
					Image:     image,
					ElapsedMs: clk.Now().Sub(startTime).Milliseconds(),
					Error:     errStr,
				},
			})
		}
	})
	return pullErr
}

// ─── Environment variables ─────────────────────────────────────────────────

func (cr *ContainerRuntime) buildEnv(fn *Function, logStream string) []string {
	// AWS_REGION must reflect the function's actual region (encoded in its
	// ARN), not the emulator's global default. SDKs sign requests with this
	// region; if we used the default (e.g. us-east-1) for a function deployed
	// to ap-southeast-2, downstream service calls (DynamoDB, S3, etc.) would
	// be routed to the wrong region's data and resources would appear missing.
	region := regionFromFunctionARN(fn.ARN)
	if region == "" {
		region = cr.cfg.Region
	}
	env := []string{
		"AWS_LAMBDA_FUNCTION_NAME=" + fn.Name,
		"AWS_LAMBDA_FUNCTION_VERSION=$LATEST",
		fmt.Sprintf("AWS_LAMBDA_FUNCTION_MEMORY_SIZE=%d", fn.MemorySize),
		"AWS_LAMBDA_LOG_GROUP_NAME=" + fn.logGroupName(),
		"AWS_LAMBDA_LOG_STREAM_NAME=" + logStream,
		// Real Lambda always sets this; Powertools and other observability
		// libraries use it for cold-start classification.
		"AWS_LAMBDA_INITIALIZATION_TYPE=on-demand",
		"AWS_REGION=" + region,
		"AWS_DEFAULT_REGION=" + region,
		"AWS_ACCOUNT_ID=" + cr.cfg.AccountID,
		"AWS_ACCESS_KEY_ID=overcast",
		"AWS_SECRET_ACCESS_KEY=overcast",
		// Real Lambda always sets a session token from execution-role
		// assumption. Some SDK credential providers (notably the JS v3
		// fromEnv chain) treat the absence of this variable as "not a
		// Lambda environment" and fall through to other providers, so we
		// always provide a placeholder.
		"AWS_SESSION_TOKEN=overcast",
		"AWS_LAMBDA_RUNTIME_API=" + cr.runtimeAPI.Addr(),
		"LAMBDA_TASK_ROOT=/var/task",
		fmt.Sprintf("AWS_LAMBDA_FUNCTION_TIMEOUT=%d", fn.Timeout),
		"TZ=:/etc/localtime",
		// Route all AWS SDK calls from the function back to the Overcast emulator.
		// AWS SDKs v2+ honour AWS_ENDPOINT_URL for every service automatically.
		"AWS_ENDPOINT_URL=" + cr.overcastEndpoint,
	}
	// _HANDLER and AWS_EXECUTION_ENV are set only for zip deployments where
	// Runtime and Handler are specified. Image functions use the image's
	// ENTRYPOINT and do not have a Runtime string.
	if fn.Handler != "" {
		env = append(env, "_HANDLER="+fn.Handler)
	}
	if fn.Runtime != "" && fn.Runtime != "image" {
		env = append(env, "AWS_EXECUTION_ENV=AWS_Lambda_"+fn.Runtime)
	}

	// User-defined env vars override above (merged last).
	for k, v := range fn.Environment {
		env = append(env, k+"="+v)
	}

	return env
}

// ─── containerInstance ─────────────────────────────────────────────────────

// containerInstance implements RuntimeInstance for a running Docker container.
type containerInstance struct {
	id             string
	containerIP    string // IP on the Lambda Docker network
	functionName   string
	functionARN    string
	codeHash       string // SHA-256 of the CodeZip at creation time
	memorySize     int    // configured memory in MB
	logStream      string
	logGroupName   string
	docker         *docker.Client
	gc             *docker.GC // async fallback when direct removal fails
	runtimeAPI     *RuntimeAPIServer
	logger         *zap.Logger
	clk            clock.Clock
	logWriter      events.LogWriter   // nil if CWL not wired
	logCtx         context.Context    // cancelled on Close
	logCancel      context.CancelFunc // cancels logCtx
	exitNotify     *exitNotifier      // Docker watcher exit notifications
	tailMu         sync.Mutex
	tailBuf        []byte        // last ≤4096 bytes of stdout+stderr for X-Amz-Log-Result
	logReadAt      atomic.Int64  // UnixNano of last Docker log read; 0 until first read
	logInFlight    atomic.Int64  // lines parsed by scanner but not yet flushed to CWL
	logCursor      logCursor     // exact Docker timestamp cursor used for reconnect/reconcile deduplication
	logDone        chan struct{} // closed when streamLogs goroutine exits
	readyCh        <-chan struct{}
	healthy        bool
	keepContainers bool // when true, Close only stops the container instead of removing it
}

func (ci *containerInstance) AwaitReady(ctx context.Context) error {
	if ci.readyCh == nil {
		return nil
	}
	var exitCh <-chan string
	var waitCancel context.CancelFunc
	if ci.exitNotify != nil && ci.id != "" {
		exitCh = ci.exitNotify.register(ci.id)
		defer ci.exitNotify.unregister(ci.id)
	} else if ci.docker != nil && ci.id != "" {
		strCh := make(chan string, 1)
		exitCh = strCh
		var waitCtx context.Context
		waitCtx, waitCancel = context.WithCancel(ctx)
		defer waitCancel()
		go func() {
			exitCode, err := ci.docker.WaitContainer(waitCtx, ci.id)
			if err == nil {
				strCh <- fmt.Sprintf("%d", exitCode)
			}
		}()
	}
	select {
	case <-ci.readyCh:
		if waitCancel != nil {
			waitCancel()
		}
		return nil
	case exitCode := <-exitCh:
		ci.healthy = false
		return fmt.Errorf("lambda container exited during init (exit code %s)", exitCode)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// LogStreamName returns the AWS-style log stream assigned to this container.
func (ci *containerInstance) LogStreamName() string { return ci.logStream }

// FunctionName returns the name of the Lambda function this container runs.
func (ci *containerInstance) FunctionName() string { return ci.functionName }

// CodeHash returns the SHA-256 hex of the deployment zip this instance was built from.
func (ci *containerInstance) CodeHash() string { return ci.codeHash }

// Invoke sends the event to the container via the Runtime API and waits for
// the result. The container's RIC picks up the event from GET /next, runs the
// handler, and POSTs the result back to /response or /error.
//
// The caller is responsible for bounding ctx with the function's configured
// Timeout; this method trusts ctx.Deadline() to derive the Lambda-Runtime-Deadline-Ms
// header, which is what the RIC uses to implement context.getRemainingTimeInMillis().
func (ci *containerInstance) Invoke(ctx context.Context, event []byte) (*InvokeResult, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		// Fallback: should not happen in normal operation because invokeSync
		// always applies a timeout, but kept as a safety net.
		deadline = ci.clk.Now().Add(900 * time.Second)
	}

	// Reset tail buffer so this invocation's output starts fresh
	// (important for warm-container reuse across multiple invocations).
	ci.tailMu.Lock()
	ci.tailBuf = ci.tailBuf[:0]
	ci.tailMu.Unlock()

	start := ci.clk.Now()
	reqID, resultCh := ci.runtimeAPI.SubmitInvocation(ci.functionARN, event, deadline)

	ci.logger.Debug("invoke submitted",
		zap.String("container", ci.id[:12]),
		zap.String("request_id", reqID),
		zap.Int("event_bytes", len(event)),
	)

	// Emit the START line that real Lambda writes before every invocation.
	ci.writeLogLine(ctx, fmt.Sprintf("START RequestId: %s Version: $LATEST", reqID))

	// Monitor container exit via the Docker event watcher (if wired) or fall
	// back to a per-invocation WaitContainer goroutine. The watcher path
	// avoids spawning a goroutine + blocking HTTP connection per invocation.
	var exitCh <-chan string
	var waitCancel context.CancelFunc
	if ci.exitNotify != nil {
		exitCh = ci.exitNotify.register(ci.id)
		defer ci.exitNotify.unregister(ci.id)
	} else {
		// Fallback: no watcher (e.g. bus not wired). Use WaitContainer.
		strCh := make(chan string, 1)
		exitCh = strCh
		var waitCtx context.Context
		waitCtx, waitCancel = context.WithCancel(ctx)
		go func() {
			exitCode, err := ci.docker.WaitContainer(waitCtx, ci.id)
			if err == nil {
				strCh <- fmt.Sprintf("%d", exitCode)
			}
		}()
	}

	var resp invokeResponse
	select {
	case resp = <-resultCh:
		if waitCancel != nil {
			waitCancel() // stop WaitContainer goroutine — container stays alive for reuse
		}
	case exitCode := <-exitCh:
		if waitCancel != nil {
			waitCancel()
		}
		ci.healthy = false
		// Cancel the pending invocation so its ResultCh is closed and no
		// drain goroutine is needed. This also removes the map entry.
		ci.runtimeAPI.CancelInvocation(reqID)
		// Drain function output before writing END/REPORT and tearing down
		// the container — otherwise any logs the function emitted before
		// crashing would be lost when logCtx is cancelled.
		if ci.logWriter != nil {
			ci.waitForLogDrain(context.Background())
		}
		elapsed := ci.clk.Now().Sub(start)
		ci.writeLogLine(context.Background(),
			fmt.Sprintf("END RequestId: %s", reqID))
		ci.writeLogLine(context.Background(),
			fmt.Sprintf("REPORT RequestId: %s\tDuration: %.2f ms\tBilled Duration: %d ms\tMemory Size: %d MB\tMax Memory Used: %d MB\tStatus: error",
				reqID, float64(elapsed.Microseconds())/1000.0, billedDuration(elapsed), ci.memorySize, ci.currentMemoryMB()))
		return nil, fmt.Errorf("lambda container exited unexpectedly (exit code %s) — check container logs for details", exitCode)
	case <-ctx.Done():
		if waitCancel != nil {
			waitCancel()
		}
		ci.healthy = false
		// Cancel the pending invocation so its ResultCh is closed and no
		// drain goroutine is needed. This also removes the map entry.
		ci.runtimeAPI.CancelInvocation(reqID)
		// Drain function output before writing END/REPORT and tearing down
		// the container — otherwise any logs the function emitted before
		// the timeout would be lost when logCtx is cancelled. Use a fresh
		// context (the invocation ctx is already done).
		if ci.logWriter != nil {
			ci.waitForLogDrain(context.Background())
		}
		elapsed := ci.clk.Now().Sub(start)
		ci.writeLogLine(context.Background(),
			fmt.Sprintf("END RequestId: %s", reqID))
		ci.writeLogLine(context.Background(),
			fmt.Sprintf("REPORT RequestId: %s\tDuration: %.2f ms\tBilled Duration: %d ms\tMemory Size: %d MB\tMax Memory Used: %d MB\tStatus: timeout",
				reqID, float64(elapsed.Microseconds())/1000.0, billedDuration(elapsed), ci.memorySize, ci.currentMemoryMB()))
		return nil, fmt.Errorf("lambda invoke timed out: %w", ctx.Err())
	}

	elapsed := ci.clk.Now().Sub(start)

	// Build the result before reading the tail so the function has had the
	// maximum opportunity to flush its stdout/stderr.
	var result *InvokeResult
	if resp.IsInitError {
		ci.healthy = false
		result = &InvokeResult{
			StatusCode:    200,
			Payload:       resp.ErrorPayload,
			FunctionError: "Unhandled",
		}
	} else if resp.FunctionError != "" {
		payload := resp.ErrorPayload
		if payload == nil {
			payload = resp.Payload
		}
		result = &InvokeResult{
			StatusCode:    200,
			Payload:       payload,
			FunctionError: resp.FunctionError,
		}
	} else {
		result = &InvokeResult{
			StatusCode: 200,
			Payload:    resp.Payload,
		}
	}

	// Wait briefly for the scanner to catch up on handler stdout so the
	// X-Amz-Log-Result tail snapshot includes the function's output. We do
	// NOT wait for CloudWatch flushes — those happen asynchronously after
	// Invoke returns. This matches AWS / LocalStack semantics: invoke
	// latency is decoupled from log delivery.
	if ci.logWriter != nil {
		ci.waitForScannerIdle()
	}

	// Emit END + REPORT lines that real Lambda writes after every invocation.
	// These are enqueued onto the synth channel for async batching alongside
	// handler output — Invoke does not wait for their CWL delivery.
	ci.writeLogLine(ctx, fmt.Sprintf("END RequestId: %s", reqID))
	ci.writeLogLine(ctx, fmt.Sprintf(
		"REPORT RequestId: %s\tDuration: %.2f ms\tBilled Duration: %d ms\tMemory Size: %d MB\tMax Memory Used: %d MB",
		reqID, float64(elapsed.Microseconds())/1000.0, billedDuration(elapsed), ci.memorySize, ci.currentMemoryMB()))

	// Snapshot the tail buffer for X-Amz-Log-Result.
	if ci.logWriter != nil {
		ci.tailMu.Lock()
		if n := len(ci.tailBuf); n > 0 {
			snap := ci.tailBuf
			if n > 4096 {
				snap = snap[n-4096:]
			}
			result.LogResult = base64.StdEncoding.EncodeToString(snap)
		}
		ci.tailMu.Unlock()
	}

	return result, nil
}

// currentMemoryMB queries Docker for the container's current memory usage (RSS)
// and returns it in megabytes. More representative per-invocation than max_usage
// which is a lifetime cgroup peak. Returns 0 on error (best-effort).
func (ci *containerInstance) currentMemoryMB() int {
	// The Docker stats endpoint (stream=false) needs two cgroup reads, which
	// can take >500 ms in Docker-in-Docker / devcontainer setups. Use a 2 s
	// timeout so we don't log spurious errors on slower hosts.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	bytes, err := ci.docker.ContainerMemoryUsage(ctx, ci.id)
	if err != nil {
		ci.logger.Debug("container stats: could not get memory usage (best-effort, non-fatal)", zap.Error(err))
		return 0
	}
	return int(bytes / (1024 * 1024))
}

// waitForScannerIdle waits briefly for the Docker log reader to go idle so
// the rolling tail buffer is up-to-date with handler output. Unlike
// waitForLogDrain it does NOT wait for CloudWatch writes to complete — those
// are batched asynchronously by streamLogs and are not on the invoke critical
// path.
//
// This is called on the invoke success path before snapshotting tailBuf for
// X-Amz-Log-Result. The cap is intentionally small (100 ms) because:
//   - typical Docker pipe flush latency is sub-millisecond on Linux,
//   - the scanner must have already produced output for logReadAt to be set,
//     which happens for every invocation that emits a non-empty stdout line,
//   - capping at 100 ms bounds worst-case invoke latency from log machinery.
//
// If the function emits no stdout (logReadAt == 0), this returns immediately.
func (ci *containerInstance) waitForScannerIdle() {
	const (
		idleThreshold = 5 * time.Millisecond
		deadlineMax   = 100 * time.Millisecond
		tickInterval  = 1 * time.Millisecond
	)
	deadline := ci.clk.Timer(deadlineMax)
	defer deadline.Stop()
	tick := ci.clk.Ticker(tickInterval)
	defer tick.Stop()
	for {
		select {
		case <-deadline.C:
			return
		case <-tick.C:
			if ci.logInFlight.Load() > 0 {
				continue
			}
			last := ci.logReadAt.Load()
			if last == 0 {
				// Function emitted nothing this invocation — nothing to wait for.
				return
			}
			if ci.clk.Since(time.Unix(0, last)) >= idleThreshold {
				return
			}
		}
	}
}

// waitForLogDrain blocks until the streamLogs pipeline is fully quiescent —
// i.e. every byte Docker has so far delivered has been parsed into a line AND
// every parsed line has been written to CloudWatch Logs. This is essential
// before tearing down the container (the streaming HTTP connection is closed
// when logCtx is cancelled, so anything still buffered in the kernel/Docker
// pipe is lost) and before writing END/REPORT lines so that ordering matches
// AWS.
//
// Two signals are combined:
//   - logReadAt: timestamp of the last successful Read from the Docker log
//     stream. "Idle" = no new bytes for ≥10 ms. This catches the case where
//     Docker is still flushing pipe data after the function returned.
//   - logInFlight: counter of lines parsed by the scanner but not yet flushed
//     to CWL. Must be 0 before drain returns, regardless of reader state.
//
// The 2 s safety-net only matters when something is genuinely stuck (slow
// CWL writer, stalled Docker connection); the common case completes in
// 10–15 ms. The function returns immediately if the stream has never
// produced any output (logReadAt == 0 AND inFlight == 0).
func (ci *containerInstance) waitForLogDrain(ctx context.Context) {
	const (
		idleThreshold = 10 * time.Millisecond
		deadlineMax   = 2 * time.Second
		tickInterval  = 2 * time.Millisecond
	)
	deadline := ci.clk.Timer(deadlineMax)
	defer deadline.Stop()
	tick := ci.clk.Ticker(tickInterval)
	defer tick.Stop()
	for {
		select {
		case <-deadline.C:
			return
		case <-tick.C:
			// Phase A: parsed-but-unflushed work outstanding — keep waiting.
			if ci.logInFlight.Load() > 0 {
				continue
			}
			last := ci.logReadAt.Load()
			// Reader has never produced anything: nothing to drain unless
			// the function is still in cold start. We can't distinguish
			// these cases without an exit signal, so return — callers in
			// the invoke success path call drain after the runtime API
			// has already returned a result, so any output the function
			// produced before responding will have triggered at least one
			// read by then.
			if last == 0 {
				return
			}
			// Reader has been idle long enough that no further bytes are
			// expected from Docker, AND inFlight is 0 — fully drained.
			if ci.clk.Since(time.Unix(0, last)) >= idleThreshold {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// writeLogLine emits a single synthesised log line (START / END / REPORT)
// directly to CloudWatch Logs and updates the rolling tail buffer.
//
// We deliberately bypass the streamLogs goroutine for these lines: they are
// produced by the execution environment, not by the container, so they never
// arrive on the Docker log stream. Routing them through the streamLogs
// channel would also create a reliability hole — if streamLogs fails to open
// the Docker log connection (e.g. transient daemon error), the goroutine
// returns before entering its select loop and any synth lines queued there
// are silently lost. Writing directly guarantees delivery.
//
// Performance note: the CWL store now serves writes from an in-memory cache
// and debounces persistence, so this synchronous path costs O(microseconds)
// per call (cache lock + slice append + per-stream metadata update). Three
// calls per invocation (START, END, REPORT) is negligible.
func (ci *containerInstance) writeLogLine(ctx context.Context, line string) {
	// Append to the rolling tail buffer so these lines appear in the
	// X-Amz-Log-Result (test tab) alongside the function's own stdout.
	lineBytes := append([]byte(line), '\n')
	ci.tailMu.Lock()
	ci.tailBuf = append(ci.tailBuf, lineBytes...)
	const maxTail = 4096
	if n := len(ci.tailBuf); n > maxTail {
		copy(ci.tailBuf, ci.tailBuf[n-maxTail:])
		ci.tailBuf = ci.tailBuf[:maxTail]
	}
	ci.tailMu.Unlock()

	if ci.logWriter == nil {
		return
	}
	// Use the container's logCtx which carries the function-ARN-derived region.
	// This ensures START/END/REPORT lines land in the same regional log stream
	// as the function's own stdout/stderr, even when invoked cross-region.
	writeCtx := ci.logCtx
	if writeCtx.Err() != nil {
		writeCtx = middleware.ContextWithRegion(context.Background(), regionFromFunctionARN(ci.functionARN))
	}
	ci.writeEventsWithRetry(writeCtx, []events.LogEntry{
		{Timestamp: ci.clk.Now().UnixMilli(), Message: line},
	})
}

func (ci *containerInstance) writeEventsWithRetry(ctx context.Context, entries []events.LogEntry) bool {
	if ci.logWriter == nil || len(entries) == 0 {
		return true
	}
	writeCtx := ctx
	if writeCtx == nil || writeCtx.Err() != nil {
		writeCtx = middleware.ContextWithRegion(context.Background(), regionFromFunctionARN(ci.functionARN))
	}
	delays := []time.Duration{10 * time.Millisecond, 50 * time.Millisecond, 250 * time.Millisecond}
	var err error
	for attempt := 0; attempt < len(delays); attempt++ {
		if attempt > 0 {
			_ = ci.logWriter.EnsureLogStream(writeCtx, ci.logGroupName, ci.logStream)
		}
		err = ci.logWriter.WriteLogEvents(writeCtx, ci.logGroupName, ci.logStream, entries)
		if err == nil {
			return true
		}
		if attempt == len(delays)-1 {
			break
		}
		select {
		case <-ci.clk.After(delays[attempt]):
		case <-writeCtx.Done():
			writeCtx = middleware.ContextWithRegion(context.Background(), regionFromFunctionARN(ci.functionARN))
		}
	}
	ci.logger.Error("container logs: write events failed after retries",
		zap.String("container", shortContainerID(ci.id)),
		zap.Int("entries", len(entries)),
		zap.Error(err),
	)
	return false
}

// billedDuration rounds elapsed time up to the nearest millisecond, matching
// how AWS reports billed duration in REPORT lines.
func billedDuration(d time.Duration) int64 {
	ms := d.Milliseconds()
	if d > time.Duration(ms)*time.Millisecond {
		ms++
	}
	return ms
}

// Healthy reports whether this container is safe to reuse.
func (ci *containerInstance) Healthy() bool { return ci.healthy }

// Close drains logs, deregisters from the Runtime API, stops the container
// immediately (to halt any code running inside), and schedules deferred removal
// via the GC with exponential backoff.
func (ci *containerInstance) Close() error {
	ci.logger.Debug("stopping lambda container", zap.String("container", ci.id[:12]))
	if ci.logWriter != nil {
		ci.waitForLogDrain(context.Background())
	}
	ci.logCancel()
	<-ci.logDone
	if ci.containerIP != "" {
		ci.runtimeAPI.UnregisterContainer(ci.containerIP)
	}
	if ci.logWriter != nil {
		ci.reconcileLogs()
	}
	// Stop immediately + queue deferred removal via GC.
	if ci.gc != nil && !ci.keepContainers {
		ci.gc.StopAndScheduleRemove(ci.id)
	} else if ci.keepContainers {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return ci.docker.StopContainer(ctx, ci.id, 5)
	}
	return nil
}

// dockerLogStripper is an io.Reader that strips the 8-byte Docker multiplexed
// log frame headers, returning only the payload bytes. This lets a bufio.Scanner
// assemble complete log lines transparently across frame boundaries, which is
// required for any multi-byte log format — particularly JSON emitted by AWS
// Lambda Powertools Logger, where one log record is a single JSON object on one
// line that may exceed a single Docker frame payload.
//
// Docker log framing (non-TTY containers):
//
//	[1 byte stream-type][3 bytes padding][4 bytes big-endian payload size][payload]
type dockerLogStripper struct {
	r         io.Reader
	remaining uint32 // bytes remaining in current frame payload
}

func (s *dockerLogStripper) Read(p []byte) (int, error) {
	for s.remaining == 0 {
		// Read the 8-byte frame header. Returns io.EOF when the log stream ends.
		var hdr [8]byte
		if _, err := io.ReadFull(s.r, hdr[:]); err != nil {
			return 0, err
		}
		s.remaining = binary.BigEndian.Uint32(hdr[4:8])
		// Skip zero-length frames and loop back to read the next header.
	}
	limit := int(s.remaining)
	if len(p) < limit {
		limit = len(p)
	}
	n, err := s.r.Read(p[:limit])
	s.remaining -= uint32(n)
	return n, err
}

// logReadTracker wraps an io.Reader and records the time of the last successful
// read. Used by waitForLogDrain to detect when streamLogs has consumed all
// buffered Docker output (i.e. the reader has gone idle).
type logReadTracker struct {
	r      io.Reader
	readAt *atomic.Int64
	clk    clock.Clock
}

const (
	maxCloudWatchLogEventBytes = 262144 - 26
	maxDockerTimestampBytes    = 64
	maxDockerLogLineBytes      = maxCloudWatchLogEventBytes + maxDockerTimestampBytes
)

type logCursor struct {
	mu      sync.Mutex
	hwNanos int64
	hwCount int
}

type logCursorAdmission struct {
	cursor    *logCursor
	replay    bool
	equalSeen int
}

func (c *logCursor) Since() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hwNanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, c.hwNanos-1)
}

func (c *logCursor) NewAdmission(replay bool) *logCursorAdmission {
	return &logCursorAdmission{cursor: c, replay: replay}
}

func (a *logCursorAdmission) Admit(ts time.Time) bool {
	if ts.IsZero() {
		return true
	}
	nanos := ts.UnixNano()
	a.cursor.mu.Lock()
	defer a.cursor.mu.Unlock()
	switch {
	case nanos < a.cursor.hwNanos:
		return false
	case nanos > a.cursor.hwNanos:
		a.cursor.hwNanos = nanos
		a.cursor.hwCount = 1
		a.equalSeen = 0
		return true
	default:
		if a.replay {
			a.equalSeen++
			if a.equalSeen <= a.cursor.hwCount {
				return false
			}
		}
		a.cursor.hwCount++
		return true
	}
}

func readBoundedLogLine(r *bufio.Reader, maxBytes int) (string, error) {
	var out []byte
	truncated := false
	for {
		part, err := r.ReadSlice('\n')
		if len(part) > 0 && !truncated {
			remaining := maxBytes - len(out)
			if remaining > 0 {
				if len(part) > remaining {
					out = append(out, part[:remaining]...)
					truncated = true
				} else {
					out = append(out, part...)
				}
			} else {
				truncated = true
			}
		}
		if err == nil {
			return strings.TrimRight(string(out), "\r\n"), nil
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err == io.EOF && len(out) > 0 {
			return strings.TrimRight(string(out), "\r\n"), nil
		}
		return "", err
	}
}

func truncateLogMessage(msg string) string {
	if len(msg) <= maxCloudWatchLogEventBytes {
		return msg
	}
	return msg[:maxCloudWatchLogEventBytes]
}

func shortContainerID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func (t *logReadTracker) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 {
		t.readAt.Store(t.clk.Now().UnixNano())
	}
	return n, err
}

// streamLogs runs as a background goroutine for the lifetime of the container.
// It opens a follow=true streaming connection to Docker's log endpoint and
// forwards each complete stdout/stderr line to CloudWatch Logs. It also
// maintains a rolling tail buffer (≤4096 bytes) used to populate X-Amz-Log-Result.
//
// Resilience: if the Docker log stream fails mid-flight (daemon hiccup, DinD
// proxy timeout, etc.), the goroutine reconnects with timestamps=true and
// since=<last-seen-timestamp minus 1ns>. Duplicates from overlapping reconnect
// windows are deduplicated via an exact timestamp+count cursor. On graceful
// shutdown (Close), a non-streaming reconciliation pass fetches any remaining
// bytes from Docker's persisted log file.
//
// Line assembly uses a bounded reader on a dockerLogStripper so oversized log
// lines are truncated instead of stalling the stream forever.
//
// The goroutine exits when logCtx is cancelled (i.e. when Close is called).
func (ci *containerInstance) streamLogs() {
	defer close(ci.logDone)
	ctx := ci.logCtx

	// Ensure the CloudWatch log group and stream exist before writing.
	if err := ci.logWriter.EnsureLogStream(ctx, ci.logGroupName, ci.logStream); err != nil {
		if ctx.Err() == nil {
			ci.logger.Debug("container logs: ensure log stream failed",
				zap.String("container", ci.id[:12]),
				zap.String("group", ci.logGroupName),
				zap.Error(err),
			)
		}
		// Carry on — we still maintain tailBuf even without CWL delivery.
	}

	// Reconnection loop: keeps streaming until logCtx is cancelled.
	var since time.Time
	backoff := 50 * time.Millisecond
	const maxBackoff = 2 * time.Second

	for {
		if ctx.Err() != nil {
			return
		}
		ci.streamOnce(ctx, since)

		// streamOnce returned — either because the Docker stream broke or
		// because ctx was cancelled.
		if ctx.Err() != nil {
			return
		}

		// Update `since` from the high-watermark for the next reconnect.
		since = ci.logCursor.Since()

		// Exponential backoff on reconnect to avoid hammering Docker.
		ci.logger.Debug("container logs: reconnecting",
			zap.String("container", ci.id[:12]),
			zap.Duration("backoff", backoff),
		)
		select {
		case <-ci.clk.After(backoff):
		case <-ctx.Done():
			return
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// streamOnce opens one streaming connection and processes lines until the
// stream ends or ctx is cancelled. It returns on any error so the outer loop
// can reconnect.
func (ci *containerInstance) streamOnce(ctx context.Context, since time.Time) {
	stream, err := ci.docker.ContainerLogsStream(ctx, ci.id, since)
	if err != nil {
		if ctx.Err() == nil {
			ci.logger.Warn("container logs: open stream failed",
				zap.String("container", ci.id[:12]),
				zap.Error(err),
			)
		}
		return
	}
	defer stream.Close()

	// flush writes the batch durably to CloudWatch and decrements logInFlight
	// only after the bounded retry loop completes.
	flush := func(batch []events.LogEntry) {
		if len(batch) == 0 {
			return
		}
		writeCtx := ctx
		if writeCtx.Err() != nil {
			writeCtx = middleware.ContextWithRegion(context.Background(), regionFromFunctionARN(ci.functionARN))
		}
		ci.writeEventsWithRetry(writeCtx, batch)
		ci.logInFlight.Add(-int64(len(batch)))
	}

	// Wrap the multiplexed stream so the line reader sees a plain byte stream.
	stripped := &dockerLogStripper{r: stream}
	tracked := &logReadTracker{r: stripped, readAt: &ci.logReadAt, clk: ci.clk}
	reader := bufio.NewReaderSize(tracked, 64*1024)
	admission := ci.logCursor.NewAdmission(!since.IsZero())

	type scanResult struct {
		line string
		err  error
	}
	lines := make(chan scanResult, 64)
	go func() {
		defer close(lines)
		for {
			line, err := readBoundedLogLine(reader, maxDockerLogLineBytes)
			if err != nil {
				if err != io.EOF {
					lines <- scanResult{err: err}
				}
				return
			}
			ci.logInFlight.Add(1)
			lines <- scanResult{line: line}
		}
	}()

	const (
		batchMax      = 25
		flushInterval = 5 * time.Millisecond
	)

	var batch []events.LogEntry
	flushTimer := ci.clk.Timer(flushInterval)
	flushTimer.Stop()
	defer flushTimer.Stop()

	for {
		select {
		case res, ok := <-lines:
			if !ok {
				flush(batch)
				return
			}
			if res.err != nil {
				if ctx.Err() == nil {
					ci.logger.Debug("container log stream ended",
						zap.String("container", ci.id[:12]),
						zap.Error(res.err),
					)
				}
				flush(batch)
				return
			}
			line := res.line
			ts, msg := parseDockerTimestamp(line)
			if !admission.Admit(ts) {
				ci.logInFlight.Add(-1)
				continue
			}
			line = truncateLogMessage(msg)
			if line == "" {
				ci.logInFlight.Add(-1)
				continue
			}

			// Append to rolling tail buffer (capped at 4096 bytes).
			lineBytes := append([]byte(line), '\n')
			ci.tailMu.Lock()
			ci.tailBuf = append(ci.tailBuf, lineBytes...)
			const maxTail = 4096
			if n := len(ci.tailBuf); n > maxTail {
				copy(ci.tailBuf, ci.tailBuf[n-maxTail:])
				ci.tailBuf = ci.tailBuf[:maxTail]
			}
			ci.tailMu.Unlock()

			batch = append(batch, events.LogEntry{
				Timestamp: logEventTimestampMillis(ts, ci.clk.Now()),
				Message:   line,
			})
			if len(batch) >= batchMax {
				flush(batch)
				batch = batch[:0]
				flushTimer.Stop()
			} else {
				flushTimer.Reset(flushInterval)
			}

		case <-flushTimer.C:
			flush(batch)
			batch = batch[:0]

		case <-ctx.Done():
			// Drain until the reader goroutine finishes or the bounded teardown cap
			// fires. Cancelling ctx closes the Docker HTTP body; any bytes already
			// buffered by the reader are still delivered before lines closes.
			drainTimer := ci.clk.Timer(time.Second)
		drainLoop:
			for {
				select {
				case res, ok := <-lines:
					if !ok {
						break drainLoop
					}
					if res.err != nil {
						break drainLoop
					}
					ts, msg := parseDockerTimestamp(res.line)
					if !admission.Admit(ts) {
						ci.logInFlight.Add(-1)
						continue
					}
					msg = truncateLogMessage(msg)
					if msg == "" {
						ci.logInFlight.Add(-1)
						continue
					}
					lineBytes := append([]byte(msg), '\n')
					ci.tailMu.Lock()
					ci.tailBuf = append(ci.tailBuf, lineBytes...)
					const maxTailDrain = 4096
					if n := len(ci.tailBuf); n > maxTailDrain {
						copy(ci.tailBuf, ci.tailBuf[n-maxTailDrain:])
						ci.tailBuf = ci.tailBuf[:maxTailDrain]
					}
					ci.tailMu.Unlock()
					batch = append(batch, events.LogEntry{
						Timestamp: logEventTimestampMillis(ts, ci.clk.Now()),
						Message:   msg,
					})
				case <-drainTimer.C:
					break drainLoop
				}
			}
			drainTimer.Stop()
			flush(batch)
			return
		}
	}
}

// parseDockerTimestamp parses the RFC3339Nano timestamp prefix that Docker adds
// when timestamps=true. Returns (time, message-without-prefix). If parsing
// fails (line has no timestamp), returns (time.Time{}, original-line).
func parseDockerTimestamp(line string) (time.Time, string) {
	// Docker format: "2006-01-02T15:04:05.999999999Z message..." or
	//                "2006-01-02T15:04:05.999999999+07:00 message..."
	// Minimum timestamp length is len("2006-01-02T15:04:05Z") = 20.
	spaceIdx := strings.IndexByte(line, ' ')
	if spaceIdx < 20 || spaceIdx > 40 {
		return time.Time{}, line
	}
	t, err := time.Parse(time.RFC3339Nano, line[:spaceIdx])
	if err != nil {
		return time.Time{}, line
	}
	return t, line[spaceIdx+1:]
}

func logEventTimestampMillis(ts time.Time, fallback time.Time) int64 {
	if ts.IsZero() {
		return fallback.UnixMilli()
	}
	return ts.UnixMilli()
}

// reconcileLogs fetches all container logs since the high-watermark via a
// non-streaming request (Docker's persisted log file is complete once the
// container has stopped) and writes any lines that the streaming follower
// missed. This is the final safety net ensuring zero log loss.
func (ci *containerInstance) reconcileLogs() {
	since := ci.logCursor.Since()

	dockerCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	body, err := ci.docker.ContainerLogsSince(dockerCtx, ci.id, since)
	if err != nil {
		ci.logger.Debug("container logs: reconciliation fetch failed",
			zap.String("container", ci.id[:12]),
			zap.Error(err),
		)
		return
	}
	defer body.Close()

	stripped := &dockerLogStripper{r: body}
	reader := bufio.NewReaderSize(stripped, 64*1024)
	admission := ci.logCursor.NewAdmission(!since.IsZero())

	var batch []events.LogEntry
	for {
		line, readErr := readBoundedLogLine(reader, maxDockerLogLineBytes)
		if readErr != nil {
			if readErr != io.EOF {
				ci.logger.Debug("container logs: reconciliation read ended",
					zap.String("container", shortContainerID(ci.id)),
					zap.Error(readErr),
				)
			}
			break
		}
		ts, msg := parseDockerTimestamp(line)
		if !admission.Admit(ts) {
			continue
		}
		msg = truncateLogMessage(msg)
		if msg == "" {
			continue
		}
		batch = append(batch, events.LogEntry{
			Timestamp: logEventTimestampMillis(ts, ci.clk.Now()),
			Message:   msg,
		})
	}
	if len(batch) > 0 {
		// Use the container's region-scoped context so reconciliation logs
		// land in the same regional log stream as the streaming path.
		writeCtx := ci.logCtx
		if writeCtx.Err() != nil {
			writeCtx = middleware.ContextWithRegion(context.Background(), regionFromFunctionARN(ci.functionARN))
		}
		ci.writeEventsWithRetry(writeCtx, batch)
	}
}

// ─── Helpers ───────────────────────────────────────────────────────────────

// lambdaLogStreamName generates an AWS-style log stream name.
// Format: YYYY/MM/DD/[$LATEST]<26-char lowercase hex>.
func lambdaLogStreamName(clk clock.Clock) string {
	date := clk.Now().UTC().Format("2006/01/02")
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d", clk.Now().UnixNano())))
	return date + "/[$LATEST]" + hex.EncodeToString(hash[:13])
}

// cpuAllocation calculates the fractional CPU count for a Lambda function based
// on its memory setting. AWS allocates CPU proportional to memory:
// 1769 MB = 1 vCPU, linear scaling. This is the steady-state allocation used
// after the INIT phase completes.
func cpuAllocation(memoryMB int) float64 {
	cpus := float64(memoryMB) / 1769.0
	if cpus < 0.0625 {
		cpus = 0.0625 // minimum: 1/16 vCPU
	}
	return cpus
}

// initBurstCPUs is the CPU allocation given to Lambda containers during the
// INIT phase (runtime bootstrap, SDK loading, global-scope execution). Real
// AWS gives functions burst CPU during INIT regardless of memory configuration;
// we emulate this by starting containers with generous CPU and throttling down
// to the proportional allocation once the RIC issues its first GET /next.
const initBurstCPUs = 2.0

// initBurstEntry tracks a container awaiting INIT-burst throttle-down.
type initBurstEntry struct {
	containerID string
	memorySize  int // configured memory — used to compute steady-state CPU
}

// registerInitBurst records that a container was started with burst CPU and
// should be throttled to the proportional allocation after INIT completes.
func (cr *ContainerRuntime) registerInitBurst(functionARN, containerID string, memoryMB int) {
	// Skip registration if the function already gets burst-level CPU or more
	// from its memory allocation — throttling would be a no-op.
	if cpuAllocation(memoryMB) >= initBurstCPUs {
		return
	}
	cr.initBurstMu.Lock()
	defer cr.initBurstMu.Unlock()
	if cr.initBurst == nil {
		cr.initBurst = make(map[string]initBurstEntry)
	}
	cr.initBurst[functionARN] = initBurstEntry{
		containerID: containerID,
		memorySize:  memoryMB,
	}
}

// clearInitBurst removes a pending init-burst entry without throttling.
// Called on error paths when the container is being torn down.
func (cr *ContainerRuntime) clearInitBurst(functionARN string) {
	cr.initBurstMu.Lock()
	delete(cr.initBurst, functionARN)
	cr.initBurstMu.Unlock()
}

// ThrottleInitBurst reduces a container's CPU allocation from the INIT burst
// level to the steady-state proportional allocation. Called when the RIC issues
// its first GET /next, signalling that the INIT phase is complete. Safe to call
// for functions that don't have a pending burst entry (no-op).
func (cr *ContainerRuntime) ThrottleInitBurst(functionARN string) {
	cr.initBurstMu.Lock()
	entry, ok := cr.initBurst[functionARN]
	if ok {
		delete(cr.initBurst, functionARN)
	}
	cr.initBurstMu.Unlock()

	if !ok {
		return
	}

	steadyCPUs := cpuAllocation(entry.memorySize)
	steadyNano := int64(steadyCPUs * 1e9)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := cr.docker.UpdateContainerResources(ctx, entry.containerID, &docker.UpdateResourcesRequest{
		NanoCPUs: steadyNano,
	})
	if err != nil {
		cr.logger.Warn("failed to throttle INIT burst CPU — container keeps burst allocation",
			zap.String("container", entry.containerID[:12]),
			zap.Float64("burst_cpus", initBurstCPUs),
			zap.Float64("steady_cpus", steadyCPUs),
			zap.Error(err))
		return
	}
	cr.logger.Info("throttled INIT burst CPU to steady state",
		zap.String("container", entry.containerID[:12]),
		zap.Float64("steady_cpus", steadyCPUs))
}

// sanitizeName makes a function name safe for use in Docker container names.
func sanitizeName(name string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, name)
}

// copyLayersToContainer expands each attached layer zip and copies it into
// /opt, matching Lambda's layer filesystem semantics.
func (cr *ContainerRuntime) copyLayersToContainer(ctx context.Context, containerID string, fn *Function) error {
	if len(fn.Layers) == 0 {
		return nil
	}
	if cr.layerFetcher == nil {
		return fmt.Errorf("layer content fetcher is not configured")
	}
	for _, layer := range fn.Layers {
		arn := strings.TrimSpace(layer.ARN)
		if arn == "" {
			continue
		}
		zipData, err := cr.layerFetcher(ctx, arn)
		if err != nil {
			// Try remote fetch if enabled.
			if cr.remoteFetcher != nil {
				remoteData, remoteErr := cr.remoteFetcher.FetchLayer(ctx, arn)
				if remoteErr != nil {
					cr.logger.Warn("layer not available locally or remotely — skipping",
						zap.String("arn", arn), zap.Error(err), zap.NamedError("remote_error", remoteErr))
					continue
				}
				zipData = remoteData
			} else {
				cr.logger.Warn("layer not available locally — skipping",
					zap.String("arn", arn), zap.Error(err))
				continue
			}
		}
		layerTar, err := zipToTarFiltered(zipData, skipExtensions)
		if err != nil {
			return fmt.Errorf("build tar for layer %s: %w", arn, err)
		}
		if err := cr.docker.CopyToContainer(ctx, containerID, "/opt", bytes.NewReader(layerTar)); err != nil {
			return fmt.Errorf("copy layer %s to /opt: %w", arn, err)
		}
	}
	return nil
}

// zipToTar converts a zip archive (raw bytes) into a tar archive suitable for
// the Docker "Put Archive" API. This avoids extracting to a local temp
// directory, which breaks when running inside a devcontainer (the Docker daemon
// runs on the host and cannot see the devcontainer's filesystem).
func zipToTar(zipData []byte) ([]byte, error) {
	return zipToTarFiltered(zipData, nil)
}

// skipExtensions is a tar entry filter that strips the extensions/ directory.
// Lambda extensions are separate processes that hook into the Lambda lifecycle.
// They cannot function outside the full Lambda platform, so we strip them to
// prevent them from running (and hanging) inside the emulated container.
func skipExtensions(name string) bool {
	return strings.HasPrefix(name, "extensions/") || name == "extensions"
}

// zipToTarFiltered converts a zip archive to a tar archive, optionally
// skipping entries where skip(name) returns true.
func zipToTarFiltered(zipData []byte, skip func(string) bool) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	for _, f := range zr.File {
		if skip != nil && skip(f.Name) {
			continue
		}

		header := &tar.Header{
			Name:    f.Name,
			Size:    int64(f.UncompressedSize64),
			Mode:    0o644,
			ModTime: f.Modified,
		}

		if f.FileInfo().IsDir() {
			header.Typeflag = tar.TypeDir
			header.Mode = 0o755
			if err := tw.WriteHeader(header); err != nil {
				return nil, fmt.Errorf("tar header %q: %w", f.Name, err)
			}
			continue
		}

		header.Typeflag = tar.TypeReg
		if err := tw.WriteHeader(header); err != nil {
			return nil, fmt.Errorf("tar header %q: %w", f.Name, err)
		}

		src, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open %q in zip: %w", f.Name, err)
		}
		_, err = io.Copy(tw, src)
		src.Close()
		if err != nil {
			return nil, fmt.Errorf("write %q to tar: %w", f.Name, err)
		}
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("close tar: %w", err)
	}

	return buf.Bytes(), nil
}
