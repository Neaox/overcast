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
	"bytes"
	"encoding/binary"
	"io"
	"time"
)

// frameHeaderLen is the size of the header in front of every frame payload.
const frameHeaderLen = 8

// maxLogTimestampLen bounds the "<RFC3339Nano> " prefix Docker writes when the
// logs endpoint is called with timestamps=true. The longest form it emits is a
// numeric offset with nanosecond precision — "2006-01-02T15:04:05.999999999+07:00"
// — at 35 bytes, plus the separating space.
const maxLogTimestampLen = 36

// logTimestampPrefixLen returns the length of the "<RFC3339Nano> " prefix at the
// head of b, or 0 when b does not begin with one. The shortest stamp Docker can
// write is "2006-01-02T15:04:05Z", so a separator before byte 20 rules one out
// before the parse is paid for.
func logTimestampPrefixLen(b []byte) int {
	sp := bytes.IndexByte(b, ' ')
	if sp < 20 || sp >= maxLogTimestampLen {
		return 0
	}
	if _, err := time.Parse(time.RFC3339Nano, string(b[:sp])); err != nil {
		return 0
	}
	return sp + 1
}

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

	// dropContinuationStamps and the two fields under it are inert unless the
	// reader came from NewLogDemuxReader; see there for what they are for.
	dropContinuationStamps bool
	atLineStart            bool
	frameStart             bool
	stamp                  [maxLogTimestampLen]byte
	stampErr               error // read error held back until stamp bytes drain
}

// NewDemuxReader wraps r, which may be a framed stream or a TTY one.
func NewDemuxReader(r io.Reader) *DemuxReader { return &DemuxReader{r: r} }

// NewLogDemuxReader wraps r like NewDemuxReader and additionally drops the
// timestamp Docker repeats at the head of every *continuation* frame. Use it
// for the logs endpoints, which Overcast calls with timestamps=true; use
// NewDemuxReader anywhere timestamps were not asked for.
//
// Docker splits a log message longer than 16 KiB into one frame per chunk and
// stamps each frame rather than each message. Reassembling the payloads — which
// is the whole job of a demux reader, since one line is not one frame — then
// splices those stamps into the middle of the line, mid-token, wherever the
// chunk boundary fell. A serialized error object comes back with an
// RFC3339Nano timestamp cutting a number in half.
//
// A stamp is Docker's own only when it opens a frame that continues a line
// already in progress; one that opens a new line is that message's timestamp,
// and the caller needs it to date the log event. So the two are told apart by
// whether the last byte emitted was a newline, which is exactly the difference
// between a message and the rest of a message.
func NewLogDemuxReader(r io.Reader) *DemuxReader {
	return &DemuxReader{r: r, dropContinuationStamps: true, atLineStart: true}
}

func (d *DemuxReader) Read(p []byte) (int, error) {
	for {
		if len(d.pending) > 0 {
			n := copy(p, d.pending)
			d.pending = d.pending[n:]
			d.noteEmitted(p[:n])
			return n, nil
		}
		if d.stampErr != nil {
			err := d.stampErr
			d.stampErr = nil
			return 0, err
		}
		if d.unframed {
			n, err := d.r.Read(p)
			d.noteEmitted(p[:n])
			return n, err
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
				break
			}
			d.remaining = binary.BigEndian.Uint32(d.hdr[4:8])
			d.frameStart = true
			// A zero-length frame carries nothing; go round for the next header.
		}
		if len(d.pending) > 0 || d.unframed {
			continue // emitted on the next turn, through the paths above
		}
		if d.frameStart {
			d.frameStart = false
			if d.dropContinuationStamps && !d.atLineStart {
				if err := d.skipContinuationStamp(); err != nil {
					return 0, err
				}
				continue
			}
		}
		if uint64(len(p)) > uint64(d.remaining) {
			p = p[:d.remaining]
		}
		n, err := d.r.Read(p)
		d.remaining -= uint32(n)
		d.noteEmitted(p[:n])
		return n, err
	}
}

// skipContinuationStamp consumes the head of the current frame and discards the
// timestamp prefix if there is one. Whatever is left — all of it, when the
// payload turned out to be the container's own bytes — is queued as pending, so
// nothing is lost either way.
//
// An error that arrives with bytes still in hand is held in stampErr rather
// than returned, because returning it now would drop those bytes: they are the
// newest output of a stream that is ending, which is the part someone reading
// the log wants most. The next Read surfaces it once pending is empty.
func (d *DemuxReader) skipContinuationStamp() error {
	want := min(int(d.remaining), maxLogTimestampLen)
	head := d.stamp[:want]
	n, err := io.ReadFull(d.r, head)
	d.remaining -= uint32(n)
	head = head[:n]
	d.pending = head[logTimestampPrefixLen(head):]
	if err == io.ErrUnexpectedEOF {
		// A frame that ended early is still a frame; the next header read is
		// where the stream's end gets reported.
		err = nil
	}
	if err != nil && len(d.pending) > 0 {
		d.stampErr = err
		return nil
	}
	return err
}

// noteEmitted tracks whether the next byte out starts a line, which is what
// separates a message's own timestamp from a continuation frame's.
func (d *DemuxReader) noteEmitted(b []byte) {
	if !d.dropContinuationStamps || len(b) == 0 {
		return
	}
	d.atLineStart = b[len(b)-1] == '\n'
}
