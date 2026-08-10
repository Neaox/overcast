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
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/containerendpoint"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/logging"
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
	endpoint         *containerendpoint.Mapper          // rewrites resource URLs and /etc/hosts for sibling containers
	pullOnce         sync.Map                           // image name → *sync.Once — ensures each image is pulled only once
	logWriter        events.LogWriter                   // nil until InitLogWriter is called
	bus              atomic.Pointer[events.Bus]         // nil until SetBus is called
	exitNotify       *exitNotifier                      // routes Docker watcher die events to per-container channels
	vpcResolver      atomic.Pointer[VPCNetworkResolver] // resolves subnet → VPC → Docker network
	efsResolver      atomic.Pointer[EFSVolumeResolver]  // resolves EFS access point → Docker volume
	layerFetcher     LayerContentFetcher                // resolves a layer ARN to zip bytes for /opt injection
	remoteFetcher    *RemoteLayerFetcher                // optional — fetches layers from real AWS
	codeFetcher      CodeFetcher                        // populates fn.CodeZip at cold start; nil in tests
	tarCache         *tarCache                          // pre-built code/layer tars; nil = disabled
	imageVerified    sync.Map                           // pull key → struct{}{}: image confirmed present, skip the per-acquire daemon check
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

// SetEFSResolver wires the EFS volume resolver for mounting file-system
// volumes declared in FileSystemConfigs.
func (cr *ContainerRuntime) SetEFSResolver(r EFSVolumeResolver) {
	cr.efsResolver.Store(&r)
}

// LayerContentFetcher returns layer zip bytes for a layer version ARN.
// The returned bytes should be an immutable copy owned by the caller.
type LayerContentFetcher func(ctx context.Context, layerVersionARN string) ([]byte, error)

// CodeFetcher populates fn.CodeZip with the stored deployment package. The
// package lives under its own store key (see lambdaStore.loadFunctionCode) so
// the invoke path never carries it; a cold start is the point where the bytes
// are actually needed.
type CodeFetcher func(ctx context.Context, fn *Function) error

// SetCodeFetcher wires deployment-package retrieval for container cold starts.
func (cr *ContainerRuntime) SetCodeFetcher(fetcher CodeFetcher) {
	cr.codeFetcher = fetcher
}

// TarCacheStats reports the artifact cache's entry count, resident bytes,
// and byte budget, for the lambda debug endpoint.
func (cr *ContainerRuntime) TarCacheStats() (entries int, bytes, maxBytes int64) {
	return cr.tarCache.stats()
}

// PrefillArtifacts builds and caches fn's cold-start artifacts (code tar and
// layer tars) ahead of the next cold start. Called from the settle debounce
// after deploys — never on the invoke path — so the first cold start of a
// new code version skips the package fetch and conversion the same way later
// ones do. Best-effort: on any miss the cold start simply builds the
// artifact itself, as before.
func (cr *ContainerRuntime) PrefillArtifacts(ctx context.Context, fn *Function) {
	if cr.tarCache == nil || fn == nil || fn.PackageType == "Image" {
		return
	}
	if hotPath, err := hotReloadBindPath(fn, cr.cfg.LambdaHotReload); err == nil && hotPath == "" && fn.CodeHash != "" {
		if _, ok := cr.tarCache.get("code:" + fn.CodeHash); !ok {
			if len(fn.CodeZip) == 0 && cr.codeFetcher != nil {
				_ = cr.codeFetcher(ctx, fn)
			}
			if len(fn.CodeZip) > 0 {
				if tarData, err := zipToTarPrefixed(fn.CodeZip, "var/task/"); err == nil {
					cr.tarCache.put(&tarCacheEntry{key: "code:" + fn.CodeHash, data: tarData})
				}
			}
		}
	}
	// resolveLayerTars fills the layer cache as a side effect; skip-warnings
	// here are advisory only — the cold start re-resolves and reports them.
	_, _, _, _ = cr.resolveLayerTars(ctx, fn)
}

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
// maxConcurrentStarts bounds concurrent container creation/INIT bursts; the
// caller resolves it from LAMBDA_DOCKER_MAX_CONCURRENT_STARTS or the Docker
// host (see resolveRuntimeLimits).
func NewContainerRuntime(
	cfg *config.Config,
	clk clock.Clock,
	docker *docker.Client,
	gc *docker.GC,
	runtimeAPI *RuntimeAPIServer,
	logger *zap.Logger,
	maxConcurrentStarts int,
) *ContainerRuntime {
	// Derive the Overcast emulator endpoint from the Runtime API address.
	// The Runtime API host is the IP that Lambda containers can route to on
	// the overcast_lambda Docker network — the same IP serves the main HTTP
	// API on cfg.Port. Setting AWS_ENDPOINT_URL lets function code call S3,
	// SQS, DynamoDB, etc. back into Overcast without any SDK configuration.
	runtimeHost, _, _ := net.SplitHostPort(runtimeAPI.Addr())
	// BaseURL rather than a hand-built string: the scheme must follow the
	// listener (https under OVERCAST_TLS), which only config knows.
	overcastEndpoint := containerendpoint.BaseURL(cfg, runtimeHost)

	limit := maxConcurrentStarts
	if limit < 1 {
		limit = 1
	}

	endpoint := containerendpoint.New(cfg, overcastEndpoint).WithPublishedPort(cfg.PublishedPort)
	return &ContainerRuntime{
		cfg:              cfg,
		clk:              clk,
		docker:           docker,
		gc:               gc,
		runtimeAPI:       runtimeAPI,
		logger:           logger,
		network:          cfg.LambdaNetwork,
		overcastEndpoint: overcastEndpoint,
		// The Runtime API address is already an address Lambda containers can
		// route to, so it is used directly rather than re-resolved. The mapper
		// applies the shared rewriting and /etc/hosts rules on top of it.
		endpoint:     endpoint,
		exitNotify:   newExitNotifier(),
		coldStartSem: make(chan struct{}, limit),
		tarCache:     newTarCache(int64(cfg.LambdaTarCacheMB) * 1024 * 1024),
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

// errColdStartBusy reports that no cold-start slot was immediately free.
// Only proactive acquisition returns it — real invocations wait instead.
var errColdStartBusy = errors.New("lambda cold-start capacity busy")

// tryAcquireColdStartSlot is acquireColdStartSlot without the wait: proactive
// environment creation must never hold a queue position that a real
// invocation could have taken.
func (cr *ContainerRuntime) tryAcquireColdStartSlot() (func(), error) {
	if cr.coldStartSem == nil {
		return func() {}, nil
	}
	select {
	case cr.coldStartSem <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() { <-cr.coldStartSem })
		}, nil
	default:
		return nil, errColdStartBusy
	}
}

// ProgressFunc is called by AcquireWithProgress to report lifecycle steps to
// the caller (e.g. an SSE endpoint streaming progress to the UI).
type ProgressFunc func(step string)

// CanHandle returns true for every runtime the catalog maps to an official
// Lambda base image — activeRuntimes is derived from lambdaRuntimeCatalog in
// runtime_catalog.go — and for PackageType=Image functions (runtimeID "image").
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
	platform := dockerPlatformForLambdaArchitectures(fn.Architectures)
	go func() {
		pullErr := cr.ensureImage(context.Background(), image, platform)
		if onReady != nil {
			onReady(pullErr)
		}
	}()
}

// Lambda's AWS_LAMBDA_INITIALIZATION_TYPE values. Observability libraries
// (Powertools among them) read this to classify cold starts.
const (
	initTypeOnDemand    = "on-demand"
	initTypeProvisioned = "provisioned-concurrency"
)

// Acquire creates and starts a Docker container for fn, then returns a
// containerInstance that can invoke the function via the Runtime API.
func (cr *ContainerRuntime) Acquire(ctx context.Context, fn *Function) (RuntimeInstance, error) {
	return cr.acquireContainer(ctx, fn, func(string) {}, initTypeOnDemand, false)
}

// AcquireProvisioned creates an environment for a provisioned concurrency
// reservation. Identical to Acquire except that the container reports
// AWS_LAMBDA_INITIALIZATION_TYPE=provisioned-concurrency, as it would on AWS.
func (cr *ContainerRuntime) AcquireProvisioned(ctx context.Context, fn *Function) (RuntimeInstance, error) {
	return cr.acquireContainer(ctx, fn, func(string) {}, initTypeProvisioned, false)
}

