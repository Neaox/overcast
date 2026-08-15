package msk

// probe.go — proving a Kafka broker is serving, not merely that a port accepts.
//
// The health check here used to be a bare `net.DialTimeout`, which is not a
// readiness signal for the reason efs/live_nfs.go had already written down:
// Docker's published port accepts connections as soon as the port proxy is up,
// well before Redpanda has bound its Kafka listener. A cluster promoted to
// ACTIVE off a successful dial was reporting the proxy, and the producer that
// connected next got a broker that was not there.
//
// What replaces it is ApiVersions, the request every Kafka client sends first
// and the only one a broker will answer before any handshake has happened. It
// is the cheapest thing on the wire that can only be answered by something
// speaking the Kafka protocol.
//
// Speaking the protocol directly rather than `docker exec`-ing `rpk` (the way
// rds/health.go runs pg_isready) is the same call probe.go makes in
// ElastiCache: this health check also runs from the reconcile path with Docker
// in states where exec is not dependable, `rpk` is Redpanda's own tool rather
// than Kafka's so a different image would not have it, and ApiVersions is
// exactly what `rpk cluster info` would have put on the wire anyway. Thirty
// lines of encoding buys independence from the image's contents.

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

const (
	// brokerProbeTimeout bounds one probe: connect, write ApiVersions, read the
	// reply. Loopback to a running broker answers in microseconds; the timeout
	// is only ever spent on a container that accepted and then said nothing.
	brokerProbeTimeout = 2 * time.Second

	// apiVersionsKey is the Kafka API key for ApiVersions.
	apiVersionsKey = 18
	// maxProbeResponse caps how much of a reply is read. An ApiVersions v0
	// response lists every API the broker supports and is a few hundred bytes;
	// anything far larger is not one, and a wrong-protocol server must not be
	// able to make the probe allocate.
	maxProbeResponse = 1 << 16
)

// probeBroker reports whether the Kafka broker at addr is serving. A nil error
// means it answered ApiVersions; any error is the evidence for why the attempt
// did not count.
func probeBroker(ctx context.Context, addr string) error {
	dialer := net.Dialer{Timeout: brokerProbeTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close() //nolint:errcheck // read-only probe connection

	// time.Now rather than the injected clock: a socket deadline is enforced by
	// the kernel against the wall clock and a mock cannot move it. Nothing
	// about the retry policy is timed here — that lives on the scheduler's
	// clock, in readiness.Watch.
	if err := conn.SetDeadline(time.Now().Add(brokerProbeTimeout)); err != nil {
		return fmt.Errorf("set deadline: %w", err)
	}

	const correlationID int32 = 0x0efc0a57 // any value; the reply must echo it
	if _, err := conn.Write(apiVersionsRequest(correlationID)); err != nil {
		return fmt.Errorf("write ApiVersions to %s: %w", addr, err)
	}

	size := make([]byte, 4)
	if _, err := io.ReadFull(conn, size); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return fmt.Errorf("%s accepted the connection but never answered ApiVersions: %w", addr, err)
		}
		return fmt.Errorf("read the response size from %s: %w", addr, err)
	}
	n := binary.BigEndian.Uint32(size)
	// A response must carry at least the correlation ID and an error code.
	if n < 6 || n > maxProbeResponse {
		return fmt.Errorf("%s answered with a %d-byte frame, which is not a Kafka response", addr, n)
	}

	body := make([]byte, n)
	if _, err := io.ReadFull(conn, body); err != nil {
		return fmt.Errorf("read the response body from %s: %w", addr, err)
	}
	if got := int32(binary.BigEndian.Uint32(body[0:4])); got != correlationID { //nolint:gosec // fixed-width protocol field
		return fmt.Errorf("%s echoed correlation ID %d, not the %d that was sent", addr, got, correlationID)
	}

	// Kafka's error codes are int16 and 0 is NONE. UNSUPPORTED_VERSION (35) is
	// the one non-zero code that still proves a broker answered — a broker too
	// new for v0 replies with it rather than closing the connection — and every
	// other code here would mean the broker is refusing to serve, which is the
	// thing being tested for.
	switch code := int16(binary.BigEndian.Uint16(body[4:6])); code { //nolint:gosec // fixed-width protocol field
	case 0, 35:
		return nil
	default:
		return fmt.Errorf("%s answered ApiVersions with Kafka error code %d", addr, code)
	}
}

// apiVersionsRequest encodes an ApiVersions v0 request: a size-prefixed frame
// carrying a v1 request header (api_key, api_version, correlation_id,
// client_id) and an empty body. v0 is deliberate — it is the version every
// broker back to Kafka 0.10 answers, and asking for a newer one gains the probe
// nothing it does not already have.
func apiVersionsRequest(correlationID int32) []byte {
	const clientID = "overcast-readiness"

	body := make([]byte, 0, 12+len(clientID))
	body = binary.BigEndian.AppendUint16(body, apiVersionsKey)
	body = binary.BigEndian.AppendUint16(body, 0)                     // api_version
	body = binary.BigEndian.AppendUint32(body, uint32(correlationID)) //nolint:gosec // fixed-width protocol field
	body = binary.BigEndian.AppendUint16(body, uint16(len(clientID))) //nolint:gosec // bounded by the constant above
	body = append(body, clientID...)

	return append(binary.BigEndian.AppendUint32(nil, uint32(len(body))), body...) //nolint:gosec // bounded by the constant above
}
