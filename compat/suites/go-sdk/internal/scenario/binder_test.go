package scenario

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// What is left of building an input at run time.
//
// cmd/compatgen resolves each member's field type from this module's own
// vendored SDK and writes the spelling into the emitted source, so these tests
// are written the way the emitted files are: the pointer, the enum conversion
// and every composite are Go, and only a deferred expression goes through
// Bind.

// TestEmittedSpellingBuildsEveryShapeOfInputField is the claim the emitter's
// design rests on. Each of these is a spelling cmd/compatgen writes, and each
// is one the pinned shape snapshot could not have predicted: ReceiveMessage's
// MaxNumberOfMessages and WaitTimeSeconds target NullableInteger in
// models/aws/shapes/sqs.json, which says pointer, and are plain int32 here.
// If the SDK moves under us, this stops compiling — which is the whole point
// of emitting typed calls rather than binding through reflection.
func TestEmittedSpellingBuildsEveryShapeOfInputField(t *testing.T) {
	bag := newContextBag()
	bag.set("queue.url", "http://q/x")
	b := &Binder{runID: "oc", group: "sqs-gen-queue", bag: bag}

	// A pointer string from a $ref, two value-typed int32s, and a list of
	// enums.
	receive := &sqs.ReceiveMessageInput{}
	receive.QueueUrl = aws.String(Bind[string](b, "QueueUrl", Ref("queue.url")))
	receive.MaxNumberOfMessages = 10
	receive.WaitTimeSeconds = 1
	receive.MessageSystemAttributeNames = []sqstypes.MessageSystemAttributeName{"All"}
	if b.err != nil {
		t.Fatalf("building ReceiveMessageInput.%s: %v", b.member, b.err)
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

	// A string map with an expression at one of its values, which is where a
	// Bind sits inside a composite the emitter spelled.
	create := &sqs.CreateQueueInput{}
	create.QueueName = aws.String(Bind[string](b, "QueueName", Name("q")))
	create.Attributes = map[string]string{
		"VisibilityTimeout": "30",
		"RedrivePolicy":     Bind[string](b, "Attributes", Concat(`{"arn":"`, Ref("queue.url"), `"}`)),
	}
	if b.err != nil {
		t.Fatalf("building CreateQueueInput.%s: %v", b.member, b.err)
	}
	if aws.ToString(create.QueueName) != "oc-sqs-gen-queue-q" {
		t.Errorf("QueueName = %v", create.QueueName)
	}
	if create.Attributes["RedrivePolicy"] != `{"arn":"http://q/x"}` {
		t.Errorf("RedrivePolicy = %q", create.Attributes["RedrivePolicy"])
	}

	// A list of nested structures, whose own members mix pointer and value
	// fields.
	batch := &sqs.SendMessageBatchInput{}
	batch.QueueUrl = aws.String(Bind[string](b, "QueueUrl", Ref("queue.url")))
	batch.Entries = []sqstypes.SendMessageBatchRequestEntry{
		{Id: aws.String("1"), MessageBody: aws.String("first")},
		{Id: aws.String("2"), MessageBody: aws.String(Bind[string](b, "Entries", Ref("queue.url")))},
	}
	if b.err != nil {
		t.Fatalf("building SendMessageBatchInput.%s: %v", b.member, b.err)
	}
	if len(batch.Entries) != 2 || aws.ToString(batch.Entries[1].MessageBody) != "http://q/x" {
		t.Errorf("Entries = %#v", batch.Entries)
	}
}

// TestBindConvertsEveryScalarTheGeneratorCanSpell keeps Bindable and convert in
// step: a type the emitter may instantiate Bind with and convert has no branch
// for would fail every call that used it, at run time, in whichever service
// first modeled that width.
func TestBindConvertsEveryScalarTheGeneratorCanSpell(t *testing.T) {
	newBinder := func() *Binder { return &Binder{runID: "oc", group: "g", bag: newContextBag()} }

	b := newBinder()
	if got := Bind[string](b, "M", "hello"); got != "hello" {
		t.Errorf("string = %q", got)
	}
	if got := Bind[bool](b, "M", true); !got {
		t.Errorf("bool = %v", got)
	}
	if got := Bind[int8](b, "M", 7); got != 7 {
		t.Errorf("int8 = %d", got)
	}
	if got := Bind[int16](b, "M", 300); got != 300 {
		t.Errorf("int16 = %d", got)
	}
	if got := Bind[int32](b, "M", 70000); got != 70000 {
		t.Errorf("int32 = %d", got)
	}
	if got := Bind[int64](b, "M", 5_000_000_000); got != 5_000_000_000 {
		t.Errorf("int64 = %d", got)
	}
	if got := Bind[float32](b, "M", 1.5); got != 1.5 {
		t.Errorf("float32 = %v", got)
	}
	if got := Bind[float64](b, "M", 1.5); got != 1.5 {
		t.Errorf("float64 = %v", got)
	}
	if b.err != nil {
		t.Fatalf("a scalar the generator can spell was refused at %q: %v", b.member, b.err)
	}

	// And the refusals, each of which is a wrong literal caught rather than
	// coerced.
	for _, tc := range []struct {
		name string
		bind func(*Binder)
	}{
		{"a string for a number", func(b *Binder) { Bind[int32](b, "M", "10") }},
		{"a number for a string", func(b *Binder) { Bind[string](b, "M", 10) }},
		{"a fraction for an integer", func(b *Binder) { Bind[int32](b, "M", 1.5) }},
		{"a number past the width", func(b *Binder) { Bind[int8](b, "M", 300) }},
		{"a string for a boolean", func(b *Binder) { Bind[bool](b, "M", "true") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newBinder()
			tc.bind(b)
			if b.err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			if b.member != "M" {
				t.Errorf("the failure named %q, want the member", b.member)
			}
		})
	}
}

// TestBindStopsAtTheFirstFailure keeps a later member from overwriting the one
// a failure message is about — and, now that the emitted code assigns the
// result rather than a field, keeps a second expression from being evaluated
// at all once the call is doomed.
func TestBindStopsAtTheFirstFailure(t *testing.T) {
	bag := newContextBag()
	bag.set("queue.url", "http://q/x")
	b := &Binder{runID: "oc", group: "g", bag: bag}
	in := &sqs.ReceiveMessageInput{}
	in.MaxNumberOfMessages = Bind[int32](b, "MaxNumberOfMessages", true)
	in.QueueUrl = aws.String(Bind[string](b, "QueueUrl", Ref("queue.url")))
	if b.member != "MaxNumberOfMessages" {
		t.Errorf("the failure named %q, want the first member that failed", b.member)
	}
	if aws.ToString(in.QueueUrl) != "" {
		t.Errorf("QueueUrl = %q; a bind after a failure must not resolve", aws.ToString(in.QueueUrl))
	}
}

// TestBindReportsAnUnresolvableRef keeps the one failure teardown is allowed to
// skip a step for reaching the binder intact.
func TestBindReportsAnUnresolvableRef(t *testing.T) {
	b := &Binder{runID: "oc", group: "g", bag: newContextBag()}
	Bind[string](b, "QueueUrl", Ref("queue.url"))
	if b.err == nil {
		t.Fatal("an unresolvable $ref was accepted")
	}
	if _, ok := b.err.(*refError); !ok {
		t.Errorf("err = %T, want *refError so teardown can skip the step", b.err)
	}
}
