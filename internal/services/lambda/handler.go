package lambda

// handler.go contains the Handler struct for Lambda.
// Lambda uses REST routing (not target-dispatch), so there is no ops map.
// Route registration is done in service.go's RegisterRoutes.
// Stub handlers live in handler_stubs.go; implemented handlers live in
// handler_<group>.go files as they are built out.

import (
	"github.com/your-org/overcast/internal/clock"
	"github.com/your-org/overcast/internal/config"
	"github.com/your-org/overcast/internal/serviceutil"
)

// Handler holds Lambda handler dependencies.
type Handler struct {
	cfg      *config.Config
	log      *serviceutil.ServiceLogger
	clk      clock.Clock
	runtimes []Runtime
}

func newHandler(cfg *config.Config, log *serviceutil.ServiceLogger, clk clock.Clock, runtimes []Runtime) *Handler {
	return &Handler{cfg: cfg, log: log, clk: clk, runtimes: runtimes}
}
