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
//
// Case folding (serviceutil.FoldHostname) then added one full pass over the
// hostname, since any byte could be upper-case and only reaching the end proves
// none is. Cost measured on the same machine, median of 3, before -> after:
//
//	PathStyleLocalhost      29.6 -> 34.1 ns/op   (+4.5)
//	IPLiteral               15.0 -> 20.1 ns/op   (+5.1)
//	S3BareVirtualHost       39.8 -> 54.7 ns/op   (+14.9)
//	S3LabelledVirtualHost   19.6 -> 36.4 ns/op   (+16.8)
//	HostRouteExecuteAPI      243 ->  268 ns/op   (+25)
//
// About 4 ns of the S3 rows is the fold living in serviceutil rather than this
// package, so it no longer inlines. That is deliberate: minting needs the
// identical rule on the way out (serviceutil.RequestBaseURL), and two copies of
// a case rule is precisely how the original defect arose — the base was folded,
// everything else was not. One implementation, 4 ns.
//
// Still 0 allocs/op on every row, and every row remains far inside the
// pre-consolidation budget above. S3LabelledVirtualHost moves most in relative
// terms because tier A used to return as soon as it found ".s3." near the front,
// so it never touched the rest of the hostname; it now pays the full scan first.
//
// A word-at-a-time (SWAR) scan was implemented and measured against this, then
// dropped. End-to-end through Classify it was better on three rows
// (S3BareVirtualHost 50.2 -> 44.3, S3LabelledVirtualHost 32.7 -> 27.2,
// HostRouteExecuteAPI 259 -> 248) and worse on PathStyleLocalhost, 33.6 -> 35.7,
// which is the row that dominates real traffic — the word loop does not pay for
// its setup on a 9-byte hostname. In isolation it looked like a clear win at
// every length, but those micro-benchmarks varied by ~2x run to run, so the
// honest reading is that the difference is at or below the noise floor. That
// makes it a choice about simplicity, and the byte loop wins: a bit-trick in the
// code that decides which service answers a request needs a differential test
// and a fuzz target before anyone can trust it. Revisit only with a profile
// showing host classification on a real critical path.

var benchHosts = []struct{ name, host string }{
	{"PathStyleLocalhost", "localhost:4566"},
	{"IPLiteral", "127.0.0.1:4566"},
	{"S3BareVirtualHost", "mybucket.localhost:4566"},
	{"S3LabelledVirtualHost", "mybucket.s3.us-east-1.localhost:4566"},
	{"HostRouteExecuteAPI", "abc123.execute-api.us-east-1.localhost.overcast.sh:4566"},
}

// BenchmarkHostClassifier_ClassifyMixedCase measures the one path that DOES
// allocate: a host carrying upper-case bytes has to be materialised in its
// folded form. Kept separate from benchHosts so the 0-allocs/op contract above
// stays a flat rule with no exceptions, and so the cost of the case that real
// clients do not send is visible rather than averaged in.
func BenchmarkHostClassifier_ClassifyMixedCase(b *testing.B) {
	c := NewHostClassifier("localhost.overcast.sh")
	const host = "ABC123.Execute-Api.US-East-1.localhost.overcast.sh:4566"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = c.Classify(host)
	}
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
