package iam

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (h *Handler) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		// Users
		"CreateUser": op.NewTyped("CreateUser", h.createUserTyped),
		"GetUser":    op.NewTyped("GetUser", h.getUserTyped),
		"ListUsers":  op.NewTyped("ListUsers", h.listUsersTyped),
		"UpdateUser": op.NewTyped("UpdateUser", h.updateUserTyped),
		"DeleteUser": op.NewTyped("DeleteUser", h.deleteUserTyped),
		// Access keys
		"CreateAccessKey": op.NewTyped("CreateAccessKey", h.createAccessKeyTyped),
		"DeleteAccessKey": op.NewTyped("DeleteAccessKey", h.deleteAccessKeyTyped),
		"ListAccessKeys":  op.NewTyped("ListAccessKeys", h.listAccessKeysTyped),
		// Inline user policies
		"PutUserPolicy":    op.NewTyped("PutUserPolicy", h.putUserPolicyTyped),
		"GetUserPolicy":    op.NewTyped("GetUserPolicy", h.getUserPolicyTyped),
		"DeleteUserPolicy": op.NewTyped("DeleteUserPolicy", h.deleteUserPolicyTyped),
		// Roles
		"CreateRole": op.NewTyped("CreateRole", h.createRoleTyped),
		"GetRole":    op.NewTyped("GetRole", h.getRoleTyped),
		"ListRoles":  op.NewTyped("ListRoles", h.listRolesTyped),
		"DeleteRole": op.NewTyped("DeleteRole", h.deleteRoleTyped),
		// Inline role policies
		"PutRolePolicy":    op.NewTyped("PutRolePolicy", h.putRolePolicyTyped),
		"GetRolePolicy":    op.NewTyped("GetRolePolicy", h.getRolePolicyTyped),
		"ListRolePolicies": op.NewTyped("ListRolePolicies", h.listRolePoliciesTyped),
		"DeleteRolePolicy": op.NewTyped("DeleteRolePolicy", h.deleteRolePolicyTyped),
		// Managed role policies
		"AttachRolePolicy":         op.NewTyped("AttachRolePolicy", h.attachRolePolicyTyped),
		"DetachRolePolicy":         op.NewTyped("DetachRolePolicy", h.detachRolePolicyTyped),
		"ListAttachedRolePolicies": op.NewTyped("ListAttachedRolePolicies", h.listAttachedRolePoliciesTyped),
		// Instance profiles
		"CreateInstanceProfile":         op.NewTyped("CreateInstanceProfile", h.createInstanceProfileTyped),
		"DeleteInstanceProfile":         op.NewTyped("DeleteInstanceProfile", h.deleteInstanceProfileTyped),
		"GetInstanceProfile":            op.NewTyped("GetInstanceProfile", h.getInstanceProfileTyped),
		"AddRoleToInstanceProfile":      op.NewTyped("AddRoleToInstanceProfile", h.addRoleToInstanceProfileTyped),
		"RemoveRoleFromInstanceProfile": op.NewTyped("RemoveRoleFromInstanceProfile", h.removeRoleFromInstanceProfileTyped),
		// Managed policies
		"CreatePolicy": op.NewTyped("CreatePolicy", h.createPolicyTyped),
		"GetPolicy":    op.NewTyped("GetPolicy", h.getPolicyTyped),
		"ListPolicies": op.NewTyped("ListPolicies", h.listPoliciesTyped),
		"DeletePolicy": op.NewTyped("DeletePolicy", h.deletePolicyTyped),
		// Groups
		"CreateGroup":         op.NewTyped("CreateGroup", h.createGroupTyped),
		"GetGroup":            op.NewTyped("GetGroup", h.getGroupTyped),
		"DeleteGroup":         op.NewTyped("DeleteGroup", h.deleteGroupTyped),
		"AddUserToGroup":      op.NewTyped("AddUserToGroup", h.addUserToGroupTyped),
		"RemoveUserFromGroup": op.NewTyped("RemoveUserFromGroup", h.removeUserFromGroupTyped),
		"ListGroupsForUser":   op.NewTyped("ListGroupsForUser", h.listGroupsForUserTyped),
		"ListGroups":          op.NewTyped("ListGroups", h.listGroupsTyped),
		// Group inline policies
		"PutGroupPolicy":    op.NewTyped("PutGroupPolicy", h.putGroupPolicyTyped),
		"GetGroupPolicy":    op.NewTyped("GetGroupPolicy", h.getGroupPolicyTyped),
		"DeleteGroupPolicy": op.NewTyped("DeleteGroupPolicy", h.deleteGroupPolicyTyped),
		"ListGroupPolicies": op.NewTyped("ListGroupPolicies", h.listGroupPoliciesTyped),
		// Managed group policies
		"AttachGroupPolicy":         op.NewTyped("AttachGroupPolicy", h.attachGroupPolicyTyped),
		"DetachGroupPolicy":         op.NewTyped("DetachGroupPolicy", h.detachGroupPolicyTyped),
		"ListAttachedGroupPolicies": op.NewTyped("ListAttachedGroupPolicies", h.listAttachedGroupPoliciesTyped),
		// Managed user policies
		"AttachUserPolicy":         op.NewTyped("AttachUserPolicy", h.attachUserPolicyTyped),
		"DetachUserPolicy":         op.NewTyped("DetachUserPolicy", h.detachUserPolicyTyped),
		"ListAttachedUserPolicies": op.NewTyped("ListAttachedUserPolicies", h.listAttachedUserPoliciesTyped),
		// Inline user policy listing
		"ListUserPolicies": op.NewTyped("ListUserPolicies", h.listUserPoliciesTyped),
		// Role tagging
		"TagRole":      op.NewTyped("TagRole", h.tagRoleTyped),
		"UntagRole":    op.NewTyped("UntagRole", h.untagRoleTyped),
		"ListRoleTags": op.NewTyped("ListRoleTags", h.listRoleTagsTyped),
		// User tagging
		"TagUser":      op.NewTyped("TagUser", h.tagUserTyped),
		"UntagUser":    op.NewTyped("UntagUser", h.untagUserTyped),
		"ListUserTags": op.NewTyped("ListUserTags", h.listUserTagsTyped),
		// Service-linked roles
		"CreateServiceLinkedRole": op.NewTyped("CreateServiceLinkedRole", h.createServiceLinkedRoleTyped),
		// Instance profile by role
		"ListInstanceProfilesForRole": op.NewTyped("ListInstanceProfilesForRole", h.listInstanceProfilesForRoleTyped),
		// Role mutation
		"UpdateAssumeRolePolicy": op.NewTyped("UpdateAssumeRolePolicy", h.updateAssumeRolePolicyTyped),
		// List instance profiles
		"ListInstanceProfiles": op.NewTyped("ListInstanceProfiles", h.listInstanceProfilesTyped),
		// Policy simulation
		"SimulatePrincipalPolicy": op.NewTyped("SimulatePrincipalPolicy", h.simulatePrincipalPolicyTyped),
		// Account-wide authorization details
		"GetAccountAuthorizationDetails": op.NewTyped("GetAccountAuthorizationDetails", h.getAccountAuthorizationDetailsTyped),
	}
}

// Operations implements router.ProtocolService.
func (s *Service) Operations() []op.Operation {
	ops := s.handler.typedOp
	out := make([]op.Operation, 0, len(ops))
	for _, operation := range ops {
		out = append(out, operation)
	}
	return out
}

// SupportedProtocols implements router.ProtocolService.
func (s *Service) SupportedProtocols() []codec.Codec {
	return []codec.Codec{codec.QueryXML}
}
