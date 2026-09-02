package ec2

// network_status.go carries the Docker facts this service owns but the startup
// probe never sees into /_overcast/health: its sweep-domain identity, and the
// verified state of each per-VPC network.
//
// It is separate from the ownership rules in handler_vpc_docker.go on purpose:
// those decide what this instance may *remove*, and this decides what it
// *reports*. A network it must not touch is still one an operator needs told
// about — and `overcast network reset`, which runs outside this process, cannot
// tell whose a network is without the identity published here.

import (
	"context"

	"github.com/overcast-sh/overcast/internal/docker"
)

// SetNetworkReporter wires the health tracker. Optional: a nil reporter is a
// service that reports nothing, which is what every test and every build
// without a health endpoint gets.
func (h *Handler) SetNetworkReporter(r docker.NetworkReporter) {
	h.networkReporter = r
}

// publishInstanceIdentity records this instance's sweep-domain identity, so a
// caller outside the process can tell this daemon's networks from a
// neighbour's. Safe to call repeatedly; the identity does not change.
func (h *Handler) publishInstanceIdentity(ctx context.Context) {
	if h.networkReporter == nil {
		return
	}
	if id := h.instanceDomain(ctx); id != "" {
		h.networkReporter.RecordInstance(id)
	}
}

// recordNetworkStatus publishes one VPC network's verified state.
//
// Every outcome is reported, not only the bad ones. A network that was checked
// and matched is the answer to "is my VPC network in the state Overcast thinks
// it is", and a health endpoint that lists only the broken ones cannot
// distinguish a healthy network from one nobody looked at.
func (h *Handler) recordNetworkStatus(status docker.NetworkStatus) {
	if h.networkReporter == nil || status.Name == "" {
		return
	}
	h.networkReporter.RecordNetworks([]docker.NetworkStatus{status})
}

// forgetNetworkStatus drops a VPC network from the report, for a network that
// no longer exists.
//
// The problem entry and the status entry are two separate records of the same
// network and both have to go with it: netProblems already did (see
// forgetVPCNetwork), and without this the network stays listed in
// /_overcast/health as though it were still there — with whatever drift it last
// carried, and therefore whatever advisory that drift raised, for the life of
// the daemon (#1583).
func (h *Handler) forgetNetworkStatus(name string) {
	if h.networkReporter == nil || name == "" {
		return
	}
	h.networkReporter.ForgetNetwork(name)
}
