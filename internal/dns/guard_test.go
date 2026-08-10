package dns

import (
	"context"
	"net/netip"
	"testing"
)

// fakeGuard refuses the names it was given, and records what it was asked.
type fakeGuard struct {
	refuse map[string]bool
	asked  []string
}

func (f *fakeGuard) Refuse(_ context.Context, name string, _ netip.Addr) bool {
	f.asked = append(f.asked, name)
	return f.refuse[name]
}

// A data-plane name that reaches this resolver has already missed Docker's
// embedded resolver, so answering it with Overcast's address connects the client
// to the emulator on the engine's port, where nothing speaks its protocol, and
// it waits for its own timeout. REFUSED fails immediately instead.
func TestRespond_guardRefusesAnUnreachableDataPlaneName(t *testing.T) {
	zone := NewZone(netip.MustParseAddr("172.18.0.2"), "localhost.overcast.sh")
	srv := NewServer(":0", zone, nil).WithLocator(fixedLocator{addr: netip.MustParseAddr("172.18.0.2")})
	srv.SetGuard(&fakeGuard{refuse: map[string]bool{
		"cache-1.us-east-1.cfg.localhost.overcast.sh": true,
	}})

	reply := queryFor(t, srv, "cache-1.us-east-1.cfg.localhost.overcast.sh", netip.MustParseAddr("172.18.0.9"))

	if got := rcodeOf(reply); got != rcodeRefused {
		t.Fatalf("rcode = %d, want REFUSED (%d)", got, rcodeRefused)
	}
}

// Everything else the zone owns keeps its answer. The guard is consulted for
// every owned name, so a guard that over-refuses would take virtual-hosted S3
// and every host-routed service down with it.
func TestRespond_guardLeavesOtherOwnedNamesAlone(t *testing.T) {
	zone := NewZone(netip.MustParseAddr("172.18.0.2"), "localhost.overcast.sh")
	guard := &fakeGuard{refuse: map[string]bool{"db.us-east-1.rds.localhost.overcast.sh": true}}
	srv := NewServer(":0", zone, nil).WithLocator(fixedLocator{addr: netip.MustParseAddr("172.18.0.2")})
	srv.SetGuard(guard)

	reply := queryFor(t, srv, "mybucket.s3.localhost.overcast.sh", netip.MustParseAddr("172.18.0.9"))

	if got := rcodeOf(reply); got != rcodeNoError {
		t.Fatalf("rcode = %d, want NOERROR (%d)", got, rcodeNoError)
	}
	if len(guard.asked) != 1 || guard.asked[0] != "mybucket.s3.localhost.overcast.sh" {
		t.Fatalf("guard asked %#v, want the queried name once", guard.asked)
	}
}

// The host reaches these engines through published ports on loopback, never by
// container name, so a refusal there would break a path that works. The guard
// is not even consulted.
func TestRespond_guardIsNotConsultedForHostCallers(t *testing.T) {
	zone := NewZone(netip.MustParseAddr("127.0.0.1"), "localhost.overcast.sh")
	guard := &fakeGuard{refuse: map[string]bool{"db.us-east-1.rds.localhost.overcast.sh": true}}
	srv := NewServer(":0", zone, nil).WithLocator(fixedLocator{addr: netip.MustParseAddr("172.18.0.2")})
	srv.SetGuard(guard)

	reply := queryFor(t, srv, "db.us-east-1.rds.localhost.overcast.sh", netip.MustParseAddr("127.0.0.1"))

	if got := rcodeOf(reply); got != rcodeNoError {
		t.Fatalf("rcode = %d, want NOERROR (%d)", got, rcodeNoError)
	}
	if len(guard.asked) != 0 {
		t.Fatalf("guard was asked %#v for a loopback caller", guard.asked)
	}
}

// With no guard the server behaves exactly as it did before the hook existed,
// which is what a deployment with no Docker daemon gets.
func TestRespond_withoutAGuardEveryOwnedNameIsAnswered(t *testing.T) {
	zone := NewZone(netip.MustParseAddr("172.18.0.2"), "localhost.overcast.sh")
	srv := NewServer(":0", zone, nil).WithLocator(fixedLocator{addr: netip.MustParseAddr("172.18.0.2")})

	reply := queryFor(t, srv, "db.us-east-1.rds.localhost.overcast.sh", netip.MustParseAddr("172.18.0.9"))

	if got := rcodeOf(reply); got != rcodeNoError {
		t.Fatalf("rcode = %d, want NOERROR (%d)", got, rcodeNoError)
	}
}

// SetGuard runs after Serve has started, because the resolver binds before the
// Docker client the guard reads from exists. Storing it must be race-free; the
// race detector is what actually checks this, under -race.
func TestSetGuard_isSafeWhileServing(t *testing.T) {
	zone := NewZone(netip.MustParseAddr("172.18.0.2"), "localhost.overcast.sh")
	srv := NewServer(":0", zone, nil).WithLocator(fixedLocator{addr: netip.MustParseAddr("172.18.0.2")})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 50 {
			queryFor(t, srv, "db.us-east-1.rds.localhost.overcast.sh", netip.MustParseAddr("172.18.0.9"))
		}
	}()
	for range 50 {
		srv.SetGuard(&fakeGuard{})
		srv.SetGuard(nil)
	}
	<-done
}

// queryFor builds an A query for name and returns the server's reply bytes.
func queryFor(t *testing.T, srv *Server, name string, peer netip.Addr) []byte {
	t.Helper()
	query := buildQuery(t, name, typeA)
	reply, forward := srv.respond(context.Background(), query, nil, peer)
	if forward {
		t.Fatalf("query for %q was forwarded; the zone should own it", name)
	}
	if len(reply) < headerLen {
		t.Fatalf("query for %q produced no reply", name)
	}
	return reply
}

func rcodeOf(reply []byte) byte { return reply[3] & 0x0f }
