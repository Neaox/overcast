package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// hostaddressing_bench_test.go — permanent regression cover for the per-request
// cost of host classification, which runs on EVERY request.
//
// Baseline before consolidation (median of 3; golang:1.24-bookworm container,
// linux/amd64, Go 1.24.13, AMD Ryzen 9 5900X, GOMAXPROCS=24), measuring the two
// independent passes the middleware chain used to make — extractS3BucketFromHost
// followed by ParseHostRoute:
//
//	PathStyleLocalhost       230 ns/op   176 B/op   4 allocs/op
//	IPLiteral                 62 ns/op     0 B/op   0 allocs/op
//	S3BareVirtualHost        272 ns/op   192 B/op   4 allocs/op
//	S3LabelledVirtualHost    188 ns/op   160 B/op   3 allocs/op
//	HostRouteExecuteAPI      465 ns/op   258 B/op   4 allocs/op
//
// The two avoidable costs were rebuilding and sorting the base list per call
// (extractS3BucketFromHost) and strings.Split on the hostname (ParseHostRoute).
// Classify must be 0 allocs/op on every row and must not regress on ns/op.
// See docs/plans/host-routing-precedence.md §7.

var benchHosts = []struct{ name, host string }{
	{"PathStyleLocalhost", "localhost:4566"},
	{"IPLiteral", "127.0.0.1:4566"},
	{"S3BareVirtualHost", "mybucket.localhost:4566"},
	{"S3LabelledVirtualHost", "mybucket.s3.us-east-1.localhost:4566"},
	{"HostRouteExecuteAPI", "abc123.execute-api.us-east-1.localhost.overcast.sh:4566"},
}

// BenchmarkHostClassifier_Classify measures the consolidated single-pass
// classification with the base list precomputed at construction, which is how
// the middleware uses it.
func BenchmarkHostClassifier_Classify(b *testing.B) {
	c := NewHostClassifier("localhost.overcast.sh")
	for _, h := range benchHosts {
		b.Run(h.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = c.Classify(h.host)
			}
		})
	}
}

// BenchmarkHostAddressing_Middleware measures end-to-end per-request cost
// through the real http.Handler chain, including the context stamp and the
// rewrite, against a no-op terminal handler.
func BenchmarkHostAddressing_Middleware(b *testing.B) {
	rows := []HostRouteRow{
		{Label: "execute-api", Rewrite: func(r *http.Request, m HostRouteMatch) {
			r.URL.Path = "/_apigateway/" + m.ID + r.URL.Path
		}},
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	mw := HostAddressing("localhost.overcast.sh", &rows, nil)(next)

	for _, h := range benchHosts {
		b.Run(h.name, func(b *testing.B) {
			w := httptest.NewRecorder()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				req := httptest.NewRequest(http.MethodGet, "/test/hello", nil)
				req.Host = h.host
				mw.ServeHTTP(w, req)
			}
		})
	}
}

// BenchmarkNewHostClassifier measures construction, which happens once per
// router.New() and therefore counts against the startup budget, not the
// per-request budget. See docs/dev/performance.md.
func BenchmarkNewHostClassifier(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = NewHostClassifier("localhost.overcast.sh")
	}
}
