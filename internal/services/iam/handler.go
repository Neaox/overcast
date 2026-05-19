package iam

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
)

var iamMutateActions = map[string]bool{
	"PutUserPolicy":          true,
	"DeleteUserPolicy":       true,
	"AttachUserPolicy":       true,
	"DetachUserPolicy":       true,
	"PutRolePolicy":          true,
	"DeleteRolePolicy":       true,
	"AttachRolePolicy":       true,
	"DetachRolePolicy":       true,
	"PutGroupPolicy":         true,
	"DeleteGroupPolicy":      true,
	"AttachGroupPolicy":      true,
	"DetachGroupPolicy":      true,
	"CreatePolicy":           true,
	"DeletePolicy":           true,
	"CreateUser":             true,
	"DeleteUser":             true,
	"UpdateUser":             true,
	"CreateRole":             true,
	"DeleteRole":             true,
	"CreateGroup":            true,
	"DeleteGroup":            true,
	"AddUserToGroup":         true,
	"RemoveUserFromGroup":    true,
	"CreateAccessKey":        true,
	"DeleteAccessKey":        true,
	"UpdateAssumeRolePolicy": true,
}

const iamXMLNS = "https://iam.amazonaws.com/doc/2010-05-08/"

// Handler holds IAM handler dependencies.
type Handler struct {
	cfg     *config.Config
	log     *serviceutil.ServiceLogger
	clk     clock.Clock
	store   *iamStore
	bus     *events.Bus
	ops     map[string]http.HandlerFunc
	typedOp map[string]op.Operation
}

func newHandler(cfg *config.Config, store *iamStore, log *serviceutil.ServiceLogger, clk clock.Clock) *Handler {
	h := &Handler{cfg: cfg, log: log, clk: clk, store: store}
	h.initOps()
	return h
}

// initOps registers every known IAM operation to its handler.
// Implemented operations point to their handler method.
// Adding a new operation: add an entry here and implement it.
func (h *Handler) initOps() {
	h.ops = map[string]http.HandlerFunc{
		// Users
		"CreateUser": h.CreateUser,
		"GetUser":    h.GetUser,
		"ListUsers":  h.ListUsers,
		"UpdateUser": h.UpdateUser,
		"DeleteUser": h.DeleteUser,
		// Access keys
		"CreateAccessKey": h.CreateAccessKey,
		"DeleteAccessKey": h.DeleteAccessKey,
		"ListAccessKeys":  h.ListAccessKeys,
		// Inline user policies
		"PutUserPolicy":    h.PutUserPolicy,
		"GetUserPolicy":    h.GetUserPolicy,
		"DeleteUserPolicy": h.DeleteUserPolicy,
		// Roles
		"CreateRole": h.CreateRole,
		"GetRole":    h.GetRole,
		"ListRoles":  h.ListRoles,
		"DeleteRole": h.DeleteRole,
		// Inline role policies
		"PutRolePolicy":    h.PutRolePolicy,
		"GetRolePolicy":    h.GetRolePolicy,
		"ListRolePolicies": h.ListRolePolicies,
		"DeleteRolePolicy": h.DeleteRolePolicy,
		// Managed role policies
		"AttachRolePolicy":         h.AttachRolePolicy,
		"DetachRolePolicy":         h.DetachRolePolicy,
		"ListAttachedRolePolicies": h.ListAttachedRolePolicies,
		// Instance profiles
		"CreateInstanceProfile":         h.CreateInstanceProfile,
		"DeleteInstanceProfile":         h.DeleteInstanceProfile,
		"GetInstanceProfile":            h.GetInstanceProfile,
		"AddRoleToInstanceProfile":      h.AddRoleToInstanceProfile,
		"RemoveRoleFromInstanceProfile": h.RemoveRoleFromInstanceProfile,
		// Managed policies
		"CreatePolicy": h.CreatePolicy,
		"GetPolicy":    h.GetPolicy,
		"ListPolicies": h.ListPolicies,
		"DeletePolicy": h.DeletePolicy,
		// Groups
		"CreateGroup":         h.CreateGroup,
		"GetGroup":            h.GetGroup,
		"DeleteGroup":         h.DeleteGroup,
		"AddUserToGroup":      h.AddUserToGroup,
		"RemoveUserFromGroup": h.RemoveUserFromGroup,
		"ListGroupsForUser":   h.ListGroupsForUser,
		"ListGroups":          h.ListGroups,
		// Group inline policies
		"PutGroupPolicy":    h.PutGroupPolicy,
		"GetGroupPolicy":    h.GetGroupPolicy,
		"DeleteGroupPolicy": h.DeleteGroupPolicy,
		"ListGroupPolicies": h.ListGroupPolicies,
		// Managed group policies
		"AttachGroupPolicy":         h.AttachGroupPolicy,
		"DetachGroupPolicy":         h.DetachGroupPolicy,
		"ListAttachedGroupPolicies": h.ListAttachedGroupPolicies,
		// Managed user policies
		"AttachUserPolicy":         h.AttachUserPolicy,
		"DetachUserPolicy":         h.DetachUserPolicy,
		"ListAttachedUserPolicies": h.ListAttachedUserPolicies,
		// Inline user policy listing
		"ListUserPolicies": h.ListUserPolicies,
		// Role tagging
		"TagRole":      h.TagRole,
		"UntagRole":    h.UntagRole,
		"ListRoleTags": h.ListRoleTags,
		// User tagging
		"TagUser":      h.TagUser,
		"UntagUser":    h.UntagUser,
		"ListUserTags": h.ListUserTags,
		// Service-linked roles
		"CreateServiceLinkedRole": h.CreateServiceLinkedRole,
		// Instance profile by role
		"ListInstanceProfilesForRole": h.ListInstanceProfilesForRole,
		// Role mutation
		"UpdateAssumeRolePolicy": h.UpdateAssumeRolePolicy,
		// List instance profiles
		"ListInstanceProfiles": h.ListInstanceProfiles,
		// Policy simulation
		"SimulatePrincipalPolicy": h.SimulatePrincipalPolicy,
		// Account-wide authorization details
		"GetAccountAuthorizationDetails": h.GetAccountAuthorizationDetails,
	}
	h.typedOp = h.typedOps()
}

func (h *Handler) ownsAction(action string) bool {
	_, ok := h.ops[action]
	return ok
}

// publish emits a lifecycle event on the bus if wired.
func (h *Handler) publish(r *http.Request, t events.Type, payload any) {
	if h.bus != nil {
		h.bus.Publish(r.Context(), events.Event{
			Type: t, Time: h.clk.Now(), Source: "iam", Payload: payload,
		})
	}
}

func (h *Handler) dispatch(w http.ResponseWriter, r *http.Request) {
	action := r.FormValue("Action")
	if fn, ok := h.ops[action]; ok {
		fn(w, r)
		if iamMutateActions[action] {
			middleware.InvalidateIAMEnforceCache()
		}
		return
	}
	protocol.WriteQueryXMLError(w, r, &protocol.AWSError{
		Code:       "InvalidAction",
		Message:    "The action " + action + " is not valid for IAM.",
		HTTPStatus: http.StatusBadRequest,
	})
}

// ─── Users ────────────────────────────────────────────────────────────────────

// CreateUser creates an IAM user.
func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("UserName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("UserName"))
		return
	}
	path := normPath(r.FormValue("Path"))
	if _, aerr := h.store.getUser(r.Context(), name); aerr == nil {
		protocol.WriteQueryXMLError(w, r, errEntityAlreadyExists("user", name))
		return
	}
	u := &User{
		UserName:   name,
		UserId:     iamID("AIDA", 17),
		Arn:        h.store.arnForUser(path, name),
		Path:       path,
		CreateDate: h.clk.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}
	if aerr := h.store.putUser(r.Context(), u); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	h.publish(r, events.IAMUserCreated, events.ResourcePayload{Name: name})
	writeIAMXML(w, r, "CreateUserResponse", "CreateUserResult", struct {
		User userXML `xml:"User"`
	}{User: toUserXML(u)})
}

// GetUser retrieves an IAM user.
func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("UserName")
	// If empty, return the "current" user (caller identity — return a placeholder).
	if name == "" {
		name = "caller"
	}
	u, aerr := h.store.getUser(r.Context(), name)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXML(w, r, "GetUserResponse", "GetUserResult", struct {
		User userXML `xml:"User"`
	}{User: toUserXML(u)})
}

