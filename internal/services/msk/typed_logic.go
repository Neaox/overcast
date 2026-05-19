package msk

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
)

// --- CreateCluster ---

type createClusterRequest struct {
	ClusterName         string              `json:"clusterName" cbor:"clusterName"`
	KafkaVersion        string              `json:"kafkaVersion" cbor:"kafkaVersion"`
	NumberOfBrokerNodes int                 `json:"numberOfBrokerNodes" cbor:"numberOfBrokerNodes"`
	BrokerNodeGroupInfo BrokerNodeGroupInfo `json:"brokerNodeGroupInfo" cbor:"brokerNodeGroupInfo"`
	Tags                map[string]string   `json:"tags" cbor:"tags"`
}

type createClusterResponse struct {
	ClusterArn  string `json:"clusterArn" cbor:"clusterArn"`
	ClusterName string `json:"clusterName" cbor:"clusterName"`
	State       string `json:"state" cbor:"state"`
}

func (s *Service) createClusterTyped(ctx context.Context, req *createClusterRequest) (*createClusterResponse, *protocol.AWSError) {
	if req.ClusterName == "" {
		return nil, &protocol.AWSError{
			Code: "ValidationException", Message: "clusterName is required",
			HTTPStatus: 400,
		}
	}
	kv := req.KafkaVersion
	if kv == "" {
		kv = "3.5.1"
	}
	numNodes := req.NumberOfBrokerNodes
	if numNodes == 0 {
		numNodes = 3
	}
	region := middleware.RegionFromContext(ctx, s.handler.cfg.Region)
	id := uuid.NewString()
	clusterARN := fmt.Sprintf("arn:aws:kafka:%s:%s:cluster/%s/%s", region, s.handler.cfg.AccountID, req.ClusterName, id)

	cluster := &Cluster{
		ClusterArn:          clusterARN,
		ClusterName:         req.ClusterName,
		State:               "CREATING",
		CreationTime:        s.handler.clk.Now(),
		CurrentVersion:      id,
		BrokerNodeGroupInfo: req.BrokerNodeGroupInfo,
		NumberOfBrokerNodes: numNodes,
		KafkaVersion:        kv,
		Tags:                req.Tags,
	}
	if aerr := s.handler.store.putCluster(ctx, cluster); aerr != nil {
		return nil, aerr
	}

	clusterARNCopy := clusterARN
	if s.handler.dockerReady.Load() {
		if s.handler.puller != nil {
			s.handler.puller.Prewarm(redpandaImage)
		}
		s.handler.dockerWg.Add(1)
		go func() {
			defer s.handler.dockerWg.Done()
			bgCtx := context.Background()
			if err := s.handler.startClusterContainer(bgCtx, clusterARNCopy); err != nil {
				s.handler.log.Warn("failed to start Docker container for MSK cluster — falling back to metadata-only",
					zap.String("cluster", clusterARNCopy), zap.Error(err))
				s.handler.clusterFallbackActive(clusterARNCopy)
			}
		}()
	} else {
		s.handler.scheduler.After(clusterARNCopy+":active", 0, func() {
			bgCtx := context.Background()
			got, aerr := s.handler.store.getCluster(bgCtx, clusterARNCopy)
			if aerr != nil {
				return
			}
			if got.State == "CREATING" {
				got.State = "ACTIVE"
				s.handler.store.putCluster(bgCtx, got) //nolint:errcheck
			}
		})
	}

	return &createClusterResponse{
		ClusterArn: clusterARN, ClusterName: req.ClusterName, State: "CREATING",
	}, nil
}

// --- DescribeCluster ---

type describeClusterRequest struct {
	ClusterArn string `json:"clusterArn" cbor:"clusterArn"`
}

type describeClusterResponse struct {
	ClusterInfo *Cluster `json:"clusterInfo" cbor:"clusterInfo"`
}

func (s *Service) describeClusterTyped(ctx context.Context, req *describeClusterRequest) (*describeClusterResponse, *protocol.AWSError) {
	cluster, aerr := s.handler.store.getCluster(ctx, req.ClusterArn)
	if aerr != nil {
		return nil, aerr
	}
	return &describeClusterResponse{ClusterInfo: cluster}, nil
}

// --- ListClusters ---

