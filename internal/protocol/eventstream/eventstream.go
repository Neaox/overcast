package eventstream

import (
	"encoding/binary"
	"hash/crc32"
	"io"
)

// ContentType is the MIME type for AWS binary event stream responses.
const ContentType = "application/vnd.amazon.eventstream"

// Header is a string-valued AWS event-stream header.
type Header struct {
	Name  string
	Value string
}

// WriteMessage encodes and writes a single AWS event-stream message.
func WriteMessage(w io.Writer, headers []Header, payload []byte) error {
	_, err := w.Write(EncodeMessage(headers, payload))
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
