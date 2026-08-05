package ecs

// handler_tasks.go — RunTask, StopTask, DescribeTasks, ListTasks handlers, and
// the locked read-modify-write of a single task record every writer goes
// through (lockTask).

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/containerendpoint"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
)

// taskLockStripes is how many mutexes lockTask spreads task records over.
const taskLockStripes = 64

// stoppedTaskRetention is the deterministic end of ECS's documented minimum
// visibility window. AWS guarantees that stopped tasks remain describable for
// at least one hour, while its console displays them for one hour:
// https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DescribeTasks.html
const stoppedTaskRetention = time.Hour

// AWS's StopTask response example uses this reason when the optional caller
// reason is omitted:
// https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_StopTask.html
const defaultUserStoppedReason = "Task stopped by user"

// lockTask serialises the read-modify-write of one task's record, returning the
// function that releases it. Same shape and same reason as lockService: a task
// is stored as one JSON blob, so every writer reads the whole record, edits it
// and writes the whole record back, and two of them overlapping means the
// second silently discards the first's edit.
//
// The pairing that made this necessary is a stop landing on a task that is
// still coming up. Every stop path cancels the task's pending PROVISIONING →
// RUNNING transition before it writes STOPPED, but cancelling only stops a
// timer that has not fired: a transition already inside its callback has read
// the task as PROVISIONING, and its write of RUNNING lands after the STOPPED
// one and resurrects the task. The transition's LastStatus guard narrows that
// window and cannot close it, because the read and the write are not one step.
// Holding this lock across both is what closes it.
//
// Striped, where lockService keeps one mutex per service, because the two have
// different lifetimes: services are few and long-lived, while a service in a
// crash loop replaces its tasks for as long as it runs, so a mutex per task ID
// would grow for the life of the process. Two tasks that share a stripe
// serialise needlessly; a critical section here is one read and one write of a
// single record, with container teardown deliberately left outside it.
//
// Region is not part of the key. Task IDs are UUIDs, so cluster and ID identify
// a task on their own, and leaving the region out lets the Docker exit notifier
// take the lock before it has scanned for the region the task is stored under.
//
// Lock ordering is service-then-task, never the reverse: stopServiceTasks takes
// a task lock while reconcile holds the service lock, so anything holding a
// task lock must release it before it locks a service.
func (h *Handler) lockTask(clusterName, taskID string) func() {
	mu := &h.taskLocks[taskLockStripe(clusterName+"/"+taskID)]
	mu.Lock()
	return mu.Unlock
}

// taskLockStripe picks a task's stripe by FNV-1a. Hand-rolled rather than
// seeded (hash/maphash) so a zero-value Handler — which several tests build —
// needs no initialisation to be safe to lock.
func taskLockStripe(key string) uint32 {
	const (
		offset32 = 2166136261
		prime32  = 16777619
	)
	sum := uint32(offset32)
	for i := 0; i < len(key); i++ {
		sum ^= uint32(key[i])
		sum *= prime32
	}
	return sum % taskLockStripes
}

// taskStop is what a stop path records on a task's record.
type taskStop struct {
	reason string
	code   string
}

