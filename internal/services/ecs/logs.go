package ecs

// logs.go — the awslogs log driver.
//
// A task definition that asks for `logDriver: awslogs` gets its container
// output in CloudWatch Logs, which is where ECS puts it and therefore the only
// place a user thinks to look. Without this a task that crash-loops explains
// itself nowhere: the container is gone before `docker logs` can reach it, and
// the emulator's own log only says the task stopped.
//
// On AWS the awslogs driver runs inside the Docker daemon, so reading a
// container's output back from the daemon is the right model here. Reading it
// back *well* is not this file's job: internal/containerlogs owns the
// reconnect, the de-duplication across one, bounded line assembly and batched
// writes. What is left here is where a container's output goes, and how long
// the follower that ships it lives.

import (
	"context"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/containerlogs"
	"github.com/overcast-sh/overcast/internal/middleware"
)

// awslogsTarget is where one container's output goes.
type awslogsTarget struct {
	group  string
	stream string
}

// awslogsTargetFor returns the CloudWatch destination for a container, and
// whether the container asked for the awslogs driver at all.
//
// The stream name follows the one ECS builds: `<prefix>/<container>/<task-id>`,
// with the prefix omitted when the task definition did not set one.
func awslogsTargetFor(cd ContainerDefinition, taskID string) (awslogsTarget, bool) {
	if cd.LogConfiguration == nil || cd.LogConfiguration.LogDriver != "awslogs" {
		return awslogsTarget{}, false
	}
	group := cd.LogConfiguration.Options["awslogs-group"]
	if group == "" {
		return awslogsTarget{}, false
	}
	stream := cd.Name + "/" + taskID
	if prefix := cd.LogConfiguration.Options["awslogs-stream-prefix"]; prefix != "" {
		stream = prefix + "/" + stream
	}
	return awslogsTarget{group: group, stream: stream}, true
}

// logPump is one container's running follower, held only so it can be stopped.
type logPump struct {
	cancel context.CancelFunc
}

// startLogStreaming pumps one container's output into CloudWatch Logs in the
// background, until the container stops or the emulator does.
//
// The pump deliberately does not use the request context: it has to outlive the
// RunTask/CreateService call that started the container. The region is carried
// over from that call so the lines land in the same region the task did.
func (h *Handler) startLogStreaming(ctx context.Context, dockerID, taskID string, cd ContainerDefinition) {
	target, ok := awslogsTargetFor(cd, taskID)
	if !ok || h.logWriter == nil || h.docker == nil {
		return
	}

	region := h.store.region(ctx)
	pumpCtx, cancel := context.WithCancel(middleware.ContextWithRegion(context.Background(), region))
	if err := h.logWriter.EnsureLogGroup(pumpCtx, target.group); err != nil {
		cancel()
		h.log.Warn("ecs: awslogs: could not create log group",
			zap.String("group", target.group), zap.Error(err))
		return
	}
	if err := h.logWriter.EnsureLogStream(pumpCtx, target.group, target.stream); err != nil {
		cancel()
		h.log.Warn("ecs: awslogs: could not create log stream",
			zap.String("group", target.group), zap.String("stream", target.stream), zap.Error(err))
		return
	}

	follower := h.newAwslogsFollower(pumpCtx, h.docker, dockerID, region, target)
	pump := h.registerLogPump(dockerID, cancel)
	go func() {
		defer h.forgetLogPump(dockerID, pump)
		follower.Follow(pumpCtx)
		// Follow returns only when the pump's context is cancelled, which is
		// what stopLogStreaming does once the container has ended. That closes
		// the daemon connection under a read that may have been mid-line, so
		// one non-streaming pass over the daemon's persisted copy picks up
		// whatever it cost: the copy is complete now the container has stopped,
		// and the follower's cursor means only what is genuinely missing gets
		// written. It races the container's removal, and loses harmlessly.
		reconcileCtx, reconcileCancel := context.WithTimeout(
			middleware.ContextWithRegion(context.Background(), region), containerLogCaptureTimeout)
		defer reconcileCancel()
		follower.Reconcile(reconcileCtx)
	}()
}

// newAwslogsFollower builds the follower that ships one container's output to a
// CloudWatch log stream. The daemon client is a parameter rather than h.docker
// so a test can drive the whole pipeline — reconnect included — without one.
func (h *Handler) newAwslogsFollower(
	ctx context.Context,
	client containerlogs.LogStreamer,
	dockerID, region string,
	target awslogsTarget,
) *containerlogs.Follower {
	return containerlogs.New(containerlogs.Config{
		Client:      client,
		ContainerID: dockerID,
		Clock:       h.clk,
		Logger:      h.log.ZapLogger(),
		Sink: containerlogs.NewCloudWatchBatcher(containerlogs.BatcherConfig{
			Writer:  h.logWriter,
			Group:   target.group,
			Stream:  target.stream,
			Context: ctx,
			Region:  region,
			Clock:   h.clk,
			Logger:  h.log.ZapLogger(),
		}),
	})
}

// registerLogPump records a container's follower so it can be stopped when the
// container is.
func (h *Handler) registerLogPump(dockerID string, cancel context.CancelFunc) *logPump {
	pump := &logPump{cancel: cancel}
	h.logPumpsMu.Lock()
	defer h.logPumpsMu.Unlock()
	if existing := h.logPumps[dockerID]; existing != nil {
		existing.cancel()
	}
	if h.logPumps == nil {
		h.logPumps = make(map[string]*logPump)
	}
	h.logPumps[dockerID] = pump
	return pump
}

// forgetLogPump drops a follower's registration once it has returned, unless a
// later one has already taken its place.
func (h *Handler) forgetLogPump(dockerID string, pump *logPump) {
	h.logPumpsMu.Lock()
	defer h.logPumpsMu.Unlock()
	if h.logPumps[dockerID] == pump {
		delete(h.logPumps, dockerID)
	}
}

// stopLogStreaming stops a container's follower.
//
// A follower reconnects whenever its stream ends, which is what keeps a running
// container's output flowing across a daemon hiccup — and which means a stopped
// container would otherwise be re-read for as long as the emulator lives.
// Cancelling makes the follower deliver what it is holding and let go. Safe to
// call for a container that never had one.
func (h *Handler) stopLogStreaming(dockerID string) {
	h.logPumpsMu.Lock()
	pump := h.logPumps[dockerID]
	delete(h.logPumps, dockerID)
	h.logPumpsMu.Unlock()
	if pump != nil {
		pump.cancel()
	}
}
