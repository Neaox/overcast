package docker

// netlock.go serialises every remove-and-recreate of a Docker network in this
// process.
//
// Docker cannot change a network's isolation, driver, addressing or driver
// options in place, so every path that has to change one of them removes the
// network and creates it again. There are several such paths — startup
// verification (EnsureNetwork), the internet-gateway isolation flip, and
// `overcast network reset` — and they can run concurrently: two attach/detach
// calls on a shared network, or an API call racing a reconcile after the Docker
// watcher reconnects.
//
// Unserialised, they interleave badly and each failure looks like success:
//
//   - Two removes race. The loser gets 404, which RemoveNetwork reports as
//     success because a missing network is normally the outcome wanted — so the
//     loser goes on to create over the winner's freshly created network.
//   - Two creates race. The loser gets "already exists", which
//     CreateNetworkWithOptions resolves by returning the existing network — so
//     it reports a network created to its spec when it was created to another's.
//   - A record is rewritten with a network id that the other path has already
//     removed, leaving every resource pointing at nothing.
//
// **The key is the network name, not its id.** An id is exactly the thing a
// recreate changes, so a lock keyed by id stops protecting the network at the
// moment it matters most; the name is stable across the whole operation and is
// unique per daemon.
//
// Process-local. Two Overcast processes on one daemon are not serialised by
// this — that is what the ownership label is for (LabelInstance): they do not
// touch each other's networks at all.

import "sync"

// networkLocks holds one mutex per network name, created on first use and kept
// for the life of the process. The set is bounded by the number of networks
// Overcast manages, which is small, so nothing reclaims them — and reclaiming
// one would mean deleting a mutex somebody may be about to take.
var networkLocks sync.Map // network name → *sync.Mutex

// LockNetwork takes the process-wide lock for one network name and returns the
// function that releases it. Every caller that removes and recreates a network
// must hold it, and must re-read the network's state after acquiring it: what
// was true before the wait is not what the next call will act on.
//
// Exported because the callers are spread across packages — EnsureNetwork here,
// the EC2 isolation flip in internal/services/ec2, and the CLI's rebuild — and
// a second lock in another package would serialise nothing.
func LockNetwork(name string) (unlock func()) {
	v, _ := networkLocks.LoadOrStore(name, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
