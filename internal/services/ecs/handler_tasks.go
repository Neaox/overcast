package ecs

// handler_tasks.go — RunTask, StopTask, DescribeTasks, ListTasks handlers.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
)

// RunTask handles AmazonEC2ContainerServiceV20141113.RunTask.
func (h *Handler) RunTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster              string                `json:"cluster"`
		TaskDefinition       string                `json:"taskDefinition"`
		Count                int                   `json:"count"`
		LaunchType           string                `json:"launchType"`
		NetworkConfiguration *NetworkConfiguration `json:"networkConfiguration"`
		PlatformVersion      string                `json:"platformVersion"`
		Overrides            *TaskOverride         `json:"overrides"`
		Group                string                `json:"group"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Cluster == "" {
		req.Cluster = "default"
	}
	if req.Count < 1 {
		req.Count = 1
	}

	// Fargate requires networkConfiguration.
	if req.LaunchType == "FARGATE" && req.NetworkConfiguration == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "Network Configuration must be provided when networkMode is 'awsvpc'.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	clusterName := extractClusterName(req.Cluster)

	// Verify cluster exists.
	cluster, aerr := h.store.getCluster(r.Context(), clusterName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Resolve task definition.
	family, revision, hasRevision := parseTaskDefRef(req.TaskDefinition)
	var td *TaskDefinition
	if hasRevision {
		td, aerr = h.store.getTaskDefinition(r.Context(), family, revision)
	} else {
		td, aerr = h.store.getLatestTaskDefinition(r.Context(), family)
	}
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	now := h.clk.Now().Unix()
	tasks := make([]Task, 0, req.Count)
	useDocker := h.dockerReady.Load()

	// Determine platform version for Fargate.
	platformVersion := req.PlatformVersion
	if platformVersion == "" && req.LaunchType == "FARGATE" {
		platformVersion = "LATEST"
	}

	awsvpcSubnetID := ""
	awsvpcNetworkID := ""
	awsvpcSubnetResolved := false
	if req.NetworkConfiguration != nil {
		var placementErr *protocol.AWSError
		awsvpcSubnetID, _, awsvpcNetworkID, awsvpcSubnetResolved, placementErr =
			h.resolveAwsvpcPlacement(r.Context(), req.NetworkConfiguration, "awsvpc tasks")
		if placementErr != nil {
			protocol.WriteJSONError(w, r, placementErr)
			return
		}
	}

	for i := 0; i < req.Count; i++ {
		taskID := uuid.New().String()
		taskArn := h.taskARN(r.Context(), clusterName, taskID)

		containers := make([]Container, 0, len(td.ContainerDefinitions))
		for _, cd := range td.ContainerDefinitions {
			containers = append(containers, Container{
				ContainerArn: h.containerARN(r.Context(), uuid.New().String()),
				Name:         cd.Name,
				Image:        cd.Image,
				LastStatus:   "PENDING",
			})
		}

		// Generate a synthetic ENI attachment for awsvpc tasks.
		var attachments []Attachment
		if req.NetworkConfiguration != nil {
			attachmentPrivateIP := "10.0." + fmt.Sprintf("%d.%d", (i+1)/256, (i+1)%256)
			if awsvpcSubnetResolved && h.vpcResolver != nil {
				if translated := h.vpcResolver.AllocatePrivateIPForSubnet(r.Context(), awsvpcSubnetID); translated != "" {
					attachmentPrivateIP = translated
				}
			}
			eniID := "eni-" + taskID[:8]
			attachments = []Attachment{{
				Id:     uuid.New().String(),
				Type:   "ElasticNetworkInterface",
				Status: "ATTACHING",
				Details: []KeyValuePair{
					{Name: "networkInterfaceId", Value: eniID},
					{Name: "subnetId", Value: awsvpcSubnetID},
					{Name: "privateIPv4Address", Value: attachmentPrivateIP},
				},
			}}
		}

		task := Task{
			TaskArn:              taskArn,
			TaskDefinitionArn:    td.TaskDefinitionArn,
			ClusterArn:           cluster.ClusterArn,
			LastStatus:           "PROVISIONING",
			DesiredStatus:        "RUNNING",
			LaunchType:           req.LaunchType,
			Cpu:                  td.Cpu,
			Memory:               td.Memory,
			PlatformVersion:      platformVersion,
			CreatedAt:            now,
			Group:                req.Group,
			Containers:           containers,
			Overrides:            req.Overrides,
			NetworkConfiguration: req.NetworkConfiguration,
			Attachments:          attachments,
		}

		if useDocker {
			if err := h.startTaskContainers(r.Context(), &task, td, clusterName, taskID, awsvpcNetworkID); err != nil {
				h.log.Warn("ecs: failed to start Docker containers, falling back to metadata-only",
					zap.String("task", taskID), zap.Error(err))
				// Fall through to metadata-only behaviour.
				h.scheduleMetadataTransition(clusterName, taskID)
			}
		} else {
			h.scheduleMetadataTransition(clusterName, taskID)
		}

		if aerr := h.store.putTask(r.Context(), &task); aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}

		tasks = append(tasks, task)
	}

	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{
		"tasks":    tasks,
		"failures": []any{},
	}, "application/x-amz-json-1.1")
}

// startTaskContainers creates and starts Docker containers for all container
// definitions in a task. On success, task containers are updated with DockerIDs
// and a scheduler transition to RUNNING is queued.
func (h *Handler) startTaskContainers(ctx context.Context, task *Task, td *TaskDefinition, clusterName, taskID, awsvpcNetworkID string) error {
	// Ensure the ECS network exists.
	if _, err := h.docker.CreateNetwork(ctx, h.cfg.ECSNetwork); err != nil {
		return fmt.Errorf("ecs: create network %s: %w", h.cfg.ECSNetwork, err)
	}

	// Build an override index by container name.
	overrides := make(map[string]*ContainerOverride)
	if task.Overrides != nil {
		for i := range task.Overrides.ContainerOverrides {
			co := &task.Overrides.ContainerOverrides[i]
			overrides[co.Name] = co
		}
	}

	// Resource ID for Docker labels: "clusterName/taskID" so the exit notifier
	// can look up the task.
	resourceID := clusterName + "/" + taskID

	for i, cd := range td.ContainerDefinitions {
		image := cd.Image

		// Pull the image (deduplicated).
		if err := h.puller.Ensure(ctx, image); err != nil {
			return fmt.Errorf("ecs: pull image %s: %w", image, err)
		}

		// Build environment variables.
		env := make([]string, 0, len(cd.Environment)+2)
		for _, kv := range cd.Environment {
			env = append(env, kv.Name+"="+kv.Value)
		}
		// Apply container overrides.
		if co, ok := overrides[cd.Name]; ok {
			for _, kv := range co.Environment {
				env = append(env, kv.Name+"="+kv.Value)
			}
		}
		// Add the Overcast endpoint so containers can call back into the emulator.
		env = append(env, fmt.Sprintf("AWS_ENDPOINT_URL=http://host.docker.internal:%d", h.cfg.Port))

		// Build command.
		var cmd []string
		if co, ok := overrides[cd.Name]; ok && len(co.Command) > 0 {
			cmd = co.Command
		} else if len(cd.Command) > 0 {
			cmd = cd.Command
		}

		// Build port bindings.
		var exposedPorts map[string]struct{}
		var portBindings map[string][]docker.PortBinding
		if len(cd.PortMappings) > 0 {
			exposedPorts = make(map[string]struct{}, len(cd.PortMappings))
			portBindings = make(map[string][]docker.PortBinding, len(cd.PortMappings))
			for _, pm := range cd.PortMappings {
				proto := pm.Protocol
				if proto == "" {
					proto = "tcp"
				}
				key := fmt.Sprintf("%d/%s", pm.ContainerPort, proto)
				exposedPorts[key] = struct{}{}
				if pm.HostPort > 0 {
					portBindings[key] = []docker.PortBinding{
						{HostIP: "0.0.0.0", HostPort: fmt.Sprintf("%d", pm.HostPort)},
					}
				}
			}
		}

		containerName := fmt.Sprintf("overcast-ecs-%s-%s-%s", clusterName, taskID[:8], cd.Name)

		ccfg := &docker.CreateContainerRequest{
			ContainerConfig: &docker.ContainerConfig{
				Image:        image,
				Env:          env,
				Cmd:          cmd,
				ExposedPorts: exposedPorts,
				Labels:       docker.ManagedLabels("ecs", resourceID),
			},
			HostConfig: &docker.HostConfig{AutoRemove: true,
				NetworkMode:  h.cfg.ECSNetwork,
				PortBindings: portBindings,
			},
			NetworkingConfig: &docker.NetworkingConfig{
				EndpointsConfig: map[string]*docker.EndpointSettings{
					h.cfg.ECSNetwork: {},
				},
			},
		}

		dockerID, err := h.docker.CreateContainer(ctx, containerName, ccfg)
		if err != nil {
			return fmt.Errorf("ecs: create container %s: %w", cd.Name, err)
		}

		if err := h.docker.StartContainer(ctx, dockerID); err != nil {
			_ = h.docker.RemoveContainerForce(dockerID)
			return fmt.Errorf("ecs: start container %s: %w", cd.Name, err)
		}
		if awsvpcNetworkID != "" {
			if err := h.docker.ConnectNetwork(ctx, awsvpcNetworkID, dockerID); err != nil {
				_ = h.docker.RemoveContainerForce(dockerID)
				return fmt.Errorf("ecs: connect container %s to VPC network %s: %w", cd.Name, awsvpcNetworkID, err)
			}
		}

		task.Containers[i].DockerID = dockerID
		task.Containers[i].RuntimeId = dockerID
	}

	// Schedule PROVISIONING → RUNNING transition with a short delay.
	capturedCluster := clusterName
	capturedTaskID := taskID
	h.scheduler.After(taskID+":pending", 200*time.Millisecond, func() {
		bgCtx := context.Background()
		got, aerr := h.store.getTask(bgCtx, capturedCluster, capturedTaskID)
		if aerr != nil || got == nil {
			return
		}
		if got.LastStatus == "PROVISIONING" || got.LastStatus == "PENDING" {
			got.LastStatus = "RUNNING"
			startedAt := h.clk.Now().Unix()
			got.StartedAt = &startedAt
			for j := range got.Containers {
				got.Containers[j].LastStatus = "RUNNING"
			}
			h.store.putTask(bgCtx, got) //nolint:errcheck
			if h.bus != nil {
				h.bus.Publish(bgCtx, events.Event{Type: events.ECSTaskStarted, Payload: events.ResourcePayload{Name: capturedTaskID}})
			}
		}
	})

	return nil
}

// scheduleMetadataTransition sets up the PROVISIONING → RUNNING transition for
// metadata-only tasks (no Docker).
func (h *Handler) scheduleMetadataTransition(clusterName, taskID string) {
	capturedCluster := clusterName
	capturedTaskID := taskID
	h.scheduler.After(taskID+":pending", 200*time.Millisecond, func() {
		ctx := context.Background()
		got, aerr := h.store.getTask(ctx, capturedCluster, capturedTaskID)
		if aerr != nil || got == nil {
			return
		}
		if got.LastStatus == "PROVISIONING" || got.LastStatus == "PENDING" {
			got.LastStatus = "RUNNING"
			startedAt := h.clk.Now().Unix()
			got.StartedAt = &startedAt
			for i := range got.Containers {
				got.Containers[i].LastStatus = "RUNNING"
			}
			h.store.putTask(ctx, got) //nolint:errcheck
			if h.bus != nil {
				h.bus.Publish(ctx, events.Event{Type: events.ECSTaskStarted, Payload: events.ResourcePayload{Name: capturedTaskID}})
			}
		}
	})
}

// StopTask handles AmazonEC2ContainerServiceV20141113.StopTask.
func (h *Handler) StopTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster string `json:"cluster"`
		Task    string `json:"task"`
		Reason  string `json:"reason"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Cluster == "" {
		req.Cluster = "default"
	}
	clusterName := extractClusterName(req.Cluster)
	taskID := extractTaskID(req.Task)

	task, aerr := h.store.getTask(r.Context(), clusterName, taskID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if task == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "The referenced task was not found.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// Cancel any pending scheduler transition.
	h.scheduler.Cancel(taskID + ":pending")

	// Stop Docker containers if Docker is available.
	if h.dockerReady.Load() {
		for _, c := range task.Containers {
			if c.DockerID == "" {
				continue
			}
			if h.gc != nil {
				h.gc.StopNow(c.DockerID)
				h.gc.ScheduleRemove(c.DockerID)
			} else {
				_ = h.docker.StopContainer(r.Context(), c.DockerID, 10)
				if !h.cfg.ECSKeepContainers {
					_ = h.docker.RemoveContainerForce(c.DockerID)
				}
			}
		}
	}

	task.LastStatus = "STOPPED"
	task.DesiredStatus = "STOPPED"
	task.StoppedReason = req.Reason
	stoppedAt := h.clk.Now().Unix()
	task.StoppedAt = &stoppedAt
	for i := range task.Containers {
		task.Containers[i].LastStatus = "STOPPED"
	}

	if aerr := h.store.putTask(r.Context(), task); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	h.publish(r, events.ECSTaskStopped, events.ResourcePayload{Name: taskID})

	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{"task": task}, "application/x-amz-json-1.1")
}