type listClustersRequest struct {
	ClusterNameFilter string `json:"clusterNameFilter" cbor:"clusterNameFilter"`
}

type listClustersResponse struct {
	ClusterInfoList []*Cluster `json:"clusterInfoList" cbor:"clusterInfoList"`
	NextToken       *string    `json:"nextToken" cbor:"nextToken"`
}

func (s *Service) listClustersTyped(ctx context.Context, req *listClustersRequest) (*listClustersResponse, *protocol.AWSError) {
	all, aerr := s.handler.store.listClusters(ctx)
	if aerr != nil {
		return nil, aerr
	}
	result := make([]*Cluster, 0, len(all))
	for _, c := range all {
		if req.ClusterNameFilter != "" && c.ClusterName != req.ClusterNameFilter {
			continue
		}
		result = append(result, c)
	}
	return &listClustersResponse{ClusterInfoList: result}, nil
}

// --- DeleteCluster ---

type deleteClusterRequest struct {
	ClusterArn string `json:"clusterArn" cbor:"clusterArn"`
}

type deleteClusterResponse struct {
	ClusterArn string `json:"clusterArn" cbor:"clusterArn"`
	State      string `json:"state" cbor:"state"`
}

func (s *Service) deleteClusterTyped(ctx context.Context, req *deleteClusterRequest) (*deleteClusterResponse, *protocol.AWSError) {
	cluster, aerr := s.handler.store.getCluster(ctx, req.ClusterArn)
	if aerr != nil {
		return nil, aerr
	}

	cluster.State = "DELETING"
	if aerr := s.handler.store.putCluster(ctx, cluster); aerr != nil {
		return nil, aerr
	}

	clusterARNCopy := req.ClusterArn
	s.handler.scheduler.Cancel(clusterARNCopy + ":health")

	id := cluster.DockerContainerID
	if s.handler.gc != nil && id != "" {
		s.handler.gc.StopNow(id)
		s.handler.gc.ScheduleRemove(id)
	}
	if cluster.HostPort > 0 {
		_ = s.handler.store.releasePort(ctx, cluster.HostPort) //nolint:errcheck
	}

	s.handler.scheduler.After(clusterARNCopy+":delete", 50*time.Millisecond, func() {
		bgCtx := context.Background()
		if aerr := s.handler.store.deleteCluster(bgCtx, clusterARNCopy); aerr != nil {
			s.handler.log.Warn("failed to delete MSK cluster record", zap.String("cluster", clusterARNCopy), zap.Error(aerr))
		}
	})

	return &deleteClusterResponse{ClusterArn: req.ClusterArn, State: "DELETING"}, nil
}

// --- GetBootstrapBrokers ---

type getBootstrapBrokersRequest struct {
	ClusterArn string `json:"clusterArn" cbor:"clusterArn"`
}

type getBootstrapBrokersResponse struct {
	BootstrapBrokerString    string `json:"bootstrapBrokerString" cbor:"bootstrapBrokerString"`
	BootstrapBrokerStringTls string `json:"bootstrapBrokerStringTls" cbor:"bootstrapBrokerStringTls"`
}

func (s *Service) getBootstrapBrokersTyped(ctx context.Context, req *getBootstrapBrokersRequest) (*getBootstrapBrokersResponse, *protocol.AWSError) {
	cluster, aerr := s.handler.store.getCluster(ctx, req.ClusterArn)
	if aerr != nil {
		return nil, aerr
	}

	var bootstrapBrokerString string
	if cluster.HostPort > 0 {
		host := s.handler.cfg.ExternalHostname()
		bootstrapBrokerString = fmt.Sprintf("%s:%d", host, cluster.HostPort)
	} else if !s.handler.dockerReady.Load() {
		bootstrapBrokerString = fmt.Sprintf("127.0.0.1:%d", s.handler.cfg.MSKPortBase)
	}

	return &getBootstrapBrokersResponse{
		BootstrapBrokerString:    bootstrapBrokerString,
		BootstrapBrokerStringTls: "",
	}, nil
}

// --- CreateConfiguration ---

type createConfigurationRequest struct {
	Name             string   `json:"name" cbor:"name"`
	Description      string   `json:"description" cbor:"description"`
	KafkaVersions    []string `json:"kafkaVersions" cbor:"kafkaVersions"`
	ServerProperties string   `json:"serverProperties" cbor:"serverProperties"`
}

