//go:build dev

package iam

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// Users
		capabilities.Capability{Service: "iam", Operation: "CreateUser", Category: "Users", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "GetUser", Category: "Users", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "ListUsers", Category: "Users", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "UpdateUser", Category: "Users", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "DeleteUser", Category: "Users", Status: capabilities.StatusSupported},
		// Access keys
		capabilities.Capability{Service: "iam", Operation: "CreateAccessKey", Category: "Access keys", Status: capabilities.StatusSupported, Notes: "Generates AKIA-prefixed key + secret"},
		capabilities.Capability{Service: "iam", Operation: "ListAccessKeys", Category: "Access keys", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "DeleteAccessKey", Category: "Access keys", Status: capabilities.StatusSupported},
		// User inline policies
		capabilities.Capability{Service: "iam", Operation: "PutUserPolicy", Category: "User inline policies", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "GetUserPolicy", Category: "User inline policies", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "DeleteUserPolicy", Category: "User inline policies", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "ListUserPolicies", Category: "User inline policies", Status: capabilities.StatusSupported},
		// User managed policies
		capabilities.Capability{Service: "iam", Operation: "AttachUserPolicy", Category: "User managed policies", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "DetachUserPolicy", Category: "User managed policies", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "ListAttachedUserPolicies", Category: "User managed policies", Status: capabilities.StatusSupported},
		// User tagging
		capabilities.Capability{Service: "iam", Operation: "TagUser", Category: "User tagging", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "UntagUser", Category: "User tagging", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "ListUserTags", Category: "User tagging", Status: capabilities.StatusSupported},
		// Roles
		capabilities.Capability{Service: "iam", Operation: "CreateRole", Category: "Roles", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "GetRole", Category: "Roles", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "ListRoles", Category: "Roles", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "DeleteRole", Category: "Roles", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "UpdateAssumeRolePolicy", Category: "Roles", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "CreateServiceLinkedRole", Category: "Roles", Status: capabilities.StatusSupported},
		// Role inline policies
		capabilities.Capability{Service: "iam", Operation: "PutRolePolicy", Category: "Role inline policies", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "GetRolePolicy", Category: "Role inline policies", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "ListRolePolicies", Category: "Role inline policies", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "DeleteRolePolicy", Category: "Role inline policies", Status: capabilities.StatusSupported},
		// Role managed policies
		capabilities.Capability{Service: "iam", Operation: "AttachRolePolicy", Category: "Role managed policies", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "DetachRolePolicy", Category: "Role managed policies", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "ListAttachedRolePolicies", Category: "Role managed policies", Status: capabilities.StatusSupported},
		// Role tagging
		capabilities.Capability{Service: "iam", Operation: "TagRole", Category: "Role tagging", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "UntagRole", Category: "Role tagging", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "ListRoleTags", Category: "Role tagging", Status: capabilities.StatusSupported},
		// Instance profiles
		capabilities.Capability{Service: "iam", Operation: "CreateInstanceProfile", Category: "Instance profiles", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "GetInstanceProfile", Category: "Instance profiles", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "DeleteInstanceProfile", Category: "Instance profiles", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "AddRoleToInstanceProfile", Category: "Instance profiles", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "RemoveRoleFromInstanceProfile", Category: "Instance profiles", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "ListInstanceProfiles", Category: "Instance profiles", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "ListInstanceProfilesForRole", Category: "Instance profiles", Status: capabilities.StatusSupported},
		// Managed policies
		capabilities.Capability{Service: "iam", Operation: "CreatePolicy", Category: "Managed policies", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "GetPolicy", Category: "Managed policies", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "ListPolicies", Category: "Managed policies", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "DeletePolicy", Category: "Managed policies", Status: capabilities.StatusSupported},
		// Groups
		capabilities.Capability{Service: "iam", Operation: "CreateGroup", Category: "Groups", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "GetGroup", Category: "Groups", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "DeleteGroup", Category: "Groups", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "ListGroups", Category: "Groups", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "AddUserToGroup", Category: "Groups", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "RemoveUserFromGroup", Category: "Groups", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "ListGroupsForUser", Category: "Groups", Status: capabilities.StatusSupported},
		// Group inline policies
		capabilities.Capability{Service: "iam", Operation: "PutGroupPolicy", Category: "Group inline policies", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "GetGroupPolicy", Category: "Group inline policies", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "DeleteGroupPolicy", Category: "Group inline policies", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "ListGroupPolicies", Category: "Group inline policies", Status: capabilities.StatusSupported},
		// Group managed policies
		capabilities.Capability{Service: "iam", Operation: "AttachGroupPolicy", Category: "Group managed policies", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "DetachGroupPolicy", Category: "Group managed policies", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "iam", Operation: "ListAttachedGroupPolicies", Category: "Group managed policies", Status: capabilities.StatusSupported},
		// Policy simulation
		capabilities.Capability{Service: "iam", Operation: "SimulatePrincipalPolicy", Category: "Policy simulation", Status: capabilities.StatusSupported, Notes: "Always returns allowed — no enforcement engine"},
		// Account details
		capabilities.Capability{Service: "iam", Operation: "GetAccountAuthorizationDetails", Category: "Account details", Status: capabilities.StatusSupported, Notes: "Returns all users, groups, roles, and managed policies in one call"},
	)
}
