package scenario

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// The two conversions the whole backend rests on: an SDK response into the
// IR's document form, and an evaluated value into an SDK input field.

func TestToDocument_nilIsAbsenceAndNumbersAreJSONNumbers(t *testing.T) {
	// Given: a response with a set member, an unset pointer, an unset list and
	// a value-typed number.
	out := &sqs.ReceiveMessageOutput{
		Messages: []sqstypes.Message{{
			MessageId:     aws.String("m-1"),
			Body:          aws.String("hello"),
			ReceiptHandle: aws.String("rh"),
			Attributes:    map[string]string{"SenderId": "AIDA"},
		}},
	}

	doc, ok := toDocument(out)
	if !ok {
		t.Fatal("a non-nil response converted to nothing")
	}
	body, _ := doc.(map[string]any)

	// Then: ResultMetadata is not a modeled member and is dropped.
	if _, present := body["ResultMetadata"]; present {
		t.Error("ResultMetadata reached the document; a path can only ever mean a modeled member")
	}
	// And: a pointer member is dereferenced, not rendered as a pointer.
	got, resolved, err := resolvePath(doc, "$.Messages[0].Body")
	if err != nil || !resolved || got != "hello" {
		t.Errorf("$.Messages[0].Body = %v (resolved %v, err %v), want \"hello\"", got, resolved, err)
	}
	// And: an unset pointer does not resolve at all, which is what makes
	// `missing` mean absence and `nonEmpty` fail on it.
	if _, resolved, _ := resolvePath(doc, "$.Messages[0].MD5OfBody"); resolved {
		t.Error("an unset pointer member resolved; nil must read as absence, not as null")
	}
}

func TestToDocument_numbersAndEnums(t *testing.T) {
	type sample struct {
		Count     int32
		Big       int64
		Ratio     float64
		Flag      bool
		Attribute sqstypes.QueueAttributeName
		When      time.Time
		Blob      []byte
		Empty     []string
		Absent    *string
	}
	doc, _ := toDocument(&sample{
		Count:     3,
		Big:       9,
		Ratio:     1.5,
		Flag:      true,
		Attribute: sqstypes.QueueAttributeNameQueueArn,
		When:      time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC),
		Blob:      []byte("hi"),
		Empty:     []string{},
	})
	body := doc.(map[string]any)

	// A number is a JSON number whatever its Go width, which is what makes an
	// `equals` compare the same here as in the interpreters.
	for name, want := range map[string]any{"Count": float64(3), "Big": float64(9), "Ratio": 1.5, "Flag": true} {
		if body[name] != want {
			t.Errorf("%s = %#v, want %#v", name, body[name], want)
		}
	}
	// A named string type — every SDK enum — is a plain string.
	if body["Attribute"] != "QueueArn" {
		t.Errorf("Attribute = %#v, want the enum's wire value", body["Attribute"])
	}
	// A timestamp is rendered, never compared.
	if body["When"] != "2026-09-06T12:00:00Z" {
		t.Errorf("When = %#v", body["When"])
	}
	// A blob is base64, as JSON carries it.
	if body["Blob"] != "aGk=" {
		t.Errorf("Blob = %#v", body["Blob"])
	}
	// An empty-but-present list is present and empty; an unset pointer is not
	// there at all.
	if list, ok := body["Empty"].([]any); !ok || len(list) != 0 {
		t.Errorf("Empty = %#v, want an empty list", body["Empty"])
	}
	if _, present := body["Absent"]; present {
		t.Error("an unset pointer reached the document")
	}
}

