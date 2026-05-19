package cognito

import (
	"testing"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/state"
	"go.uber.org/zap"
)

var allCognitoOps = []string{
	"CreateUserPool", "DescribeUserPool", "DeleteUserPool", "ListUserPools", "UpdateUserPool", "SetUserPoolMfaConfig", "GetUserPoolMfaConfig",
	"CreateUserPoolDomain", "DescribeUserPoolDomain", "DeleteUserPoolDomain", "UpdateUserPoolDomain",
	"CreateUserPoolClient", "DescribeUserPoolClient", "DeleteUserPoolClient", "ListUserPoolClients", "UpdateUserPoolClient",
	"AdminCreateUser", "AdminDeleteUser", "AdminGetUser", "AdminSetUserPassword", "AdminConfirmSignUp",
	"AdminUpdateUserAttributes", "AdminDeleteUserAttributes", "AdminDisableUser", "AdminEnableUser",
	"AdminInitiateAuth", "AdminRespondToAuthChallenge", "ListUsers",
	"SignUp", "ConfirmSignUp", "ResendConfirmationCode",
	"InitiateAuth", "RespondToAuthChallenge", "ConfirmDevice", "GetDevice", "ListDevices", "UpdateDeviceStatus", "ForgetDevice",
	"AdminGetDevice", "AdminListDevices", "AdminUpdateDeviceStatus", "AdminForgetDevice",
	"ForgotPassword", "ConfirmForgotPassword", "ChangePassword",
	"AssociateSoftwareToken", "VerifySoftwareToken", "StartWebAuthnRegistration", "CompleteWebAuthnRegistration", "SetUserMFAPreference", "AdminSetUserMFAPreference",
	"CreateGroup", "GetGroup", "DeleteGroup", "UpdateGroup", "ListGroups",
	"AdminAddUserToGroup", "AdminRemoveUserFromGroup", "AdminListGroupsForUser", "ListUsersInGroup",
	"GetUser", "UpdateUserAttributes", "VerifyUserAttribute", "GetUserAttributeVerificationCode", "DeleteUserAttributes", "GlobalSignOut", "RevokeToken",
}

func TestTypedOps_matchAllOperations(t *testing.T) {
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())

	if len(s.typedOp) != len(allCognitoOps) {
		t.Fatalf("typed op count = %d, want %d", len(s.typedOp), len(allCognitoOps))
	}
	for _, name := range allCognitoOps {
		operation, ok := s.typedOp[name]
		if !ok {
			t.Fatalf("missing typed operation %s", name)
		}
		if operation.Name() != name {
			t.Fatalf("typed operation %s reports name %s", name, operation.Name())
		}
		if _, raw := operation.(*op.Raw); raw {
			t.Fatalf("typed operation %s still uses raw adapter", name)
		}
	}
}