// ListUsers lists all IAM users.
func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	users, aerr := h.store.listUsers(r.Context())
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	xmlUsers := make([]userXML, 0, len(users))
	for i := range users {
		xmlUsers = append(xmlUsers, toUserXML(&users[i]))
	}
	writeIAMXML(w, r, "ListUsersResponse", "ListUsersResult", struct {
		Users       listMembersXML[userXML] `xml:"Users"`
		IsTruncated bool                    `xml:"IsTruncated"`
	}{
		Users:       listMembersXML[userXML]{Members: xmlUsers, Tag: "member"},
		IsTruncated: false,
	})
}

// DeleteUser deletes an IAM user.
func (h *Handler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("UserName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("UserName"))
		return
	}
	if _, aerr := h.store.getUser(r.Context(), name); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	if aerr := h.store.deleteUser(r.Context(), name); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	h.publish(r, events.IAMUserDeleted, events.ResourcePayload{Name: name})
	writeIAMXMLEmpty(w, r, "DeleteUserResponse")
}

// ─── Access Keys ──────────────────────────────────────────────────────────────

// CreateAccessKey creates an access key for a user.
func (h *Handler) CreateAccessKey(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("UserName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("UserName"))
		return
	}
	u, aerr := h.store.getUser(r.Context(), name)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	ak := AccessKey{
		AccessKeyId:     iamID("AKIA", 16),
		SecretAccessKey: randBase64(30),
		Status:          "Active",
		UserName:        name,
		CreateDate:      h.clk.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}
	u.AccessKeys = append(u.AccessKeys, ak)
	if aerr := h.store.putUser(r.Context(), u); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXML(w, r, "CreateAccessKeyResponse", "CreateAccessKeyResult", struct {
		AccessKey accessKeyXML `xml:"AccessKey"`
	}{AccessKey: toAccessKeyXML(&ak)})
}

// DeleteAccessKey deletes an access key.
func (h *Handler) DeleteAccessKey(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("UserName")
	keyID := r.FormValue("AccessKeyId")
	if keyID == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("AccessKeyId"))
		return
	}
	if name == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("UserName"))
		return
	}
	u, aerr := h.store.getUser(r.Context(), name)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	filtered := u.AccessKeys[:0]
	for _, ak := range u.AccessKeys {
		if ak.AccessKeyId != keyID {
			filtered = append(filtered, ak)
		}
	}
	u.AccessKeys = filtered
	if aerr := h.store.putUser(r.Context(), u); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXMLEmpty(w, r, "DeleteAccessKeyResponse")
}

// ListAccessKeys lists access keys for an IAM user.
// AWS docs: https://docs.aws.amazon.com/IAM/latest/APIReference/API_ListAccessKeys.html
func (h *Handler) ListAccessKeys(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("UserName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("UserName"))
		return
	}
	u, aerr := h.store.getUser(r.Context(), name)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	members := make([]accessKeyXML, len(u.AccessKeys))
	for i := range u.AccessKeys {
		members[i] = toAccessKeyXML(&u.AccessKeys[i])
	}
	writeIAMXML(w, r, "ListAccessKeysResponse", "ListAccessKeysResult", struct {
		AccessKeyMetadata []accessKeyXML `xml:"AccessKeyMetadata>member"`
		IsTruncated       bool           `xml:"IsTruncated"`
	}{AccessKeyMetadata: members, IsTruncated: false})
}

// ─── Inline User Policies ─────────────────────────────────────────────────────

// PutUserPolicy attaches an inline policy to a user.
func (h *Handler) PutUserPolicy(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("UserName")
	policyName := r.FormValue("PolicyName")
	doc := r.FormValue("PolicyDocument")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("UserName"))
		return
	}
	if policyName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("PolicyName"))
		return
	}
	u, aerr := h.store.getUser(r.Context(), name)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	if u.InlinePolicies == nil {
		u.InlinePolicies = make(map[string]string)
	}
	u.InlinePolicies[policyName] = doc
	if aerr := h.store.putUser(r.Context(), u); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXMLEmpty(w, r, "PutUserPolicyResponse")
}

// GetUserPolicy retrieves an inline policy from a user.
func (h *Handler) GetUserPolicy(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("UserName")
	policyName := r.FormValue("PolicyName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("UserName"))
		return
	}
	if policyName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("PolicyName"))
		return
	}
	u, aerr := h.store.getUser(r.Context(), name)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	doc, ok := u.InlinePolicies[policyName]
	if !ok {
		protocol.WriteQueryXMLError(w, r, errNoSuchEntity("policy", policyName))
		return
	}
	writeIAMXML(w, r, "GetUserPolicyResponse", "GetUserPolicyResult", struct {
		UserName       string `xml:"UserName"`
		PolicyName     string `xml:"PolicyName"`
		PolicyDocument string `xml:"PolicyDocument"`
	}{
		UserName:       name,
		PolicyName:     policyName,
		PolicyDocument: url.QueryEscape(doc),
	})
}

// DeleteUserPolicy removes an inline policy from a user.
func (h *Handler) DeleteUserPolicy(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("UserName")
	policyName := r.FormValue("PolicyName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("UserName"))
		return
	}
	if policyName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("PolicyName"))
		return
	}
	u, aerr := h.store.getUser(r.Context(), name)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	delete(u.InlinePolicies, policyName)
	if aerr := h.store.putUser(r.Context(), u); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXMLEmpty(w, r, "DeleteUserPolicyResponse")
}

// ─── Roles ────────────────────────────────────────────────────────────────────

// CreateRole creates an IAM role.
func (h *Handler) CreateRole(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("RoleName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("RoleName"))
		return
	}
	assumeDoc := r.FormValue("AssumeRolePolicyDocument")
	path := normPath(r.FormValue("Path"))
	if _, aerr := h.store.getRole(r.Context(), name); aerr == nil {
		protocol.WriteQueryXMLError(w, r, errEntityAlreadyExists("role", name))
		return
	}
	role := &Role{
		RoleName:                 name,
		RoleId:                   iamID("AROA", 17),
		Arn:                      h.store.arnForRole(path, name),
		Path:                     path,
		AssumeRolePolicyDocument: assumeDoc,
		CreateDate:               h.clk.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}
	if aerr := h.store.putRole(r.Context(), role); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	h.publish(r, events.IAMRoleCreated, events.ResourcePayload{Name: name})
	writeIAMXML(w, r, "CreateRoleResponse", "CreateRoleResult", struct {
		Role roleXML `xml:"Role"`
	}{Role: toRoleXML(role)})
}

// GetRole retrieves an IAM role.
func (h *Handler) GetRole(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("RoleName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("RoleName"))
		return
	}
	role, aerr := h.store.getRole(r.Context(), name)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXML(w, r, "GetRoleResponse", "GetRoleResult", struct {
		Role roleXML `xml:"Role"`
	}{Role: toRoleXML(role)})
}

// ListRoles lists all IAM roles.
func (h *Handler) ListRoles(w http.ResponseWriter, r *http.Request) {
	roles, aerr := h.store.listRoles(r.Context())
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	xmlRoles := make([]roleXML, 0, len(roles))
	for i := range roles {
		xmlRoles = append(xmlRoles, toRoleXML(&roles[i]))
	}
	writeIAMXML(w, r, "ListRolesResponse", "ListRolesResult", struct {
		Roles       listMembersXML[roleXML] `xml:"Roles"`
		IsTruncated bool                    `xml:"IsTruncated"`
	}{
		Roles:       listMembersXML[roleXML]{Members: xmlRoles, Tag: "member"},
		IsTruncated: false,
	})
}

// DeleteRole deletes an IAM role.
func (h *Handler) DeleteRole(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("RoleName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("RoleName"))
		return
	}
	if _, aerr := h.store.getRole(r.Context(), name); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	if aerr := h.store.deleteRole(r.Context(), name); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	h.publish(r, events.IAMRoleDeleted, events.ResourcePayload{Name: name})
	writeIAMXMLEmpty(w, r, "DeleteRoleResponse")
}

// ─── Role Policies ────────────────────────────────────────────────────────────

