package eventstream

import (
	"encoding/binary"
	"hash/crc32"
	"io"
)

// ContentType is the MIME type for AWS binary event stream responses.
const ContentType = "application/vnd.amazon.eventstream"

// JSONContentType is the payload content type of an event carrying a JSON
// document — the AWS JSON protocols' event streams and Lambda's terminal
// InvokeComplete event alike.
const JSONContentType = "application/json"

// InitialResponseEventType names the event that must open an event-stream
// response whose operation output has members outside the stream itself.
//
// It carries the operation's initial response document, and for the AWS JSON
// protocols it is the only place that document can travel — there is no other
// body. Client runtimes therefore block on it: smithy-go parks the operation's
// deserializer on the first frame and hands the caller its stream only once
// that frame has arrived and deserialized, so a response that opens with a
// domain event instead deadlocks the client rather than failing it (#1064).
// Write it first, before any domain event.
const InitialResponseEventType = "initial-response"

// Header is a string-valued AWS event-stream header.
type Header struct {
	Name  string
	Value string
}

// WriteEvent encodes and writes one AWS event-stream `event` message of the
// given event type, carrying a payload of the given content type.
func WriteEvent(w io.Writer, eventType, contentType string, payload []byte) error {
	_, err := w.Write(EncodeMessage([]Header{
		{Name: ":message-type", Value: "event"},
		{Name: ":event-type", Value: eventType},
		{Name: ":content-type", Value: contentType},
	}, payload))
	return err
}

// EncodeMessage returns one AWS event-stream binary message.
func EncodeMessage(headers []Header, payload []byte) []byte {
	var headerLen int
	for _, h := range headers {
		headerLen += 1 + len(h.Name) + 1 + 2 + len(h.Value)
	}

	headerBuf := make([]byte, 0, headerLen)
	for _, h := range headers {
		headerBuf = append(headerBuf, byte(len(h.Name)))
		headerBuf = append(headerBuf, []byte(h.Name)...)
		headerBuf = append(headerBuf, 7) // string type
		headerBuf = binary.BigEndian.AppendUint16(headerBuf, uint16(len(h.Value)))
		headerBuf = append(headerBuf, []byte(h.Value)...)
	}

	totalLen := uint32(12 + len(headerBuf) + len(payload) + 4)
	buf := make([]byte, 0, int(totalLen))
	buf = binary.BigEndian.AppendUint32(buf, totalLen)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(headerBuf)))
	buf = binary.BigEndian.AppendUint32(buf, crc32.ChecksumIEEE(buf[:8]))
	buf = append(buf, headerBuf...)
	buf = append(buf, payload...)
	buf = binary.BigEndian.AppendUint32(buf, crc32.ChecksumIEEE(buf))
	return buf
}
