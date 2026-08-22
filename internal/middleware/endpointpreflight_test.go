package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/Neaox/overcast/internal/config"
)

func serveThrough(mw func(http.Handler) http.Handler, host string) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = host
	mw(next).ServeHTTP(httptest.NewRecorder(), req)
}

func TestWarnRealAWSHost_firesOnRealAWSHostname(t *testing.T) {
	tests := []string{
		"s3.amazonaws.com",
		"sqs.us-east-1.amazonaws.com",
		"my-bucket.s3.amazonaws.com:443",
		"lambda.ap-southeast-2.on.aws",
	}
	for _, host := range tests {
		t.Run(host, func(t *testing.T) {
			core, logs := observer.New(zap.WarnLevel)
			logger := zap.New(core)
			mw := WarnRealAWSHost(&config.Config{Port: 4566}, logger)

			serveThrough(mw, host)

			if logs.Len() != 1 {
				t.Fatalf("expected exactly one Warn, got %d: %v", logs.Len(), logs.All())
			}
			entry := logs.All()[0]
			if got := entry.ContextMap()["host"]; got == "" {
				t.Errorf("expected the host field to be set, entry = %+v", entry)
			}
		})
	}
}

func TestWarnRealAWSHost_silentOnOrdinaryHosts(t *testing.T) {
	tests := []string{
		"localhost:4566",
		"localhost",
		"localhost.overcast.sh:4566",
		"mybucket.s3.localhost:4566",
		"127.0.0.1:4566",
		"overcast:4566", // a compose service name
	}
	for _, host := range tests {
		t.Run(host, func(t *testing.T) {
			core, logs := observer.New(zap.WarnLevel)
			logger := zap.New(core)
			mw := WarnRealAWSHost(&config.Config{Port: 4566}, logger)

			serveThrough(mw, host)

			if logs.Len() != 0 {
				t.Errorf("expected no warning for ordinary host %q, got %v", host, logs.All())
			}
		})
	}
}

func TestWarnRealAWSHost_firesOnlyOncePerMiddlewareInstance(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	logger := zap.New(core)
	mw := WarnRealAWSHost(&config.Config{Port: 4566}, logger)

	serveThrough(mw, "s3.amazonaws.com")
	serveThrough(mw, "s3.amazonaws.com")
	serveThrough(mw, "sqs.us-east-1.amazonaws.com")

	if logs.Len() != 1 {
		t.Errorf("expected exactly one Warn across repeated requests, got %d: %v", logs.Len(), logs.All())
	}
}

func TestWarnRealAWSHost_nilLoggerIsNoop(t *testing.T) {
	mw := WarnRealAWSHost(&config.Config{Port: 4566}, nil)
	// Must not panic.
	serveThrough(mw, "s3.amazonaws.com")
}

func TestWarnRealAWSHost_requestStillReachesTheHandler(t *testing.T) {
	core, _ := observer.New(zap.WarnLevel)
	logger := zap.New(core)
	mw := WarnRealAWSHost(&config.Config{Port: 4566}, logger)

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "s3.amazonaws.com"
	mw(next).ServeHTTP(httptest.NewRecorder(), req)

	if !called {
		t.Error("expected the wrapped handler to still be called — this is a hint, not an enforcement point")
	}
}

func TestIsRealAWSHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"s3.amazonaws.com", true},
		{"sqs.us-east-1.amazonaws.com", true},
		{"lambda.ap-southeast-2.on.aws", true},
		{"something.aws", true},
		{"localhost", false},
		{"localhost.overcast.sh", false},
		{"127.0.0.1", false},
		{"", false},
		{"amazonaws.com.evil.example", false}, // suffix match only, not substring
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			if got := IsRealAWSHost(tt.host); got != tt.want {
				t.Errorf("IsRealAWSHost(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}
