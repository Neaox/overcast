package mcp

// request_meta.go — what a request says about itself.
//
// Revision 2026-07-28 makes MCP stateless. Instead of negotiating once with
// `initialize` and remembering the answer for the connection, every request
// carries its own protocol version, client identity and client capabilities in
// `_meta`:
//
//	"There is no negotiation handshake. Every request carries its protocol
//	 version, and the server accepts or rejects each request independently"
//
// This file reads that block and decides what it obliges the server to do. See
// docs/plans/mcp-2026-07-28-migration.md for the phases either side of it.
//
// # A request that does not describe itself is not one this server can serve
//
// This was additive while both eras were live: a request with no `_meta`
// protocol version was a 2025-11-25 request and took the handshake path. Phase
// 4 removed that path, so there is nothing for such a request to fall back to
// and `validate` refuses it. That is the whole of what "modern-only" means in
// code, and it is deliberate rather than incidental — the revision is explicit
// that "legacy clients have no fall-forward mechanism", so serving one would
// mean keeping the era it belongs to, not bending this one.

import (
	"encoding/json"
	"net/http"
	"strings"
)

// requestMeta is one request's self-description, parsed from `params._meta`.
//
// A zero value means the request named no protocol version, which is a request
// this server refuses.
type requestMeta struct {
	// versioned is true when the request declared a protocol version. It is
	// separate from protocolVersion being non-empty so that "said nothing" and
	// "said something wrong" stay different answers with different error codes.
	versioned bool

	protocolVersion string
	clientInfo      map[string]any
	capabilities    map[string]any

	// logLevel is the threshold this request asked to be told about, empty when
	// it asked for none — which the revision defines as "tell me nothing"
	// rather than as a default. Consumed by requestStream.wantsLog.
	logLevel string
}

// rawRequestMeta is the shape `_meta` is decoded through. Every field is
// optional here so that "absent" and "malformed" stay distinguishable — the
// difference decides between a request that named no version and one that named
// a bad one.
type rawRequestMeta struct {
	Meta map[string]json.RawMessage `json:"_meta"`
}

// parseRequestMeta reads `params._meta` and reports what the request claims.
//
// Params that are absent or unparseable are not an error here: a great many
// requests legitimately carry no params, and a malformed body is caught by the
// handler that needs it. What such a request does not do is name a version, and
// `validate` is where that is refused.
func parseRequestMeta(params json.RawMessage) (requestMeta, *rpcError) {
	var out requestMeta
	if len(params) == 0 {
		return out, nil
	}
	var raw rawRequestMeta
	if err := json.Unmarshal(params, &raw); err != nil || raw.Meta == nil {
		return out, nil
	}

	version, err := metaString(raw.Meta, metaProtocolVersion)
	if err != nil {
		return out, err
	}
	if version == "" {
		// No version, whatever else `_meta` carries — the progressToken lives
		// here too, so `_meta` alone means nothing. Reported rather than refused
		// here so the refusal has one home, in validate.
		return out, nil
	}
	out.versioned = true
	out.protocolVersion = version

	// The request has named a version, so the fields the revision marks required
	// are required. "A request missing any required field is malformed; the
	// server MUST reject it with JSON-RPC error code -32602 (Invalid params)."
	capsRaw, ok := raw.Meta[metaClientCapabilities]
	if !ok {
		return out, &rpcError{
			Code:       RPCInvalidParams,
			Message:    "_meta." + metaClientCapabilities + " is required on a " + version + " request",
			httpStatus: http.StatusBadRequest,
		}
	}
	if err := json.Unmarshal(capsRaw, &out.capabilities); err != nil {
		return out, &rpcError{
			Code:       RPCInvalidParams,
			Message:    "_meta." + metaClientCapabilities + " must be an object",
			httpStatus: http.StatusBadRequest,
		}
	}

	// clientInfo is SHOULD rather than MUST, and is advisory in any case: the
	// spec is explicit that it is self-reported, unverified, and must not
	// change behaviour or be relied on for security decisions. Read it for
	// logging, do not require it.
	if infoRaw, ok := raw.Meta[metaClientInfo]; ok {
		_ = json.Unmarshal(infoRaw, &out.clientInfo)
	}

	// The log threshold, which 2026-07-28 moves off the connection and onto each
	// request: "servers MUST NOT emit notifications/message for requests that
	// did not include this field". An empty value is therefore silence rather
	// than a default, and is carried through as one — see
	// requestStream.wantsLog, which is what reads this.
	level, err := metaString(raw.Meta, metaLogLevel)
	if err != nil {
		return out, err
	}
	out.logLevel = level

	return out, nil
}

// ClientRequestMeta is the `_meta` block a client inside this repository puts on
// every request it sends to an MCP server.
//
// Exported because the keys are not: a caller in another package that hand-rolls
// this block gets the names right by copying them, and then keeps whatever it
// copied when they change. The workspace probe in
// internal/mcp/providers/repo_provider.go sent no `_meta` at all and was
// answered -32602 by any conforming server, this one included (#1035).
//
// Both members it sets are required on every request: the protocol version, and
// the capabilities — an empty object being the honest answer for a client that
// supports no optional capabilities, rather than a member to leave out.
// clientInfo is a SHOULD and is filled in from the caller's own name.
func ClientRequestMeta(clientName, clientVersion string) map[string]any {
	return map[string]any{
		metaProtocolVersion:    ProtocolVersion,
		metaClientCapabilities: map[string]any{},
		metaClientInfo: map[string]any{
			"name":    clientName,
			"version": clientVersion,
		},
	}
}

