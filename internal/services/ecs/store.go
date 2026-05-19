package ecs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	nsClusters           = "ecs:clusters"
	nsTaskDefs           = "ecs:task-definitions"
	nsTaskDefFamilies    = "ecs:task-def-families"
	nsTasks              = "ecs:tasks"
	nsServices           = "ecs:services"
	nsTags               = "ecs:tags"
	nsCapacityProviders  = "ecs:capacity-providers"
	nsTaskSets           = "ecs:task-sets"
	nsAccountSettings    = "ecs:account-settings"
	nsContainerInstances = "ecs:container-instances"
)

// Tag is a key-value pair attached to an ECS resource.
type Tag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Cluster represents a stored ECS cluster.
type Cluster struct {
	ClusterName                       string                         `json:"clusterName"`
	ClusterArn                        string                         `json:"clusterArn"`
	Status                            string                         `json:"status"`
	RegisteredContainerInstancesCount int                            `json:"registeredContainerInstancesCount"`
	RunningTasksCount                 int                            `json:"runningTasksCount"`
	PendingTasksCount                 int                            `json:"pendingTasksCount"`
	ActiveServicesCount               int                            `json:"activeServicesCount"`
	CapacityProviders                 []string                       `json:"capacityProviders,omitempty"`
	DefaultCapacityProviderStrategy   []CapacityProviderStrategyItem `json:"defaultCapacityProviderStrategy,omitempty"`
}

// CapacityProvider represents an ECS capacity provider resource.
type CapacityProvider struct {
	CapacityProviderArn string `json:"capacityProviderArn"`
	Name                string `json:"name"`
	Status              string `json:"status"`
	UpdateStatus        string `json:"updateStatus,omitempty"`
	Tags                []Tag  `json:"tags,omitempty"`
}

// Scale specifies a percentage-based scale value for a task set.
type Scale struct {
	Unit  string  `json:"unit"`
	Value float64 `json:"value"`
}

// TaskSet represents an ECS task set within a service (CODE_DEPLOY or EXTERNAL controller).
type TaskSet struct {
	Id                   string                `json:"id"`
	TaskSetArn           string                `json:"taskSetArn"`
	ServiceArn           string                `json:"serviceArn"`
	ClusterArn           string                `json:"clusterArn"`
	TaskDefinition       string                `json:"taskDefinition"`
	Status               string                `json:"status"`
	LaunchType           string                `json:"launchType"`
	PlatformVersion      string                `json:"platformVersion,omitempty"`
	PlatformFamily       string                `json:"platformFamily,omitempty"`
	NetworkConfiguration *NetworkConfiguration `json:"networkConfiguration,omitempty"`
	Scale                Scale                 `json:"scale"`
	StabilityStatus      string                `json:"stabilityStatus"`
	StabilityStatusAt    *int64                `json:"stabilityStatusAt,omitempty"`
	CreatedAt            int64                 `json:"createdAt"`
	UpdatedAt            int64                 `json:"updatedAt"`
	ExternalId           string                `json:"externalId,omitempty"`
	Tags                 []Tag                 `json:"tags,omitempty"`
	RunningCount         int                   `json:"runningCount"`
	PendingCount         int                   `json:"pendingCount"`
	ComputedDesiredCount int                   `json:"computedDesiredCount"`
}

// TaskDefinition represents a stored ECS task definition.
type TaskDefinition struct {
	TaskDefinitionArn       string                `json:"taskDefinitionArn"`
	Family                  string                `json:"family"`
	Revision                int                   `json:"revision"`
	Status                  string                `json:"status"`
	NetworkMode             string                `json:"networkMode,omitempty"`
	RequiresCompatibilities []string              `json:"requiresCompatibilities,omitempty"`
	Cpu                     string                `json:"cpu,omitempty"`
	Memory                  string                `json:"memory,omitempty"`
	ContainerDefinitions    []ContainerDefinition `json:"containerDefinitions"`
}

// ContainerDefinition represents a container within a task definition.
type ContainerDefinition struct {
	Name             string            `json:"name"`
	Image            string            `json:"image"`
	Cpu              int               `json:"cpu,omitempty"`
	Memory           int               `json:"memory,omitempty"`
	Essential        *bool             `json:"essential,omitempty"`
	PortMappings     []PortMapping     `json:"portMappings,omitempty"`
	Environment      []KeyValuePair    `json:"environment,omitempty"`
	Command          []string          `json:"command,omitempty"`
	LogConfiguration *LogConfiguration `json:"logConfiguration,omitempty"`
}

