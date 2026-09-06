package initproto_test

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/services/lambda/initproto"
)

func TestFrameRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		frame initproto.Frame
	}{
		{
			name:  "init phase line has no request id",
			frame: initproto.Frame{Seq: 1, Src: initproto.SrcStdout, T: 1724371200000, Msg: "INIT_START"},
		},
		{
			name:  "invocation line carries the request id",
			frame: initproto.Frame{Seq: 2, Req: "8f3c1a2e-0000-4000-8000-000000000001", Src: initproto.SrcStderr, T: 1724371200123, Msg: "boom"},
		},
		{
			name:  "extension line names the extension",
			frame: initproto.Frame{Seq: 3, Src: initproto.ExtensionSrc("telemetry"), T: 1724371200456, Msg: "ready"},
		},
		{
			name:  "gap frame reports frames lost to backlog overflow",
			frame: initproto.Frame{Seq: 99, Gap: 12, T: 1724371200789},
		},
		{
			name:  "empty message survives",
			frame: initproto.Frame{Seq: 4, Src: initproto.SrcStdout, T: 1},
		},
		{
			name:  "embedded newline and quotes survive",
			frame: initproto.Frame{Seq: 5, Src: initproto.SrcStdout, T: 2, Msg: "a\nb\t\"c\""},
		},
		{
			name:  "init start record",
			frame: initproto.Frame{Seq: 6, T: 3, Rec: initproto.Record{Type: initproto.RecInitStart}},
		},
		{
			name:  "init runtime done record carries a status",
			frame: initproto.Frame{Seq: 7, T: 4, Rec: initproto.Record{Type: initproto.RecInitRuntimeDone, Status: initproto.StatusSuccess}},
		},
		{
			name:  "init report record carries a status and a duration",
			frame: initproto.Frame{Seq: 8, T: 5, Rec: initproto.Record{Type: initproto.RecInitReport, Status: initproto.StatusError, DurationMs: 214.88}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf strings.Builder
			if err := tc.frame.Encode(&buf); err != nil {
				t.Fatalf("Encode: %v", err)
			}
			encoded := buf.String()
			if !strings.HasSuffix(encoded, "\n") {
				t.Fatalf("Encode did not terminate the frame with a newline: %q", encoded)
			}
			if strings.Count(encoded, "\n") != 1 {
				t.Fatalf("Encode wrote more than one line: %q", encoded)
			}

			got, err := initproto.Decode(bufio.NewReader(strings.NewReader(encoded)))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if !reflect.DeepEqual(got, tc.frame) {
				t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, tc.frame)
			}
		})
	}
}

func TestDecodeStreamsFramesInOrder(t *testing.T) {
	var buf strings.Builder
	for i := uint64(1); i <= 3; i++ {
		f := initproto.Frame{Seq: i, Src: initproto.SrcStdout, T: int64(i), Msg: "line"}
		if err := f.Encode(&buf); err != nil {
			t.Fatalf("Encode: %v", err)
		}
	}

	r := bufio.NewReader(strings.NewReader(buf.String()))
	for i := uint64(1); i <= 3; i++ {
		f, err := initproto.Decode(r)
		if err != nil {
			t.Fatalf("Decode frame %d: %v", i, err)
		}
		if f.Seq != i {
			t.Fatalf("frame %d: got seq %d", i, f.Seq)
		}
	}
	if _, err := initproto.Decode(r); !errors.Is(err, io.EOF) {
		t.Fatalf("after the last frame, got err %v, want io.EOF", err)
	}
}