// stopTaskRecord writes a stop onto one task's record under lockTask, and
// returns the stored task so the caller can tear its containers down outside
// the lock. changed is false when there was nothing left to stop.
//
// It re-reads the task inside the lock rather than writing back the copy the
// caller already had: that copy predates the lock, so it can be missing a
// transition to RUNNING that landed in between, and writing it back would undo
// more than the status.
//
// A task that is already STOPPED is left exactly as it is. One that died on its
// own between the caller's read and this write keeps the stop code and reason
// it died with — overwriting them would file a container that exited on its own
// under the stopper's name and take a deployment failure off the books, which
// is the same mistake the exit notifier avoids in the other direction.
func (h *Handler) stopTaskRecord(ctx context.Context, clusterName, taskID string, stop taskStop) (task *Task, changed bool, aerr *protocol.AWSError) {
	defer h.lockTask(clusterName, taskID)()

	task, aerr = h.store.getTask(ctx, clusterName, taskID)
	if aerr != nil || task == nil {
		return nil, false, aerr
	}
	if task.LastStatus == "STOPPED" {
		h.deleteStoppedTaskTags(ctx, task)
		return task, false, nil
	}
	if stop.code == "UserInitiated" && stop.reason == "" {
		stop.reason = defaultUserStoppedReason
	}

	task.LastStatus = "STOPPED"
	task.DesiredStatus = "STOPPED"
	task.StoppedReason = stop.reason
	task.StopCode = stop.code
	stoppedAt := h.clk.Now().Unix()
	task.StoppedAt = &stoppedAt
	task.StoppingAt = &stoppedAt
	for i := range task.Containers {
		task.Containers[i].LastStatus = "STOPPED"
	}
	if aerr := h.store.putTask(ctx, task); aerr != nil {
		return nil, false, aerr
	}
	h.deleteStoppedTaskTags(ctx, task)
	return task, true, nil
}

func (h *Handler) deleteStoppedTaskTags(ctx context.Context, task *Task) {
	if aerr := h.store.deleteTags(ctx, task.TaskArn); aerr != nil && h.log != nil {
		h.log.Warn("ecs: failed to delete stopped task tags",
			zap.String("task", task.TaskArn),
			zap.String("error", aerr.Message))
	}
}

func (h *Handler) stoppedTaskExpired(task *Task) bool {
	if task == nil || task.LastStatus != "STOPPED" || task.StoppedAt == nil {
		return false
	}
	return !h.clk.Now().Before(time.Unix(*task.StoppedAt, 0).Add(stoppedTaskRetention))
}

// getVisibleTask applies ECS's stopped-task retention contract and lazily
// removes expired records so both memory and persistent stores stay bounded.
func (h *Handler) getVisibleTask(ctx context.Context, clusterName, taskID string) (*Task, *protocol.AWSError) {
	task, aerr := h.store.getTask(ctx, clusterName, taskID)
	if aerr != nil || !h.stoppedTaskExpired(task) {
		return task, aerr
	}
	if aerr := h.store.deleteTags(ctx, task.TaskArn); aerr != nil {
		return nil, aerr
	}
	if aerr := h.store.deleteTask(ctx, clusterName, taskID); aerr != nil {
		return nil, aerr
	}
	return nil, nil
}

func (h *Handler) listVisibleTasks(ctx context.Context, clusterName string) ([]Task, *protocol.AWSError) {
	tasks, aerr := h.store.listTasks(ctx, clusterName)
	if aerr != nil {
		return nil, aerr
	}
	visible := make([]Task, 0, len(tasks))
	for i := range tasks {
		if !h.stoppedTaskExpired(&tasks[i]) {
			visible = append(visible, tasks[i])
			continue
		}
		if aerr := h.store.deleteTags(ctx, tasks[i].TaskArn); aerr != nil {
			return nil, aerr
		}
		if aerr := h.store.deleteTask(ctx, clusterName, extractTaskID(tasks[i].TaskArn)); aerr != nil {
			return nil, aerr
		}
	}
	return visible, nil
}

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
		StartedBy            string                `json:"startedBy"`
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

	// awsvpc networking is required by the task definition's networkMode, so
	// this can only be checked once the task definition has been resolved.
	if aerr := validateAwsvpcNetworkConfiguration(td, req.LaunchType, req.NetworkConfiguration); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Determine platform version for Fargate.
	platformVersion := req.PlatformVersion
	if platformVersion == "" && req.LaunchType == "FARGATE" {
		platformVersion = "LATEST"
	}

	placement, placementErr := h.resolveAwsvpcPlacement(r.Context(), req.NetworkConfiguration, "awsvpc tasks")
	if placementErr != nil {
		protocol.WriteJSONError(w, r, placementErr)
		return
	}

	tasks := make([]Task, 0, req.Count)
	for i := 0; i < req.Count; i++ {
		// A task that fails to start is returned like any other: AWS reports
		// the ARN from RunTask and the failure through the task's own STOPPED
		// state, reserving the failures array for placement rejections.
		task, aerr, _ := h.launchTask(r.Context(), taskLaunchSpec{
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
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		tasks = append(tasks, *task)
	}

	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{
		"tasks":    tasks,
		"failures": []any{},
	}, "application/x-amz-json-1.1")
}

