package pipes

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

// stubInvoker stands in for the Lambda service. Enrichment is a synchronous
// invoke, which a test cannot drive for real without a container runtime.
type stubInvoker struct {
	gotName    string
	gotPayload []byte
	outcome    *events.InvokeOutcome
	err        error
}

func (s *stubInvoker) Invoke(_ context.Context, name string, payload []byte) (*events.InvokeOutcome, error) {
	s.gotName = name
	s.gotPayload = append([]byte(nil), payload...)
	return s.outcome, s.err
}

func newEnrichmentHandler(t *testing.T, invoker events.FunctionSyncInvoker) (*Handler, *Pipe) {
	t.Helper()
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	log := serviceutil.NewServiceLogger(zap.NewNop(), serviceName)
	h := newHandler(cfg, state.NewMemoryStore(), log, clock.NewMock())
	h.enricher = invoker
	p := &Pipe{
		Name:       "orders",
		Arn:        "arn:aws:pipes:us-east-1:000000000000:pipe/orders",
		SourceArn:  "arn:aws:sqs:us-east-1:000000000000:src",
		Enrichment: "arn:aws:lambda:us-east-1:000000000000:function:enricher",
		TargetArn:  "arn:aws:sqs:us-east-1:000000000000:dst",
	}
	return h, p
}

func TestEnrich_returnValueReplacesTheBatch(t *testing.T) {
	// Given: an enrichment that returns a different batch
	invoker := &stubInvoker{outcome: &events.InvokeOutcome{Payload: []byte(`[{"enriched":true}]`)}}
	h, p := newEnrichmentHandler(t, invoker)

	// When: a batch is enriched
	out, drop, err := h.enrich(context.Background(), p, []byte(`[{"body":"raw"}]`))
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}

	// Then: what the function returned is what the target will receive
	if drop {
		t.Fatal("batch dropped, want it delivered")
	}
	if string(out) != `[{"enriched":true}]` {
		t.Errorf("batch = %s, want the enrichment's return value", out)
	}
	if invoker.gotName != "enricher" {
		t.Errorf("invoked %q, want the enrichment function name", invoker.gotName)
	}
	if string(invoker.gotPayload) != `[{"body":"raw"}]` {
		t.Errorf("enrichment received %s, want the source batch", invoker.gotPayload)
	}
}

func TestEnrich_emptyResponseDropsTheBatch(t *testing.T) {
	// AWS: "If you don't want to invoke the target, return an empty response,
	// such as "", {}, or []."
	for _, response := range []string{"", `""`, "{}", "[]", "null", "   "} {
		invoker := &stubInvoker{outcome: &events.InvokeOutcome{Payload: []byte(response)}}
		h, p := newEnrichmentHandler(t, invoker)

		out, drop, err := h.enrich(context.Background(), p, []byte(`[{"body":"raw"}]`))
		if err != nil {
			t.Fatalf("enrich(%q): %v", response, err)
		}
		if !drop {
			t.Errorf("enrich(%q) delivered %s, want the batch dropped", response, out)
		}
	}

	// But an array holding an empty object is an explicit "invoke with nothing".
	invoker := &stubInvoker{outcome: &events.InvokeOutcome{Payload: []byte(`[{}]`)}}
	h, p := newEnrichmentHandler(t, invoker)
	_, drop, err := h.enrich(context.Background(), p, []byte(`[{"body":"raw"}]`))
	if err != nil {
		t.Fatalf("enrich([{}]): %v", err)
	}
	if drop {
		t.Error("enrich([{}]) dropped the batch, want the target invoked with it")
	}
}

func TestEnrich_failureIsADeliveryFailure(t *testing.T) {
	// Given: an enrichment whose handler raised
	invoker := &stubInvoker{outcome: &events.InvokeOutcome{
		Payload:       []byte(`{"errorMessage":"boom"}`),
		FunctionError: "Unhandled",
	}}
	h, p := newEnrichmentHandler(t, invoker)

	// When/Then: the batch is not silently forwarded unenriched
	if _, _, err := h.enrich(context.Background(), p, []byte(`[{}]`)); err == nil {
		t.Fatal("expected a function error to fail the enrichment")
	}

	// And an infrastructure failure fails it too.
	h2, p2 := newEnrichmentHandler(t, &stubInvoker{err: errors.New("no such function")})
	if _, _, err := h2.enrich(context.Background(), p2, []byte(`[{}]`)); err == nil {
		t.Fatal("expected an invoke error to fail the enrichment")
	}
}

func TestEnrich_withoutAnEnrichmentIsAPassThrough(t *testing.T) {
	h, p := newEnrichmentHandler(t, &stubInvoker{})
	p.Enrichment = ""

	out, drop, err := h.enrich(context.Background(), p, []byte(`[{"a":1}]`))
	if err != nil || drop {
		t.Fatalf("enrich with no enrichment configured: err=%v drop=%v", err, drop)
	}
	if string(out) != `[{"a":1}]` {
		t.Errorf("batch = %s, want it untouched", out)
	}
}