// AcquireProactive creates an environment ahead of any invocation, mirroring
// AWS's documented proactive initialization. Two deliberate differences from
// Acquire: cold-start capacity is try-acquired (errColdStartBusy instead of
// waiting — proactive work never queues ahead of real invocations), and the
// environment records no invoke-triggered init start, so its first REPORT
// line omits Init Duration — exactly what a proactively initialized
// environment looks like on AWS. AWS_LAMBDA_INITIALIZATION_TYPE stays
// "on-demand", also matching AWS.
func (cr *ContainerRuntime) AcquireProactive(ctx context.Context, fn *Function) (RuntimeInstance, error) {
	return cr.acquireContainer(ctx, fn, func(string) {}, initTypeOnDemand, true)
}

// Release is a no-op for ContainerRuntime itself — InstancePool wraps it and
// handles warm-instance storage and eviction.
func (cr *ContainerRuntime) Release(_ context.Context, _ RuntimeInstance, _ bool) {}

// AcquireWithProgress is like Acquire but calls progress at each lifecycle step
// so callers (e.g. the SSE invoke endpoint) can stream status to the UI.
func (cr *ContainerRuntime) AcquireWithProgress(ctx context.Context, fn *Function, progress ProgressFunc) (RuntimeInstance, error) {
	return cr.acquireContainer(ctx, fn, progress, initTypeOnDemand, false)
}

// acquireContainer is the single implementation behind Acquire and
// AcquireWithProgress. The progress callback is called at each lifecycle step;
// callers that don't need progress pass a no-op.
//
// Each phase's duration is captured (phases/mark) and reported on the
// "lambda container started" line, so where a cold start spends its time is
// always visible — the measurement Phase 0 of docs/plans/lambda-cold-start.md
// requires before any cold-path change.
func (cr *ContainerRuntime) acquireContainer(ctx context.Context, fn *Function, progress ProgressFunc, initType string, proactive bool) (RuntimeInstance, error) {
	isImage := fn.PackageType == "Image"

	acquireStart := cr.clk.Now()
	phaseStart := acquireStart
	var phases []zap.Field
	mark := func(name string) {
		now := cr.clk.Now()
		phases = append(phases, zap.Duration(name, now.Sub(phaseStart)))
		phaseStart = now
	}

	image, err := imageForFunction(fn)
	if err != nil {
		return nil, err
	}
	platform := dockerPlatformForLambdaArchitectures(fn.Architectures)

	// Ensure the image is present (lazy, once per image). Once verified, later
	// acquires skip this daemon round trip entirely; the create-time
	// missing-image retry below covers an image removed behind our back.
	pullKey := imagePullKey(image, platform)
	if _, verified := cr.imageVerified.Load(pullKey); !verified {
		exists, _ := cr.docker.ImageMatchesPlatform(ctx, image, platform)
		if !exists {
			progress("Pulling image " + image)
		}
		if err := cr.ensureImage(ctx, image, platform); err != nil {
			return nil, fmt.Errorf("pull image: %w", err)
		}
		if !exists {
			progress("Image ready")
		}
		cr.imageVerified.Store(pullKey, struct{}{})
	}
	mark("image_check")

	progress("Waiting for cold-start capacity")
	var releaseColdStart func()
	if proactive {
		releaseColdStart, err = cr.tryAcquireColdStartSlot()
	} else {
		releaseColdStart, err = cr.acquireColdStartSlot(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("wait for lambda cold-start slot: %w", err)
	}
	defer releaseColdStart()
	mark("slot_wait")

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
		cacheKey := ""
		if fn.CodeHash != "" {
			cacheKey = "code:" + fn.CodeHash
		}
		if cached, ok := cr.tarCache.get(cacheKey); ok {
			// A hit skips both materializing the package from the store and
			// the zip→tar conversion.
			codeTar = cached.data
		} else {
			// The function record travels without its package; materialize the
			// stored bytes now — the one place on the invoke path that needs them.
			if len(fn.CodeZip) == 0 && cr.codeFetcher != nil {
				if err := cr.codeFetcher(ctx, fn); err != nil {
					return nil, fmt.Errorf("fetch function code: %w", err)
				}
			}
			codeTar, err = zipToTarPrefixed(fn.CodeZip, "var/task/")
			if err != nil {
				return nil, fmt.Errorf("build code tar: %w", explainUnreadablePackage(fn, err))
			}
			// Re-derive the key: materializing a generation that has since been
			// superseded moves the snapshot onto the current one (see
			// lambdaStore.loadFunctionCode), and the entry must name the bytes
			// that were actually converted.
			if fn.CodeHash != "" {
				cacheKey = "code:" + fn.CodeHash
			}
			if cacheKey != "" {
				cr.tarCache.put(&tarCacheEntry{key: cacheKey, data: codeTar})
			}
		}
	}

	// Assemble the whole provisioning archive — code (var/task/…), TLS trust
	// root, layer contents (opt/…, in layer order, so later layers overwrite
	// earlier ones exactly as with the previous per-layer copies and as on
	// AWS), and the bootstrap — BEFORE the container exists: it is pure CPU
	// plus store reads, an error here wastes nothing, and it keeps the
	// create→start critical section as short as possible. Directories the
	// base image owns (/var, /var/task, /opt, /etc) are never emitted as
	// headers, so extraction cannot reset their modes.
	layerTars, expectedExtensions, skippedLayerLogLines, layerErr := cr.resolveLayerTars(ctx, fn)
	if layerErr != nil {
		return nil, fmt.Errorf("prepare layers: %w", layerErr)
	}
	var prov bytes.Buffer
	tw := tar.NewWriter(&prov)
	hasEntries := false
	appendComponent := func(what string, component []byte) error {
		if len(component) == 0 {
			return nil
		}
		if err := appendTarEntries(tw, component); err != nil {
			return fmt.Errorf("assemble %s entries: %w", what, err)
		}
		hasEntries = true
		return nil
	}
	if !isImage && hotReloadPath == "" {
		if err := appendComponent("code", codeTar); err != nil {
			return nil, err
		}
	}
	// With TLS on, the function must trust the CA that minted Overcast's
	// certificate before its first SDK call — otherwise every AWS call from
	// inside the function fails certificate verification. Injected by the same
	// mechanism as code: a dockerized Overcast has no host path to bind-mount.
	// The Mapper caches the trust-root tar after the first build.
	if caTar, caErr := cr.endpoint.CABundleTar(); caErr != nil {
		cr.logger.Warn("CA bundle unavailable; function TLS calls to Overcast will fail verification", zap.Error(caErr))
	} else if err := appendComponent("CA bundle", caTar); err != nil {
		return nil, err
	}
	for _, layerTar := range layerTars {
		if err := appendComponent("layer", layerTar); err != nil {
			return nil, err
		}
	}
	if !isImage {
		bootTar, bootErr := lambdaBootstrapTar()
		if bootErr != nil {
			return nil, fmt.Errorf("build bootstrap tar: %w", bootErr)
		}
		if err := appendComponent("bootstrap", bootTar); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("close provisioning tar: %w", err)
	}
	mark("code_prep")

	// This execution environment's own Runtime API endpoint, allocated before
	// the container exists because its address is baked into the environment at
	// create time. The listener a request arrives on is what identifies the
	// container to the Runtime API — see containerListener for why its source
	// address is not enough. Handed to the containerInstance on success and
	// closed on every other path out of here.
	rapiListener, rapiErr := cr.runtimeAPI.AddContainerListener()
	if rapiErr != nil {
		return nil, fmt.Errorf("allocate runtime api endpoint: %w", rapiErr)
	}
	listenerHandedOff := false
	defer func() {
		if !listenerHandedOff {
			_ = rapiListener.Close()
		}
	}()

	logStream := lambdaLogStreamName(cr.clk)
	env := cr.buildEnv(fn, logStream, initType, rapiListener.Addr())
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
		ccfg.Entrypoint = []string{"/bin/sh", "/var/overcast/bootstrap"}
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
		Platform:        platform,
		HostConfig: &docker.HostConfig{
			Binds:       bindMountTaskDir(hotReloadPath),
			Mounts:      cr.efsMounts(ctx, fn),
			NetworkMode: cr.network,
			Memory:      int64(fn.MemorySize) * 1024 * 1024,
			MemorySwap:  int64(fn.MemorySize) * 1024 * 1024, // disable swap
			NanoCPUs:    int64(initBurstCPUs * 1e9),
			// Split-horizon hostnames resolve to 127.0.0.1 in public DNS for
			// host-side callers; inside the container they must point at
			// Overcast instead. See internal/containerendpoint.
			//
			// ExtraHosts covers the apex names exactly; Dns covers their
			// subdomains — virtual-hosted S3, execute-api — which /etc/hosts
			// cannot express. Dns is empty unless the resolver is listening.
			ExtraHosts: cr.endpoint.ExtraHosts(),
			Dns:        cr.endpoint.DNSServers(),
		},
	}

	id, err := cr.docker.CreateContainer(ctx, containerName, req)
	if err != nil && docker.IsImageMissingErr(err) {
		// The image vanished after verification (docker rmi / prune). Drop
		// both caches — pullOnce too, or ensureImage's spent sync.Once would
		// skip the pull and this function could never run again without a
		// restart — then re-pull and retry the create once.
		cr.imageVerified.Delete(pullKey)
		cr.pullOnce.Delete(pullKey)
		cr.logger.Info("lambda image missing at container create — re-pulling",
			zap.String("image", image), zap.String("function", fn.Name))
		if pullErr := cr.ensureImage(ctx, image, platform); pullErr == nil {
			cr.imageVerified.Store(pullKey, struct{}{})
			id, err = cr.docker.CreateContainer(ctx, containerName, req)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("create container: %w", decorateHotReloadMountError(err, hotReloadPath))
	}

	var containerIP string
	var registeredIP string
	if inspect, inspectErr := cr.docker.InspectContainer(ctx, id); inspectErr == nil {
		containerIP = cr.extractContainerIP(inspect)
		if containerIP != "" {
			cr.runtimeAPI.RegisterContainerConfig(containerIP, runtimeContainerConfig{
				FunctionARN:        fn.ARN,
				FunctionName:       fn.Name,
				Handler:            fn.Handler,
				ExpectedExtensions: expectedExtensions,
			})
			rapiListener.Attach(containerIP)
			registeredIP = containerIP
		}
	} else {
		cr.logger.Debug("inspect lambda container before start", zap.String("container", id[:12]), zap.Error(inspectErr))
	}
	mark("create")

	// cleanup removes the container on any error after creation.
	cleanup := func() {
		if registeredIP != "" {
			cr.runtimeAPI.UnregisterContainer(registeredIP)
		}
		_ = cr.docker.RemoveContainerForce(id)
	}

	// One daemon round trip provisions the whole filesystem (assembled above,
	// before the container existed).
	progress("Provisioning container filesystem")
	if hasEntries {
		if err := cr.docker.CopyToContainer(ctx, id, "/", bytes.NewReader(prov.Bytes())); err != nil {
			cleanup()
			return nil, fmt.Errorf("provision container filesystem: %w", err)
		}
	}
	mark("copy")

	progress("Starting container")
	if err := cr.docker.StartContainer(ctx, id); err != nil {
		cleanup()
		return nil, fmt.Errorf("start container: %w", decorateHotReloadMountError(err, hotReloadPath))
	}
	// Init Duration is measured from here to the RIC's first GET /next. Only
	// environments whose init was triggered by an invoke report it, exactly as
	// on AWS: provisioned environments never do, and neither do proactively
	// initialized ones — their init ran ahead of any invocation, so the first
	// REPORT line carries no Init Duration even though the environment still
	// reports AWS_LAMBDA_INITIALIZATION_TYPE=on-demand.
	var initStartedAt time.Time
	if initType == initTypeOnDemand && !proactive {
		initStartedAt = cr.clk.Now()
	}

	// Connect to VPC Docker network if the function has a VpcConfig.
	if err := cr.connectVPCNetworks(ctx, id, fn); err != nil {
		cleanup()
		return nil, err
	}

	// Register for INIT-burst throttle-down when the RIC first polls /next.
	cr.registerInitBurst(fn.ARN, id, fn.MemorySize)
	mark("start")

	progress("Waiting for runtime to initialize")
	if containerIP == "" {
		containerIP, err = cr.awaitContainerIP(ctx, id)
		if err != nil {
			cr.clearInitBurst(fn.ARN)
			cleanup()
			return nil, err
		}
		cr.runtimeAPI.RegisterContainerConfig(containerIP, runtimeContainerConfig{
			FunctionARN:        fn.ARN,
			FunctionName:       fn.Name,
			Handler:            fn.Handler,
			ExpectedExtensions: expectedExtensions,
		})
		rapiListener.Attach(containerIP)
		registeredIP = containerIP
	}

	mark("await_ip")
	// One line per cold start carrying the phase breakdown; INIT time (runtime
	// bootstrap to first GET /next) is reported separately as the REPORT line's
	// Init Duration.
	cr.logger.Info("lambda container started",
		append([]zap.Field{
			zap.String("function", fn.Name),
			zap.String("container", id[:12]),
			zap.String("image", image),
			zap.String("container_ip", containerIP),
			zap.String("runtime_api", rapiListener.Addr()),
			zap.String("log_stream", logStream),
			zap.Duration("acquire_total", cr.clk.Now().Sub(acquireStart)),
		}, phases...)...,
	)

	ci := cr.newContainerInstance(id, containerIP, fn, logStream, rapiListener)
	listenerHandedOff = true
	ci.initStartedAt = initStartedAt
	for _, line := range skippedLayerLogLines {
		ci.writeLogLine(ctx, line)
	}
	return ci, nil
}