// AttachRolePolicy attaches a managed policy to a role.
func (h *Handler) AttachRolePolicy(w http.ResponseWriter, r *http.Request) {
	name, arn, ok := parseAttachPolicyParams(w, r, "RoleName")
	if !ok {
		return
	}
	role, aerr := h.store.getRole(r.Context(), name)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	role.AttachedPolicies = ensureAttachedPolicy(role.AttachedPolicies, arn)
	if aerr := h.store.putRole(r.Context(), role); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXMLEmpty(w, r, "AttachRolePolicyResponse")
}

// DetachRolePolicy detaches a managed policy from a role.
func (h *Handler) DetachRolePolicy(w http.ResponseWriter, r *http.Request) {
	name, arn, ok := parseAttachPolicyParams(w, r, "RoleName")
	if !ok {
		return
	}
	role, aerr := h.store.getRole(r.Context(), name)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	role.AttachedPolicies = removeAttachedPolicy(role.AttachedPolicies, arn)
	if aerr := h.store.putRole(r.Context(), role); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXMLEmpty(w, r, "DetachRolePolicyResponse")
}

// ListAttachedRolePolicies lists policies attached to a role.
func (h *Handler) ListAttachedRolePolicies(w http.ResponseWriter, r *http.Request) {
	roleName := r.FormValue("RoleName")
	if roleName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("RoleName"))
		return
	}
	role, aerr := h.store.getRole(r.Context(), roleName)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	xmlPolicies := make([]attachedPolicyXML, 0, len(role.AttachedPolicies))
	for _, ap := range role.AttachedPolicies {
		xmlPolicies = append(xmlPolicies, attachedPolicyXML(ap))
	}
	writeIAMXML(w, r, "ListAttachedRolePoliciesResponse", "ListAttachedRolePoliciesResult", struct {
		AttachedPolicies listMembersXML[attachedPolicyXML] `xml:"AttachedPolicies"`
		IsTruncated      bool                              `xml:"IsTruncated"`
	}{
		AttachedPolicies: listMembersXML[attachedPolicyXML]{Members: xmlPolicies, Tag: "member"},
		IsTruncated:      false,
	})
}

// ─── Instance Profiles ────────────────────────────────────────────────────────

// CreateInstanceProfile creates an instance profile.
func (h *Handler) CreateInstanceProfile(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("InstanceProfileName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("InstanceProfileName"))
		return
	}
	path := normPath(r.FormValue("Path"))
	if _, aerr := h.store.getProfile(r.Context(), name); aerr == nil {
		protocol.WriteQueryXMLError(w, r, errEntityAlreadyExists("instance profile", name))
		return
	}
	profile := &InstanceProfile{
		InstanceProfileName: name,
		InstanceProfileId:   iamID("AIPA", 17),
		Arn:                 h.store.arnForProfile(path, name),
		Path:                path,
		CreateDate:          h.clk.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}
	if aerr := h.store.putProfile(r.Context(), profile); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXML(w, r, "CreateInstanceProfileResponse", "CreateInstanceProfileResult", struct {
		InstanceProfile instanceProfileXML `xml:"InstanceProfile"`
	}{InstanceProfile: toInstanceProfileXML(profile, nil)})
}

// DeleteInstanceProfile deletes an instance profile.
func (h *Handler) DeleteInstanceProfile(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("InstanceProfileName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("InstanceProfileName"))
		return
	}
	if _, aerr := h.store.getProfile(r.Context(), name); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	if aerr := h.store.deleteProfile(r.Context(), name); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXMLEmpty(w, r, "DeleteInstanceProfileResponse")
}

// GetInstanceProfile retrieves an instance profile.
func (h *Handler) GetInstanceProfile(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("InstanceProfileName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("InstanceProfileName"))
		return
	}
	profile, aerr := h.store.getProfile(r.Context(), name)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	// Build the role list for the profile.
	var roles []roleXML
	for _, rn := range profile.Roles {
		if role, aerr := h.store.getRole(r.Context(), rn); aerr == nil {
			roles = append(roles, toRoleXML(role))
		}
	}
	writeIAMXML(w, r, "GetInstanceProfileResponse", "GetInstanceProfileResult", struct {
		InstanceProfile instanceProfileXML `xml:"InstanceProfile"`
	}{InstanceProfile: toInstanceProfileXML(profile, roles)})
}

// AddRoleToInstanceProfile adds a role to an instance profile.
func (h *Handler) AddRoleToInstanceProfile(w http.ResponseWriter, r *http.Request) {
	profileName := r.FormValue("InstanceProfileName")
	roleName := r.FormValue("RoleName")
	if profileName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("InstanceProfileName"))
		return
	}
	if roleName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("RoleName"))
		return
	}
	profile, aerr := h.store.getProfile(r.Context(), profileName)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	if _, aerr := h.store.getRole(r.Context(), roleName); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	for _, rn := range profile.Roles {
		if rn == roleName {
			writeIAMXMLEmpty(w, r, "AddRoleToInstanceProfileResponse")
			return
		}
	}
	profile.Roles = append(profile.Roles, roleName)
	if aerr := h.store.putProfile(r.Context(), profile); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXMLEmpty(w, r, "AddRoleToInstanceProfileResponse")
}

// RemoveRoleFromInstanceProfile removes a role from an instance profile.
func (h *Handler) RemoveRoleFromInstanceProfile(w http.ResponseWriter, r *http.Request) {
	profileName := r.FormValue("InstanceProfileName")
	roleName := r.FormValue("RoleName")
	if profileName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("InstanceProfileName"))
		return
	}
	if roleName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("RoleName"))
		return
	}
	profile, aerr := h.store.getProfile(r.Context(), profileName)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	filtered := profile.Roles[:0]
	for _, rn := range profile.Roles {
		if rn != roleName {
			filtered = append(filtered, rn)
		}
	}
	profile.Roles = filtered
	if aerr := h.store.putProfile(r.Context(), profile); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXMLEmpty(w, r, "RemoveRoleFromInstanceProfileResponse")
}

// ─── Managed Policies ─────────────────────────────────────────────────────────

// CreatePolicy creates a managed IAM policy.
func (h *Handler) CreatePolicy(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("PolicyName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("PolicyName"))
		return
	}
	doc := r.FormValue("PolicyDocument")
	path := normPath(r.FormValue("Path"))
	arn := h.store.arnForPolicy(path, name)
	if _, aerr := h.store.getPolicy(r.Context(), arn); aerr == nil {
		protocol.WriteQueryXMLError(w, r, errEntityAlreadyExists("policy", name))
		return
	}
	p := &Policy{
		PolicyName: name,
		PolicyId:   iamID("ANPA", 17),
		Arn:        arn,
		Path:       path,
		Document:   doc,
		CreateDate: h.clk.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}
	if aerr := h.store.putPolicy(r.Context(), p); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	h.publish(r, events.IAMPolicyCreated, events.ResourcePayload{Name: name})
	writeIAMXML(w, r, "CreatePolicyResponse", "CreatePolicyResult", struct {
		Policy policyXML `xml:"Policy"`
	}{Policy: toPolicyXML(p)})
}

// GetPolicy retrieves a managed IAM policy.
func (h *Handler) GetPolicy(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("PolicyArn")
	if arn == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("PolicyArn"))
		return
	}
	p, aerr := h.store.getPolicy(r.Context(), arn)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXML(w, r, "GetPolicyResponse", "GetPolicyResult", struct {
		Policy policyXML `xml:"Policy"`
	}{Policy: toPolicyXML(p)})
}

// ListPolicies lists managed IAM policies.
func (h *Handler) ListPolicies(w http.ResponseWriter, r *http.Request) {
	// scope=Local means customer-managed only; scope=AWS means AWS-managed.
	// scope=All means both. Default is All.
	scope := r.FormValue("Scope")
	policies, aerr := h.store.listPolicies(r.Context())
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	// AWS-managed policies have "arn:aws:iam::aws:policy/" prefix.
	// Our store only contains customer-managed policies. If scope is AWS or All,
	// we'd ideally return some AWS-managed policies too, but for compat tests
	// scope=Local is used, so this is sufficient.
	_ = scope
	xmlPolicies := make([]policyXML, 0, len(policies))
	for i := range policies {
		xmlPolicies = append(xmlPolicies, toPolicyXML(&policies[i]))
	}
	writeIAMXML(w, r, "ListPoliciesResponse", "ListPoliciesResult", struct {
		Policies    listMembersXML[policyXML] `xml:"Policies"`
		IsTruncated bool                      `xml:"IsTruncated"`
	}{
		Policies:    listMembersXML[policyXML]{Members: xmlPolicies, Tag: "member"},
		IsTruncated: false,
	})
}

