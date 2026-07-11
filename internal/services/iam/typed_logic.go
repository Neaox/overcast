package iam

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
)

// ─── respMeta ────────────────────────────────────────────────────────────────

type respMeta struct {
	RequestId string `xml:"RequestId"`
}

func metaFromCtx(ctx context.Context) respMeta {
	return respMeta{RequestId: protocol.RequestIDFromContext(ctx)}
}

// ─── Request types ──────────────────────────────────────────────────────────

type createUserReq struct {
	UserName string `json:"UserName"`
	Path     string `json:"Path"`
}

type getUserReq struct {
	UserName string `json:"UserName"`
}

type listUsersReq struct{}

type deleteUserReq struct {
	UserName string `json:"UserName"`
}

type updateUserReq struct {
	UserName    string `json:"UserName"`
	NewPath     string `json:"NewPath"`
	NewUserName string `json:"NewUserName"`
}

type createAccessKeyReq struct {
	UserName string `json:"UserName"`
}

type deleteAccessKeyReq struct {
	UserName    string `json:"UserName"`
	AccessKeyId string `json:"AccessKeyId"`
}

type listAccessKeysReq struct {
	UserName string `json:"UserName"`
}

type putUserPolicyReq struct {
	UserName       string `json:"UserName"`
	PolicyName     string `json:"PolicyName"`
	PolicyDocument string `json:"PolicyDocument"`
}

type getUserPolicyReq struct {
	UserName   string `json:"UserName"`
	PolicyName string `json:"PolicyName"`
}

type deleteUserPolicyReq struct {
	UserName   string `json:"UserName"`
	PolicyName string `json:"PolicyName"`
}

type createRoleReq struct {
	RoleName                 string `json:"RoleName"`
	AssumeRolePolicyDocument string `json:"AssumeRolePolicyDocument"`
	Path                     string `json:"Path"`
}

type getRoleReq struct {
	RoleName string `json:"RoleName"`
}

type listRolesReq struct{}

type deleteRoleReq struct {
	RoleName string `json:"RoleName"`
}

type putRolePolicyReq struct {
	RoleName       string `json:"RoleName"`
	PolicyName     string `json:"PolicyName"`
	PolicyDocument string `json:"PolicyDocument"`
}

type getRolePolicyReq struct {
	RoleName   string `json:"RoleName"`
	PolicyName string `json:"PolicyName"`
}

type listRolePoliciesReq struct {
	RoleName string `json:"RoleName"`
}

type deleteRolePolicyReq struct {
	RoleName   string `json:"RoleName"`
	PolicyName string `json:"PolicyName"`
}

type attachRolePolicyReq struct {
	RoleName  string `json:"RoleName"`
	PolicyArn string `json:"PolicyArn"`
}

type detachRolePolicyReq struct {
	RoleName  string `json:"RoleName"`
	PolicyArn string `json:"PolicyArn"`
}

type listAttachedRolePoliciesReq struct {
	RoleName string `json:"RoleName"`
}

type createInstanceProfileReq struct {
	InstanceProfileName string `json:"InstanceProfileName"`
	Path                string `json:"Path"`
}

type deleteInstanceProfileReq struct {
	InstanceProfileName string `json:"InstanceProfileName"`
}

type getInstanceProfileReq struct {
	InstanceProfileName string `json:"InstanceProfileName"`
}

type addRoleToInstanceProfileReq struct {
	InstanceProfileName string `json:"InstanceProfileName"`
	RoleName            string `json:"RoleName"`
}

type removeRoleFromInstanceProfileReq struct {
	InstanceProfileName string `json:"InstanceProfileName"`
	RoleName            string `json:"RoleName"`
}

type createPolicyReq struct {
	PolicyName     string `json:"PolicyName"`
	PolicyDocument string `json:"PolicyDocument"`
	Path           string `json:"Path"`
}

type getPolicyReq struct {
	PolicyArn string `json:"PolicyArn"`
}

type listPoliciesReq struct {
	Scope string `json:"Scope"`
}

type deletePolicyReq struct {
	PolicyArn string `json:"PolicyArn"`
}

type createGroupReq struct {
	GroupName string `json:"GroupName"`
	Path      string `json:"Path"`
}

type getGroupReq struct {
	GroupName string `json:"GroupName"`
}

type deleteGroupReq struct {
	GroupName string `json:"GroupName"`
}

type addUserToGroupReq struct {
	GroupName string `json:"GroupName"`
	UserName  string `json:"UserName"`
}

type removeUserFromGroupReq struct {
	GroupName string `json:"GroupName"`
	UserName  string `json:"UserName"`
}

type listGroupsForUserReq struct {
	UserName string `json:"UserName"`
}

type listGroupsReq struct{}

type putGroupPolicyReq struct {
	GroupName      string `json:"GroupName"`
	PolicyName     string `json:"PolicyName"`
	PolicyDocument string `json:"PolicyDocument"`
}

type getGroupPolicyReq struct {
	GroupName  string `json:"GroupName"`
	PolicyName string `json:"PolicyName"`
}

type deleteGroupPolicyReq struct {
	GroupName  string `json:"GroupName"`
	PolicyName string `json:"PolicyName"`
}

type listGroupPoliciesReq struct {
	GroupName string `json:"GroupName"`
}

type attachGroupPolicyReq struct {
	GroupName string `json:"GroupName"`
	PolicyArn string `json:"PolicyArn"`
}

type detachGroupPolicyReq struct {
	GroupName string `json:"GroupName"`
	PolicyArn string `json:"PolicyArn"`
}

type listAttachedGroupPoliciesReq struct {
	GroupName string `json:"GroupName"`
}

type attachUserPolicyReq struct {
	UserName  string `json:"UserName"`
	PolicyArn string `json:"PolicyArn"`
}

type detachUserPolicyReq struct {
	UserName  string `json:"UserName"`
	PolicyArn string `json:"PolicyArn"`
}

type listAttachedUserPoliciesReq struct {
	UserName string `json:"UserName"`
}

type listUserPoliciesReq struct {
	UserName string `json:"UserName"`
}

type tagEntry struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

type tagRoleReq struct {
	RoleName string     `json:"RoleName"`
	Tags     []tagEntry `json:"Tags"`
}

type untagRoleReq struct {
	RoleName string   `json:"RoleName"`
	TagKeys  []string `json:"TagKeys"`
}

type listRoleTagsReq struct {
	RoleName string `json:"RoleName"`
}

type tagUserReq struct {
	UserName string     `json:"UserName"`
	Tags     []tagEntry `json:"Tags"`
}

type untagUserReq struct {
	UserName string   `json:"UserName"`
	TagKeys  []string `json:"TagKeys"`
}

type listUserTagsReq struct {
	UserName string `json:"UserName"`
}

type createServiceLinkedRoleReq struct {
	AWSServiceName string `json:"AWSServiceName"`
}

type listInstanceProfilesForRoleReq struct {
	RoleName string `json:"RoleName"`
}

type updateAssumeRolePolicyReq struct {
	RoleName       string `json:"RoleName"`
	PolicyDocument string `json:"PolicyDocument"`
}

type listInstanceProfilesReq struct{}

type simulatePrincipalPolicyReq struct {
	PolicySourceArn string   `json:"PolicySourceArn"`
	ActionNames     []string `json:"ActionNames"`
	ResourceArns    []string `json:"ResourceArns"`
}

type getAccountAuthorizationDetailsReq struct{}

// ─── Response types ─────────────────────────────────────────────────────────

// --- Users ---

type createUserResp struct {
	XMLName struct{}         `xml:"CreateUserResponse"`
	Xmlns   string           `xml:"xmlns,attr"`
	Result  createUserResult `xml:"CreateUserResult"`
	Meta    respMeta         `xml:"ResponseMetadata"`
}
type createUserResult struct {
	User userXML `xml:"User"`
}

type getUserResp struct {
	XMLName struct{}      `xml:"GetUserResponse"`
	Xmlns   string        `xml:"xmlns,attr"`
	Result  getUserResult `xml:"GetUserResult"`
	Meta    respMeta      `xml:"ResponseMetadata"`
}
type getUserResult struct {
	User userXML `xml:"User"`
}

type listUsersResp struct {
	XMLName struct{}        `xml:"ListUsersResponse"`
	Xmlns   string          `xml:"xmlns,attr"`
	Result  listUsersResult `xml:"ListUsersResult"`
	Meta    respMeta        `xml:"ResponseMetadata"`
}
type listUsersResult struct {
	Users       listMembersXML[userXML] `xml:"Users"`
	IsTruncated bool                    `xml:"IsTruncated"`
}

