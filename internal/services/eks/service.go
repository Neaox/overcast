// Package eks provides Amazon EKS control-plane emulation.
//
// Implemented operations (REST JSON):
//   - POST   /clusters
//   - GET    /clusters
//   - GET    /clusters/{name}
//   - POST   /clusters/{name}/updates
//   - POST   /clusters/{name}/kubeconfig
//   - DELETE /clusters/{name}
//   - POST   /clusters/{name}/node-groups
//   - GET    /clusters/{name}/node-groups
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
	"github.com/Neaox/overcast/internal/docker"
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
	dockerReady atomic.Bool

	liveMu       sync.Mutex
	liveRuntimes map[string]*liveClusterRuntime
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
	}
	s.typedOp = s.typedOps()
	return s
}

func (s *Service) Name() string { return serviceName }

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

// SetDocker wires the Docker client used for future live-mode control-plane runtime work.
func (s *Service) SetDocker(dc *docker.Client) {
	s.docker = dc
	s.dockerReady.Store(dc != nil)
}

// Stop satisfies router.Stopper. Live-mode cleanup is best-effort for both the
// in-memory runtime registry and any persisted live clusters that need runtime
// reconciliation after a process restart.
func (s *Service) Stop(ctx context.Context) {
	s.reconcilePersistedLiveClusterRuntimes(ctx)
	for _, runtime := range s.drainLiveClusterRuntimes() {
		if err := s.cleanupLiveClusterRuntime(ctx, runtime); err != nil {
			s.log.Warn("failed to clean up EKS live runtime during stop", zap.Error(err))
		}
	}
}

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
	r.Get("/access-policies/{name}", s.describeAccessPolicy)
	r.Get("/cluster-versions", s.describeClusterVersions)
	r.Get("/clusters/{name}/identity-provider-configs", s.listIdentityProviderConfigs)
	r.Get("/clusters/{name}/identity-provider-configs/{configType}/{configName}", s.describeIdentityProviderConfig)
	r.Post("/clusters/{name}/identity-provider-configs/{configType}/{configName}/update", s.updateIdentityProviderConfig)
	r.Post("/clusters/{name}/identity-provider-configs/associate", s.associateIdentityProviderConfig)
	r.Post("/clusters/{name}/identity-provider-configs/disassociate", s.disassociateIdentityProviderConfig)
	r.Post("/clusters/{name}/pod-identity-associations", s.createPodIdentityAssociation)
	r.Get("/clusters/{name}/pod-identity-associations", s.listPodIdentityAssociations)
	r.Get("/clusters/{name}/pod-identity-associations/{associationId}", s.describePodIdentityAssociation)
	r.Post("/clusters/{name}/pod-identity-associations/{associationId}", s.updatePodIdentityAssociation)
	r.Delete("/clusters/{name}/pod-identity-associations/{associationId}", s.deletePodIdentityAssociation)
	r.Get("/clusters/{name}/updates", s.listUpdates)
	r.Post("/clusters/{name}/updates", s.updateClusterVersion)
	r.Get("/clusters/{name}/insights", s.listInsights)
	r.Get("/clusters/{name}/insights/{insightId}", s.describeInsight)
	r.Post("/clusters/{name}/update-config", s.updateClusterConfig)
	r.Get("/clusters/{name}/updates/{updateId}", s.describeUpdate)
	r.Post("/clusters/{name}/kubeconfig", s.updateKubeconfig)
	r.Delete("/clusters/{name}", s.deleteCluster)
	r.Post("/clusters/{name}/node-groups", s.createNodegroup)
	r.Post("/clusters/{name}/node-groups/{nodegroupName}/updates", s.updateNodegroupVersion)
	r.Post("/clusters/{name}/node-groups/{nodegroupName}/update-config", s.updateNodegroupConfig)
	r.Get("/clusters/{name}/node-groups", s.listNodegroups)
	r.Get("/clusters/{name}/node-groups/{nodegroupName}", s.describeNodegroup)
	r.Delete("/clusters/{name}/node-groups/{nodegroupName}", s.deleteNodegroup)
	r.Get("/clusters/{name}/fargate-profiles", s.listFargateProfiles)
	r.Get("/clusters/{name}/fargate-profiles/{fargateProfileName}", s.describeFargateProfile)
	r.Post("/clusters/{name}/fargate-profiles", s.createFargateProfile)
	r.Delete("/clusters/{name}/fargate-profiles/{fargateProfileName}", s.deleteFargateProfile)
	r.Get("/tags/{resourceArn}", s.listTagsForResource)
	r.Post("/tags/{resourceArn}", s.tagResource)
	r.Delete("/tags/{resourceArn}", s.untagResource)
	r.Post("/clusters/{name}/addons", s.createAddon)
	r.Get("/clusters/{name}/addons", s.listAddons)
	r.Get("/clusters/{name}/addons/{addonName}", s.describeAddon)
	r.Post("/clusters/{name}/addons/{addonName}/updates", s.updateAddon)
	r.Delete("/clusters/{name}/addons/{addonName}", s.deleteAddon)
	r.Get("/addons/{addonName}/versions", s.describeAddonVersions)
	r.Get("/addons/{addonName}/configuration", s.describeAddonConfiguration)
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

func (s *Service) writeLiveModeNotImplemented(w http.ResponseWriter, r *http.Request) {
	protocol.WriteJSONError(w, r, &protocol.AWSError{
		Code:       protocol.ErrNotImplemented.Code,
		Message:    "Operation is unavailable for clusters created in mock mode while OVERCAST_EKS_MODE=live.",
		HTTPStatus: http.StatusNotImplemented,
	})
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
	cluster, found, err := s.getCluster(r.Context(), region, name)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return nil, false
	}
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "No cluster found for name: " + name,
			HTTPStatus: http.StatusNotFound,
		})
		return nil, false
	}
	if s.liveModeEnabled() && s.isMockModeClusterRecord(cluster) {
		s.writeLiveModeNotImplemented(w, r)
		return nil, false
	}
	return cluster, true
}

// k3sImageForVersion maps a Kubernetes minor version string (e.g. "1.31") to a
// k3s Docker image tag. Patch version is pinned to .3 per current LTS cycles.
