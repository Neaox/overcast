package trace

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

// BenchmarkRecorderAddHop_deploy models the hop volume of a CloudFormation /
// CDK deploy: one trace that dispatches many internal service calls. Each
// iteration builds a fresh Recorder so the per-trace stack budget is exercised
// exactly as it is in production.
func BenchmarkRecorderAddHop_deploy(b *testing.B) {
	const hops = 200
	reqBody := []byte(`{"QueueName":"cdk-queue","Attributes":{"VisibilityTimeout":"30"}}`)
	respBody := []byte(`{"QueueUrl":"http://localhost:4566/000000000000/cdk-queue"}`)
	hdr := http.Header{"Content-Type": []string{"application/x-amz-json-1.0"}}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := NewRecorder("req-1", time.Unix(0, 0), http.MethodPost, "/", "localhost:4566", "", hdr)
		for h := 0; h < hops; h++ {
			rec.AddHop(Hop{
				CallerService:  "cloudformation",
				Service:        "sqs",
				Operation:      "CreateQueue",
				TargetURI:      "POST / (X-Amz-Target: AmazonSQS.CreateQueue)",
				RequestBody:    reqBody,
				ResponseStatus: 200,
				ResponseBody:   respBody,
				Duration:       time.Millisecond,
			})
		}
	}
}

// BenchmarkRecorderAddHop_deployIndexed is the same deploy with each hop
// carrying the request ID of the call it dispatched — what the internal
// dispatch actually records. Against BenchmarkRecorderAddHop_deploy it isolates
// the cost of the hop request-ID index that makes the HopsFor filter O(1).
func BenchmarkRecorderAddHop_deployIndexed(b *testing.B) {
	const hops = 200
	reqBody := []byte(`{"QueueName":"cdk-queue","Attributes":{"VisibilityTimeout":"30"}}`)
	respBody := []byte(`{"QueueUrl":"http://localhost:4566/000000000000/cdk-queue"}`)
	hdr := http.Header{"Content-Type": []string{"application/x-amz-json-1.0"}}
	hopIDs := make([]string, hops)
	for i := range hopIDs {
		hopIDs[i] = "hop-req-" + strconv.Itoa(i)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := NewRecorder("req-1", time.Unix(0, 0), http.MethodPost, "/", "localhost:4566", "", hdr)
		for h := 0; h < hops; h++ {
			rec.AddHop(Hop{
				CallerService:  "cloudformation",
				Service:        "sqs",
				Operation:      "CreateQueue",
				RequestID:      hopIDs[h],
				TargetURI:      "POST / (X-Amz-Target: AmazonSQS.CreateQueue)",
				RequestBody:    reqBody,
				ResponseStatus: 200,
				ResponseBody:   respBody,
				Duration:       time.Millisecond,
			})
		}
	}
}

// BenchmarkBufferListSummaries covers the read path the web UI polls once a
// second: a full ring buffer, listed and filtered. It must stay cheap now that
// listing reads live recorders rather than immutable snapshots.
func BenchmarkBufferListSummaries(b *testing.B) {
	const capacity = 1000
	hdr := http.Header{"Content-Type": []string{"application/x-amz-json-1.0"}}
	buf := NewBuffer(capacity)
	base := time.Unix(0, 0)
	for i := 0; i < capacity; i++ {
		rec := NewRecorder("req-"+strconv.Itoa(i), base.Add(time.Duration(i)*time.Millisecond),
			http.MethodPost, "/", "localhost:4566", "", hdr)
		rec.SetServiceInfo("sqs", "CreateQueue", "us-east-1")
		rec.SetResponse(http.Header{}, []byte(`{"ok":true}`), 200+i%300, 1024, false)
		for h := 0; h < 20; h++ {
			rec.AddHop(Hop{Service: "sqs", RequestID: "hop-" + strconv.Itoa(i) + "-" + strconv.Itoa(h), ResponseStatus: 200})
		}
		buf.Add(rec)
	}

	benchmarks := []struct {
		name   string
		filter ListFilter
	}{
		{"unfiltered", ListFilter{Limit: 50}},
		{"errors", ListFilter{Statuses: []string{"4xx", "5xx"}, Limit: 50}},
		{"methods", ListFilter{Methods: []string{"GET", "POST"}, Limit: 50}},
		{"hopsFor", ListFilter{HopsFor: "hop-999-19", Limit: 50}},
	}
	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf.ListSummaries(bm.filter)
			}
		})
	}
}