type deleteUserResp struct {
	XMLName struct{} `xml:"DeleteUserResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

type updateUserResp struct {
	XMLName struct{} `xml:"UpdateUserResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

// --- Access Keys ---

type createAccessKeyResp struct {
	XMLName struct{}              `xml:"CreateAccessKeyResponse"`
	Xmlns   string                `xml:"xmlns,attr"`
	Result  createAccessKeyResult `xml:"CreateAccessKeyResult"`
	Meta    respMeta              `xml:"ResponseMetadata"`
}
type createAccessKeyResult struct {
	AccessKey accessKeyXML `xml:"AccessKey"`
}

type deleteAccessKeyResp struct {
	XMLName struct{} `xml:"DeleteAccessKeyResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

type listAccessKeysResp struct {
	XMLName struct{}             `xml:"ListAccessKeysResponse"`
	Xmlns   string               `xml:"xmlns,attr"`
	Result  listAccessKeysResult `xml:"ListAccessKeysResult"`
	Meta    respMeta             `xml:"ResponseMetadata"`
}
type listAccessKeysResult struct {
	AccessKeyMetadata []accessKeyXML `xml:"AccessKeyMetadata>member"`
	IsTruncated       bool           `xml:"IsTruncated"`
}

// --- Inline User Policies ---

type putUserPolicyResp struct {
	XMLName struct{} `xml:"PutUserPolicyResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

type getUserPolicyResp struct {
	XMLName struct{}            `xml:"GetUserPolicyResponse"`
	Xmlns   string              `xml:"xmlns,attr"`
	Result  getUserPolicyResult `xml:"GetUserPolicyResult"`
	Meta    respMeta            `xml:"ResponseMetadata"`
}
type getUserPolicyResult struct {
	UserName       string `xml:"UserName"`
	PolicyName     string `xml:"PolicyName"`
	PolicyDocument string `xml:"PolicyDocument"`
}

type deleteUserPolicyResp struct {
	XMLName struct{} `xml:"DeleteUserPolicyResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

// --- Roles ---

type createRoleResp struct {
	XMLName struct{}         `xml:"CreateRoleResponse"`
	Xmlns   string           `xml:"xmlns,attr"`
	Result  createRoleResult `xml:"CreateRoleResult"`
	Meta    respMeta         `xml:"ResponseMetadata"`
}
type createRoleResult struct {
	Role roleXML `xml:"Role"`
}

type getRoleResp struct {
	XMLName struct{}      `xml:"GetRoleResponse"`
	Xmlns   string        `xml:"xmlns,attr"`
	Result  getRoleResult `xml:"GetRoleResult"`
	Meta    respMeta      `xml:"ResponseMetadata"`
}
type getRoleResult struct {
	Role roleXML `xml:"Role"`
}

type listRolesResp struct {
	XMLName struct{}        `xml:"ListRolesResponse"`
	Xmlns   string          `xml:"xmlns,attr"`
	Result  listRolesResult `xml:"ListRolesResult"`
	Meta    respMeta        `xml:"ResponseMetadata"`
}
type listRolesResult struct {
	Roles       listMembersXML[roleXML] `xml:"Roles"`
	IsTruncated bool                    `xml:"IsTruncated"`
}

type deleteRoleResp struct {
	XMLName struct{} `xml:"DeleteRoleResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

// --- Inline Role Policies ---

type putRolePolicyResp struct {
	XMLName struct{} `xml:"PutRolePolicyResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

type getRolePolicyResp struct {
	XMLName struct{}            `xml:"GetRolePolicyResponse"`
	Xmlns   string              `xml:"xmlns,attr"`
	Result  getRolePolicyResult `xml:"GetRolePolicyResult"`
	Meta    respMeta            `xml:"ResponseMetadata"`
}
type getRolePolicyResult struct {
	RoleName       string `xml:"RoleName"`
	PolicyName     string `xml:"PolicyName"`
	PolicyDocument string `xml:"PolicyDocument"`
}

type listRolePoliciesResp struct {
	XMLName struct{}               `xml:"ListRolePoliciesResponse"`
	Xmlns   string                 `xml:"xmlns,attr"`
	Result  listRolePoliciesResult `xml:"ListRolePoliciesResult"`
	Meta    respMeta               `xml:"ResponseMetadata"`
}
type listRolePoliciesResult struct {
	PolicyNames listMembersXML[string] `xml:"PolicyNames"`
	IsTruncated bool                   `xml:"IsTruncated"`
}

type deleteRolePolicyResp struct {
	XMLName struct{} `xml:"DeleteRolePolicyResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

// --- Managed Role Policies ---

type attachRolePolicyResp struct {
	XMLName struct{} `xml:"AttachRolePolicyResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

type detachRolePolicyResp struct {
	XMLName struct{} `xml:"DetachRolePolicyResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

type listAttachedRolePoliciesResp struct {
	XMLName struct{}                       `xml:"ListAttachedRolePoliciesResponse"`
	Xmlns   string                         `xml:"xmlns,attr"`
	Result  listAttachedRolePoliciesResult `xml:"ListAttachedRolePoliciesResult"`
	Meta    respMeta                       `xml:"ResponseMetadata"`
}
type listAttachedRolePoliciesResult struct {
	AttachedPolicies listMembersXML[attachedPolicyXML] `xml:"AttachedPolicies"`
	IsTruncated      bool                              `xml:"IsTruncated"`
}

// --- Instance Profiles ---

type createInstanceProfileResp struct {
	XMLName struct{}                    `xml:"CreateInstanceProfileResponse"`
	Xmlns   string                      `xml:"xmlns,attr"`
	Result  createInstanceProfileResult `xml:"CreateInstanceProfileResult"`
	Meta    respMeta                    `xml:"ResponseMetadata"`
}
type createInstanceProfileResult struct {
	InstanceProfile instanceProfileXML `xml:"InstanceProfile"`
}

type deleteInstanceProfileResp struct {
	XMLName struct{} `xml:"DeleteInstanceProfileResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

type getInstanceProfileResp struct {
	XMLName struct{}                 `xml:"GetInstanceProfileResponse"`
	Xmlns   string                   `xml:"xmlns,attr"`
	Result  getInstanceProfileResult `xml:"GetInstanceProfileResult"`
	Meta    respMeta                 `xml:"ResponseMetadata"`
}
type getInstanceProfileResult struct {
	InstanceProfile instanceProfileXML `xml:"InstanceProfile"`
}

type addRoleToInstanceProfileResp struct {
	XMLName struct{} `xml:"AddRoleToInstanceProfileResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

type removeRoleFromInstanceProfileResp struct {
	XMLName struct{} `xml:"RemoveRoleFromInstanceProfileResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

// --- Managed Policies ---

type createPolicyResp struct {
	XMLName struct{}           `xml:"CreatePolicyResponse"`
	Xmlns   string             `xml:"xmlns,attr"`
	Result  createPolicyResult `xml:"CreatePolicyResult"`
	Meta    respMeta           `xml:"ResponseMetadata"`
}
type createPolicyResult struct {
	Policy policyXML `xml:"Policy"`
}

type getPolicyResp struct {
	XMLName struct{}        `xml:"GetPolicyResponse"`
	Xmlns   string          `xml:"xmlns,attr"`
	Result  getPolicyResult `xml:"GetPolicyResult"`
	Meta    respMeta        `xml:"ResponseMetadata"`
}
type getPolicyResult struct {
	Policy policyXML `xml:"Policy"`
}

type listPoliciesResp struct {
	XMLName struct{}           `xml:"ListPoliciesResponse"`
	Xmlns   string             `xml:"xmlns,attr"`
	Result  listPoliciesResult `xml:"ListPoliciesResult"`
	Meta    respMeta           `xml:"ResponseMetadata"`
}
type listPoliciesResult struct {
	Policies    listMembersXML[policyXML] `xml:"Policies"`
	IsTruncated bool                      `xml:"IsTruncated"`
}

type deletePolicyResp struct {
	XMLName struct{} `xml:"DeletePolicyResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

// --- Groups ---

type createGroupResp struct {
	XMLName struct{}          `xml:"CreateGroupResponse"`
	Xmlns   string            `xml:"xmlns,attr"`
	Result  createGroupResult `xml:"CreateGroupResult"`
	Meta    respMeta          `xml:"ResponseMetadata"`
}
type createGroupResult struct {
	Group groupXML `xml:"Group"`
}

type getGroupResp struct {
	XMLName struct{}       `xml:"GetGroupResponse"`
	Xmlns   string         `xml:"xmlns,attr"`
	Result  getGroupResult `xml:"GetGroupResult"`
	Meta    respMeta       `xml:"ResponseMetadata"`
}
type getGroupResult struct {
	Group       groupXML                `xml:"Group"`
	Users       listMembersXML[userXML] `xml:"Users"`
	IsTruncated bool                    `xml:"IsTruncated"`
}

type deleteGroupResp struct {
	XMLName struct{} `xml:"DeleteGroupResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

type addUserToGroupResp struct {
	XMLName struct{} `xml:"AddUserToGroupResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

type removeUserFromGroupResp struct {
	XMLName struct{} `xml:"RemoveUserFromGroupResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

type listGroupsForUserResp struct {
	XMLName struct{}                `xml:"ListGroupsForUserResponse"`
	Xmlns   string                  `xml:"xmlns,attr"`
	Result  listGroupsForUserResult `xml:"ListGroupsForUserResult"`
	Meta    respMeta                `xml:"ResponseMetadata"`
}
type listGroupsForUserResult struct {
	Groups      listMembersXML[groupXML] `xml:"Groups"`
	IsTruncated bool                     `xml:"IsTruncated"`
}

type listGroupsResp struct {
	XMLName struct{}         `xml:"ListGroupsResponse"`
	Xmlns   string           `xml:"xmlns,attr"`
	Result  listGroupsResult `xml:"ListGroupsResult"`
	Meta    respMeta         `xml:"ResponseMetadata"`
}
type listGroupsResult struct {
	Groups      listMembersXML[groupXML] `xml:"Groups"`
	IsTruncated bool                     `xml:"IsTruncated"`
}

// --- Inline Group Policies ---

type putGroupPolicyResp struct {
	XMLName struct{} `xml:"PutGroupPolicyResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

type getGroupPolicyResp struct {
	XMLName struct{}             `xml:"GetGroupPolicyResponse"`
	Xmlns   string               `xml:"xmlns,attr"`
	Result  getGroupPolicyResult `xml:"GetGroupPolicyResult"`
	Meta    respMeta             `xml:"ResponseMetadata"`
}
type getGroupPolicyResult struct {
	GroupName      string `xml:"GroupName"`
	PolicyName     string `xml:"PolicyName"`
	PolicyDocument string `xml:"PolicyDocument"`
}

type deleteGroupPolicyResp struct {
	XMLName struct{} `xml:"DeleteGroupPolicyResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

type listGroupPoliciesResp struct {
	XMLName struct{}                `xml:"ListGroupPoliciesResponse"`
	Xmlns   string                  `xml:"xmlns,attr"`
	Result  listGroupPoliciesResult `xml:"ListGroupPoliciesResult"`
	Meta    respMeta                `xml:"ResponseMetadata"`
}
type listGroupPoliciesResult struct {
	PolicyNames listMembersXML[string] `xml:"PolicyNames"`
	IsTruncated bool                   `xml:"IsTruncated"`
}

// --- Managed Group Policies ---

type attachGroupPolicyResp struct {
	XMLName struct{} `xml:"AttachGroupPolicyResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

type detachGroupPolicyResp struct {
	XMLName struct{} `xml:"DetachGroupPolicyResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

type listAttachedGroupPoliciesResp struct {
	XMLName struct{}                        `xml:"ListAttachedGroupPoliciesResponse"`
	Xmlns   string                          `xml:"xmlns,attr"`
	Result  listAttachedGroupPoliciesResult `xml:"ListAttachedGroupPoliciesResult"`
	Meta    respMeta                        `xml:"ResponseMetadata"`
}
type listAttachedGroupPoliciesResult struct {
	AttachedPolicies listMembersXML[attachedPolicyXML] `xml:"AttachedPolicies"`
	IsTruncated      bool                              `xml:"IsTruncated"`
}

// --- Managed User Policies ---

type attachUserPolicyResp struct {
	XMLName struct{} `xml:"AttachUserPolicyResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

type detachUserPolicyResp struct {
	XMLName struct{} `xml:"DetachUserPolicyResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

type listAttachedUserPoliciesResp struct {
	XMLName struct{}                       `xml:"ListAttachedUserPoliciesResponse"`
	Xmlns   string                         `xml:"xmlns,attr"`
	Result  listAttachedUserPoliciesResult `xml:"ListAttachedUserPoliciesResult"`
	Meta    respMeta                       `xml:"ResponseMetadata"`
}
type listAttachedUserPoliciesResult struct {
	AttachedPolicies listMembersXML[attachedPolicyXML] `xml:"AttachedPolicies"`
	IsTruncated      bool                              `xml:"IsTruncated"`
}

// --- Inline User Policy Listing ---

type listUserPoliciesResp struct {
	XMLName struct{}               `xml:"ListUserPoliciesResponse"`
	Xmlns   string                 `xml:"xmlns,attr"`
	Result  listUserPoliciesResult `xml:"ListUserPoliciesResult"`
	Meta    respMeta               `xml:"ResponseMetadata"`
}
type listUserPoliciesResult struct {
	PolicyNames listMembersXML[string] `xml:"PolicyNames"`
	IsTruncated bool                   `xml:"IsTruncated"`
}

// --- Role Tagging ---

type tagRoleResp struct {
	XMLName struct{} `xml:"TagRoleResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

type untagRoleResp struct {
	XMLName struct{} `xml:"UntagRoleResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

type listRoleTagsResp struct {
	XMLName struct{}           `xml:"ListRoleTagsResponse"`
	Xmlns   string             `xml:"xmlns,attr"`
	Result  listRoleTagsResult `xml:"ListRoleTagsResult"`
	Meta    respMeta           `xml:"ResponseMetadata"`
}
type listRoleTagsResult struct {
	Tags        listMembersXML[tagXML] `xml:"Tags"`
	IsTruncated bool                   `xml:"IsTruncated"`
}

// --- User Tagging ---

type tagUserResp struct {
	XMLName struct{} `xml:"TagUserResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

type untagUserResp struct {
	XMLName struct{} `xml:"UntagUserResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

type listUserTagsResp struct {
	XMLName struct{}           `xml:"ListUserTagsResponse"`
	Xmlns   string             `xml:"xmlns,attr"`
	Result  listUserTagsResult `xml:"ListUserTagsResult"`
	Meta    respMeta           `xml:"ResponseMetadata"`
}
type listUserTagsResult struct {
	Tags        listMembersXML[tagXML] `xml:"Tags"`
	IsTruncated bool                   `xml:"IsTruncated"`
}

// --- Service-Linked Roles ---

type createServiceLinkedRoleResp struct {
	XMLName struct{}                      `xml:"CreateServiceLinkedRoleResponse"`
	Xmlns   string                        `xml:"xmlns,attr"`
	Result  createServiceLinkedRoleResult `xml:"CreateServiceLinkedRoleResult"`
	Meta    respMeta                      `xml:"ResponseMetadata"`
}
type createServiceLinkedRoleResult struct {
	Role roleXML `xml:"Role"`
}

// --- Instance Profiles For Role ---

type listInstanceProfilesForRoleResp struct {
	XMLName struct{}                          `xml:"ListInstanceProfilesForRoleResponse"`
	Xmlns   string                            `xml:"xmlns,attr"`
	Result  listInstanceProfilesForRoleResult `xml:"ListInstanceProfilesForRoleResult"`
	Meta    respMeta                          `xml:"ResponseMetadata"`
}
type listInstanceProfilesForRoleResult struct {
	InstanceProfiles listMembersXML[instanceProfileXML] `xml:"InstanceProfiles"`
	IsTruncated      bool                               `xml:"IsTruncated"`
}

// --- Role Mutation ---

type updateAssumeRolePolicyResp struct {
	XMLName struct{} `xml:"UpdateAssumeRolePolicyResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

// --- List Instance Profiles ---

type listInstanceProfilesResp struct {
	XMLName struct{}                   `xml:"ListInstanceProfilesResponse"`
	Xmlns   string                     `xml:"xmlns,attr"`
	Result  listInstanceProfilesResult `xml:"ListInstanceProfilesResult"`
	Meta    respMeta                   `xml:"ResponseMetadata"`
}
type listInstanceProfilesResult struct {
	InstanceProfiles listMembersXML[instanceProfileXML] `xml:"InstanceProfiles"`
	IsTruncated      bool                               `xml:"IsTruncated"`
}

// --- Policy Simulation ---

type evalResult struct {
	EvalActionName   string `xml:"EvalActionName"`
	EvalDecision     string `xml:"EvalDecision"`
	EvalResourceName string `xml:"EvalResourceName"`
}

type simulatePrincipalPolicyResp struct {
	XMLName struct{}                      `xml:"SimulatePrincipalPolicyResponse"`
	Xmlns   string                        `xml:"xmlns,attr"`
	Result  simulatePrincipalPolicyResult `xml:"SimulatePrincipalPolicyResult"`
	Meta    respMeta                      `xml:"ResponseMetadata"`
}
type simulatePrincipalPolicyResult struct {
	EvaluationResults listMembersXML[evalResult] `xml:"EvaluationResults"`
	IsTruncated       bool                       `xml:"IsTruncated"`
}

// --- GetAccountAuthorizationDetails ---

type getAccountAuthorizationDetailsResp struct {
	XMLName struct{}                             `xml:"GetAccountAuthorizationDetailsResponse"`
	Xmlns   string                               `xml:"xmlns,attr"`
	Result  getAccountAuthorizationDetailsResult `xml:"GetAccountAuthorizationDetailsResult"`
	Meta    respMeta                             `xml:"ResponseMetadata"`
}
type getAccountAuthorizationDetailsResult struct {
	UserDetailList  listMembersXML[userDetailXML]  `xml:"UserDetailList"`
	GroupDetailList listMembersXML[groupDetailXML] `xml:"GroupDetailList"`
	RoleDetailList  listMembersXML[roleDetailXML]  `xml:"RoleDetailList"`
	Policies        listMembersXML[policyXML]      `xml:"Policies"`
	IsTruncated     bool                           `xml:"IsTruncated"`
}

// ─── Typed handler functions ────────────────────────────────────────────────

// --- Users ---

func (h *Handler) createUserTyped(ctx context.Context, req *createUserReq) (*createUserResp, *protocol.AWSError) {
	name := req.UserName
	path := normPath(req.Path)
	if _, aerr := h.store.getUser(ctx, name); aerr == nil {
		return nil, errEntityAlreadyExists("user", name)
	}
	u := &User{
		UserName:   name,
		UserId:     iamID("AIDA", 17),
		Arn:        h.store.arnForUser(path, name),
		Path:       path,
		CreateDate: h.clk.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}
	if aerr := h.store.putUser(ctx, u); aerr != nil {
		return nil, aerr
	}
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.IAMUserCreated, Time: h.clk.Now(), Source: "iam", Payload: events.ResourcePayload{Name: name}})
	}
	return &createUserResp{Xmlns: iamXMLNS, Result: createUserResult{User: toUserXML(u)}, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) getUserTyped(ctx context.Context, req *getUserReq) (*getUserResp, *protocol.AWSError) {
	name := req.UserName
	if name == "" {
		name = "caller"
	}
	u, aerr := h.store.getUser(ctx, name)
	if aerr != nil {
		return nil, aerr
	}
	return &getUserResp{Xmlns: iamXMLNS, Result: getUserResult{User: toUserXML(u)}, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) listUsersTyped(ctx context.Context, _ *listUsersReq) (*listUsersResp, *protocol.AWSError) {
	users, aerr := h.store.listUsers(ctx)
	if aerr != nil {
		return nil, aerr
	}
	xmlUsers := make([]userXML, 0, len(users))
	for i := range users {
		xmlUsers = append(xmlUsers, toUserXML(&users[i]))
	}
	return &listUsersResp{Xmlns: iamXMLNS, Result: listUsersResult{
		Users: listMembersXML[userXML]{Members: xmlUsers, Tag: "member"}, IsTruncated: false,
	}, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) updateUserTyped(ctx context.Context, req *updateUserReq) (*updateUserResp, *protocol.AWSError) {
	name := req.UserName
	u, aerr := h.store.getUser(ctx, name)
	if aerr != nil {
		return nil, aerr
	}
	if req.NewPath != "" {
		u.Path = req.NewPath
		u.Arn = h.store.arnForUser(req.NewPath, u.UserName)
	}
	if req.NewUserName != "" {
		if aerr := h.store.deleteUser(ctx, name); aerr != nil {
			return nil, aerr
		}
		u.UserName = req.NewUserName
		u.Arn = h.store.arnForUser(u.Path, req.NewUserName)
	}
	if aerr := h.store.putUser(ctx, u); aerr != nil {
		return nil, aerr
	}
	return &updateUserResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) deleteUserTyped(ctx context.Context, req *deleteUserReq) (*deleteUserResp, *protocol.AWSError) {
	if _, aerr := h.store.getUser(ctx, req.UserName); aerr != nil {
		return nil, aerr
	}
	if aerr := h.store.deleteUser(ctx, req.UserName); aerr != nil {
		return nil, aerr
	}
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.IAMUserDeleted, Time: h.clk.Now(), Source: "iam", Payload: events.ResourcePayload{Name: req.UserName}})
	}
	return &deleteUserResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

// --- Access Keys ---

func (h *Handler) createAccessKeyTyped(ctx context.Context, req *createAccessKeyReq) (*createAccessKeyResp, *protocol.AWSError) {
	u, aerr := h.store.getUser(ctx, req.UserName)
	if aerr != nil {
		return nil, aerr
	}
	ak := AccessKey{
		AccessKeyId:     iamID("AKIA", 16),
		SecretAccessKey: randBase64(30),
		Status:          "Active",
		UserName:        req.UserName,
		CreateDate:      h.clk.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}
	u.AccessKeys = append(u.AccessKeys, ak)
	if aerr := h.store.putUser(ctx, u); aerr != nil {
		return nil, aerr
	}
	return &createAccessKeyResp{Xmlns: iamXMLNS, Result: createAccessKeyResult{AccessKey: toAccessKeyXML(&ak)}, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) deleteAccessKeyTyped(ctx context.Context, req *deleteAccessKeyReq) (*deleteAccessKeyResp, *protocol.AWSError) {
	u, aerr := h.store.getUser(ctx, req.UserName)
	if aerr != nil {
		return nil, aerr
	}
	filtered := u.AccessKeys[:0]
	for _, ak := range u.AccessKeys {
		if ak.AccessKeyId != req.AccessKeyId {
			filtered = append(filtered, ak)
		}
	}
	u.AccessKeys = filtered
	if aerr := h.store.putUser(ctx, u); aerr != nil {
		return nil, aerr
	}
	return &deleteAccessKeyResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) listAccessKeysTyped(ctx context.Context, req *listAccessKeysReq) (*listAccessKeysResp, *protocol.AWSError) {
	u, aerr := h.store.getUser(ctx, req.UserName)
	if aerr != nil {
		return nil, aerr
	}
	members := make([]accessKeyXML, len(u.AccessKeys))
	for i := range u.AccessKeys {
		members[i] = toAccessKeyXML(&u.AccessKeys[i])
	}
	return &listAccessKeysResp{Xmlns: iamXMLNS, Result: listAccessKeysResult{
		AccessKeyMetadata: members, IsTruncated: false,
	}, Meta: metaFromCtx(ctx)}, nil
}

// --- Inline User Policies ---

func (h *Handler) putUserPolicyTyped(ctx context.Context, req *putUserPolicyReq) (*putUserPolicyResp, *protocol.AWSError) {
	u, aerr := h.store.getUser(ctx, req.UserName)
	if aerr != nil {
		return nil, aerr
	}
	if u.InlinePolicies == nil {
		u.InlinePolicies = make(map[string]string)
	}
	u.InlinePolicies[req.PolicyName] = req.PolicyDocument
	if aerr := h.store.putUser(ctx, u); aerr != nil {
		return nil, aerr
	}
	return &putUserPolicyResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) getUserPolicyTyped(ctx context.Context, req *getUserPolicyReq) (*getUserPolicyResp, *protocol.AWSError) {
	u, aerr := h.store.getUser(ctx, req.UserName)
	if aerr != nil {
		return nil, aerr
	}
	doc, ok := u.InlinePolicies[req.PolicyName]
	if !ok {
		return nil, errNoSuchEntity("policy", req.PolicyName)
	}
	return &getUserPolicyResp{Xmlns: iamXMLNS, Result: getUserPolicyResult{
		UserName: req.UserName, PolicyName: req.PolicyName, PolicyDocument: url.QueryEscape(doc),
	}, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) deleteUserPolicyTyped(ctx context.Context, req *deleteUserPolicyReq) (*deleteUserPolicyResp, *protocol.AWSError) {
	u, aerr := h.store.getUser(ctx, req.UserName)
	if aerr != nil {
		return nil, aerr
	}
	delete(u.InlinePolicies, req.PolicyName)
	if aerr := h.store.putUser(ctx, u); aerr != nil {
		return nil, aerr
	}
	return &deleteUserPolicyResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

// --- Roles ---

func (h *Handler) createRoleTyped(ctx context.Context, req *createRoleReq) (*createRoleResp, *protocol.AWSError) {
	path := normPath(req.Path)
	if _, aerr := h.store.getRole(ctx, req.RoleName); aerr == nil {
		return nil, errEntityAlreadyExists("role", req.RoleName)
	}
	role := &Role{
		RoleName:                 req.RoleName,
		RoleId:                   iamID("AROA", 17),
		Arn:                      h.store.arnForRole(path, req.RoleName),
		Path:                     path,
		AssumeRolePolicyDocument: req.AssumeRolePolicyDocument,
		CreateDate:               h.clk.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}
	if aerr := h.store.putRole(ctx, role); aerr != nil {
		return nil, aerr
	}
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.IAMRoleCreated, Time: h.clk.Now(), Source: "iam", Payload: events.ResourcePayload{Name: req.RoleName}})
	}
	return &createRoleResp{Xmlns: iamXMLNS, Result: createRoleResult{Role: toRoleXML(role)}, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) getRoleTyped(ctx context.Context, req *getRoleReq) (*getRoleResp, *protocol.AWSError) {
	role, aerr := h.store.getRole(ctx, req.RoleName)
	if aerr != nil {
		return nil, aerr
	}
	return &getRoleResp{Xmlns: iamXMLNS, Result: getRoleResult{Role: toRoleXML(role)}, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) listRolesTyped(ctx context.Context, _ *listRolesReq) (*listRolesResp, *protocol.AWSError) {
	roles, aerr := h.store.listRoles(ctx)
	if aerr != nil {
		return nil, aerr
	}
	xmlRoles := make([]roleXML, 0, len(roles))
	for i := range roles {
		xmlRoles = append(xmlRoles, toRoleXML(&roles[i]))
	}
	return &listRolesResp{Xmlns: iamXMLNS, Result: listRolesResult{
		Roles: listMembersXML[roleXML]{Members: xmlRoles, Tag: "member"}, IsTruncated: false,
	}, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) deleteRoleTyped(ctx context.Context, req *deleteRoleReq) (*deleteRoleResp, *protocol.AWSError) {
	if _, aerr := h.store.getRole(ctx, req.RoleName); aerr != nil {
		return nil, aerr
	}
	if aerr := h.store.deleteRole(ctx, req.RoleName); aerr != nil {
		return nil, aerr
	}
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.IAMRoleDeleted, Time: h.clk.Now(), Source: "iam", Payload: events.ResourcePayload{Name: req.RoleName}})
	}
	return &deleteRoleResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

// --- Inline Role Policies ---

func (h *Handler) putRolePolicyTyped(ctx context.Context, req *putRolePolicyReq) (*putRolePolicyResp, *protocol.AWSError) {
	role, aerr := h.store.getRole(ctx, req.RoleName)
	if aerr != nil {
		return nil, aerr
	}
	if role.InlinePolicies == nil {
		role.InlinePolicies = make(map[string]string)
	}
	role.InlinePolicies[req.PolicyName] = req.PolicyDocument
	if aerr := h.store.putRole(ctx, role); aerr != nil {
		return nil, aerr
	}
	return &putRolePolicyResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) getRolePolicyTyped(ctx context.Context, req *getRolePolicyReq) (*getRolePolicyResp, *protocol.AWSError) {
	role, aerr := h.store.getRole(ctx, req.RoleName)
	if aerr != nil {
		return nil, aerr
	}
	doc, ok := role.InlinePolicies[req.PolicyName]
	if !ok {
		return nil, errNoSuchEntity("policy", req.PolicyName)
	}
	return &getRolePolicyResp{Xmlns: iamXMLNS, Result: getRolePolicyResult{
		RoleName: req.RoleName, PolicyName: req.PolicyName, PolicyDocument: url.QueryEscape(doc),
	}, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) listRolePoliciesTyped(ctx context.Context, req *listRolePoliciesReq) (*listRolePoliciesResp, *protocol.AWSError) {
	role, aerr := h.store.getRole(ctx, req.RoleName)
	if aerr != nil {
		return nil, aerr
	}
	names := make([]string, 0, len(role.InlinePolicies))
	for name := range role.InlinePolicies {
		names = append(names, name)
	}
	return &listRolePoliciesResp{Xmlns: iamXMLNS, Result: listRolePoliciesResult{
		PolicyNames: listMembersXML[string]{Members: names, Tag: "member"}, IsTruncated: false,
	}, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) deleteRolePolicyTyped(ctx context.Context, req *deleteRolePolicyReq) (*deleteRolePolicyResp, *protocol.AWSError) {
	role, aerr := h.store.getRole(ctx, req.RoleName)
	if aerr != nil {
		return nil, aerr
	}
	delete(role.InlinePolicies, req.PolicyName)
	if aerr := h.store.putRole(ctx, role); aerr != nil {
		return nil, aerr
	}
	return &deleteRolePolicyResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

// --- Managed Role Policies ---

func (h *Handler) attachRolePolicyTyped(ctx context.Context, req *attachRolePolicyReq) (*attachRolePolicyResp, *protocol.AWSError) {
	role, aerr := h.store.getRole(ctx, req.RoleName)
	if aerr != nil {
		return nil, aerr
	}
	role.AttachedPolicies = ensureAttachedPolicy(role.AttachedPolicies, req.PolicyArn)
	if aerr := h.store.putRole(ctx, role); aerr != nil {
		return nil, aerr
	}
	return &attachRolePolicyResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) detachRolePolicyTyped(ctx context.Context, req *detachRolePolicyReq) (*detachRolePolicyResp, *protocol.AWSError) {
	role, aerr := h.store.getRole(ctx, req.RoleName)
	if aerr != nil {
		return nil, aerr
	}
	role.AttachedPolicies = removeAttachedPolicy(role.AttachedPolicies, req.PolicyArn)
	if aerr := h.store.putRole(ctx, role); aerr != nil {
		return nil, aerr
	}
	return &detachRolePolicyResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) listAttachedRolePoliciesTyped(ctx context.Context, req *listAttachedRolePoliciesReq) (*listAttachedRolePoliciesResp, *protocol.AWSError) {
	role, aerr := h.store.getRole(ctx, req.RoleName)
	if aerr != nil {
		return nil, aerr
	}
	xmlPolicies := make([]attachedPolicyXML, 0, len(role.AttachedPolicies))
	for _, ap := range role.AttachedPolicies {
		xmlPolicies = append(xmlPolicies, attachedPolicyXML(ap))
	}
	return &listAttachedRolePoliciesResp{Xmlns: iamXMLNS, Result: listAttachedRolePoliciesResult{
		AttachedPolicies: listMembersXML[attachedPolicyXML]{Members: xmlPolicies, Tag: "member"}, IsTruncated: false,
	}, Meta: metaFromCtx(ctx)}, nil
}

// --- Instance Profiles ---

func (h *Handler) createInstanceProfileTyped(ctx context.Context, req *createInstanceProfileReq) (*createInstanceProfileResp, *protocol.AWSError) {
	path := normPath(req.Path)
	if _, aerr := h.store.getProfile(ctx, req.InstanceProfileName); aerr == nil {
		return nil, errEntityAlreadyExists("instance profile", req.InstanceProfileName)
	}
	profile := &InstanceProfile{
		InstanceProfileName: req.InstanceProfileName,
		InstanceProfileId:   iamID("AIPA", 17),
		Arn:                 h.store.arnForProfile(path, req.InstanceProfileName),
		Path:                path,
		CreateDate:          h.clk.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}
	if aerr := h.store.putProfile(ctx, profile); aerr != nil {
		return nil, aerr
	}
	return &createInstanceProfileResp{Xmlns: iamXMLNS, Result: createInstanceProfileResult{
		InstanceProfile: toInstanceProfileXML(profile, nil),
	}, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) deleteInstanceProfileTyped(ctx context.Context, req *deleteInstanceProfileReq) (*deleteInstanceProfileResp, *protocol.AWSError) {
	if _, aerr := h.store.getProfile(ctx, req.InstanceProfileName); aerr != nil {
		return nil, aerr
	}
	if aerr := h.store.deleteProfile(ctx, req.InstanceProfileName); aerr != nil {
		return nil, aerr
	}
	return &deleteInstanceProfileResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

func toInstanceProfileResp(ctx context.Context, store *iamStore, profile *InstanceProfile) instanceProfileXML {
	var roles []roleXML
	for _, rn := range profile.Roles {
		if role, aerr := store.getRole(ctx, rn); aerr == nil {
			roles = append(roles, toRoleXML(role))
		}
	}
	return toInstanceProfileXML(profile, roles)
}

func (h *Handler) getInstanceProfileTyped(ctx context.Context, req *getInstanceProfileReq) (*getInstanceProfileResp, *protocol.AWSError) {
	profile, aerr := h.store.getProfile(ctx, req.InstanceProfileName)
	if aerr != nil {
		return nil, aerr
	}
	return &getInstanceProfileResp{Xmlns: iamXMLNS, Result: getInstanceProfileResult{
		InstanceProfile: toInstanceProfileResp(ctx, h.store, profile),
	}, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) addRoleToInstanceProfileTyped(ctx context.Context, req *addRoleToInstanceProfileReq) (*addRoleToInstanceProfileResp, *protocol.AWSError) {
	profile, aerr := h.store.getProfile(ctx, req.InstanceProfileName)
	if aerr != nil {
		return nil, aerr
	}
	if _, aerr := h.store.getRole(ctx, req.RoleName); aerr != nil {
		return nil, aerr
	}
	for _, rn := range profile.Roles {
		if rn == req.RoleName {
			return &addRoleToInstanceProfileResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
		}
	}
	profile.Roles = append(profile.Roles, req.RoleName)
	if aerr := h.store.putProfile(ctx, profile); aerr != nil {
		return nil, aerr
	}
	return &addRoleToInstanceProfileResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) removeRoleFromInstanceProfileTyped(ctx context.Context, req *removeRoleFromInstanceProfileReq) (*removeRoleFromInstanceProfileResp, *protocol.AWSError) {
	profile, aerr := h.store.getProfile(ctx, req.InstanceProfileName)
	if aerr != nil {
		return nil, aerr
	}
	filtered := profile.Roles[:0]
	for _, rn := range profile.Roles {
		if rn != req.RoleName {
			filtered = append(filtered, rn)
		}
	}
	profile.Roles = filtered
	if aerr := h.store.putProfile(ctx, profile); aerr != nil {
		return nil, aerr
	}
	return &removeRoleFromInstanceProfileResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

// --- Managed Policies ---

func (h *Handler) createPolicyTyped(ctx context.Context, req *createPolicyReq) (*createPolicyResp, *protocol.AWSError) {
	path := normPath(req.Path)
	arn := h.store.arnForPolicy(path, req.PolicyName)
	if _, aerr := h.store.getPolicy(ctx, arn); aerr == nil {
		return nil, errEntityAlreadyExists("policy", req.PolicyName)
	}
	p := &Policy{
		PolicyName: req.PolicyName,
		PolicyId:   iamID("ANPA", 17),
		Arn:        arn,
		Path:       path,
		Document:   req.PolicyDocument,
		CreateDate: h.clk.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}
	if aerr := h.store.putPolicy(ctx, p); aerr != nil {
		return nil, aerr
	}
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.IAMPolicyCreated, Time: h.clk.Now(), Source: "iam", Payload: events.ResourcePayload{Name: req.PolicyName}})
	}
	return &createPolicyResp{Xmlns: iamXMLNS, Result: createPolicyResult{Policy: toPolicyXML(p)}, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) getPolicyTyped(ctx context.Context, req *getPolicyReq) (*getPolicyResp, *protocol.AWSError) {
	p, aerr := h.store.getPolicy(ctx, req.PolicyArn)
	if aerr != nil {
		return nil, aerr
	}
	return &getPolicyResp{Xmlns: iamXMLNS, Result: getPolicyResult{Policy: toPolicyXML(p)}, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) listPoliciesTyped(ctx context.Context, _ *listPoliciesReq) (*listPoliciesResp, *protocol.AWSError) {
	policies, aerr := h.store.listPolicies(ctx)
	if aerr != nil {
		return nil, aerr
	}
	xmlPolicies := make([]policyXML, 0, len(policies))
	for i := range policies {
		xmlPolicies = append(xmlPolicies, toPolicyXML(&policies[i]))
	}
	return &listPoliciesResp{Xmlns: iamXMLNS, Result: listPoliciesResult{
		Policies: listMembersXML[policyXML]{Members: xmlPolicies, Tag: "member"}, IsTruncated: false,
	}, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) deletePolicyTyped(ctx context.Context, req *deletePolicyReq) (*deletePolicyResp, *protocol.AWSError) {
	if _, aerr := h.store.getPolicy(ctx, req.PolicyArn); aerr != nil {
		return nil, aerr
	}
	if aerr := h.store.deletePolicy(ctx, req.PolicyArn); aerr != nil {
		return nil, aerr
	}
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.IAMPolicyDeleted, Time: h.clk.Now(), Source: "iam", Payload: events.ResourcePayload{Name: req.PolicyArn}})
	}
	return &deletePolicyResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

// --- Groups ---

func (h *Handler) createGroupTyped(ctx context.Context, req *createGroupReq) (*createGroupResp, *protocol.AWSError) {
	path := normPath(req.Path)
	if _, aerr := h.store.getGroup(ctx, req.GroupName); aerr == nil {
		return nil, errEntityAlreadyExists("group", req.GroupName)
	}
	g := &Group{
		GroupName:  req.GroupName,
		GroupId:    iamID("AGPA", 17),
		Arn:        h.store.arnForGroup(path, req.GroupName),
		Path:       path,
		CreateDate: h.clk.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}
	if aerr := h.store.putGroup(ctx, g); aerr != nil {
		return nil, aerr
	}
	return &createGroupResp{Xmlns: iamXMLNS, Result: createGroupResult{Group: toGroupXML(g)}, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) getGroupTyped(ctx context.Context, req *getGroupReq) (*getGroupResp, *protocol.AWSError) {
	g, aerr := h.store.getGroup(ctx, req.GroupName)
	if aerr != nil {
		return nil, aerr
	}
	return &getGroupResp{Xmlns: iamXMLNS, Result: getGroupResult{
		Group: toGroupXML(g), Users: listMembersXML[userXML]{Members: nil, Tag: "member"}, IsTruncated: false,
	}, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) deleteGroupTyped(ctx context.Context, req *deleteGroupReq) (*deleteGroupResp, *protocol.AWSError) {
	if _, aerr := h.store.getGroup(ctx, req.GroupName); aerr != nil {
		return nil, aerr
	}
	if aerr := h.store.deleteGroup(ctx, req.GroupName); aerr != nil {
		return nil, aerr
	}
	return &deleteGroupResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) addUserToGroupTyped(ctx context.Context, req *addUserToGroupReq) (*addUserToGroupResp, *protocol.AWSError) {
	g, aerr := h.store.getGroup(ctx, req.GroupName)
	if aerr != nil {
		return nil, aerr
	}
	for _, m := range g.Members {
		if m == req.UserName {
			return &addUserToGroupResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
		}
	}
	g.Members = append(g.Members, req.UserName)
	if aerr := h.store.putGroup(ctx, g); aerr != nil {
		return nil, aerr
	}
	return &addUserToGroupResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) removeUserFromGroupTyped(ctx context.Context, req *removeUserFromGroupReq) (*removeUserFromGroupResp, *protocol.AWSError) {
	g, aerr := h.store.getGroup(ctx, req.GroupName)
	if aerr != nil {
		return nil, aerr
	}
	filtered := g.Members[:0]
	for _, m := range g.Members {
		if m != req.UserName {
			filtered = append(filtered, m)
		}
	}
	g.Members = filtered
	if aerr := h.store.putGroup(ctx, g); aerr != nil {
		return nil, aerr
	}
	return &removeUserFromGroupResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) listGroupsForUserTyped(ctx context.Context, req *listGroupsForUserReq) (*listGroupsForUserResp, *protocol.AWSError) {
	groups, aerr := h.store.listGroupsForUser(ctx, req.UserName)
	if aerr != nil {
		return nil, aerr
	}
	xmlGroups := make([]groupXML, 0, len(groups))
	for i := range groups {
		xmlGroups = append(xmlGroups, toGroupXML(&groups[i]))
	}
	return &listGroupsForUserResp{Xmlns: iamXMLNS, Result: listGroupsForUserResult{
		Groups: listMembersXML[groupXML]{Members: xmlGroups, Tag: "member"}, IsTruncated: false,
	}, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) listGroupsTyped(ctx context.Context, _ *listGroupsReq) (*listGroupsResp, *protocol.AWSError) {
	groups, aerr := h.store.listGroups(ctx)
	if aerr != nil {
		return nil, aerr
	}
	xmlGroups := make([]groupXML, 0, len(groups))
	for i := range groups {
		xmlGroups = append(xmlGroups, toGroupXML(&groups[i]))
	}
	return &listGroupsResp{Xmlns: iamXMLNS, Result: listGroupsResult{
		Groups: listMembersXML[groupXML]{Members: xmlGroups, Tag: "member"}, IsTruncated: false,
	}, Meta: metaFromCtx(ctx)}, nil
}

// --- Inline Group Policies ---

func (h *Handler) putGroupPolicyTyped(ctx context.Context, req *putGroupPolicyReq) (*putGroupPolicyResp, *protocol.AWSError) {
	g, aerr := h.store.getGroup(ctx, req.GroupName)
	if aerr != nil {
		return nil, aerr
	}
	if g.InlinePolicies == nil {
		g.InlinePolicies = make(map[string]string)
	}
	g.InlinePolicies[req.PolicyName] = req.PolicyDocument
	if aerr := h.store.putGroup(ctx, g); aerr != nil {
		return nil, aerr
	}
	return &putGroupPolicyResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) getGroupPolicyTyped(ctx context.Context, req *getGroupPolicyReq) (*getGroupPolicyResp, *protocol.AWSError) {
	g, aerr := h.store.getGroup(ctx, req.GroupName)
	if aerr != nil {
		return nil, aerr
	}
	doc, ok := g.InlinePolicies[req.PolicyName]
	if !ok {
		return nil, errNoSuchEntity("policy", req.PolicyName)
	}
	return &getGroupPolicyResp{Xmlns: iamXMLNS, Result: getGroupPolicyResult{
		GroupName: req.GroupName, PolicyName: req.PolicyName, PolicyDocument: url.QueryEscape(doc),
	}, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) deleteGroupPolicyTyped(ctx context.Context, req *deleteGroupPolicyReq) (*deleteGroupPolicyResp, *protocol.AWSError) {
	g, aerr := h.store.getGroup(ctx, req.GroupName)
	if aerr != nil {
		return nil, aerr
	}
	delete(g.InlinePolicies, req.PolicyName)
	if aerr := h.store.putGroup(ctx, g); aerr != nil {
		return nil, aerr
	}
	return &deleteGroupPolicyResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) listGroupPoliciesTyped(ctx context.Context, req *listGroupPoliciesReq) (*listGroupPoliciesResp, *protocol.AWSError) {
	g, aerr := h.store.getGroup(ctx, req.GroupName)
	if aerr != nil {
		return nil, aerr
	}
	names := make([]string, 0, len(g.InlinePolicies))
	for name := range g.InlinePolicies {
		names = append(names, name)
	}
	return &listGroupPoliciesResp{Xmlns: iamXMLNS, Result: listGroupPoliciesResult{
		PolicyNames: listMembersXML[string]{Members: names, Tag: "member"}, IsTruncated: false,
	}, Meta: metaFromCtx(ctx)}, nil
}

// --- Managed Group Policies ---

func (h *Handler) attachGroupPolicyTyped(ctx context.Context, req *attachGroupPolicyReq) (*attachGroupPolicyResp, *protocol.AWSError) {
	g, aerr := h.store.getGroup(ctx, req.GroupName)
	if aerr != nil {
		return nil, aerr
	}
	g.AttachedPolicies = ensureAttachedPolicy(g.AttachedPolicies, req.PolicyArn)
	if aerr := h.store.putGroup(ctx, g); aerr != nil {
		return nil, aerr
	}
	return &attachGroupPolicyResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) detachGroupPolicyTyped(ctx context.Context, req *detachGroupPolicyReq) (*detachGroupPolicyResp, *protocol.AWSError) {
	g, aerr := h.store.getGroup(ctx, req.GroupName)
	if aerr != nil {
		return nil, aerr
	}
	g.AttachedPolicies = removeAttachedPolicy(g.AttachedPolicies, req.PolicyArn)
	if aerr := h.store.putGroup(ctx, g); aerr != nil {
		return nil, aerr
	}
	return &detachGroupPolicyResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) listAttachedGroupPoliciesTyped(ctx context.Context, req *listAttachedGroupPoliciesReq) (*listAttachedGroupPoliciesResp, *protocol.AWSError) {
	g, aerr := h.store.getGroup(ctx, req.GroupName)
	if aerr != nil {
		return nil, aerr
	}
	xmlPolicies := make([]attachedPolicyXML, 0, len(g.AttachedPolicies))
	for _, ap := range g.AttachedPolicies {
		xmlPolicies = append(xmlPolicies, attachedPolicyXML(ap))
	}
	return &listAttachedGroupPoliciesResp{Xmlns: iamXMLNS, Result: listAttachedGroupPoliciesResult{
		AttachedPolicies: listMembersXML[attachedPolicyXML]{Members: xmlPolicies, Tag: "member"}, IsTruncated: false,
	}, Meta: metaFromCtx(ctx)}, nil
}

// --- Managed User Policies ---

func (h *Handler) attachUserPolicyTyped(ctx context.Context, req *attachUserPolicyReq) (*attachUserPolicyResp, *protocol.AWSError) {
	u, aerr := h.store.getUser(ctx, req.UserName)
	if aerr != nil {
		return nil, aerr
	}
	u.AttachedPolicies = ensureAttachedPolicy(u.AttachedPolicies, req.PolicyArn)
	if aerr := h.store.putUser(ctx, u); aerr != nil {
		return nil, aerr
	}
	return &attachUserPolicyResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) detachUserPolicyTyped(ctx context.Context, req *detachUserPolicyReq) (*detachUserPolicyResp, *protocol.AWSError) {
	u, aerr := h.store.getUser(ctx, req.UserName)
	if aerr != nil {
		return nil, aerr
	}
	u.AttachedPolicies = removeAttachedPolicy(u.AttachedPolicies, req.PolicyArn)
	if aerr := h.store.putUser(ctx, u); aerr != nil {
		return nil, aerr
	}
	return &detachUserPolicyResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) listAttachedUserPoliciesTyped(ctx context.Context, req *listAttachedUserPoliciesReq) (*listAttachedUserPoliciesResp, *protocol.AWSError) {
	u, aerr := h.store.getUser(ctx, req.UserName)
	if aerr != nil {
		return nil, aerr
	}
	xmlPolicies := make([]attachedPolicyXML, 0, len(u.AttachedPolicies))
	for _, ap := range u.AttachedPolicies {
		xmlPolicies = append(xmlPolicies, attachedPolicyXML(ap))
	}
	return &listAttachedUserPoliciesResp{Xmlns: iamXMLNS, Result: listAttachedUserPoliciesResult{
		AttachedPolicies: listMembersXML[attachedPolicyXML]{Members: xmlPolicies, Tag: "member"}, IsTruncated: false,
	}, Meta: metaFromCtx(ctx)}, nil
}

// --- Inline User Policy Listing ---

func (h *Handler) listUserPoliciesTyped(ctx context.Context, req *listUserPoliciesReq) (*listUserPoliciesResp, *protocol.AWSError) {
	u, aerr := h.store.getUser(ctx, req.UserName)
	if aerr != nil {
		return nil, aerr
	}
	names := make([]string, 0, len(u.InlinePolicies))
	for name := range u.InlinePolicies {
		names = append(names, name)
	}
	return &listUserPoliciesResp{Xmlns: iamXMLNS, Result: listUserPoliciesResult{
		PolicyNames: listMembersXML[string]{Members: names, Tag: "member"}, IsTruncated: false,
	}, Meta: metaFromCtx(ctx)}, nil
}

// --- Role Tagging ---

func (h *Handler) tagRoleTyped(ctx context.Context, req *tagRoleReq) (*tagRoleResp, *protocol.AWSError) {
	role, aerr := h.store.getRole(ctx, req.RoleName)
	if aerr != nil {
		return nil, aerr
	}
	if role.Tags == nil {
		role.Tags = make(map[string]string)
	}
	for _, t := range req.Tags {
		role.Tags[t.Key] = t.Value
	}
	if aerr := h.store.putRole(ctx, role); aerr != nil {
		return nil, aerr
	}
	return &tagRoleResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) untagRoleTyped(ctx context.Context, req *untagRoleReq) (*untagRoleResp, *protocol.AWSError) {
	role, aerr := h.store.getRole(ctx, req.RoleName)
	if aerr != nil {
		return nil, aerr
	}
	for _, k := range req.TagKeys {
		delete(role.Tags, k)
	}
	if aerr := h.store.putRole(ctx, role); aerr != nil {
		return nil, aerr
	}
	return &untagRoleResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) listRoleTagsTyped(ctx context.Context, req *listRoleTagsReq) (*listRoleTagsResp, *protocol.AWSError) {
	role, aerr := h.store.getRole(ctx, req.RoleName)
	if aerr != nil {
		return nil, aerr
	}
	tags := make([]tagXML, 0, len(role.Tags))
	for k, v := range role.Tags {
		tags = append(tags, tagXML{Key: k, Value: v})
	}
	return &listRoleTagsResp{Xmlns: iamXMLNS, Result: listRoleTagsResult{
		Tags: listMembersXML[tagXML]{Members: tags, Tag: "member"}, IsTruncated: false,
	}, Meta: metaFromCtx(ctx)}, nil
}

// --- User Tagging ---

func (h *Handler) tagUserTyped(ctx context.Context, req *tagUserReq) (*tagUserResp, *protocol.AWSError) {
	u, aerr := h.store.getUser(ctx, req.UserName)
	if aerr != nil {
		return nil, aerr
	}
	if u.Tags == nil {
		u.Tags = make(map[string]string)
	}
	for _, t := range req.Tags {
		u.Tags[t.Key] = t.Value
	}
	if aerr := h.store.putUser(ctx, u); aerr != nil {
		return nil, aerr
	}
	return &tagUserResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) untagUserTyped(ctx context.Context, req *untagUserReq) (*untagUserResp, *protocol.AWSError) {
	u, aerr := h.store.getUser(ctx, req.UserName)
	if aerr != nil {
		return nil, aerr
	}
	for _, k := range req.TagKeys {
		delete(u.Tags, k)
	}
	if aerr := h.store.putUser(ctx, u); aerr != nil {
		return nil, aerr
	}
	return &untagUserResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

func (h *Handler) listUserTagsTyped(ctx context.Context, req *listUserTagsReq) (*listUserTagsResp, *protocol.AWSError) {
	u, aerr := h.store.getUser(ctx, req.UserName)
	if aerr != nil {
		return nil, aerr
	}
	tags := make([]tagXML, 0, len(u.Tags))
	for k, v := range u.Tags {
		tags = append(tags, tagXML{Key: k, Value: v})
	}
	return &listUserTagsResp{Xmlns: iamXMLNS, Result: listUserTagsResult{
		Tags: listMembersXML[tagXML]{Members: tags, Tag: "member"}, IsTruncated: false,
	}, Meta: metaFromCtx(ctx)}, nil
}

// --- Service-Linked Roles ---

func (h *Handler) createServiceLinkedRoleTyped(ctx context.Context, req *createServiceLinkedRoleReq) (*createServiceLinkedRoleResp, *protocol.AWSError) {
	serviceName := req.AWSServiceName
	suffix := serviceName
	if idx := len(suffix) - len(".amazonaws.com"); idx > 0 && suffix[idx:] == ".amazonaws.com" {
		suffix = suffix[:idx]
	}
	roleName := "AWSServiceRoleFor" + strings.ToUpper(suffix[:1]) + suffix[1:]
	path := "/aws-service-role/" + serviceName + "/"

	if _, aerr := h.store.getRole(ctx, roleName); aerr == nil {
		return nil, errEntityAlreadyExists("role", roleName)
	}
	trustDoc := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"` + serviceName + `"},"Action":"sts:AssumeRole"}]}`
	role := &Role{
		RoleName:                 roleName,
		RoleId:                   iamID("AROA", 17),
		Arn:                      h.store.arnForRole(path, roleName),
		Path:                     path,
		AssumeRolePolicyDocument: trustDoc,
		CreateDate:               h.clk.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}
	if aerr := h.store.putRole(ctx, role); aerr != nil {
		return nil, aerr
	}
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.IAMRoleCreated, Time: h.clk.Now(), Source: "iam", Payload: events.ResourcePayload{Name: roleName}})
	}
	return &createServiceLinkedRoleResp{Xmlns: iamXMLNS, Result: createServiceLinkedRoleResult{Role: toRoleXML(role)}, Meta: metaFromCtx(ctx)}, nil
}

// --- Instance Profiles For Role ---

func (h *Handler) listInstanceProfilesForRoleTyped(ctx context.Context, req *listInstanceProfilesForRoleReq) (*listInstanceProfilesForRoleResp, *protocol.AWSError) {
	profiles, aerr := h.store.listProfilesForRole(ctx, req.RoleName)
	if aerr != nil {
		return nil, aerr
	}
	var xmlProfiles []instanceProfileXML
	for i := range profiles {
		xmlProfiles = append(xmlProfiles, toInstanceProfileResp(ctx, h.store, &profiles[i]))
	}
	return &listInstanceProfilesForRoleResp{Xmlns: iamXMLNS, Result: listInstanceProfilesForRoleResult{
		InstanceProfiles: listMembersXML[instanceProfileXML]{Members: xmlProfiles, Tag: "member"}, IsTruncated: false,
	}, Meta: metaFromCtx(ctx)}, nil
}

// --- Role Mutation ---

func (h *Handler) updateAssumeRolePolicyTyped(ctx context.Context, req *updateAssumeRolePolicyReq) (*updateAssumeRolePolicyResp, *protocol.AWSError) {
	role, aerr := h.store.getRole(ctx, req.RoleName)
	if aerr != nil {
		return nil, aerr
	}
	role.AssumeRolePolicyDocument = req.PolicyDocument
	if aerr := h.store.putRole(ctx, role); aerr != nil {
		return nil, aerr
	}
	return &updateAssumeRolePolicyResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

// --- List Instance Profiles ---

func (h *Handler) listInstanceProfilesTyped(ctx context.Context, _ *listInstanceProfilesReq) (*listInstanceProfilesResp, *protocol.AWSError) {
	profiles, aerr := h.store.listProfiles(ctx)
	if aerr != nil {
		return nil, aerr
	}
	var xmlProfiles []instanceProfileXML
	for i := range profiles {
		xmlProfiles = append(xmlProfiles, toInstanceProfileResp(ctx, h.store, &profiles[i]))
	}
	return &listInstanceProfilesResp{Xmlns: iamXMLNS, Result: listInstanceProfilesResult{
		InstanceProfiles: listMembersXML[instanceProfileXML]{Members: xmlProfiles, Tag: "member"}, IsTruncated: false,
	}, Meta: metaFromCtx(ctx)}, nil
}

// --- Policy Simulation ---

func (h *Handler) simulatePrincipalPolicyTyped(ctx context.Context, req *simulatePrincipalPolicyReq) (*simulatePrincipalPolicyResp, *protocol.AWSError) {
	resource := "*"
	if len(req.ResourceArns) > 0 {
		resource = req.ResourceArns[0]
	}
	results := make([]evalResult, 0, len(req.ActionNames))
	for _, a := range req.ActionNames {
		results = append(results, evalResult{
			EvalActionName:   a,
			EvalDecision:     "allowed",
			EvalResourceName: resource,
		})
	}
	return &simulatePrincipalPolicyResp{Xmlns: iamXMLNS, Result: simulatePrincipalPolicyResult{
		EvaluationResults: listMembersXML[evalResult]{Members: results, Tag: "member"}, IsTruncated: false,
	}, Meta: metaFromCtx(ctx)}, nil
}

// --- GetAccountAuthorizationDetails ---

func (h *Handler) getAccountAuthorizationDetailsTyped(ctx context.Context, _ *getAccountAuthorizationDetailsReq) (*getAccountAuthorizationDetailsResp, *protocol.AWSError) {
	users, aerr := h.store.listUsers(ctx)
	if aerr != nil {
		return nil, aerr
	}
	groups, aerr := h.store.listGroups(ctx)
	if aerr != nil {
		return nil, aerr
	}
	roles, aerr := h.store.listRoles(ctx)
	if aerr != nil {
		return nil, aerr
	}
	policies, aerr := h.store.listPolicies(ctx)
	if aerr != nil {
		return nil, aerr
	}

	userDetails := make([]userDetailXML, 0, len(users))
	for _, u := range users {
		userDetails = append(userDetails, userDetailXML{
			Path:                    u.Path,
			UserName:                u.UserName,
			UserId:                  u.UserId,
			Arn:                     u.Arn,
			CreateDate:              u.CreateDate,
			UserPolicyList:          inlinePolicyListXML(u.InlinePolicies),
			AttachedManagedPolicies: attachedPolicyListXML(u.AttachedPolicies),
		})
	}
	groupDetails := make([]groupDetailXML, 0, len(groups))
	for _, g := range groups {
		groupDetails = append(groupDetails, groupDetailXML{
			Path:                    g.Path,
			GroupName:               g.GroupName,
			GroupId:                 g.GroupId,
			Arn:                     g.Arn,
			CreateDate:              g.CreateDate,
			GroupPolicyList:         inlinePolicyListXML(g.InlinePolicies),
			AttachedManagedPolicies: attachedPolicyListXML(g.AttachedPolicies),
		})
	}
	roleDetails := make([]roleDetailXML, 0, len(roles))
	for _, ro := range roles {
		roleDetails = append(roleDetails, roleDetailXML{
			Path:                     ro.Path,
			RoleName:                 ro.RoleName,
			RoleId:                   ro.RoleId,
			Arn:                      ro.Arn,
			CreateDate:               ro.CreateDate,
			AssumeRolePolicyDocument: ro.AssumeRolePolicyDocument,
			RolePolicyList:           inlinePolicyListXML(ro.InlinePolicies),
			AttachedManagedPolicies:  attachedPolicyListXML(ro.AttachedPolicies),
		})
	}
	policyDetails := make([]policyXML, 0, len(policies))
	for i := range policies {
		policyDetails = append(policyDetails, toPolicyXML(&policies[i]))
	}

	return &getAccountAuthorizationDetailsResp{Xmlns: iamXMLNS, Result: getAccountAuthorizationDetailsResult{
		UserDetailList:  listMembersXML[userDetailXML]{Members: userDetails, Tag: "member"},
		GroupDetailList: listMembersXML[groupDetailXML]{Members: groupDetails, Tag: "member"},
		RoleDetailList:  listMembersXML[roleDetailXML]{Members: roleDetails, Tag: "member"},
		Policies:        listMembersXML[policyXML]{Members: policyDetails, Tag: "member"},
		IsTruncated:     false,
	}, Meta: metaFromCtx(ctx)}, nil
}

// typedPublish emits an event on the bus if wired, using context instead of *http.Request.
//
//nolint:unused // Kept for typed IAM operations that publish events.
func (h *Handler) typedPublish(ctx context.Context, t events.Type, payload any) {
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{
			Type: t, Time: h.clk.Now(), Source: "iam", Payload: payload,
		})
	}
}

var _ = json.Marshal // keep json import
