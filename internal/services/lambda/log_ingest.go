package lambda

// log_ingest.go — the host end of the in-container init's log channel.
//
// This is not an AWS API. It is an emulator-internal channel between two
// Overcast components — the init inside a Lambda execution environment and the
// Overcast process that started it — and it lives on the container-facing
// Runtime API listener, under a path prefix no AWS Lambda API uses, the way
// LocalStack's executor endpoint does. Nothing outside a Lambda container is
// meant to reach it, and nothing outside one can: it is served only on the
// per-execution-environment listeners, and a request is attributed to a
// container exactly as GET /next is (the listener it arrived on first, the
// source address second), so one container cannot write into another's log.
//
// See docs/plans/lambda-in-container-init.md § 3.4.

import (
	"bufio"
	"errors"
	"io"
	"net/http"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/services/lambda/initproto"
)

// ingestQueueDepth lets the frame decoder run ahead of the batching loop by a
// batch or two, so a slow CloudWatch write never leaves the init's connection
// unread.
const ingestQueueDepth = 64

// frameResult is one frame off the decoder goroutine, or the error that ended
// it.
type frameResult struct {
	frame initproto.Frame
	err   error
}

// handleContainerLogs serves POST /overcast/v1/logs: one long-lived chunked
// request per init, carrying newline-delimited initproto.Frames for the life of
// the container.
//
// It answers only when the body ends. The init treats a response as the end of
// its session and reconnects, so replying early would put the log channel in a
// reconnect loop; the status it eventually gets is for its diagnostics.
func (s *RuntimeAPIServer) handleContainerLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// The listener the request arrived on, first and normally last: it is
	// assigned per execution environment and indexed before the container is
	// created, so the init's very first connect succeeds instead of waiting out
	// a registration that needs Docker to report an address. That matters more
	// here than anywhere else on this mux — everything the container prints
	// during INIT is queued behind this connection, and a host that took even
	// 100 ms to accept it would write the first invocation's START before that
	// output arrived.
	var cfg runtimeContainerConfig
	sink := s.logSinkForPort(localPortOf(r))
	if sink == nil {
		var ok bool
		_, cfg, ok = s.lookupContainerConfigWait(r.Context(), r)
		if ok {
			sink = cfg.LogSink
		}
	}
	if sink == nil {
		// Same answer as any other unattributable Runtime API call, with the
		// same diagnostic: the init retries with backoff and replays its
		// backlog, so a registration that has not landed yet costs nothing but
		// the delay.
		s.logUnknownContainer(r)
		http.Error(w, "unknown container", http.StatusNotFound)
		return
	}

	sink.streamOpened()
	defer sink.streamClosed()

	// Decoding runs on its own goroutine so the batch's flush timer can fire
	// while the connection is idle — the same shape the shared follower uses.
	frames := make(chan frameResult, ingestQueueDepth)
	go func() {
		defer close(frames)
		br := bufio.NewReader(r.Body)
		for {
			f, err := initproto.Decode(br)
			if err != nil {
				if !errors.Is(err, io.EOF) {
					frames <- frameResult{err: err}
				}
				return
			}
			frames <- frameResult{frame: f}
		}
	}()

	flushTimer := s.clk.Timer(ingestFlushInterval)
	flushTimer.Stop()
	defer flushTimer.Stop()

	for {
		select {
		case res, ok := <-frames:
			if !ok {
				// The init closed the stream after its final drain, which is
				// the event the crash and timeout paths wait on.
				sink.flush()
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if res.err != nil {
				if r.Context().Err() == nil {
					s.logger.Debug("lambda log ingest: stream ended",
						zap.String("function", cfg.FunctionName),
						zap.String("container", shortContainerID(cfg.ContainerID)),
						zap.Error(res.err),
					)
				}
				sink.flush()
				return
			}
			if !sink.frame(res.frame) {
				continue
			}
			if sink.batchFull() {
				sink.flush()
				flushTimer.Stop()
			} else {
				flushTimer.Reset(ingestFlushInterval)
			}

		case <-flushTimer.C:
			sink.flush()

		case <-r.Context().Done():
			sink.flush()
			return

		case <-s.done:
			// Overcast is shutting down; let the handler go so Shutdown can
			// complete rather than holding it on a connection that will be
			// closed anyway.
			sink.flush()
			return
		}
	}
}