type createConfigurationResponse struct {
	Arn            string        `json:"arn" cbor:"arn"`
	CreationTime   time.Time     `json:"creationTime" cbor:"creationTime"`
	LatestRevision *revisionInfo `json:"latestRevision" cbor:"latestRevision"`
	Name           string        `json:"name" cbor:"name"`
	State          string        `json:"state" cbor:"state"`
}

type revisionInfo struct {
	CreationTime time.Time `json:"creationTime" cbor:"creationTime"`
	Revision     int64     `json:"revision" cbor:"revision"`
}

func (s *Service) createConfigurationTyped(ctx context.Context, req *createConfigurationRequest) (*createConfigurationResponse, *protocol.AWSError) {
	if req.Name == "" {
		return nil, &protocol.AWSError{
			Code: "ValidationException", Message: "name is required",
			HTTPStatus: 400,
		}
	}

	region := middleware.RegionFromContext(ctx, s.handler.cfg.Region)
	id := uuid.NewString()
	arn := fmt.Sprintf("arn:aws:kafka:%s:%s:configuration/%s/%s", region, s.handler.cfg.AccountID, req.Name, id)
	now := s.handler.clk.Now()

	cfg := &ClusterConfiguration{
		Arn:           arn,
		Name:          req.Name,
		Description:   req.Description,
		KafkaVersions: req.KafkaVersions,
		CreationTime:  now,
		LatestRevision: Revision{
			CreationTime: now,
			Revision:     1,
		},
		State: "ACTIVE",
	}

	if aerr := s.handler.store.putConfiguration(ctx, cfg); aerr != nil {
		return nil, aerr
	}

	return &createConfigurationResponse{
		Arn:          arn,
		CreationTime: now,
		LatestRevision: &revisionInfo{
			CreationTime: now,
			Revision:     1,
		},
		Name:  req.Name,
		State: "ACTIVE",
	}, nil
}

// --- DescribeConfiguration ---

type describeConfigurationRequest struct {
	Arn string `json:"arn" cbor:"arn"`
}

type describeConfigurationResponse struct {
	Arn            string        `json:"arn" cbor:"arn"`
	Name           string        `json:"name" cbor:"name"`
	Description    string        `json:"description,omitempty" cbor:"description,omitempty"`
	KafkaVersions  []string      `json:"kafkaVersions,omitempty" cbor:"kafkaVersions,omitempty"`
	CreationTime   time.Time     `json:"creationTime" cbor:"creationTime"`
	LatestRevision *revisionInfo `json:"latestRevision" cbor:"latestRevision"`
	State          string        `json:"state" cbor:"state"`
}

func (s *Service) describeConfigurationTyped(ctx context.Context, req *describeConfigurationRequest) (*describeConfigurationResponse, *protocol.AWSError) {
	cfg, aerr := s.handler.store.getConfiguration(ctx, req.Arn)
	if aerr != nil {
		return nil, aerr
	}
	return &describeConfigurationResponse{
		Arn:           cfg.Arn,
		Name:          cfg.Name,
		Description:   cfg.Description,
		KafkaVersions: cfg.KafkaVersions,
		CreationTime:  cfg.CreationTime,
		LatestRevision: &revisionInfo{
			CreationTime: cfg.LatestRevision.CreationTime,
			Revision:     cfg.LatestRevision.Revision,
		},
		State: cfg.State,
	}, nil
}

// --- ListConfigurations ---

type listConfigurationsRequest struct{}

type listConfigurationsResponse struct {
	Configurations []*ClusterConfiguration `json:"configurations" cbor:"configurations"`
}

func (s *Service) listConfigurationsTyped(ctx context.Context, _ *listConfigurationsRequest) (*listConfigurationsResponse, *protocol.AWSError) {
	all, aerr := s.handler.store.listConfigurations(ctx)
	if aerr != nil {
		return nil, aerr
	}
	return &listConfigurationsResponse{Configurations: all}, nil
}

// --- DeleteConfiguration ---

type deleteConfigurationRequest struct {
	Arn string `json:"arn" cbor:"arn"`
}

