package docker

// logframe.go — Docker's multiplexed stdout/stderr framing.
//
// A container or exec created without a TTY has its two output streams
// multiplexed onto one connection, each write prefixed by an 8-byte header:
//
//	[1 byte stream type][3 bytes zero padding][4 bytes big-endian payload length][payload]
//
// The stream type is 0 (stdin), 1 (stdout) or 2 (stderr). With a TTY there is
// no header at all — a terminal has one channel to write to, so the stream is
// the process's raw output. Both shapes arrive from the same endpoints, so
// everything that reads container output has to tell them apart; this file is
// the one place that does, for every service.
//
// Reference: https://docs.docker.com/engine/api/v1.45/#tag/Container/operation/ContainerAttach

import (
	"encoding/binary"
	"io"
)

// frameHeaderLen is the size of the header in front of every frame payload.
const frameHeaderLen = 8

// isFrameHeader reports whether b starts with something shaped like a frame
// header: a stream type Docker emits, followed by the three bytes it zeroes.
// The padding is what makes the test worth anything — output beginning with a
// byte under 3 is unremarkable, output beginning with one followed by three
// NULs is not.
func isFrameHeader(b []byte) bool {
	return len(b) >= frameHeaderLen && b[0] <= 2 && b[1] == 0 && b[2] == 0 && b[3] == 0
}

// DemuxStream returns the payload of a multiplexed Docker stream with the frame
// headers removed, or raw unchanged when raw is not framed at all.
//
// The declared payload length is only ever trusted as far as the bytes that are
// actually present. Callers bound their reads — ContainerLogs stops at 64 KiB —
// so the final frame of a long log routinely arrives cut short, and those are
// the newest lines, the ones someone opened the log to read. A remainder too
// short to hold a header is the other half of that cut: binary junk rather than
// output, so it is dropped instead of printed.
//
// A header that does not validate part-way through ends the walk and the rest
// is returned as it is. A stream that desynchronised is still the container's
// own bytes, and swallowing them would lose exactly what is being looked for.
//
// Use DemuxReader instead when the source is an io.Reader — it does the same
// job without holding the whole stream in memory.
func DemuxStream(raw []byte) []byte {
	if !isFrameHeader(raw) {
		return raw
	}
	out := make([]byte, 0, len(raw))
	for len(raw) >= frameHeaderLen {
		if !isFrameHeader(raw) {
			return append(out, raw...)
		}
		size := int(binary.BigEndian.Uint32(raw[4:8]))
		raw = raw[frameHeaderLen:]
		if size > len(raw) {
			size = len(raw)
		}
		out = append(out, raw[:size]...)
		raw = raw[size:]
	}
	return out
}

// DemuxReader strips Docker's frame headers from a multiplexed stream as it is
// read, so a bufio.Scanner or bufio.Reader on top of it sees the container's
// own bytes and can assemble lines across frame boundaries — which it has to,
// because one line is not one frame: a JSON record from a structured logger
// routinely spans several.
//
// Reads are served from the caller's own buffer and the type allocates nothing
// per Read; every line of every Lambda invocation goes through here.
//
// An unframed (TTY) stream is recognised at the first header-sized read and
// copied through verbatim from then on.
type DemuxReader struct {
	r         io.Reader
	remaining uint32 // payload bytes left in the current frame
	hdr       [frameHeaderLen]byte
	pending   []byte // unconsumed bytes of hdr, once the stream proved unframed
	unframed  bool
}

// NewDemuxReader wraps r, which may be a framed stream or a TTY one.
func NewDemuxReader(r io.Reader) *DemuxReader { return &DemuxReader{r: r} }

func (d *DemuxReader) Read(p []byte) (int, error) {
	if len(d.pending) > 0 {
		n := copy(p, d.pending)
		d.pending = d.pending[n:]
		return n, nil
	}
	if d.unframed {
		return d.r.Read(p)
	}
	for d.remaining == 0 {
		// A short read here is a header the stream ended in the middle of.
		// io.ReadFull says so with io.ErrUnexpectedEOF, and reporting that
		// beats handing the caller half a header as though it were output.
		if _, err := io.ReadFull(d.r, d.hdr[:]); err != nil {
			return 0, err
		}
		if !isFrameHeader(d.hdr[:]) {
			// A TTY was allocated, so there is no framing: these 8 bytes are
			// already output, and so is every byte after them.
			d.unframed = true
			d.pending = d.hdr[:]
			n := copy(p, d.pending)
			d.pending = d.pending[n:]
			return n, nil
		}
		d.remaining = binary.BigEndian.Uint32(d.hdr[4:8])
		// A zero-length frame carries nothing; go round for the next header.
	}
	if uint64(len(p)) > uint64(d.remaining) {
		p = p[:d.remaining]
	}
	n, err := d.r.Read(p)
	d.remaining -= uint32(n)
	return n, err
}