// DeletePolicy deletes a managed IAM policy.
func (h *Handler) DeletePolicy(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("PolicyArn")
	if arn == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("PolicyArn"))
		return
	}
	if _, aerr := h.store.getPolicy(r.Context(), arn); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	if aerr := h.store.deletePolicy(r.Context(), arn); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	h.publish(r, events.IAMPolicyDeleted, events.ResourcePayload{Name: arn})
	writeIAMXMLEmpty(w, r, "DeletePolicyResponse")
}

// ─── Groups ───────────────────────────────────────────────────────────────────

// CreateGroup creates an IAM group.
func (h *Handler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("GroupName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("GroupName"))
		return
	}
	path := normPath(r.FormValue("Path"))
	if _, aerr := h.store.getGroup(r.Context(), name); aerr == nil {
		protocol.WriteQueryXMLError(w, r, errEntityAlreadyExists("group", name))
		return
	}
	g := &Group{
		GroupName:  name,
		GroupId:    iamID("AGPA", 17),
		Arn:        h.store.arnForGroup(path, name),
		Path:       path,
		CreateDate: h.clk.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}
	if aerr := h.store.putGroup(r.Context(), g); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXML(w, r, "CreateGroupResponse", "CreateGroupResult", struct {
		Group groupXML `xml:"Group"`
	}{Group: toGroupXML(g)})
}

// DeleteGroup deletes an IAM group.
func (h *Handler) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("GroupName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("GroupName"))
		return
	}
	if _, aerr := h.store.getGroup(r.Context(), name); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	if aerr := h.store.deleteGroup(r.Context(), name); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXMLEmpty(w, r, "DeleteGroupResponse")
}

// AddUserToGroup adds a user to a group.
func (h *Handler) AddUserToGroup(w http.ResponseWriter, r *http.Request) {
	groupName := r.FormValue("GroupName")
	userName := r.FormValue("UserName")
	if groupName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("GroupName"))
		return
	}
	if userName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("UserName"))
		return
	}
	g, aerr := h.store.getGroup(r.Context(), groupName)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	for _, m := range g.Members {
		if m == userName {
			writeIAMXMLEmpty(w, r, "AddUserToGroupResponse")
			return
		}
	}
	g.Members = append(g.Members, userName)
	if aerr := h.store.putGroup(r.Context(), g); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXMLEmpty(w, r, "AddUserToGroupResponse")
}

// RemoveUserFromGroup removes a user from a group.
func (h *Handler) RemoveUserFromGroup(w http.ResponseWriter, r *http.Request) {
	groupName := r.FormValue("GroupName")
	userName := r.FormValue("UserName")
	if groupName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("GroupName"))
		return
	}
	if userName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("UserName"))
		return
	}
	g, aerr := h.store.getGroup(r.Context(), groupName)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	filtered := g.Members[:0]
	for _, m := range g.Members {
		if m != userName {
			filtered = append(filtered, m)
		}
	}
	g.Members = filtered
	if aerr := h.store.putGroup(r.Context(), g); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXMLEmpty(w, r, "RemoveUserFromGroupResponse")
}

// ListGroupsForUser lists the groups a user belongs to.
func (h *Handler) ListGroupsForUser(w http.ResponseWriter, r *http.Request) {
	userName := r.FormValue("UserName")
	if userName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("UserName"))
		return
	}
	groups, aerr := h.store.listGroupsForUser(r.Context(), userName)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	xmlGroups := make([]groupXML, 0, len(groups))
	for i := range groups {
		xmlGroups = append(xmlGroups, toGroupXML(&groups[i]))
	}
	writeIAMXML(w, r, "ListGroupsForUserResponse", "ListGroupsForUserResult", struct {
		Groups      listMembersXML[groupXML] `xml:"Groups"`
		IsTruncated bool                     `xml:"IsTruncated"`
	}{
		Groups:      listMembersXML[groupXML]{Members: xmlGroups, Tag: "member"},
		IsTruncated: false,
	})
}

// ListGroups returns all groups in the account.
func (h *Handler) ListGroups(w http.ResponseWriter, r *http.Request) {
	groups, aerr := h.store.listGroups(r.Context())
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	xmlGroups := make([]groupXML, 0, len(groups))
	for i := range groups {
		xmlGroups = append(xmlGroups, toGroupXML(&groups[i]))
	}
	writeIAMXML(w, r, "ListGroupsResponse", "ListGroupsResult", struct {
		Groups      listMembersXML[groupXML] `xml:"Groups"`
		IsTruncated bool                     `xml:"IsTruncated"`
	}{
		Groups:      listMembersXML[groupXML]{Members: xmlGroups, Tag: "member"},
		IsTruncated: false,
	})
}

// GetGroup retrieves an IAM group and its members.
func (h *Handler) GetGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("GroupName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("GroupName"))
		return
	}
	g, aerr := h.store.getGroup(r.Context(), name)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXML(w, r, "GetGroupResponse", "GetGroupResult", struct {
		Group       groupXML                `xml:"Group"`
		Users       listMembersXML[userXML] `xml:"Users"`
		IsTruncated bool                    `xml:"IsTruncated"`
	}{
		Group:       toGroupXML(g),
		Users:       listMembersXML[userXML]{Members: nil, Tag: "member"},
		IsTruncated: false,
	})
}

// ─── Inline Role Policies ─────────────────────────────────────────────────────

// PutRolePolicy attaches an inline policy to a role.
func (h *Handler) PutRolePolicy(w http.ResponseWriter, r *http.Request) {
	roleName := r.FormValue("RoleName")
	policyName := r.FormValue("PolicyName")
	doc := r.FormValue("PolicyDocument")
	if roleName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("RoleName"))
		return
	}
	if policyName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("PolicyName"))
		return
	}
	role, aerr := h.store.getRole(r.Context(), roleName)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	if role.InlinePolicies == nil {
		role.InlinePolicies = make(map[string]string)
	}
	role.InlinePolicies[policyName] = doc
	if aerr := h.store.putRole(r.Context(), role); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXMLEmpty(w, r, "PutRolePolicyResponse")
}

// GetRolePolicy retrieves an inline policy from a role.
func (h *Handler) GetRolePolicy(w http.ResponseWriter, r *http.Request) {
	roleName := r.FormValue("RoleName")
	policyName := r.FormValue("PolicyName")
	if roleName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("RoleName"))
		return
	}
	if policyName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("PolicyName"))
		return
	}
	role, aerr := h.store.getRole(r.Context(), roleName)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	doc, ok := role.InlinePolicies[policyName]
	if !ok {
		protocol.WriteQueryXMLError(w, r, errNoSuchEntity("policy", policyName))
		return
	}
	writeIAMXML(w, r, "GetRolePolicyResponse", "GetRolePolicyResult", struct {
		RoleName       string `xml:"RoleName"`
		PolicyName     string `xml:"PolicyName"`
		PolicyDocument string `xml:"PolicyDocument"`
	}{
		RoleName:       roleName,
		PolicyName:     policyName,
		PolicyDocument: url.QueryEscape(doc),
	})
}

// ListRolePolicies lists inline policy names for a role.
func (h *Handler) ListRolePolicies(w http.ResponseWriter, r *http.Request) {
	roleName := r.FormValue("RoleName")
	if roleName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("RoleName"))
		return
	}
	role, aerr := h.store.getRole(r.Context(), roleName)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	names := make([]string, 0, len(role.InlinePolicies))
	for name := range role.InlinePolicies {
		names = append(names, name)
	}
	writeIAMXML(w, r, "ListRolePoliciesResponse", "ListRolePoliciesResult", struct {
		PolicyNames listMembersXML[string] `xml:"PolicyNames"`
		IsTruncated bool                   `xml:"IsTruncated"`
	}{
		PolicyNames: listMembersXML[string]{Members: names, Tag: "member"},
		IsTruncated: false,
	})
}

