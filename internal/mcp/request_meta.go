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
// This file reads that block and decides what it obliges the server to do. It
// is additive on purpose: a request with no `_meta` protocol version is a
// 2025-11-25 request and takes the handshake path unchanged. See
// docs/plans/mcp-2026-07-28-migration.md for the phases either side of this.
//
// # Why a request that describes itself skips the lifecycle gate
//
// The gate exists to refuse work before `initialize` has established what the
// client is. A modern request supplies that in the request, so there is nothing
// left to establish and nothing for the gate to check. The Go SDK draws the
// line in the same place: "if the request carries
// io.modelcontextprotocol/protocolVersion in its _meta field, it follows the
// new sessionless protocol. The initialization gate is skipped for such
// requests."
//
// Worth knowing, because it makes this cheaper than it looks: the handshake
// never fed anything either. handleInitialize writes clientCapabilities and
// negotiatedVersion and nothing ever reads them — the result hardcodes
// ProtocolVersion. Per-request metadata is not competing with a live negotiated
// value; it is the first time this server has had one at all.

import (
	"encoding/json"
	"net/http"
	"strings"
)

// requestMeta is one request's self-description, parsed from `params._meta`.
//
// A zero value means the request carried no protocol version, which is how a
// 2025-11-25 request looks and is what `modern` reports on.
type requestMeta struct {
	// modern is true when the request declared a protocol version, and is what
	// decides whether the handshake governs it.
	modern bool

	protocolVersion string
	clientInfo      map[string]any
	capabilities    map[string]any
	logLevel        string
}

// rawRequestMeta is the shape `_meta` is decoded through. Every field is
// optional here so that "absent" and "malformed" stay distinguishable —
// the difference decides between an unchecked legacy request and -32602.
type rawRequestMeta struct {
	Meta map[string]json.RawMessage `json:"_meta"`
}

// parseRequestMeta reads `params._meta` and reports what the request claims.
//
// Params that are absent or unparseable are not an error here: a great many
// requests legitimately carry no params, and a malformed body is caught by the
// handler that needs it. Only a request that names a protocol version is held
// to the modern rules.
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
		// No version: a legacy request, whatever else `_meta` carries. The
		// progressToken lives here too, so `_meta` alone means nothing.
		return out, nil
	}
	out.modern = true
	out.protocolVersion = version

	// From here the request has opted into the modern rules, so the fields the
	// revision marks required are required. "A request missing any required
	// field is malformed; the server MUST reject it with JSON-RPC error code
	// -32602 (Invalid params)."
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

	// Read but not yet honoured. 2026-07-28 moves the log level onto each
	// request and says a server "MUST NOT emit notifications/message for
	// requests that did not include this field" — which is a change to emission,
	// not to parsing, and belongs with the phase that reworks notification
	// delivery. Validating the shape here means a client sending it correctly
	// is not rejected in the meantime.
	level, err := metaString(raw.Meta, metaLogLevel)
	if err != nil {
		return out, err
	}
	out.logLevel = level

	return out, nil
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

// supportedProtocolVersions is what this server will answer to, newest first.
// Both eras are live during the migration; the older entry goes when the
// handshake does.
func supportedProtocolVersions() []string {
	return []string{ModernProtocolVersion, ProtocolVersion}
}

// validate checks a modern request's version against what this server speaks,
// and against the header carrying the same claim.
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
	if !m.modern {
		return &rpcError{
			Code: RPCInvalidParams,
			Message: "request must carry its protocol version in _meta." +
				metaProtocolVersion,
			Data: map[string]any{"supported": supportedProtocolVersions()},
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
		metaProtocolVersion: ModernProtocolVersion,
		metaClientInfo: map[string]any{
			"name":    clientName,
			"version": clientVersion,
		},
		metaClientCapabilities: map[string]any{},
	}
}
