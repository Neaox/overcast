package cloudwatch

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/state"
)

// newTestService returns a cloudwatch Service wired for tests, with cleanup
// registered so its background goroutines (alarm evaluator, metric sweeper)
// stop cleanly.
func newTestService(t *testing.T) *Service {
	t.Helper()
	mock := clock.NewMock()
	store := state.NewMemoryStore()
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	svc := New(cfg, store, zap.NewNop(), mock)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		svc.Stop(ctx)
	})
	return svc
}

// TestDispatch_UsesCodecContext_NotBespokeHeaderParsing pins the P1 Track
// 1.4 migration: Dispatch must resolve the operation name from
// codec.FromContext (populated by middleware.Protocol, exactly like every
// other JSON-tier service) rather than re-deriving it via its own
// GraniteServiceVersion20100801. prefix parsing of the raw X-Amz-Target
// header.
//
// The request here deliberately carries a garbage/absent X-Amz-Target
// header — if Dispatch is still doing its own bespoke parsing, it will
// fail to find a valid target and return UnknownOperationException. Once
// Dispatch trusts the codec-resolved context instead, the request must
// succeed regardless of what the raw header says.
func TestDispatch_UsesCodecContext_NotBespokeHeaderParsing(t *testing.T) {
	svc := newTestService(t)

	body := []byte(`{"Namespace":"AWS/EC2"}`)
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/x-amz-json-1.0")
	// Intentionally NOT set: X-Amz-Target. A bespoke-header-parsing
	// Dispatch has nothing to trim and fails; a context-driven Dispatch
	// doesn't care, because middleware.Protocol already resolved the op.
	ctx := codec.WithDispatch(r.Context(), codec.JSON10, "ListMetrics")
	r = r.WithContext(ctx)

	w := httptest.NewRecorder()
	svc.Dispatch(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("Dispatch: status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
}

// TestDispatch_FallsBackToHeaderParsing_WhenNoCodecContext preserves the
// defensive fallback for callers that invoke Dispatch directly without
// going through middleware.Protocol (e.g. hypothetical direct unit
// tests) — mirrors the same convention used by every other
// TargetDispatcher service (see kinesis.Service.Dispatch).
func TestDispatch_FallsBackToHeaderParsing_WhenNoCodecContext(t *testing.T) {
	svc := newTestService(t)

	body := []byte(`{"Namespace":"AWS/EC2"}`)
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/x-amz-json-1.0")
	r.Header.Set("X-Amz-Target", "GraniteServiceVersion20100801.ListMetrics")

	w := httptest.NewRecorder()
	svc.Dispatch(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("Dispatch: status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
}

// TestDispatch_UnknownTarget_NoCodecContext_NoHeader pins the error path:
// with neither a resolved codec context nor a parseable header, Dispatch
// must still return the AWS-shaped UnknownOperationException rather than
// panicking or falling through silently.
func TestDispatch_UnknownTarget_NoCodecContext_NoHeader(t *testing.T) {
	svc := newTestService(t)

	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(nil))
	r.Header.Set("Content-Type", "application/x-amz-json-1.0")
	// No X-Amz-Target, no codec context.

	w := httptest.NewRecorder()
	svc.Dispatch(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Dispatch: status = %d, want 400; body = %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("UnknownOperationException")) {
		t.Fatalf("expected UnknownOperationException in body, got: %s", w.Body.String())
	}
}
