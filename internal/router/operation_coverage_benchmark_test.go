package router

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

func BenchmarkRouterConstruction(b *testing.B) {
	cfg, err := config.Load()
	if err != nil {
		b.Fatal(err)
	}
	cfg.DataDir = b.TempDir()
	cfg.LambdaDockerSocket = ""
	logger := zap.NewNop()

	b.ReportAllocs()
	for b.Loop() {
		handler, _, cleanup, waitReady := New(cfg, state.NewMemoryStore(), logger, clock.New(), nil)
		waitReady()
		cleanup(context.Background())
		runtime.KeepAlive(handler)
	}
}

func BenchmarkOperationCoverageRoutes(b *testing.B) {
	cfg, err := config.Load()
	if err != nil {
		b.Fatal(err)
	}
	cfg.DataDir = b.TempDir()
	cfg.LambdaDockerSocket = ""
	handler, _, cleanup, waitReady := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New(), nil)
	waitReady()
	b.Cleanup(func() { cleanup(context.Background()) })

	benchmarks := []struct {
		name    string
		request func() *http.Request
	}{
		{
			name: "S3ListBuckets",
			request: func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "/", nil)
			},
		},
		{
			name: "JSONTargetFallback",
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("{}"))
				req.Header.Set("Content-Type", "application/x-amz-json-1.1")
				req.Header.Set("X-Amz-Target", "GameLift.ListBuilds")
				return req
			},
		},
		{
			name: "QueryFallback",
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("Action=AssumeRoot&Version=2011-06-15"))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				return req
			},
		},
		{
			name: "RESTFallback",
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "/analyzer", nil)
				req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260728/us-east-1/access-analyzer/aws4_request, SignedHeaders=host, Signature=test")
				return req
			},
		},
	}

	for _, benchmark := range benchmarks {
		b.Run(benchmark.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, benchmark.request())
			}
		})
	}
}
