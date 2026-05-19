package iam

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/state"
)

const (
	nsUsers    = "iam:users"
	nsRoles    = "iam:roles"
	nsPolicies = "iam:policies"
	nsGroups   = "iam:groups"
	nsProfiles = "iam:profiles"
)

// User represents an IAM user.
type User struct {
	UserName         string            `json:"UserName"`
	UserId           string            `json:"UserId"`
	Arn              string            `json:"Arn"`
	Path             string            `json:"Path"`
	CreateDate       string            `json:"CreateDate"`
	AccessKeys       []AccessKey       `json:"AccessKeys,omitempty"`
	InlinePolicies   map[string]string `json:"InlinePolicies,omitempty"`
	AttachedPolicies []AttachedPolicy  `json:"AttachedPolicies,omitempty"`
	Tags             map[string]string `json:"Tags,omitempty"`
}

// AccessKey represents an IAM access key.
type AccessKey struct {
	AccessKeyId     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	Status          string `json:"Status"`
	UserName        string `json:"UserName"`
	CreateDate      string `json:"CreateDate"`
}

// Role represents an IAM role.
type Role struct {
	RoleName                 string            `json:"RoleName"`
	RoleId                   string            `json:"RoleId"`
	Arn                      string            `json:"Arn"`
	Path                     string            `json:"Path"`
	AssumeRolePolicyDocument string            `json:"AssumeRolePolicyDocument"`
	CreateDate               string            `json:"CreateDate"`
	AttachedPolicies         []AttachedPolicy  `json:"AttachedPolicies,omitempty"`
	InlinePolicies           map[string]string `json:"InlinePolicies,omitempty"`
	Tags                     map[string]string `json:"Tags,omitempty"`
}

// AttachedPolicy is a managed policy attached to a role.
type AttachedPolicy struct {
	PolicyArn  string `json:"PolicyArn"`
	PolicyName string `json:"PolicyName"`
}

// Policy represents an IAM managed policy.
type Policy struct {
	PolicyName      string `json:"PolicyName"`
	PolicyId        string `json:"PolicyId"`
	Arn             string `json:"Arn"`
	Path            string `json:"Path"`
	Document        string `json:"Document"`
	CreateDate      string `json:"CreateDate"`
	AttachmentCount int    `json:"AttachmentCount"`
}

// Group represents an IAM group.
type Group struct {
	GroupName        string            `json:"GroupName"`
	GroupId          string            `json:"GroupId"`
	Arn              string            `json:"Arn"`
	Path             string            `json:"Path"`
	CreateDate       string            `json:"CreateDate"`
	Members          []string          `json:"Members,omitempty"`
	AttachedPolicies []AttachedPolicy  `json:"AttachedPolicies,omitempty"`
	InlinePolicies   map[string]string `json:"InlinePolicies,omitempty"`
}

// InstanceProfile represents an IAM instance profile.
type InstanceProfile struct {
	InstanceProfileName string   `json:"InstanceProfileName"`
	InstanceProfileId   string   `json:"InstanceProfileId"`
	Arn                 string   `json:"Arn"`
	Path                string   `json:"Path"`
	CreateDate          string   `json:"CreateDate"`
	Roles               []string `json:"Roles,omitempty"`
}

// iamStore wraps state.Store with IAM-specific access helpers.
type iamStore struct {
	store state.Store
	cfg   *config.Config
	clk   clock.Clock
}

func newIAMStore(store state.Store, cfg *config.Config, clk clock.Clock) *iamStore {
	return &iamStore{store: store, cfg: cfg, clk: clk}
}

func (s *iamStore) arnForUser(path, name string) string {
	return fmt.Sprintf("arn:aws:iam::%s:user%s%s", s.cfg.AccountID, path, name)
}

func (s *iamStore) arnForRole(path, name string) string {
	return fmt.Sprintf("arn:aws:iam::%s:role%s%s", s.cfg.AccountID, path, name)
}

func (s *iamStore) arnForPolicy(path, name string) string {
	return fmt.Sprintf("arn:aws:iam::%s:policy%s%s", s.cfg.AccountID, path, name)
}

func (s *iamStore) arnForGroup(path, name string) string {
	return fmt.Sprintf("arn:aws:iam::%s:group%s%s", s.cfg.AccountID, path, name)
}

func (s *iamStore) arnForProfile(path, name string) string {
	return fmt.Sprintf("arn:aws:iam::%s:instance-profile%s%s", s.cfg.AccountID, path, name)
}

// normPath ensures a path value starts and ends with "/".
func normPath(p string) string {
	if p == "" {
		return "/"
	}
	return p
}

// ─── User operations ─────────────────────────────────────────────────────────

func (s *iamStore) getUser(ctx context.Context, name string) (*User, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsUsers, name)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, errNoSuchEntity("user", name)
	}
	var u User
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &u, nil
}

func (s *iamStore) putUser(ctx context.Context, u *User) *protocol.AWSError {
	raw, err := json.Marshal(u)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsUsers, u.UserName, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *iamStore) deleteUser(ctx context.Context, name string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsUsers, name); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *iamStore) listUsers(ctx context.Context) ([]User, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsUsers, "")
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	users := make([]User, 0, len(pairs))
	for _, pair := range pairs {
		var u User
		if err := json.Unmarshal([]byte(pair.Value), &u); err != nil {
			continue
		}
		users = append(users, u)
	}
	sort.Slice(users, func(i, j int) bool { return users[i].UserName < users[j].UserName })
	return users, nil
}

