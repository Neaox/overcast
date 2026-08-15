package ecs

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// ---- Request types ----

type createClusterRequest struct {
	ClusterName                     string                         `json:"clusterName" cbor:"clusterName"`
	CapacityProviders               []string                       `json:"capacityProviders" cbor:"capacityProviders"`
	DefaultCapacityProviderStrategy []CapacityProviderStrategyItem `json:"defaultCapacityProviderStrategy" cbor:"defaultCapacityProviderStrategy"`
}

type describeClustersRequest struct {
	Clusters []string `json:"clusters" cbor:"clusters"`
}

type deleteClusterRequest struct {
	Cluster string `json:"cluster" cbor:"cluster"`
}

type updateClusterRequest struct {
	Cluster       string `json:"cluster" cbor:"cluster"`
	Configuration *struct {
		ExecuteCommandConfiguration *struct {
			Logging string `json:"logging" cbor:"logging"`
		} `json:"executeCommandConfiguration" cbor:"executeCommandConfiguration"`
	} `json:"configuration" cbor:"configuration"`
}

type updateClusterSettingsRequest struct {
	Cluster  string `json:"cluster" cbor:"cluster"`
	Settings []struct {
		Name  string `json:"name" cbor:"name"`
		Value string `json:"value" cbor:"value"`
	} `json:"settings" cbor:"settings"`
}

type registerTaskDefinitionRequest struct {
	Family                  string                `json:"family" cbor:"family"`
	ContainerDefinitions    []ContainerDefinition `json:"containerDefinitions" cbor:"containerDefinitions"`
	NetworkMode             string                `json:"networkMode" cbor:"networkMode"`
	RequiresCompatibilities []string              `json:"requiresCompatibilities" cbor:"requiresCompatibilities"`
	Cpu                     string                `json:"cpu" cbor:"cpu"`
	Memory                  string                `json:"memory" cbor:"memory"`
	TaskRoleArn             string                `json:"taskRoleArn" cbor:"taskRoleArn"`
	ExecutionRoleArn        string                `json:"executionRoleArn" cbor:"executionRoleArn"`
	RuntimePlatform         *RuntimePlatform      `json:"runtimePlatform" cbor:"runtimePlatform"`
	EphemeralStorage        *EphemeralStorage     `json:"ephemeralStorage" cbor:"ephemeralStorage"`
	PidMode                 string                `json:"pidMode" cbor:"pidMode"`
	IpcMode                 string                `json:"ipcMode" cbor:"ipcMode"`
	Volumes                 []TaskVolume          `json:"volumes" cbor:"volumes"`
	Tags                    []Tag                 `json:"tags" cbor:"tags"`
}

// validateTaskVolumes rejects container mount points that reference a volume
// the task definition does not declare, matching AWS's ClientException, and
// enforces the volume-type rules AWS enforces at registration.
//
// fargate must be the task definition's own Fargate compatibility: two of the
// rules below exist only for Fargate, and getting them wrong in either
// direction is worse than not having them. Accepting what AWS rejects lets a
// task definition pass here and fail its first real deploy — the failure an
// emulator exists to prevent — so these are not gated by any flag.
func validateTaskVolumes(volumes []TaskVolume, containers []ContainerDefinition, fargate bool) *protocol.AWSError {
	names := make(map[string]struct{}, len(volumes))
	for _, v := range volumes {
		names[v.Name] = struct{}{}
	}
	for _, v := range volumes {
		if v.configCount() > 1 {
			return &protocol.AWSError{
				Code:       "ClientException",
				Message:    fmt.Sprintf("Volume '%s': only one volume configuration may be specified.", v.Name),
				HTTPStatus: http.StatusBadRequest,
			}
		}
		if fargate && v.Host != nil && v.Host.SourcePath != "" {
			// Verbatim from AWS. Fargate has no container instance to bind a
			// path from, and this is the message a real RegisterTaskDefinition
			// returns; callers switch on it, so it is reproduced exactly.
			return &protocol.AWSError{
				Code:       "ClientException",
				Message:    "host.sourcePath should not be set for volumes in Fargate",
				HTTPStatus: http.StatusBadRequest,
			}
		}
		if fargate && v.DockerVolumeConfiguration != nil {
			return &protocol.AWSError{
				Code:       "ClientException",
				Message:    fmt.Sprintf("Volume '%s': dockerVolumeConfiguration is not supported for tasks using the Fargate launch type.", v.Name),
				HTTPStatus: http.StatusBadRequest,
			}
		}
		if dvc := v.DockerVolumeConfiguration; dvc != nil {
			// AWS: a task-scoped volume is created and destroyed with the task,
			// so provisioning it ahead of time is meaningless rather than
			// merely redundant.
			if strings.EqualFold(dvc.Scope, "task") && dvc.Autoprovision {
				return &protocol.AWSError{
					Code:       "ClientException",
					Message:    fmt.Sprintf("Volume '%s': autoprovision must be omitted or false when scope is 'task'.", v.Name),
					HTTPStatus: http.StatusBadRequest,
				}
			}
			if dvc.Scope != "" && !strings.EqualFold(dvc.Scope, "task") && !strings.EqualFold(dvc.Scope, "shared") {
				return &protocol.AWSError{
					Code:       "ClientException",
					Message:    fmt.Sprintf("Volume '%s': scope must be 'task' or 'shared'.", v.Name),
					HTTPStatus: http.StatusBadRequest,
				}
			}
		}
		cfg := v.EFSVolumeConfiguration
		if cfg == nil || cfg.AuthorizationConfig == nil || cfg.AuthorizationConfig.AccessPointId == "" {
			continue
		}
		// AWS: an access point supplies the root directory, so rootDirectory
		// must be omitted or "/" when accessPointId is set.
		if cfg.RootDirectory != "" && cfg.RootDirectory != "/" {
			return &protocol.AWSError{
				Code:       "ClientException",
				Message:    fmt.Sprintf("Volume '%s': rootDirectory must be omitted or '/' when an access point is specified.", v.Name),
				HTTPStatus: http.StatusBadRequest,
			}
		}
	}
	for _, cd := range containers {
		for _, mp := range cd.MountPoints {
			if _, ok := names[mp.SourceVolume]; !ok {
				return &protocol.AWSError{
					Code:       "ClientException",
					Message:    fmt.Sprintf("Container '%s' references undefined volume '%s'.", cd.Name, mp.SourceVolume),
					HTTPStatus: http.StatusBadRequest,
				}
			}
		}
	}
	return nil
}

type taskDefinitionRefRequest struct {
	TaskDefinition string `json:"taskDefinition" cbor:"taskDefinition"`
}

type listTaskDefinitionsRequest struct {
	FamilyPrefix string `json:"familyPrefix" cbor:"familyPrefix"`
}

type listTaskDefinitionFamiliesRequest struct {
	FamilyPrefix string `json:"familyPrefix" cbor:"familyPrefix"`
	Status       string `json:"status" cbor:"status"`
}

type runTaskRequest struct {
	Cluster              string                `json:"cluster" cbor:"cluster"`
	TaskDefinition       string                `json:"taskDefinition" cbor:"taskDefinition"`
	Count                int                   `json:"count" cbor:"count"`
	LaunchType           string                `json:"launchType" cbor:"launchType"`
	NetworkConfiguration *NetworkConfiguration `json:"networkConfiguration" cbor:"networkConfiguration"`
	PlatformVersion      string                `json:"platformVersion" cbor:"platformVersion"`
	Overrides            *TaskOverride         `json:"overrides" cbor:"overrides"`
	Group                string                `json:"group" cbor:"group"`
	StartedBy            string                `json:"startedBy" cbor:"startedBy"`
}

type stopTaskRequest struct {
	Cluster string `json:"cluster" cbor:"cluster"`
	Task    string `json:"task" cbor:"task"`
	Reason  string `json:"reason" cbor:"reason"`
}

type describeTasksRequest struct {
	Cluster string   `json:"cluster" cbor:"cluster"`
	Tasks   []string `json:"tasks" cbor:"tasks"`
}

type listTasksRequest struct {
	Cluster       string `json:"cluster" cbor:"cluster"`
	DesiredStatus string `json:"desiredStatus" cbor:"desiredStatus"`
	Family        string `json:"family" cbor:"family"`
}