// DescribeTasks handles AmazonEC2ContainerServiceV20141113.DescribeTasks.
func (h *Handler) DescribeTasks(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster string   `json:"cluster"`
		Tasks   []string `json:"tasks"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Cluster == "" {
		req.Cluster = "default"
	}
	clusterName := extractClusterName(req.Cluster)

	type failure struct {
		Arn    string `json:"arn"`
		Reason string `json:"reason"`
	}
	found := make([]Task, 0, len(req.Tasks))
	failures := make([]failure, 0)

	for _, ref := range req.Tasks {
		taskID := extractTaskID(ref)
		task, aerr := h.store.getTask(r.Context(), clusterName, taskID)
		if aerr != nil || task == nil {
			arn := ref
			if !strings.HasPrefix(arn, "arn:") {
				arn = h.taskARN(r.Context(), clusterName, taskID)
			}
			failures = append(failures, failure{Arn: arn, Reason: "MISSING"})
			continue
		}
		found = append(found, *task)
	}

	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{
		"tasks":    found,
		"failures": failures,
	}, "application/x-amz-json-1.1")
}

// ListTasks handles AmazonEC2ContainerServiceV20141113.ListTasks.
func (h *Handler) ListTasks(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster       string `json:"cluster"`
		DesiredStatus string `json:"desiredStatus"`
		Family        string `json:"family"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Cluster == "" {
		req.Cluster = "default"
	}
	clusterName := extractClusterName(req.Cluster)

	tasks, aerr := h.store.listTasks(r.Context(), clusterName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	arns := make([]string, 0, len(tasks))
	for _, t := range tasks {
		if req.DesiredStatus != "" && t.DesiredStatus != req.DesiredStatus {
			continue
		}
		if req.Family != "" && !strings.Contains(t.TaskDefinitionArn, "/"+req.Family+":") {
			continue
		}
		arns = append(arns, t.TaskArn)
	}

	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{"taskArns": arns}, "application/x-amz-json-1.1")
}

// firstOrEmpty extracts a string from a pointer value using a getter, returning "" if nil.
func firstOrEmpty[T any](v *T, fn func(*T) string) string {
	if v == nil {
		return ""
	}
	return fn(v)
}
