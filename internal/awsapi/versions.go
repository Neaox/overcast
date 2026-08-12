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

// queryVersionOwners names the Overcast service that answers each version
// above. It is the same statement each service makes in its own OwnsVersion —
// rds.OwnsVersion is `version == awsapi.VersionRDS` — read from the one place
// both can share, so the registry resolves a Query claim the same way the
// router routes it.
//
// It exists because AWS gives a shared model to a family of services. Every one
// of the 71 collisions in the generated query index is DocumentDB, Neptune and
// RDS sharing 2014-10-31 and most of its action names, and of the three
// Overcast implements only RDS — so the models cannot attribute the action and
// Overcast can. Without this an unsigned `Action=DescribeDBInstances` was
// classified as the S3 fallback, and IAM-authorised as an `s3:` action.
//
// Only the RDS entry is load-bearing today; the rest are the same fact about
// versions that do not currently collide, and are here so that a model refresh
// which introduces a collision resolves without anyone noticing it had to.
// resolveCollision honours an entry only when the named service is one of the
// modeled services that actually collided, so a stale entry cannot claim an
// action its service never declared.
var queryVersionOwners = map[string]string{
	VersionCloudFormation: "cloudformation",
	VersionCloudWatch:     "cloudwatch",
	VersionEC2:            "ec2",
	VersionRDS:            "rds",
	VersionElastiCache:    "elasticache",
	VersionELBv2:          "elbv2",
	VersionAutoScaling:    "autoscaling",
}