// startTaskContainers creates and starts Docker containers for all container
// definitions in a task. On success, task containers are updated with
// DockerIDs. The RUNNING transition is queued by launchTask once the task
// record has been persisted, not here — see the comment there.
func (h *Handler) startTaskContainers(ctx context.Context, task *Task, td *TaskDefinition, clusterName, taskID string, placement awsvpcPlacement) error {
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

		// Pull the image (deduplicated). The puller's error already names the
		// image and the failure, so it is not wrapped again — this message
		// reaches the user as a task's stoppedReason.
		if err := h.puller.Ensure(ctx, image); err != nil {
			return fmt.Errorf("ecs: %w", err)
		}

		// Build environment variables, including any resolved from Secrets
		// Manager or SSM via the definition's secrets.
		env := buildContainerEnv(cd, overrides[cd.Name], endpoint)
		env = append(env, h.secretEnv(ctx, cd)...)

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
				Image: image,
				Env:   env,
				Cmd:   cmd,
				// entryPoint and workingDirectory override the image's own, as
				// they do on ECS. A task definition that sets either and has it
				// dropped runs something other than what it asked for, silently.
				Entrypoint:   cd.EntryPoint,
				WorkingDir:   cd.WorkingDirectory,
				User:         cd.User,
				ExposedPorts: exposedPorts,
				Labels:       mergeDockerLabels(docker.ManagedLabels("ecs", resourceID), cd.DockerLabels),
			},
			// Keep the container through Docker's die event so the final bounded
			// log tail can be retained for post-mortem diagnostics. The exit
			// notifier schedules removal immediately after capturing it.
			HostConfig: &docker.HostConfig{AutoRemove: false,
				Privileged:   cd.Privileged != nil && *cd.Privileged,
				Mounts:       h.efsMountsForContainer(ctx, td, &td.ContainerDefinitions[i]),
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
		if placement.networkID != "" {
			// Only the first container carries the task's ENI address. AWS gives
			// every container in an awsvpc task one shared network namespace and
			// so one address; Docker gives each container its own, and the task
			// reports a single privateIPv4Address either way.
			if err := h.attachTaskENI(ctx, task, placement, dockerID, i == 0); err != nil {
				_ = h.docker.RemoveContainerForce(dockerID)
				return fmt.Errorf("ecs: connect container %s to VPC network %s: %w", cd.Name, placement.networkID, err)
			}
		}

		task.Containers[i].DockerID = dockerID
		task.Containers[i].RuntimeId = dockerID

		// Ship this container's output to CloudWatch Logs when the task
		// definition asked for the awslogs driver. Started after the container
		// is running so the stream exists to attach to.
		h.startLogStreaming(ctx, dockerID, taskID, cd)
	}

	return nil
}

// mergeDockerLabels adds a container definition's dockerLabels to Overcast's
// own. Overcast's win: the GC and the exit notifier find containers by them.
func mergeDockerLabels(managed, extra map[string]string) map[string]string {
	if len(extra) == 0 {
		return managed
	}
	merged := make(map[string]string, len(managed)+len(extra))
	for k, v := range extra {
		merged[k] = v
	}
	for k, v := range managed {
		merged[k] = v
	}
	return merged
}

