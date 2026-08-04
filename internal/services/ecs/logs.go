package ecs

// logs.go — the awslogs log driver.
//
// A task definition that asks for `logDriver: awslogs` gets its container
// output in CloudWatch Logs, which is where ECS puts it and therefore the only
// place a user thinks to look. Without this a task that crash-loops explains
// itself nowhere: the container is gone before `docker logs` can reach it, and
// the emulator's own log only says the task stopped.

import (
	"bufio"
	"context"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
)

// maxLogLine bounds a single log line so a container emitting one enormous
// unterminated line cannot grow the buffer without limit.
const maxLogLine = 256 * 1024

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

// startLogStreaming pumps one container's output into CloudWatch Logs in the
// background, until the container exits or the emulator stops.
//
// The pump deliberately does not use the request context: it has to outlive the
// RunTask/CreateService call that started the container. The region is carried
// over from that call so the lines land in the same region the task did.
func (h *Handler) startLogStreaming(ctx context.Context, dockerID, taskID string, cd ContainerDefinition) {
	target, ok := awslogsTargetFor(cd, taskID)
	if !ok || h.logWriter == nil || h.docker == nil {
		return
	}

	pumpCtx := middleware.ContextWithRegion(context.Background(), h.store.region(ctx))
	if err := h.logWriter.EnsureLogGroup(pumpCtx, target.group); err != nil {
		h.log.Warn("ecs: awslogs: could not create log group",
			zap.String("group", target.group), zap.Error(err))
		return
	}
	if err := h.logWriter.EnsureLogStream(pumpCtx, target.group, target.stream); err != nil {
		h.log.Warn("ecs: awslogs: could not create log stream",
			zap.String("group", target.group), zap.String("stream", target.stream), zap.Error(err))
		return
	}

	go h.pumpContainerLogs(pumpCtx, dockerID, target)
}

// pumpContainerLogs copies a container's log stream into CloudWatch Logs one
// line at a time.
func (h *Handler) pumpContainerLogs(ctx context.Context, dockerID string, target awslogsTarget) {
	stream, err := h.docker.ContainerLogsStream(ctx, dockerID, time.Time{})
	if err != nil {
		h.log.Warn("ecs: awslogs: could not open container log stream",
			zap.String("container", dockerID), zap.Error(err))
		return
	}
	defer stream.Close() //nolint:errcheck

	scanner := bufio.NewScanner(docker.NewDemuxReader(stream))
	scanner.Buffer(make([]byte, 0, 64*1024), maxLogLine)
	for scanner.Scan() {
		ts, message := splitDockerTimestamp(scanner.Text())
		if message == "" {
			continue
		}
		err := h.logWriter.WriteLogEvents(ctx, target.group, target.stream, []events.LogEntry{
			{Timestamp: ts, Message: message},
		})
		if err != nil {
			// A stream that vanished under us (state reset, log group deleted)
			// is not worth retrying the whole container's output for.
			h.log.Debug("ecs: awslogs: write failed",
				zap.String("group", target.group), zap.String("stream", target.stream), zap.Error(err))
			return
		}
	}
}

// splitDockerTimestamp separates the RFC3339 timestamp Docker prefixes onto each
// line (the stream is opened with timestamps=true) from the message itself,
// falling back to now for a line that does not carry one.
func splitDockerTimestamp(line string) (int64, string) {
	ts, msg, found := strings.Cut(line, " ")
	if !found {
		return time.Now().UnixMilli(), strings.TrimRight(line, "\r")
	}
	parsed, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Now().UnixMilli(), strings.TrimRight(line, "\r")
	}
	return parsed.UnixMilli(), strings.TrimRight(msg, "\r")
}
