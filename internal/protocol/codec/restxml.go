package codec

import (
	"encoding/xml"
	"io"
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
)

// restXML implements Codec for the AWS REST-XML protocol (S3, CloudFront).
//
// Phase 0 scope:
//   - WriteResponse / WriteError: thin wrappers over protocol.WriteXML and
//     protocol.WriteXMLError. Wire bytes are byte-identical to legacy.
//   - Decode: best-effort XML body decode. REST-XML services lean heavily
//     on HTTP binding traits (path variables, headers, raw body bytes)
//     so the typical S3 handler bypasses this and reads raw bytes
//     directly. Services that opt into the typed dispatcher with this
//     codec are expected to use plain XML body shapes; everything else
//     stays on the bespoke path per docs/plans/smithy.md §10.
type restXML struct{}

// RESTXML is the singleton AWS REST-XML codec.
var RESTXML Codec = restXML{}

func (restXML) Name() string { return NameRESTXML }

func (restXML) Decode(r *http.Request, into any) *protocol.AWSError {
	if r.Body == nil {
		return nil
	}
	if into == nil {
		// Drain even when there's nothing to decode into, to keep
		// connection-reuse semantics consistent.
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		return nil
	}
	body, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if len(body) == 0 {
		return nil
	}
	if err := xml.Unmarshal(body, into); err != nil {
		return protocol.ErrInvalidArgument(
			"The request body could not be parsed as XML: " + err.Error(),
		)
	}
	return nil
}

func (restXML) WriteResponse(w http.ResponseWriter, r *http.Request, status int, v any) {
	if v == nil {
		protocol.WriteEmpty(w, r, status)
		return
	}
	protocol.WriteXML(w, r, status, v)
}

func (restXML) WriteError(w http.ResponseWriter, r *http.Request, aerr *protocol.AWSError) {
	protocol.WriteXMLError(w, r, aerr)
}