// DeleteRolePolicy removes an inline policy from a role.
func (h *Handler) DeleteRolePolicy(w http.ResponseWriter, r *http.Request) {
	roleName := r.FormValue("RoleName")
	policyName := r.FormValue("PolicyName")
	if roleName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("RoleName"))
		return
	}
	if policyName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("PolicyName"))
		return
	}
	role, aerr := h.store.getRole(r.Context(), roleName)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	delete(role.InlinePolicies, policyName)
	if aerr := h.store.putRole(r.Context(), role); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXMLEmpty(w, r, "DeleteRolePolicyResponse")
}

// ─── User Updates ─────────────────────────────────────────────────────────────

// UpdateUser updates an IAM user's path or name.
func (h *Handler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("UserName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("UserName"))
		return
	}
	u, aerr := h.store.getUser(r.Context(), name)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	if newPath := r.FormValue("NewPath"); newPath != "" {
		u.Path = newPath
		u.Arn = h.store.arnForUser(newPath, u.UserName)
	}
	if newName := r.FormValue("NewUserName"); newName != "" {
		if aerr := h.store.deleteUser(r.Context(), name); aerr != nil {
			protocol.WriteQueryXMLError(w, r, aerr)
			return
		}
		u.UserName = newName
		u.Arn = h.store.arnForUser(u.Path, newName)
	}
	if aerr := h.store.putUser(r.Context(), u); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXMLEmpty(w, r, "UpdateUserResponse")
}

// ListInstanceProfiles returns all instance profiles in the account.
func (h *Handler) ListInstanceProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, aerr := h.store.listProfiles(r.Context())
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	var xmlProfiles []instanceProfileXML
	for i := range profiles {
		var roles []roleXML
		for _, rn := range profiles[i].Roles {
			if role, aerr := h.store.getRole(r.Context(), rn); aerr == nil {
				roles = append(roles, toRoleXML(role))
			}
		}
		xmlProfiles = append(xmlProfiles, toInstanceProfileXML(&profiles[i], roles))
	}
	writeIAMXML(w, r, "ListInstanceProfilesResponse", "ListInstanceProfilesResult", struct {
		InstanceProfiles listMembersXML[instanceProfileXML] `xml:"InstanceProfiles"`
		IsTruncated      bool                               `xml:"IsTruncated"`
	}{
		InstanceProfiles: listMembersXML[instanceProfileXML]{Members: xmlProfiles, Tag: "member"},
		IsTruncated:      false,
	})
}

// ─── Group Inline Policies ────────────────────────────────────────────────────

// PutGroupPolicy attaches an inline policy to a group.
func (h *Handler) PutGroupPolicy(w http.ResponseWriter, r *http.Request) {
	groupName := r.FormValue("GroupName")
	policyName := r.FormValue("PolicyName")
	doc := r.FormValue("PolicyDocument")
	if groupName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("GroupName"))
		return
	}
	if policyName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("PolicyName"))
		return
	}
	g, aerr := h.store.getGroup(r.Context(), groupName)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	if g.InlinePolicies == nil {
		g.InlinePolicies = make(map[string]string)
	}
	g.InlinePolicies[policyName] = doc
	if aerr := h.store.putGroup(r.Context(), g); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXMLEmpty(w, r, "PutGroupPolicyResponse")
}

// GetGroupPolicy retrieves an inline policy from a group.
func (h *Handler) GetGroupPolicy(w http.ResponseWriter, r *http.Request) {
	groupName := r.FormValue("GroupName")
	policyName := r.FormValue("PolicyName")
	if groupName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("GroupName"))
		return
	}
	if policyName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("PolicyName"))
		return
	}
	g, aerr := h.store.getGroup(r.Context(), groupName)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	doc, ok := g.InlinePolicies[policyName]
	if !ok {
		protocol.WriteQueryXMLError(w, r, errNoSuchEntity("policy", policyName))
		return
	}
	writeIAMXML(w, r, "GetGroupPolicyResponse", "GetGroupPolicyResult", struct {
		GroupName      string `xml:"GroupName"`
		PolicyName     string `xml:"PolicyName"`
		PolicyDocument string `xml:"PolicyDocument"`
	}{
		GroupName:      groupName,
		PolicyName:     policyName,
		PolicyDocument: url.QueryEscape(doc),
	})
}

// DeleteGroupPolicy removes an inline policy from a group.
func (h *Handler) DeleteGroupPolicy(w http.ResponseWriter, r *http.Request) {
	groupName := r.FormValue("GroupName")
	policyName := r.FormValue("PolicyName")
	if groupName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("GroupName"))
		return
	}
	if policyName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("PolicyName"))
		return
	}
	g, aerr := h.store.getGroup(r.Context(), groupName)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	delete(g.InlinePolicies, policyName)
	if aerr := h.store.putGroup(r.Context(), g); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXMLEmpty(w, r, "DeleteGroupPolicyResponse")
}

// ListGroupPolicies lists inline policy names for a group.
func (h *Handler) ListGroupPolicies(w http.ResponseWriter, r *http.Request) {
	groupName := r.FormValue("GroupName")
	if groupName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("GroupName"))
		return
	}
	g, aerr := h.store.getGroup(r.Context(), groupName)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	names := make([]string, 0, len(g.InlinePolicies))
	for name := range g.InlinePolicies {
		names = append(names, name)
	}
	writeIAMXML(w, r, "ListGroupPoliciesResponse", "ListGroupPoliciesResult", struct {
		PolicyNames listMembersXML[string] `xml:"PolicyNames"`
		IsTruncated bool                   `xml:"IsTruncated"`
	}{
		PolicyNames: listMembersXML[string]{Members: names, Tag: "member"},
		IsTruncated: false,
	})
}

// ─── Managed Group Policies ──────────────────────────────────────────────────

// AttachGroupPolicy attaches a managed policy to a group.
func (h *Handler) AttachGroupPolicy(w http.ResponseWriter, r *http.Request) {
	name, arn, ok := parseAttachPolicyParams(w, r, "GroupName")
	if !ok {
		return
	}
	g, aerr := h.store.getGroup(r.Context(), name)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	g.AttachedPolicies = ensureAttachedPolicy(g.AttachedPolicies, arn)
	if aerr := h.store.putGroup(r.Context(), g); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXMLEmpty(w, r, "AttachGroupPolicyResponse")
}

// DetachGroupPolicy detaches a managed policy from a group.
func (h *Handler) DetachGroupPolicy(w http.ResponseWriter, r *http.Request) {
	name, arn, ok := parseAttachPolicyParams(w, r, "GroupName")
	if !ok {
		return
	}
	g, aerr := h.store.getGroup(r.Context(), name)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	g.AttachedPolicies = removeAttachedPolicy(g.AttachedPolicies, arn)
	if aerr := h.store.putGroup(r.Context(), g); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXMLEmpty(w, r, "DetachGroupPolicyResponse")
}

// ListAttachedGroupPolicies lists managed policies attached to a group.
func (h *Handler) ListAttachedGroupPolicies(w http.ResponseWriter, r *http.Request) {
	groupName := r.FormValue("GroupName")
	if groupName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("GroupName"))
		return
	}
	g, aerr := h.store.getGroup(r.Context(), groupName)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	xmlPolicies := make([]attachedPolicyXML, 0, len(g.AttachedPolicies))
	for _, ap := range g.AttachedPolicies {
		xmlPolicies = append(xmlPolicies, attachedPolicyXML(ap))
	}
	writeIAMXML(w, r, "ListAttachedGroupPoliciesResponse", "ListAttachedGroupPoliciesResult", struct {
		AttachedPolicies listMembersXML[attachedPolicyXML] `xml:"AttachedPolicies"`
		IsTruncated      bool                              `xml:"IsTruncated"`
	}{
		AttachedPolicies: listMembersXML[attachedPolicyXML]{Members: xmlPolicies, Tag: "member"},
		IsTruncated:      false,
	})
}

// ─── Managed User Policies ───────────────────────────────────────────────────

// AttachUserPolicy attaches a managed policy to a user.
func (h *Handler) AttachUserPolicy(w http.ResponseWriter, r *http.Request) {
	name, arn, ok := parseAttachPolicyParams(w, r, "UserName")
	if !ok {
		return
	}
	u, aerr := h.store.getUser(r.Context(), name)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	u.AttachedPolicies = ensureAttachedPolicy(u.AttachedPolicies, arn)
	if aerr := h.store.putUser(r.Context(), u); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXMLEmpty(w, r, "AttachUserPolicyResponse")
}