func TestDecodeToleratesUnknownFieldsAndBlankLines(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want initproto.Frame
	}{
		{
			name: "unknown field is ignored",
			in:   `{"seq":7,"src":"stdout","t":42,"msg":"hi","futureField":{"a":[1,2]}}` + "\n",
			want: initproto.Frame{Seq: 7, Src: initproto.SrcStdout, T: 42, Msg: "hi"},
		},
		{
			name: "omitted fields take their zero value",
			in:   `{"seq":8}` + "\n",
			want: initproto.Frame{Seq: 8},
		},
		{
			name: "blank lines between frames are skipped",
			in:   "\n\n" + `{"seq":9,"src":"stderr","t":1,"msg":"x"}` + "\n",
			want: initproto.Frame{Seq: 9, Src: initproto.SrcStderr, T: 1, Msg: "x"},
		},
		{
			name: "a final frame without a trailing newline still decodes",
			in:   `{"seq":10,"src":"stdout","t":1,"msg":"tail"}`,
			want: initproto.Frame{Seq: 10, Src: initproto.SrcStdout, T: 1, Msg: "tail"},
		},
		{
			name: "a line frame carrying no record decodes to a zero record",
			in:   `{"seq":11,"src":"stdout","t":1,"msg":"hi"}` + "\n",
			want: initproto.Frame{Seq: 11, Src: initproto.SrcStdout, T: 1, Msg: "hi"},
		},
		{
			name: "a record with a member this decoder does not know keeps the rest",
			in:   `{"seq":12,"t":1,"rec":{"type":"initReport","status":"success","durationMs":1.5,"futureMember":[]}}` + "\n",
			want: initproto.Frame{Seq: 12, T: 1, Rec: initproto.Record{Type: initproto.RecInitReport, Status: initproto.StatusSuccess, DurationMs: 1.5}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := initproto.Decode(bufio.NewReader(strings.NewReader(tc.in)))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// A record frame is a frame like any other — same stream, same sequence — and
// its existence costs an ordinary line frame nothing on the wire: "rec" is
// omitted entirely, so what a line looks like is exactly what it always looked
// like.
func TestRecordFrameWireShape(t *testing.T) {
	var line strings.Builder
	if err := (initproto.Frame{Seq: 1, Src: initproto.SrcStdout, T: 7, Msg: "hi"}).Encode(&line); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if got, want := strings.TrimSpace(line.String()), `{"seq":1,"src":"stdout","t":7,"msg":"hi"}`; got != want {
		t.Fatalf("line frame:\n got %s\nwant %s", got, want)
	}

	var rec strings.Builder
	if err := (initproto.Frame{Seq: 2, T: 8, Rec: initproto.Record{Type: initproto.RecInitReport, Status: initproto.StatusSuccess, DurationMs: 214.88}}).Encode(&rec); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if got, want := strings.TrimSpace(rec.String()), `{"seq":2,"t":8,"rec":{"type":"initReport","status":"success","durationMs":214.88}}`; got != want {
		t.Fatalf("record frame:\n got %s\nwant %s", got, want)
	}
}

func TestDecodeRejectsMalformedJSON(t *testing.T) {
	_, err := initproto.Decode(bufio.NewReader(strings.NewReader("{not json}\n")))
	if err == nil {
		t.Fatal("Decode accepted malformed JSON")
	}
	if errors.Is(err, io.EOF) {
		t.Fatalf("malformed JSON reported as EOF: %v", err)
	}
}

func TestExtensionSrcRoundTrip(t *testing.T) {
	src := initproto.ExtensionSrc("logs-collector")
	if src != "ext:logs-collector" {
		t.Fatalf("ExtensionSrc = %q", src)
	}

	tests := []struct {
		src      string
		wantName string
		wantOK   bool
	}{
		{src: "ext:logs-collector", wantName: "logs-collector", wantOK: true},
		{src: "ext:", wantName: "", wantOK: false},
		{src: initproto.SrcStdout, wantName: "", wantOK: false},
		{src: initproto.SrcStderr, wantName: "", wantOK: false},
		{src: "", wantName: "", wantOK: false},
	}
	for _, tc := range tests {
		name, ok := initproto.ExtensionName(tc.src)
		if name != tc.wantName || ok != tc.wantOK {
			t.Fatalf("ExtensionName(%q) = (%q, %v), want (%q, %v)", tc.src, name, ok, tc.wantName, tc.wantOK)
		}
	}
}

// TestTelemetryDeliveryForwardsTheBodyVerbatim pins the one property the
// Telemetry relay exists to preserve: the host marshals the batch, and what the
// extension receives is those exact bytes. A delivery that re-marshalled the
// array would reorder object members and reformat numbers, and the subscriber
// would be reading something Overcast wrote rather than something the AWS
// schema owner did.
func TestTelemetryDeliveryForwardsTheBodyVerbatim(t *testing.T) {
	body := `[{"time":"2026-09-06T00:00:00.000Z","type":"platform.initStart","record":{"phase":"init","initializationType":"on-demand"}}]`
	encoded, err := json.Marshal(initproto.TelemetryDelivery{
		ID:   7,
		URI:  "http://127.0.0.1:9999",
		Body: json.RawMessage(body),
	})
	if err != nil {
		t.Fatal(err)
	}
	var got initproto.TelemetryDelivery
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if string(got.Body) != body {
		t.Errorf("body round-tripped to %s, want %s", got.Body, body)
	}
	if got.ID != 7 || got.URI != "http://127.0.0.1:9999" {
		t.Errorf("delivery round-tripped to %+v", got)
	}
}

// TestTelemetryPollOmitsAnAbsentResult keeps the first poll of a channel — and
// every poll that follows one the host answered with nothing — free of a result
// the host would have to tell apart from a real one.
func TestTelemetryPollOmitsAnAbsentResult(t *testing.T) {
	encoded, err := json.Marshal(initproto.TelemetryPoll{})
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != "{}" {
		t.Errorf("an empty poll encodes as %s, want {}", encoded)
	}

	encoded, err = json.Marshal(initproto.TelemetryPoll{Result: &initproto.TelemetryResult{ID: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"result":{"id":3}}` {
		t.Errorf("a successful result encodes as %s", encoded)
	}

	encoded, err = json.Marshal(initproto.TelemetryPoll{Result: &initproto.TelemetryResult{ID: 4, Error: "connection refused"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"result":{"id":4,"error":"connection refused"}}` {
		t.Errorf("a failed result encodes as %s", encoded)
	}
}
