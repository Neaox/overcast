package lambda

// handler.go contains the Handler struct for Lambda.
// Lambda uses REST routing (not target-dispatch), so there is no ops map.
// Route registration is done in service.go's RegisterRoutes.
// Stub handlers live in handler_stubs.go; implemented handlers live in
// handler_<group>.go files as they are built out.

import (
	"context"
	"sync"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// Handler holds Lambda handler dependencies.
type Handler struct {
	cfg         *config.Config
	log         *serviceutil.ServiceLogger
	clk         clock.Clock
	runtimes    *runtimeRegistry
	ls          *lambdaStore
	logWriter   events.LogWriter
	bus         *events.Bus
	tracker     *instanceTracker
	rtCache     *runtimeCache
	esm         *esmStore
	esmDelivery *esmDeliveryManager
	asyncWg     sync.WaitGroup // tracks in-flight async invocations
	vpcResolver VPCNetworkResolver
	// prewarmer, when set, starts a background Docker image pull for fn so
	// the first Invoke does not pay the cold-pull cost on the request path.
	// Wired by Service.initDockerRuntime once ContainerRuntime is up; nil
	// before Docker is ready and in test servers that don't use containers.
	// onReady is called on the background goroutine with the pull result.
	prewarmer func(fn *Function, onReady func(err error))
	// s3Fetch, when set, allows handlers to eagerly fetch code zips from S3
	// at CreateFunction / UpdateFunctionCode time. Without this, code that
	// is uploaded to S3 *before* the Lambda function is created (the typical
	// CDK deploy ordering: PutObject → CreateFunction) would never have its
	// CodeZip populated — the s3SyncWatcher only fires on subsequent
	// PutObject events. Wired by Service.InitS3Sync.
	s3Fetch S3FetchFunc
}

// VPCNetworkResolver resolves VPC configuration for Lambda functions.
// Implemented by the EC2 service; nil when EC2 is not enabled.
type VPCNetworkResolver interface {
	// VpcIDForSubnet returns the VPC ID that owns the given subnet.
	VpcIDForSubnet(ctx context.Context, subnetID string) string
	// VPCNetworkStatus returns the launchability status for the VPC.
	VPCNetworkStatus(ctx context.Context, vpcID string) string
	// DockerNetworkForVpc returns the Docker network ID for the given VPC.
	// Returns empty string if the VPC has no Docker network.
	DockerNetworkForVpc(ctx context.Context, vpcID string) string
}

func newHandler(cfg *config.Config, log *serviceutil.ServiceLogger, clk clock.Clock, runtimes *runtimeRegistry, ls *lambdaStore, tracker *instanceTracker, rtCache *runtimeCache) *Handler {
	return &Handler{cfg: cfg, log: log, clk: clk, runtimes: runtimes, ls: ls, tracker: tracker, rtCache: rtCache}
}

// StopAsync waits for all in-flight async invocations to complete, with a
// timeout provided by ctx. This prevents goroutine leaks on shutdown.
func (h *Handler) StopAsync(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		h.asyncWg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}