type createServiceRequest struct {
	Cluster                  string                         `json:"cluster" cbor:"cluster"`
	ServiceName              string                         `json:"serviceName" cbor:"serviceName"`
	TaskDefinition           string                         `json:"taskDefinition" cbor:"taskDefinition"`
	DesiredCount             *int                           `json:"desiredCount" cbor:"desiredCount"`
	LaunchType               string                         `json:"launchType" cbor:"launchType"`
	SchedulingStrategy       string                         `json:"schedulingStrategy" cbor:"schedulingStrategy"`
	NetworkConfiguration     *NetworkConfiguration          `json:"networkConfiguration" cbor:"networkConfiguration"`
	DeploymentController     *DeploymentController          `json:"deploymentController" cbor:"deploymentController"`
	DeploymentConfiguration  *DeploymentConfiguration       `json:"deploymentConfiguration" cbor:"deploymentConfiguration"`
	CapacityProviderStrategy []CapacityProviderStrategyItem `json:"capacityProviderStrategy" cbor:"capacityProviderStrategy"`
	LoadBalancers            []ServiceLoadBalancer          `json:"loadBalancers" cbor:"loadBalancers"`
	PlatformVersion          string                         `json:"platformVersion" cbor:"platformVersion"`

	HealthCheckGracePeriodSeconds *int              `json:"healthCheckGracePeriodSeconds" cbor:"healthCheckGracePeriodSeconds"`
	EnableExecuteCommand          bool              `json:"enableExecuteCommand" cbor:"enableExecuteCommand"`
	PropagateTags                 string            `json:"propagateTags" cbor:"propagateTags"`
	ServiceRegistries             []ServiceRegistry `json:"serviceRegistries" cbor:"serviceRegistries"`
	PlacementStrategy             []PlacementItem   `json:"placementStrategy" cbor:"placementStrategy"`
	PlacementConstraints          []PlacementItem   `json:"placementConstraints" cbor:"placementConstraints"`
}

type updateServiceRequest struct {
	Cluster              string                `json:"cluster" cbor:"cluster"`
	Service              string                `json:"service" cbor:"service"`
	TaskDefinition       string                `json:"taskDefinition" cbor:"taskDefinition"`
	DesiredCount         *int                  `json:"desiredCount" cbor:"desiredCount"`
	ForceNewDeployment   bool                  `json:"forceNewDeployment" cbor:"forceNewDeployment"`
	NetworkConfiguration *NetworkConfiguration `json:"networkConfiguration" cbor:"networkConfiguration"`
	PlatformVersion      string                `json:"platformVersion" cbor:"platformVersion"`
}

type deleteServiceRequest struct {
	Cluster string `json:"cluster" cbor:"cluster"`
	Service string `json:"service" cbor:"service"`
	Force   bool   `json:"force" cbor:"force"`
}

type describeServicesRequest struct {
	Cluster  string   `json:"cluster" cbor:"cluster"`
	Services []string `json:"services" cbor:"services"`
}

type listServicesRequest struct {
	Cluster    string `json:"cluster" cbor:"cluster"`
	LaunchType string `json:"launchType" cbor:"launchType"`
}

type tagResourceRequest struct {
	ResourceArn string `json:"resourceArn" cbor:"resourceArn"`
	Tags        []Tag  `json:"tags" cbor:"tags"`
}

type untagResourceRequest struct {
	ResourceArn string   `json:"resourceArn" cbor:"resourceArn"`
	TagKeys     []string `json:"tagKeys" cbor:"tagKeys"`
}

type listTagsForResourceRequest struct {
	ResourceArn string `json:"resourceArn" cbor:"resourceArn"`
}

type createCapacityProviderRequest struct {
	Name string `json:"name" cbor:"name"`
	Tags []Tag  `json:"tags" cbor:"tags"`
}

type describeCapacityProvidersRequest struct {
	CapacityProviders []string `json:"capacityProviders" cbor:"capacityProviders"`
}

type updateCapacityProviderRequest struct {
	Name string `json:"name" cbor:"name"`
}

type putClusterCapacityProvidersRequest struct {
	Cluster                         string                         `json:"cluster" cbor:"cluster"`
	CapacityProviders               []string                       `json:"capacityProviders" cbor:"capacityProviders"`
	DefaultCapacityProviderStrategy []CapacityProviderStrategyItem `json:"defaultCapacityProviderStrategy" cbor:"defaultCapacityProviderStrategy"`
}

type createTaskSetRequest struct {
	Cluster              string                `json:"cluster" cbor:"cluster"`
	Service              string                `json:"service" cbor:"service"`
	TaskDefinition       string                `json:"taskDefinition" cbor:"taskDefinition"`
	LaunchType           string                `json:"launchType" cbor:"launchType"`
	PlatformVersion      string                `json:"platformVersion" cbor:"platformVersion"`
	NetworkConfiguration *NetworkConfiguration `json:"networkConfiguration" cbor:"networkConfiguration"`
	Scale                *Scale                `json:"scale" cbor:"scale"`
	ExternalId           string                `json:"externalId" cbor:"externalId"`
	Tags                 []Tag                 `json:"tags" cbor:"tags"`
}

type updateTaskSetRequest struct {
	Cluster string `json:"cluster" cbor:"cluster"`
	Service string `json:"service" cbor:"service"`
	TaskSet string `json:"taskSet" cbor:"taskSet"`
	Scale   Scale  `json:"scale" cbor:"scale"`
}

type deleteTaskSetRequest struct {
	Cluster string `json:"cluster" cbor:"cluster"`
	Service string `json:"service" cbor:"service"`
	TaskSet string `json:"taskSet" cbor:"taskSet"`
	Force   bool   `json:"force" cbor:"force"`
}

type describeTaskSetsRequest struct {
	Cluster  string   `json:"cluster" cbor:"cluster"`
	Service  string   `json:"service" cbor:"service"`
	TaskSets []string `json:"taskSets" cbor:"taskSets"`
}

type updateServicePrimaryTaskSetRequest struct {
	Cluster        string `json:"cluster" cbor:"cluster"`
	Service        string `json:"service" cbor:"service"`
	PrimaryTaskSet string `json:"primaryTaskSet" cbor:"primaryTaskSet"`
}

type registerContainerInstanceRequest struct {
	Cluster       string `json:"cluster" cbor:"cluster"`
	Ec2InstanceId string `json:"ec2InstanceId" cbor:"ec2InstanceId"`
}

type deregisterContainerInstanceRequest struct {
	Cluster           string `json:"cluster" cbor:"cluster"`
	ContainerInstance string `json:"containerInstance" cbor:"containerInstance"`
	Force             bool   `json:"force" cbor:"force"`
}

type describeContainerInstancesRequest struct {
	Cluster            string   `json:"cluster" cbor:"cluster"`
	ContainerInstances []string `json:"containerInstances" cbor:"containerInstances"`
}

type listContainerInstancesRequest struct {
	Cluster    string `json:"cluster" cbor:"cluster"`
	Filter     string `json:"filter" cbor:"filter"`
	MaxResults *int   `json:"maxResults" cbor:"maxResults"`
	NextToken  string `json:"nextToken" cbor:"nextToken"`
	Status     string `json:"status" cbor:"status"`
}

type listAccountSettingsRequest struct {
	Name              string `json:"name" cbor:"name"`
	Value             string `json:"value" cbor:"value"`
	PrincipalArn      string `json:"principalArn" cbor:"principalArn"`
	EffectiveSettings bool   `json:"effectiveSettings" cbor:"effectiveSettings"`
}

type putAccountSettingRequest struct {
	Name  string `json:"name" cbor:"name"`
	Value string `json:"value" cbor:"value"`
}

type deleteAccountSettingRequest struct {
	Name         string `json:"name" cbor:"name"`
	PrincipalArn string `json:"principalArn" cbor:"principalArn"`
}

// ---- Response types ----

type createClusterResponse struct {
	Cluster Cluster `json:"cluster" cbor:"cluster"`
}

type describeClustersResponse struct {
	Clusters []Cluster        `json:"clusters" cbor:"clusters"`
	Failures []clusterFailure `json:"failures" cbor:"failures"`
}

type clusterFailure struct {
	Arn    string `json:"arn" cbor:"arn"`
	Reason string `json:"reason" cbor:"reason"`
}

type listClustersResponse struct {
	ClusterArns []string `json:"clusterArns" cbor:"clusterArns"`
}

type deleteClusterResponse struct {
	Cluster Cluster `json:"cluster" cbor:"cluster"`
}

type updateClusterResponse struct {
	Cluster Cluster `json:"cluster" cbor:"cluster"`
}

