package middleware_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/middleware"
)

// BenchmarkLoggerRequestBody measures the Logger middleware's per-request cost
// with debug off — the hot path every AWS call takes. The interesting axis is
// the request body: a middleware that buffers it unconditionally pays for
// bodies no handler asked for, and pays proportionally to upload size.
func BenchmarkLoggerRequestBody(b *testing.B) {
	logger := zap.NewNop()

	run := func(b *testing.B, method string, bodySize int, handlerReads bool) {
		h := middleware.Logger(logger, clock.New())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if handlerReads {
				_, _ = io.Copy(io.Discard, r.Body)
			}
			w.WriteHeader(http.StatusOK)
		}))
		payload := bytes.Repeat([]byte("a"), bodySize)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			var req *http.Request
			if bodySize == 0 {
				req = httptest.NewRequest(method, "/", nil)
			} else {
				req = httptest.NewRequest(method, "/", bytes.NewReader(payload))
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
		}
	}

	// A bodyless request — the shape of every List/Describe/Get call.
	b.Run("no-body", func(b *testing.B) { run(b, http.MethodGet, 0, false) })
	// A typical AWS JSON/Query API call whose handler parses the body.
	b.Run("1KiB-handler-reads", func(b *testing.B) { run(b, http.MethodPost, 1<<10, true) })
	// An S3-style upload the handler streams straight through.
	b.Run("1MiB-handler-reads", func(b *testing.B) { run(b, http.MethodPut, 1<<20, true) })
	// An upload rejected before the handler touches the body (auth/validation
	// failure): nothing should be buffered on this path at all.
	b.Run("1MiB-handler-ignores", func(b *testing.B) { run(b, http.MethodPut, 1<<20, false) })
}
