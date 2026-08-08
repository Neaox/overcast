package trace

import (
	"net/http"
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
