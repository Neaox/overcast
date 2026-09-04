package eventstream_test

import (
	"bytes"
	"testing"

	smithyeventstream "github.com/aws/smithy-go/eventstream"

	"github.com/overcast-sh/overcast/internal/protocol/eventstream"
)

// TestWriteEvent_decodesWithTheSDKDecoder pins the encoder against the decoder
// every AWS SDK for Go v2 client uses, so a framing or CRC mistake fails here
// rather than in whichever service happens to stream next.
func TestWriteEvent_decodesWithTheSDKDecoder(t *testing.T) {
	// Given: an event message written by the shared encoder
	var buf bytes.Buffer
	if err := eventstream.WriteEvent(&buf, "sessionUpdate", eventstream.JSONContentType, []byte(`{"a":1}`)); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}

	// When: the SDK's decoder reads it
	msg, err := smithyeventstream.NewDecoder().Decode(&buf, nil)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Then: the headers and payload survive the round trip
	for _, want := range []struct{ name, value string }{
		{":message-type", "event"},
		{":event-type", "sessionUpdate"},
		{":content-type", "application/json"},
	} {
		got := msg.Headers.Get(want.name)
		if got == nil || got.String() != want.value {
			t.Fatalf("header %s = %v, want %q", want.name, got, want.value)
		}
	}
	if string(msg.Payload) != `{"a":1}` {
		t.Fatalf("payload = %q, want %q", msg.Payload, `{"a":1}`)
	}
	if buf.Len() != 0 {
		t.Fatalf("%d bytes left over after one message", buf.Len())
	}
}

// TestWriteEvent_initialResponseIsAnEmptyJSONDocument records the frame the AWS
// JSON event-stream deserializers block on: an `event` message typed
// initial-response, carrying the operation's initial output document.
func TestWriteEvent_initialResponseIsAnEmptyJSONDocument(t *testing.T) {
	// Given: an initial-response message with an empty output document
	var buf bytes.Buffer
	if err := eventstream.WriteEvent(&buf, eventstream.InitialResponseEventType, eventstream.JSONContentType, []byte("{}")); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}

	// When: the SDK's decoder reads it
	msg, err := smithyeventstream.NewDecoder().Decode(&buf, nil)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Then: it is the frame the deserializers recognise
	if got := msg.Headers.Get(":event-type"); got == nil || got.String() != "initial-response" {
		t.Fatalf(":event-type = %v, want initial-response", got)
	}
	if string(msg.Payload) != "{}" {
		t.Fatalf("payload = %q, want %q", msg.Payload, "{}")
	}
}