// PortMapping maps a container port to a host port.
type PortMapping struct {
	ContainerPort int    `json:"containerPort"`
	HostPort      int    `json:"hostPort,omitempty"`
	Protocol      string `json:"protocol,omitempty"`
}

// KeyValuePair is a name-value pair used for environment variables.
type KeyValuePair struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// LogConfiguration specifies the log driver and options for a container.
type LogConfiguration struct {
	LogDriver string            `json:"logDriver"`
	Options   map[string]string `json:"options,omitempty"`
}

// NetworkConfiguration holds VPC networking settings for awsvpc mode.
type NetworkConfiguration struct {
	AwsvpcConfiguration *AwsvpcConfiguration `json:"awsvpcConfiguration,omitempty"`
}

// AwsvpcConfiguration specifies VPC subnets and security groups for a task.
type AwsvpcConfiguration struct {
	Subnets        []string `json:"subnets,omitempty"`
	SecurityGroups []string `json:"securityGroups,omitempty"`
	AssignPublicIp string   `json:"assignPublicIp,omitempty"`
}

// Attachment represents a network interface (ENI) or other resource attached to a task.
type Attachment struct {
	Id      string         `json:"id"`
	Type    string         `json:"type"`
	Status  string         `json:"status"`
	Details []KeyValuePair `json:"details,omitempty"`
}

// Task represents a running ECS task.
type Task struct {
	TaskArn              string                `json:"taskArn"`
	TaskDefinitionArn    string                `json:"taskDefinitionArn"`
	ClusterArn           string                `json:"clusterArn"`
	LastStatus           string                `json:"lastStatus"`
	DesiredStatus        string                `json:"desiredStatus"`
	LaunchType           string                `json:"launchType"`
	Cpu                  string                `json:"cpu,omitempty"`
	Memory               string                `json:"memory,omitempty"`
	PlatformVersion      string                `json:"platformVersion,omitempty"`
	PlatformFamily       string                `json:"platformFamily,omitempty"`
	StartedAt            *int64                `json:"startedAt,omitempty"`
	StoppedAt            *int64                `json:"stoppedAt,omitempty"`
	StoppedReason        string                `json:"stoppedReason,omitempty"`
	CreatedAt            int64                 `json:"createdAt"`
	Group                string                `json:"group,omitempty"`
	Containers           []Container           `json:"containers"`
	Overrides            *TaskOverride         `json:"overrides,omitempty"`
	NetworkConfiguration *NetworkConfiguration `json:"networkConfiguration,omitempty"`
	Attachments          []Attachment          `json:"attachments,omitempty"`
}

// Container represents a container within a running task.
type Container struct {
	ContainerArn string `json:"containerArn"`
	Name         string `json:"name"`
	Image        string `json:"image"`
	LastStatus   string `json:"lastStatus"`
	ExitCode     *int   `json:"exitCode,omitempty"`
	Reason       string `json:"reason,omitempty"`
	RuntimeId    string `json:"runtimeId,omitempty"`
	DockerID     string `json:"dockerId,omitempty"` // Docker container ID when backed by Docker
}

// TaskOverride holds overrides applied at RunTask time.
type TaskOverride struct {
	ContainerOverrides []ContainerOverride `json:"containerOverrides,omitempty"`
}

// ContainerOverride holds per-container overrides.
type ContainerOverride struct {
	Name        string         `json:"name,omitempty"`
	Command     []string       `json:"command,omitempty"`
	Environment []KeyValuePair `json:"environment,omitempty"`
}

// DeploymentController specifies the controller type for an ECS service deployment.
type DeploymentController struct {
	Type string `json:"type"`
}

// CapacityProviderStrategyItem specifies a capacity provider and its weight/base.
type CapacityProviderStrategyItem struct {
	CapacityProvider string `json:"capacityProvider"`
	Weight           int    `json:"weight,omitempty"`
	Base             int    `json:"base,omitempty"`
}