type updateClusterSettingsResponse struct {
	Cluster Cluster `json:"cluster" cbor:"cluster"`
}

type registerTaskDefinitionResponse struct {
	TaskDefinition TaskDefinition `json:"taskDefinition" cbor:"taskDefinition"`
}

type describeTaskDefinitionResponse struct {
	TaskDefinition TaskDefinition `json:"taskDefinition" cbor:"taskDefinition"`
}

type listTaskDefinitionsResponse struct {
	TaskDefinitionArns []string `json:"taskDefinitionArns" cbor:"taskDefinitionArns"`
}

type deregisterTaskDefinitionResponse struct {
	TaskDefinition TaskDefinition `json:"taskDefinition" cbor:"taskDefinition"`
}

type listTaskDefinitionFamiliesResponse struct {
	Families []string `json:"families" cbor:"families"`
}

type runTaskResponse struct {
	Tasks    []Task `json:"tasks" cbor:"tasks"`
	Failures []any  `json:"failures" cbor:"failures"`
}

type stopTaskResponse struct {
	Task Task `json:"task" cbor:"task"`
}

type describeTasksResponse struct {
	Tasks    []Task        `json:"tasks" cbor:"tasks"`
	Failures []taskFailure `json:"failures" cbor:"failures"`
}

type taskFailure struct {
	Arn    string `json:"arn" cbor:"arn"`
	Reason string `json:"reason" cbor:"reason"`
}

type listTasksResponse struct {
	TaskArns []string `json:"taskArns" cbor:"taskArns"`
}

type createServiceResponse struct {
	Service ecsService `json:"service" cbor:"service"`
}

type updateServiceResponse struct {
	Service ecsService `json:"service" cbor:"service"`
}

type deleteServiceResponse struct {
	Service ecsService `json:"service" cbor:"service"`
}

type describeServicesResponse struct {
	Services []ecsService     `json:"services" cbor:"services"`
	Failures []serviceFailure `json:"failures" cbor:"failures"`
}

type serviceFailure struct {
	Arn    string `json:"arn" cbor:"arn"`
	Reason string `json:"reason" cbor:"reason"`
}

type listServicesResponse struct {
	ServiceArns []string `json:"serviceArns" cbor:"serviceArns"`
}

type tagResourceResponse struct{}

type untagResourceResponse struct{}

type listTagsForResourceResponse struct {
	Tags []Tag `json:"tags" cbor:"tags"`
}

type createCapacityProviderResponse struct {
	CapacityProvider CapacityProvider `json:"capacityProvider" cbor:"capacityProvider"`
}

type describeCapacityProvidersResponse struct {
	CapacityProviders []CapacityProvider    `json:"capacityProviders" cbor:"capacityProviders"`
	Failures          []capacityFailureResp `json:"failures" cbor:"failures"`
}

type capacityFailureResp struct {
	Arn    string `json:"arn" cbor:"arn"`
	Reason string `json:"reason" cbor:"reason"`
}

type updateCapacityProviderResponse struct {
	CapacityProvider CapacityProvider `json:"capacityProvider" cbor:"capacityProvider"`
}

type putClusterCapacityProvidersResponse struct {
	Cluster Cluster `json:"cluster" cbor:"cluster"`
}

type createTaskSetResponse struct {
	TaskSet TaskSet `json:"taskSet" cbor:"taskSet"`
}

type updateTaskSetResponse struct {
	TaskSet TaskSet `json:"taskSet" cbor:"taskSet"`
}

type deleteTaskSetResponse struct {
	TaskSet TaskSet `json:"taskSet" cbor:"taskSet"`
}

type describeTaskSetsResponse struct {
	TaskSets []TaskSet `json:"taskSets" cbor:"taskSets"`
	Failures []failure `json:"failures" cbor:"failures"`
}

type failure struct {
	Arn    string `json:"arn" cbor:"arn"`
	Reason string `json:"reason" cbor:"reason"`
}

type updateServicePrimaryTaskSetResponse struct {
	TaskSet TaskSet `json:"taskSet" cbor:"taskSet"`
}

type registerContainerInstanceResponse struct {
	ContainerInstance ContainerInstance `json:"containerInstance" cbor:"containerInstance"`
}

type deregisterContainerInstanceResponse struct {
	ContainerInstance ContainerInstance `json:"containerInstance" cbor:"containerInstance"`
}

type describeContainerInstancesResponse struct {
	ContainerInstances []ContainerInstance `json:"containerInstances" cbor:"containerInstances"`
	Failures           []ciFailure         `json:"failures" cbor:"failures"`
}

type ciFailure struct {
	Arn    string `json:"arn" cbor:"arn"`
	Reason string `json:"reason" cbor:"reason"`
}

type listContainerInstancesResponse struct {
	ContainerInstanceArns []string `json:"containerInstanceArns" cbor:"containerInstanceArns"`
}

type listAccountSettingsResponse struct {
	Settings []accountSettingWire `json:"settings" cbor:"settings"`
}

type accountSettingWire struct {
	Name         string `json:"name" cbor:"name"`
	Value        string `json:"value" cbor:"value"`
	PrincipalArn string `json:"principalArn" cbor:"principalArn"`
}

type putAccountSettingResponse struct {
	Setting AccountSetting `json:"setting" cbor:"setting"`
}

type deleteAccountSettingResponse struct {
	Setting AccountSetting `json:"setting" cbor:"setting"`
}

// ---- Typed handler functions ----

func (h *Handler) createClusterTyped(ctx context.Context, req *createClusterRequest) (*createClusterResponse, *protocol.AWSError) {
	h.ensureBuiltinProviders(ctx)
	if req.ClusterName == "" {
		req.ClusterName = "default"
	}
	c := &Cluster{
		ClusterName:                       req.ClusterName,
		ClusterArn:                        h.clusterARN(ctx, req.ClusterName),
		Status:                            "ACTIVE",
		RegisteredContainerInstancesCount: 0,
		RunningTasksCount:                 0,
		PendingTasksCount:                 0,
		ActiveServicesCount:               0,
		CapacityProviders:                 req.CapacityProviders,
		DefaultCapacityProviderStrategy:   req.DefaultCapacityProviderStrategy,
	}
	if aerr := h.store.putCluster(ctx, c); aerr != nil {
		return nil, aerr
	}
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.ECSClusterCreated, Payload: events.ResourcePayload{Name: c.ClusterName}})
	}
	return &createClusterResponse{Cluster: *c}, nil
}

func (h *Handler) describeClustersTyped(ctx context.Context, req *describeClustersRequest) (*describeClustersResponse, *protocol.AWSError) {
	found := make([]Cluster, 0, len(req.Clusters))
	failures := make([]clusterFailure, 0)
	for _, ref := range req.Clusters {
		name := extractClusterName(ref)
		c, aerr := h.store.getCluster(ctx, name)
		if aerr != nil {
			failures = append(failures, clusterFailure{
				Arn:    h.clusterARN(ctx, name),
				Reason: "MISSING",
			})
			continue
		}
		if aerr := h.refreshClusterCounts(ctx, c); aerr != nil {
			return nil, aerr
		}
		found = append(found, *c)
	}
	return &describeClustersResponse{Clusters: found, Failures: failures}, nil
}

func (h *Handler) listClustersTyped(ctx context.Context, _ *struct{}) (*listClustersResponse, *protocol.AWSError) {
	clusters, aerr := h.store.listClusters(ctx)
	if aerr != nil {
		return nil, aerr
	}
	arns := make([]string, 0, len(clusters))
	for _, c := range clusters {
		arns = append(arns, c.ClusterArn)
	}
	return &listClustersResponse{ClusterArns: arns}, nil
}

func (h *Handler) deleteClusterTyped(ctx context.Context, req *deleteClusterRequest) (*deleteClusterResponse, *protocol.AWSError) {
	name := extractClusterName(req.Cluster)
	c, aerr := h.store.getCluster(ctx, name)
	if aerr != nil {
		return nil, aerr
	}
	c.Status = "INACTIVE"
	if aerr := h.store.deleteCluster(ctx, name); aerr != nil {
		return nil, aerr
	}
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.ECSClusterDeleted, Payload: events.ResourcePayload{Name: name}})
	}
	return &deleteClusterResponse{Cluster: *c}, nil
}

