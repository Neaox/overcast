//go:build slim

package router

import (
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/state"
	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

// registerMCPRoutes is intentionally a no-op for slim builds.
func registerMCPRoutes(_ chi.Router, _ *config.Config, _ state.Store, _ *events.Bus, _ *zap.Logger) {
}
