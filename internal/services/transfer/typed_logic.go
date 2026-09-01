package transfer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

type createServerRequest struct {
	EndpointType         string        `json:"EndpointType" cbor:"EndpointType"`
	IdentityProviderType string        `json:"IdentityProviderType" cbor:"IdentityProviderType"`
	Tags                 []transferTag `json:"Tags" cbor:"Tags"`
}

type createServerResponse struct {
	ServerId string `json:"ServerId" cbor:"ServerId"`
}

type describeServerRequest struct {
	ServerID string `json:"ServerId" cbor:"ServerId"`
}

type describeServerResponse struct {
	Server *transferServer `json:"Server" cbor:"Server"`
}

type listServersRequest struct{}

type serverListItem struct {
	ServerId     string `json:"ServerId" cbor:"ServerId"`
	Arn          string `json:"Arn" cbor:"Arn"`
	State        string `json:"State" cbor:"State"`
	EndpointType string `json:"EndpointType" cbor:"EndpointType"`
}

type listServersResponse struct {
	Servers []serverListItem `json:"Servers" cbor:"Servers"`
}

type updateServerRequest struct {
	ServerID             string `json:"ServerId" cbor:"ServerId"`
	EndpointType         string `json:"EndpointType" cbor:"EndpointType"`
	IdentityProviderType string `json:"IdentityProviderType" cbor:"IdentityProviderType"`
}

type deleteServerRequest struct {
	ServerID string `json:"ServerId" cbor:"ServerId"`
}

type createUserRequest struct {
	ServerID      string        `json:"ServerId" cbor:"ServerId"`
	UserName      string        `json:"UserName" cbor:"UserName"`
	Role          string        `json:"Role" cbor:"Role"`
	HomeDirectory string        `json:"HomeDirectory" cbor:"HomeDirectory"`
	Policy        string        `json:"Policy" cbor:"Policy"`
	Tags          []transferTag `json:"Tags" cbor:"Tags"`
}

type createUserResponse struct {
	ServerId string `json:"ServerId" cbor:"ServerId"`
	UserName string `json:"UserName" cbor:"UserName"`
}

type describeUserRequest struct {
	ServerID string `json:"ServerId" cbor:"ServerId"`
	UserName string `json:"UserName" cbor:"UserName"`
}

type describeUserResponse struct {
	User *transferUser `json:"User" cbor:"User"`
}

type listUsersRequest struct {
	ServerID string `json:"ServerId" cbor:"ServerId"`
}

type userListItem struct {
	Arn           string `json:"Arn" cbor:"Arn"`
	HomeDirectory string `json:"HomeDirectory" cbor:"HomeDirectory"`
	Role          string `json:"Role" cbor:"Role"`
	UserName      string `json:"UserName" cbor:"UserName"`
}

type listUsersResponse struct {
	Users []userListItem `json:"Users" cbor:"Users"`
}

type updateUserRequest struct {
	ServerID      string `json:"ServerId" cbor:"ServerId"`
	UserName      string `json:"UserName" cbor:"UserName"`
	Role          string `json:"Role" cbor:"Role"`
	HomeDirectory string `json:"HomeDirectory" cbor:"HomeDirectory"`
	Policy        string `json:"Policy" cbor:"Policy"`
}

type updateUserResponse struct {
	ServerId string `json:"ServerId" cbor:"ServerId"`
	UserName string `json:"UserName" cbor:"UserName"`
}

type deleteUserRequest struct {
	ServerID string `json:"ServerId" cbor:"ServerId"`
	UserName string `json:"UserName" cbor:"UserName"`
}

// ---- Resource tagging --------------------------------------------------------
//
// Transfer's tag operations address the resource by an `Arn` member, not the
// `ResourceArn` most services use.

// transferTag is one tag in AWS' wire shape.
type transferTag struct {
	Key   string `json:"Key" cbor:"Key"`
	Value string `json:"Value" cbor:"Value"`
}

type tagResourceRequest struct {
	Arn  string        `json:"Arn" cbor:"Arn"`
	Tags []transferTag `json:"Tags" cbor:"Tags"`
}

type untagResourceRequest struct {
	Arn     string   `json:"Arn" cbor:"Arn"`
	TagKeys []string `json:"TagKeys" cbor:"TagKeys"`
}

