package awsapi

// Protocol identifies the wire protocol declared by an AWS Smithy service model.
type Protocol string

const (
	ProtocolUnknown   Protocol = ""
	ProtocolAWSJSON10 Protocol = "awsJson1_0"
	ProtocolAWSJSON11 Protocol = "awsJson1_1"
	ProtocolAWSQuery  Protocol = "awsQuery"
	ProtocolEC2Query  Protocol = "ec2Query"
	ProtocolRESTJSON  Protocol = "restJson1"
	ProtocolRESTXML   Protocol = "restXml"
	ProtocolRPCV2CBOR Protocol = "rpcv2Cbor"
	ProtocolRPCV2JSON Protocol = "rpcv2Json"
)

// ProtocolSet retains every recognized protocol trait carried by a Smithy
// service. It is a compact build-time-generated bitset, not runtime model data.
type ProtocolSet uint16

const (
	ProtocolsAWSJSON10 ProtocolSet = 1 << iota
	ProtocolsAWSJSON11
	ProtocolsAWSQuery
	ProtocolsEC2Query
	ProtocolsRESTJSON
	ProtocolsRESTXML
	ProtocolsRPCV2CBOR
	ProtocolsRPCV2JSON
)

// Operation is static routing metadata generated from the pinned AWS Smithy models.
// It deliberately contains no request or response shapes: service packages remain
// responsible for implemented behavior. The generated corpus is private so it
// cannot be mutated outside this package.
type Operation struct {
	Service    string
	SDKID      string
	APIVersion string
	Name       string
	Protocol   Protocol
	Protocols  ProtocolSet
	// TargetPrefix is the X-Amz-Target service shape name, including its trailing
	// dot (for example "DynamoDB_20120810."). It is empty for non-AWS JSON APIs.
	TargetPrefix string
	HTTPMethod   string
	URI          string
}
