package dataplane

// VPCNetworkProblem is a VPC whose Docker network does not carry the isolation
// its internet-gateway state calls for, and could not be made to — or one whose
// containers could not rejoin the network after it was recreated. The EC2
// service records them; the router reports the set as a health advisory. It
// lives here rather than in either package so the router's advisory rules
// stay off the concrete EC2 service.
type VPCNetworkProblem struct {
	VpcID     string
	NetworkID string
	Detail    string
}