func (h *Handler) updateClusterTyped(ctx context.Context, req *updateClusterRequest) (*updateClusterResponse, *protocol.AWSError) {
	name := extractClusterName(req.Cluster)
	c, aerr := h.store.getCluster(ctx, name)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := h.store.putCluster(ctx, c); aerr != nil {
		return nil, aerr
	}
	return &updateClusterResponse{Cluster: *c}, nil
}

func (h *Handler) updateClusterSettingsTyped(ctx context.Context, req *updateClusterSettingsRequest) (*updateClusterSettingsResponse, *protocol.AWSError) {
	name := extractClusterName(req.Cluster)
	c, aerr := h.store.getCluster(ctx, name)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := h.store.putCluster(ctx, c); aerr != nil {
		return nil, aerr
	}
	return &updateClusterSettingsResponse{Cluster: *c}, nil
}

func (h *Handler) registerTaskDefinitionTyped(ctx context.Context, req *registerTaskDefinitionRequest) (*registerTaskDefinitionResponse, *protocol.AWSError) {
	if req.Family == "" {
		return nil, &protocol.AWSError{
			Code: "ClientException", Message: "Family is required.", HTTPStatus: http.StatusBadRequest,
		}
	}
	if isFargate(req.RequiresCompatibilities) {
		if req.NetworkMode != "" && req.NetworkMode != "awsvpc" {
			return nil, &protocol.AWSError{
				Code: "ClientException", Message: "FARGATE requires networkMode to be 'awsvpc'.", HTTPStatus: http.StatusBadRequest,
			}
		}
		if req.Cpu == "" || req.Memory == "" {
			return nil, &protocol.AWSError{
				Code: "ClientException", Message: "FARGATE requires both cpu and memory to be specified at the task level.", HTTPStatus: http.StatusBadRequest,
			}
		}
		if err := validateFargateCPUMemory(req.Cpu, req.Memory); err != nil {
			return nil, &protocol.AWSError{
				Code: "ClientException", Message: err.Error(), HTTPStatus: http.StatusBadRequest,
			}
		}
		if req.NetworkMode == "" {
			req.NetworkMode = "awsvpc"
		}
	}
	if aerr := validateTaskVolumes(req.Volumes, req.ContainerDefinitions, isFargate(req.RequiresCompatibilities)); aerr != nil {
		return nil, aerr
	}
	rev, aerr := h.store.nextRevision(ctx, req.Family)
	if aerr != nil {
		return nil, aerr
	}
	td := &TaskDefinition{
		TaskDefinitionArn:       h.taskDefinitionARN(ctx, req.Family, rev),
		Family:                  req.Family,
		Revision:                rev,
		Status:                  "ACTIVE",
		NetworkMode:             req.NetworkMode,
		RequiresCompatibilities: req.RequiresCompatibilities,
		Cpu:                     req.Cpu,
		Memory:                  req.Memory,
		ContainerDefinitions:    req.ContainerDefinitions,
		Volumes:                 req.Volumes,
		TaskRoleArn:             req.TaskRoleArn,
		ExecutionRoleArn:        req.ExecutionRoleArn,
		RuntimePlatform:         req.RuntimePlatform,
		EphemeralStorage:        req.EphemeralStorage,
		PidMode:                 req.PidMode,
		IpcMode:                 req.IpcMode,
	}
	if aerr := h.store.putTaskDefinition(ctx, td); aerr != nil {
		return nil, aerr
	}
	if aerr := h.storeTaskDefinitionTags(ctx, td.TaskDefinitionArn, req.Tags); aerr != nil {
		return nil, aerr
	}
	if h.puller != nil {
		for _, c := range td.ContainerDefinitions {
			h.puller.Prewarm(c.Image)
		}
	}
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.ECSTaskDefinitionRegistered, Payload: events.ResourcePayload{Name: req.Family}})
	}
	return &registerTaskDefinitionResponse{TaskDefinition: *td}, nil
}

func (h *Handler) describeTaskDefinitionTyped(ctx context.Context, req *taskDefinitionRefRequest) (*describeTaskDefinitionResponse, *protocol.AWSError) {
	if req.TaskDefinition == "" {
		return nil, &protocol.AWSError{
			Code: "ClientException", Message: "taskDefinition is required.", HTTPStatus: http.StatusBadRequest,
		}
	}
	family, revision, hasRevision := parseTaskDefRef(req.TaskDefinition)
	var td *TaskDefinition
	var aerr *protocol.AWSError
	if hasRevision {
		td, aerr = h.store.getTaskDefinition(ctx, family, revision)
	} else {
		td, aerr = h.store.getLatestTaskDefinition(ctx, family)
	}
	if aerr != nil {
		return nil, aerr
	}
	return &describeTaskDefinitionResponse{TaskDefinition: *td}, nil
}

func (h *Handler) listTaskDefinitionsTyped(ctx context.Context, req *listTaskDefinitionsRequest) (*listTaskDefinitionsResponse, *protocol.AWSError) {
	var defs []TaskDefinition
	var aerr *protocol.AWSError
	if req.FamilyPrefix != "" {
		defs, aerr = h.store.listTaskDefinitionsByFamily(ctx, req.FamilyPrefix)
	} else {
		defs, aerr = h.store.listTaskDefinitions(ctx)
	}
	if aerr != nil {
		return nil, aerr
	}
	arns := make([]string, 0, len(defs))
	for _, td := range defs {
		arns = append(arns, td.TaskDefinitionArn)
	}
	return &listTaskDefinitionsResponse{TaskDefinitionArns: arns}, nil
}

func (h *Handler) deregisterTaskDefinitionTyped(ctx context.Context, req *taskDefinitionRefRequest) (*deregisterTaskDefinitionResponse, *protocol.AWSError) {
	if req.TaskDefinition == "" {
		return nil, &protocol.AWSError{
			Code: "ClientException", Message: "taskDefinition is required.", HTTPStatus: http.StatusBadRequest,
		}
	}
	family, revision, hasRevision := parseTaskDefRef(req.TaskDefinition)
	if !hasRevision {
		return nil, &protocol.AWSError{
			Code: "ClientException", Message: "taskDefinition must include a revision (family:revision).", HTTPStatus: http.StatusBadRequest,
		}
	}
	td, aerr := h.store.getTaskDefinition(ctx, family, revision)
	if aerr != nil {
		return nil, aerr
	}
	td.Status = "INACTIVE"
	if aerr := h.store.putTaskDefinition(ctx, td); aerr != nil {
		return nil, aerr
	}
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.ECSTaskDefinitionDeregistered, Payload: events.ResourcePayload{Name: family}})
	}
	return &deregisterTaskDefinitionResponse{TaskDefinition: *td}, nil
}

func (h *Handler) listTaskDefinitionFamiliesTyped(ctx context.Context, req *listTaskDefinitionFamiliesRequest) (*listTaskDefinitionFamiliesResponse, *protocol.AWSError) {
	families, aerr := h.store.listTaskDefinitionFamilies(ctx)
	if aerr != nil {
		return nil, aerr
	}
	filtered := make([]string, 0, len(families))
	for _, f := range families {
		if req.FamilyPrefix != "" && !strings.HasPrefix(f, req.FamilyPrefix) {
			continue
		}
		filtered = append(filtered, f)
	}
	return &listTaskDefinitionFamiliesResponse{Families: filtered}, nil
}

func (h *Handler) runTaskTyped(ctx context.Context, req *runTaskRequest) (*runTaskResponse, *protocol.AWSError) {
	if req.Cluster == "" {
		req.Cluster = "default"
	}
	if req.Count < 1 {
		req.Count = 1
	}
	clusterName := extractClusterName(req.Cluster)
	cluster, aerr := h.store.getCluster(ctx, clusterName)
	if aerr != nil {
		return nil, aerr
	}
	family, revision, hasRevision := parseTaskDefRef(req.TaskDefinition)
	var td *TaskDefinition
	if hasRevision {
		td, aerr = h.store.getTaskDefinition(ctx, family, revision)
	} else {
		td, aerr = h.store.getLatestTaskDefinition(ctx, family)
	}
	if aerr != nil {
		return nil, aerr
	}
	// awsvpc networking is required by the task definition's networkMode, so
	// this can only be checked once the task definition has been resolved.
	if aerr := validateAwsvpcNetworkConfiguration(td, req.LaunchType, req.NetworkConfiguration); aerr != nil {
		return nil, aerr
	}
	platformVersion := req.PlatformVersion
	if platformVersion == "" && req.LaunchType == "FARGATE" {
		platformVersion = "LATEST"
	}
	placement, placementErr := h.resolveAwsvpcPlacement(ctx, req.NetworkConfiguration, "awsvpc tasks")
	if placementErr != nil {
		return nil, placementErr
	}
	tasks := make([]Task, 0, req.Count)
	for i := 0; i < req.Count; i++ {
		task, aerr, _ := h.launchTask(ctx, taskLaunchSpec{
			clusterName:     clusterName,
			clusterArn:      cluster.ClusterArn,
			td:              td,
			launchType:      req.LaunchType,
			platformVersion: platformVersion,
			group:           req.Group,
			startedBy:       req.StartedBy,
			overrides:       req.Overrides,
			netCfg:          req.NetworkConfiguration,
			placement:       placement,
			ordinal:         i,
		})
		if aerr != nil {
			return nil, aerr
		}
		tasks = append(tasks, *task)
	}
	return &runTaskResponse{Tasks: tasks, Failures: []any{}}, nil
}

