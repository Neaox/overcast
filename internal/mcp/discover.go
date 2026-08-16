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
		"_meta": map[string]any{
			metaServerInfo: map[string]string{
				"name":    "overcast-mcp",
				"version": "1.0.0",
			},
		},
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