// TestBinderSetsEverySpellingOfAnInputField is the claim the emitter's design
// rests on: one helper writes a member whether smithy-go made it a pointer, a
// value, an enum, a list of enums, a string map or a slice of nested
// structures — none of which the pinned shape snapshot can be trusted to
// predict, since it and the vendored SDK are generated from different
// revisions of the AWS model.
func TestBinderSetsEverySpellingOfAnInputField(t *testing.T) {
	bag := newContextBag()
	bag.set("queue.url", "http://q/x")
	b := &Binder{runID: "oc", group: "sqs-gen-queue", bag: bag}

	// A pointer string, and a value int32 — ReceiveMessage has both, and the
	// model says NullableInteger for the second.
	receive := &sqs.ReceiveMessageInput{}
	b.Set("QueueUrl", &receive.QueueUrl, Ref("queue.url"))
	b.Set("MaxNumberOfMessages", &receive.MaxNumberOfMessages, 10)
	b.Set("WaitTimeSeconds", &receive.WaitTimeSeconds, 1)
	// A list of enums.
	b.Set("MessageSystemAttributeNames", &receive.MessageSystemAttributeNames, []any{"All"})
	if b.err != nil {
		t.Fatalf("Set on ReceiveMessageInput.%s: %v", b.member, b.err)
	}
	if aws.ToString(receive.QueueUrl) != "http://q/x" {
		t.Errorf("QueueUrl = %v", receive.QueueUrl)
	}
	if receive.MaxNumberOfMessages != 10 || receive.WaitTimeSeconds != 1 {
		t.Errorf("MaxNumberOfMessages = %d, WaitTimeSeconds = %d", receive.MaxNumberOfMessages, receive.WaitTimeSeconds)
	}
	if len(receive.MessageSystemAttributeNames) != 1 || receive.MessageSystemAttributeNames[0] != "All" {
		t.Errorf("MessageSystemAttributeNames = %#v", receive.MessageSystemAttributeNames)
	}

	// A string map, with an expression inside it.
	create := &sqs.CreateQueueInput{}
	b.Set("QueueName", &create.QueueName, Name("q"))
	b.Set("Attributes", &create.Attributes, map[string]any{
		"VisibilityTimeout": "30",
		"RedrivePolicy":     Concat(`{"arn":"`, Ref("queue.url"), `"}`),
	})
	if b.err != nil {
		t.Fatalf("Set on CreateQueueInput.%s: %v", b.member, b.err)
	}
	if aws.ToString(create.QueueName) != "oc-sqs-gen-queue-q" {
		t.Errorf("QueueName = %v", create.QueueName)
	}
	if create.Attributes["RedrivePolicy"] != `{"arn":"http://q/x"}` {
		t.Errorf("RedrivePolicy = %q", create.Attributes["RedrivePolicy"])
	}

	// A list of nested structures, whose own members are a mix of pointer and
	// value fields.
	batch := &sqs.SendMessageBatchInput{}
	b.Set("QueueUrl", &batch.QueueUrl, Ref("queue.url"))
	b.Set("Entries", &batch.Entries, []any{
		map[string]any{"Id": "1", "MessageBody": "first"},
		map[string]any{"Id": "2", "MessageBody": "second", "DelaySeconds": 0},
	})
	if b.err != nil {
		t.Fatalf("Set on SendMessageBatchInput.%s: %v", b.member, b.err)
	}
	if len(batch.Entries) != 2 || aws.ToString(batch.Entries[1].MessageBody) != "second" {
		t.Errorf("Entries = %#v", batch.Entries)
	}
}

func TestBinderRefusesAValueTheFieldCannotTake(t *testing.T) {
	b := &Binder{runID: "oc", group: "g", bag: newContextBag()}
	in := &sqs.ReceiveMessageInput{}
	b.Set("MaxNumberOfMessages", &in.MaxNumberOfMessages, "10")
	if b.err == nil {
		t.Fatal(`"10" was accepted for an integer member; a string is not a number anywhere else in the IR either`)
	}
	if b.member != "MaxNumberOfMessages" {
		t.Errorf("the failure named %q, want the member", b.member)
	}
}

// TestBinderStopsAtTheFirstFailure keeps a later assignment from overwriting
// the member a failure message is about.
func TestBinderStopsAtTheFirstFailure(t *testing.T) {
	b := &Binder{runID: "oc", group: "g", bag: newContextBag()}
	in := &sqs.ReceiveMessageInput{}
	b.Set("MaxNumberOfMessages", &in.MaxNumberOfMessages, true)
	b.Set("QueueUrl", &in.QueueUrl, "http://q/x")
	if b.member != "MaxNumberOfMessages" {
		t.Errorf("the failure named %q, want the first member that failed", b.member)
	}
	if in.QueueUrl != nil {
		t.Error("an assignment after a failure was still applied")
	}
}
