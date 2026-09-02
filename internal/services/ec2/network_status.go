package ec2

// network_status.go carries the verified state of each per-VPC network into
// /_overcast/health, beside the two planes docker.Probe reports at startup.
//
// It is separate from the ownership rules in handler_vpc_docker.go on purpose:
// those decide what this instance may *remove*, and this decides what it
// *reports*. A network it must not touch is still one an operator needs told
// about.

import (
	"github.com/overcast-sh/overcast/internal/docker"
)

// SetNetworkStatusSink wires the recorder that carries per-VPC network state
// into /_overcast/health.
//
// Optional: a nil sink is a service that reports nothing, which is what every
// test and every build without a health endpoint gets.
func (h *Handler) SetNetworkStatusSink(sink docker.NetworkStatusSink) {
	h.networkStatus = sink
}

// recordNetworkStatus publishes one VPC network's verified state.
//
// Every outcome is reported, not only the bad ones. A network that was checked
// and matched is the answer to "is my VPC network in the state Overcast thinks
// it is", and a health endpoint that lists only the broken ones cannot
// distinguish a healthy network from one nobody looked at.
func (h *Handler) recordNetworkStatus(status docker.NetworkStatus) {
	if h.networkStatus == nil || status.Name == "" {
		return
	}
	h.networkStatus.RecordNetworks([]docker.NetworkStatus{status})
}
