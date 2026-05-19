package iam

import (
	"testing"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/state"
	"go.uber.org/zap"
)

var iamOps = []string{
	"CreateUser", "GetUser", "ListUsers", "UpdateUser", "DeleteUser",
	"CreateAccessKey", "DeleteAccessKey", "ListAccessKeys",
	"PutUserPolicy", "GetUserPolicy", "DeleteUserPolicy",
	"CreateRole", "GetRole", "ListRoles", "DeleteRole",
	"PutRolePolicy", "GetRolePolicy", "ListRolePolicies", "DeleteRolePolicy",
	"AttachRolePolicy", "DetachRolePolicy", "ListAttachedRolePolicies",
	"CreateInstanceProfile", "DeleteInstanceProfile", "GetInstanceProfile",
	"AddRoleToInstanceProfile", "RemoveRoleFromInstanceProfile",
	"CreatePolicy", "GetPolicy", "ListPolicies", "DeletePolicy",
	"CreateGroup", "GetGroup", "DeleteGroup",
	"AddUserToGroup", "RemoveUserFromGroup", "ListGroupsForUser", "ListGroups",
	"PutGroupPolicy", "GetGroupPolicy", "DeleteGroupPolicy", "ListGroupPolicies",
	"AttachGroupPolicy", "DetachGroupPolicy", "ListAttachedGroupPolicies",
	"AttachUserPolicy", "DetachUserPolicy", "ListAttachedUserPolicies",
	"ListUserPolicies",
	"TagRole", "UntagRole", "ListRoleTags",
	"TagUser", "UntagUser", "ListUserTags",
	"CreateServiceLinkedRole",
	"ListInstanceProfilesForRole",
	"UpdateAssumeRolePolicy",
	"ListInstanceProfiles",
	"SimulatePrincipalPolicy",
	"GetAccountAuthorizationDetails",
}

func TestTypedOps_matchLegacyRegistry(t *testing.T) {
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())

	if len(s.handler.typedOp) != len(s.handler.ops) {
		t.Fatalf("typed op count = %d, want legacy count %d", len(s.handler.typedOp), len(s.handler.ops))
	}
	for _, name := range iamOps {
		operation, ok := s.handler.typedOp[name]
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
