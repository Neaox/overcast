package dataplane

// VPCNetworkProblem is a VPC whose Docker network does not carry the isolation
// its internet-gateway state calls for, and could not be made to — or one whose
// containers could not rejoin the network after it was recreated — or one that
// has no network at all because the daemon refused to create it. The EC2
// service records them; the router reports the set as health advisories. It
// lives here rather than in either package so the router's advisory rules
// stay off the concrete EC2 service.
type VPCNetworkProblem struct {
	VpcID     string
	NetworkID string
	Detail    string
	// Unbacked marks a VPC with no Docker network because the daemon refused
	// to create one — an address pool another network already holds, most
	// often. Nothing can be placed in such a VPC, which is a different
	// advisory from a network that exists in the wrong state.
	Unbacked bool
}