func (s *Service) deleteConfigurationTyped(ctx context.Context, req *deleteConfigurationRequest) (*struct{}, *protocol.AWSError) {
	if _, aerr := s.handler.store.getConfiguration(ctx, req.Arn); aerr != nil {
		return nil, aerr
	}
	if aerr := s.handler.store.deleteConfiguration(ctx, req.Arn); aerr != nil {
		return nil, aerr
	}
	return &struct{}{}, nil
}

// --- ListKafkaVersions ---

type listKafkaVersionsRequest struct{}

type listKafkaVersionsResponse struct {
	KafkaVersions []kafkaVersionItem `json:"kafkaVersions" cbor:"kafkaVersions"`
}

type kafkaVersionItem struct {
	Status  string `json:"status" cbor:"status"`
	Version string `json:"version" cbor:"version"`
}

func (s *Service) listKafkaVersionsTyped(ctx context.Context, _ *listKafkaVersionsRequest) (*listKafkaVersionsResponse, *protocol.AWSError) {
	versions := []kafkaVersionItem{
		{Status: "ACTIVE", Version: "3.6.0"},
		{Status: "ACTIVE", Version: "3.5.1"},
		{Status: "ACTIVE", Version: "3.4.0"},
		{Status: "ACTIVE", Version: "2.8.1"},
		{Status: "DEPRECATED", Version: "2.6.0"},
	}
	return &listKafkaVersionsResponse{KafkaVersions: versions}, nil
}

// --- TagResource ---

type tagResourceRequest struct {
	ResourceArn string            `json:"resourceArn" cbor:"resourceArn"`
	Tags        map[string]string `json:"tags" cbor:"tags"`
}

func (s *Service) tagResourceTyped(ctx context.Context, req *tagResourceRequest) (*struct{}, *protocol.AWSError) {
	tags, aerr := s.handler.store.getTags(ctx, req.ResourceArn)
	if aerr != nil {
		return nil, aerr
	}
	for k, v := range req.Tags {
		tags[k] = v
	}
	if aerr := s.handler.store.setTags(ctx, req.ResourceArn, tags); aerr != nil {
		return nil, aerr
	}
	return &struct{}{}, nil
}

// --- UntagResource ---

type untagResourceRequest struct {
	ResourceArn string   `json:"resourceArn" cbor:"resourceArn"`
	TagKeys     []string `json:"tagKeys" cbor:"tagKeys"`
}

func (s *Service) untagResourceTyped(ctx context.Context, req *untagResourceRequest) (*struct{}, *protocol.AWSError) {
	tags, aerr := s.handler.store.getTags(ctx, req.ResourceArn)
	if aerr != nil {
		return nil, aerr
	}
	for _, k := range req.TagKeys {
		delete(tags, k)
	}
	if aerr := s.handler.store.setTags(ctx, req.ResourceArn, tags); aerr != nil {
		return nil, aerr
	}
	return &struct{}{}, nil
}

// --- ListTagsForResource ---

type listTagsForResourceRequest struct {
	ResourceArn string `json:"resourceArn" cbor:"resourceArn"`
}

type listTagsForResourceResponse struct {
	Tags map[string]string `json:"tags" cbor:"tags"`
}

func (s *Service) listTagsForResourceTyped(ctx context.Context, req *listTagsForResourceRequest) (*listTagsForResourceResponse, *protocol.AWSError) {
	tags, aerr := s.handler.store.getTags(ctx, req.ResourceArn)
	if aerr != nil {
		return nil, aerr
	}
	return &listTagsForResourceResponse{Tags: tags}, nil
}

// --- CreateClusterV2 ---

type createClusterV2Request struct {
	ClusterName string            `json:"clusterName" cbor:"clusterName"`
	Tags        map[string]string `json:"tags" cbor:"tags"`
	Provisioned *provisionedInput `json:"provisioned" cbor:"provisioned"`
	Serverless  *struct{}         `json:"serverless" cbor:"serverless"`
}

type provisionedInput struct {
	KafkaVersion        string              `json:"kafkaVersion" cbor:"kafkaVersion"`
	NumberOfBrokerNodes int                 `json:"numberOfBrokerNodes" cbor:"numberOfBrokerNodes"`
	BrokerNodeGroupInfo BrokerNodeGroupInfo `json:"brokerNodeGroupInfo" cbor:"brokerNodeGroupInfo"`
}

