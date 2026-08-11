package cloudformation

// Emulation limitations become a resource's status reason.
//
// A resource handler provisions by dispatching to the emulated service through
// the router, and a service that creates something Overcast will not fully act
// on marks its response with protocol.EmulationLimitationHeader. This is where
// that mark becomes ResourceStatusReason, so `cdk deploy` and
// describe-stack-resources show it beside the resource it applies to.
//
// It is collected from the context rather than returned by the handler, because
// every handler already dispatches through one function and none of them should
// have to know this exists. A service opting in writes one header; no handler
// signature moves, and no handler has to remember to pass it along.

import (
	"context"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"

	"github.com/Neaox/overcast/internal/protocol"
)

// limitationCollectorKey is the context key for the collector a resource's
// provisioning runs under.
type limitationCollectorKey struct{}

// limitationCollector gathers what the services reported while one resource was
// provisioned. A handler may dispatch several calls — a create, then a tag,
// then a policy — and any of them may report something, so they accumulate.
// Mutex-guarded because a handler is free to dispatch concurrently.
type limitationCollector struct {
	mu       sync.Mutex
	messages []string
}

// withLimitationCollector returns a context that gathers emulation limitations,
// and the collector to read them from once provisioning returns.
func withLimitationCollector(ctx context.Context) (context.Context, *limitationCollector) {
	collector := &limitationCollector{}
	return context.WithValue(ctx, limitationCollectorKey{}, collector), collector
}

// noteLimitations records whatever one dispatched response reported, if it
// reported anything and if this context is collecting.
func noteLimitations(ctx context.Context, rec *httptest.ResponseRecorder) {
	if rec == nil {
		return
	}
	messages := protocol.Limitations(rec.Header())
	if len(messages) == 0 {
		return
	}
	collector, ok := ctx.Value(limitationCollectorKey{}).(*limitationCollector)
	if !ok || collector == nil {
		return
	}
	collector.add(messages...)
}

func (c *limitationCollector) add(messages ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, message := range messages {
		// A handler that dispatches several calls to the same service gets the
		// same sentence back from each; the reason should say it once.
		if !slices.Contains(c.messages, message) {
			c.messages = append(c.messages, message)
		}
	}
}

// reason joins what was collected into the resource's status reason, or returns
// empty for a resource with nothing to declare.
func (c *limitationCollector) reason() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.messages, " ")
}
