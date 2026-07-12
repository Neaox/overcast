package eks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// ─── Cluster ──────────────────────────────────────────────────────────────────

type createClusterRequest struct {
	Name                    string            `json:"name" cbor:"name"`
	RoleArn                 string            `json:"roleArn" cbor:"roleArn"`
	Version                 string            `json:"version" cbor:"version"`
	ResourcesVPCConfig      map[string]any    `json:"resourcesVpcConfig" cbor:"resourcesVpcConfig"`
	KubernetesNetworkConfig map[string]any    `json:"kubernetesNetworkConfig" cbor:"kubernetesNetworkConfig"`
	EncryptionConfig        []map[string]any  `json:"encryptionConfig" cbor:"encryptionConfig"`
	Tags                    map[string]string `json:"tags" cbor:"tags"`
}

type createClusterResponse struct {
	Cluster *Cluster `json:"cluster" cbor:"cluster"`
}

func (s *Service) createClusterTyped(ctx context.Context, req *createClusterRequest) (*createClusterResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, found, err := s.getCluster(ctx, region, req.Name); err != nil {
		return nil, protocol.ErrInternalError
	} else if found {
		return nil, &protocol.AWSError{Code: "ResourceInUseException", Message: fmt.Sprintf("Cluster %q already exists", req.Name), HTTPStatus: 409}
	}
	version := req.Version
	if version == "" {
		version = "1.31"
	}
	cluster := &Cluster{
		Name:                    req.Name,
		Arn:                     s.clusterARN(region, req.Name),
		CreatedAt:               s.clk.Now(),
		Version:                 version,
		RoleArn:                 req.RoleArn,
		ResourcesVPCConfig:      req.ResourcesVPCConfig,
		KubernetesNetworkConfig: req.KubernetesNetworkConfig,
		EncryptionConfig:        req.EncryptionConfig,
		Endpoint:                fmt.Sprintf("https://%s.mock.eks.local", req.Name),
		Status:                  "ACTIVE",
		CertificateAuthority:    map[string]any{"data": "LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0t"},
	}
	if err := s.putCluster(ctx, region, cluster); err != nil {
		return nil, protocol.ErrInternalError
	}
	_ = s.putInlineTags(ctx, cluster.Arn, req.Tags)
	cluster.Tags = s.readTagsForARN(ctx, cluster.Arn)
	return &createClusterResponse{Cluster: cluster}, nil
}

type describeClusterRequest struct {
	Name string `json:"name" cbor:"name"`
}

type describeClusterResponse struct {
	Cluster *Cluster `json:"cluster" cbor:"cluster"`
}

func (s *Service) describeClusterTyped(ctx context.Context, req *describeClusterRequest) (*describeClusterResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	cluster, found, err := s.getCluster(ctx, region, req.Name)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: "No cluster found for name: " + req.Name, HTTPStatus: 404}
	}
	cluster.Tags = s.readTagsForARN(ctx, cluster.Arn)
	return &describeClusterResponse{Cluster: cluster}, nil
}

type listClustersRequest struct{}

type listClustersResponse struct {
	Clusters []string `json:"clusters" cbor:"clusters"`
}

func (s *Service) listClustersTyped(ctx context.Context, _ *listClustersRequest) (*listClustersResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	pairs, err := s.store.Scan(ctx, nsClusters, serviceutil.RegionKey(region, ""))
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	clusters := make([]string, 0, len(pairs))
	for _, kv := range pairs {
		var c Cluster
		if json.Unmarshal([]byte(kv.Value), &c) != nil {
			continue
		}
		clusters = append(clusters, c.Name)
	}
	sort.Strings(clusters)
	return &listClustersResponse{Clusters: clusters}, nil
}

type deleteClusterRequest struct {
	Name string `json:"name" cbor:"name"`
}

type deleteClusterResponse struct {
	Cluster *Cluster `json:"cluster" cbor:"cluster"`
}

func (s *Service) deleteClusterTyped(ctx context.Context, req *deleteClusterRequest) (*deleteClusterResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	cluster, found, err := s.getCluster(ctx, region, req.Name)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("No cluster found for name: %s", req.Name), HTTPStatus: 404}
	}
	if err := s.store.Delete(ctx, nsClusters, clusterKey(region, req.Name)); err != nil {
		return nil, protocol.ErrInternalError
	}
	clusterPrefix := serviceutil.RegionKey(region, req.Name+"/")
	for _, ns := range []string{nsNodegroups, nsUpdates, nsFargate, nsAddons, nsIDPConfigs, nsAccess, nsAccessPol, nsPodIDAssoc} {
		_ = s.deleteNamespaceByPrefix(ctx, ns, clusterPrefix)
	}
	_ = s.store.Delete(ctx, nsTags, tagKey(cluster.Arn))
	cluster.Status = "DELETING"
	return &deleteClusterResponse{Cluster: cluster}, nil
}

// ─── Cluster Version ──────────────────────────────────────────────────────────

type updateClusterVersionRequest struct {
	ClusterName string `json:"name" cbor:"name"`
	Version     string `json:"version" cbor:"version"`
}

type updateClusterVersionResponse struct {
	Update *Update `json:"update" cbor:"update"`
}

func (s *Service) updateClusterVersionTyped(ctx context.Context, req *updateClusterVersionRequest) (*updateClusterVersionResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	cluster, aerr := s.validateCluster(ctx, region, req.ClusterName)
	if aerr != nil {
		return nil, aerr
	}
	cluster.Version = req.Version
	if err := s.putCluster(ctx, region, cluster); err != nil {
		return nil, protocol.ErrInternalError
	}
	update := &Update{
		ID:     fmt.Sprintf("upd-%s-%d", req.ClusterName, s.clk.Now().UnixNano()),
		Status: "Successful", Type: "VersionUpdate", CreatedAt: s.clk.Now(),
		Params: []map[string]any{{"type": "Version", "value": req.Version}},
	}
	_ = s.putUpdate(ctx, region, req.ClusterName, update)
	return &updateClusterVersionResponse{Update: update}, nil
}

type describeClusterVersionsRequest struct{}

type describeClusterVersionsResponse struct {
	ClusterVersions []map[string]any `json:"clusterVersions" cbor:"clusterVersions"`
}

func (s *Service) describeClusterVersionsTyped(ctx context.Context, _ *describeClusterVersionsRequest) (*describeClusterVersionsResponse, *protocol.AWSError) {
	return &describeClusterVersionsResponse{
		ClusterVersions: []map[string]any{
			{"clusterVersion": "1.27", "defaultVersion": false, "status": "STANDARD_SUPPORT"},
			{"clusterVersion": "1.28", "defaultVersion": false, "status": "STANDARD_SUPPORT"},
			{"clusterVersion": "1.29", "defaultVersion": true, "status": "STANDARD_SUPPORT"},
		},
	}, nil
}

// ─── Updates ──────────────────────────────────────────────────────────────────

type listUpdatesRequest struct {
	ClusterName string `json:"name" cbor:"name"`
}

type listUpdatesResponse struct {
	UpdateIds []string `json:"updateIds" cbor:"updateIds"`
}

func (s *Service) listUpdatesTyped(ctx context.Context, req *listUpdatesRequest) (*listUpdatesResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	pairs, err := s.store.Scan(ctx, nsUpdates, serviceutil.RegionKey(region, req.ClusterName+"/"))
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	ids := make([]string, 0, len(pairs))
	for _, kv := range pairs {
		var u Update
		if json.Unmarshal([]byte(kv.Value), &u) != nil {
			continue
		}
		if u.ID != "" {
			ids = append(ids, u.ID)
		}
	}
	sort.Strings(ids)
	return &listUpdatesResponse{UpdateIds: ids}, nil
}

type describeUpdateRequest struct {
	ClusterName string `json:"name" cbor:"name"`
	UpdateId    string `json:"updateId" cbor:"updateId"`
}

type describeUpdateResponse struct {
	Update *Update `json:"update" cbor:"update"`
}

