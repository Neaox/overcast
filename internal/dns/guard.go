package dns

import (
	"context"
	"net/netip"
)

// Guard decides whether a name this zone owns should be answered with
// Overcast's address, or refused because it names a container the caller
// cannot reach.
//
// It exists because the two things this resolver is authoritative for overlap.
// Overcast owns every subdomain of the split-horizon domains, and the endpoint
// of a container Overcast started — an RDS instance, a cache node — is one of
// those subdomains. Docker's embedded resolver answers such a name from the
// container's network aliases and never forwards it here. So a data-plane name
// arriving at this server has *already* missed: the alias it needed is not on
// any network the caller is attached to.
//
// Answering that with Overcast's own address is the failure this package's doc
// comment describes: the client connects to the emulator on the database port
// and waits for a protocol nothing there speaks. A refusal fails immediately
// instead, and the implementation logs which resource and which caller, which
// is the half AWS itself cannot give you.
//
// Implementations must be safe for concurrent use and must not block for long:
// this runs on the query path. The cost is bounded by the fact above — only a
// query Docker could not answer ever reaches it.
type Guard interface {
	// Refuse reports whether the query for name from peer should be refused.
	//
	// name is the queried name without its trailing dot. Returning false means
	// "answer normally", which is the right answer for everything that is not a
	// positively identified, unreachable container endpoint — including names
	// that merely look like one.
	Refuse(ctx context.Context, name string, peer netip.Addr) bool
}

// SetGuard wires the guard consulted before a name this zone owns is answered,
// and may be called at any time — including after Serve has started.
//
// That matters because of startup order: the resolver binds before any service
// is constructed, so that cfg.DNSListening is settled before a container could
// be pointed at it, while the Docker client the guard reads from only exists
// once the supervisor has probed. The guard therefore arrives late, and it
// arrives on a server already answering queries.
//
// With no guard set — the default — every owned name is answered with
// Overcast's address, which is the behaviour that predates this hook.
func (s *Server) SetGuard(g Guard) {
	if g == nil {
		s.guard.Store(nil)
		return
	}
	s.guard.Store(&g)
}

// loadGuard returns the guard, or nil.
func (s *Server) loadGuard() Guard {
	if g := s.guard.Load(); g != nil {
		return *g
	}
	return nil
}

// refuseByGuard reports whether the guard rejects this query.
//
// Host-local callers are never refused. The guard's question is "does the
// caller share a plane with the container", and a caller that is not on a
// Docker network at all has no plane to share: the host reaches these engines
// through published ports on loopback, not by container name, so a refusal
// there would break a path that works.
func (s *Server) refuseByGuard(ctx context.Context, name string, peer netip.Addr) bool {
	g := s.loadGuard()
	if g == nil || !peer.IsValid() || peer.IsLoopback() {
		return false
	}
	return g.Refuse(ctx, name, peer)
}
