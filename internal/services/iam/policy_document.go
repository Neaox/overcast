package iam

// policy_document.go — the one place IAM checks a policy document before
// storing it (#1717).
//
// Seven operations take a document — CreatePolicy, CreatePolicyVersion,
// CreateRole, UpdateAssumeRolePolicy and the three Put*Policy writers — and
// AWS refuses a malformed one at each of them with MalformedPolicyDocument
// (400): "The request was rejected because the policy document was malformed.
// The error message describes the specific error." (IAM API Reference,
// API_CreatePolicy.html Errors, and identically on the other six pages.)
//
// The parsing itself belongs to internal/iampolicy — the same package the
// simulator and OVERCAST_ENFORCE_IAM evaluate through — so what IAM accepts
// and what enforcement can read are decided by one reader, not two.

import (
	"net/http"
	"strings"

	"github.com/overcast-sh/overcast/internal/iampolicy"
	"github.com/overcast-sh/overcast/internal/protocol"
)

// checkPolicyDocument validates a policy document supplied to a write
// operation, returning AWS's MalformedPolicyDocument fault when it is one AWS
// would refuse.
//
// An absent document is left to the operation's own required-parameter
// handling: AWS reports a missing or empty one as a ValidationError against
// the member's minimum length, not as a malformed document, and modelling that
// is a separate concern from parsing what was supplied.
func checkPolicyDocument(document string) *protocol.AWSError {
	if strings.TrimSpace(document) == "" {
		return nil
	}
	if err := iampolicy.ValidateDocument(document); err != nil {
		return errMalformedPolicyDocument(err.Error())
	}
	return nil
}

// errMalformedPolicyDocument renders AWS's MalformedPolicyDocument fault. The
// message names the defect because AWS's own documentation promises it does.
func errMalformedPolicyDocument(detail string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "MalformedPolicyDocument",
		Message:    "The policy document is malformed: " + detail,
		HTTPStatus: http.StatusBadRequest,
	}
}
