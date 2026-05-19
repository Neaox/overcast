package sns

import (
	"context"
	"net/http"
	"sync"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/smtp"
	"github.com/Neaox/overcast/internal/state"
)

// Handler holds SNS handler dependencies.
type Handler struct {
	cfg       *config.Config
	snsStore  *snsStore
	log       *serviceutil.ServiceLogger
	clk       clock.Clock
	enqueuer  events.MessageEnqueuer
	mailer    smtp.Mailer
	smsSender smtp.SMSSender
	outbound  smtp.OutboundCapture
	bus       *events.Bus
	ops       map[string]http.HandlerFunc
	typedOp   map[string]op.Operation
	wg        sync.WaitGroup
}

// newHandler constructs a Handler from the raw dependencies.
func newHandler(cfg *config.Config, store state.Store, log *serviceutil.ServiceLogger, clk clock.Clock) *Handler {
	h := &Handler{
		cfg:      cfg,
		snsStore: newSNSStore(store, clk, cfg.Region),
		log:      log,
		clk:      clk,
	}
	h.initOps()
	return h
}

// Stop waits for all in-flight fan-out goroutines to finish, or until ctx is
// cancelled. If the context deadline is reached before delivery completes, a
// warning is logged and Stop returns so shutdown is not blocked indefinitely.
func (h *Handler) Stop(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		h.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		h.log.Logger().Warn("SNS: timed out waiting for in-flight deliveries to complete",
			zap.Error(ctx.Err()))
	}
}

// initOps registers every known SNS operation to its handler.
// Implemented operations point to their handler method; stubs live in handler_stubs.go.
// Adding a new operation: add an entry here, implement in handler.go, delete from handler_stubs.go.
func (h *Handler) initOps() {
	h.ops = map[string]http.HandlerFunc{
		// Topic lifecycle — handler_topic.go
		"CreateTopic":        h.CreateTopic,
		"DeleteTopic":        h.DeleteTopic,
		"ListTopics":         h.ListTopics,
		"GetTopicAttributes": h.GetTopicAttributes,
		"SetTopicAttributes": h.SetTopicAttributes,
		// Subscriptions — handler_subscription.go
		"Subscribe":                 h.Subscribe,
		"Unsubscribe":               h.Unsubscribe,
		"ListSubscriptionsByTopic":  h.ListSubscriptionsByTopic,
		"ListSubscriptions":         h.ListSubscriptions,
		"GetSubscriptionAttributes": h.GetSubscriptionAttributes,
		"SetSubscriptionAttributes": h.SetSubscriptionAttributes,
		"ConfirmSubscription":       h.ConfirmSubscription,
		// Publish — handler_publish.go
		"Publish":      h.Publish,
		"PublishBatch": h.PublishBatch,
	}
	h.typedOp = h.typedOps()
}

// dispatch routes to the correct SNS handler based on the Query-protocol Action field.
// r.ParseForm() is called by the router before this method is invoked.
func (h *Handler) dispatch(w http.ResponseWriter, r *http.Request) {
	action := r.FormValue("Action")
	if fn, ok := h.ops[action]; ok {
		fn(w, r)
		return
	}
	protocol.WriteQueryXMLError(w, r, &protocol.AWSError{
		Code:       "InvalidAction",
		Message:    "The action " + action + " is not valid for this web service.",
		HTTPStatus: http.StatusBadRequest,
	})
}

// OwnsAction implements router.QueryActionOwner.
// It returns true for any Action value that this SNS handler recognises.
func (h *Handler) OwnsAction(action string) bool {
	_, ok := h.ops[action]
	return ok
}

// requireForm returns a form value or writes a QueryXML error and returns ("", false).
func (h *Handler) requireForm(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	v := r.FormValue(name)
	if v == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter(name))
		return "", false
	}
	return v, true
}