// ─── Role operations ──────────────────────────────────────────────────────────

func (s *iamStore) getRole(ctx context.Context, name string) (*Role, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsRoles, name)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, errNoSuchEntity("role", name)
	}
	var r Role
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &r, nil
}

func (s *iamStore) putRole(ctx context.Context, r *Role) *protocol.AWSError {
	raw, err := json.Marshal(r)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsRoles, r.RoleName, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *iamStore) deleteRole(ctx context.Context, name string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsRoles, name); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *iamStore) listRoles(ctx context.Context) ([]Role, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsRoles, "")
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	roles := make([]Role, 0, len(pairs))
	for _, pair := range pairs {
		var r Role
		if err := json.Unmarshal([]byte(pair.Value), &r); err != nil {
			continue
		}
		roles = append(roles, r)
	}
	sort.Slice(roles, func(i, j int) bool { return roles[i].RoleName < roles[j].RoleName })
	return roles, nil
}

// ─── Policy operations ────────────────────────────────────────────────────────

func (s *iamStore) getPolicy(ctx context.Context, arn string) (*Policy, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsPolicies, arn)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, errNoSuchEntity("policy", arn)
	}
	var p Policy
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &p, nil
}

func (s *iamStore) putPolicy(ctx context.Context, p *Policy) *protocol.AWSError {
	raw, err := json.Marshal(p)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsPolicies, p.Arn, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *iamStore) deletePolicy(ctx context.Context, arn string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsPolicies, arn); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *iamStore) listPolicies(ctx context.Context) ([]Policy, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsPolicies, "")
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	policies := make([]Policy, 0, len(pairs))
	for _, pair := range pairs {
		var p Policy
		if err := json.Unmarshal([]byte(pair.Value), &p); err != nil {
			continue
		}
		policies = append(policies, p)
	}
	sort.Slice(policies, func(i, j int) bool { return policies[i].PolicyName < policies[j].PolicyName })
	return policies, nil
}

// ─── Group operations ─────────────────────────────────────────────────────────

func (s *iamStore) getGroup(ctx context.Context, name string) (*Group, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsGroups, name)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, errNoSuchEntity("group", name)
	}
	var g Group
	if err := json.Unmarshal([]byte(raw), &g); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &g, nil
}

func (s *iamStore) putGroup(ctx context.Context, g *Group) *protocol.AWSError {
	raw, err := json.Marshal(g)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsGroups, g.GroupName, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *iamStore) deleteGroup(ctx context.Context, name string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsGroups, name); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *iamStore) listGroupsForUser(ctx context.Context, userName string) ([]Group, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsGroups, "")
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	var groups []Group
	for _, pair := range pairs {
		var g Group
		if err := json.Unmarshal([]byte(pair.Value), &g); err != nil {
			continue
		}
		for _, m := range g.Members {
			if m == userName {
				groups = append(groups, g)
				break
			}
		}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].GroupName < groups[j].GroupName })
	return groups, nil
}

// ─── Instance Profile operations ──────────────────────────────────────────────

func (s *iamStore) getProfile(ctx context.Context, name string) (*InstanceProfile, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsProfiles, name)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, errNoSuchEntity("instance profile", name)
	}
	var p InstanceProfile
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &p, nil
}

func (s *iamStore) putProfile(ctx context.Context, p *InstanceProfile) *protocol.AWSError {
	raw, err := json.Marshal(p)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsProfiles, p.InstanceProfileName, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *iamStore) deleteProfile(ctx context.Context, name string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsProfiles, name); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *iamStore) listProfiles(ctx context.Context) ([]InstanceProfile, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsProfiles, "")
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	profiles := make([]InstanceProfile, 0, len(pairs))
	for _, pair := range pairs {
		var p InstanceProfile
		if err := json.Unmarshal([]byte(pair.Value), &p); err != nil {
			continue
		}
		profiles = append(profiles, p)
	}
	sort.Slice(profiles, func(i, j int) bool {
		return profiles[i].InstanceProfileName < profiles[j].InstanceProfileName
	})
	return profiles, nil
}

func (s *iamStore) listProfilesForRole(ctx context.Context, roleName string) ([]InstanceProfile, *protocol.AWSError) {
	all, aerr := s.listProfiles(ctx)
	if aerr != nil {
		return nil, aerr
	}
	var result []InstanceProfile
	for _, p := range all {
		for _, rn := range p.Roles {
			if rn == roleName {
				result = append(result, p)
				break
			}
		}
	}
	return result, nil
}

func (s *iamStore) listGroups(ctx context.Context) ([]Group, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsGroups, "")
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	groups := make([]Group, 0, len(pairs))
	for _, pair := range pairs {
		var g Group
		if err := json.Unmarshal([]byte(pair.Value), &g); err != nil {
			continue
		}
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].GroupName < groups[j].GroupName })
	return groups, nil
}

// ─── Error helpers ────────────────────────────────────────────────────────────

func errNoSuchEntity(kind, name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "NoSuchEntity",
		Message:    "The " + kind + " with name " + name + " cannot be found.",
		HTTPStatus: 404,
	}
}

func errEntityAlreadyExists(kind, name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "EntityAlreadyExists",
		Message:    "The " + kind + " with name " + name + " already exists.",
		HTTPStatus: 409,
	}
}
