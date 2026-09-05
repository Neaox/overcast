package cloudformation

// dynamic_refs_ssm_secure.go — where an {{resolve:ssm-secure:...}} reference
// may appear.
//
// Of the three reference schemes only ssm-secure carries a placement
// restriction. AWS: "Secure strings can only be used for resource properties
// that support the ssm-secure dynamic reference pattern." The secretsmanager
// scheme is explicitly unrestricted, and a plain ssm parameter is plaintext
// with nothing to protect — so this table is consulted for ssm-secure alone.
//
// Overcast resolved an ssm-secure reference in any property, which is the
// expensive direction of divergence: the template deploys locally and is
// rejected by AWS.
//
// Source: "Resources that support dynamic parameter patterns for secure
// strings", fetched 2026-09-05 from
// https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/dynamic-references-ssm-secure-strings.html
// Eleven rows, reproduced below verbatim in resource-type order.
//
// # How a property path is written
//
// A key is one segment; segments are joined with "/". The docs' "Property
// type" column names the CloudFormation property type that holds the
// property, which is reached through the identically named property on the
// resource — so AWS::IAM::User's LoginProfile/Password is the Password inside
// the resource's LoginProfile.
//
// A **list index is not a segment**. AWS::OpsWorks::Stack's RdsDbInstances is
// a list, and every element's DbPassword is equally allowed, so the entry is
// "RdsDbInstances/DbPassword" and the walk skips indices rather than writing
// "RdsDbInstances[0]/DbPassword" or a wildcard. That is also how AWS itself
// reports the path it refuses — "SSM Secure reference is not supported in:
// [AWS::ECS::TaskDefinition/Properties/ContainerDefinitions/Environment]",
// where both ContainerDefinitions and Environment are lists and neither is
// indexed.
//
// # Failure shape
//
// Real AWS rejects a disallowed reference synchronously, at CreateStack:
// "An error occurred (ValidationError) when calling the CreateStack
// operation: SSM Secure reference is not supported in: [<path>]". Overcast
// fails the containing **resource** instead, which rolls the stack back —
// the same shape as every other unresolvable reference here (see
// expandDynamicRefsInString), reached at the point the walk knows both the
// resource type and the property path. The message keeps AWS's wording so it
// is recognisable, and adds the reference itself, which AWS's does not carry.
// A refused reference is never resolved, so the secure string is not
// decrypted on its way to being rejected.

import (
	"fmt"
	"strings"
)

// ssmSecureProperties maps a resource type to the property paths AWS allows an
// ssm-secure reference in. A type absent from the map allows none.
var ssmSecureProperties = map[string][]string{
	"AWS::DirectoryService::MicrosoftAD":   {"Password"},
	"AWS::DirectoryService::SimpleAD":      {"Password"},
	"AWS::ElastiCache::ReplicationGroup":   {"AuthToken"},
	"AWS::IAM::User":                       {"LoginProfile/Password"},
	"AWS::KinesisFirehose::DeliveryStream": {"RedshiftDestinationConfiguration/Password"},
	"AWS::OpsWorks::App":                   {"Source/Password"},
	"AWS::OpsWorks::Stack":                 {"CustomCookbooksSource/Password", "RdsDbInstances/DbPassword"},
	"AWS::RDS::DBCluster":                  {"MasterUserPassword"},
	"AWS::RDS::DBInstance":                 {"MasterUserPassword"},
	"AWS::Redshift::Cluster":               {"MasterUserPassword"},
}

// ssmSecureAllowedAt reports whether an ssm-secure reference may appear at
// path within resType's properties.
func ssmSecureAllowedAt(resType string, path []string) bool {
	for _, allowed := range ssmSecureProperties[resType] {
		if propertyPathIs(path, allowed) {
			return true
		}
	}
	return false
}

// propertyPathIs reports whether path equals the "/"-separated want, without
// splitting want into a slice — this runs per candidate entry for every
// ssm-secure reference, and there is no reason for it to allocate.
func propertyPathIs(path []string, want string) bool {
	for i, seg := range path {
		if !strings.HasPrefix(want, seg) {
			return false
		}
		want = want[len(seg):]
		if i == len(path)-1 {
			return want == ""
		}
		var ok bool
		if want, ok = strings.CutPrefix(want, "/"); !ok {
			return false
		}
	}
	// An empty path is not a property and matches nothing.
	return false
}

// ssmSecureNotSupportedErr is the failure for an ssm-secure reference outside
// the allowlist. The bracketed path is AWS's, so the message a user searches
// for finds the same answer; the reference is Overcast's addition, because a
// stack event that names only the property leaves the reader hunting for which
// of its values was the reference.
func ssmSecureNotSupportedErr(ref dynamicRef, resType string, path []string) error {
	return fmt.Errorf("%s: SSM Secure reference is not supported in: [%s]",
		ref.Raw, awsPropertyPath(resType, path))
}

// awsPropertyPath renders a property path the way CloudFormation's own error
// does: "AWS::SQS::Queue/Properties/QueueName".
func awsPropertyPath(resType string, path []string) string {
	var b strings.Builder
	if resType != "" {
		b.WriteString(resType)
		b.WriteByte('/')
	}
	b.WriteString("Properties")
	for _, seg := range path {
		b.WriteByte('/')
		b.WriteString(seg)
	}
	return b.String()
}