func (h *Handler) stopTaskTyped(ctx context.Context, req *stopTaskRequest) (*stopTaskResponse, *protocol.AWSError) {
	if req.Cluster == "" {
		req.Cluster = "default"
	}
	clusterName := extractClusterName(req.Cluster)
	taskID := extractTaskID(req.Task)
	task, aerr := h.store.getTask(ctx, clusterName, taskID)
	if aerr != nil {
		return nil, aerr
	}
	if task == nil {
		return nil, &protocol.AWSError{
			Code: "InvalidParameterException", Message: "The referenced task was not found.", HTTPStatus: http.StatusBadRequest,
		}
	}
	h.scheduler.CancelScoped(h.store.region(ctx), taskID, "pending")
	if h.dockerReady.Load() {
		for _, c := range task.Containers {
			if c.DockerID == "" {
				continue
			}
			if h.gc != nil {
				// The GC captures the container's output before it removes
				// it — see SetBeforeRemove, wired in SetDocker.
				h.gc.StopNow(c.DockerID)
				h.gc.ScheduleRemove(c.DockerID)
			} else {
				// Without one, this path owns the ordering itself, as
				// retireTaskContainers does: stop, capture, then remove.
				_ = h.docker.StopContainer(ctx, c.DockerID, 10)
				h.captureContainerLogs(ctx, task, c.DockerID)
				if !h.cfg.ECSKeepContainers {
					_ = h.docker.RemoveContainerForce(c.DockerID)
				}
			}
		}
	}
	task, _, aerr = h.stopTaskRecord(ctx, clusterName, taskID, taskStop{
		reason: req.Reason, code: "UserInitiated",
	})
	if aerr != nil {
		return nil, aerr
	}
	if task == nil {
		return nil, &protocol.AWSError{
			Code: "InvalidParameterException", Message: "The referenced task was not found.", HTTPStatus: http.StatusBadRequest,
		}
	}
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.ECSTaskStopped, Payload: events.ResourcePayload{Name: taskID}})
	}
	return &stopTaskResponse{Task: *task}, nil
}

func (h *Handler) describeTasksTyped(ctx context.Context, req *describeTasksRequest) (*describeTasksResponse, *protocol.AWSError) {
	if req.Cluster == "" {
		req.Cluster = "default"
	}
	clusterName := extractClusterName(req.Cluster)
	found := make([]Task, 0, len(req.Tasks))
	failures := make([]taskFailure, 0)
	for _, ref := range req.Tasks {
		taskID := extractTaskID(ref)
		task, aerr := h.getVisibleTask(ctx, clusterName, taskID)
		if aerr != nil {
			return nil, aerr
		}
		if task == nil {
			arn := ref
			if !strings.HasPrefix(arn, "arn:") {
				arn = h.taskARN(ctx, clusterName, taskID)
			}
			failures = append(failures, taskFailure{Arn: arn, Reason: "MISSING"})
			continue
		}
		found = append(found, *task)
	}
	return &describeTasksResponse{Tasks: found, Failures: failures}, nil
}

func (h *Handler) listTasksTyped(ctx context.Context, req *listTasksRequest) (*listTasksResponse, *protocol.AWSError) {
	if req.Cluster == "" {
		req.Cluster = "default"
	}
	clusterName := extractClusterName(req.Cluster)
	tasks, aerr := h.listVisibleTasks(ctx, clusterName)
	if aerr != nil {
		return nil, aerr
	}
	desiredStatus := req.DesiredStatus
	if desiredStatus == "" {
		desiredStatus = "RUNNING"
	}
	arns := make([]string, 0, len(tasks))
	for _, t := range tasks {
		if t.DesiredStatus != desiredStatus {
			continue
		}
		if req.Family != "" && !strings.Contains(t.TaskDefinitionArn, "/"+req.Family+":") {
			continue
		}
		arns = append(arns, t.TaskArn)
	}
	return &listTasksResponse{TaskArns: arns}, nil
}

func (h *Handler) createServiceTyped(ctx context.Context, req *createServiceRequest) (*createServiceResponse, *protocol.AWSError) {
	if req.Cluster == "" {
		req.Cluster = "default"
	}
	if req.SchedulingStrategy == "" {
		req.SchedulingStrategy = "REPLICA"
	}
	if req.DeploymentController == nil {
		req.DeploymentController = &DeploymentController{Type: "ECS"}
	}
	clusterName := extractClusterName(req.Cluster)
	cluster, aerr := h.store.getCluster(ctx, clusterName)
	if aerr != nil {
		return nil, aerr
	}
	if req.ServiceName == "" {
		return nil, &protocol.AWSError{
			Code: "InvalidParameterException", Message: "1 validation error detected: Value at 'serviceName' failed to satisfy constraint: Member must not be null", HTTPStatus: http.StatusBadRequest,
		}
	}
	if req.TaskDefinition == "" {
		return nil, &protocol.AWSError{
			Code: "InvalidParameterException", Message: "taskDefinition must be specified when creating a service.", HTTPStatus: http.StatusBadRequest,
		}
	}
	family, revision, hasRevision := parseTaskDefRef(req.TaskDefinition)
	var td *TaskDefinition
	if hasRevision {
		td, aerr = h.store.getTaskDefinition(ctx, family, revision)
	} else {
		td, aerr = h.store.getLatestTaskDefinition(ctx, family)
	}
	if aerr != nil {
		return nil, aerr
	}
	// awsvpc networking is required by the task definition's networkMode, so
	// this can only be checked once the task definition has been resolved.
	if aerr := validateAwsvpcNetworkConfiguration(td, req.LaunchType, req.NetworkConfiguration); aerr != nil {
		return nil, aerr
	}
	if _, placementErr := h.resolveAwsvpcPlacement(ctx, req.NetworkConfiguration, "awsvpc services"); placementErr != nil {
		return nil, placementErr
	}
	existing, _ := h.store.getService(ctx, clusterName, req.ServiceName)
	if existing != nil && existing.Status == "ACTIVE" {
		return nil, &protocol.AWSError{
			Code: "InvalidParameterException", Message: "Creation of service was not idempotent.", HTTPStatus: http.StatusBadRequest,
		}
	}
	desired := 0
	if req.DesiredCount != nil {
		desired = *req.DesiredCount
	}
	platformVersion := req.PlatformVersion
	if platformVersion == "" && req.LaunchType == "FARGATE" {
		platformVersion = "LATEST"
	}
	now := h.clk.Now().Unix()
	svc := &ecsService{
		ServiceName:              req.ServiceName,
		ServiceArn:               h.serviceARN(ctx, clusterName, req.ServiceName),
		ClusterArn:               cluster.ClusterArn,
		TaskDefinition:           td.TaskDefinitionArn,
		DesiredCount:             desired,
		RunningCount:             0,
		PendingCount:             0,
		Status:                   "ACTIVE",
		LaunchType:               req.LaunchType,
		CreatedAt:                now,
		SchedulingStrategy:       req.SchedulingStrategy,
		NetworkConfiguration:     req.NetworkConfiguration,
		DeploymentController:     req.DeploymentController,
		DeploymentConfiguration:  req.DeploymentConfiguration,
		LoadBalancers:            req.LoadBalancers,
		CapacityProviderStrategy: req.CapacityProviderStrategy,

		HealthCheckGracePeriodSeconds: req.HealthCheckGracePeriodSeconds,
		EnableExecuteCommand:          req.EnableExecuteCommand,
		PropagateTags:                 req.PropagateTags,
		ServiceRegistries:             req.ServiceRegistries,
		PlacementStrategy:             req.PlacementStrategy,
		PlacementConstraints:          req.PlacementConstraints,
		PlatformVersion:               platformVersion,
		Events:                        make([]ServiceEvent, 0),
		Deployments:                   []Deployment{newPrimaryDeployment(td.TaskDefinitionArn, desired, now, platformVersion, req.NetworkConfiguration, req.DeploymentController)},
	}
	if aerr := h.store.putService(ctx, clusterName, svc); aerr != nil {
		return nil, aerr
	}
	h.reconcile(ctx, clusterName, req.ServiceName)
	svc, _ = h.store.getService(ctx, clusterName, req.ServiceName)
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.ECSServiceCreated, Payload: events.ResourcePayload{Name: req.ServiceName}})
	}
	return &createServiceResponse{Service: *svc}, nil
}

