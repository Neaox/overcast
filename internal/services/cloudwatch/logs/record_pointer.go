package logs

// Event identity — FilterLogEvents' `eventId` and GetLogRecord's
// `logRecordPointer` are the same opaque value here.
//
// AWS documents FilteredLogEvent$eventId only as "The ID of the event", and
// logRecordPointer only as a value "you get ... from the response of a
// GetQueryResults operation" — so neither has a wire format a client may
// parse, exactly like the continuation tokens in tokens.go. On real AWS the
// pointer comes from a Logs Insights `@ptr`. Insights is not emulated here
// (StartQuery/GetQueryResults are 501), so FilterLogEvents mints the id in
// this encoding and GetLogRecord accepts it: that is what keeps the two
// operations linked, which is the point of returning the field at all (#1721).
//
// https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_FilteredLogEvent.html
// https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_GetLogRecord.html
//
// The payload identifies an event by facts that survive its journey out of the
// per-stream write buffer into the backend: group, stream, timestamp, and a
// digest of the message. The `Seq` a range read carries is deliberately NOT
// used — a buffered event's Seq is synthesised from its index in the unflushed
// buffer (see bufferedSeqBase) and is replaced by the backend's own on flush,
// so an id built on it would stop resolving about one debounce interval after
// it was handed out. Two byte-identical messages in the same stream and
// millisecond do share an id; they are indistinguishable in every field
// GetLogRecord returns, so resolving either to the first is not observable.

import (
	"encoding/base64"
	"encoding/json"
	"hash/fnv"
	"strconv"
)

// eventPointer is the decoded form of an eventId / logRecordPointer.
type eventPointer struct {
	Group      string `json:"g"`
	Stream     string `json:"s"`
	Timestamp  int64  `json:"t"`
	MessageSum string `json:"m"`
}

// messageDigest is the message discriminator carried in a pointer. FNV-1a is
// enough: it separates different messages that share a stream and a
// millisecond, and nothing here depends on it being unforgeable.
func messageDigest(message string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(message)) // hash.Hash never reports an error
	return strconv.FormatUint(h.Sum64(), 16)
}

// encodeEventID builds the opaque id FilterLogEvents returns for one event.
func encodeEventID(group, stream string, timestamp int64, message string) string {
	payload := eventPointer{
		Group:      group,
		Stream:     stream,
		Timestamp:  timestamp,
		MessageSum: messageDigest(message),
	}
	b, _ := json.Marshal(payload) // fixed shape; Marshal cannot fail here
	return base64.URLEncoding.EncodeToString(b)
}

// decodeEventID decodes an eventId / logRecordPointer. ok is false for
// anything that is not validly one of ours, which the caller reports as
// GetLogRecord's InvalidParameterException — a pointer that never named an
// event is a bad parameter, not a missing resource.
func decodeEventID(id string) (eventPointer, bool) {
	raw, err := base64.URLEncoding.DecodeString(id)
	if err != nil {
		return eventPointer{}, false
	}
	var payload eventPointer
	if err := json.Unmarshal(raw, &payload); err != nil {
		return eventPointer{}, false
	}
	if payload.Group == "" || payload.Stream == "" {
		return eventPointer{}, false
	}
	return payload, true
}
