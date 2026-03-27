package sns

import (
	"net/http"

	"github.com/your-org/overcast/internal/clock"
	"github.com/your-org/overcast/internal/config"
	"github.com/your-org/overcast/internal/serviceutil"
	"github.com/your-org/overcast/internal/state"
)

// Handler holds SNS handler dependencies.
type Handler struct {
	cfg   *config.Config
	store state.Store
	log   *serviceutil.ServiceLogger
	clk   clock.Clock
	ops   map[string]http.HandlerFunc
}

// newHandler constructs a Handler from the raw dependencies.
func newHandler(cfg *config.Config, store state.Store, log *serviceutil.ServiceLogger, clk clock.Clock) *Handler {
	h := &Handler{cfg: cfg, store: store, log: log, clk: clk}
	h.initOps()
	return h
}

// initOps registers every known SNS operation to its handler.
// Implemented operations point to their handler method; stubs live in handler_stubs.go.
// Adding a new operation: add an entry here, implement in handler.go, delete from handler_stubs.go.
func (h *Handler) initOps() {
	h.ops = map[string]http.HandlerFunc{
		// TODO(priority:P1): implement topic lifecycle
		"CreateTopic": h.CreateTopic,
		"DeleteTopic": h.DeleteTopic,
		"ListTopics":  h.ListTopics,
		// TODO(priority:P1): implement subscriptions
		"Subscribe":                h.Subscribe,
		"Unsubscribe":              h.Unsubscribe,
		"ListSubscriptionsByTopic": h.ListSubscriptionsByTopic,
		// TODO(priority:P1): implement publish
		"Publish":      h.Publish,
		"PublishBatch": h.PublishBatch,
		// TODO(priority:P2): implement topic attributes
		"GetTopicAttributes": h.GetTopicAttributes,
		"SetTopicAttributes": h.SetTopicAttributes,
	}
}