func (h *Handler) updateServiceTyped(ctx context.Context, req *updateServiceRequest) (*updateServiceResponse, *protocol.AWSError) {
	svc, aerr := h.updateServiceRecord(ctx, req)
	if aerr != nil {
		return nil, aerr
	}
	serviceName := extractServiceName(req.Service)
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.ECSServiceUpdated, Payload: events.ResourcePayload{Name: serviceName}})
	}
	return &updateServiceResponse{Service: *svc}, nil
}

func (h *Handler) deleteServiceTyped(ctx context.Context, req *deleteServiceRequest) (*deleteServiceResponse, *protocol.AWSError) {
	svc, aerr := h.drainServiceRecord(ctx, req.Cluster, req.Service)
	if aerr != nil {
		return nil, aerr
	}
	serviceName := extractServiceName(req.Service)
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.ECSServiceDeleted, Payload: events.ResourcePayload{Name: serviceName}})
	}
	return &deleteServiceResponse{Service: *svc}, nil
}

func (h *Handler) describeServicesTyped(ctx context.Context, req *describeServicesRequest) (*describeServicesResponse, *protocol.AWSError) {
	if req.Cluster == "" {
		req.Cluster = "default"
	}
	clusterName := extractClusterName(req.Cluster)
	found := make([]ecsService, 0, len(req.Services))
	failures := make([]serviceFailure, 0)
	for _, ref := range req.Services {
		name := extractServiceName(ref)
		svc, aerr := h.store.getService(ctx, clusterName, name)
		if aerr != nil {
			arn := ref
			if !strings.HasPrefix(arn, "arn:") {
				arn = h.serviceARN(ctx, clusterName, name)
			}
			failures = append(failures, serviceFailure{Arn: arn, Reason: "MISSING"})
			continue
		}
		h.refreshServiceCounts(ctx, clusterName, svc)
		found = append(found, *svc)
	}
	return &describeServicesResponse{Services: found, Failures: failures}, nil
}

func (h *Handler) listServicesTyped(ctx context.Context, req *listServicesRequest) (*listServicesResponse, *protocol.AWSError) {
	if req.Cluster == "" {
		req.Cluster = "default"
	}
	clusterName := extractClusterName(req.Cluster)
	services, aerr := h.store.listServices(ctx, clusterName)
	if aerr != nil {
		return nil, aerr
	}
	arns := make([]string, 0, len(services))
	for _, s := range services {
		if req.LaunchType != "" && s.LaunchType != req.LaunchType {
			continue
		}
		arns = append(arns, s.ServiceArn)
	}
	return &listServicesResponse{ServiceArns: arns}, nil
}

func (h *Handler) tagResourceTyped(ctx context.Context, req *tagResourceRequest) (*tagResourceResponse, *protocol.AWSError) {
	if req.ResourceArn == "" {
		return nil, &protocol.AWSError{
			Code: "InvalidParameterException", Message: "resourceArn must not be null", HTTPStatus: http.StatusBadRequest,
		}
	}
	region := h.store.region(ctx)
	pairs := make([]serviceutil.TagPair, len(req.Tags))
	for i, t := range req.Tags {
		pairs[i] = serviceutil.TagPair{Key: t.Key, Value: t.Value}
	}
	if _, aerr := serviceutil.ApplyTagsToStore(ctx, ecsTagCfg, nsTags, serviceutil.RegionKey(region, req.ResourceArn), pairs, h.store.store); aerr != nil {
		return nil, aerr
	}
	return &tagResourceResponse{}, nil
}

func (h *Handler) untagResourceTyped(ctx context.Context, req *untagResourceRequest) (*untagResourceResponse, *protocol.AWSError) {
	if req.ResourceArn == "" {
		return nil, &protocol.AWSError{
			Code: "InvalidParameterException", Message: "resourceArn must not be null", HTTPStatus: http.StatusBadRequest,
		}
	}
	region := h.store.region(ctx)
	if _, aerr := serviceutil.RemoveTagsFromStore(ctx, nsTags, serviceutil.RegionKey(region, req.ResourceArn), req.TagKeys, h.store.store); aerr != nil {
		return nil, aerr
	}
	return &untagResourceResponse{}, nil
}

func (h *Handler) listTagsForResourceTyped(ctx context.Context, req *listTagsForResourceRequest) (*listTagsForResourceResponse, *protocol.AWSError) {
	if req.ResourceArn == "" {
		return nil, &protocol.AWSError{
			Code: "InvalidParameterException", Message: "resourceArn must not be null", HTTPStatus: http.StatusBadRequest,
		}
	}
	region := h.store.region(ctx)
	existing, aerr := serviceutil.TagsFromStore(ctx, h.store.store, nsTags, serviceutil.RegionKey(region, req.ResourceArn))
	if aerr != nil {
		return nil, aerr
	}
	tags := make([]Tag, 0, len(existing))
	for k, v := range existing {
		tags = append(tags, Tag{Key: k, Value: v})
	}
	return &listTagsForResourceResponse{Tags: tags}, nil
}

func (h *Handler) createCapacityProviderTyped(ctx context.Context, req *createCapacityProviderRequest) (*createCapacityProviderResponse, *protocol.AWSError) {
	h.ensureBuiltinProviders(ctx)
	if req.Name == "" {
		return nil, &protocol.AWSError{
			Code: "InvalidParameterException", Message: "The name must not be null or empty.", HTTPStatus: http.StatusBadRequest,
		}
	}
	if strings.HasPrefix(req.Name, "FARGATE") {
		return nil, &protocol.AWSError{
			Code: "InvalidParameterException", Message: "The capacity provider name prefix 'FARGATE' is reserved.", HTTPStatus: http.StatusBadRequest,
		}
	}
	existing, aerr := h.store.getCapacityProvider(ctx, req.Name)
	if aerr != nil {
		return nil, aerr
	}
	if existing != nil {
		return nil, &protocol.AWSError{
			Code: "InvalidParameterException", Message: fmt.Sprintf("A capacity provider with the name '%s' already exists.", req.Name), HTTPStatus: http.StatusBadRequest,
		}
	}
	cp := &CapacityProvider{
		CapacityProviderArn: h.capacityProviderARN(ctx, req.Name),
		Name:                req.Name,
		Status:              "ACTIVE",
		Tags:                req.Tags,
	}
	if aerr := h.store.putCapacityProvider(ctx, cp); aerr != nil {
		return nil, aerr
	}
	return &createCapacityProviderResponse{CapacityProvider: *cp}, nil
}

func (h *Handler) describeCapacityProvidersTyped(ctx context.Context, req *describeCapacityProvidersRequest) (*describeCapacityProvidersResponse, *protocol.AWSError) {
	h.ensureBuiltinProviders(ctx)
	var cps []CapacityProvider
	var failures []capacityFailureResp
	if len(req.CapacityProviders) == 0 {
		var aerr *protocol.AWSError
		cps, aerr = h.store.listCapacityProviders(ctx)
		if aerr != nil {
			return nil, aerr
		}
	} else {
		cps = make([]CapacityProvider, 0, len(req.CapacityProviders))
		failures = make([]capacityFailureResp, 0)
		for _, name := range req.CapacityProviders {
			cp, aerr := h.store.getCapacityProvider(ctx, name)
			if aerr != nil {
				return nil, aerr
			}
			if cp == nil {
				failures = append(failures, capacityFailureResp{
					Arn:    h.capacityProviderARN(ctx, name),
					Reason: "MISSING",
				})
				continue
			}
			cps = append(cps, *cp)
		}
	}
	return &describeCapacityProvidersResponse{CapacityProviders: cps, Failures: failures}, nil
}