type createClusterV2Response struct {
	ClusterArn  string `json:"clusterArn" cbor:"clusterArn"`
	ClusterName string `json:"clusterName" cbor:"clusterName"`
	ClusterType string `json:"clusterType" cbor:"clusterType"`
	State       string `json:"state" cbor:"state"`
}

func (s *Service) createClusterV2Typed(ctx context.Context, req *createClusterV2Request) (*createClusterV2Response, *protocol.AWSError) {
	if req.ClusterName == "" {
		return nil, &protocol.AWSError{
			Code: "ValidationException", Message: "clusterName is required",
			HTTPStatus: 400,
		}
	}

	region := middleware.RegionFromContext(ctx, s.handler.cfg.Region)
	id := uuid.NewString()
	clusterARN := fmt.Sprintf("arn:aws:kafka:%s:%s:cluster/%s/%s", region, s.handler.cfg.AccountID, req.ClusterName, id)

	clusterType := "SERVERLESS"
	initialState := "ACTIVE"
	cluster := &Cluster{
		ClusterArn:     clusterARN,
		ClusterName:    req.ClusterName,
		ClusterType:    clusterType,
		State:          initialState,
		CreationTime:   s.handler.clk.Now(),
		CurrentVersion: id,
		Tags:           req.Tags,
	}

	if req.Provisioned != nil {
		cluster.ClusterType = "PROVISIONED"
		cluster.State = "CREATING"
		cluster.KafkaVersion = req.Provisioned.KafkaVersion
		cluster.NumberOfBrokerNodes = req.Provisioned.NumberOfBrokerNodes
		cluster.BrokerNodeGroupInfo = req.Provisioned.BrokerNodeGroupInfo
		if cluster.KafkaVersion == "" {
			cluster.KafkaVersion = "3.5.1"
		}
		if cluster.NumberOfBrokerNodes == 0 {
			cluster.NumberOfBrokerNodes = 3
		}
	}

	if aerr := s.handler.store.putCluster(ctx, cluster); aerr != nil {
		return nil, aerr
	}

	if cluster.ClusterType == "PROVISIONED" {
		clusterARNCopy := clusterARN
		if s.handler.dockerReady.Load() {
			if s.handler.puller != nil {
				s.handler.puller.Prewarm(redpandaImage)
			}
			s.handler.dockerWg.Add(1)
			go func() {
				defer s.handler.dockerWg.Done()
				bgCtx := context.Background()
				if err := s.handler.startClusterContainer(bgCtx, clusterARNCopy); err != nil {
					s.handler.log.Warn("failed to start Docker container for MSK V2 cluster — falling back to metadata-only",
						zap.String("cluster", clusterARNCopy), zap.Error(err))
					s.handler.clusterFallbackActive(clusterARNCopy)
				}
			}()
		} else {
			s.handler.scheduler.After(clusterARNCopy+":active", 0, func() {
				bgCtx := context.Background()
				got, aerr := s.handler.store.getCluster(bgCtx, clusterARNCopy)
				if aerr != nil {
					return
				}
				if got.State == "CREATING" {
					got.State = "ACTIVE"
					s.handler.store.putCluster(bgCtx, got) //nolint:errcheck
				}
			})
		}
	}

	return &createClusterV2Response{
		ClusterArn:  clusterARN,
		ClusterName: req.ClusterName,
		ClusterType: cluster.ClusterType,
		State:       cluster.State,
	}, nil
}

// --- DescribeClusterV2 ---

type describeClusterV2Request struct {
	ClusterArn string `json:"clusterArn" cbor:"clusterArn"`
}

type describeClusterV2Response struct {
	ClusterInfo *clusterV2Info `json:"clusterInfo" cbor:"clusterInfo"`
}

type clusterV2Info struct {
	ClusterArn     string            `json:"clusterArn" cbor:"clusterArn"`
	ClusterName    string            `json:"clusterName" cbor:"clusterName"`
	ClusterType    string            `json:"clusterType" cbor:"clusterType"`
	State          string            `json:"state" cbor:"state"`
	CreationTime   time.Time         `json:"creationTime" cbor:"creationTime"`
	CurrentVersion string            `json:"currentVersion" cbor:"currentVersion"`
	Tags           map[string]string `json:"tags,omitempty" cbor:"tags,omitempty"`
	Provisioned    *provisionedInfo  `json:"provisioned,omitempty" cbor:"provisioned,omitempty"`
	Serverless     *struct{}         `json:"serverless,omitempty" cbor:"serverless,omitempty"`
}