type listTagsForResourceRequest struct {
	Arn string `json:"Arn" cbor:"Arn"`
}

type listTagsForResourceResponse struct {
	Arn  string        `json:"Arn" cbor:"Arn"`
	Tags []transferTag `json:"Tags" cbor:"Tags"`
}

// transferTagCfg tunes shared tag validation to Transfer's error shape.
var transferTagCfg = serviceutil.TagValidationConfig{
	ExceededCode:    "InvalidRequestException",
	InvalidCode:     "InvalidRequestException",
	ExceededMessage: "A resource can have a maximum of 50 tags.",
}

// tagMap and tagList convert between the stored wire shape and the map the
// shared validation helper works in. tagList sorts, because Go randomizes map
// iteration per process and the array would otherwise reorder between
// otherwise identical responses.
func tagMap(list []transferTag) map[string]string {
	out := make(map[string]string, len(list))
	for _, t := range list {
		out[t.Key] = t.Value
	}
	return out
}

func tagList(m map[string]string) []transferTag {
	out := make([]transferTag, 0, len(m))
	for k, v := range m {
		out = append(out, transferTag{Key: k, Value: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// validatedTagList validates an inline Tags list from a create call.
func validatedTagList(list []transferTag) ([]transferTag, *protocol.AWSError) {
	if len(list) == 0 {
		return nil, nil
	}
	m := tagMap(list)
	if aerr := serviceutil.ValidateTags(transferTagCfg, m); aerr != nil {
		return nil, aerr
	}
	return tagList(m), nil
}

// taggedResource is either of the two resources Transfer can tag, reduced to
// what the tag operations need: read the current tags, write them back.
type taggedResource struct {
	tags []transferTag
	save func(ctx context.Context, tags []transferTag) *protocol.AWSError
}

// resolveTaggedResource maps a Transfer ARN onto the resource it names.
// Agreements, certificates, connectors, profiles and workflows are Transfer's
// other taggable resources; none is emulated, so their ARNs are refused rather
// than silently accepted.
func (s *Service) resolveTaggedResource(ctx context.Context, arn string) (*taggedResource, *protocol.AWSError) {
	if arn == "" {
		return nil, validationErr("Arn is required")
	}
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 6 {
		return nil, validationErr(fmt.Sprintf("Invalid resource ARN: %s", arn))
	}
	resource := parts[5]
	switch {
	case strings.HasPrefix(resource, "server/"):
		id := strings.TrimPrefix(resource, "server/")
		server, exists, aerr := s.getServer(ctx, id)
		if aerr != nil {
			return nil, aerr
		}
		if !exists {
			return nil, notFoundErr("Server not found")
		}
		return &taggedResource{
			tags: server.Tags,
			save: func(ctx context.Context, tags []transferTag) *protocol.AWSError {
				server.Tags = tags
				return s.putServer(ctx, server)
			},
		}, nil
	case strings.HasPrefix(resource, "user/"):
		// user/<serverId>/<userName>
		rest := strings.TrimPrefix(resource, "user/")
		serverID, userName, ok := strings.Cut(rest, "/")
		if !ok || serverID == "" || userName == "" {
			return nil, validationErr(fmt.Sprintf("Invalid resource ARN: %s", arn))
		}
		key := userKey(serverID, userName)
		user, exists, aerr := s.getUser(ctx, key)
		if aerr != nil {
			return nil, aerr
		}
		if !exists {
			return nil, notFoundErr("User not found")
		}
		return &taggedResource{
			tags: user.Tags,
			save: func(ctx context.Context, tags []transferTag) *protocol.AWSError {
				user.Tags = tags
				return s.putUser(ctx, key, user)
			},
		}, nil
	default:
		return nil, validationErr(fmt.Sprintf("Invalid resource ARN: %s", arn))
	}
}

func (s *Service) tagResourceTyped(ctx context.Context, req *tagResourceRequest) (*struct{}, *protocol.AWSError) {
	res, aerr := s.resolveTaggedResource(ctx, req.Arn)
	if aerr != nil {
		return nil, aerr
	}
	merged := tagMap(res.tags)
	for _, t := range req.Tags {
		merged[t.Key] = t.Value
	}
	if aerr := serviceutil.ValidateTags(transferTagCfg, merged); aerr != nil {
		return nil, aerr
	}
	if aerr := res.save(ctx, tagList(merged)); aerr != nil {
		return nil, aerr
	}
	return &struct{}{}, nil
}

func (s *Service) untagResourceTyped(ctx context.Context, req *untagResourceRequest) (*struct{}, *protocol.AWSError) {
	res, aerr := s.resolveTaggedResource(ctx, req.Arn)
	if aerr != nil {
		return nil, aerr
	}
	remaining := tagMap(res.tags)
	for _, k := range req.TagKeys {
		delete(remaining, k)
	}
	if aerr := res.save(ctx, tagList(remaining)); aerr != nil {
		return nil, aerr
	}
	return &struct{}{}, nil
}

func (s *Service) listTagsForResourceTyped(ctx context.Context, req *listTagsForResourceRequest) (*listTagsForResourceResponse, *protocol.AWSError) {
	res, aerr := s.resolveTaggedResource(ctx, req.Arn)
	if aerr != nil {
		return nil, aerr
	}
	tags := res.tags
	if tags == nil {
		tags = []transferTag{}
	}
	return &listTagsForResourceResponse{Arn: req.Arn, Tags: tags}, nil
}

func (s *Service) createServerTyped(ctx context.Context, req *createServerRequest) (*createServerResponse, *protocol.AWSError) {
	kvs, err := s.store.Scan(ctx, nsServers, "")
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	tags, aerr := validatedTagList(req.Tags)
	if aerr != nil {
		return nil, aerr
	}
	id := fmt.Sprintf("s-%08d", len(kvs)+1)
	server := transferServer{
		ServerID:             id,
		Arn:                  s.serverARN(id),
		EndpointType:         defaultString(req.EndpointType, "PUBLIC"),
		IdentityProviderType: defaultString(req.IdentityProviderType, "SERVICE_MANAGED"),
		State:                "ONLINE",
		Tags:                 tags,
	}
	if aerr := s.putServer(ctx, &server); aerr != nil {
		return nil, aerr
	}
	return &createServerResponse{ServerId: server.ServerID}, nil
}

func (s *Service) describeServerTyped(ctx context.Context, req *describeServerRequest) (*describeServerResponse, *protocol.AWSError) {
	server, exists, aerr := s.getServer(ctx, req.ServerID)
	if aerr != nil {
		return nil, aerr
	}
	if !exists {
		return nil, notFoundErr("Server not found")
	}
	return &describeServerResponse{Server: server}, nil
}

func (s *Service) listServersTyped(ctx context.Context, _ *listServersRequest) (*listServersResponse, *protocol.AWSError) {
	kvs, err := s.store.Scan(ctx, nsServers, "")
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	out := make([]serverListItem, 0, len(kvs))
	for _, kv := range kvs {
		var server transferServer
		if json.Unmarshal([]byte(kv.Value), &server) == nil {
			out = append(out, serverListItem{
				ServerId:     server.ServerID,
				Arn:          server.Arn,
				State:        server.State,
				EndpointType: server.EndpointType,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ServerId < out[j].ServerId })
	return &listServersResponse{Servers: out}, nil
}

func (s *Service) updateServerTyped(ctx context.Context, req *updateServerRequest) (*struct{}, *protocol.AWSError) {
	server, exists, aerr := s.getServer(ctx, req.ServerID)
	if aerr != nil {
		return nil, aerr
	}
	if !exists {
		return nil, notFoundErr("Server not found")
	}
	if req.EndpointType != "" {
		server.EndpointType = req.EndpointType
	}
	if req.IdentityProviderType != "" {
		server.IdentityProviderType = req.IdentityProviderType
	}
	if aerr := s.putServer(ctx, server); aerr != nil {
		return nil, aerr
	}
	return &struct{}{}, nil
}

func (s *Service) deleteServerTyped(ctx context.Context, req *deleteServerRequest) (*struct{}, *protocol.AWSError) {
	if _, exists, aerr := s.getServer(ctx, req.ServerID); aerr != nil {
		return nil, aerr
	} else if !exists {
		return nil, notFoundErr("Server not found")
	}
	if err := s.store.Delete(ctx, nsServers, req.ServerID); err != nil {
		return nil, protocol.ErrInternalError
	}
	users, err := s.store.Scan(ctx, nsUsers, req.ServerID+"/")
	if err == nil {
		for _, u := range users {
			_ = s.store.Delete(ctx, nsUsers, u.Key)
		}
	}
	return &struct{}{}, nil
}

func (s *Service) createUserTyped(ctx context.Context, req *createUserRequest) (*createUserResponse, *protocol.AWSError) {
	if req.ServerID == "" || req.UserName == "" || req.Role == "" {
		return nil, validationErr("ServerId, UserName, and Role are required")
	}
	if _, exists, aerr := s.getServer(ctx, req.ServerID); aerr != nil {
		return nil, aerr
	} else if !exists {
		return nil, notFoundErr("Server not found")
	}
	key := userKey(req.ServerID, req.UserName)
	if _, exists, aerr := s.getUser(ctx, key); aerr != nil {
		return nil, aerr
	} else if exists {
		return nil, &protocol.AWSError{Code: "ConflictException", Message: "User already exists", HTTPStatus: http.StatusConflict}
	}
	tags, aerr := validatedTagList(req.Tags)
	if aerr != nil {
		return nil, aerr
	}
	user := transferUser{
		ServerID:      req.ServerID,
		UserName:      req.UserName,
		Arn:           s.userARN(req.ServerID, req.UserName),
		Role:          req.Role,
		HomeDirectory: req.HomeDirectory,
		Policy:        req.Policy,
		Tags:          tags,
	}
	if aerr := s.putUser(ctx, key, &user); aerr != nil {
		return nil, aerr
	}
	return &createUserResponse{ServerId: req.ServerID, UserName: req.UserName}, nil
}

func (s *Service) describeUserTyped(ctx context.Context, req *describeUserRequest) (*describeUserResponse, *protocol.AWSError) {
	user, exists, aerr := s.getUser(ctx, userKey(req.ServerID, req.UserName))
	if aerr != nil {
		return nil, aerr
	}
	if !exists {
		return nil, notFoundErr("User not found")
	}
	return &describeUserResponse{User: user}, nil
}

func (s *Service) listUsersTyped(ctx context.Context, req *listUsersRequest) (*listUsersResponse, *protocol.AWSError) {
	kvs, err := s.store.Scan(ctx, nsUsers, req.ServerID+"/")
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	out := make([]userListItem, 0, len(kvs))
	for _, kv := range kvs {
		var user transferUser
		if json.Unmarshal([]byte(kv.Value), &user) == nil {
			out = append(out, userListItem{Arn: user.Arn, HomeDirectory: user.HomeDirectory, Role: user.Role, UserName: user.UserName})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UserName < out[j].UserName })
	return &listUsersResponse{Users: out}, nil
}

func (s *Service) updateUserTyped(ctx context.Context, req *updateUserRequest) (*updateUserResponse, *protocol.AWSError) {
	key := userKey(req.ServerID, req.UserName)
	user, exists, aerr := s.getUser(ctx, key)
	if aerr != nil {
		return nil, aerr
	}
	if !exists {
		return nil, notFoundErr("User not found")
	}
	if req.Role != "" {
		user.Role = req.Role
	}
	if req.HomeDirectory != "" {
		user.HomeDirectory = req.HomeDirectory
	}
	if req.Policy != "" {
		user.Policy = req.Policy
	}
	if aerr := s.putUser(ctx, key, user); aerr != nil {
		return nil, aerr
	}
	return &updateUserResponse{ServerId: req.ServerID, UserName: req.UserName}, nil
}

func (s *Service) deleteUserTyped(ctx context.Context, req *deleteUserRequest) (*struct{}, *protocol.AWSError) {
	key := userKey(req.ServerID, req.UserName)
	if _, exists, aerr := s.getUser(ctx, key); aerr != nil {
		return nil, aerr
	} else if !exists {
		return nil, notFoundErr("User not found")
	}
	if err := s.store.Delete(ctx, nsUsers, key); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &struct{}{}, nil
}

func notFoundErr(msg string) *protocol.AWSError {
	return &protocol.AWSError{Code: "ResourceNotFoundException", Message: msg, HTTPStatus: http.StatusNotFound}
}

func validationErr(msg string) *protocol.AWSError {
	return &protocol.AWSError{Code: "ValidationException", Message: msg, HTTPStatus: http.StatusBadRequest}
}