// ecsService represents an ECS service resource.
type ecsService struct {
	ServiceName              string                         `json:"serviceName"`
	ServiceArn               string                         `json:"serviceArn"`
	ClusterArn               string                         `json:"clusterArn"`
	TaskDefinition           string                         `json:"taskDefinition"`
	DesiredCount             int                            `json:"desiredCount"`
	RunningCount             int                            `json:"runningCount"`
	PendingCount             int                            `json:"pendingCount"`
	Status                   string                         `json:"status"`
	LaunchType               string                         `json:"launchType"`
	Events                   []ServiceEvent                 `json:"events"`
	CreatedAt                int64                          `json:"createdAt"`
	Deployments              []Deployment                   `json:"deployments"`
	SchedulingStrategy       string                         `json:"schedulingStrategy"`
	NetworkConfiguration     *NetworkConfiguration          `json:"networkConfiguration,omitempty"`
	DeploymentController     *DeploymentController          `json:"deploymentController,omitempty"`
	CapacityProviderStrategy []CapacityProviderStrategyItem `json:"capacityProviderStrategy,omitempty"`
	PlatformVersion          string                         `json:"platformVersion,omitempty"`
	PlatformFamily           string                         `json:"platformFamily,omitempty"`
	TaskSets                 []string                       `json:"taskSets,omitempty"`
}

// ServiceEvent represents a timestamped event in a service's history.
type ServiceEvent struct {
	ID        string `json:"id"`
	CreatedAt int64  `json:"createdAt"`
	Message   string `json:"message"`
}

// Deployment represents a deployment within an ECS service.
type Deployment struct {
	ID                   string                `json:"id"`
	Status               string                `json:"status"`
	TaskDefinition       string                `json:"taskDefinition"`
	DesiredCount         int                   `json:"desiredCount"`
	RunningCount         int                   `json:"runningCount"`
	PendingCount         int                   `json:"pendingCount"`
	CreatedAt            int64                 `json:"createdAt"`
	UpdatedAt            int64                 `json:"updatedAt"`
	NetworkConfiguration *NetworkConfiguration `json:"networkConfiguration,omitempty"`
	PlatformVersion      string                `json:"platformVersion,omitempty"`
	PlatformFamily       string                `json:"platformFamily,omitempty"`
}

// ecsStore wraps state.Store with ECS-specific helpers.
type ecsStore struct {
	mu            sync.Mutex
	store         state.Store
	defaultRegion string
}

func newECSStore(store state.Store, defaultRegion string) *ecsStore {
	return &ecsStore{store: store, defaultRegion: defaultRegion}
}

// region extracts the per-request region from context, falling back to the default.
func (s *ecsStore) region(ctx context.Context) string {
	return middleware.RegionFromContext(ctx, s.defaultRegion)
}

// ---- Cluster operations -------------------------------------------------------

func (s *ecsStore) putCluster(ctx context.Context, c *Cluster) *protocol.AWSError {
	raw, err := json.Marshal(c)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsClusters, serviceutil.RegionKey(s.region(ctx), c.ClusterName), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *ecsStore) getCluster(ctx context.Context, name string) (*Cluster, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsClusters, serviceutil.RegionKey(s.region(ctx), name))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, &protocol.AWSError{
			Code:       "ClusterNotFoundException",
			Message:    fmt.Sprintf("Cluster not found: %s", name),
			HTTPStatus: http.StatusBadRequest,
		}
	}
	var c Cluster
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &c, nil
}

func (s *ecsStore) listClusters(ctx context.Context) ([]Cluster, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsClusters, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	clusters := make([]Cluster, 0, len(pairs))
	for _, p := range pairs {
		var c Cluster
		if err := json.Unmarshal([]byte(p.Value), &c); err != nil {
			continue
		}
		clusters = append(clusters, c)
	}
	return clusters, nil
}

