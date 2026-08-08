package dynamodb

import (
	"fmt"
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
)

// Billing modes modeled by DynamoDB's BillingMode enum.
const (
	billingModeProvisioned   = "PROVISIONED"
	billingModePayPerRequest = "PAY_PER_REQUEST"
)

// invalidParameterPrefix is the sentence AWS puts in front of every DynamoDB
// parameter-combination rejection.
const invalidParameterPrefix = "One or more parameter values were invalid: "

// resolveBillingMode returns the capacity mode a CreateTable request asks for.
//
// An omitted BillingMode is PROVISIONED: DynamoDB documents
// CreateTableInput.ProvisionedThroughput as required whenever BillingMode is
// PROVISIONED and forbidden whenever it is PAY_PER_REQUEST, and a request that
// names no billing mode at all must still carry throughput — which is only
// consistent with PROVISIONED being the default. Overcast previously had
// CloudFormation paper over this by injecting PAY_PER_REQUEST, which changed
// the AWS default and moved validation out of the service that owns it.
func resolveBillingMode(requested string) (string, *protocol.AWSError) {
	switch requested {
	case "":
		return billingModeProvisioned, nil
	case billingModeProvisioned, billingModePayPerRequest:
		return requested, nil
	default:
		return "", &protocol.AWSError{
			Code: "ValidationException",
			Message: fmt.Sprintf(
				"1 validation error detected: Value '%s' at 'billingMode' failed to satisfy constraint: Member must satisfy enum value set: [%s, %s]",
				requested, billingModeProvisioned, billingModePayPerRequest),
			HTTPStatus: http.StatusBadRequest,
		}
	}
}

// throughputSpecified reports whether a ProvisionedThroughput block names both
// capacity units. ReadCapacityUnits and WriteCapacityUnits are both required
// members of a PositiveLongObject (range min 1), so an absent block, an empty
// block, and a block carrying only one unit are all equally unspecified.
func throughputSpecified(pt *ProvisionedThroughput) bool {
	return pt != nil && pt.ReadCapacityUnits > 0 && pt.WriteCapacityUnits > 0
}

// throughputPresent reports whether the caller said anything about provisioned
// capacity at all. This is the test for the PAY_PER_REQUEST prohibition: naming
// even one capacity unit under on-demand billing is rejected.
func throughputPresent(pt *ProvisionedThroughput) bool {
	return pt != nil && (pt.ReadCapacityUnits != 0 || pt.WriteCapacityUnits != 0)
}

// validateTableThroughput enforces the table-level throughput rules for a
// billing mode: PROVISIONED requires both capacity units, PAY_PER_REQUEST
// forbids them.
func validateTableThroughput(billingMode string, pt *ProvisionedThroughput) *protocol.AWSError {
	if billingMode == billingModePayPerRequest {
		if throughputPresent(pt) {
			return errValidation(invalidParameterPrefix +
				"Neither ReadCapacityUnits nor WriteCapacityUnits can be specified when BillingMode is PAY_PER_REQUEST")
		}
		return nil
	}
	if !throughputSpecified(pt) {
		return errValidation(invalidParameterPrefix +
			"ReadCapacityUnits and WriteCapacityUnits must both be specified when BillingMode is PROVISIONED")
	}
	return nil
}

// validateIndexThroughput enforces the same rules for one global secondary
// index. Each GSI carries its own capacity under PROVISIONED billing and must
// carry none under PAY_PER_REQUEST — local secondary indexes have no
// ProvisionedThroughput member at all and share the table's capacity, so they
// are not validated here.
func validateIndexThroughput(billingMode string, idx *SecondaryIndex) *protocol.AWSError {
	if billingMode == billingModePayPerRequest {
		if throughputPresent(idx.ProvisionedThroughput) {
			return errValidation(fmt.Sprintf(
				"%sNeither ReadCapacityUnits nor WriteCapacityUnits can be specified when BillingMode is PAY_PER_REQUEST for index: %s",
				invalidParameterPrefix, idx.IndexName))
		}
		return nil
	}
	if !throughputSpecified(idx.ProvisionedThroughput) {
		return errValidation(fmt.Sprintf(
			"%sBoth ReadCapacityUnits and WriteCapacityUnits must be specified for index: %s",
			invalidParameterPrefix, idx.IndexName))
	}
	return nil
}

func errValidation(message string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ValidationException",
		Message:    message,
		HTTPStatus: http.StatusBadRequest,
	}
}
