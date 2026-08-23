//go:build linux

package lambdainit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Neaox/overcast/internal/services/lambda/initproto"
)

const (
	// defaultBacklogFrames / defaultBacklogBytes bound the replay buffer. It
	// exists so that a log connection dropping mid-life costs nothing: the
	// init reconnects and replays, and the host de-duplicates by seq. Whichever
	// bound is hit first evicts the oldest frame; frames evicted before they
	// were ever written are counted and reported as a gap.
	defaultBacklogFrames = 4096
	defaultBacklogBytes  = 1 << 20

	// Reconnect backoff, the same shape the host-side log follower uses.
	backoffMin = 50 * time.Millisecond
	backoffMax = 2 * time.Second

	// firstConnectBackoff is how long the *first* connection waits before
	// retrying. It is much shorter than backoffMin because the only thing it
	// ever waits out is the host finishing the container's registration, which
	// is a race the init loses by microseconds if at all — not a daemon
	// problem, and not something worth 50 ms of a cold start. Once a connection
	// has done real work the ordinary backoff takes over.
	firstConnectBackoff = 5 * time.Millisecond

	// frameOverheadBytes approximates the JSON envelope around a frame's
	// strings, so the byte bound accounts for framing rather than payload
	// alone.
	frameOverheadBytes = 96
)

// shipper owns the init's single long-lived connection to the host's log
// ingest: one chunked POST whose body is an unending NDJSON stream of frames.
// Frames are numbered per init, and the number is the only ordering authority
// on either side.
type shipper struct {
	url    string
	client *http.Client
	now    func() time.Time
	diag   *diagLog

	maxFrames int
	maxBytes  int

	mu       sync.Mutex
	cond     *sync.Cond
	backlog  []initproto.Frame
	bytes    int
	seq      uint64
	next     int    // index in backlog of the next frame to write on this connection
	written  uint64 // highest seq handed to a connection
	dropped  uint64 // frames evicted before they were ever written
	inFlight uint64 // dropped count carried by a gap frame that is not yet written
	sessions int    // connections opened, for the diagnostics
	closed   bool

	done chan struct{}
}

func newShipper(hostAddr string, backlogFrames, backlogBytes int, now func() time.Time, diag *diagLog) *shipper {
	if backlogFrames <= 0 {
		backlogFrames = defaultBacklogFrames
	}
	if backlogBytes <= 0 {
		backlogBytes = defaultBacklogBytes
	}
	s := &shipper{
		url: "http://" + hostAddr + initproto.LogsPath,
		client: &http.Client{
			// The shipper gets its own transport rather than sharing the
			// proxy's: its connection is held open for the life of the
			// container, and it must not sit in the same idle pool as the
			// invoke traffic.
			Transport: &http.Transport{
				DialContext:        (&net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
				DisableCompression: true,
				DisableKeepAlives:  false,
				MaxIdleConns:       2,
				ForceAttemptHTTP2:  false,
			},
		},
		now:       now,
		diag:      diag,
		maxFrames: backlogFrames,
		maxBytes:  backlogBytes,
		done:      make(chan struct{}),
	}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func frameSize(f initproto.Frame) int {
	return len(f.Msg) + len(f.Req) + len(f.Src) + frameOverheadBytes
}

// publish assigns the next seq to a line and queues it. It never blocks on the
// network: the pipe readers must keep draining whatever the host is doing.
func (s *shipper) publish(req, src, msg string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.seq++
	f := initproto.Frame{Seq: s.seq, Req: req, Src: src, T: s.now().UnixMilli(), Msg: msg}
	s.backlog = append(s.backlog, f)
	s.bytes += frameSize(f)
	s.evict()
	s.cond.Broadcast()
	return f.Seq
}

// evict trims the backlog to its bounds, oldest first. Dropping a frame that
// has already been written to a connection loses nothing; dropping one that has
// not is a gap, and is counted.
func (s *shipper) evict() {
	for len(s.backlog) > 1 && (len(s.backlog) > s.maxFrames || s.bytes > s.maxBytes) {
		s.bytes -= frameSize(s.backlog[0])
		s.backlog = s.backlog[1:]
		if s.next > 0 {
			s.next--
		} else {
			s.dropped++
		}
	}
}

// currentSeq reports the highest seq assigned so far.
func (s *shipper) currentSeq() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seq
}

// writtenSeq reports the highest seq written to a connection.
func (s *shipper) writtenSeq() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.written
}

// run keeps a connection to the host's ingest for as long as there is anything
// to say, reconnecting with backoff. It returns when the shipper is closed or
// ctx ends.
func (s *shipper) run(ctx context.Context) {
	defer close(s.done)

	backoff := firstConnectBackoff
	for {
		if !s.waitForWork(ctx) {
			return
		}
		before := s.writtenSeq()
		err := s.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			if s.isClosed() {
				return
			}
			// The host ended the stream cleanly. Reconnect and replay.
			err = errors.New("host closed the log stream")
		}
		s.diag.printf("log channel disconnected: %v", err)
		s.rewind()
		if s.writtenSeq() > before {
			// The connection did real work before it failed; this is not a
			// tight failure loop, so start the backoff over.
			backoff = backoffMin
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		backoff *= 2
		if backoff > backoffMax {
			backoff = backoffMax
		}
	}
}

// waitForWork blocks until there is a frame to send, or the shipper is closed
// with nothing left. It reports whether the caller should open a connection.
func (s *shipper) waitForWork(ctx context.Context) bool {
	stop := context.AfterFunc(ctx, func() {
		s.mu.Lock()
		s.cond.Broadcast()
		s.mu.Unlock()
	})
	defer stop()

	s.mu.Lock()
	defer s.mu.Unlock()
	for s.next >= len(s.backlog) && !s.closed && ctx.Err() == nil {
		s.cond.Wait()
	}
	return ctx.Err() == nil && s.next < len(s.backlog)
}