// containerEndpoint returns the endpoint mapper for ECS task containers,
// resolving Overcast's container-reachable address once on first use. Resolving
// lazily rather than at SetDocker time keeps the ordering honest: the address
// may be Overcast's own IP on the ECS network, which only exists once the
// network has been created.
// efsMountsForContainer resolves a container's mount points against the task
// definition's EFS-backed volumes, returning Docker volume mounts. The mount
// is scoped by a volume Subpath from either the authorization access point's
// root directory or the efsVolumeConfiguration's rootDirectory. Non-EFS
// volumes and unresolvable references (EFS mock mode, Docker down, unknown
// IDs) are skipped with a warning so the task still starts — mirroring the
// best-effort posture of EFS live mode. A rootDirectory subpath that does not
// exist in the volume makes the container start fail, which mirrors AWS's
// mount failure for missing root directories.
// efsMountSkipReason explains why an EFS volume could not be resolved, which
// is almost always the mode EFS is running in rather than anything wrong with
// the task definition. The three causes need different things from the reader,
// so they get different messages instead of one that lists all three.
func (h *Handler) efsMountSkipReason() (msg, hint string) {
	switch {
	case h.cfg != nil && h.cfg.EFSMode != config.EFSModeLive:
		return "ecs: EFS mount skipped — EFS is in mock mode, so file systems have no storage behind them",
			"the EFS control plane is fully emulated in mock mode (file systems, mount targets, access points, tags) — only the data plane is absent. Live mode is the default and backs each file system with a Docker volume tasks can actually read and write; drop OVERCAST_EFS_MODE=mock from this instance's environment to get it."
	case !h.dockerReady.Load():
		return "ecs: EFS mount skipped — no container runtime, so there is no volume to mount",
			"EFS live mode backs file systems with Docker volumes; start Overcast with a Docker socket available."
	default:
		return "ecs: EFS mount skipped — the file system or access point in the task definition was not found",
			"check that the efsVolumeConfiguration references a file system (and access point) that exists in this region."
	}
}

