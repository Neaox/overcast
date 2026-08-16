package mcp

// discover.go — `server/discover`, and the result vocabulary 2026-07-28 puts
// around every answer.
//
// # What it replaces
//
// `initialize` did two jobs: it told the client what the server was and could
// do, and it established a connection-scoped session. 2026-07-28 keeps the
// first and abolishes the second. `server/discover` is the part that survives —
// "lets a client query a server's supported protocol versions, capabilities,
// and identity before sending any other requests. Servers MUST implement it."
//
// # Why it answers before anything else does
//
// This is the era probe. A client that does not know whether it is talking to a
// handshake-based server or a stateless one sends this first and reads the
// answer: a DiscoverResult means modern, a "method not found" or a timeout
// means legacy and it should fall back to `initialize`. That only works if the
// method is answerable by a client that knows nothing yet — so it cannot
// require a completed lifecycle, and it cannot require the caller to declare a
// protocol version in order to ask which versions exist.
//
// It is still not a free pass. A client that *does* declare a version gets it
// validated like anywhere else; the refusal carries the supported list, which
// is the same thing discover would have told it.
//
// Unlike `initialize`, answering this confers no state. There is nothing to
// remember, which is the whole point of the revision.

import (
	"net/http"
)

const (
	// resultTypeComplete is the ordinary answer: this result is the result.
	// 2026-07-28 requires a resultType on every result, and reserves other
	// values for answers that are not final — `input_required` is the one MRTR
	// uses. Clients "MUST treat an absent resultType as complete" for
	// compatibility with older servers, but a method this revision defines
	// should say so rather than lean on that.
	resultTypeComplete = "complete"

	// metaServerInfo is where this revision puts what `initialize` returned as
	// `serverInfo`. Advisory only: the spec is explicit that it is self-reported
	// and "SHOULD NOT" change behaviour or inform security decisions.
	metaServerInfo = "io.modelcontextprotocol/serverInfo"

	// discoverTTLMs is how long a client may reuse this answer. The result is
	// derived from what is compiled into the binary — the supported versions and
	// the registered capabilities — so it cannot change under a running server,
	// and an hour is the spec's own example. Re-probing more often than that
	// would cost a round trip to learn nothing.
	discoverTTLMs = 3600000

	// cacheScopePublic marks an answer that is identical for every caller. This
	// one carries no per-client and no per-credential content, so a shared cache
	// cannot leak one client's view to another.
	cacheScopePublic = "public"
)

// cacheableListTTLMs is how long a client may reuse a list or a resource read.
//
// Deliberately far shorter than discover's hour: these answers genuinely
// change. A provider registering new tools moves `tools/list`, and a resource
// can change under any request. What keeps the number from mattering much is
// that the notifications are the real mechanism — a client that subscribes is
// told the moment a list changes, and the TTL is only the backstop for one that
// does not. A minute is short enough that an unsubscribed client is not working
// from a stale list for long, and long enough that a chatty one is not
// re-listing on every call.
const cacheableListTTLMs = 60000

// serverInfoBlock is what this server reports as its identity. Advisory: the
// spec is explicit that it is self-reported, unverified, and "SHOULD NOT" be
// used to change behaviour or inform security decisions.
func serverInfoBlock() map[string]string {
	return map[string]string{"name": "overcast-mcp", "version": "1.0.0"}
}

// stampResult adds what 2026-07-28 requires of every result: a resultType, and
// the server's identity in `_meta`.
//
// Applied centrally at the point results are written rather than at each of the
// dozens of sites that build one, so a new handler cannot forget it. Existing
// values win, so a result that has already said it is something other than
// `complete` — an MRTR `input_required`, when that arrives — is left alone.
//
// Only map-shaped results are stamped. The one typed result left is
// `ToolResult`, which every `tools/call` is answered with: a struct with a
// fixed set of JSON fields and nowhere to put a resultType. That is legal,
// because the revision defines an absent resultType as `complete` — but it is
// also what phase 5 has to change, since MRTR needs a `tools/call` to be able
// to say `input_required`.
func stampResult(payload any) any {
	result, ok := payload.(map[string]any)
	if !ok {
		return payload
	}
	if _, exists := result["resultType"]; !exists {
		result["resultType"] = resultTypeComplete
	}
	if _, exists := result["_meta"]; !exists {
		result["_meta"] = map[string]any{metaServerInfo: serverInfoBlock()}
	}
	return result
}

// markCacheable adds the ttlMs/cacheScope pair the revision requires on
// `tools/list`, `prompts/list`, `resources/list`, `resources/read` and
// `resources/templates/list`.
//
// The scope is public on all of them because none varies by caller: the spec
// requires that a list "MUST NOT vary per-connection", and nothing here varies
// by credential either, so one client's cached view cannot misrepresent
// another's.
func markCacheable(result map[string]any) map[string]any {
	if result == nil {
		return result
	}
	result["ttlMs"] = cacheableListTTLMs
	result["cacheScope"] = cacheScopePublic
	return result
}

// discoverResult is the shape `server/discover` returns.
//
// Written as an explicit map rather than a struct because the capability block
// is already assembled as one by defaultServerCapabilities, and mirroring it
// into a typed shape here would create a second definition of the same thing to
// keep in step.
func (s *Server) discoverResult() map[string]any {
	s.mu.RLock()
	caps := s.capabilities
	s.mu.RUnlock()

	return map[string]any{
		"resultType":        resultTypeComplete,
		"supportedVersions": supportedProtocolVersions(),
		"capabilities":      caps,
		"ttlMs":             discoverTTLMs,
		"cacheScope":        cacheScopePublic,
		"_meta":             map[string]any{metaServerInfo: serverInfoBlock()},
	}
}

// handleDiscover answers the probe.
//
// It takes the request only to keep the signature honest about what a handler
// is given; nothing here reads it, because nothing about the answer depends on
// who is asking.
func (s *Server) handleDiscover(w http.ResponseWriter, req jsonRPCRequest, _ *http.Request) {
	writeRPCResult(w, req.ID, s.discoverResult())
}