func (h *Handler) updateCapacityProviderTyped(ctx context.Context, req *updateCapacityProviderRequest) (*updateCapacityProviderResponse, *protocol.AWSError) {
	h.ensureBuiltinProviders(ctx)
	if req.Name == "" {
		return nil, &protocol.AWSError{
			Code: "InvalidParameterException", Message: "The name must not be null or empty.", HTTPStatus: http.StatusBadRequest,
		}
	}
	cp, aerr := h.store.getCapacityProvider(ctx, req.Name)
	if aerr != nil {
		return nil, aerr
	}
	if cp == nil {
		return nil, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: fmt.Sprintf("Capacity provider not found: %s", req.Name), HTTPStatus: http.StatusBadRequest,
		}
	}
	if cp.Name == "FARGATE" || cp.Name == "FARGATE_SPOT" {
		return nil, &protocol.AWSError{
			Code: "ClientException", Message: fmt.Sprintf("The capacity provider '%s' is a managed capacity provider and cannot be updated.", cp.Name), HTTPStatus: http.StatusBadRequest,
		}
	}
	if aerr := h.store.putCapacityProvider(ctx, cp); aerr != nil {
		return nil, aerr
	}
	return &updateCapacityProviderResponse{CapacityProvider: *cp}, nil
}

func (h *Handler) putClusterCapacityProvidersTyped(ctx context.Context, req *putClusterCapacityProvidersRequest) (*putClusterCapacityProvidersResponse, *protocol.AWSError) {
	h.ensureBuiltinProviders(ctx)
	clusterName := extractClusterName(req.Cluster)
	cluster, aerr := h.store.getCluster(ctx, clusterName)
	if aerr != nil {
		return nil, aerr
	}
	cluster.CapacityProviders = req.CapacityProviders
	cluster.DefaultCapacityProviderStrategy = req.DefaultCapacityProviderStrategy
	if aerr := h.store.putCluster(ctx, cluster); aerr != nil {
		return nil, aerr
	}
	return &putClusterCapacityProvidersResponse{Cluster: *cluster}, nil
}

func (h *Handler) createTaskSetTyped(ctx context.Context, req *createTaskSetRequest) (*createTaskSetResponse, *protocol.AWSError) {
	if req.Cluster == "" {
		req.Cluster = "default"
	}
	clusterName := extractClusterName(req.Cluster)
	serviceName := extractServiceName(req.Service)
	cluster, aerr := h.store.getCluster(ctx, clusterName)
	if aerr != nil {
		return nil, aerr
	}
	svc, aerr := h.store.getService(ctx, clusterName, serviceName)
	if aerr != nil {
		return nil, aerr
	}
	if svc.DeploymentController == nil || svc.DeploymentController.Type == "ECS" {
		return nil, &protocol.AWSError{
			Code: "InvalidParameterException", Message: "Task sets can only be created for services using the CODE_DEPLOY or EXTERNAL deployment controller.", HTTPStatus: http.StatusBadRequest,
		}
	}
	family, revision, hasRevision := parseTaskDefRef(req.TaskDefinition)
	var td *TaskDefinition
	if hasRevision {
		td, aerr = h.store.getTaskDefinition(ctx, family, revision)
	} else {
		td, aerr = h.store.getLatestTaskDefinition(ctx, family)
	}
	if aerr != nil {
		return nil, aerr
	}
	scale := Scale{Unit: "PERCENT", Value: 100}
	if req.Scale != nil {
		scale = *req.Scale
	}
	platformVersion := req.PlatformVersion
	if platformVersion == "" {
		platformVersion = "LATEST"
	}
	now := h.clk.Now().Unix()
	tsID := uuid.New().String()
	ts := &TaskSet{
		Id:                   tsID,
		TaskSetArn:           h.taskSetARN(ctx, clusterName, serviceName, tsID),
		ServiceArn:           svc.ServiceArn,
		ClusterArn:           cluster.ClusterArn,
		TaskDefinition:       td.TaskDefinitionArn,
		Status:               "ACTIVE",
		LaunchType:           req.LaunchType,
		PlatformVersion:      platformVersion,
		NetworkConfiguration: req.NetworkConfiguration,
		Scale:                scale,
		StabilityStatus:      "STABILIZING",
		CreatedAt:            now,
		UpdatedAt:            now,
		ExternalId:           req.ExternalId,
		Tags:                 req.Tags,
		ComputedDesiredCount: computeDesiredCount(scale, svc.DesiredCount),
	}
	if aerr := h.store.putTaskSet(ctx, clusterName, serviceName, ts); aerr != nil {
		return nil, aerr
	}
	h.registerServiceTaskSet(ctx, clusterName, serviceName, ts.TaskSetArn)
	return &createTaskSetResponse{TaskSet: *ts}, nil
}

func (h *Handler) updateTaskSetTyped(ctx context.Context, req *updateTaskSetRequest) (*updateTaskSetResponse, *protocol.AWSError) {
	if req.Cluster == "" {
		req.Cluster = "default"
	}
	clusterName := extractClusterName(req.Cluster)
	serviceName := extractServiceName(req.Service)
	taskSetID := extractTaskSetID(req.TaskSet)
	svc, aerr := h.store.getService(ctx, clusterName, serviceName)
	if aerr != nil {
		return nil, aerr
	}
	ts, aerr := h.store.getTaskSet(ctx, clusterName, serviceName, taskSetID)
	if aerr != nil {
		return nil, aerr
	}
	if ts == nil {
		return nil, &protocol.AWSError{
			Code: "InvalidParameterException", Message: fmt.Sprintf("The task set %s was not found.", req.TaskSet), HTTPStatus: http.StatusBadRequest,
		}
	}
	ts.Scale = req.Scale
	ts.ComputedDesiredCount = computeDesiredCount(req.Scale, svc.DesiredCount)
	ts.UpdatedAt = h.clk.Now().Unix()
	if aerr := h.store.putTaskSet(ctx, clusterName, serviceName, ts); aerr != nil {
		return nil, aerr
	}
	return &updateTaskSetResponse{TaskSet: *ts}, nil
}

func (h *Handler) deleteTaskSetTyped(ctx context.Context, req *deleteTaskSetRequest) (*deleteTaskSetResponse, *protocol.AWSError) {
	if req.Cluster == "" {
		req.Cluster = "default"
	}
	clusterName := extractClusterName(req.Cluster)
	serviceName := extractServiceName(req.Service)
	taskSetID := extractTaskSetID(req.TaskSet)
	// The service is read only to reject a task set on one that is not there.
	if _, aerr := h.store.getService(ctx, clusterName, serviceName); aerr != nil {
		return nil, aerr
	}
	ts, aerr := h.store.getTaskSet(ctx, clusterName, serviceName, taskSetID)
	if aerr != nil {
		return nil, aerr
	}
	if ts == nil {
		return nil, &protocol.AWSError{
			Code: "InvalidParameterException", Message: fmt.Sprintf("The task set %s was not found.", req.TaskSet), HTTPStatus: http.StatusBadRequest,
		}
	}
	if aerr := h.store.deleteTaskSet(ctx, clusterName, serviceName, taskSetID); aerr != nil {
		return nil, aerr
	}
	h.deregisterServiceTaskSet(ctx, clusterName, serviceName, taskSetID)
	ts.Status = "DRAINING"
	return &deleteTaskSetResponse{TaskSet: *ts}, nil
}

func (h *Handler) describeTaskSetsTyped(ctx context.Context, req *describeTaskSetsRequest) (*describeTaskSetsResponse, *protocol.AWSError) {
	if req.Cluster == "" {
		req.Cluster = "default"
	}
	clusterName := extractClusterName(req.Cluster)
	serviceName := extractServiceName(req.Service)
	var result []TaskSet
	var failures []failure
	if len(req.TaskSets) == 0 {
		all, aerr := h.store.listTaskSets(ctx, clusterName, serviceName)
		if aerr != nil {
			return nil, aerr
		}
		result = all
	} else {
		result = make([]TaskSet, 0, len(req.TaskSets))
		failures = make([]failure, 0)
		for _, ref := range req.TaskSets {
			tsID := extractTaskSetID(ref)
			ts, aerr := h.store.getTaskSet(ctx, clusterName, serviceName, tsID)
			if aerr != nil {
				return nil, aerr
			}
			if ts == nil {
				arn := ref
				if !strings.HasPrefix(arn, "arn:") {
					arn = h.taskSetARN(ctx, clusterName, serviceName, tsID)
				}
				failures = append(failures, failure{Arn: arn, Reason: "MISSING"})
				continue
			}
			result = append(result, *ts)
		}
	}
	return &describeTaskSetsResponse{TaskSets: result, Failures: failures}, nil
}

