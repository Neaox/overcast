//go:build linux

package lambdainit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"time"
)

const (
	// maxLineBytes bounds one assembled line. A longer line is published
	// truncated and the rest is discarded up to its newline, which is what the
	// host-side reader does with an over-long Docker log line. The tee is not
	// affected: `docker logs` still shows every byte the child wrote.
	maxLineBytes = 256 * 1024

	// readChunkBytes is one read() worth. A pipe holds 64 KiB by default on
	// Linux, so this empties a full pipe in one syscall.
	readChunkBytes = 64 * 1024
)

// pipeReader owns one end of one pipe: it is the only goroutine that reads that
// fd, which is what makes "drained" a fact rather than an estimate.
//
// The fd is non-blocking and registered with the runtime poller (every
// os.Pipe() file is). The reader takes it through
// (*os.File).SyscallConn().Read, whose callback is invoked once per readability
// wakeup: inside it we read() until EAGAIN, publish everything we assembled,
// record the mark, and return false to park until the pipe is readable again.
//
// A drain request (see drain) needs a mark that is known to have *started after
// the request*, otherwise a mark already in flight could satisfy it while bytes
// written just before the request are still in the pipe. Each read attempt
// therefore snapshots pokeGen — a counter every drain request bumps — when it
// begins, and publishes that snapshot with its mark; a request waits for a mark
// carrying at least its own generation. To make a parked reader re-attempt at
// all, the request sets an immediate read deadline, which fails the poller wait
// with os.ErrDeadlineExceeded; the loop clears the deadline and re-arms. That
// converges in at most two attempts and never busy-waits.
type pipeReader struct {
	f        *os.File
	src      string
	tee      io.Writer
	annotate bool
	// publish hands one assembled line to the log shipper and returns the seq
	// it was given.
	publish func(src, msg string) uint64
	// annotation returns the "req=<id> " prefix for the teed copy of a line,
	// used only when annotate is on.
	annotation func() string
	maxLine    int

	buf      []byte
	pending  []byte
	dropping bool // discarding the tail of an over-long line

	mu        sync.Mutex
	cond      *sync.Cond
	pokeGen   uint64 // bumped by every drain request
	satisfied uint64 // highest pokeGen a completed read attempt covers
	drainSeq  uint64 // highest seq published as of the last mark
	lastSeq   uint64
	lines     uint64
	finished  bool
	err       error
}

func newPipeReader(f *os.File, src string, tee io.Writer, publish func(src, msg string) uint64) (*pipeReader, error) {
	// Probe for poller support: a pipe that cannot take a deadline cannot be
	// woken for a drain, and everything below would silently degrade to
	// "drained when the next byte arrives". os.Pipe always qualifies; this
	// catches a caller handing us something else.
	if err := f.SetReadDeadline(time.Time{}); err != nil {
		return nil, fmt.Errorf("pipe for %s does not support deadlines: %w", src, err)
	}
	p := &pipeReader{
		f:       f,
		src:     src,
		tee:     tee,
		publish: publish,
		maxLine: maxLineBytes,
		buf:     make([]byte, readChunkBytes),
	}
	p.cond = sync.NewCond(&p.mu)
	return p, nil
}

// run reads the pipe until EOF or an unrecoverable error. It returns only when
// the reader is finished, so a caller can wait on it.
func (p *pipeReader) run() {
	defer p.finish()

	rc, err := p.f.SyscallConn()
	if err != nil {
		p.setErr(err)
		return
	}

	for {
		err := rc.Read(func(fd uintptr) bool { return p.attempt(fd) })
		switch {
		case err == nil:
			// The callback returned true: EOF or a fatal read error.
			return
		case errors.Is(err, os.ErrDeadlineExceeded):
			// A drain request woke us. Clear the deadline and read again; the
			// new attempt snapshots the requester's generation.
			if derr := p.f.SetReadDeadline(time.Time{}); derr != nil {
				p.setErr(derr)
				return
			}
		default:
			p.setErr(err)
			return
		}
	}
}

// attempt drains the fd until EAGAIN. It returns false to park until the pipe
// is readable again, true when the reader is done with the fd for good.
func (p *pipeReader) attempt(fd uintptr) bool {
	gen := p.generation()
	for {
		n, err := syscall.Read(int(fd), p.buf)
		switch {
		case err == nil && n > 0:
			p.consume(p.buf[:n])
		case err == nil && n == 0:
			return true // writer end closed: EOF
		case errors.Is(err, syscall.EINTR):
			// Interrupted before reading anything; try again.
		case errors.Is(err, syscall.EAGAIN):
			p.mark(gen)
			return false
		default:
			p.setErr(err)
			return true
		}
	}
}