// metaString reads one string-valued `_meta` key, distinguishing absent from
// present-but-not-a-string.
func metaString(meta map[string]json.RawMessage, key string) (string, *rpcError) {
	raw, ok := meta[key]
	if !ok {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", &rpcError{Code: RPCInvalidParams, Message: "_meta." + key + " must be a string"}
	}
	return strings.TrimSpace(value), nil
}

// supportedProtocolVersions is what this server will answer to.
//
// One entry, and a slice rather than the bare constant because that is the
// shape both its readers need: `server/discover` publishes a list, and -32022
// hands back the list to retry with. Listing a revision this server no longer
// implements would invite a client to send a request it would then be refused
// for, which is worse than telling it plainly there is one era here.
func supportedProtocolVersions() []string {
	return []string{ProtocolVersion}
}

// validate checks a request's version against what this server speaks, and
// against the header carrying the same claim.
//
// The two failures are deliberately different codes, because they are different
// mistakes. A version this server does not implement is -32022 and carries the
// list to retry with — the client is expected to pick from it. A header that
// contradicts the body is -32020: nothing is wrong with either value on its
// own, but an intermediary routing on the header and a server acting on the
// body would disagree about what they are handling, which is the security
// problem the header/body validation rule exists to prevent.
func (m requestMeta) validate(header string) *rpcError {
	// Every request carries its own version now, so one that does not is not a
	// request this server can serve. This also settles a case the revision
	// leaves open: a missing MCP-Protocol-Version header is -32020 and a missing
	// `_meta` protocol version is -32602, and it does not say which applies when
	// both are absent. The body wins, because the body is where protocol
	// metadata lives — "All protocol metadata travels in the message body … A
	// binding MAY additionally mirror selected body fields into envelope
	// metadata." A missing mirror is a lesser fault than a missing original, and
	// stdio has no mirror at all.
	//
	// `server/discover` is **not** exempt, though it reads as though it should
	// be. The revision marks `protocolVersion` and `clientCapabilities` required
	// on every request, gives `DiscoverRequest` the same `RequestParams` as
	// everything else, and grants no exemption anywhere — so a client probes by
	// naming the version it speaks and reading -32022's `supported` list when it
	// guesses wrong, not by declaring nothing. Discover exists to report
	// capabilities up front, and the blog for this revision is explicit that it
	// "is not required" at all.
	//
	// The 400 is required too, and was the bug (#1035): "A request missing any
	// required field is malformed; the server MUST reject it with JSON-RPC error
	// code -32602 (Invalid params). On HTTP, the response status MUST be 400 Bad
	// Request." The sibling refusal below in parseRequestMeta always set it;
	// this one did not, so a missing version came back -32602 inside a 200 and
	// an intermediary reading status alone saw a success.
	if !m.versioned {
		return &rpcError{
			Code: RPCInvalidParams,
			Message: "request must carry its protocol version in _meta." +
				metaProtocolVersion,
			Data:       map[string]any{"supported": supportedProtocolVersions()},
			httpStatus: http.StatusBadRequest,
		}
	}
	supported := supportedProtocolVersions()
	if !containsString(supported, m.protocolVersion) {
		return &rpcError{
			Code:    RPCUnsupportedProtocolVersion,
			Message: "unsupported protocol version",
			Data: map[string]any{
				"supported": supported,
				"requested": m.protocolVersion,
			},
		}
	}
	if header = strings.TrimSpace(header); header != "" && header != m.protocolVersion {
		return &rpcError{
			Code: RPCHeaderMismatch,
			Message: "header mismatch: MCP-Protocol-Version header value " + header +
				" does not match _meta." + metaProtocolVersion + " value " + m.protocolVersion,
		}
	}
	return nil
}

// Deliberately not here yet: a requireCapability helper returning -32021
// (RPCMissingRequiredClientCapability, whose status mapping does exist in
// httpStatusForRPCError). The revision says "A server MUST NOT rely on
// capabilities the client has not declared", but nothing this server does
// requires one, so the check would have no caller and no test — speculative
// behaviour is worse than none. It belongs with MRTR, the first feature that
// needs a declared capability. `capabilities` is parsed and carried here ready
// for it.

func containsString(haystack []string, needle string) bool {
	for _, candidate := range haystack {
		if candidate == needle {
			return true
		}
	}
	return false
}

// NewRequestMeta builds the `_meta` block a 2026-07-28 client puts on every
// request.
//
// Exported because overcast is a client of its own MCP surface as well as its
// server: `runtime_mcp_call` and the workspace probe in internal/mcp/providers
// talk to /_overcast/mcp over HTTP, and since the handshake was removed a
// request that does not carry this is refused like any other legacy one. The
// alternative was for the client side to hardcode the spec's `_meta` key names,
// which would put the same two strings in two packages and let them drift.
func NewRequestMeta(clientName, clientVersion string) map[string]any {
	return map[string]any{
		metaProtocolVersion: ProtocolVersion,
		metaClientInfo: map[string]any{
			"name":    clientName,
			"version": clientVersion,
		},
		metaClientCapabilities: map[string]any{},
	}
}