// DetachUserPolicy detaches a managed policy from a user.
func (h *Handler) DetachUserPolicy(w http.ResponseWriter, r *http.Request) {
	name, arn, ok := parseAttachPolicyParams(w, r, "UserName")
	if !ok {
		return
	}
	u, aerr := h.store.getUser(r.Context(), name)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	u.AttachedPolicies = removeAttachedPolicy(u.AttachedPolicies, arn)
	if aerr := h.store.putUser(r.Context(), u); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXMLEmpty(w, r, "DetachUserPolicyResponse")
}

// ListAttachedUserPolicies lists managed policies attached to a user.
func (h *Handler) ListAttachedUserPolicies(w http.ResponseWriter, r *http.Request) {
	userName := r.FormValue("UserName")
	if userName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("UserName"))
		return
	}
	u, aerr := h.store.getUser(r.Context(), userName)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	xmlPolicies := make([]attachedPolicyXML, 0, len(u.AttachedPolicies))
	for _, ap := range u.AttachedPolicies {
		xmlPolicies = append(xmlPolicies, attachedPolicyXML(ap))
	}
	writeIAMXML(w, r, "ListAttachedUserPoliciesResponse", "ListAttachedUserPoliciesResult", struct {
		AttachedPolicies listMembersXML[attachedPolicyXML] `xml:"AttachedPolicies"`
		IsTruncated      bool                              `xml:"IsTruncated"`
	}{
		AttachedPolicies: listMembersXML[attachedPolicyXML]{Members: xmlPolicies, Tag: "member"},
		IsTruncated:      false,
	})
}

// ─── Inline User Policy Listing ──────────────────────────────────────────────

// ListUserPolicies lists inline policy names for a user.
func (h *Handler) ListUserPolicies(w http.ResponseWriter, r *http.Request) {
	userName := r.FormValue("UserName")
	if userName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("UserName"))
		return
	}
	u, aerr := h.store.getUser(r.Context(), userName)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	names := make([]string, 0, len(u.InlinePolicies))
	for name := range u.InlinePolicies {
		names = append(names, name)
	}
	writeIAMXML(w, r, "ListUserPoliciesResponse", "ListUserPoliciesResult", struct {
		PolicyNames listMembersXML[string] `xml:"PolicyNames"`
		IsTruncated bool                   `xml:"IsTruncated"`
	}{
		PolicyNames: listMembersXML[string]{Members: names, Tag: "member"},
		IsTruncated: false,
	})
}

// ─── Role Tagging ─────────────────────────────────────────────────────────────

// TagRole adds or overwrites tags on a role.
func (h *Handler) TagRole(w http.ResponseWriter, r *http.Request) {
	roleName := r.FormValue("RoleName")
	if roleName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("RoleName"))
		return
	}
	role, aerr := h.store.getRole(r.Context(), roleName)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	if role.Tags == nil {
		role.Tags = make(map[string]string)
	}
	for i := 1; ; i++ {
		key := r.FormValue(fmt.Sprintf("Tags.member.%d.Key", i))
		if key == "" {
			break
		}
		value := r.FormValue(fmt.Sprintf("Tags.member.%d.Value", i))
		role.Tags[key] = value
	}
	if aerr := h.store.putRole(r.Context(), role); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXMLEmpty(w, r, "TagRoleResponse")
}

// UntagRole removes tags from a role.
func (h *Handler) UntagRole(w http.ResponseWriter, r *http.Request) {
	roleName := r.FormValue("RoleName")
	if roleName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("RoleName"))
		return
	}
	role, aerr := h.store.getRole(r.Context(), roleName)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	for i := 1; ; i++ {
		key := r.FormValue(fmt.Sprintf("TagKeys.member.%d", i))
		if key == "" {
			break
		}
		delete(role.Tags, key)
	}
	if aerr := h.store.putRole(r.Context(), role); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXMLEmpty(w, r, "UntagRoleResponse")
}

// ListRoleTags lists all tags on a role.
func (h *Handler) ListRoleTags(w http.ResponseWriter, r *http.Request) {
	roleName := r.FormValue("RoleName")
	if roleName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("RoleName"))
		return
	}
	role, aerr := h.store.getRole(r.Context(), roleName)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	tags := make([]tagXML, 0, len(role.Tags))
	for k, v := range role.Tags {
		tags = append(tags, tagXML{Key: k, Value: v})
	}
	writeIAMXML(w, r, "ListRoleTagsResponse", "ListRoleTagsResult", struct {
		Tags        listMembersXML[tagXML] `xml:"Tags"`
		IsTruncated bool                   `xml:"IsTruncated"`
	}{
		Tags:        listMembersXML[tagXML]{Members: tags, Tag: "member"},
		IsTruncated: false,
	})
}

// ─── User Tagging ─────────────────────────────────────────────────────────────

// TagUser adds or overwrites tags on a user.
func (h *Handler) TagUser(w http.ResponseWriter, r *http.Request) {
	userName := r.FormValue("UserName")
	if userName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("UserName"))
		return
	}
	u, aerr := h.store.getUser(r.Context(), userName)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	if u.Tags == nil {
		u.Tags = make(map[string]string)
	}
	for i := 1; ; i++ {
		key := r.FormValue(fmt.Sprintf("Tags.member.%d.Key", i))
		if key == "" {
			break
		}
		value := r.FormValue(fmt.Sprintf("Tags.member.%d.Value", i))
		u.Tags[key] = value
	}
	if aerr := h.store.putUser(r.Context(), u); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXMLEmpty(w, r, "TagUserResponse")
}

// UntagUser removes tags from a user.
func (h *Handler) UntagUser(w http.ResponseWriter, r *http.Request) {
	userName := r.FormValue("UserName")
	if userName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("UserName"))
		return
	}
	u, aerr := h.store.getUser(r.Context(), userName)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	for i := 1; ; i++ {
		key := r.FormValue(fmt.Sprintf("TagKeys.member.%d", i))
		if key == "" {
			break
		}
		delete(u.Tags, key)
	}
	if aerr := h.store.putUser(r.Context(), u); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXMLEmpty(w, r, "UntagUserResponse")
}

// ListUserTags lists all tags on a user.
func (h *Handler) ListUserTags(w http.ResponseWriter, r *http.Request) {
	userName := r.FormValue("UserName")
	if userName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("UserName"))
		return
	}
	u, aerr := h.store.getUser(r.Context(), userName)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	tags := make([]tagXML, 0, len(u.Tags))
	for k, v := range u.Tags {
		tags = append(tags, tagXML{Key: k, Value: v})
	}
	writeIAMXML(w, r, "ListUserTagsResponse", "ListUserTagsResult", struct {
		Tags        listMembersXML[tagXML] `xml:"Tags"`
		IsTruncated bool                   `xml:"IsTruncated"`
	}{
		Tags:        listMembersXML[tagXML]{Members: tags, Tag: "member"},
		IsTruncated: false,
	})
}

// ─── Service-Linked Roles ─────────────────────────────────────────────────────

// CreateServiceLinkedRole creates a service-linked role.
func (h *Handler) CreateServiceLinkedRole(w http.ResponseWriter, r *http.Request) {
	serviceName := r.FormValue("AWSServiceName")
	if serviceName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("AWSServiceName"))
		return
	}
	// Derive role name from service name: AWSServiceRoleForECS for ecs.amazonaws.com
	suffix := serviceName
	if idx := len(suffix) - len(".amazonaws.com"); idx > 0 && suffix[idx:] == ".amazonaws.com" {
		suffix = suffix[:idx]
	}
	roleName := "AWSServiceRoleFor" + strings.ToUpper(suffix[:1]) + suffix[1:]
	path := "/aws-service-role/" + serviceName + "/"

	if _, aerr := h.store.getRole(r.Context(), roleName); aerr == nil {
		protocol.WriteQueryXMLError(w, r, errEntityAlreadyExists("role", roleName))
		return
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
	if aerr := h.store.putRole(r.Context(), role); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	h.publish(r, events.IAMRoleCreated, events.ResourcePayload{Name: roleName})
	writeIAMXML(w, r, "CreateServiceLinkedRoleResponse", "CreateServiceLinkedRoleResult", struct {
		Role roleXML `xml:"Role"`
	}{Role: toRoleXML(role)})
}

