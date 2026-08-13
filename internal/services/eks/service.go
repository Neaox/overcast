// Package eks provides Amazon EKS control-plane emulation.
//
// EKS is restJson1. Every route this package registers is the method and URI
// the pinned model binds the operation to — run
// `go run -tags dev ./cmd/capgen --routes --service eks` for the generated
// skeleton, and see RegisterRoutes for the one endpoint that is deliberately
// not an AWS operation.
//
// By default (`OVERCAST_EKS_MODE=mock`) the implementation is metadata-only.
// In live mode (`OVERCAST_EKS_MODE=live`), CreateCluster starts a k3s
// control-plane container and transitions the cluster to ACTIVE once the API
// server readiness endpoint responds.
package eks

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/dataplane"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "eks"

const (
	nsClusters   = "eks:clusters"
	nsNodegroups = "eks:nodegroups"
	nsUpdates    = "eks:updates"
	nsTags       = "eks:tags"
	nsFargate    = "eks:fargate"
	nsAddons     = "eks:addons"
	nsIDPConfigs = "eks:idpconfigs"
	nsAccess     = "eks:accessentries"
	nsAccessPol  = "eks:accesspolicies"
	nsPodIDAssoc = "eks:podidentityassociations"
	nsInstance   = "eks:instance"
)

// Cluster represents the EKS cluster metadata returned by controller APIs.
type Cluster struct {
	Name                    string            `json:"name"`
	Arn                     string            `json:"arn"`
	CreatedAt               time.Time         `json:"createdAt"`
	Version                 string            `json:"version"`
	Endpoint                string            `json:"endpoint"`
	RoleArn                 string            `json:"roleArn"`
	Status                  string            `json:"status"`
	Health                  map[string]any    `json:"health,omitempty"`
	CertificateAuthority    map[string]any    `json:"certificateAuthority"`
	ResourcesVPCConfig      map[string]any    `json:"resourcesVpcConfig"`
	KubernetesNetworkConfig map[string]any    `json:"kubernetesNetworkConfig,omitempty"`
	EncryptionConfig        []map[string]any  `json:"encryptionConfig,omitempty"`
	Logging                 map[string]any    `json:"logging,omitempty"`
	Tags                    map[string]string `json:"tags,omitempty"`
}

