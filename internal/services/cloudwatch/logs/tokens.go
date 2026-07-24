package logs

// Continuation-token codecs for GetLogEvents (pagination-plan.md G1) and
// FilterLogEvents (pagination-plan.md G6).
//
// Both operations' tokens are documented by AWS as opaque strings — clients
// must never parse them. Internally we use the same base64(JSON) idiom
// serviceutil.Paginate/H1 established (docs/plans/pagination-plan.md H1;
// docs/plans/storage-access-plan.md M1), just with our own payload shapes:
// neither operation's resume position is a flat-slice index (Paginate's
// cursor shape), since storage-access-plan.md A4 replaces the old
// materialize-everything reads with indexed range queries that resume from
// a (Timestamp, Seq) — or, for the group-wide FilterLogEvents case,
// (Timestamp, StreamName, Seq) — position instead.
//
// GetLogEvents additionally prefixes its token with "f/" or "b/" (AWS's
// documented nextForwardToken/nextBackwardToken shape —
// https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_GetLogEvents.html
// — real tokens are observed to start with "f/" or "b/" followed by an
// opaque payload) so the direction a token was issued for is recoverable
// without a side channel.

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// ---- GetLogEvents f/·b/· position tokens -----------------------------------

type logEventsTokenPayload struct {
	Timestamp int64 `json:"ts"`
	Seq       int64 `json:"sq"`
}

// encodeLogEventsToken builds a GetLogEvents nextForwardToken (forward=true)
// or nextBackwardToken (forward=false) for a resume position.
func encodeLogEventsToken(forward bool, ts, seq int64) string {
	payload := logEventsTokenPayload{Timestamp: ts, Seq: seq}
	b, _ := json.Marshal(payload) // fixed shape; Marshal cannot fail here
	encoded := base64.URLEncoding.EncodeToString(b)
	if forward {
		return "f/" + encoded
	}
	return "b/" + encoded
}

// decodeLogEventsToken decodes a GetLogEvents nextToken, returning the
// direction it was issued for and its (Timestamp, Seq) resume position.
// ok is false for anything that isn't validly one of ours (wrong prefix,
// truncated/garbled payload) — the caller maps that to GetLogEvents'
// documented InvalidParameterException rather than silently restarting the
// walk from page 1 (pagination-plan.md's H1/G3 silent-restart class).
func decodeLogEventsToken(token string) (forward bool, cur eventCursor, ok bool) {
	var encoded string
	switch {
	case strings.HasPrefix(token, "f/"):
		forward, encoded = true, token[len("f/"):]
	case strings.HasPrefix(token, "b/"):
		forward, encoded = false, token[len("b/"):]
	default:
		return false, eventCursor{}, false
	}
	if encoded == "" {
		return false, eventCursor{}, false
	}
	raw, err := base64.URLEncoding.DecodeString(encoded)
	if err != nil {
		return false, eventCursor{}, false
	}
	var payload logEventsTokenPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return false, eventCursor{}, false
	}
	return forward, eventCursor{Valid: true, Timestamp: payload.Timestamp, Seq: payload.Seq}, true
}

// ---- FilterLogEvents nextToken ----------------------------------------------

type filterEventsTokenPayload struct {
	Timestamp  int64  `json:"ts"`
	StreamName string `json:"sn"`
	Seq        int64  `json:"sq"`
}

// encodeFilterEventsToken builds a FilterLogEvents nextToken wrapping the
// (stream, ts, seq) resume position storage-access-plan.md A4 specifies.
func encodeFilterEventsToken(ts int64, streamName string, seq int64) string {
	payload := filterEventsTokenPayload{Timestamp: ts, StreamName: streamName, Seq: seq}
	b, _ := json.Marshal(payload)
	return base64.URLEncoding.EncodeToString(b)
}

// decodeFilterEventsToken decodes a FilterLogEvents nextToken. ok is false
// for a garbled/invalid token — the caller maps that to
// InvalidParameterException, matching GetLogEvents' error and consistent
// with this file's existing conventions (see errInvalidParameter in
// store.go) rather than the third-convention risk the pagination plan warns
// against.
func decodeFilterEventsToken(token string) (groupCursor, bool) {
	raw, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return groupCursor{}, false
	}
	var payload filterEventsTokenPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return groupCursor{}, false
	}
	return groupCursor{Valid: true, Timestamp: payload.Timestamp, StreamName: payload.StreamName, Seq: payload.Seq}, true
}
