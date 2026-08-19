package events

import (
	"context"
	"strconv"
	"testing"

	"github.com/Neaox/overcast/internal/protocol"
)

// BenchmarkBus_Publish measures the publish path with no subscribers — the
// common case on an idle server, and the one every service handler pays
// whether or not anyone is watching the event stream.
//
// The axis that matters here is context depth. Publish reads the request ID
// with a context.Value lookup, and context.Value walks the chain link by link
// until it finds the key or reaches the root; an absent key is the worst case
// because it walks the whole thing. A real request context is several links
// deep by the time a handler publishes — request ID, region, trace recorder,
// body capture, the server's own cancellation — so benchmarking at depth 0
// would measure a lookup that never happens in production and report the
// feature as free.
//
// "no request id" is not a control here, it is the shape background publishes
// take: a poller's context has no ID, and it walks to the root to find that
// out.
func BenchmarkBus_Publish(b *testing.B) {
	for _, depth := range []int{0, 4, 8} {
		payload := ResourcePayload{Name: "q", ARN: "arn:aws:sqs:us-east-1:000000000000:q"}

		b.Run("depth="+strconv.Itoa(depth)+"/with-request-id", func(b *testing.B) {
			// The ID goes on first, so the padding sits between it and the
			// leaf — the lookup walks past all of it, as it does in a handler.
			ctx := padContext(protocol.ContextWithRequestID(context.Background(), "bench-request-id"), depth)
			benchPublish(b, ctx, payload)
		})

		b.Run("depth="+strconv.Itoa(depth)+"/no-request-id", func(b *testing.B) {
			benchPublish(b, padContext(context.Background(), depth), payload)
		})
	}
}

func benchPublish(b *testing.B, ctx context.Context, payload any) {
	b.Helper()
	bus := NewBus()
	defer bus.Stop()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bus.Publish(ctx, Event{Type: SQSQueueCreated, Source: "sqs", Payload: payload})
	}
}

// padKey is a distinct context key type per link, so the padding is a chain of
// real links rather than one value shadowing another.
type padKey int

// padContext wraps ctx in n nested value links.
func padContext(ctx context.Context, n int) context.Context {
	for i := 0; i < n; i++ {
		ctx = context.WithValue(ctx, padKey(i), i)
	}
	return ctx
}
