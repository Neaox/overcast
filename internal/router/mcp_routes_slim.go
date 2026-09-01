//go:build slim

package router

import (
	"github.com/go-chi/chi/v5"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/state"
	"go.uber.org/zap"
)

// registerMCPRoutes is intentionally a no-op for slim builds.
func registerMCPRoutes(_ chi.Router, _ *config.Config, _ state.Store, _ *events.Bus, _ *zap.Logger, _ <-chan struct{}) {
}