// consume turns raw bytes into teed output and published lines.
func (p *pipeReader) consume(b []byte) {
	// The tee is a byte-for-byte copy of what the child wrote, so `docker
	// logs` is unchanged by the init being in the tree. Annotation is the
	// documented exception: it rewrites the stream per line, which is why it
	// is opt-in.
	if p.tee != nil && !p.annotate {
		_, _ = p.tee.Write(b)
	}

	p.pending = append(p.pending, b...)
	off := 0
	for {
		i := bytes.IndexByte(p.pending[off:], '\n')
		if i < 0 {
			break
		}
		line := p.pending[off : off+i]
		if p.dropping {
			p.dropping = false
		} else {
			p.emit(line, true)
		}
		off += i + 1
	}
	p.pending = append(p.pending[:0], p.pending[off:]...)

	if p.dropping {
		p.pending = p.pending[:0]
		return
	}
	if len(p.pending) > p.maxLine {
		p.emit(p.pending[:p.maxLine], false)
		p.pending = p.pending[:0]
		p.dropping = true
	}
}

// emit publishes one line and, when annotating, writes the annotated copy to
// the tee. complete is false for a line cut short at maxLine.
func (p *pipeReader) emit(line []byte, complete bool) {
	msg := string(bytes.TrimRight(line, "\r"))
	seq := p.publish(p.src, msg)

	p.mu.Lock()
	p.lastSeq = seq
	p.lines++
	p.mu.Unlock()

	if p.tee != nil && p.annotate {
		var out []byte
		if prefix := p.annotation(); prefix != "" {
			out = append(out, prefix...)
		}
		out = append(out, line...)
		if complete {
			out = append(out, '\n')
		}
		_, _ = p.tee.Write(out)
	}
}

// flushPartial publishes a trailing line that never got its newline, which is
// what a runtime that writes and then exits leaves behind.
func (p *pipeReader) flushPartial() {
	if len(p.pending) == 0 || p.dropping {
		p.pending = p.pending[:0]
		return
	}
	p.emit(p.pending, false)
	p.pending = p.pending[:0]
}

func (p *pipeReader) generation() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pokeGen
}

// mark records that every byte in the pipe as of the start of this attempt has
// been read and published, satisfying any drain request of generation gen or
// lower.
func (p *pipeReader) mark(gen uint64) {
	p.mu.Lock()
	if gen > p.satisfied {
		p.satisfied = gen
	}
	p.drainSeq = p.lastSeq
	p.cond.Broadcast()
	p.mu.Unlock()
}

func (p *pipeReader) setErr(err error) {
	p.mu.Lock()
	if p.err == nil && err != nil {
		p.err = err
	}
	p.mu.Unlock()
}

func (p *pipeReader) finish() {
	p.flushPartial()
	p.mu.Lock()
	p.finished = true
	p.drainSeq = p.lastSeq
	p.cond.Broadcast()
	p.mu.Unlock()
}

// drain blocks until this reader has emptied the pipe at least once since the
// call, and returns the highest seq it had published at that moment. A finished
// reader returns immediately: there is nothing left to arrive.
func (p *pipeReader) drain(ctx context.Context) uint64 {
	p.mu.Lock()
	if p.finished {
		seq := p.drainSeq
		p.mu.Unlock()
		return seq
	}
	p.pokeGen++
	want := p.pokeGen
	p.mu.Unlock()

	// Wake the reader if it is parked in the poller.
	_ = p.f.SetReadDeadline(time.Now())

	stop := context.AfterFunc(ctx, func() {
		p.mu.Lock()
		p.cond.Broadcast()
		p.mu.Unlock()
	})
	defer stop()

	p.mu.Lock()
	defer p.mu.Unlock()
	for p.satisfied < want && !p.finished && p.err == nil && ctx.Err() == nil {
		p.cond.Wait()
	}
	return p.drainSeq
}

// drainer fans a drain request out over every pipe the init owns.
type drainer struct {
	mu      sync.Mutex
	readers []*pipeReader
}

func (d *drainer) add(readers ...*pipeReader) {
	d.mu.Lock()
	d.readers = append(d.readers, readers...)
	d.mu.Unlock()
}

// drain blocks until every pipe has been emptied since the call and returns the
// highest seq any of them had published at its mark. That is the number the
// proxy puts in X-Overcast-Log-Seq: every frame at or below it was published
// before the runtime's response was forwarded.
func (d *drainer) drain(ctx context.Context) uint64 {
	d.mu.Lock()
	readers := make([]*pipeReader, len(d.readers))
	copy(readers, d.readers)
	d.mu.Unlock()

	seqs := make([]uint64, len(readers))
	var wg sync.WaitGroup
	for i, r := range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			seqs[i] = r.drain(ctx)
		}()
	}
	wg.Wait()

	var highest uint64
	for _, s := range seqs {
		if s > highest {
			highest = s
		}
	}
	return highest
}
