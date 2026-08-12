package trace

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

// fillBuffer builds a buffer of n user traces, each carrying hops, shaped like
// what the list page actually reads.
func fillBuffer(n int) *Buffer {
	hdr := http.Header{"Content-Type": []string{"application/x-amz-json-1.0"}}
	buf := NewBuffer(n)
	base := time.Unix(0, 0)
	for i := 0; i < n; i++ {
		rec := NewRecorder("req-"+strconv.Itoa(i), base.Add(time.Duration(i)*time.Millisecond),
			http.MethodPost, "/", "localhost:4566", "", hdr)
		rec.SetServiceInfo("sqs", "CreateQueue", "us-east-1")
		rec.SetResponse(http.Header{}, []byte(`{"ok":true}`), 200+i%300, 1024, false)
		for h := 0; h < 20; h++ {
			rec.AddHop(Hop{Service: "sqs", RequestID: "hop-" + strconv.Itoa(i) + "-" + strconv.Itoa(h), ResponseStatus: 200})
		}
		buf.Add(rec)
	}
	return buf
}

// BenchmarkBufferListSummaries_depth is the number the retention ceiling rests
// on: one page must cost the same whether the buffer holds a thousand traces or
// ten thousand.
//
// It did not, before the rings were split. Listing materialised and sorted
// every retained trace to return fifty of them, so the per-poll cost — and a
// ring-sized allocation with it — grew with whatever the buffer was holding.
// The walk now stops at the page, so depth should show up as noise rather than
// as a slope. If a later change makes this scale with n again, raising the
// ceiling stops being free and this benchmark is where that shows.
func BenchmarkBufferListSummaries_depth(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		buf := fillBuffer(n)
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf.ListSummaries(ListFilter{Limit: 50})
			}
		})
	}
}

// BenchmarkBufferAdd_internal covers the admission path that used to scan.
//
// An internal trace arriving at a full buffer once walked the ring looking for
// the oldest internal entry to recycle — on every health check, every SSE
// reconnect, every second of an open UI. With its own ring it is a head
// advance, and the cost no longer depends on how much is retained.
//
// **The saturation loop below is the whole benchmark.** That scan only ran once
// the internal population was at its cap, which the old policy set to
// capacity/5 — two thousand entries at a depth of ten thousand. Timing a few
// hundred cold adds measures the admission path and never reaches the scan at
// all, which reads as "no regression" for the exact case that motivated this
// work. Saturate first, then time.
func BenchmarkBufferAdd_internal(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			buf := fillBuffer(n)
			hdr := http.Header{}
			base := time.Unix(0, 0)
			internal := func(i int) *Recorder {
				return NewRecorder("poll-"+strconv.Itoa(i), base.Add(time.Duration(i)*time.Millisecond),
					http.MethodGet, "/_overcast/health", "localhost:4566", "", hdr)
			}
			// Enough to fill the internal population under either policy.
			for i := 0; i < n/5+10; i++ {
				buf.Add(internal(i))
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf.Add(internal(1 << 20 * (i + 1)))
			}
		})
	}
}
