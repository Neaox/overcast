package lambda

// log_ingest_test.go — POST /overcast/v1/logs, the emulator-internal channel
// the in-container init ships its output on.

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/services/lambda/initproto"
)

// newIngestServer stands up a Runtime API server with one registered execution
// environment on loopback, and returns its address and that environment's sink.
// initTimeout is short so an unidentifiable request does not spend the
// production registration wait.
func newIngestServer(t *testing.T) (*RuntimeAPIServer, string, *logSink) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	srv, err := NewRuntimeAPIServerFromListeners([]net.Listener{ln}, addr, 400*time.Millisecond, zap.NewNop(), clock.New())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })

	sink := newLogSink(logSinkConfig{
		logger:     zap.NewNop(),
		clk:        clock.New(),
		group:      "/aws/lambda/demo",
		stream:     "stream",
		region:     "us-east-1",
		runtimeAPI: srv,
	})
	t.Cleanup(sink.close)
	srv.RegisterContainerConfig("127.0.0.1", runtimeContainerConfig{
		FunctionARN:  "arn:aws:lambda:us-east-1:000000000000:function:demo",
		FunctionName: "demo",
		ContainerID:  "cafebabe1234deadbeef",
		LogSink:      sink,
	})
	return srv, addr, sink
}

// waitFor polls until cond holds, so a test never has to guess how long the
// ingest goroutine takes to pick a frame up.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The stream is long-lived: frames are handed to the sink as they arrive, not
// when the request ends, and the request is not answered until the body does —
// the init treats a response as the end of its session and would reconnect.
func TestLogIngest_framesReachTheSinkWhileTheRequestIsStillOpen(t *testing.T) {
	_, addr, sink := newIngestServer(t)

	pr, pw := io.Pipe()
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+initproto.LogsPath, pr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")

	type result struct {
		status int
		err    error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			done <- result{err: err}
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		done <- result{status: resp.StatusCode}
	}()

	if err := frameOf(1, "req-1", "first line").Encode(pw); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the first frame to reach the sink", func() bool {
		return string(sink.snapshot("req-1")) == "first line\n"
	})

	select {
	case r := <-done:
		t.Fatalf("the ingest answered while the stream was still open: %+v", r)
	case <-time.After(50 * time.Millisecond):
	}

	if err := frameOf(2, "req-1", "second line").Encode(pw); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the second frame to reach the sink", func() bool {
		return string(sink.snapshot("req-1")) == "first line\nsecond line\n"
	})

	// The init closes the stream after its final drain, which is the event the
	// crash and timeout paths wait on.
	pw.Close()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("the ingest request failed: %v", r.err)
		}
		if r.status != http.StatusNoContent {
			t.Errorf("status = %d, want 204", r.status)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the ingest did not answer after the body ended")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !sink.awaitStreamEnd(ctx) {
		t.Error("the sink did not see the stream end")
	}
}

// A replayed backlog is de-duplicated by sequence, and a gap frame advances the
// sequence without becoming a line.
func TestLogIngest_dedupesAReplayAndCarriesAGap(t *testing.T) {
	_, addr, sink := newIngestServer(t)

	var body bytes.Buffer
	for _, f := range []initproto.Frame{
		frameOf(1, "req-1", "one"),
		frameOf(2, "req-1", "two"),
		// The connection dropped here; this is the replay.
		frameOf(1, "req-1", "one"),
		frameOf(2, "req-1", "two"),
		{Seq: 9, T: 1700000000000, Gap: 6},
		frameOf(10, "req-1", "after the gap"),
	} {
		if err := f.Encode(&body); err != nil {
			t.Fatal(err)
		}
	}

	resp, err := http.Post("http://"+addr+initproto.LogsPath, "application/x-ndjson", bytes.NewReader(body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if got := string(sink.snapshot("req-1")); got != "one\ntwo\nafter the gap\n" {
		t.Errorf("req-1 tail = %q, want each line once with the gap carrying none", got)
	}
	sink.mu.Lock()
	last := sink.lastSeq
	sink.mu.Unlock()
	if last != 10 {
		t.Errorf("lastSeq = %d, want 10", last)
	}
}

// The ingest is identified exactly as GET /next is. A request Overcast cannot
// attribute to an execution environment is refused, and the init retries with
// backoff and replays — so nothing is lost by refusing one.
func TestLogIngest_unidentifiableContainerIsRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	srv, err := NewRuntimeAPIServerFromListeners([]net.Listener{ln}, addr, 400*time.Millisecond, zap.NewNop(), clock.New())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })

	resp, err := http.Post("http://"+addr+initproto.LogsPath, "application/x-ndjson", bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d for a container the Runtime API does not know, want 404", resp.StatusCode)
	}
}

func TestLogIngest_rejectsAnythingButPost(t *testing.T) {
	_, addr, _ := newIngestServer(t)
	resp, err := http.Get("http://" + addr + initproto.LogsPath)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d for GET, want 405", resp.StatusCode)
	}
}

// The init puts the highest sequence it published on the response it forwards,
// and that is what the invoke path waits for. It has to survive the trip
// through the Runtime API into invokeResponse.
func TestRuntimeAPI_responseCarriesTheInitsLogSeq(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action string
		header string
		want   uint64
	}{
		{name: "response", action: "response", header: "42", want: 42},
		{name: "error", action: "error", header: "7", want: 7},
		{name: "no header is nothing to wait for", action: "response", header: "", want: 0},
		{name: "an unparsable header is nothing to wait for", action: "response", header: "not-a-number", want: 0},
		{name: "a header past uint64 is nothing to wait for", action: "response", header: "184467440737095516150", want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, addr := newRuntimeAPITestServer(t)
			srv.RegisterContainer("127.0.0.1", "arn:aws:lambda:us-east-1:000000000000:function:demo")

			reqID, resultCh := srv.SubmitInvocation("arn:aws:lambda:us-east-1:000000000000:function:demo", []byte(`{}`), time.Now().Add(10*time.Second))
			// The invocation has to be handed out before it can be answered.
			go func() {
				resp, err := http.Get("http://" + addr + "/2018-06-01/runtime/invocation/next")
				if err == nil {
					_, _ = io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
			}()

			var post *http.Request
			var err error
			deadline := time.Now().Add(10 * time.Second)
			for {
				post, err = http.NewRequest(http.MethodPost,
					"http://"+addr+"/2018-06-01/runtime/invocation/"+reqID+"/"+tc.action,
					bytes.NewReader([]byte(`"ok"`)))
				if err != nil {
					t.Fatal(err)
				}
				if tc.header != "" {
					post.Header.Set(initproto.HeaderLogSeq, tc.header)
				}
				resp, err := http.DefaultClient.Do(post)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode == http.StatusAccepted {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("the Runtime API never accepted the response: status %d", resp.StatusCode)
				}
			}

			select {
			case got := <-resultCh:
				if got.LogSeq != tc.want {
					t.Errorf("invokeResponse.LogSeq = %d, want %d", got.LogSeq, tc.want)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("no invokeResponse arrived")
			}
		})
	}
}