func (h *Handler) updateServicePrimaryTaskSetTyped(ctx context.Context, req *updateServicePrimaryTaskSetRequest) (*updateServicePrimaryTaskSetResponse, *protocol.AWSError) {
	if req.Cluster == "" {
		req.Cluster = "default"
	}
	clusterName := extractClusterName(req.Cluster)
	serviceName := extractServiceName(req.Service)
	primaryID := extractTaskSetID(req.PrimaryTaskSet)
	primary, aerr := h.store.getTaskSet(ctx, clusterName, serviceName, primaryID)
	if aerr != nil {
		return nil, aerr
	}
	if primary == nil {
		return nil, &protocol.AWSError{
			Code: "InvalidParameterException", Message: fmt.Sprintf("The task set %s was not found.", req.PrimaryTaskSet), HTTPStatus: http.StatusBadRequest,
		}
	}
	all, aerr := h.store.listTaskSets(ctx, clusterName, serviceName)
	if aerr != nil {
		return nil, aerr
	}
	now := h.clk.Now().Unix()
	for i := range all {
		if all[i].Id == primaryID {
			all[i].Status = "PRIMARY"
		} else if all[i].Status == "PRIMARY" {
			all[i].Status = "ACTIVE"
		}
		all[i].UpdatedAt = now
		_ = h.store.putTaskSet(ctx, clusterName, serviceName, &all[i])
	}
	primary, _ = h.store.getTaskSet(ctx, clusterName, serviceName, primaryID)
	return &updateServicePrimaryTaskSetResponse{TaskSet: *primary}, nil
}

func (h *Handler) registerContainerInstanceTyped(ctx context.Context, req *registerContainerInstanceRequest) (*registerContainerInstanceResponse, *protocol.AWSError) {
	clusterName := extractClusterName(req.Cluster)
	if clusterName == "" {
		clusterName = "default"
	}
	id := uuid.New().String()
	arn := h.containerInstanceARN(ctx, clusterName, id)
	ci := &ContainerInstance{
		ContainerInstanceArn: arn,
		Ec2InstanceId:        req.Ec2InstanceId,
		Status:               "ACTIVE",
		AgentConnected:       true,
		RegisteredAt:         h.clk.Now().Unix(),
		ClusterName:          clusterName,
	}
	if aerr := h.store.putContainerInstance(ctx, ci); aerr != nil {
		return nil, aerr
	}
	return &registerContainerInstanceResponse{ContainerInstance: *ci}, nil
}

func (h *Handler) deregisterContainerInstanceTyped(ctx context.Context, req *deregisterContainerInstanceRequest) (*deregisterContainerInstanceResponse, *protocol.AWSError) {
	if req.ContainerInstance == "" {
		return nil, protocol.ErrMissingParameter("containerInstance")
	}
	clusterName := extractClusterName(req.Cluster)
	if clusterName == "" {
		clusterName = "default"
	}
	ciID := extractContainerInstanceID(req.ContainerInstance)
	ci, aerr := h.store.getContainerInstance(ctx, clusterName, req.ContainerInstance)
	if aerr != nil {
		return nil, aerr
	}
	if ci == nil {
		ci, aerr = h.store.getContainerInstance(ctx, clusterName, h.containerInstanceARN(ctx, clusterName, ciID))
		if aerr != nil {
			return nil, aerr
		}
	}
	if ci == nil {
		return nil, &protocol.AWSError{
			Code: "InvalidParameterException", Message: "Container instance not found: " + req.ContainerInstance, HTTPStatus: http.StatusBadRequest,
		}
	}
	if aerr := h.store.deleteContainerInstance(ctx, clusterName, ci.ContainerInstanceArn); aerr != nil {
		return nil, aerr
	}
	ci.Status = "INACTIVE"
	ci.AgentConnected = false
	return &deregisterContainerInstanceResponse{ContainerInstance: *ci}, nil
}

func (h *Handler) describeContainerInstancesTyped(ctx context.Context, req *describeContainerInstancesRequest) (*describeContainerInstancesResponse, *protocol.AWSError) {
	clusterName := extractClusterName(req.Cluster)
	if clusterName == "" {
		clusterName = "default"
	}
	var found []ContainerInstance
	var failures []ciFailure
	for _, ref := range req.ContainerInstances {
		ci, aerr := h.store.getContainerInstance(ctx, clusterName, ref)
		if aerr != nil {
			return nil, aerr
		}
		if ci == nil {
			fullARN := h.containerInstanceARN(ctx, clusterName, extractContainerInstanceID(ref))
			ci, aerr = h.store.getContainerInstance(ctx, clusterName, fullARN)
			if aerr != nil {
				return nil, aerr
			}
		}
		if ci == nil {
			failures = append(failures, ciFailure{Arn: ref, Reason: "MISSING"})
		} else {
			found = append(found, *ci)
		}
	}
	return &describeContainerInstancesResponse{ContainerInstances: found, Failures: failures}, nil
}

func (h *Handler) listContainerInstancesTyped(ctx context.Context, req *listContainerInstancesRequest) (*listContainerInstancesResponse, *protocol.AWSError) {
	clusterName := extractClusterName(req.Cluster)
	if clusterName == "" {
		clusterName = "default"
	}
	instances, aerr := h.store.listContainerInstances(ctx, clusterName)
	if aerr != nil {
		return nil, aerr
	}
	arns := make([]string, 0, len(instances))
	for _, ci := range instances {
		if req.Status == "" || strings.EqualFold(ci.Status, req.Status) {
			arns = append(arns, ci.ContainerInstanceArn)
		}
	}
	return &listContainerInstancesResponse{ContainerInstanceArns: arns}, nil
}

func (h *Handler) listAccountSettingsTyped(ctx context.Context, req *listAccountSettingsRequest) (*listAccountSettingsResponse, *protocol.AWSError) {
	var settings []accountSettingWire
	if req.Name != "" {
		s := h.resolvedSettingTyped(ctx, req.Name)
		settings = append(settings, accountSettingWire(s))
	} else {
		for name := range defaultAccountSettings {
			s := h.resolvedSettingTyped(ctx, name)
			settings = append(settings, accountSettingWire(s))
		}
	}
	return &listAccountSettingsResponse{Settings: settings}, nil
}

func (h *Handler) putAccountSettingTyped(ctx context.Context, req *putAccountSettingRequest) (*putAccountSettingResponse, *protocol.AWSError) {
	if req.Name == "" {
		return nil, protocol.ErrMissingParameter("name")
	}
	setting := &AccountSetting{Name: req.Name, Value: req.Value}
	if aerr := h.store.putAccountSetting(ctx, setting); aerr != nil {
		return nil, aerr
	}
	return &putAccountSettingResponse{Setting: *setting}, nil
}

func (h *Handler) putAccountSettingDefaultTyped(ctx context.Context, req *putAccountSettingRequest) (*putAccountSettingResponse, *protocol.AWSError) {
	return h.putAccountSettingTyped(ctx, req)
}

func (h *Handler) deleteAccountSettingTyped(ctx context.Context, req *deleteAccountSettingRequest) (*deleteAccountSettingResponse, *protocol.AWSError) {
	if req.Name == "" {
		return nil, protocol.ErrMissingParameter("name")
	}
	existing, aerr := h.store.getAccountSetting(ctx, req.Name)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := h.store.deleteAccountSetting(ctx, req.Name); aerr != nil {
		return nil, aerr
	}
	var returned AccountSetting
	if existing != nil {
		returned = *existing
	} else {
		returned = h.resolvedSettingTyped(ctx, req.Name)
	}
	return &deleteAccountSettingResponse{Setting: returned}, nil
}

func (h *Handler) notImplementedTyped(ctx context.Context, _ *struct{}) (*struct{}, *protocol.AWSError) {
	return nil, protocol.ErrNotImplemented
}

func (h *Handler) resolvedSettingTyped(ctx context.Context, name string) AccountSetting {
	stored, _ := h.store.getAccountSetting(ctx, name)
	if stored != nil {
		return *stored
	}
	defaultVal := defaultAccountSettings[name]
	return AccountSetting{Name: name, Value: defaultVal}
}