// Nodegroup is metadata for managed nodegroups in mock mode.
type Nodegroup struct {
	ClusterName    string            `json:"clusterName"`
	NodegroupName  string            `json:"nodegroupName"`
	NodegroupArn   string            `json:"nodegroupArn"`
	CreatedAt      time.Time         `json:"createdAt"`
	Status         string            `json:"status"`
	Version        string            `json:"version,omitempty"`
	ReleaseVersion string            `json:"releaseVersion,omitempty"`
	NodeRole       string            `json:"nodeRole"`
	Subnets        []string          `json:"subnets,omitempty"`
	InstanceTypes  []string          `json:"instanceTypes,omitempty"`
	AmiType        string            `json:"amiType,omitempty"`
	CapacityType   string            `json:"capacityType,omitempty"`
	DiskSize       int               `json:"diskSize,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
	Taints         []map[string]any  `json:"taints,omitempty"`
	ScalingConfig  map[string]any    `json:"scalingConfig,omitempty"`
	UpdateConfig   map[string]any    `json:"updateConfig,omitempty"`
	LaunchTemplate map[string]any    `json:"launchTemplate,omitempty"`
	Tags           map[string]string `json:"tags,omitempty"`
}

// FargateProfile is metadata for EKS Fargate profiles in mock mode.
type FargateProfile struct {
	ClusterName         string            `json:"clusterName"`
	FargateProfileName  string            `json:"fargateProfileName"`
	FargateProfileArn   string            `json:"fargateProfileArn"`
	CreatedAt           time.Time         `json:"createdAt"`
	Status              string            `json:"status"`
	PodExecutionRoleArn string            `json:"podExecutionRoleArn"`
	Subnets             []string          `json:"subnets,omitempty"`
	Selectors           []map[string]any  `json:"selectors,omitempty"`
	Tags                map[string]string `json:"tags,omitempty"`
}

// Update represents an EKS update operation result.
type Update struct {
	ID        string           `json:"id"`
	Status    string           `json:"status"`
	Type      string           `json:"type"`
	CreatedAt time.Time        `json:"createdAt"`
	Params    []map[string]any `json:"params,omitempty"`
}

// Addon represents an EKS managed add-on in mock mode.
type Addon struct {
	ClusterName           string            `json:"clusterName"`
	AddonName             string            `json:"addonName"`
	AddonArn              string            `json:"addonArn"`
	AddonVersion          string            `json:"addonVersion,omitempty"`
	ConfigurationValues   string            `json:"configurationValues,omitempty"`
	ServiceAccountRoleArn string            `json:"serviceAccountRoleArn,omitempty"`
	CreatedAt             time.Time         `json:"createdAt"`
	Status                string            `json:"status"`
	Tags                  map[string]string `json:"tags,omitempty"`
}

// IdentityProviderConfig represents an EKS associated identity provider config.
type IdentityProviderConfig struct {
	ClusterName string            `json:"clusterName"`
	Type        string            `json:"type"`
	Name        string            `json:"name"`
	OIDC        map[string]any    `json:"oidc,omitempty"`
	CreatedAt   time.Time         `json:"createdAt"`
	Tags        map[string]string `json:"tags,omitempty"`
}

// AccessEntry represents an EKS access entry association.
type AccessEntry struct {
	ClusterName      string            `json:"clusterName"`
	PrincipalArn     string            `json:"principalArn"`
	Type             string            `json:"type,omitempty"`
	Username         string            `json:"username,omitempty"`
	KubernetesGroups []string          `json:"kubernetesGroups,omitempty"`
	CreatedAt        time.Time         `json:"createdAt"`
	ModifiedAt       time.Time         `json:"modifiedAt"`
	Tags             map[string]string `json:"tags,omitempty"`
}

// AssociatedAccessPolicy is metadata for policies associated with an access entry.
type AssociatedAccessPolicy struct {
	ClusterName  string         `json:"clusterName"`
	PrincipalArn string         `json:"principalArn"`
	PolicyArn    string         `json:"policyArn"`
	AccessScope  map[string]any `json:"accessScope,omitempty"`
	AssociatedAt time.Time      `json:"associatedAt"`
	ModifiedAt   time.Time      `json:"modifiedAt"`
}

// PodIdentityAssociation is metadata for EKS pod identity associations.
type PodIdentityAssociation struct {
	ClusterName    string            `json:"clusterName"`
	AssociationID  string            `json:"associationId"`
	AssociationArn string            `json:"associationArn"`
	Namespace      string            `json:"namespace"`
	ServiceAccount string            `json:"serviceAccount"`
	RoleArn        string            `json:"roleArn"`
	CreatedAt      time.Time         `json:"createdAt"`
	ModifiedAt     time.Time         `json:"modifiedAt"`
	Tags           map[string]string `json:"tags,omitempty"`
}

// Service implements router.Service for EKS.
type Service struct {
	cfg   *config.Config
	store state.Store
	clk   clock.Clock
	log   *serviceutil.ServiceLogger

	typedOp map[string]op.Operation

	docker      *docker.Client
	puller      *docker.ImagePuller
	dockerReady atomic.Bool
	vpcResolver VPCNetworkResolver
	instances   *serviceutil.InstanceDomain

	liveMu       sync.Mutex
	liveRuntimes map[string]*liveClusterRuntime

	// liveCtx is the context cluster bootstraps run under; liveCancel ends
	// them. A bootstrap outlives the request that started it, so it cannot
	// use the request's context — but running it on context.Background() left
	// Stop with no way to end one, and a bootstrap can sit in pollK3sReady
	// for five minutes waiting on a control plane that never answers. Every
	// service's Stop shares one shutdown budget, so that is not a wait Stop
	// can simply take. Same shape as ElastiCache's bgCtx/bgCancel.
	liveCtx    context.Context
	liveCancel context.CancelFunc

	// liveWg tracks in-flight bootstraps so Stop can wait for them to finish
	// before it takes stock of what is running. A bootstrap registers its
	// container on the way out, so draining the registry while one is still
	// in flight leaves that container behind. Same shape as the dockerWg that
	// RDS, ElastiCache and MSK track their container starts with.
	liveWg sync.WaitGroup
	// liveLifecycleMu closes the WaitGroup Add/Wait boundary for recovery
	// goroutines when service shutdown begins.
	liveLifecycleMu sync.Mutex
	liveStopping    bool
	// liveRecoveries coalesces startup, reconnect, and watcher-driven recovery
	// for one persisted control plane.
	liveRecoveries sync.Map

	// startLiveClusterHook, when non-nil, stands in for the background
	// bootstrap CreateCluster hands off to. Always nil in production; set
	// only by tests in this package that need the hand-off under their own
	// control — either to assert on the record CreateCluster persisted, which
	// the real bootstrap races (a bootstrap that cannot reach Docker now moves
	// the cluster to the terminal FAILED state (#895), so "still CREATING"
	// holds only until the goroutine is scheduled — see suspendLiveBootstrap),
	// or to hold a bootstrap in flight while Stop runs.
	startLiveClusterHook func(ctx context.Context, region string, cluster *Cluster)
}

// VPCNetworkResolver resolves a cluster's resourcesVpcConfig subnets back to
// EC2 VPC network state. Implemented by the EC2 service; nil when EC2 is not
// enabled, which leaves every control plane on the default data plane.
type VPCNetworkResolver interface {
	dataplane.VPCResolver
	// VpcIDForSubnet returns the VPC ID that owns the given subnet.
	VpcIDForSubnet(ctx context.Context, subnetID string) string
}

type liveClusterRuntime struct {
	containerID string
}

// New returns a configured EKS service.
func New(cfg *config.Config, st state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	s := &Service{
		cfg:          cfg,
		store:        st,
		clk:          clk,
		log:          serviceutil.NewServiceLogger(logger, serviceName),
		liveRuntimes: make(map[string]*liveClusterRuntime),
		instances:    serviceutil.NewInstanceDomain(st, nsInstance),
	}
	s.liveCtx, s.liveCancel = context.WithCancel(context.Background())
	s.typedOp = s.typedOps()
	return s
}

func (s *Service) Name() string { return serviceName }

// InitBus wires immediate managed-control-plane recovery for Docker exits.
// Startup and event-stream gaps use the same recovery path through
// ReconcileContainers.
func (s *Service) InitBus(bus *events.Bus) {
	bus.Subscribe(events.DockerContainerDied, s.handleLiveRuntimeDied)
}

func (s *Service) TargetPrefix() string { return "EKS." }

func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if codec.Supports(s.SupportedProtocols(), c) {
			if typed, ok := s.typedOp[opName]; ok {
				typed.Invoke(w, r, c)
				return
			}
		}
		c.WriteError(w, r, protocol.ErrNotImplemented)
		return
	}
	protocol.NotImplementedJSON(w, r)
}

// SetVPCResolver wires the EC2 VPC resolver, so a cluster whose
// resourcesVpcConfig names subnets has its control plane placed on that VPC's
// network rather than the default plane.
func (s *Service) SetVPCResolver(r VPCNetworkResolver) {
	s.vpcResolver = r
}

// SetDocker wires the Docker client used for live-mode control-plane runtime
// work, and the shared image puller that fetches the k3s image before a
// control plane is created.
func (s *Service) SetDocker(dc *docker.Client) {
	s.docker = dc
	if dc != nil {
		s.puller = docker.NewImagePuller(dc)
	} else {
		s.puller = nil
	}
	s.dockerReady.Store(dc != nil)
}

// ReconcileContainers satisfies router.ContainerReconciler. Amazon EKS
// replaces unhealthy control-plane instances; persisted CREATING/ACTIVE
// clusters therefore re-adopt a running runtime or start a replacement.
func (s *Service) ReconcileContainers(ctx context.Context, containers []docker.ContainerSummary) {
	s.reconcileLiveClusterContainers(ctx, containers)
}

// Stop satisfies router.Stopper. Live-mode cleanup is best-effort for both the
// in-memory runtime registry and any persisted live clusters that need runtime
// reconciliation after a process restart.
//
// In-flight bootstraps are ended and waited for first. Cleanup works from the
// runtime registry, and a bootstrap only registers its container once it has
// started it, so cleaning up while one is still running would tear down the
// containers Stop knows about and leave that one behind. ctx bounds the wait,
// so a bootstrap that will not end still cannot hold shutdown open.
func (s *Service) Stop(ctx context.Context) {
	s.liveLifecycleMu.Lock()
	s.liveStopping = true
	s.liveCancel()
	s.liveLifecycleMu.Unlock()
	bootstrapsDone := make(chan struct{})
	go func() {
		s.liveWg.Wait()
		close(bootstrapsDone)
	}()
	select {
	case <-bootstrapsDone:
	case <-ctx.Done():
	}

	s.reconcilePersistedLiveClusterRuntimes(ctx)
	for _, runtime := range s.drainLiveClusterRuntimes() {
		if err := s.cleanupLiveClusterRuntime(ctx, runtime); err != nil {
			s.log.Warn("failed to clean up EKS live runtime during stop", zap.Error(err))
		}
	}
}

// RegisterRoutes registers EKS at the bindings the pinned model gives it.
//
// Two conventions are worth knowing before editing this list:
//
//   - The model spells the cluster label {clusterName} on most operations and
//     {name} on the older ones (DescribeCluster, DeleteCluster,
//     UpdateClusterConfig, ListUpdates, UpdateClusterVersion, DescribeUpdate,
//     CancelUpdate). A chi label's name is local, and mixing two names at one
//     segment position puts two param nodes in the trie for the same path.
//     Every cluster label here is therefore {name}; the path shape, which is
//     what a client sends, is identical either way.
//   - POST /_overcast/eks/clusters/{name}/kubeconfig is the one endpoint here that AWS does
//     not model. `aws eks update-kubeconfig` is a CLI-side command that calls
//     DescribeCluster and writes the file locally, so there is no API to be
//     faithful to; Overcast serves the generated kubeconfig instead, and the
//     capability row says so. It is not reachable by any SDK and is not meant
//     to be.
func (s *Service) RegisterRoutes(r chi.Router) {
	r.Post("/clusters", s.createCluster)
	r.Get("/clusters", s.listClusters)
	r.Get("/clusters/{name}", s.describeCluster)
	r.Post("/clusters/{name}/access-entries", s.createAccessEntry)
	r.Get("/clusters/{name}/access-entries", s.listAccessEntries)
	r.Get("/clusters/{name}/access-entries/{principalArn}", s.describeAccessEntry)
	r.Post("/clusters/{name}/access-entries/{principalArn}", s.updateAccessEntry)
	r.Delete("/clusters/{name}/access-entries/{principalArn}", s.deleteAccessEntry)
	r.Post("/clusters/{name}/access-entries/{principalArn}/access-policies", s.associateAccessPolicy)
	r.Get("/clusters/{name}/access-entries/{principalArn}/access-policies", s.listAssociatedAccessPolicies)
	r.Delete("/clusters/{name}/access-entries/{principalArn}/access-policies/{policyArn}", s.disassociateAccessPolicy)
	r.Get("/access-policies", s.listAccessPolicies)
	r.Get("/cluster-versions", s.describeClusterVersions)
	r.Get("/clusters/{name}/identity-provider-configs", s.listIdentityProviderConfigs)
	r.Post("/clusters/{name}/identity-provider-configs/describe", s.describeIdentityProviderConfig)
	r.Post("/clusters/{name}/identity-provider-configs/associate", s.associateIdentityProviderConfig)
	r.Post("/clusters/{name}/identity-provider-configs/disassociate", s.disassociateIdentityProviderConfig)
	r.Post("/clusters/{name}/pod-identity-associations", s.createPodIdentityAssociation)
	r.Get("/clusters/{name}/pod-identity-associations", s.listPodIdentityAssociations)
	r.Get("/clusters/{name}/pod-identity-associations/{associationId}", s.describePodIdentityAssociation)
	r.Post("/clusters/{name}/pod-identity-associations/{associationId}", s.updatePodIdentityAssociation)
	r.Delete("/clusters/{name}/pod-identity-associations/{associationId}", s.deletePodIdentityAssociation)
	r.Get("/clusters/{name}/updates", s.listUpdates)
	r.Post("/clusters/{name}/updates", s.updateClusterVersion)
	r.Post("/clusters/{name}/insights", s.listInsights)
	r.Get("/clusters/{name}/insights/{insightId}", s.describeInsight)
	r.Post("/clusters/{name}/update-config", s.updateClusterConfig)
	r.Get("/clusters/{name}/updates/{updateId}", s.describeUpdate)
	r.Post("/_overcast/eks/clusters/{name}/kubeconfig", s.updateKubeconfig)
	r.Delete("/clusters/{name}", s.deleteCluster)
	r.Post("/clusters/{name}/node-groups", s.createNodegroup)
	r.Post("/clusters/{name}/node-groups/{nodegroupName}/update-version", s.updateNodegroupVersion)
	r.Post("/clusters/{name}/node-groups/{nodegroupName}/update-config", s.updateNodegroupConfig)
	r.Get("/clusters/{name}/node-groups", s.listNodegroups)
	r.Get("/clusters/{name}/node-groups/{nodegroupName}", s.describeNodegroup)
	r.Delete("/clusters/{name}/node-groups/{nodegroupName}", s.deleteNodegroup)
	r.Get("/clusters/{name}/fargate-profiles", s.listFargateProfiles)
	r.Get("/clusters/{name}/fargate-profiles/{fargateProfileName}", s.describeFargateProfile)
	r.Post("/clusters/{name}/fargate-profiles", s.createFargateProfile)
	r.Delete("/clusters/{name}/fargate-profiles/{fargateProfileName}", s.deleteFargateProfile)
	// NOTE: /tags routes are NOT registered here — see TagsRouter. The /tags
	// path space is shared with Pipes and API Gateway, so the main router
	// owns it and dispatches by the resource ARN's service prefix.
	r.Post("/clusters/{name}/addons", s.createAddon)
	r.Get("/clusters/{name}/addons", s.listAddons)
	r.Get("/clusters/{name}/addons/{addonName}", s.describeAddon)
	r.Post("/clusters/{name}/addons/{addonName}/update", s.updateAddon)
	r.Delete("/clusters/{name}/addons/{addonName}", s.deleteAddon)
	// Both add-on catalog operations bind a fixed path and take the add-on name
	// as an httpQuery member, so there is no label to read here.
	r.Get("/addons/supported-versions", s.describeAddonVersions)
	r.Get("/addons/configuration-schemas", s.describeAddonConfiguration)
}

// PathPrefixes satisfies router.PathPrefixService with the four root paths EKS
// owns. They matter only to router tests that register a subset of services:
// without them an excluded EKS would leave these paths to S3's wildcard and the
// test would see a fallthrough no real run produces. They are deliberately not
// added to detectService — see the note there on why claiming a root segment
// costs a legal S3 bucket name, and
// internal/middleware/detectservice_routes_test.go, which records EKS as
// classified from the SigV4 credential scope instead.
func (s *Service) PathPrefixes() []string {
	return []string{"/clusters", "/addons", "/access-policies", "/cluster-versions"}
}

// TagsRouter returns a chi.Router for the EKS tagging routes that live under
// the shared /tags/{resourceArn} path space. The main router mounts it
// behind an ARN-dispatching owner shared with Pipes and API Gateway. EKS
// clients always URL-escape the ARN into a single path segment, which is
// what the {resourceArn} pattern matches.
func (s *Service) TagsRouter() chi.Router {
	r := chi.NewRouter()
	r.Get("/{resourceArn}", s.listTagsForResource)
	r.Post("/{resourceArn}", s.tagResource)
	r.Delete("/{resourceArn}", s.untagResource)
	return r
}

func (s *Service) region(r *http.Request) string {
	return middleware.RegionFromContext(r.Context(), s.cfg.Region)
}

func (s *Service) accountID() string {
	if s.cfg != nil && strings.TrimSpace(s.cfg.AccountID) != "" {
		return s.cfg.AccountID
	}
	return "000000000000"
}

func (s *Service) liveModeEnabled() bool {
	return s.cfg != nil && s.cfg.EKSMode == config.EKSModeLive
}

// liveModeNotImplementedError is the mixed-mode refusal: a cluster created in
// mock mode is not readable while OVERCAST_EKS_MODE=live.
func liveModeNotImplementedError() *protocol.AWSError {
	return &protocol.AWSError{
		Code:       protocol.ErrNotImplemented.Code,
		Message:    "Operation is unavailable for clusters created in mock mode while OVERCAST_EKS_MODE=live.",
		HTTPStatus: http.StatusNotImplemented,
	}
}

// invalidParameter builds EKS's modeled 400. Every operation in this package
// binds InvalidParameterException; none binds AWS's generic MissingParameter,
// so a missing required member is reported as this rather than through
// serviceutil.RequireString.
func invalidParameter(message string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "InvalidParameterException",
		Message:    message,
		HTTPStatus: http.StatusBadRequest,
	}
}

// missingRequiredMember names a member the model marks @required.
func missingRequiredMember(member string) *protocol.AWSError {
	return invalidParameter(member + " is required")
}

// invalidNextToken is the answer to a continuation token that does not decode.
// Treating one as "start again from page one" hands the caller the whole item
// set a second time, which SDK pagination reads as a legitimate page.
func invalidNextToken() *protocol.AWSError {
	return invalidParameter("The specified nextToken is not valid.")
}

func resourceNotFound(message string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ResourceNotFoundException",
		Message:    message,
		HTTPStatus: http.StatusNotFound,
	}
}

// writeResult writes a handler core's result: the modeled error if there is
// one, otherwise the response document with a 200.
//
// The REST handlers for the operations #858 re-bound decode the request from
// its HTTP binding and then call the same core the typed dispatch path calls,
// ending here. Every other operation in this package still has two
// implementations of its logic — the handler and the *Typed function — which is
// how updateAddonTyped came to be missing validation the REST handler had.
func writeResult[T any](w http.ResponseWriter, r *http.Request, out *T, aerr *protocol.AWSError) {
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, out)
}

func (s *Service) writeLiveModeRequiresDocker(w http.ResponseWriter, r *http.Request) {
	protocol.WriteJSONError(w, r, &protocol.AWSError{
		Code:       "ServiceUnavailableException",
		Message:    "EKS live mode requires Docker. Configure EKS_DOCKER_SOCKET and ensure the daemon is reachable.",
		HTTPStatus: http.StatusServiceUnavailable,
	})
}

func (s *Service) isMockModeClusterRecord(cluster *Cluster) bool {
	if cluster == nil {
		return false
	}
	return strings.Contains(cluster.Endpoint, ".mock.eks.local")
}

func (s *Service) requireAccessibleCluster(w http.ResponseWriter, r *http.Request, region, name string) (*Cluster, bool) {
	cluster, aerr := s.accessibleCluster(r.Context(), region, name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return nil, false
	}
	return cluster, true
}

// accessibleCluster loads a cluster and applies the mixed-mode rule: a record
// created in mock mode is not readable while OVERCAST_EKS_MODE=live. Handler
// cores shared between the REST and typed paths call this rather than
// validateCluster, so the rule holds on both.
func (s *Service) accessibleCluster(ctx context.Context, region, name string) (*Cluster, *protocol.AWSError) {
	cluster, found, err := s.getCluster(ctx, region, name)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, resourceNotFound("No cluster found for name: " + name)
	}
	if s.liveModeEnabled() && s.isMockModeClusterRecord(cluster) {
		return nil, liveModeNotImplementedError()
	}
	return cluster, nil
}

// k3sImageForVersion maps a Kubernetes minor version string (e.g. "1.31") to a
// k3s Docker image tag. Patch version is pinned to .3 per current LTS cycles.