type provisionedInfo struct {
	NumberOfBrokerNodes       int                 `json:"numberOfBrokerNodes" cbor:"numberOfBrokerNodes"`
	BrokerNodeGroupInfo       BrokerNodeGroupInfo `json:"brokerNodeGroupInfo" cbor:"brokerNodeGroupInfo"`
	CurrentBrokerSoftwareInfo *brokerSoftwareInfo `json:"currentBrokerSoftwareInfo" cbor:"currentBrokerSoftwareInfo"`
}

type brokerSoftwareInfo struct {
	KafkaVersion string `json:"kafkaVersion" cbor:"kafkaVersion"`
}

func (s *Service) describeClusterV2Typed(ctx context.Context, req *describeClusterV2Request) (*describeClusterV2Response, *protocol.AWSError) {
	cluster, aerr := s.handler.store.getCluster(ctx, req.ClusterArn)
	if aerr != nil {
		return nil, aerr
	}

	clusterType := cluster.ClusterType
	if clusterType == "" {
		clusterType = "PROVISIONED"
	}

	info := &clusterV2Info{
		ClusterArn:     cluster.ClusterArn,
		ClusterName:    cluster.ClusterName,
		ClusterType:    clusterType,
		State:          cluster.State,
		CreationTime:   cluster.CreationTime,
		CurrentVersion: cluster.CurrentVersion,
		Tags:           cluster.Tags,
	}

	if clusterType == "PROVISIONED" {
		info.Provisioned = &provisionedInfo{
			NumberOfBrokerNodes: cluster.NumberOfBrokerNodes,
			BrokerNodeGroupInfo: cluster.BrokerNodeGroupInfo,
			CurrentBrokerSoftwareInfo: &brokerSoftwareInfo{
				KafkaVersion: cluster.KafkaVersion,
			},
		}
	} else {
		info.Serverless = &struct{}{}
	}

	return &describeClusterV2Response{ClusterInfo: info}, nil
}

// --- UpdateClusterConfiguration ---

type updateClusterConfigurationRequest struct {
	ClusterArn            string `json:"clusterArn" cbor:"clusterArn"`
	ConfigurationArn      string `json:"configurationArn" cbor:"configurationArn"`
	ConfigurationRevision int64  `json:"configurationRevision" cbor:"configurationRevision"`
	CurrentVersion        string `json:"currentVersion" cbor:"currentVersion"`
}

type updateClusterConfigurationResponse struct {
	ClusterArn          string `json:"clusterArn" cbor:"clusterArn"`
	ClusterOperationArn string `json:"clusterOperationArn" cbor:"clusterOperationArn"`
}

func (s *Service) updateClusterConfigurationTyped(ctx context.Context, req *updateClusterConfigurationRequest) (*updateClusterConfigurationResponse, *protocol.AWSError) {
	cluster, aerr := s.handler.store.getCluster(ctx, req.ClusterArn)
	if aerr != nil {
		return nil, aerr
	}

	if req.CurrentVersion != "" && req.CurrentVersion != cluster.CurrentVersion {
		return nil, &protocol.AWSError{
			Code:       "BadRequestException",
			Message:    "currentVersion does not match the cluster's current version",
			HTTPStatus: 400,
		}
	}

	if req.ConfigurationArn != "" {
		if _, aerr := s.handler.store.getConfiguration(ctx, req.ConfigurationArn); aerr != nil {
			return nil, aerr
		}
	}

	region := middleware.RegionFromContext(ctx, s.handler.cfg.Region)
	opID := uuid.NewString()
	opArn := fmt.Sprintf("arn:aws:kafka:%s:%s:cluster-operation/%s", region, s.handler.cfg.AccountID, opID)

	cluster.CurrentVersion = opID
	if aerr := s.handler.store.putCluster(ctx, cluster); aerr != nil {
		return nil, aerr
	}

	return &updateClusterConfigurationResponse{
		ClusterArn:          req.ClusterArn,
		ClusterOperationArn: opArn,
	}, nil
}