func (s *ecsStore) deleteCluster(ctx context.Context, name string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsClusters, serviceutil.RegionKey(s.region(ctx), name)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// ---- Task definition operations -----------------------------------------------

func (s *ecsStore) putTaskDefinition(ctx context.Context, td *TaskDefinition) *protocol.AWSError {
	raw, err := json.Marshal(td)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	key := fmt.Sprintf("%s:%d", td.Family, td.Revision)
	if err := s.store.Set(ctx, nsTaskDefs, serviceutil.RegionKey(s.region(ctx), key), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *ecsStore) getTaskDefinition(ctx context.Context, family string, revision int) (*TaskDefinition, *protocol.AWSError) {
	key := fmt.Sprintf("%s:%d", family, revision)
	raw, found, err := s.store.Get(ctx, nsTaskDefs, serviceutil.RegionKey(s.region(ctx), key))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, &protocol.AWSError{
			Code:       "ClientException",
			Message:    fmt.Sprintf("Unable to describe task definition: %s:%d", family, revision),
			HTTPStatus: http.StatusBadRequest,
		}
	}
	var td TaskDefinition
	if err := json.Unmarshal([]byte(raw), &td); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &td, nil
}

func (s *ecsStore) getLatestTaskDefinition(ctx context.Context, family string) (*TaskDefinition, *protocol.AWSError) {
	rev, aerr := s.currentRevision(ctx, family)
	if aerr != nil {
		return nil, aerr
	}
	if rev == 0 {
		return nil, &protocol.AWSError{
			Code:       "ClientException",
			Message:    fmt.Sprintf("Unable to describe task definition: %s", family),
			HTTPStatus: http.StatusBadRequest,
		}
	}
	return s.getTaskDefinition(ctx, family, rev)
}

func (s *ecsStore) listTaskDefinitions(ctx context.Context) ([]TaskDefinition, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsTaskDefs, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	defs := make([]TaskDefinition, 0, len(pairs))
	for _, p := range pairs {
		var td TaskDefinition
		if err := json.Unmarshal([]byte(p.Value), &td); err != nil {
			continue
		}
		defs = append(defs, td)
	}
	return defs, nil
}

func (s *ecsStore) listTaskDefinitionsByFamily(ctx context.Context, family string) ([]TaskDefinition, *protocol.AWSError) {
	prefix := serviceutil.RegionKey(s.region(ctx), family+":")
	pairs, err := s.store.Scan(ctx, nsTaskDefs, prefix)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	defs := make([]TaskDefinition, 0, len(pairs))
	for _, p := range pairs {
		var td TaskDefinition
		if err := json.Unmarshal([]byte(p.Value), &td); err != nil {
			continue
		}
		defs = append(defs, td)
	}
	return defs, nil
}

// ---- Revision counter ---------------------------------------------------------

// nextRevision increments and returns the next revision number for family.
func (s *ecsStore) nextRevision(ctx context.Context, family string) (int, *protocol.AWSError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := serviceutil.RegionKey(s.region(ctx), family)
	raw, found, err := s.store.Get(ctx, nsTaskDefFamilies, key)
	if err != nil {
		return 0, protocol.Wrap(protocol.ErrInternalError, err)
	}
	rev := 0
	if found {
		rev, _ = strconv.Atoi(raw)
	}
	rev++
	if err := s.store.Set(ctx, nsTaskDefFamilies, key, strconv.Itoa(rev)); err != nil {
		return 0, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return rev, nil
}

// currentRevision returns the current (latest) revision for family without incrementing.
func (s *ecsStore) currentRevision(ctx context.Context, family string) (int, *protocol.AWSError) {
	key := serviceutil.RegionKey(s.region(ctx), family)
	raw, found, err := s.store.Get(ctx, nsTaskDefFamilies, key)
	if err != nil {
		return 0, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return 0, nil
	}
	rev, _ := strconv.Atoi(raw)
	return rev, nil
}

// ---- Task operations ----------------------------------------------------------

func (s *ecsStore) putTask(ctx context.Context, task *Task) *protocol.AWSError {
	raw, err := json.Marshal(task)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	key := serviceutil.RegionKey(s.region(ctx), extractClusterName(task.ClusterArn)+"/"+extractTaskID(task.TaskArn))
	if err := s.store.Set(ctx, nsTasks, key, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *ecsStore) getTask(ctx context.Context, cluster, taskID string) (*Task, *protocol.AWSError) {
	key := serviceutil.RegionKey(s.region(ctx), cluster+"/"+taskID)
	raw, found, err := s.store.Get(ctx, nsTasks, key)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, nil
	}
	var t Task
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &t, nil
}

func (s *ecsStore) listTasks(ctx context.Context, cluster string) ([]Task, *protocol.AWSError) {
	prefix := serviceutil.RegionKey(s.region(ctx), cluster+"/")
	pairs, err := s.store.Scan(ctx, nsTasks, prefix)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	tasks := make([]Task, 0, len(pairs))
	for _, p := range pairs {
		var t Task
		if err := json.Unmarshal([]byte(p.Value), &t); err != nil {
			continue
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// listAllTasks returns all tasks across all clusters in the current region.
func (s *ecsStore) listAllTasks(ctx context.Context) ([]Task, *protocol.AWSError) {
	prefix := serviceutil.RegionKey(s.region(ctx), "")
	pairs, err := s.store.Scan(ctx, nsTasks, prefix)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	tasks := make([]Task, 0, len(pairs))
	for _, p := range pairs {
		var t Task
		if err := json.Unmarshal([]byte(p.Value), &t); err != nil {
			continue
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// ---- Service operations -------------------------------------------------------

func (s *ecsStore) putService(ctx context.Context, clusterName string, svc *ecsService) *protocol.AWSError {
	raw, err := json.Marshal(svc)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	key := serviceutil.RegionKey(s.region(ctx), clusterName+"/"+svc.ServiceName)
	if err := s.store.Set(ctx, nsServices, key, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *ecsStore) getService(ctx context.Context, clusterName, serviceName string) (*ecsService, *protocol.AWSError) {
	key := serviceutil.RegionKey(s.region(ctx), clusterName+"/"+serviceName)
	raw, found, err := s.store.Get(ctx, nsServices, key)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, &protocol.AWSError{
			Code:       "ServiceNotFoundException",
			Message:    "Service not found.",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	var svc ecsService
	if err := json.Unmarshal([]byte(raw), &svc); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &svc, nil
}

func (s *ecsStore) listServices(ctx context.Context, clusterName string) ([]ecsService, *protocol.AWSError) {
	prefix := serviceutil.RegionKey(s.region(ctx), clusterName+"/")
	pairs, err := s.store.Scan(ctx, nsServices, prefix)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	services := make([]ecsService, 0, len(pairs))
	for _, p := range pairs {
		var svc ecsService
		if err := json.Unmarshal([]byte(p.Value), &svc); err != nil {
			continue
		}
		services = append(services, svc)
	}
	return services, nil
}

// ---- Tag operations -----------------------------------------------------------

func (s *ecsStore) putTags(ctx context.Context, arn string, tags map[string]string) *protocol.AWSError {
	raw, err := json.Marshal(tags)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsTags, serviceutil.RegionKey(s.region(ctx), arn), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *ecsStore) getTags(ctx context.Context, arn string) (map[string]string, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsTags, serviceutil.RegionKey(s.region(ctx), arn))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, nil
	}
	var tags map[string]string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return tags, nil
}

// ---- Task definition family listing -------------------------------------------

// listTaskDefinitionFamilies returns the distinct family names that have registered task definitions.
func (s *ecsStore) listTaskDefinitionFamilies(ctx context.Context) ([]string, *protocol.AWSError) {
	keys, err := s.store.List(ctx, nsTaskDefFamilies, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	prefix := serviceutil.RegionKey(s.region(ctx), "")
	families := make([]string, 0, len(keys))
	for _, k := range keys {
		if len(k) > len(prefix) {
			families = append(families, k[len(prefix):])
		}
	}
	return families, nil
}

// ---- Capacity provider operations --------------------------------------------

func (s *ecsStore) putCapacityProvider(ctx context.Context, cp *CapacityProvider) *protocol.AWSError {
	raw, err := json.Marshal(cp)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsCapacityProviders, serviceutil.RegionKey(s.region(ctx), cp.Name), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *ecsStore) getCapacityProvider(ctx context.Context, name string) (*CapacityProvider, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsCapacityProviders, serviceutil.RegionKey(s.region(ctx), name))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, nil
	}
	var cp CapacityProvider
	if err := json.Unmarshal([]byte(raw), &cp); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &cp, nil
}

func (s *ecsStore) listCapacityProviders(ctx context.Context) ([]CapacityProvider, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsCapacityProviders, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	cps := make([]CapacityProvider, 0, len(pairs))
	for _, p := range pairs {
		var cp CapacityProvider
		if err := json.Unmarshal([]byte(p.Value), &cp); err != nil {
			continue
		}
		cps = append(cps, cp)
	}
	return cps, nil
}

// ---- Task set operations -----------------------------------------------------

func (s *ecsStore) putTaskSet(ctx context.Context, clusterName, serviceName string, ts *TaskSet) *protocol.AWSError {
	raw, err := json.Marshal(ts)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	key := serviceutil.RegionKey(s.region(ctx), clusterName+"/"+serviceName+"/"+ts.Id)
	if err := s.store.Set(ctx, nsTaskSets, key, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *ecsStore) getTaskSet(ctx context.Context, clusterName, serviceName, taskSetID string) (*TaskSet, *protocol.AWSError) {
	key := serviceutil.RegionKey(s.region(ctx), clusterName+"/"+serviceName+"/"+taskSetID)
	raw, found, err := s.store.Get(ctx, nsTaskSets, key)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, nil
	}
	var ts TaskSet
	if err := json.Unmarshal([]byte(raw), &ts); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &ts, nil
}

func (s *ecsStore) listTaskSets(ctx context.Context, clusterName, serviceName string) ([]TaskSet, *protocol.AWSError) {
	prefix := serviceutil.RegionKey(s.region(ctx), clusterName+"/"+serviceName+"/")
	pairs, err := s.store.Scan(ctx, nsTaskSets, prefix)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	sets := make([]TaskSet, 0, len(pairs))
	for _, p := range pairs {
		var ts TaskSet
		if err := json.Unmarshal([]byte(p.Value), &ts); err != nil {
			continue
		}
		sets = append(sets, ts)
	}
	return sets, nil
}

func (s *ecsStore) deleteTaskSet(ctx context.Context, clusterName, serviceName, taskSetID string) *protocol.AWSError {
	key := serviceutil.RegionKey(s.region(ctx), clusterName+"/"+serviceName+"/"+taskSetID)
	if err := s.store.Delete(ctx, nsTaskSets, key); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// ---- Account setting operations ----------------------------------------------

// AccountSetting holds a user-level ECS account setting override.
type AccountSetting struct {
	Name         string `json:"name"`
	Value        string `json:"value"`
	PrincipalArn string `json:"principalArn"`
}

func (s *ecsStore) putAccountSetting(ctx context.Context, setting *AccountSetting) *protocol.AWSError {
	raw, err := json.Marshal(setting)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	key := serviceutil.RegionKey(s.region(ctx), setting.Name)
	if err := s.store.Set(ctx, nsAccountSettings, key, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *ecsStore) getAccountSetting(ctx context.Context, name string) (*AccountSetting, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsAccountSettings, serviceutil.RegionKey(s.region(ctx), name))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, nil
	}
	var setting AccountSetting
	if err := json.Unmarshal([]byte(raw), &setting); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &setting, nil
}

func (s *ecsStore) deleteAccountSetting(ctx context.Context, name string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsAccountSettings, serviceutil.RegionKey(s.region(ctx), name)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// ---- Container instance operations ------------------------------------------

// ContainerInstance represents a registered ECS container instance.
type ContainerInstance struct {
	ContainerInstanceArn string `json:"containerInstanceArn"`
	Ec2InstanceId        string `json:"ec2InstanceId,omitempty"`
	Status               string `json:"status"`
	AgentConnected       bool   `json:"agentConnected"`
	RunningTasksCount    int    `json:"runningTasksCount"`
	PendingTasksCount    int    `json:"pendingTasksCount"`
	RegisteredAt         int64  `json:"registeredAt"`
	ClusterName          string `json:"clusterName"`
}

func (s *ecsStore) putContainerInstance(ctx context.Context, ci *ContainerInstance) *protocol.AWSError {
	raw, err := json.Marshal(ci)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	key := serviceutil.RegionKey(s.region(ctx), ci.ClusterName+"/"+ci.ContainerInstanceArn)
	if err := s.store.Set(ctx, nsContainerInstances, key, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *ecsStore) getContainerInstance(ctx context.Context, clusterName, arnOrID string) (*ContainerInstance, *protocol.AWSError) {
	key := serviceutil.RegionKey(s.region(ctx), clusterName+"/"+arnOrID)
	raw, found, err := s.store.Get(ctx, nsContainerInstances, key)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, nil
	}
	var ci ContainerInstance
	if err := json.Unmarshal([]byte(raw), &ci); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &ci, nil
}

func (s *ecsStore) listContainerInstances(ctx context.Context, clusterName string) ([]ContainerInstance, *protocol.AWSError) {
	prefix := serviceutil.RegionKey(s.region(ctx), clusterName+"/")
	pairs, err := s.store.Scan(ctx, nsContainerInstances, prefix)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	instances := make([]ContainerInstance, 0, len(pairs))
	for _, p := range pairs {
		var ci ContainerInstance
		if err := json.Unmarshal([]byte(p.Value), &ci); err != nil {
			continue
		}
		instances = append(instances, ci)
	}
	return instances, nil
}

func (s *ecsStore) deleteContainerInstance(ctx context.Context, clusterName, arn string) *protocol.AWSError {
	key := serviceutil.RegionKey(s.region(ctx), clusterName+"/"+arn)
	if err := s.store.Delete(ctx, nsContainerInstances, key); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}
