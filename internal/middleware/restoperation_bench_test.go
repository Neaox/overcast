package middleware

import (
	"net/http"
	"testing"
)

// BenchmarkRestOperation measures the method+path lookup every REST-routed AWS
// call pays, on the axis the escaped-path walk moves: whether the request
// carries a RawPath.
//
// The rows are chosen so the added term is visible rather than averaged away.
// EscapedPath re-encodes Path when there is no RawPath and returns it unchanged
// when nothing needs escaping, so an ordinary path must stay at one trie walk
// and no allocation — "plain" and "plain-miss" are what guards that. An
// ARN-labelled path is the case that now does real work: it walks the escaped
// form, and only falls back to the decoded one when that misses.
func BenchmarkRestOperation(b *testing.B) {
	run := func(b *testing.B, svc, method, target string, wantNamed bool) {
		r := signedRequest(method, target, "kafka")
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if got := restOperation(svc, r); (got != "") != wantNamed {
				b.Fatalf("restOperation(%s, %s %s) = %q", svc, method, target, got)
			}
		}
	}

	escaped := escapedARN(testClusterARN)

	// An ordinary modeled path: no RawPath, so one walk and nothing escaped.
	b.Run("plain", func(b *testing.B) {
		run(b, "msk", http.MethodGet, "/v1/clusters", true)
	})
	// The same shape on a path no binding claims — the walk still has to fail
	// before the caller can conclude there is no operation.
	b.Run("plain-miss", func(b *testing.B) {
		run(b, "msk", http.MethodGet, "/v1/no/such/thing", false)
	})
	// An ARN bound to a non-greedy label: named by the escaped walk, which is
	// the match the decoded path could not produce.
	b.Run("arn-label", func(b *testing.B) {
		run(b, "msk", http.MethodGet, "/v1/clusters/"+escaped+"/nodes", true)
	})
	// The worst case the change introduces: a RawPath whose escaped form
	// matches nothing, so both walks run before the answer is "".
	b.Run("arn-label-miss", func(b *testing.B) {
		run(b, "msk", http.MethodGet, "/v1/clusters/"+escaped+"/not-a-subresource", false)
	})
}