func (s *Service) describeUpdateTyped(ctx context.Context, req *describeUpdateRequest) (*describeUpdateResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	update, found, err := s.getUpdate(ctx, region, req.ClusterName, req.UpdateId)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("No update found for id: %s", req.UpdateId), HTTPStatus: 404}
	}
	return &describeUpdateResponse{Update: update}, nil
}

// ─── Insights ─────────────────────────────────────────────────────────────────

type listInsightsRequest struct {
	ClusterName string `json:"name" cbor:"name"`
}

type listInsightsResponse struct {
	Insights []map[string]any `json:"insights" cbor:"insights"`
}

func (s *Service) listInsightsTyped(ctx context.Context, req *listInsightsRequest) (*listInsightsResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	return &listInsightsResponse{Insights: syntheticClusterInsights(req.ClusterName)}, nil
}

type describeInsightRequest struct {
	ClusterName string `json:"name" cbor:"name"`
	InsightId   string `json:"insightId" cbor:"insightId"`
}

type describeInsightResponse struct {
	Insight map[string]any `json:"insight" cbor:"insight"`
}

func (s *Service) describeInsightTyped(ctx context.Context, req *describeInsightRequest) (*describeInsightResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	for _, insight := range syntheticClusterInsights(req.ClusterName) {
		if insight["id"] == req.InsightId {
			return &describeInsightResponse{Insight: insight}, nil
		}
	}
	return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("No insight found for id: %s", req.InsightId), HTTPStatus: 404}
}

// ─── Config ───────────────────────────────────────────────────────────────────

type updateClusterConfigRequest struct {
	ClusterName             string         `json:"name" cbor:"name"`
	Logging                 map[string]any `json:"logging" cbor:"logging"`
	ResourcesVPCConfig      map[string]any `json:"resourcesVpcConfig" cbor:"resourcesVpcConfig"`
	KubernetesNetworkConfig map[string]any `json:"kubernetesNetworkConfig" cbor:"kubernetesNetworkConfig"`
}

type updateClusterConfigResponse struct {
	Update *Update `json:"update" cbor:"update"`
}

func (s *Service) updateClusterConfigTyped(ctx context.Context, req *updateClusterConfigRequest) (*updateClusterConfigResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	cluster, aerr := s.validateCluster(ctx, region, req.ClusterName)
	if aerr != nil {
		return nil, aerr
	}
	updateType := "ConfigUpdate"
	var params []map[string]any
	if req.Logging != nil {
		cluster.Logging = req.Logging
		updateType = "LoggingUpdate"
		params = append(params, map[string]any{"type": "ClusterLogging", "value": "updated"})
	}
	if req.ResourcesVPCConfig != nil {
		cluster.ResourcesVPCConfig = req.ResourcesVPCConfig
		if updateType == "ConfigUpdate" {
			updateType = "VpcConfigUpdate"
		}
		params = append(params, map[string]any{"type": "VpcConfig", "value": "updated"})
	}
	if req.KubernetesNetworkConfig != nil {
		cluster.KubernetesNetworkConfig = req.KubernetesNetworkConfig
		params = append(params, map[string]any{"type": "KubernetesNetworkConfig", "value": "updated"})
	}
	if len(params) == 0 {
		return nil, &protocol.AWSError{Code: "InvalidParameterException", Message: "No configuration changes requested", HTTPStatus: 400}
	}
	if err := s.putCluster(ctx, region, cluster); err != nil {
		return nil, protocol.ErrInternalError
	}
	update := &Update{
		ID:     fmt.Sprintf("upd-%s-%d", req.ClusterName, s.clk.Now().UnixNano()),
		Status: "Successful", Type: updateType, CreatedAt: s.clk.Now(), Params: params,
	}
	_ = s.putUpdate(ctx, region, req.ClusterName, update)
	return &updateClusterConfigResponse{Update: update}, nil
}

// ─── Kubeconfig ───────────────────────────────────────────────────────────────

type updateKubeconfigRequest struct {
	ClusterName string `json:"name" cbor:"name"`
}

type updateKubeconfigResponse struct {
	Kubeconfig string `json:"kubeconfig" cbor:"kubeconfig"`
}