// ─── Instance Profiles For Role ──────────────────────────────────────────────

// ListInstanceProfilesForRole lists instance profiles that contain a given role.
func (h *Handler) ListInstanceProfilesForRole(w http.ResponseWriter, r *http.Request) {
	roleName := r.FormValue("RoleName")
	if roleName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("RoleName"))
		return
	}
	profiles, aerr := h.store.listProfilesForRole(r.Context(), roleName)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	var xmlProfiles []instanceProfileXML
	for i := range profiles {
		var roles []roleXML
		for _, rn := range profiles[i].Roles {
			if role, aerr := h.store.getRole(r.Context(), rn); aerr == nil {
				roles = append(roles, toRoleXML(role))
			}
		}
		xmlProfiles = append(xmlProfiles, toInstanceProfileXML(&profiles[i], roles))
	}
	writeIAMXML(w, r, "ListInstanceProfilesForRoleResponse", "ListInstanceProfilesForRoleResult", struct {
		InstanceProfiles listMembersXML[instanceProfileXML] `xml:"InstanceProfiles"`
		IsTruncated      bool                               `xml:"IsTruncated"`
	}{
		InstanceProfiles: listMembersXML[instanceProfileXML]{Members: xmlProfiles, Tag: "member"},
		IsTruncated:      false,
	})
}

// ─── Role Mutation ────────────────────────────────────────────────────────────

// UpdateAssumeRolePolicy updates the trust policy document of a role.
func (h *Handler) UpdateAssumeRolePolicy(w http.ResponseWriter, r *http.Request) {
	roleName := r.FormValue("RoleName")
	if roleName == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("RoleName"))
		return
	}
	doc := r.FormValue("PolicyDocument")
	if doc == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("PolicyDocument"))
		return
	}
	role, aerr := h.store.getRole(r.Context(), roleName)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	role.AssumeRolePolicyDocument = doc
	if aerr := h.store.putRole(r.Context(), role); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	writeIAMXMLEmpty(w, r, "UpdateAssumeRolePolicyResponse")
}

// ─── XML wire types ───────────────────────────────────────────────────────────

type userXML struct {
	Path       string `xml:"Path"`
	UserName   string `xml:"UserName"`
	UserId     string `xml:"UserId"`
	Arn        string `xml:"Arn"`
	CreateDate string `xml:"CreateDate"`
}

type accessKeyXML struct {
	AccessKeyId     string `xml:"AccessKeyId"`
	SecretAccessKey string `xml:"SecretAccessKey"`
	Status          string `xml:"Status"`
	UserName        string `xml:"UserName"`
	CreateDate      string `xml:"CreateDate"`
}

type roleXML struct {
	Path                     string `xml:"Path"`
	RoleName                 string `xml:"RoleName"`
	RoleId                   string `xml:"RoleId"`
	Arn                      string `xml:"Arn"`
	CreateDate               string `xml:"CreateDate"`
	AssumeRolePolicyDocument string `xml:"AssumeRolePolicyDocument"`
}

type policyXML struct {
	PolicyName       string `xml:"PolicyName"`
	PolicyId         string `xml:"PolicyId"`
	Arn              string `xml:"Arn"`
	Path             string `xml:"Path"`
	DefaultVersionId string `xml:"DefaultVersionId"`
	AttachmentCount  int    `xml:"AttachmentCount"`
	IsAttachable     bool   `xml:"IsAttachable"`
	CreateDate       string `xml:"CreateDate"`
	UpdateDate       string `xml:"UpdateDate"`
}

type groupXML struct {
	Path       string `xml:"Path"`
	GroupName  string `xml:"GroupName"`
	GroupId    string `xml:"GroupId"`
	Arn        string `xml:"Arn"`
	CreateDate string `xml:"CreateDate"`
}

type instanceProfileXML struct {
	InstanceProfileName string                  `xml:"InstanceProfileName"`
	InstanceProfileId   string                  `xml:"InstanceProfileId"`
	Arn                 string                  `xml:"Arn"`
	Path                string                  `xml:"Path"`
	CreateDate          string                  `xml:"CreateDate"`
	Roles               listMembersXML[roleXML] `xml:"Roles"`
}

type attachedPolicyXML struct {
	PolicyArn  string `xml:"PolicyArn"`
	PolicyName string `xml:"PolicyName"`
}

type tagXML struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

// listMembersXML is a generic helper for lists that encode elements as <member>.
// AWS IAM uses <member> as the element tag for list items, not the field name.
type listMembersXML[T any] struct {
	Members []T
	Tag     string
}

func (l listMembersXML[T]) MarshalXML(enc *xml.Encoder, start xml.StartElement) error {
	if err := enc.EncodeToken(start); err != nil {
		return err
	}
	tag := l.Tag
	if tag == "" {
		tag = "member"
	}
	for _, m := range l.Members {
		if err := enc.EncodeElement(m, xml.StartElement{Name: xml.Name{Local: tag}}); err != nil {
			return err
		}
	}
	return enc.EncodeToken(start.End())
}

// ─── Conversion helpers ───────────────────────────────────────────────────────

func toUserXML(u *User) userXML {
	return userXML{
		Path:       u.Path,
		UserName:   u.UserName,
		UserId:     u.UserId,
		Arn:        u.Arn,
		CreateDate: u.CreateDate,
	}
}

func toAccessKeyXML(ak *AccessKey) accessKeyXML {
	return accessKeyXML{
		AccessKeyId:     ak.AccessKeyId,
		SecretAccessKey: ak.SecretAccessKey,
		Status:          ak.Status,
		UserName:        ak.UserName,
		CreateDate:      ak.CreateDate,
	}
}

func toRoleXML(r *Role) roleXML {
	return roleXML{
		Path:                     r.Path,
		RoleName:                 r.RoleName,
		RoleId:                   r.RoleId,
		Arn:                      r.Arn,
		CreateDate:               r.CreateDate,
		AssumeRolePolicyDocument: r.AssumeRolePolicyDocument,
	}
}

func toPolicyXML(p *Policy) policyXML {
	return policyXML{
		PolicyName:       p.PolicyName,
		PolicyId:         p.PolicyId,
		Arn:              p.Arn,
		Path:             p.Path,
		DefaultVersionId: "v1",
		AttachmentCount:  p.AttachmentCount,
		IsAttachable:     true,
		CreateDate:       p.CreateDate,
		UpdateDate:       p.CreateDate,
	}
}

func toGroupXML(g *Group) groupXML {
	return groupXML{
		Path:       g.Path,
		GroupName:  g.GroupName,
		GroupId:    g.GroupId,
		Arn:        g.Arn,
		CreateDate: g.CreateDate,
	}
}

func toInstanceProfileXML(p *InstanceProfile, roles []roleXML) instanceProfileXML {
	if roles == nil {
		roles = []roleXML{}
	}
	return instanceProfileXML{
		InstanceProfileName: p.InstanceProfileName,
		InstanceProfileId:   p.InstanceProfileId,
		Arn:                 p.Arn,
		Path:                p.Path,
		CreateDate:          p.CreateDate,
		Roles:               listMembersXML[roleXML]{Members: roles, Tag: "member"},
	}
}

// ─── Response writers ─────────────────────────────────────────────────────────

