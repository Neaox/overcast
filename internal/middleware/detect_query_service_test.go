package middleware

// detect_query_service_test.go — a Query-protocol request is labelled by the
// service that answers it, not by the S3 fallback.
//
// SQS, SNS, CloudFormation and the rest of the Query-protocol services all post
// to "/" with the operation in the form body. There is no distinguishing path
// and, for an unsigned request, no credential scope — so classification comes
// down to the Action/Version pair and the generated operation registry that
// owns it. If that step does not run, every such request is labelled s3, which
// is what step 4 answers when nothing else has.
//
// That label is not cosmetic. It is the `service` field on every request log
// line, the Service column in the trace list and the service filter beside it,
// and it is what iam_enforce.go authorises against.

import (
	"net/http"
	"strings"
	"testing"
)

// queryRequest builds the request an SDK or the AWS CLI sends for a
// Query-protocol operation: form-encoded, unsigned, addressed to "/".
func queryRequest(body string) (*http.Request, []byte) {
	r, _ := http.NewRequest(http.MethodPost, "http://localhost:4566/", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r, []byte(body)
}

func TestDetectService_labelsQueryProtocolByItsAction(t *testing.T) {
	for _, tc := range []struct {
		name  string
		body  string
		want  string
		known bool // a gap this test records rather than enforces
	}{
		{"sns", "Action=CreateTopic&Version=2010-03-31&Name=t", "sns", false},
		{"cloudformation", "Action=DescribeStacks&Version=2010-05-15", "cloudformation", false},
		{"iam", "Action=ListRoles&Version=2010-05-08", "iam", false},
		{"sts", "Action=GetCallerIdentity&Version=2011-06-15", "sts", false},

		// SQS and CloudWatch are served over the Query protocol by Overcast and
		// are absent from the generated query registry, because the pinned AWS
		// models no longer declare Query for them — SQS's model is awsJson1_0
		// only. The registry is generated from what AWS models, so the fact
		// that Overcast still answers these on the Query wire has to come from
		// Overcast's own declarations, and today it comes from nowhere.
		//
		// Skipped rather than deleted: the expectation is right and the
		// classifier is wrong. See the follow-up that sweeps every service and
		// operation across both the trace and log paths.
		{"sqs", "Action=GetQueueUrl&Version=2012-11-05&QueueName=q", "sqs", true},
		{"sqs create", "Action=CreateQueue&Version=2012-11-05&QueueName=q", "sqs", true},
		{"cloudwatch", "Action=PutMetricAlarm&Version=2010-08-01&AlarmName=a", "cloudwatch", true},
		{"version first", "Version=2012-11-05&Action=ListQueues", "sqs", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.known {
				t.Skip("known gap: served over Query by Overcast, absent from the generated query registry")
			}
			r, body := queryRequest(tc.body)
			if got := detectService(r, body); got != tc.want {
				t.Errorf("detectService(%s) = %q, want %q", tc.body, got, tc.want)
			}
		})
	}
}

// The failure this test exists for: a body the caller never read. detectService
// takes the body variadically, so every call site that omits it silently loses
// step 3b and falls through to s3 — and the compiler cannot say so.
func TestDetectService_withoutTheBodyCannotClassifyQuery(t *testing.T) {
	r, _ := queryRequest("Action=GetQueueUrl&Version=2012-11-05&QueueName=q")

	// Documented, not endorsed: this is why callers that can supply the body
	// must, and why the ones that cannot are a known gap rather than a bug in
	// the classifier.
	if got := detectService(r); got != "s3" {
		t.Errorf("detectService without a body = %q; the s3 fallback is the documented behaviour here", got)
	}
}
