package scenario

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// The conversion the response half of the backend rests on: an SDK output
// struct into the IR's document form, so a path resolves and an `equals`
// compares in the same type system as in the three interpreters.
//
// The other direction — a value into an SDK input field — is no longer a
// conversion at all. cmd/compatgen resolves the field's type from the vendored
// SDK and writes the spelling into the emitted source, so only the deferred
// part of a value reaches run time; see binder_test.go.

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