func (s *Service) updateKubeconfigTyped(ctx context.Context, req *updateKubeconfigRequest) (*updateKubeconfigResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	cluster, aerr := s.validateCluster(ctx, region, req.ClusterName)
	if aerr != nil {
		return nil, aerr
	}
	caData, _ := cluster.CertificateAuthority["data"].(string)
	kubeconfig := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: %s
  cluster:
    server: %s
    certificate-authority-data: %s
contexts:
- name: %s
  context:
    cluster: %s
    user: %s
current-context: %s
users:
- name: %s
  user:
    token: overcast-dev-token
`, cluster.Name, cluster.Endpoint, caData, cluster.Name, cluster.Name, cluster.Name, cluster.Name, cluster.Name)
	return &updateKubeconfigResponse{Kubeconfig: kubeconfig}, nil
}

// ─── Nodegroup ────────────────────────────────────────────────────────────────

type createNodegroupRequest struct {
	ClusterName    string            `json:"name" cbor:"name"`
	NodegroupName  string            `json:"nodegroupName" cbor:"nodegroupName"`
	NodeRole       string            `json:"nodeRole" cbor:"nodeRole"`
	Version        string            `json:"version" cbor:"version"`
	ReleaseVersion string            `json:"releaseVersion" cbor:"releaseVersion"`
	Subnets        []string          `json:"subnets" cbor:"subnets"`
	InstanceTypes  []string          `json:"instanceTypes" cbor:"instanceTypes"`
	AmiType        string            `json:"amiType" cbor:"amiType"`
	CapacityType   string            `json:"capacityType" cbor:"capacityType"`
	DiskSize       int               `json:"diskSize" cbor:"diskSize"`
	Labels         map[string]string `json:"labels" cbor:"labels"`
	Taints         []map[string]any  `json:"taints" cbor:"taints"`
	ScalingConfig  map[string]any    `json:"scalingConfig" cbor:"scalingConfig"`
	UpdateConfig   map[string]any    `json:"updateConfig" cbor:"updateConfig"`
	LaunchTemplate map[string]any    `json:"launchTemplate" cbor:"launchTemplate"`
	Tags           map[string]string `json:"tags" cbor:"tags"`
}

type createNodegroupResponse struct {
	Nodegroup *Nodegroup `json:"nodegroup" cbor:"nodegroup"`
}

func (s *Service) createNodegroupTyped(ctx context.Context, req *createNodegroupRequest) (*createNodegroupResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	key := nodegroupKey(region, req.ClusterName, req.NodegroupName)
	if _, found, err := s.store.Get(ctx, nsNodegroups, key); err != nil {
		return nil, protocol.ErrInternalError
	} else if found {
		return nil, &protocol.AWSError{Code: "ResourceInUseException", Message: fmt.Sprintf("Nodegroup %q already exists", req.NodegroupName), HTTPStatus: 409}
	}
	ng := &Nodegroup{
		ClusterName: req.ClusterName, NodegroupName: req.NodegroupName,
		NodegroupArn: s.nodegroupARN(region, req.ClusterName, req.NodegroupName),
		CreatedAt:    s.clk.Now(), Status: "ACTIVE", Version: req.Version,
		ReleaseVersion: req.ReleaseVersion, NodeRole: req.NodeRole,
		Subnets: req.Subnets, InstanceTypes: req.InstanceTypes,
		AmiType: req.AmiType, CapacityType: req.CapacityType, DiskSize: req.DiskSize,
		Labels: req.Labels, Taints: req.Taints, ScalingConfig: req.ScalingConfig,
		UpdateConfig: req.UpdateConfig, LaunchTemplate: req.LaunchTemplate,
	}
	if err := s.putNodegroup(ctx, region, ng); err != nil {
		return nil, protocol.ErrInternalError
	}
	_ = s.putInlineTags(ctx, ng.NodegroupArn, req.Tags)
	ng.Tags = s.readTagsForARN(ctx, ng.NodegroupArn)
	return &createNodegroupResponse{Nodegroup: ng}, nil
}

type listNodegroupsRequest struct {
	ClusterName string `json:"name" cbor:"name"`
}

type listNodegroupsResponse struct {
	Nodegroups []string `json:"nodegroups" cbor:"nodegroups"`
}

func (s *Service) listNodegroupsTyped(ctx context.Context, req *listNodegroupsRequest) (*listNodegroupsResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	nodegroups, err := s.listNodegroupsForCluster(ctx, region, req.ClusterName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	names := make([]string, 0, len(nodegroups))
	for _, ng := range nodegroups {
		names = append(names, ng.NodegroupName)
	}
	sort.Strings(names)
	return &listNodegroupsResponse{Nodegroups: names}, nil
}

type describeNodegroupRequest struct {
	ClusterName   string `json:"name" cbor:"name"`
	NodegroupName string `json:"nodegroupName" cbor:"nodegroupName"`
}

type describeNodegroupResponse struct {
	Nodegroup *Nodegroup `json:"nodegroup" cbor:"nodegroup"`
}

func (s *Service) describeNodegroupTyped(ctx context.Context, req *describeNodegroupRequest) (*describeNodegroupResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	nodegroup, found, err := s.getNodegroup(ctx, region, req.ClusterName, req.NodegroupName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("No nodegroup found for name: %s", req.NodegroupName), HTTPStatus: 404}
	}
	nodegroup.Tags = s.readTagsForARN(ctx, nodegroup.NodegroupArn)
	return &describeNodegroupResponse{Nodegroup: nodegroup}, nil
}

type deleteNodegroupRequest struct {
	ClusterName   string `json:"name" cbor:"name"`
	NodegroupName string `json:"nodegroupName" cbor:"nodegroupName"`
}

type deleteNodegroupResponse struct {
	Nodegroup *Nodegroup `json:"nodegroup" cbor:"nodegroup"`
}

func (s *Service) deleteNodegroupTyped(ctx context.Context, req *deleteNodegroupRequest) (*deleteNodegroupResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	ng, found, err := s.getNodegroup(ctx, region, req.ClusterName, req.NodegroupName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("No nodegroup found for name: %s", req.NodegroupName), HTTPStatus: 404}
	}
	_ = s.store.Delete(ctx, nsNodegroups, nodegroupKey(region, req.ClusterName, req.NodegroupName))
	_ = s.store.Delete(ctx, nsTags, tagKey(ng.NodegroupArn))
	ng.Status = "DELETING"
	return &deleteNodegroupResponse{Nodegroup: ng}, nil
}

type updateNodegroupVersionRequest struct {
	ClusterName   string `json:"name" cbor:"name"`
	NodegroupName string `json:"nodegroupName" cbor:"nodegroupName"`
	Version       string `json:"version" cbor:"version"`
}

type updateNodegroupVersionResponse struct {
	Update *Update `json:"update" cbor:"update"`
}

func (s *Service) updateNodegroupVersionTyped(ctx context.Context, req *updateNodegroupVersionRequest) (*updateNodegroupVersionResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	nodegroup, found, err := s.getNodegroup(ctx, region, req.ClusterName, req.NodegroupName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("No nodegroup found for name: %s", req.NodegroupName), HTTPStatus: 404}
	}
	nodegroup.Version = req.Version
	if err := s.putNodegroup(ctx, region, nodegroup); err != nil {
		return nil, protocol.ErrInternalError
	}
	update := &Update{
		ID:     fmt.Sprintf("upd-ng-%s-%d", req.NodegroupName, s.clk.Now().UnixNano()),
		Status: "Successful", Type: "VersionUpdate", CreatedAt: s.clk.Now(),
		Params: []map[string]any{
			{"type": "Version", "value": req.Version},
			{"type": "NodegroupName", "value": req.NodegroupName},
		},
	}
	_ = s.putUpdate(ctx, region, req.ClusterName, update)
	return &updateNodegroupVersionResponse{Update: update}, nil
}

type updateNodegroupConfigRequest struct {
	ClusterName   string            `json:"name" cbor:"name"`
	NodegroupName string            `json:"nodegroupName" cbor:"nodegroupName"`
	Labels        map[string]string `json:"labels" cbor:"labels"`
	Taints        []map[string]any  `json:"taints" cbor:"taints"`
	ScalingConfig map[string]any    `json:"scalingConfig" cbor:"scalingConfig"`
	UpdateConfig  map[string]any    `json:"updateConfig" cbor:"updateConfig"`
}

type updateNodegroupConfigResponse struct {
	Update *Update `json:"update" cbor:"update"`
}

func (s *Service) updateNodegroupConfigTyped(ctx context.Context, req *updateNodegroupConfigRequest) (*updateNodegroupConfigResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	nodegroup, found, err := s.getNodegroup(ctx, region, req.ClusterName, req.NodegroupName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("No nodegroup found for name: %s", req.NodegroupName), HTTPStatus: 404}
	}
	params := make([]map[string]any, 0, 5)
	if req.Labels != nil {
		nodegroup.Labels = req.Labels
		params = append(params, map[string]any{"type": "Labels", "value": "updated"})
	}
	if req.Taints != nil {
		nodegroup.Taints = req.Taints
		params = append(params, map[string]any{"type": "Taints", "value": "updated"})
	}
	if req.ScalingConfig != nil {
		nodegroup.ScalingConfig = req.ScalingConfig
		params = append(params, map[string]any{"type": "ScalingConfig", "value": "updated"})
	}
	if req.UpdateConfig != nil {
		nodegroup.UpdateConfig = req.UpdateConfig
		params = append(params, map[string]any{"type": "UpdateConfig", "value": "updated"})
	}
	params = append(params, map[string]any{"type": "NodegroupName", "value": req.NodegroupName})
	if err := s.putNodegroup(ctx, region, nodegroup); err != nil {
		return nil, protocol.ErrInternalError
	}
	update := &Update{
		ID:     fmt.Sprintf("upd-ngcfg-%s-%d", req.NodegroupName, s.clk.Now().UnixNano()),
		Status: "Successful", Type: "ConfigUpdate", CreatedAt: s.clk.Now(), Params: params,
	}
	_ = s.putUpdate(ctx, region, req.ClusterName, update)
	return &updateNodegroupConfigResponse{Update: update}, nil
}

// ─── Fargate ──────────────────────────────────────────────────────────────────

type listFargateProfilesRequest struct {
	ClusterName string `json:"name" cbor:"name"`
}

type listFargateProfilesResponse struct {
	FargateProfileNames []string `json:"fargateProfileNames" cbor:"fargateProfileNames"`
}

func (s *Service) listFargateProfilesTyped(ctx context.Context, req *listFargateProfilesRequest) (*listFargateProfilesResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	profiles, err := s.listFargateProfilesForCluster(ctx, region, req.ClusterName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	names := make([]string, 0, len(profiles)+1)
	for _, fp := range profiles {
		names = append(names, fp.FargateProfileName)
	}
	hasDefault := false
	for _, n := range names {
		if n == "default" {
			hasDefault = true
			break
		}
	}
	if !hasDefault {
		names = append(names, "default")
	}
	sort.Strings(names)
	return &listFargateProfilesResponse{FargateProfileNames: names}, nil
}

type describeFargateProfileRequest struct {
	ClusterName        string `json:"name" cbor:"name"`
	FargateProfileName string `json:"fargateProfileName" cbor:"fargateProfileName"`
}

type describeFargateProfileResponse struct {
	FargateProfile *FargateProfile `json:"fargateProfile" cbor:"fargateProfile"`
}

func (s *Service) describeFargateProfileTyped(ctx context.Context, req *describeFargateProfileRequest) (*describeFargateProfileResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	cluster, aerr := s.validateCluster(ctx, region, req.ClusterName)
	if aerr != nil {
		return nil, aerr
	}
	fp, found, err := s.getFargateProfile(ctx, region, req.ClusterName, req.FargateProfileName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if found {
		fp.Tags = s.readTagsForARN(ctx, fp.FargateProfileArn)
		return &describeFargateProfileResponse{FargateProfile: fp}, nil
	}
	if req.FargateProfileName != "default" {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("No Fargate profile found for name: %s", req.FargateProfileName), HTTPStatus: 404}
	}
	profile := &FargateProfile{
		ClusterName: req.ClusterName, FargateProfileName: req.FargateProfileName,
		FargateProfileArn: s.fargateProfileARN(region, req.ClusterName, req.FargateProfileName),
		CreatedAt:         cluster.CreatedAt, Status: "ACTIVE",
		PodExecutionRoleArn: fmt.Sprintf("arn:aws:iam::%s:role/eks-fargate-pod-execution-role", s.accountID()),
		Selectors:           []map[string]any{{"namespace": "default"}},
	}
	return &describeFargateProfileResponse{FargateProfile: profile}, nil
}

type createFargateProfileRequest struct {
	ClusterName         string            `json:"name" cbor:"name"`
	FargateProfileName  string            `json:"fargateProfileName" cbor:"fargateProfileName"`
	PodExecutionRoleArn string            `json:"podExecutionRoleArn" cbor:"podExecutionRoleArn"`
	Subnets             []string          `json:"subnets" cbor:"subnets"`
	Selectors           []map[string]any  `json:"selectors" cbor:"selectors"`
	Tags                map[string]string `json:"tags" cbor:"tags"`
}

type createFargateProfileResponse struct {
	FargateProfile *FargateProfile `json:"fargateProfile" cbor:"fargateProfile"`
}

func (s *Service) createFargateProfileTyped(ctx context.Context, req *createFargateProfileRequest) (*createFargateProfileResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	fp := &FargateProfile{
		ClusterName: req.ClusterName, FargateProfileName: req.FargateProfileName,
		FargateProfileArn:   s.fargateProfileARN(region, req.ClusterName, req.FargateProfileName),
		CreatedAt:           s.clk.Now(),
		Status:              "ACTIVE",
		PodExecutionRoleArn: req.PodExecutionRoleArn,
		Subnets:             req.Subnets,
		Selectors:           req.Selectors,
	}
	if err := s.putFargateProfile(ctx, region, fp); err != nil {
		return nil, protocol.ErrInternalError
	}
	_ = s.putInlineTags(ctx, fp.FargateProfileArn, req.Tags)
	fp.Tags = s.readTagsForARN(ctx, fp.FargateProfileArn)
	return &createFargateProfileResponse{FargateProfile: fp}, nil
}

type deleteFargateProfileRequest struct {
	ClusterName        string `json:"name" cbor:"name"`
	FargateProfileName string `json:"fargateProfileName" cbor:"fargateProfileName"`
}

type deleteFargateProfileResponse struct {
	FargateProfile *FargateProfile `json:"fargateProfile" cbor:"fargateProfile"`
}

func (s *Service) deleteFargateProfileTyped(ctx context.Context, req *deleteFargateProfileRequest) (*deleteFargateProfileResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	fp, found, err := s.getFargateProfile(ctx, region, req.ClusterName, req.FargateProfileName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("No Fargate profile found for name: %s", req.FargateProfileName), HTTPStatus: 404}
	}
	_ = s.store.Delete(ctx, nsFargate, fargateProfileKey(region, req.ClusterName, req.FargateProfileName))
	_ = s.store.Delete(ctx, nsTags, tagKey(fp.FargateProfileArn))
	fp.Status = "DELETING"
	return &deleteFargateProfileResponse{FargateProfile: fp}, nil
}

// ─── Addons ───────────────────────────────────────────────────────────────────

type createAddonRequest struct {
	ClusterName           string            `json:"name" cbor:"name"`
	AddonName             string            `json:"addonName" cbor:"addonName"`
	AddonVersion          string            `json:"addonVersion" cbor:"addonVersion"`
	ConfigurationValues   string            `json:"configurationValues" cbor:"configurationValues"`
	ServiceAccountRoleArn string            `json:"serviceAccountRoleArn" cbor:"serviceAccountRoleArn"`
	Tags                  map[string]string `json:"tags" cbor:"tags"`
}

type createAddonResponse struct {
	Addon *Addon `json:"addon" cbor:"addon"`
}

func (s *Service) createAddonTyped(ctx context.Context, req *createAddonRequest) (*createAddonResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	a := &Addon{
		ClusterName: req.ClusterName, AddonName: req.AddonName,
		AddonArn:     s.addonARN(region, req.ClusterName, req.AddonName),
		AddonVersion: req.AddonVersion, ConfigurationValues: req.ConfigurationValues,
		ServiceAccountRoleArn: req.ServiceAccountRoleArn,
		CreatedAt:             s.clk.Now(), Status: "ACTIVE",
	}
	if err := s.putAddon(ctx, region, a); err != nil {
		return nil, protocol.ErrInternalError
	}
	_ = s.putInlineTags(ctx, a.AddonArn, req.Tags)
	a.Tags = s.readTagsForARN(ctx, a.AddonArn)
	return &createAddonResponse{Addon: a}, nil
}

type listAddonsRequest struct {
	ClusterName string `json:"name" cbor:"name"`
}

type listAddonsResponse struct {
	Addons []string `json:"addons" cbor:"addons"`
}

func (s *Service) listAddonsTyped(ctx context.Context, req *listAddonsRequest) (*listAddonsResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	addons, err := s.listAddonsForCluster(ctx, region, req.ClusterName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	names := make([]string, 0, len(addons))
	for _, a := range addons {
		names = append(names, a.AddonName)
	}
	sort.Strings(names)
	return &listAddonsResponse{Addons: names}, nil
}

type describeAddonRequest struct {
	ClusterName string `json:"name" cbor:"name"`
	AddonName   string `json:"addonName" cbor:"addonName"`
}

type describeAddonResponse struct {
	Addon *Addon `json:"addon" cbor:"addon"`
}

func (s *Service) describeAddonTyped(ctx context.Context, req *describeAddonRequest) (*describeAddonResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	a, found, err := s.getAddon(ctx, region, req.ClusterName, req.AddonName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("No addon found for name: %s", req.AddonName), HTTPStatus: 404}
	}
	a.Tags = s.readTagsForARN(ctx, a.AddonArn)
	return &describeAddonResponse{Addon: a}, nil
}

type deleteAddonRequest struct {
	ClusterName string `json:"name" cbor:"name"`
	AddonName   string `json:"addonName" cbor:"addonName"`
}

type deleteAddonResponse struct {
	Addon *Addon `json:"addon" cbor:"addon"`
}

func (s *Service) deleteAddonTyped(ctx context.Context, req *deleteAddonRequest) (*deleteAddonResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	a, found, err := s.getAddon(ctx, region, req.ClusterName, req.AddonName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("No addon found for name: %s", req.AddonName), HTTPStatus: 404}
	}
	_ = s.store.Delete(ctx, nsAddons, addonKey(region, req.ClusterName, req.AddonName))
	_ = s.store.Delete(ctx, nsTags, tagKey(a.AddonArn))
	a.Status = "DELETING"
	return &deleteAddonResponse{Addon: a}, nil
}

type updateAddonRequest struct {
	ClusterName           string `json:"name" cbor:"name"`
	AddonName             string `json:"addonName" cbor:"addonName"`
	AddonVersion          string `json:"addonVersion" cbor:"addonVersion"`
	ConfigurationValues   string `json:"configurationValues" cbor:"configurationValues"`
	ServiceAccountRoleArn string `json:"serviceAccountRoleArn" cbor:"serviceAccountRoleArn"`
}

type updateAddonResponse struct {
	Update *Update `json:"update" cbor:"update"`
}

func (s *Service) updateAddonTyped(ctx context.Context, req *updateAddonRequest) (*updateAddonResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	a, found, err := s.getAddon(ctx, region, req.ClusterName, req.AddonName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("No addon found for name: %s", req.AddonName), HTTPStatus: 404}
	}
	params := make([]map[string]any, 0, 4)
	if req.AddonVersion != "" {
		a.AddonVersion = req.AddonVersion
		params = append(params, map[string]any{"type": "AddonVersion", "value": req.AddonVersion})
	}
	if req.ConfigurationValues != "" {
		a.ConfigurationValues = req.ConfigurationValues
		params = append(params, map[string]any{"type": "ConfigurationValues", "value": "updated"})
	}
	if req.ServiceAccountRoleArn != "" {
		a.ServiceAccountRoleArn = req.ServiceAccountRoleArn
		params = append(params, map[string]any{"type": "ServiceAccountRoleArn", "value": req.ServiceAccountRoleArn})
	}
	params = append(params, map[string]any{"type": "AddonName", "value": req.AddonName})
	if err := s.putAddon(ctx, region, a); err != nil {
		return nil, protocol.ErrInternalError
	}
	update := &Update{
		ID:     fmt.Sprintf("upd-addon-%s-%d", req.AddonName, s.clk.Now().UnixNano()),
		Status: "Successful", Type: "AddonUpdate", CreatedAt: s.clk.Now(), Params: params,
	}
	_ = s.putUpdate(ctx, region, req.ClusterName, update)
	return &updateAddonResponse{Update: update}, nil
}

type describeAddonVersionsRequest struct {
	AddonName string `json:"addonName" cbor:"addonName"`
}

type describeAddonVersionsResponse struct {
	Addons []any `json:"addons" cbor:"addons"`
}

func (s *Service) describeAddonVersionsTyped(ctx context.Context, req *describeAddonVersionsRequest) (*describeAddonVersionsResponse, *protocol.AWSError) {
	versions := addonVersionCatalog[req.AddonName]
	if len(versions) == 0 {
		return &describeAddonVersionsResponse{Addons: []any{}}, nil
	}
	versionEntries := make([]map[string]any, 0, len(versions))
	for _, v := range versions {
		versionEntries = append(versionEntries, map[string]any{
			"addonVersion": v,
			"compatibilities": []map[string]any{
				{"clusterVersion": "1.30", "defaultVersion": v == versions[0]},
				{"clusterVersion": "1.29", "defaultVersion": false},
			},
		})
	}
	entry := map[string]any{"addonName": req.AddonName, "addonVersions": versionEntries}
	return &describeAddonVersionsResponse{Addons: []any{entry}}, nil
}

type describeAddonConfigurationRequest struct {
	AddonName string `json:"addonName" cbor:"addonName"`
}

type describeAddonConfigurationResponse struct {
	AddonName           string `json:"addonName" cbor:"addonName"`
	AddonVersion        string `json:"addonVersion" cbor:"addonVersion"`
	ConfigurationSchema string `json:"configurationSchema" cbor:"configurationSchema"`
}

func (s *Service) describeAddonConfigurationTyped(ctx context.Context, req *describeAddonConfigurationRequest) (*describeAddonConfigurationResponse, *protocol.AWSError) {
	cfg, ok := addonConfigurationCatalog[req.AddonName]
	if !ok {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("No addon configuration found for name: %s", req.AddonName), HTTPStatus: 404}
	}
	return &describeAddonConfigurationResponse{
		AddonName: req.AddonName, AddonVersion: cfg.Version, ConfigurationSchema: cfg.Schema,
	}, nil
}

// ─── Access Entries ───────────────────────────────────────────────────────────

//nolint:unused // Kept for AWS-shaped access-entry response members.
type accessEntryARNs struct {
	PrincipalArn string `json:"principalArn" cbor:"principalArn"`
}

type listAccessEntriesRequest struct {
	ClusterName string `json:"name" cbor:"name"`
}

type listAccessEntriesResponse struct {
	AccessEntries []string `json:"accessEntries" cbor:"accessEntries"`
}

func (s *Service) listAccessEntriesTyped(ctx context.Context, req *listAccessEntriesRequest) (*listAccessEntriesResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	entries, err := s.listAccessEntriesForCluster(ctx, region, req.ClusterName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	arns := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.PrincipalArn != "" {
			arns = append(arns, e.PrincipalArn)
		}
	}
	sort.Strings(arns)
	return &listAccessEntriesResponse{AccessEntries: arns}, nil
}

type createAccessEntryRequest struct {
	ClusterName      string            `json:"name" cbor:"name"`
	PrincipalArn     string            `json:"principalArn" cbor:"principalArn"`
	Type             string            `json:"type" cbor:"type"`
	Username         string            `json:"username" cbor:"username"`
	KubernetesGroups []string          `json:"kubernetesGroups" cbor:"kubernetesGroups"`
	Tags             map[string]string `json:"tags" cbor:"tags"`
}

type createAccessEntryResponse struct {
	AccessEntry *AccessEntry `json:"accessEntry" cbor:"accessEntry"`
}

func (s *Service) createAccessEntryTyped(ctx context.Context, req *createAccessEntryRequest) (*createAccessEntryResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	key := accessEntryKey(region, req.ClusterName, req.PrincipalArn)
	if _, found, err := s.store.Get(ctx, nsAccess, key); err != nil {
		return nil, protocol.ErrInternalError
	} else if found {
		return nil, &protocol.AWSError{Code: "ResourceInUseException", Message: fmt.Sprintf("Access entry for principal %q already exists", req.PrincipalArn), HTTPStatus: 409}
	}
	entryType := req.Type
	if strings.TrimSpace(entryType) == "" {
		entryType = "STANDARD"
	}
	entry := &AccessEntry{
		ClusterName: req.ClusterName, PrincipalArn: req.PrincipalArn,
		Type: entryType, Username: req.Username, KubernetesGroups: req.KubernetesGroups,
		CreatedAt: s.clk.Now(), ModifiedAt: s.clk.Now(),
	}
	if err := s.putAccessEntry(ctx, region, entry); err != nil {
		return nil, protocol.ErrInternalError
	}
	entryARN := s.accessEntryARN(region, req.ClusterName, req.PrincipalArn)
	_ = s.putInlineTags(ctx, entryARN, req.Tags)
	entry.Tags = s.readTagsForARN(ctx, entryARN)
	return &createAccessEntryResponse{AccessEntry: entry}, nil
}

type describeAccessEntryRequest struct {
	ClusterName  string `json:"name" cbor:"name"`
	PrincipalArn string `json:"principalArn" cbor:"principalArn"`
}

type describeAccessEntryResponse struct {
	AccessEntry *AccessEntry `json:"accessEntry" cbor:"accessEntry"`
}

func (s *Service) describeAccessEntryTyped(ctx context.Context, req *describeAccessEntryRequest) (*describeAccessEntryResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	entry, found, err := s.getAccessEntry(ctx, region, req.ClusterName, req.PrincipalArn)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("No access entry found for principalArn: %s", req.PrincipalArn), HTTPStatus: 404}
	}
	entry.Tags = s.readTagsForARN(ctx, s.accessEntryARN(region, req.ClusterName, req.PrincipalArn))
	return &describeAccessEntryResponse{AccessEntry: entry}, nil
}

type updateAccessEntryRequest struct {
	ClusterName      string   `json:"name" cbor:"name"`
	PrincipalArn     string   `json:"principalArn" cbor:"principalArn"`
	Username         string   `json:"username" cbor:"username"`
	KubernetesGroups []string `json:"kubernetesGroups" cbor:"kubernetesGroups"`
}

type updateAccessEntryResponse struct {
	AccessEntry *AccessEntry `json:"accessEntry" cbor:"accessEntry"`
}

func (s *Service) updateAccessEntryTyped(ctx context.Context, req *updateAccessEntryRequest) (*updateAccessEntryResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	entry, found, err := s.getAccessEntry(ctx, region, req.ClusterName, req.PrincipalArn)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("No access entry found for principalArn: %s", req.PrincipalArn), HTTPStatus: 404}
	}
	if req.Username != "" {
		entry.Username = req.Username
	}
	if req.KubernetesGroups != nil {
		entry.KubernetesGroups = req.KubernetesGroups
	}
	entry.ModifiedAt = s.clk.Now()
	if err := s.putAccessEntry(ctx, region, entry); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &updateAccessEntryResponse{AccessEntry: entry}, nil
}

type deleteAccessEntryRequest struct {
	ClusterName  string `json:"name" cbor:"name"`
	PrincipalArn string `json:"principalArn" cbor:"principalArn"`
}

func (s *Service) deleteAccessEntryTyped(ctx context.Context, req *deleteAccessEntryRequest) (any, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	if _, found, err := s.getAccessEntry(ctx, region, req.ClusterName, req.PrincipalArn); err != nil {
		return nil, protocol.ErrInternalError
	} else if !found {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("No access entry found for principalArn: %s", req.PrincipalArn), HTTPStatus: 404}
	}
	associatedPolicies, err := s.listAssociatedAccessPoliciesForEntry(ctx, region, req.ClusterName, req.PrincipalArn)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	for _, p := range associatedPolicies {
		_ = s.store.Delete(ctx, nsAccessPol, associatedAccessPolicyKey(region, req.ClusterName, req.PrincipalArn, p.PolicyArn))
	}
	_ = s.store.Delete(ctx, nsAccess, accessEntryKey(region, req.ClusterName, req.PrincipalArn))
	_ = s.store.Delete(ctx, nsTags, tagKey(s.accessEntryARN(region, req.ClusterName, req.PrincipalArn)))
	return struct{}{}, nil
}

// ─── Access Policies ──────────────────────────────────────────────────────────

type associateAccessPolicyRequest struct {
	ClusterName  string         `json:"name" cbor:"name"`
	PrincipalArn string         `json:"principalArn" cbor:"principalArn"`
	PolicyArn    string         `json:"policyArn" cbor:"policyArn"`
	AccessScope  map[string]any `json:"accessScope" cbor:"accessScope"`
}

type associateAccessPolicyResponse struct {
	AssociatedAccessPolicy *AssociatedAccessPolicy `json:"associatedAccessPolicy" cbor:"associatedAccessPolicy"`
}

func (s *Service) associateAccessPolicyTyped(ctx context.Context, req *associateAccessPolicyRequest) (*associateAccessPolicyResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	if _, found, err := s.getAccessEntry(ctx, region, req.ClusterName, req.PrincipalArn); err != nil {
		return nil, protocol.ErrInternalError
	} else if !found {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("No access entry found for principalArn: %s", req.PrincipalArn), HTTPStatus: 404}
	}
	if _, found, err := s.getAssociatedAccessPolicy(ctx, region, req.ClusterName, req.PrincipalArn, req.PolicyArn); err != nil {
		return nil, protocol.ErrInternalError
	} else if found {
		return nil, &protocol.AWSError{Code: "ResourceInUseException", Message: fmt.Sprintf("Access policy %q is already associated", req.PolicyArn), HTTPStatus: 409}
	}
	scope := req.AccessScope
	if scope == nil {
		scope = map[string]any{"type": "cluster"}
	}
	assoc := &AssociatedAccessPolicy{
		ClusterName: req.ClusterName, PrincipalArn: req.PrincipalArn,
		PolicyArn: req.PolicyArn, AccessScope: scope,
		AssociatedAt: s.clk.Now(), ModifiedAt: s.clk.Now(),
	}
	if err := s.putAssociatedAccessPolicy(ctx, region, assoc); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &associateAccessPolicyResponse{AssociatedAccessPolicy: assoc}, nil
}

type listAssociatedAccessPoliciesRequest struct {
	ClusterName  string `json:"name" cbor:"name"`
	PrincipalArn string `json:"principalArn" cbor:"principalArn"`
}

type listAssociatedAccessPoliciesResponse struct {
	AssociatedAccessPolicies []*AssociatedAccessPolicy `json:"associatedAccessPolicies" cbor:"associatedAccessPolicies"`
}

func (s *Service) listAssociatedAccessPoliciesTyped(ctx context.Context, req *listAssociatedAccessPoliciesRequest) (*listAssociatedAccessPoliciesResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	if _, found, err := s.getAccessEntry(ctx, region, req.ClusterName, req.PrincipalArn); err != nil {
		return nil, protocol.ErrInternalError
	} else if !found {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("No access entry found for principalArn: %s", req.PrincipalArn), HTTPStatus: 404}
	}
	items, err := s.listAssociatedAccessPoliciesForEntry(ctx, region, req.ClusterName, req.PrincipalArn)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	sort.Slice(items, func(i, j int) bool { return items[i].PolicyArn < items[j].PolicyArn })
	return &listAssociatedAccessPoliciesResponse{AssociatedAccessPolicies: items}, nil
}

type disassociateAccessPolicyRequest struct {
	ClusterName  string `json:"name" cbor:"name"`
	PrincipalArn string `json:"principalArn" cbor:"principalArn"`
	PolicyArn    string `json:"policyArn" cbor:"policyArn"`
}

func (s *Service) disassociateAccessPolicyTyped(ctx context.Context, req *disassociateAccessPolicyRequest) (any, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	if _, found, err := s.getAccessEntry(ctx, region, req.ClusterName, req.PrincipalArn); err != nil {
		return nil, protocol.ErrInternalError
	} else if !found {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("No access entry found for principalArn: %s", req.PrincipalArn), HTTPStatus: 404}
	}
	if _, found, err := s.getAssociatedAccessPolicy(ctx, region, req.ClusterName, req.PrincipalArn, req.PolicyArn); err != nil {
		return nil, protocol.ErrInternalError
	} else if !found {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("No associated access policy found for policyArn: %s", req.PolicyArn), HTTPStatus: 404}
	}
	_ = s.store.Delete(ctx, nsAccessPol, associatedAccessPolicyKey(region, req.ClusterName, req.PrincipalArn, req.PolicyArn))
	return struct{}{}, nil
}

type listAccessPoliciesRequest struct{}

type listAccessPoliciesResponse struct {
	AccessPolicies []map[string]any `json:"accessPolicies" cbor:"accessPolicies"`
}

func (s *Service) listAccessPoliciesTyped(ctx context.Context, _ *listAccessPoliciesRequest) (*listAccessPoliciesResponse, *protocol.AWSError) {
	return &listAccessPoliciesResponse{AccessPolicies: managedAccessPolicies()}, nil
}

type describeAccessPolicyRequest struct {
	Name string `json:"name" cbor:"name"`
}

type describeAccessPolicyResponse struct {
	AccessPolicy map[string]any `json:"accessPolicy" cbor:"accessPolicy"`
}

func (s *Service) describeAccessPolicyTyped(ctx context.Context, req *describeAccessPolicyRequest) (*describeAccessPolicyResponse, *protocol.AWSError) {
	for _, policy := range managedAccessPolicies() {
		if policy["name"] == req.Name {
			return &describeAccessPolicyResponse{AccessPolicy: policy}, nil
		}
	}
	return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("No access policy found for name: %s", req.Name), HTTPStatus: 404}
}

// ─── Identity Provider Configs ───────────────────────────────────────────────

type listIdentityProviderConfigsRequest struct {
	ClusterName string `json:"name" cbor:"name"`
}

type listIdentityProviderConfigsResponse struct {
	IdentityProviderConfigs []map[string]any `json:"identityProviderConfigs" cbor:"identityProviderConfigs"`
}

func (s *Service) listIdentityProviderConfigsTyped(ctx context.Context, req *listIdentityProviderConfigsRequest) (*listIdentityProviderConfigsResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	configs, err := s.listIdentityProviderConfigsForCluster(ctx, region, req.ClusterName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	items := make([]map[string]any, 0, len(configs))
	for _, cfg := range configs {
		items = append(items, map[string]any{"type": cfg.Type, "name": cfg.Name})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i]["type"] == items[j]["type"] {
			return fmt.Sprint(items[i]["name"]) < fmt.Sprint(items[j]["name"])
		}
		return fmt.Sprint(items[i]["type"]) < fmt.Sprint(items[j]["type"])
	})
	return &listIdentityProviderConfigsResponse{IdentityProviderConfigs: items}, nil
}

type describeIdentityProviderConfigRequest struct {
	ClusterName string `json:"name" cbor:"name"`
	ConfigType  string `json:"configType" cbor:"configType"`
	ConfigName  string `json:"configName" cbor:"configName"`
}

type describeIdentityProviderConfigResponse struct {
	IdentityProviderConfig *IdentityProviderConfig `json:"identityProviderConfig" cbor:"identityProviderConfig"`
}

func (s *Service) describeIdentityProviderConfigTyped(ctx context.Context, req *describeIdentityProviderConfigRequest) (*describeIdentityProviderConfigResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	cfg, found, err := s.getIdentityProviderConfig(ctx, region, req.ClusterName, req.ConfigType, req.ConfigName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("No identity provider config found for %s/%s", req.ConfigType, req.ConfigName), HTTPStatus: 404}
	}
	cfg.Tags = s.readTagsForARN(ctx, s.identityProviderConfigARN(region, req.ClusterName, req.ConfigType, req.ConfigName))
	return &describeIdentityProviderConfigResponse{IdentityProviderConfig: cfg}, nil
}

type updateIdentityProviderConfigRequest struct {
	ClusterName string            `json:"name" cbor:"name"`
	ConfigType  string            `json:"configType" cbor:"configType"`
	ConfigName  string            `json:"configName" cbor:"configName"`
	OIDC        map[string]any    `json:"oidc" cbor:"oidc"`
	Tags        map[string]string `json:"tags" cbor:"tags"`
}

type updateIdentityProviderConfigResponse struct {
	Update *Update `json:"update" cbor:"update"`
}

func (s *Service) updateIdentityProviderConfigTyped(ctx context.Context, req *updateIdentityProviderConfigRequest) (*updateIdentityProviderConfigResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	cfg, found, err := s.getIdentityProviderConfig(ctx, region, req.ClusterName, req.ConfigType, req.ConfigName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("No identity provider config found for %s/%s", req.ConfigType, req.ConfigName), HTTPStatus: 404}
	}
	if req.ConfigType == "oidc" && req.OIDC != nil {
		if cfg.OIDC == nil {
			cfg.OIDC = map[string]any{}
		}
		for k, v := range req.OIDC {
			cfg.OIDC[k] = v
		}
	}
	if err := s.putIdentityProviderConfig(ctx, region, cfg); err != nil {
		return nil, protocol.ErrInternalError
	}
	update := &Update{
		ID:     fmt.Sprintf("upd-idp-update-%s-%d", req.ConfigName, s.clk.Now().UnixNano()),
		Status: "Successful", Type: "UpdateIdentityProviderConfig", CreatedAt: s.clk.Now(),
		Params: []map[string]any{
			{"type": "IdentityProviderConfigType", "value": req.ConfigType},
			{"type": "IdentityProviderConfigName", "value": req.ConfigName},
		},
	}
	_ = s.putUpdate(ctx, region, req.ClusterName, update)
	return &updateIdentityProviderConfigResponse{Update: update}, nil
}

type associateIdentityProviderConfigRequest struct {
	ClusterName string            `json:"name" cbor:"name"`
	OIDC        map[string]any    `json:"oidc" cbor:"oidc"`
	Tags        map[string]string `json:"tags" cbor:"tags"`
}

type associateIdentityProviderConfigResponse struct {
	Update *Update `json:"update" cbor:"update"`
}

func (s *Service) associateIdentityProviderConfigTyped(ctx context.Context, req *associateIdentityProviderConfigRequest) (*associateIdentityProviderConfigResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	name, _ := req.OIDC["identityProviderConfigName"].(string)
	if name == "" {
		return nil, &protocol.AWSError{Code: "InvalidParameterException", Message: "oidc.identityProviderConfigName is required", HTTPStatus: 400}
	}
	cfg := &IdentityProviderConfig{
		ClusterName: req.ClusterName, Type: "oidc", Name: name,
		OIDC: req.OIDC, CreatedAt: s.clk.Now(),
	}
	if err := s.putIdentityProviderConfig(ctx, region, cfg); err != nil {
		return nil, protocol.ErrInternalError
	}
	_ = s.putInlineTags(ctx, s.identityProviderConfigARN(region, req.ClusterName, cfg.Type, cfg.Name), req.Tags)
	update := &Update{
		ID:     fmt.Sprintf("upd-idp-%s-%d", name, s.clk.Now().UnixNano()),
		Status: "Successful", Type: "AssociateIdentityProviderConfig", CreatedAt: s.clk.Now(),
		Params: []map[string]any{
			{"type": "IdentityProviderConfigType", "value": "oidc"},
			{"type": "IdentityProviderConfigName", "value": name},
		},
	}
	_ = s.putUpdate(ctx, region, req.ClusterName, update)
	return &associateIdentityProviderConfigResponse{Update: update}, nil
}

type disassociateIdentityProviderConfigRequest struct {
	ClusterName string `json:"name" cbor:"name"`
	ConfigType  string `json:"type" cbor:"type"`
	ConfigName  string `json:"configName" cbor:"configName"`
}

type disassociateIdentityProviderConfigResponse struct {
	Update *Update `json:"update" cbor:"update"`
}

func (s *Service) disassociateIdentityProviderConfigTyped(ctx context.Context, req *disassociateIdentityProviderConfigRequest) (*disassociateIdentityProviderConfigResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	_ = s.store.Delete(ctx, nsIDPConfigs, idpConfigKey(region, req.ClusterName, req.ConfigType, req.ConfigName))
	_ = s.store.Delete(ctx, nsTags, tagKey(s.identityProviderConfigARN(region, req.ClusterName, req.ConfigType, req.ConfigName)))
	update := &Update{
		ID:     fmt.Sprintf("upd-idp-disassoc-%s-%d", req.ConfigName, s.clk.Now().UnixNano()),
		Status: "Successful", Type: "DisassociateIdentityProviderConfig", CreatedAt: s.clk.Now(),
		Params: []map[string]any{
			{"type": "IdentityProviderConfigType", "value": req.ConfigType},
			{"type": "IdentityProviderConfigName", "value": req.ConfigName},
		},
	}
	_ = s.putUpdate(ctx, region, req.ClusterName, update)
	return &disassociateIdentityProviderConfigResponse{Update: update}, nil
}

// ─── Pod Identity Associations ───────────────────────────────────────────────

type listPodIdentityAssociationsRequest struct {
	ClusterName string `json:"name" cbor:"name"`
}

type listPodIdentityAssociationsResponse struct {
	Associations []*PodIdentityAssociation `json:"associations" cbor:"associations"`
}

func (s *Service) listPodIdentityAssociationsTyped(ctx context.Context, req *listPodIdentityAssociationsRequest) (*listPodIdentityAssociationsResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	associations, err := s.listPodIdentityAssociationsForCluster(ctx, region, req.ClusterName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	sort.Slice(associations, func(i, j int) bool { return associations[i].AssociationID < associations[j].AssociationID })
	return &listPodIdentityAssociationsResponse{Associations: associations}, nil
}

type createPodIdentityAssociationRequest struct {
	ClusterName    string            `json:"name" cbor:"name"`
	Namespace      string            `json:"namespace" cbor:"namespace"`
	ServiceAccount string            `json:"serviceAccount" cbor:"serviceAccount"`
	RoleArn        string            `json:"roleArn" cbor:"roleArn"`
	Tags           map[string]string `json:"tags" cbor:"tags"`
}

type createPodIdentityAssociationResponse struct {
	Association *PodIdentityAssociation `json:"association" cbor:"association"`
}

func (s *Service) createPodIdentityAssociationTyped(ctx context.Context, req *createPodIdentityAssociationRequest) (*createPodIdentityAssociationResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	existing, err := s.listPodIdentityAssociationsForCluster(ctx, region, req.ClusterName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	for _, assoc := range existing {
		if assoc.Namespace == req.Namespace && assoc.ServiceAccount == req.ServiceAccount {
			return nil, &protocol.AWSError{Code: "ResourceInUseException", Message: fmt.Sprintf("Pod identity association for namespace %q service account %q already exists", req.Namespace, req.ServiceAccount), HTTPStatus: 409}
		}
	}
	associationID := fmt.Sprintf("pia-%d", s.clk.Now().UnixNano())
	assoc := &PodIdentityAssociation{
		ClusterName: req.ClusterName, AssociationID: associationID,
		AssociationArn: s.podIdentityAssociationARN(region, req.ClusterName, associationID),
		Namespace:      req.Namespace, ServiceAccount: req.ServiceAccount,
		RoleArn: req.RoleArn, CreatedAt: s.clk.Now(), ModifiedAt: s.clk.Now(),
	}
	if err := s.putPodIdentityAssociation(ctx, region, assoc); err != nil {
		return nil, protocol.ErrInternalError
	}
	_ = s.putInlineTags(ctx, assoc.AssociationArn, req.Tags)
	assoc.Tags = s.readTagsForARN(ctx, assoc.AssociationArn)
	return &createPodIdentityAssociationResponse{Association: assoc}, nil
}

type describePodIdentityAssociationRequest struct {
	ClusterName   string `json:"name" cbor:"name"`
	AssociationId string `json:"associationId" cbor:"associationId"`
}

type describePodIdentityAssociationResponse struct {
	Association *PodIdentityAssociation `json:"association" cbor:"association"`
}

func (s *Service) describePodIdentityAssociationTyped(ctx context.Context, req *describePodIdentityAssociationRequest) (*describePodIdentityAssociationResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	assoc, found, err := s.getPodIdentityAssociation(ctx, region, req.ClusterName, req.AssociationId)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("No pod identity association found for id: %s", req.AssociationId), HTTPStatus: 404}
	}
	assoc.Tags = s.readTagsForARN(ctx, assoc.AssociationArn)
	return &describePodIdentityAssociationResponse{Association: assoc}, nil
}

type updatePodIdentityAssociationRequest struct {
	ClusterName   string `json:"name" cbor:"name"`
	AssociationId string `json:"associationId" cbor:"associationId"`
	RoleArn       string `json:"roleArn" cbor:"roleArn"`
}

type updatePodIdentityAssociationResponse struct {
	Association *PodIdentityAssociation `json:"association" cbor:"association"`
}

func (s *Service) updatePodIdentityAssociationTyped(ctx context.Context, req *updatePodIdentityAssociationRequest) (*updatePodIdentityAssociationResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	assoc, found, err := s.getPodIdentityAssociation(ctx, region, req.ClusterName, req.AssociationId)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("No pod identity association found for id: %s", req.AssociationId), HTTPStatus: 404}
	}
	assoc.RoleArn = req.RoleArn
	assoc.ModifiedAt = s.clk.Now()
	if err := s.putPodIdentityAssociation(ctx, region, assoc); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &updatePodIdentityAssociationResponse{Association: assoc}, nil
}

type deletePodIdentityAssociationRequest struct {
	ClusterName   string `json:"name" cbor:"name"`
	AssociationId string `json:"associationId" cbor:"associationId"`
}

type deletePodIdentityAssociationResponse struct {
	Association *PodIdentityAssociation `json:"association" cbor:"association"`
}

func (s *Service) deletePodIdentityAssociationTyped(ctx context.Context, req *deletePodIdentityAssociationRequest) (*deletePodIdentityAssociationResponse, *protocol.AWSError) {
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	if _, aerr := s.validateCluster(ctx, region, req.ClusterName); aerr != nil {
		return nil, aerr
	}
	assoc, found, err := s.getPodIdentityAssociation(ctx, region, req.ClusterName, req.AssociationId)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("No pod identity association found for id: %s", req.AssociationId), HTTPStatus: 404}
	}
	_ = s.store.Delete(ctx, nsPodIDAssoc, podIdentityAssociationKey(region, req.ClusterName, req.AssociationId))
	_ = s.store.Delete(ctx, nsTags, tagKey(assoc.AssociationArn))
	return &deletePodIdentityAssociationResponse{Association: assoc}, nil
}

// ─── Tags ─────────────────────────────────────────────────────────────────────

type eksTagResourceRequest struct {
	ResourceArn string            `json:"resourceArn" cbor:"resourceArn"`
	Tags        map[string]string `json:"tags" cbor:"tags"`
}

func (s *Service) eksTagResourceTyped(ctx context.Context, req *eksTagResourceRequest) (any, *protocol.AWSError) {
	arn := s.tryDecodeARN(req.ResourceArn)
	existing := map[string]string{}
	if raw, found, err := s.store.Get(ctx, nsTags, tagKey(arn)); err != nil {
		return nil, protocol.ErrInternalError
	} else if found {
		_ = json.Unmarshal([]byte(raw), &existing)
	}
	for k, v := range req.Tags {
		existing[k] = v
	}
	raw, err := json.Marshal(existing)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if err := s.store.Set(ctx, nsTags, tagKey(arn), string(raw)); err != nil {
		return nil, protocol.ErrInternalError
	}
	return struct{}{}, nil
}

type eksUntagResourceRequest struct {
	ResourceArn string   `json:"resourceArn" cbor:"resourceArn"`
	TagKeys     []string `json:"tagKeys" cbor:"tagKeys"`
}

func (s *Service) eksUntagResourceTyped(ctx context.Context, req *eksUntagResourceRequest) (any, *protocol.AWSError) {
	arn := s.tryDecodeARN(req.ResourceArn)
	if len(req.TagKeys) == 0 {
		return nil, &protocol.AWSError{Code: "InvalidParameterException", Message: "at least one tagKeys query parameter is required", HTTPStatus: 400}
	}
	raw, found, err := s.store.Get(ctx, nsTags, tagKey(arn))
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return struct{}{}, nil
	}
	var tags map[string]string
	_ = json.Unmarshal([]byte(raw), &tags)
	for _, k := range req.TagKeys {
		delete(tags, k)
	}
	updated, err := json.Marshal(tags)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	_ = s.store.Set(ctx, nsTags, tagKey(arn), string(updated))
	return struct{}{}, nil
}

type eksListTagsForResourceRequest struct {
	ResourceArn string `json:"resourceArn" cbor:"resourceArn"`
}

type eksListTagsForResourceResponse struct {
	Tags map[string]string `json:"tags" cbor:"tags"`
}

func (s *Service) eksListTagsForResourceTyped(ctx context.Context, req *eksListTagsForResourceRequest) (*eksListTagsForResourceResponse, *protocol.AWSError) {
	arn := s.tryDecodeARN(req.ResourceArn)
	raw, found, err := s.store.Get(ctx, nsTags, tagKey(arn))
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return &eksListTagsForResourceResponse{Tags: map[string]string{}}, nil
	}
	var tags map[string]string
	_ = json.Unmarshal([]byte(raw), &tags)
	return &eksListTagsForResourceResponse{Tags: tags}, nil
}

func (s *Service) tryDecodeARN(arn string) string {
	if decoded, err := url.PathUnescape(arn); err == nil {
		return decoded
	}
	return arn
}

// validateCluster loads a cluster and returns an error on miss.
func (s *Service) validateCluster(ctx context.Context, region, name string) (*Cluster, *protocol.AWSError) {
	c, found, err := s.getCluster(ctx, region, name)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: "No cluster found for name: " + name, HTTPStatus: 404}
	}
	return c, nil
}