func (h *Handler) efsMountsForContainer(ctx context.Context, td *TaskDefinition, cd *ContainerDefinition) []docker.Mount {
	if len(cd.MountPoints) == 0 || h.efsResolver == nil {
		return nil
	}
	volumesByName := make(map[string]*TaskVolume, len(td.Volumes))
	for i := range td.Volumes {
		volumesByName[td.Volumes[i].Name] = &td.Volumes[i]
	}
	mounts := make([]docker.Mount, 0, len(cd.MountPoints))
	for _, mp := range cd.MountPoints {
		v := volumesByName[mp.SourceVolume]
		if v == nil || v.EFSVolumeConfiguration == nil {
			h.log.Warn("ecs: mount point skipped — source volume is not EFS-backed",
				zap.String("container", cd.Name), zap.String("volume", mp.SourceVolume))
			continue
		}
		cfg := v.EFSVolumeConfiguration
		var volume, subpath string
		var ok bool
		if cfg.AuthorizationConfig != nil && cfg.AuthorizationConfig.AccessPointId != "" {
			volume, subpath, ok = h.efsResolver.EFSVolumeForAccessPointID(ctx, cfg.AuthorizationConfig.AccessPointId)
		} else {
			volume, ok = h.efsResolver.EFSVolumeForFileSystem(ctx, cfg.FileSystemId)
			subpath = strings.Trim(cfg.RootDirectory, "/")
		}
		if !ok {
			msg, hint := h.efsMountSkipReason()
			h.log.Warn(msg,
				zap.String("container", cd.Name),
				zap.String("file_system", cfg.FileSystemId),
				zap.String("container_path", mp.ContainerPath),
				zap.String("consequence", "the container starts without the mount: writes to "+mp.ContainerPath+" go to its own writable layer and are lost when the task stops, and nothing is shared with other tasks"),
				zap.String("hint", hint))
			continue
		}
		m := docker.Mount{Type: "volume", Source: volume, Target: mp.ContainerPath, ReadOnly: mp.ReadOnly}
		if subpath != "" {
			m.VolumeOptions = &docker.MountVolumeOptions{Subpath: subpath}
		}
		mounts = append(mounts, m)
	}
	if len(mounts) == 0 {
		return nil
	}
	return mounts
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

// scheduleRunningTransition sets up a task's PROVISIONING → RUNNING transition.
// Both placement paths use it: Docker-backed tasks, whose containers are
// already started by the time it runs, and metadata-only tasks placed while no
// container runtime is wired. region is the region the task is stored under,
// resolved from the request context at schedule time.
//
// A task owned by a service reconciles that service when it starts, which is
// what moves the service to its running count and, at the desired count, into
// a steady state — and, mid-rollout, what retires the task the new one has
// just replaced.
func (h *Handler) scheduleRunningTransition(region, clusterName, taskID string) {
	capturedCluster := clusterName
	capturedTaskID := taskID
	h.scheduler.AfterScoped(region, taskID, "pending", 200*time.Millisecond, func(ctx context.Context) {
		started := h.applyRunningTransition(ctx, capturedCluster, capturedTaskID)
		if started == nil {
			return
		}
		if h.bus != nil {
			h.bus.Publish(ctx, events.Event{Type: events.ECSTaskStarted, Payload: events.ResourcePayload{Name: capturedTaskID}})
		}
		if serviceName, ok := serviceNameFromGroup(started.Group); ok {
			h.reconcile(ctx, capturedCluster, serviceName)
		}
	})
}

// applyRunningTransition moves one task from PROVISIONING/PENDING to RUNNING,
// returning the stored task, or nil when the task is gone or has already left
// those states — a task stopped while it was coming up must stay stopped.
//
// Under lockTask, and the lock is released before the caller reconciles the
// owning service: the read and the write are one step, and the ordering stays
// service-then-task. See lockTask.
func (h *Handler) applyRunningTransition(ctx context.Context, clusterName, taskID string) *Task {
	defer h.lockTask(clusterName, taskID)()

	got, aerr := h.store.getTask(ctx, clusterName, taskID)
	if aerr != nil || got == nil {
		return nil
	}
	if got.LastStatus != "PROVISIONING" && got.LastStatus != "PENDING" {
		return nil
	}
	got.LastStatus = "RUNNING"
	startedAt := h.clk.Now().Unix()
	got.StartedAt = &startedAt
	for i := range got.Containers {
		got.Containers[i].LastStatus = "RUNNING"
	}
	// A running awsvpc task's ENI is ATTACHED, not still ATTACHING — that
	// is the status callers wait on before treating the address as usable.
	for i := range got.Attachments {
		if got.Attachments[i].Type == "ElasticNetworkInterface" && got.Attachments[i].Status == "ATTACHING" {
			got.Attachments[i].Status = "ATTACHED"
		}
	}
	if aerr := h.store.putTask(ctx, got); aerr != nil {
		return nil
	}
	return got
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

	// A task a service owns leaves that service's target groups before it is
	// torn down, and the service then replaces it — its desired count has not
	// changed just because one task was stopped by hand. Both used to fall out
	// of the container's die event; they belong here now that the exit notifier
	// leaves a task the caller already stopped alone.
	serviceName, ownedByService := serviceNameFromGroup(task.Group)
	if ownedByService {
		if svc, aerr := h.store.getService(r.Context(), clusterName, serviceName); aerr == nil && svc != nil {
			h.deregisterTaskTargets(r.Context(), svc, task)
		}
	}

	// Record the stop before touching the containers: killing them raises a
	// Docker die event whose handler races this write, and a caller-initiated
	// stop that loses the race is reported as a task that died on its own.
	task, _, aerr = h.stopTaskRecord(r.Context(), clusterName, taskID, taskStop{
		reason: req.Reason, code: "UserInitiated",
	})
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
	h.publish(r, events.ECSTaskStopped, events.ResourcePayload{Name: taskID})
	if ownedByService {
		h.scheduleServiceReplacement(r.Context(), h.store.region(r.Context()), clusterName, serviceName)
	}

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
		task, aerr := h.getVisibleTask(r.Context(), clusterName, taskID)
		if aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		if task == nil {
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

	tasks, aerr := h.listVisibleTasks(r.Context(), clusterName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
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

	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{"taskArns": arns}, "application/x-amz-json-1.1")
}

// firstOrEmpty extracts a string from a pointer value using a getter, returning "" if nil.
func firstOrEmpty[T any](v *T, fn func(*T) string) string {
	if v == nil {
		return ""
	}
	return fn(v)
}
