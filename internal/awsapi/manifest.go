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
)

// Operation is static routing metadata generated from the pinned AWS Smithy models.
// It deliberately contains no request or response shapes: service packages remain
// responsible for implemented behavior.
type Operation struct {
	Service      string
	SDKID        string
	APIVersion   string
	Name         string
	Protocol     Protocol
	TargetPrefix string
	HTTPMethod   string
	URI          string
}
