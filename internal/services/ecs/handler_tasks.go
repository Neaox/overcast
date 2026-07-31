package ecs

// handler_tasks.go — RunTask, StopTask, DescribeTasks, ListTasks handlers.

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/containerendpoint"
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
				h.scheduleMetadataTransition(h.store.region(r.Context()), clusterName, taskID)
			}
		} else {
			h.scheduleMetadataTransition(h.store.region(r.Context()), clusterName, taskID)
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

	// Resolve how task containers reach Overcast. Deferred until here because
	// it may attach Overcast to the ECS network, which must exist first.
	endpoint := h.containerEndpoint(ctx)

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
		env := buildContainerEnv(cd, overrides[cd.Name], endpoint)

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
				Binds:        h.efsBindsForContainer(ctx, td, &td.ContainerDefinitions[i]),
				NetworkMode:  h.cfg.ECSNetwork,
				PortBindings: portBindings,
				ExtraHosts:   endpoint.ExtraHosts(),
				Dns:          endpoint.DNSServers(),
			},
			NetworkingConfig: &docker.NetworkingConfig{
				EndpointsConfig: map[string]*docker.EndpointSettings{
					h.cfg.ECSNetwork: {},
				},
			},
		}

		// The puller retries once when the image was removed behind our back
		// (docker rmi after the recorded pull) instead of failing until restart.
		dockerID, err := h.puller.CreateContainerWithRetry(ctx, containerName, ccfg)
		if err != nil {
			return fmt.Errorf("ecs: create container %s: %w", cd.Name, err)
		}

		// With TLS on, the task must trust the CA that minted Overcast's
		// certificate before its first SDK call. Same mechanism as function
		// code: CopyToContainer, because a dockerized Overcast has no host
		// path to bind-mount from.
		if caTar, caErr := endpoint.CABundleTar(); caErr != nil {
			h.log.ZapLogger().Warn("ecs: CA bundle unavailable; task TLS calls to Overcast will fail verification", zap.Error(caErr))
		} else if caTar != nil {
			if err := h.docker.CopyToContainer(ctx, dockerID, "/", bytes.NewReader(caTar)); err != nil {
				_ = h.docker.RemoveContainerForce(dockerID)
				return fmt.Errorf("ecs: inject CA bundle into %s: %w", cd.Name, err)
			}
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
	h.scheduler.AfterScoped(h.store.region(ctx), taskID, "pending", 200*time.Millisecond, func(bgCtx context.Context) {
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

// containerEndpoint returns the endpoint mapper for ECS task containers,
// resolving Overcast's container-reachable address once on first use. Resolving
// lazily rather than at SetDocker time keeps the ordering honest: the address
// may be Overcast's own IP on the ECS network, which only exists once the
// network has been created.
// efsBindsForContainer resolves a container's mount points against the task
// definition's EFS-backed volumes, returning Docker named-volume bind entries
// ("volume:/path[:ro]"). Non-EFS volumes and unresolvable file systems (EFS
// mock mode, Docker down, unknown ID) are skipped with a warning so the task
// still starts — mirroring the best-effort posture of EFS live mode.
func (h *Handler) efsBindsForContainer(ctx context.Context, td *TaskDefinition, cd *ContainerDefinition) []string {
	if len(cd.MountPoints) == 0 || h.efsResolver == nil {
		return nil
	}
	volumesByName := make(map[string]*TaskVolume, len(td.Volumes))
	for i := range td.Volumes {
		volumesByName[td.Volumes[i].Name] = &td.Volumes[i]
	}
	binds := make([]string, 0, len(cd.MountPoints))
	for _, mp := range cd.MountPoints {
		v := volumesByName[mp.SourceVolume]
		if v == nil || v.EFSVolumeConfiguration == nil {
			h.log.Warn("ecs: mount point skipped — source volume is not EFS-backed",
				zap.String("container", cd.Name), zap.String("volume", mp.SourceVolume))
			continue
		}
		volume, ok := h.efsResolver.EFSVolumeForFileSystem(ctx, v.EFSVolumeConfiguration.FileSystemId)
		if !ok {
			h.log.Warn("ecs: EFS mount skipped — file system has no backing volume (mock mode, Docker down, or unknown file system)",
				zap.String("container", cd.Name),
				zap.String("file_system", v.EFSVolumeConfiguration.FileSystemId),
				zap.String("container_path", mp.ContainerPath))
			continue
		}
		bind := volume + ":" + mp.ContainerPath
		if mp.ReadOnly {
			bind += ":ro"
		}
		binds = append(binds, bind)
	}
	if len(binds) == 0 {
		return nil
	}
	return binds
}

func (h *Handler) containerEndpoint(ctx context.Context) *containerendpoint.Mapper {
	h.endpointOnce.Do(func() {
		// ResolveHost + BaseURL rather than Resolve: the endpoint scheme must
		// follow the listener (https under OVERCAST_TLS), which only config knows.
		host := containerendpoint.ResolveHost(ctx, h.docker, h.cfg.ECSNetwork, h.log.ZapLogger())
		h.endpoint = containerendpoint.New(h.cfg, containerendpoint.BaseURL(h.cfg, host)).WithPublishedPort(h.cfg.PublishedPort)
	})
	return h.endpoint
}

// buildContainerEnv builds the Docker environment for one task container:
// task-definition environment, then any RunTask container override (applied
// last so it wins, as in ECS), then Overcast's endpoint.
//
// Values are passed through the endpoint mapper because AWS SDKs resolve the
// SQS endpoint from the QueueUrl rather than from AWS_ENDPOINT_URL, so a queue
// URL baked in by a host-side deploy would otherwise point the task's SQS
// client at the task's own loopback. See internal/containerendpoint.
func buildContainerEnv(cd ContainerDefinition, co *ContainerOverride, endpoint *containerendpoint.Mapper) []string {
	env := make([]string, 0, len(cd.Environment)+2)
	appendKV := func(kvs []KeyValuePair) {
		for _, kv := range kvs {
			env = append(env, kv.Name+"="+endpoint.RewriteURLs(kv.Value))
		}
	}

	appendKV(cd.Environment)
	if co != nil {
		appendKV(co.Environment)
	}

	// Add the Overcast endpoint so containers can call back into the emulator.
	// Named rather than numbered: it survives Overcast being recreated on a
	// different address, and it lets an SDK derive virtual-hosted URLs from it.
	if addr := endpoint.ClientEndpoint(); addr != "" {
		env = append(env, "AWS_ENDPOINT_URL="+addr)
	}
	// With TLS on, point every SDK TLS stack at the injected CA bundle.
	for k, v := range endpoint.CABundleEnv() {
		env = append(env, k+"="+v)
	}
	return env
}

// scheduleMetadataTransition sets up the PROVISIONING → RUNNING transition for
// metadata-only tasks (no Docker). region is the region the task is stored
// under, resolved from the request context at schedule time.
func (h *Handler) scheduleMetadataTransition(region, clusterName, taskID string) {
	capturedCluster := clusterName
	capturedTaskID := taskID
	h.scheduler.AfterScoped(region, taskID, "pending", 200*time.Millisecond, func(ctx context.Context) {
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
	h.scheduler.CancelScoped(h.store.region(r.Context()), taskID, "pending")

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