// session opens one POST and pumps frames into it until the connection or the
// shipper ends.
func (s *shipper) session(ctx context.Context) error {
	// sctx ends with this connection. The pump can be parked waiting for the
	// next frame when the host hangs up — nothing is being written, so no write
	// error will ever surface — and this is what wakes it.
	sctx, endSession := context.WithCancel(ctx)
	defer endSession()

	pr, pw := io.Pipe()
	req, err := http.NewRequestWithContext(sctx, http.MethodPost, s.url, pr)
	if err != nil {
		return err
	}
	// No Content-Length: the body is unbounded, so Go frames it as chunked,
	// which is what makes each write visible to the host immediately.
	req.Header.Set("Content-Type", "application/x-ndjson")

	type result struct{ err error }
	resCh := make(chan result, 1)
	go func() {
		resp, derr := s.client.Do(req)
		if derr == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusNoContent {
				derr = fmt.Errorf("log ingest returned %s", resp.Status)
			}
		}
		// Unblock the pump, whether it is mid-write or waiting for the next
		// frame: this connection is finished either way.
		pr.CloseWithError(io.ErrClosedPipe)
		resCh <- result{err: derr}
		endSession()
	}()

	perr := s.pump(sctx, pw)
	if perr != nil {
		pw.CloseWithError(perr)
	} else {
		pw.Close()
	}

	res := <-resCh
	if perr != nil && !errors.Is(perr, io.ErrClosedPipe) {
		return perr
	}
	return res.err
}

// pump writes queued frames to the open connection until the shipper is closed
// (returning nil, having written everything) or the write fails.
func (s *shipper) pump(ctx context.Context, w io.Writer) error {
	s.connected()
	for {
		frames, ok := s.take(ctx)
		if !ok {
			return nil
		}
		for _, f := range frames {
			if err := f.Encode(w); err != nil {
				return err
			}
		}
		s.confirm(frames[len(frames)-1].Seq)
	}
}

// take returns the next batch of frames to write, blocking until there are some
// or the shipper is closed and drained. A batch is preceded by a gap frame when
// the backlog overflowed since the last write.
func (s *shipper) take(ctx context.Context) ([]initproto.Frame, bool) {
	stop := context.AfterFunc(ctx, func() {
		s.mu.Lock()
		s.cond.Broadcast()
		s.mu.Unlock()
	})
	defer stop()

	s.mu.Lock()
	defer s.mu.Unlock()
	for s.next >= len(s.backlog) && !s.closed && ctx.Err() == nil {
		s.cond.Wait()
	}
	if s.next >= len(s.backlog) || ctx.Err() != nil {
		return nil, false
	}

	out := make([]initproto.Frame, 0, len(s.backlog)-s.next+1)
	if s.dropped > 0 {
		// The gap frame takes the seq of the last frame that was lost — the one
		// immediately before the oldest frame we still hold — so a host
		// tracking contiguity sees an unbroken sequence and learns exactly how
		// many frames it will never receive.
		out = append(out, initproto.Frame{
			Seq: s.backlog[s.next].Seq - 1,
			T:   s.now().UnixMilli(),
			Gap: s.dropped,
		})
		s.diag.printf("log channel gap: %d frames dropped from the replay backlog before seq %d", s.dropped, s.backlog[s.next].Seq)
		s.inFlight = s.dropped
		s.dropped = 0
	}
	out = append(out, s.backlog[s.next:]...)
	s.next = len(s.backlog)
	return out, true
}

// confirm records that everything up to seq reached the connection.
func (s *shipper) confirm(seq uint64) {
	s.mu.Lock()
	if seq > s.written {
		s.written = seq
	}
	s.inFlight = 0
	s.cond.Broadcast()
	s.mu.Unlock()
}

// rewind restores the write cursor to the oldest frame still held, so the next
// connection replays it. Duplicates are cheaper than losses: the host
// de-duplicates by seq, and nothing else can guarantee that a frame written to
// a connection that then failed was actually received.
func (s *shipper) rewind() {
	s.mu.Lock()
	s.next = 0
	s.dropped += s.inFlight
	s.inFlight = 0
	s.cond.Broadcast()
	s.mu.Unlock()
}

// connected reports a new stream. It fires when the init starts writing to it,
// which is the first moment the connection is known to be usable.
func (s *shipper) connected() {
	s.mu.Lock()
	s.sessions++
	n := s.sessions
	s.mu.Unlock()
	if n == 1 {
		s.diag.printf("log channel connected")
		return
	}
	s.diag.printf("log channel reconnected (stream %d)", n)
}

func (s *shipper) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// flush blocks until every frame published before the call has been written to
// the connection, or ctx ends. Used on exit, where the whole point is that the
// last thing the runtime printed reaches the host before the container dies.
func (s *shipper) flush(ctx context.Context) error {
	stop := context.AfterFunc(ctx, func() {
		s.mu.Lock()
		s.cond.Broadcast()
		s.mu.Unlock()
	})
	defer stop()

	s.mu.Lock()
	defer s.mu.Unlock()
	target := s.seq
	for s.written < target && ctx.Err() == nil {
		s.cond.Wait()
	}
	if s.written < target {
		return fmt.Errorf("log channel flushed through seq %d of %d", s.written, target)
	}
	return nil
}

// close ends the stream once everything queued has been written, and waits for
// the sender to finish.
func (s *shipper) close(ctx context.Context) {
	s.mu.Lock()
	s.closed = true
	s.cond.Broadcast()
	s.mu.Unlock()

	select {
	case <-s.done:
	case <-ctx.Done():
	}
}
