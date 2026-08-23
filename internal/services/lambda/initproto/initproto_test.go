package initproto_test

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/services/lambda/initproto"
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
			if got != tc.frame {
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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := initproto.Decode(bufio.NewReader(strings.NewReader(tc.in)))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
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
