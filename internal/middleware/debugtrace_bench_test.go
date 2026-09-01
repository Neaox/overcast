package middleware_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/middleware"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/trace"
)

func BenchmarkMiddlewareOverhead(b *testing.B) {
	runBench := func(b *testing.B, debug bool) {
		cfg := &config.Config{Debug: debug, Region: "us-east-1", LogLevel: "warn"}
		buf := trace.NewBuffer(1000)
		logger := zap.NewNop()

		r := chi.NewRouter()
		r.Use(middleware.RequestID)
		r.Use(middleware.DebugTrace(cfg, buf, clock.New()))
		r.Use(middleware.Logger(logger, clock.New()))
		r.Post("/", func(w http.ResponseWriter, r *http.Request) {
			_ = protocol.RequestIDFromContext(r.Context())
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"result":"ok"}`))
		})

		srv := httptest.NewServer(r)
		defer srv.Close()

		latencies := make([]time.Duration, b.N)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			body := fmt.Sprintf(`Action=SendMessage&MessageBody=test-%d`, i)
			req, _ := http.NewRequest("POST", srv.URL, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("X-Amz-Target", "AmazonSQS.SendMessage")

			start := time.Now()
			resp, err := http.DefaultClient.Do(req)
			latencies[i] = time.Since(start)
			if err != nil {
				b.Fatal(err)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}

		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		p50 := latencies[b.N/2]
		idx99 := int(float64(b.N) * 0.99)
		if idx99 >= b.N {
			idx99 = b.N - 1
		}
		p99 := latencies[idx99]
		avg := time.Duration(0)
		for _, l := range latencies {
			avg += l
		}
		avg /= time.Duration(b.N)
		b.ReportMetric(float64(p50.Microseconds()), "p50-us")
		b.ReportMetric(float64(p99.Microseconds()), "p99-us")
		b.ReportMetric(float64(avg.Microseconds()), "avg-us")
	}

	b.Run("debug-off", func(b *testing.B) { runBench(b, false) })
	b.Run("debug-on", func(b *testing.B) { runBench(b, true) })
}
