package protocol_test

import (
	"strings"
	"testing"

	"github.com/your-org/overcast/internal/protocol"
)

func TestARN_s3_omitsRegionAndAccount(t *testing.T) {
	got := protocol.ARN("us-east-1", "123456789012", "s3", "my-bucket")
	want := "arn:aws:s3:::my-bucket"
	if got != want {
		t.Errorf("ARN s3: expected %q, got %q", want, got)
	}
}

func TestARN_sqs(t *testing.T) {
	got := protocol.ARN("us-east-1", "000000000000", "sqs", "my-queue")
	want := "arn:aws:sqs:us-east-1:000000000000:my-queue"
	if got != want {
		t.Errorf("ARN sqs: expected %q, got %q", want, got)
	}
}

func TestQueueARN(t *testing.T) {
	got := protocol.QueueARN("eu-west-1", "123456789012", "my-queue")
	if !strings.Contains(got, "sqs") {
		t.Errorf("QueueARN: expected SQS ARN, got %q", got)
	}
	if !strings.HasSuffix(got, ":my-queue") {
		t.Errorf("QueueARN: expected suffix ':my-queue', got %q", got)
	}
}

func TestTopicARN(t *testing.T) {
	got := protocol.TopicARN("us-east-1", "000000000000", "my-topic")
	want := "arn:aws:sns:us-east-1:000000000000:my-topic"
	if got != want {
		t.Errorf("TopicARN: expected %q, got %q", want, got)
	}
}

func TestLambdaARN(t *testing.T) {
	got := protocol.LambdaARN("us-west-2", "111111111111", "my-func")
	want := "arn:aws:lambda:us-west-2:111111111111:function:my-func"
	if got != want {
		t.Errorf("LambdaARN: expected %q, got %q", want, got)
	}
}

func TestTableARN(t *testing.T) {
	got := protocol.TableARN("ap-southeast-1", "222222222222", "my-table")
	want := "arn:aws:dynamodb:ap-southeast-1:222222222222:table/my-table"
	if got != want {
		t.Errorf("TableARN: expected %q, got %q", want, got)
	}
}