func dockerPlatformForLambdaArchitectures(architectures []string) string {
	if len(architectures) == 0 {
		return "linux/amd64"
	}
	switch architectures[0] {
	case "arm64":
		return "linux/arm64"
	case "x86_64":
		return "linux/amd64"
	default:
		return ""
	}
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
func (cr *ContainerRuntime) newContainerInstance(id, containerIP string, fn *Function, logStream string, rapiListener *containerListener) *containerInstance {
	logRegion := regionFromFunctionARN(fn.ARN)
	if logRegion == "" {
		logRegion = cr.cfg.Region
	}
	logCtx, logCancel := context.WithCancel(middleware.ContextWithRegion(context.Background(), logRegion))

	appLogLevel, sysLogLevel := resolveLogLevels(fn)
	ci := &containerInstance{
		id:             id,
		instanceID:     uuid.NewString(),
		containerIP:    containerIP,
		functionName:   fn.Name,
		functionARN:    fn.ARN,
		configIdentity: functionInstanceIdentity(fn),
		memorySize:     fn.MemorySize,
		logStream:      logStream,
		logGroupName:   fn.logGroupName(),
		logFormat:      resolveLogFormat(fn),
		appLogLevel:    appLogLevel,
		sysLogLevel:    sysLogLevel,
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
		endpoint:       cr.endpoint,
		rapiListener:   rapiListener,
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

// efsMounts resolves the function's FileSystemConfigs to Docker volume
// mounts, scoped to the access point's root directory via a volume Subpath
// when one is declared. Unresolvable configs — EFS mock mode, Docker
// unavailable, unknown access point — are skipped with a warning so the
// function still runs, just without shared storage (mirrors the best-effort
// posture of the EFS live mode itself).
func (cr *ContainerRuntime) efsMounts(ctx context.Context, fn *Function) []docker.Mount {
	if len(fn.FileSystemConfigs) == 0 {
		return nil
	}
	rp := cr.efsResolver.Load()
	if rp == nil {
		return nil
	}
	mounts := make([]docker.Mount, 0, len(fn.FileSystemConfigs))
	for _, fsc := range fn.FileSystemConfigs {
		volume, subpath, ok := (*rp).EFSVolumeForAccessPoint(ctx, fsc.Arn)
		if !ok {
			cr.logger.Warn("lambda: EFS mount skipped — access point has no backing volume (mock mode, Docker down, or unknown access point)",
				zap.String("function", fn.Name),
				zap.String("access_point", fsc.Arn),
				zap.String("mount_path", fsc.LocalMountPath))
			continue
		}
		mounts = append(mounts, volumeMount(volume, subpath, fsc.LocalMountPath, false))
	}
	if len(mounts) == 0 {
		return nil
	}
	return mounts
}

// volumeMount builds a named-volume Mount, attaching a Subpath option only
// when the mount is scoped below the volume root.
func volumeMount(volume, subpath, target string, readOnly bool) docker.Mount {
	m := docker.Mount{Type: "volume", Source: volume, Target: target, ReadOnly: readOnly}
	if subpath != "" {
		m.VolumeOptions = &docker.MountVolumeOptions{Subpath: subpath}
	}
	return m
}

// ─── Image management ──────────────────────────────────────────────────────

// SeedImages pre-pulls Docker images in parallel so the first cold start of a
// runtime skips the image pull entirely. The seed runs in a background goroutine
// with a detached context — it does not block startup or callers. Call after
// the ContainerRuntime is fully wired (i.e. after initDockerRuntime).
//
// Pre-pulling at startup is the single biggest lever for cold-start latency:
// the base images are 200–500 MB and pulling them on the first Invoke path can
// take minutes. By the time the user creates a function and invokes it the
// images are already cached locally.
//
// Only runtimes AWS still supports are seeded. Overcast can execute every
// runtime AWS still lets you deploy, including ones already past their
// deprecation date, but pre-pulling all of them would cost several extra
// gigabytes for images almost nobody deploys. A deprecated runtime is still
// pulled on demand at its first cold start.
func (cr *ContainerRuntime) SeedImages() {
	now := cr.clk.Now()
	// Deduplicate: distinct runtimes may share one underlying image.
	uniq := make(map[string]struct{}, len(activeRuntimes))
	for runtimeID, image := range activeRuntimes {
		if runtimeDeprecated(runtimeID, now) {
			continue
		}
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
				err := cr.ensureImage(context.Background(), image, dockerPlatformForLambdaArchitectures(nil))
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

// imagePullKey identifies an image+platform pair in the pullOnce and
// imageVerified maps — the same key must be used to seed and to invalidate.
func imagePullKey(image, platform string) string {
	if platform == "" {
		return image
	}
	return image + "@" + platform
}

func (cr *ContainerRuntime) ensureImage(ctx context.Context, image, platform string) error {
	// Check if already pulled.
	exists, err := cr.docker.ImageMatchesPlatform(ctx, image, platform)
	if err == nil && exists {
		return nil
	}

	// Use sync.Once per image+platform to avoid concurrent pulls while still
	// allowing arm64 and x86_64 variants of the same Lambda tag to be requested.
	pullKey := imagePullKey(image, platform)
	once, _ := cr.pullOnce.LoadOrStore(pullKey, &sync.Once{})
	var pullErr error
	once.(*sync.Once).Do(func() {
		cr.logger.Info("pulling Lambda image (first use)", zap.String("image", image), zap.String("platform", platform))
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
		pullErr = cr.docker.PullImageForPlatform(ctx, image, platform)
		if pullErr != nil {
			// Reset so we retry on next call.
			cr.pullOnce.Delete(pullKey)
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

// runtimeAPIAddr is this execution environment's own Runtime API endpoint, not
// the shared one: the port it dials is what identifies it to the Runtime API.
func (cr *ContainerRuntime) buildEnv(fn *Function, logStream, initType, runtimeAPIAddr string) []string {
	// AWS_REGION must reflect the function's actual region (encoded in its
	// ARN), not the emulator's global default. SDKs sign requests with this
	// region; if we used the default (e.g. us-east-1) for a function deployed
	// to ap-southeast-2, downstream service calls (DynamoDB, S3, etc.) would
	// be routed to the wrong region's data and resources would appear missing.
	region := regionFromFunctionARN(fn.ARN)
	if region == "" {
		region = cr.cfg.Region
	}
	env := make(map[string]string, len(fn.Environment)+32)
	// User environment values are rewritten on the way in: a deploy performed
	// from the host bakes host-side URLs (a CDK stack passing queue.queueUrl)
	// into function env, and AWS SDKs resolve endpoints from those URLs rather
	// than from AWS_ENDPOINT_URL. See internal/containerendpoint.
	for k, v := range fn.Environment {
		env[k] = cr.endpoint.RewriteURLs(v)
	}

	// The endpoint containers are told to use. Falls back to the address form
	// when no split-horizon name can be pinned for this container.
	clientEndpoint := cr.endpoint.ClientEndpoint()
	if clientEndpoint == "" {
		clientEndpoint = cr.overcastEndpoint
	}

	// Runtime-provided variables are applied after user variables. This matches
	// Lambda's reserved-env behavior and guarantees extensions inherit the local
	// endpoint and credential values even if a deployment template contains empty
	// AWS_* placeholders.
	runtimeEnv := map[string]string{
		"AWS_LAMBDA_FUNCTION_NAME":        fn.Name,
		"AWS_LAMBDA_FUNCTION_VERSION":     "$LATEST",
		"AWS_LAMBDA_FUNCTION_MEMORY_SIZE": fmt.Sprint(fn.MemorySize),
		"AWS_LAMBDA_LOG_GROUP_NAME":       fn.logGroupName(),
		"AWS_LAMBDA_LOG_STREAM_NAME":      logStream,
		// Lambda's advanced logging controls are delivered to the runtime as
		// environment variables — the managed runtimes read them to decide
		// whether to structure their own output, and AWS documents them as the
		// integration point for custom runtimes.
		"AWS_LAMBDA_LOG_FORMAT": resolveLogFormat(fn),
		// Real Lambda always sets this; Powertools and other observability
		// libraries use it for cold-start classification.
		"AWS_LAMBDA_INITIALIZATION_TYPE": initType,
		"AWS_REGION":                     region,
		"AWS_DEFAULT_REGION":             region,
		"AWS_ACCOUNT_ID":                 cr.cfg.AccountID,
		"AWS_ACCESS_KEY_ID":              "overcast",
		"AWS_SECRET_ACCESS_KEY":          "overcast",
		// Real Lambda always sets a session token from execution-role
		// assumption. Some SDK credential providers (notably the JS v3
		// fromEnv chain) treat the absence of this variable as "not a
		// Lambda environment" and fall through to other providers, so we
		// always provide a placeholder.
		"AWS_SESSION_TOKEN":           "overcast",
		"AWS_LAMBDA_RUNTIME_API":      runtimeAPIAddr,
		"LAMBDA_TASK_ROOT":            "/var/task",
		"AWS_LAMBDA_FUNCTION_TIMEOUT": fmt.Sprint(fn.Timeout),
		"TZ":                          ":/etc/localtime",
		// Route all AWS SDK calls from the function back to the Overcast emulator.
		// AWS SDKs v2+ honour AWS_ENDPOINT_URL for every service automatically.
		//
		// A name rather than an address: it survives Overcast being recreated on
		// a different one, and it lets an SDK build the virtual-hosted URLs it
		// derives from the endpoint ({bucket}.s3.{host} and friends), which an IP
		// endpoint forces to path-style. See containerendpoint.ClientEndpoint.
		"AWS_ENDPOINT_URL": clientEndpoint,
		// Per AWS SDK reference for service-specific endpoints:
		// SSM and Secrets Manager use these variables, overriding the global endpoint.
		// Verified with AWS Parameters and Secrets Lambda Extension 1.0.342
		// (2026-07-14): SSM and Secrets Manager requests honor these vars.
		"AWS_ENDPOINT_URL_SSM":             clientEndpoint,
		"AWS_ENDPOINT_URL_SECRETS_MANAGER": clientEndpoint,
	}
	// With TLS on, point every SDK TLS stack at the injected CA bundle
	// (AWS_CA_BUNDLE for botocore/Go/CLI, NODE_EXTRA_CA_CERTS for the Node
	// runtimes, SSL_CERT_FILE/REQUESTS_CA_BUNDLE for OpenSSL and requests).
	for k, v := range cr.endpoint.CABundleEnv() {
		runtimeEnv[k] = v
	}
	for k, v := range runtimeEnv {
		env[k] = v
	}
	// _HANDLER and AWS_EXECUTION_ENV are set only for zip deployments where
	// Runtime and Handler are specified. Image functions use the image's
	// ENTRYPOINT and do not have a Runtime string.
	if fn.Handler != "" {
		env["_HANDLER"] = fn.Handler
	}
	if fn.Runtime != "" && fn.Runtime != "image" {
		env["AWS_EXECUTION_ENV"] = "AWS_Lambda_" + fn.Runtime
	}
	// Only JSON functions have an application log level to hand down; in Text
	// mode AWS does not set the variable at all, and setting it would tell a
	// runtime to filter output that Lambda has not asked it to filter.
	if resolveLogFormat(fn) == logFormatJSON {
		applicationLevel, _ := resolveLogLevels(fn)
		env["AWS_LAMBDA_LOG_LEVEL"] = applicationLevel.String()
	}

	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

// ─── containerInstance ─────────────────────────────────────────────────────

// containerInstance implements RuntimeInstance for a running Docker container.
type containerInstance struct {
	id             string
	containerIP    string // IP on the Lambda Docker network
	functionName   string
	functionARN    string
	instanceID     string // stable execution-environment ID, survives warm reuse
	configIdentity string // fingerprint of the code + config baked in at creation time
	memorySize     int    // configured memory in MB
	logStream      string
	logGroupName   string
	// Logging configuration, resolved from the function at creation time. The
	// zero value (empty logFormat) is Text, which is what every containerInstance
	// built outside newContainerInstance wants. See logging_json.go.
	logFormat      string
	appLogLevel    logLevel
	sysLogLevel    logLevel
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
	tailAppendAt   atomic.Int64  // UnixNano of last container line appended to tailBuf; 0 until the first
	logReadAt      atomic.Int64  // UnixNano of last Docker log read; 0 until first read
	logInFlight    atomic.Int64  // lines parsed by scanner but not yet flushed to CWL
	peakMemMB      atomic.Int64  // running peak memory (MB) — what REPORT's Max Memory Used reads
	logCursor      logCursor     // exact Docker timestamp cursor used for reconnect/reconcile deduplication
	logDone        chan struct{} // closed when streamLogs goroutine exits
	readyCh        <-chan struct{}
	endpoint       *containerendpoint.Mapper // re-points Overcast URLs inside invoke payloads
	rapiListener   *containerListener        // this environment's own Runtime API endpoint
	healthy        bool
	keepContainers bool // when true, Close only stops the container instead of removing it

	// tailUnclaimed records that a tail wait gave up before its invocation's
	// output arrived, so whatever lands next belongs to an invocation that has
	// already answered. Guarded by tailMu, along with the boundary that wait was
	// working to. See discardUnclaimedOutput.
	tailUnclaimed     bool
	tailUnclaimedMark tailMark

	// initStartedAt is when the container was started, for environments whose
	// init was triggered by an invoke (on-demand cold starts). Zero for
	// proactively initialised environments (provisioned concurrency), which —
	// like real Lambda — never report an Init Duration.
	initStartedAt time.Time
	initReported  bool // true once Init Duration has been written to a REPORT line
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
		// Only the budget running out is a fault worth explaining. The caller
		// giving up — an abandoned invoke, a shutdown — cancels this same
		// context and has nothing to diagnose.
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			ci.logInitTimeout()
		}
		return ctx.Err()
	}
}

const (
	// initTimeoutLogTail is how many lines of the container's output the
	// INIT-timeout diagnostic quotes.
	initTimeoutLogTail = "50"
	// initTimeoutLogTimeout bounds the wait for the daemon to hand them over.
	// The invocation has already failed by this point, so the diagnostic must
	// not be what keeps the caller waiting.
	initTimeoutLogTimeout = 3 * time.Second
)

// logInitTimeout explains an execution environment that never finished INIT.
//
// What the caller gets is "lambda runtime did not initialize within 10s", which
// names neither the container nor anything that separates the causes. #800 is
// what that costs: an entire investigation went into proving the Runtime API
// was reachable on the address in the startup log — the shared one — when the
// container had been handed a per-environment port that appears nowhere else,
// and nothing in the log said whether anything had ever connected to it.
//
// So this answers, for this environment specifically: the address it was told
// to dial, whether anything ever arrived there, and what it printed.
//
// Connections narrow it honestly rather than conclusively. The RIC does not
// dial until it has imported the handler, so none means either the import is
// still running or the container cannot route back to this host — two very
// different jobs, but both are now a named pair to choose between instead of
// nothing at all. Some, with no invocation served, rules the route out: what is
// left is identity, and the 403 sits next to this line.
func (ci *containerInstance) logInitTimeout() {
	if ci.logger == nil {
		return
	}
	connections := ci.rapiListener.Accepted()
	msg := "lambda INIT timed out — the container reached its Runtime API endpoint but never polled for work"
	if connections == 0 {
		msg = "lambda INIT timed out — the container never reached its Runtime API endpoint; it is either still initialising or cannot route back to this host"
	}

	fields := []zap.Field{
		zap.String("function", ci.functionName),
		zap.String("container_ip", ci.containerIP),
		zap.String("runtime_api", ci.rapiListener.Addr()),
		zap.Int64("runtime_api_connections", connections),
	}
	if ci.id != "" {
		fields = append(fields, zap.String("container", shortContainerID(ci.id)))
	}
	output, err := ci.initOutput()
	switch {
	case err != nil:
		fields = append(fields, zap.NamedError("container_output_error", err))
	case output == "":
		// Worth stating rather than omitting: an empty log is the normal state
		// for a healthy AWS base image, whose RIC prints nothing until it runs
		// the handler. Reading it as suppressed output is a dead end, and one
		// #800 went down.
		fields = append(fields, zap.String("container_output", "(the container printed nothing)"))
	default:
		fields = append(fields, zap.String("container_output", output))
	}
	ci.logger.Warn(msg, fields...)
}

// initOutput is a bounded tail of what the container printed. Fetched only on
// the failure path, and on a context of its own — the one that expired is
// already cancelled.
func (ci *containerInstance) initOutput() (string, error) {
	if ci.docker == nil || ci.id == "" {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), initTimeoutLogTimeout)
	defer cancel()
	raw, err := ci.docker.ContainerLogs(ctx, ci.id, initTimeoutLogTail)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(docker.DemuxStream(raw))), nil
}

// LogStreamName returns the AWS-style log stream assigned to this container.
func (ci *containerInstance) LogStreamName() string { return ci.logStream }

// FunctionName returns the name of the Lambda function this container runs.
func (ci *containerInstance) FunctionName() string { return ci.functionName }

// ConfigIdentity returns the fingerprint of the code and configuration this
// container was created with.
func (ci *containerInstance) ConfigIdentity() string { return ci.configIdentity }

// ContainerID returns the Docker container ID backing this instance.
func (ci *containerInstance) ContainerID() string { return ci.id }

// InstanceID returns the stable execution-environment ID for this container.
func (ci *containerInstance) InstanceID() string { return ci.instanceID }

// invokeTimeoutError reports that a function ran past its configured timeout.
// It carries what real Lambda puts in the timeout response so the invoke
// handlers can shape the payload the way AWS does rather than leaking a Go
// error string into the caller's response.
type invokeTimeoutError struct {
	RequestID string
	Timeout   time.Duration
	At        time.Time
}

func (e *invokeTimeoutError) Error() string {
	return fmt.Sprintf("Task timed out after %.2f seconds", e.Timeout.Seconds())
}

// Payload is the response body real Lambda returns for a timed-out
// invocation: a lone errorMessage of the form
// "<UTC timestamp> <request id> Task timed out after N.NN seconds", with the
// error type carried by X-Amz-Function-Error rather than the body.
func (e *invokeTimeoutError) Payload() []byte {
	out, _ := json.Marshal(map[string]string{
		"errorMessage": fmt.Sprintf("%s %s Task timed out after %.2f seconds",
			e.At.UTC().Format("2006-01-02T15:04:05.000Z"), e.RequestID, e.Timeout.Seconds()),
	})
	return out
}

// Invoke sends the event to the container via the Runtime API and waits for
// the result. The container's RIC picks up the event from GET /next, runs the
// handler, and POSTs the result back to /response or /error.
//
// The caller is responsible for bounding ctx with the function's configured
// Timeout; this method trusts ctx.Deadline() to derive the Lambda-Runtime-Deadline-Ms
// header, which is what the RIC uses to implement context.getRemainingTimeInMillis().
func (ci *containerInstance) Invoke(ctx context.Context, event []byte, opts InvokeOptions) (*InvokeResult, error) {
	// A payload can carry Overcast URLs a host-side caller minted — a queue URL
	// passed to Invoke is the usual one. AWS SDKs resolve the service endpoint
	// from such a URL rather than from AWS_ENDPOINT_URL, so an origin this
	// container cannot dial would send its client to its own loopback. Function
	// environment is rewritten for exactly this reason; a payload is the same
	// value arriving by another route.
	event = ci.endpoint.RewriteBytes(event)

	deadline, ok := ctx.Deadline()
	if !ok {
		// Fallback: should not happen in normal operation because invokeSync
		// always applies a timeout, but kept as a safety net.
		deadline = ci.clk.Now().Add(900 * time.Second)
	}

	// Settle the previous invocation's straggling output before this one's tail
	// opens, under the same gate as the wait itself: only a caller that will
	// read LogResult pays for it.
	if opts.LogTail && ci.logWriter != nil {
		ci.discardUnclaimedOutput()
	}
	logMark := ci.beginTail()
	if reason, ok := ci.runtimeAPI.ContainerError(ci.containerIP); ok {
		ci.healthy = false
		return extensionInvokeError(reason), nil
	}

	start := ci.clk.Now()
	reqID, resultCh := ci.runtimeAPI.SubmitInvocation(ci.functionARN, event, deadline)
	// Overlap the memory sample with handler execution; REPORT waits on it
	// only briefly (see reportMemoryMB).
	memSample := ci.startMemorySample()

	ci.logger.Debug("invoke submitted",
		zap.String("container", ci.id[:12]),
		zap.String("request_id", reqID),
		zap.Int("event_bytes", len(event)),
	)

	// Emit the start record that real Lambda writes before every invocation.
	ci.emitInvocationStart(reqID)

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
			ci.waitForLogDrain(context.Background(), logDrainFirstReadGrace)
		}
		elapsed := ci.clk.Now().Sub(start)
		ci.emitInvocationEnd(reqID, outcomeCrashed, elapsed, ci.reportMemoryMB(memSample), 0)
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
			ci.waitForLogDrain(context.Background(), logDrainFirstReadGrace)
		}
		elapsed := ci.clk.Now().Sub(start)
		// Only a deadline is a Lambda timeout. A plain cancellation means the
		// caller went away — the console closing its progress stream, an SDK
		// client disconnecting — and labelling that "timeout" sends people
		// hunting for a handler bug that isn't there, with a REPORT line that
		// contradicts the function's configured timeout.
		timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
		outcome := outcomeCrashed
		if timedOut {
			outcome = outcomeTimedOut
		}
		ci.emitInvocationEnd(reqID, outcome, elapsed, ci.reportMemoryMB(memSample), 0)
		if timedOut {
			return nil, &invokeTimeoutError{RequestID: reqID, Timeout: deadline.Sub(start), At: ci.clk.Now()}
		}
		return nil, fmt.Errorf("lambda invoke cancelled before the function returned: %w", ctx.Err())
	}

	elapsed := ci.clk.Now().Sub(start)
	if reason, ok := ci.runtimeAPI.ContainerError(ci.containerIP); ok {
		ci.healthy = false
		return extensionInvokeError(reason), nil
	}

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

	// Wait for the scanner to catch up on handler stdout so the
	// X-Amz-Log-Result tail snapshot includes the function's output. Only
	// callers that asked for a tail pay this: every other path discards
	// LogResult, and warm p50 is ~6 ms, so an unconditional wait here would
	// dominate it. We do NOT wait for CloudWatch flushes — those happen
	// asynchronously after Invoke returns, as on AWS, where a function's logs
	// reach CloudWatch some time after its caller has its response.
	//
	// LocalStack chose the opposite trade and is worth knowing about rather
	// than copying: its in-container init POSTs the invocation's collected logs
	// back to LocalStack and only then the result, so every invoke pays for log
	// delivery, tail or no tail. That ordering is what lets it attribute a line
	// to an invocation without waiting on a clock — it owns the runtime's
	// stdout, so the buffer boundary is the invocation boundary. Reading logs
	// back off the Docker daemon, as here, buys the looser coupling and pays
	// for it in waitForScannerIdle.
	logWaitStart := ci.clk.Now()
	if opts.LogTail && ci.logWriter != nil {
		ci.waitForScannerIdle(logMark)
	}
	logWait := ci.clk.Now().Sub(logWaitStart)

	// Emit the END + REPORT records that real Lambda writes after every
	// invocation. These are enqueued onto the synth channel for async batching
	// alongside handler output — Invoke does not wait for their CWL delivery.
	memWaitStart := ci.clk.Now()
	memMB := ci.reportMemoryMB(memSample)
	memWait := ci.clk.Now().Sub(memWaitStart)
	outcome := outcomeSuccess
	if result.FunctionError != "" {
		outcome = outcomeHandlerError
	}
	ci.emitInvocationEnd(reqID, outcome, elapsed, memMB, len(result.Payload))

	// Per-invocation timing breakdown (Phase 0 of
	// docs/plans/lambda-cold-start.md): what the emulator added around the
	// handler's own runtime. TRACE — it fires on every invoke.
	ci.logger.Log(logging.TraceLevel, "lambda invoke timings",
		zap.String("container", shortContainerID(ci.id)),
		zap.Duration("handler", elapsed),
		zap.Duration("log_wait", logWait),
		zap.Duration("mem_wait", memWait),
		zap.Duration("total", ci.clk.Now().Sub(start)),
	)

	// Snapshot the tail buffer for X-Amz-Log-Result.
	if opts.LogTail && ci.logWriter != nil {
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

func extensionInvokeError(reason string) *InvokeResult {
	payload, _ := json.Marshal(map[string]string{
		"errorMessage": reason,
		"errorType":    "Runtime.ExtensionError",
	})
	return &InvokeResult{StatusCode: 200, Payload: payload, FunctionError: "Unhandled"}
}

// reportMemoryWaitMax bounds how long a REPORT line waits for the in-flight
// memory sample before falling back to the peak recorded so far.
const reportMemoryWaitMax = 50 * time.Millisecond

// startMemorySample kicks off an asynchronous memory read that folds into the
// instance's running peak. AWS's Max Memory Used is the execution
// environment's monotonic peak across warm reuses, so REPORT reads the peak,
// never a lone point sample. Started at invocation submit so the stats round
// trip overlaps handler execution instead of sitting on the response path.
// The returned channel closes once the sample has been folded in.
func (ci *containerInstance) startMemorySample() <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		m := int64(ci.currentMemoryMB())
		if m <= 0 {
			return // best-effort: a failed sample never lowers the peak
		}
		for {
			cur := ci.peakMemMB.Load()
			if m <= cur || ci.peakMemMB.CompareAndSwap(cur, m) {
				return
			}
		}
	}()
	return done
}

// reportMemoryMB waits briefly for the in-flight sample and returns the peak
// memory recorded for this environment. The wait is capped so writing the
// REPORT line never stalls the invoke response on a slow stats endpoint
// (pre-one-shot this was 1–2 s, and Docker-in-Docker hosts can still take
// hundreds of ms); an unresolved sample reports the previous peak, and the
// still-running goroutine folds its result in for the next invocation.
func (ci *containerInstance) reportMemoryMB(sample <-chan struct{}) int {
	if sample != nil {
		timer := ci.clk.Timer(reportMemoryWaitMax)
		defer timer.Stop()
		select {
		case <-sample:
		case <-timer.C:
		}
	}
	return int(ci.peakMemMB.Load())
}

// currentMemoryMB queries Docker for the container's current memory usage
// and returns it in megabytes. Returns 0 on error (best-effort). Callers on
// the invoke path go through startMemorySample/reportMemoryMB rather than
// calling this synchronously.
func (ci *containerInstance) currentMemoryMB() int {
	// The stats call uses one-shot=true, so it returns the current sample
	// immediately instead of waiting a daemon collection cycle (~1–2 s) —
	// which used to blow this timeout in Docker-in-Docker setups and report 0.
	// The 2 s timeout stays as a bound for genuinely slow daemons.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	bytes, err := ci.docker.ContainerMemoryUsage(ctx, ci.id)
	if err != nil {
		ci.logger.Debug("container stats: could not get memory usage (best-effort, non-fatal)", zap.Error(err))
		return 0
	}
	return int(bytes / (1024 * 1024))
}

// takeInitDuration returns how long this environment's initialization took, and
// consumes the right to report it: only the first REPORT of an on-demand cold
// start carries Init Duration, exactly as on AWS. Warm invokes and proactively
// initialised (provisioned) environments report nothing, and neither does an
// environment whose RIC never polled (init failure) — the duration runs from
// container start to the RIC's first GET /next, the same signal that drives
// readiness and the INIT-burst throttle.
func (ci *containerInstance) takeInitDuration() (time.Duration, bool) {
	if ci.initReported || ci.initStartedAt.IsZero() {
		return 0, false
	}
	firstNext, ok := ci.runtimeAPI.FirstNextAt(ci.containerIP)
	if !ok {
		return 0, false
	}
	ci.initReported = true
	initDur := firstNext.Sub(ci.initStartedAt)
	if initDur < 0 {
		initDur = 0
	}
	return initDur, true
}

// durationMillis renders a duration in the fractional milliseconds Lambda uses
// for every duration it reports.
func durationMillis(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000.0
}

// waitForScannerIdle waits for *this* invocation's container output to reach
// the rolling tail buffer, then for the Docker log reader to go quiet, so the
// X-Amz-Log-Result snapshot taken immediately afterwards is complete. Unlike
// waitForLogDrain it does NOT wait for CloudWatch writes to complete — those
// are batched asynchronously by streamLogs and are not on the invoke critical
// path.
//
// It runs on the invoke success path only when the caller asked for a tail
// (InvokeOptions.LogTail); see the call site for why that gate matters.
//
// mark is where the container's two log watermarks stood when the invocation
// started, and it is what makes the wait per-invocation. Both only ever move
// forward across the container's whole life, so neither says on its own *whose*
// output set it:
//
//   - Cold container: both are still 0 while the handler's stdout sits
//     undelivered in Docker's pipe. Reading that as "the function emitted
//     nothing" — which this function used to do — snapshots a tail holding only
//     the START/END/REPORT lines writeLogLine puts there directly.
//   - Warm container: both still hold the *previous* invocation's timestamps,
//     already older than idleThreshold, so the idle test passed instantly and
//     the wait was effectively a no-op.
//
// The two watermarks answer different questions, and asking logReadAt the other
// one's question is the second defect this collapses:
//
//   - tailAppendAt moves when a container line is appended to tailBuf. That is
//     the signal that means what the caller needs, because tailBuf is exactly
//     what the snapshot reads.
//   - logReadAt moves when Docker hands over bytes, several steps earlier. A
//     Read seldom lands a whole line — the line reader keeps reading until it
//     finds a newline, and logInFlight does not count the line until it has one
//     — so the pipeline routinely sits in a state where logReadAt has moved and
//     the tail is still empty. Treating that as arrived-and-quiet returned a
//     tail without the handler's own line, which then landed in whichever
//     invocation's buffer was current when it was finally parsed: the next one.
//
// logReadAt still earns its place, for the question it does answer — whether
// this invocation produced *anything* — which is what separates the two ways of
// having an empty tail. Four bounds shape the wait:
//
//   - firstReadMax caps how long we wait when not a byte has arrived. A handler
//     that prints nothing is indistinguishable from one whose output has not
//     left Docker's pipe, so silence costs this much — paid only by
//     tail-requesting invokes.
//   - Once bytes have arrived, output exists and is on its way, so there is
//     nothing to guess at: the wait holds for the line itself, bounded only by
//     deadlineMax. A handler that writes without a trailing newline pays that
//     full bound, because the line reader has nothing to hand over until a
//     newline turns up and the tail could not have shown the write either way.
//   - idleThreshold is how long the appends must stop after one lands before
//     the output is treated as complete; a handler's lines can span reads.
//   - deadlineMax bounds the whole thing, so a wedged log pipeline can never
//     hold up the invoke response.
func (ci *containerInstance) waitForScannerIdle(mark tailMark) {
	const (
		idleThreshold = 5 * time.Millisecond
		firstReadMax  = 25 * time.Millisecond
		deadlineMax   = 100 * time.Millisecond
		tickInterval  = 1 * time.Millisecond
	)
	start := ci.clk.Now()
	deadline := ci.clk.Timer(deadlineMax)
	defer deadline.Stop()
	tick := ci.clk.Ticker(tickInterval)
	defer tick.Stop()
	// However this wait ends, if it ends without this invocation's own output
	// then that output is still coming and is still this invocation's. Leave
	// the boundary behind so the next tail can drop it rather than open with a
	// line it never wrote — see discardUnclaimedOutput.
	defer func() {
		if ci.tailAppendAt.Load() > mark.appended {
			return
		}
		ci.tailMu.Lock()
		ci.tailUnclaimed, ci.tailUnclaimedMark = true, mark
		ci.tailMu.Unlock()
	}()
	for {
		select {
		case <-deadline.C:
			return
		case <-tick.C:
			// Lines parsed but not yet flushed mean output exists and is still
			// moving, whatever the watermarks currently say.
			if ci.logInFlight.Load() > 0 {
				continue
			}
			if last := ci.tailAppendAt.Load(); last > mark.appended {
				// This invocation's output has reached the buffer. One append
				// is not the end of it, so wait out a quiet spell before
				// calling the tail complete.
				if ci.clk.Since(time.Unix(0, last)) >= idleThreshold {
					return
				}
				continue
			}
			if ci.logReadAt.Load() > mark.read {
				// Docker has handed over bytes for this invocation but no
				// complete line has come of them yet. Output exists; wait for
				// it rather than for the clock.
				continue
			}
			// Nothing has been read at all since this invocation started. Give
			// Docker a bounded grace period before concluding the handler was
			// simply silent.
			if ci.clk.Since(start) >= firstReadMax {
				return
			}
		}
	}
}

// tailMark is one invocation's boundary in the container's log history: both
// watermarks as they stood when its tail buffer was reset.
type tailMark struct {
	read     int64 // ci.logReadAt — Docker handed over bytes
	appended int64 // ci.tailAppendAt — a line reached ci.tailBuf
}

// beginTail starts a fresh tail for an invocation and returns its boundary. The
// buffer is emptied so the invocation's output starts clean (which matters on
// warm-container reuse), and the watermarks are read in the same critical
// section: they are container-lifetime values, so what they hold at the reset
// is precisely what "before this invocation" means to waitForScannerIdle.
// Reading them outside the lock would let a line land in between and belong to
// neither side of the boundary.
func (ci *containerInstance) beginTail() tailMark {
	ci.tailMu.Lock()
	defer ci.tailMu.Unlock()
	ci.tailBuf = ci.tailBuf[:0]
	return tailMark{read: ci.logReadAt.Load(), appended: ci.tailAppendAt.Load()}
}

// discardUnclaimedOutput settles the previous invocation's account before a new
// tail opens: if its wait expired before its output arrived, that output is
// still on its way, and the invocation it belongs to has already answered. Run
// immediately before beginTail, whose reset is what actually drops it.
//
// The wait it runs is the same bounded one the invocation itself ran, against
// the same boundary — long enough for Docker to hand over what it was holding,
// and no longer. Two things follow, both deliberate:
//
//   - The cost lands on the next tail-requesting invoke, and only after a
//     give-up. Non-tail invokes never call this, for the same reason they never
//     wait: they discard LogResult, and warm p50 is ~6 ms.
//   - Straggler output is dropped from the *tail* only. It still reaches
//     CloudWatch Logs by the streaming path, which never consults any of this,
//     so nothing is lost — it is read one call further out.
//
// This narrows the window rather than closing it: output that stays in Docker's
// pipe through both waits is still unattributable, and still lands in the next
// tail. Closing it outright needs the line tagged at the point it is written,
// which needs Overcast to own the runtime's stdout inside the container the way
// AWS's RIE and LocalStack's init do. What this buys is that a single stall now
// costs a truncated tail — which is what LogResult promises, "the last 4 KB of
// the execution log" — instead of one carrying another request's line, which
// LogResult can never mean.
func (ci *containerInstance) discardUnclaimedOutput() {
	ci.tailMu.Lock()
	unclaimed, mark := ci.tailUnclaimed, ci.tailUnclaimedMark
	ci.tailUnclaimed = false
	ci.tailMu.Unlock()
	if unclaimed {
		ci.waitForScannerIdle(mark)
	}
}

// logDrainFirstReadGrace is how long a teardown that follows a failed
// invocation waits for a log stream that has produced nothing yet, before
// accepting that the container really did print nothing. Bounded small: it is
// only ever paid once per dying container, and only when there is no output at
// all to go on.
const logDrainFirstReadGrace = 25 * time.Millisecond

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
// 10–15 ms.
//
// firstReadGrace says how long to keep waiting when the stream has never
// produced anything at all (logReadAt == 0 AND inFlight == 0). That state is
// ambiguous in the same way waitForScannerIdle's is — a container that logged
// nothing all its life looks exactly like one whose first bytes are still in
// Docker's pipe — but here the stakes and the costs differ by caller, so the
// choice is theirs:
//
//   - A container that died mid-invocation, or an invocation that timed out,
//     passes logDrainFirstReadGrace. Whatever it printed on the way out is the
//     output most worth keeping and the likeliest to be in flight, and logCtx
//     is cancelled moments later, which loses it from CloudWatch for good.
//   - Close passes 0. Pool eviction closes stale instances on the acquire path
//     (InstancePool.takeWarm), so a grace there is charged to the next cold
//     start; and a container that produced no reads across its whole lifetime,
//     having already drained through one of the paths above on any failed
//     invocation, has nothing left to wait for.
func (ci *containerInstance) waitForLogDrain(ctx context.Context, firstReadGrace time.Duration) {
	const (
		idleThreshold = 10 * time.Millisecond
		deadlineMax   = 2 * time.Second
		tickInterval  = 2 * time.Millisecond
	)
	start := ci.clk.Now()
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
			// Reader has never produced anything — see firstReadGrace above.
			if last == 0 {
				if ci.clk.Since(start) >= firstReadGrace {
					return
				}
				continue
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
func (ci *containerInstance) writeLogLine(_ context.Context, line string) {
	ci.publishRuntimeLog("platform", line)
	ci.deliverSynthLine(line)
}

// publishRuntimeLog hands a record to Telemetry/Logs API subscribers. It is
// deliberately separate from delivery: subscribers always see the complete set
// of records, whatever SystemLogLevel filters out of CloudWatch.
func (ci *containerInstance) publishRuntimeLog(logType, line string) {
	if ci.containerIP != "" {
		ci.runtimeAPI.PublishExtensionLog(ci.containerIP, logType, line)
	}
}

// deliverSynthLine writes a synthesised line to the rolling tail buffer and to
// CloudWatch Logs.
func (ci *containerInstance) deliverSynthLine(line string) {
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
	_ = ci.logWriter.EnsureLogStream(writeCtx, ci.logGroupName, ci.logStream)
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
		ci.waitForLogDrain(context.Background(), 0)
	}
	if ci.containerIP != "" {
		if queued := ci.runtimeAPI.EnqueueExtensionShutdown(ci.containerIP, "SPINDOWN", ci.clk.Now().Add(2*time.Second)); queued > 0 {
			select {
			case <-ci.clk.After(100 * time.Millisecond):
			case <-ci.logCtx.Done():
			}
		}
	}
	ci.logCancel()
	<-ci.logDone
	if ci.containerIP != "" {
		ci.runtimeAPI.UnregisterContainer(ci.containerIP)
	}
	// The environment's Runtime API endpoint goes with the environment: the
	// port is per-execution-environment, and leaving it bound would accumulate
	// one dead listener per container the pool retires.
	_ = ci.rapiListener.Close()
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
// Line assembly uses a bounded reader on a docker.DemuxReader so oversized log
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

	// Wrap the multiplexed stream so the line reader sees a plain byte stream,
	// with the timestamp Docker repeats on each continuation chunk of a long
	// line dropped rather than spliced into the middle of it.
	stripped := docker.NewLogDemuxReader(stream)
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
			if !ci.ingestContainerLine(line) {
				ci.logInFlight.Add(-1)
				continue
			}

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
					if !ci.ingestContainerLine(msg) {
						ci.logInFlight.Add(-1)
						continue
					}
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

	stripped := docker.NewLogDemuxReader(body)
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

// lambdaLogStreamName generates an AWS-style log stream name. AWS names the
// stream after the date and the execution environment's GUID, so the suffix is
// 32 hex characters — 2019/07/12/[$LATEST]312c2d81e2e64af58dbe557754f9aa13 —
// and a shorter one fails anything matching stream names against that shape.
// Format: YYYY/MM/DD/[$LATEST]<32-char lowercase hex>.
func lambdaLogStreamName(clk clock.Clock) string {
	date := clk.Now().UTC().Format("2006/01/02")
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d", clk.Now().UnixNano())))
	return date + "/[$LATEST]" + hex.EncodeToString(hash[:16])
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

// resolveLayerTars fetches (or serves from cache) each attached layer as an
// opt/-prefixed tar for the single provisioning archive, in layer order —
// later layers overwrite earlier ones at extraction, matching AWS layer
// merge semantics. No Docker calls happen here; assembly is pure CPU.
// Returns the ordered tars, discovered external extension names, and one
// synthesized warning log line per layer that had to be skipped.
func (cr *ContainerRuntime) resolveLayerTars(ctx context.Context, fn *Function) ([][]byte, []string, []string, error) {
	if len(fn.Layers) == 0 {
		return nil, nil, nil, nil
	}
	if cr.layerFetcher == nil {
		return nil, nil, nil, fmt.Errorf("layer content fetcher is not configured")
	}
	tars := make([][]byte, 0, len(fn.Layers))
	extensionSet := make(map[string]bool)
	skippedLayerLogLines := make([]string, 0)
	for _, layer := range fn.Layers {
		arn := strings.TrimSpace(layer.ARN)
		if arn == "" {
			continue
		}
		// Layer versions are immutable, so ARN-keyed tars (and the extension
		// scan) are cached across cold starts and functions.
		if cached, ok := cr.tarCache.get("layer:" + arn); ok {
			tars = append(tars, cached.data)
			for _, name := range cached.extensions {
				extensionSet[name] = true
			}
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
					skippedLayerLogLines = append(skippedLayerLogLines, skippedLayerWarningLogLine(arn, remoteErr))
					continue
				}
				zipData = remoteData
			} else {
				cr.logger.Warn("layer not available locally — skipping",
					zap.String("arn", arn), zap.Error(err))
				skippedLayerLogLines = append(skippedLayerLogLines, skippedLayerWarningLogLine(arn, err))
				continue
			}
		}
		layerTar, err := zipToTarPrefixed(zipData, "opt/")
		if err != nil {
			return nil, nil, nil, fmt.Errorf("build tar for layer %s: %w", arn, err)
		}
		tars = append(tars, layerTar)
		discovered := discoverExternalExtensions(zipData)
		for _, name := range discovered {
			extensionSet[name] = true
		}
		cr.tarCache.put(&tarCacheEntry{key: "layer:" + arn, data: layerTar, extensions: discovered})
	}
	extensions := make([]string, 0, len(extensionSet))
	for name := range extensionSet {
		extensions = append(extensions, name)
	}
	slices.Sort(extensions)
	if len(extensions) == 0 {
		extensions = nil
	}
	return tars, extensions, skippedLayerLogLines, nil
}

func skippedLayerWarningLogLine(layerVersionARN string, cause error) string {
	name := layerVersionARN
	version := ""
	if parsed, err := parseLayerARN(layerVersionARN); err == nil {
		name = parsed.LayerName
		version = parsed.Version
	}
	if version != "" {
		return fmt.Sprintf("[Overcast Lambda] WARNING: Lambda layer %s:%s (%s) was not loaded because it is not available locally and remote AWS layer fetching is not configured or failed: %v", name, version, layerVersionARN, cause)
	}
	return fmt.Sprintf("[Overcast Lambda] WARNING: Lambda layer %s was not loaded because it is not available locally and remote AWS layer fetching is not configured or failed: %v", layerVersionARN, cause)
}

func discoverExternalExtensions(zipData []byte) []string {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil
	}
	extensions := make([]string, 0)
	for _, f := range zr.File {
		name := strings.TrimPrefix(f.Name, "./")
		if strings.Count(name, "/") != 1 || !strings.HasPrefix(name, "extensions/") || strings.HasSuffix(name, "/") {
			continue
		}
		if f.FileInfo().Mode().Perm()&0o111 == 0 {
			continue
		}
		extensions = append(extensions, strings.TrimPrefix(name, "extensions/"))
	}
	slices.Sort(extensions)
	return extensions
}

// lambdaBootstrapTar is the static bootstrap archive, built once — its
// content never varies between cold starts.
var lambdaBootstrapTar = sync.OnceValues(buildLambdaBootstrapTar)

func buildLambdaBootstrapTar() ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	script := lambdaBootstrapScript()
	if err := tw.WriteHeader(&tar.Header{
		Name:     "var/overcast/",
		Mode:     0o755,
		Typeflag: tar.TypeDir,
	}); err != nil {
		return nil, err
	}
	if err := tw.WriteHeader(&tar.Header{
		Name: "var/overcast/bootstrap",
		Size: int64(len(script)),
		Mode: 0o755,
	}); err != nil {
		return nil, err
	}
	if _, err := tw.Write(script); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// appendTarEntries re-writes every entry of a component tar into tw, so
// independently built (and cached) component archives can be merged into the
// single provisioning archive without re-reading their sources.
func appendTarEntries(tw *tar.Writer, component []byte) error {
	tr := tar.NewReader(bytes.NewReader(component))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := io.Copy(tw, tr); err != nil {
			return err
		}
	}
}

func lambdaBootstrapScript() []byte {
	return []byte(`#!/bin/sh
set -eu

if [ -d /opt/extensions ]; then
  for ext in /opt/extensions/*; do
    if [ -f "$ext" ] && [ -x "$ext" ]; then
      name="$(basename "$ext")"
      ("$ext" 2>&1 | while IFS= read -r line; do printf '[overcast-extension:%s] %s\n' "$name" "$line"; done) &
    fi
  done
fi

exec /lambda-entrypoint.sh "$@"
`)
}

func classifyRuntimeLogLine(line string) (string, string) {
	const prefix = "[overcast-extension:"
	if !strings.HasPrefix(line, prefix) {
		return "function", line
	}
	end := strings.Index(line, "] ")
	if end < len(prefix) {
		return "function", line
	}
	name := line[len(prefix):end]
	record := strings.TrimPrefix(line[end+2:], " ")
	if name == "" || record == "" {
		return "function", line
	}
	return "extension", record
}

// zipToTar converts a zip archive (raw bytes) into a tar archive suitable for
// the Docker "Put Archive" API. This avoids extracting to a local temp
// directory, which breaks when running inside a devcontainer (the Docker daemon
// runs on the host and cannot see the devcontainer's filesystem).
func zipToTar(zipData []byte) ([]byte, error) {
	return zipToTarPrefixed(zipData, "")
}

// zipToTarPrefixed converts a zip archive to a tar archive whose entry names
// carry prefix (e.g. "var/task/"), so the result can be extracted at the
// container root as part of the single provisioning archive. The prefix root
// itself is never emitted as a directory header — the base image owns those
// directories, and a header would reset their modes on extraction.
func zipToTarPrefixed(zipData []byte, prefix string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	for _, f := range zr.File {
		// Zip tools sometimes emit "./"-relative names; normalize before
		// prefixing so "./index.js" becomes "var/task/index.js", not
		// "var/task/./index.js".
		name := prefix + strings.TrimPrefix(f.Name, "./")
		if name == prefix || name == "" {
			continue // a bare "./" entry would emit the prefix root itself
		}
		mode := int64(f.FileInfo().Mode().Perm())
		if mode == 0 {
			mode = 0o644
		}
		header := &tar.Header{
			Name:    name,
			Size:    int64(f.UncompressedSize64),
			Mode:    mode,
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