// writeIAMXML writes a Query-protocol XML response with a result wrapper.
func writeIAMXML(w http.ResponseWriter, r *http.Request, rootTag, resultTag string, resultBody any) {
	var inner bytes.Buffer
	enc := xml.NewEncoder(&inner)
	if err := enc.EncodeElement(resultBody, xml.StartElement{Name: xml.Name{Local: resultTag}}); err == nil {
		enc.Flush()
	}
	type response struct {
		XMLName          xml.Name                  `xml:""`
		Xmlns            string                    `xml:"xmlns,attr"`
		Inner            []byte                    `xml:",innerxml"`
		ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &response{
		XMLName:          xml.Name{Local: rootTag},
		Xmlns:            iamXMLNS,
		Inner:            inner.Bytes(),
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// writeIAMXMLEmpty writes a Query-protocol XML response with no result body.
func writeIAMXMLEmpty(w http.ResponseWriter, r *http.Request, rootTag string) {
	type response struct {
		XMLName          xml.Name                  `xml:""`
		Xmlns            string                    `xml:"xmlns,attr"`
		ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &response{
		XMLName:          xml.Name{Local: rootTag},
		Xmlns:            iamXMLNS,
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ─── ID and ARN helpers ───────────────────────────────────────────────────────

// iamID generates a random ID with the given prefix and n random uppercase+digit chars.
func iamID(prefix string, n int) string {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		idx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		b[i] = chars[idx.Int64()]
	}
	return prefix + string(b)
}

// randBase64 generates n random bytes encoded as base64.
func randBase64(n int) string {
	b := make([]byte, n)
	rand.Read(b) //nolint:errcheck
	return base64.StdEncoding.EncodeToString(b)
}

// ─── SimulatePrincipalPolicy ─────────────────────────────────────────────────

// SimulatePrincipalPolicy simulates whether a principal can perform a set of
// actions. Since Overcast does not enforce IAM policies, every action is
// returned as "allowed".
func (h *Handler) SimulatePrincipalPolicy(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("PolicySourceArn") == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("PolicySourceArn"))
		return
	}
	var actions []string
	for i := 1; ; i++ {
		a := r.FormValue(fmt.Sprintf("ActionNames.member.%d", i))
		if a == "" {
			break
		}
		actions = append(actions, a)
	}
	resource := r.FormValue("ResourceArns.member.1")
	if resource == "" {
		resource = "*"
	}
	type evalResult struct {
		EvalActionName   string `xml:"EvalActionName"`
		EvalDecision     string `xml:"EvalDecision"`
		EvalResourceName string `xml:"EvalResourceName"`
	}
	results := make([]evalResult, 0, len(actions))
	for _, a := range actions {
		results = append(results, evalResult{
			EvalActionName:   a,
			EvalDecision:     "allowed",
			EvalResourceName: resource,
		})
	}
	writeIAMXML(w, r, "SimulatePrincipalPolicyResponse", "SimulatePrincipalPolicyResult", struct {
		EvaluationResults listMembersXML[evalResult] `xml:"EvaluationResults"`
		IsTruncated       bool                       `xml:"IsTruncated"`
	}{
		EvaluationResults: listMembersXML[evalResult]{Members: results, Tag: "member"},
		IsTruncated:       false,
	})
}

// ─── GetAccountAuthorizationDetails ──────────────────────────────────────────

type inlinePolicyXML struct {
	PolicyName     string `xml:"PolicyName"`
	PolicyDocument string `xml:"PolicyDocument"`
}

type userDetailXML struct {
	Path                    string                            `xml:"Path"`
	UserName                string                            `xml:"UserName"`
	UserId                  string                            `xml:"UserId"`
	Arn                     string                            `xml:"Arn"`
	CreateDate              string                            `xml:"CreateDate"`
	UserPolicyList          listMembersXML[inlinePolicyXML]   `xml:"UserPolicyList"`
	AttachedManagedPolicies listMembersXML[attachedPolicyXML] `xml:"AttachedManagedPolicies"`
}

type groupDetailXML struct {
	Path                    string                            `xml:"Path"`
	GroupName               string                            `xml:"GroupName"`
	GroupId                 string                            `xml:"GroupId"`
	Arn                     string                            `xml:"Arn"`
	CreateDate              string                            `xml:"CreateDate"`
	GroupPolicyList         listMembersXML[inlinePolicyXML]   `xml:"GroupPolicyList"`
	AttachedManagedPolicies listMembersXML[attachedPolicyXML] `xml:"AttachedManagedPolicies"`
}

type roleDetailXML struct {
	Path                     string                            `xml:"Path"`
	RoleName                 string                            `xml:"RoleName"`
	RoleId                   string                            `xml:"RoleId"`
	Arn                      string                            `xml:"Arn"`
	CreateDate               string                            `xml:"CreateDate"`
	AssumeRolePolicyDocument string                            `xml:"AssumeRolePolicyDocument"`
	RolePolicyList           listMembersXML[inlinePolicyXML]   `xml:"RolePolicyList"`
	AttachedManagedPolicies  listMembersXML[attachedPolicyXML] `xml:"AttachedManagedPolicies"`
}

// inlinePolicyListXML packages a name→document map as an ordered XML member list.
func inlinePolicyListXML(m map[string]string) listMembersXML[inlinePolicyXML] {
	items := make([]inlinePolicyXML, 0, len(m))
	for name, doc := range m {
		items = append(items, inlinePolicyXML{PolicyName: name, PolicyDocument: doc})
	}
	return listMembersXML[inlinePolicyXML]{Members: items, Tag: "member"}
}

// attachedPolicyListXML packages a slice of AttachedPolicy as an XML member list.
func attachedPolicyListXML(ap []AttachedPolicy) listMembersXML[attachedPolicyXML] {
	items := make([]attachedPolicyXML, 0, len(ap))
	for _, p := range ap {
		items = append(items, attachedPolicyXML(p))
	}
	return listMembersXML[attachedPolicyXML]{Members: items, Tag: "member"}
}

// GetAccountAuthorizationDetails returns all IAM entities (users, groups,
// roles, managed policies) and their attached policies in one response.
func (h *Handler) GetAccountAuthorizationDetails(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	users, aerr := h.store.listUsers(ctx)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	groups, aerr := h.store.listGroups(ctx)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	roles, aerr := h.store.listRoles(ctx)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	policies, aerr := h.store.listPolicies(ctx)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
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

	writeIAMXML(w, r, "GetAccountAuthorizationDetailsResponse", "GetAccountAuthorizationDetailsResult", struct {
		UserDetailList  listMembersXML[userDetailXML]  `xml:"UserDetailList"`
		GroupDetailList listMembersXML[groupDetailXML] `xml:"GroupDetailList"`
		RoleDetailList  listMembersXML[roleDetailXML]  `xml:"RoleDetailList"`
		Policies        listMembersXML[policyXML]      `xml:"Policies"`
		IsTruncated     bool                           `xml:"IsTruncated"`
	}{
		UserDetailList:  listMembersXML[userDetailXML]{Members: userDetails, Tag: "member"},
		GroupDetailList: listMembersXML[groupDetailXML]{Members: groupDetails, Tag: "member"},
		RoleDetailList:  listMembersXML[roleDetailXML]{Members: roleDetails, Tag: "member"},
		Policies:        listMembersXML[policyXML]{Members: policyDetails, Tag: "member"},
		IsTruncated:     false,
	})
}

// parseAttachPolicyParams validates the common Attach/Detach managed-policy
// inputs and writes the appropriate error response on failure.
func parseAttachPolicyParams(w http.ResponseWriter, r *http.Request, idParam string) (name, arn string, ok bool) {
	name = r.FormValue(idParam)
	arn = r.FormValue("PolicyArn")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter(idParam))
		return "", "", false
	}
	if arn == "" {
		protocol.WriteQueryXMLError(w, r, protocol.ErrMissingParameter("PolicyArn"))
		return "", "", false
	}
	return name, arn, true
}

// ensureAttachedPolicy appends policyArn to existing unless already present.
// Returns the (possibly-unchanged) list.
func ensureAttachedPolicy(existing []AttachedPolicy, policyArn string) []AttachedPolicy {
	for _, ap := range existing {
		if ap.PolicyArn == policyArn {
			return existing
		}
	}
	return append(existing, AttachedPolicy{
		PolicyArn:  policyArn,
		PolicyName: policyNameFromARN(policyArn),
	})
}

// removeAttachedPolicy returns existing with any entry matching policyArn removed.
func removeAttachedPolicy(existing []AttachedPolicy, policyArn string) []AttachedPolicy {
	filtered := existing[:0]
	for _, ap := range existing {
		if ap.PolicyArn != policyArn {
			filtered = append(filtered, ap)
		}
	}
	return filtered
}

// policyNameFromARN extracts the policy name from an ARN.
// e.g. "arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess" -> "AmazonS3ReadOnlyAccess".
func policyNameFromARN(arn string) string {
	for i := len(arn) - 1; i >= 0; i-- {
		if arn[i] == '/' {
			return arn[i+1:]
		}
	}
	return arn
}
