package efs

import (
	"context"
	"strings"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/events"
)

// handleExportContainerDied immediately schedules the same idempotent sweep
// used at startup/reconnect. Export exits are rare, and one service-scoped list
// avoids duplicating the adoption, ownership, port, and delete-race rules here.
func (s *Service) handleExportContainerDied(ctx context.Context, e events.Event) {
	p, ok := e.Payload.(events.DockerContainerPayload)
	if !ok || p.Service != serviceName || !strings.HasPrefix(p.ResourceID, "fsmt-") || !s.volumesActive() {
		return
	}
	containers, err := s.docker.ListContainers(ctx, serviceName)
	if err != nil {
		s.log.Warn("efs: reconcile after export container exit", zap.Error(err))
		return
	}
	s.ReconcileContainers(ctx, containers)
}
