// Package awsapi contains shared constants for the AWS Query protocol.
package awsapi

// AWS Query-protocol API versions. Each version uniquely identifies a service,
// which is how the router disambiguates Query-protocol requests that share
// action names across services (e.g. CloudFormation and SES both have GetTemplate).
//
// Source: https://docs.aws.amazon.com/general/latest/gr/aws-apis.html
const (
	VersionCloudFormation = "2010-05-15"
	VersionCloudWatch     = "2010-08-01"
	VersionEC2            = "2016-11-15"
	VersionRDS            = "2014-10-31"
	VersionElastiCache    = "2015-02-02"
	VersionELBv2          = "2015-12-01"
	VersionAutoScaling    = "2011-01-01"
)
