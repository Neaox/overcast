package transfer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	serviceName   = "transfer"
	targetPrefix  = "TransferService."
	nsServers     = "transfer:servers"
	nsUsers       = "transfer:users"
	defaultRegion = "us-east-1"
)

type Service struct {
	cfg     *config.Config
	store   state.Store
	log     *serviceutil.ServiceLogger
	clk     clock.Clock
	typedOp map[string]op.Operation
}

func New(cfg *config.Config, st state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	s := &Service{
		cfg:   cfg,
		store: st,
		log:   serviceutil.NewServiceLogger(logger, serviceName),
		clk:   clk,
	}
	s.typedOp = s.typedOps()
	return s
}

func (s *Service) Name() string { return serviceName }

func (s *Service) TargetPrefix() string { return targetPrefix }

func (s *Service) RegisterRoutes(_ chi.Router) {}

func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			w.Header().Set("x-emulator-unsupported-protocol", c.Name())
			c.WriteError(w, r, &protocol.AWSError{
				Code:       "UnsupportedProtocol",
				Message:    "Transfer does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
		if c.Name() != codec.NameRPCv2CBOR {
			s.dispatchLegacy(w, r, opName)
			return
		}
		if typed, ok := s.typedOp[opName]; ok {
			typed.Invoke(w, r, c)
			return
		}
		c.WriteError(w, r, protocol.ErrNotImplemented)
		return
	}

	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
	s.dispatchLegacy(w, r, op)
}

func (s *Service) dispatchLegacy(w http.ResponseWriter, r *http.Request, op string) {
	switch op {
	case "CreateServer":
		s.createServer(w, r)
	case "DescribeServer":
		s.describeServer(w, r)
	case "ListServers":
		s.listServers(w, r)
	case "UpdateServer":
		s.updateServer(w, r)
	case "DeleteServer":
		s.deleteServer(w, r)
	case "CreateUser":
		s.createUser(w, r)
	case "DescribeUser":
		s.describeUser(w, r)
	case "ListUsers":
		s.listUsers(w, r)
	case "UpdateUser":
		s.updateUser(w, r)
	case "DeleteUser":
		s.deleteUser(w, r)
	default:
		protocol.NotImplementedJSON(w, r)
	}
}

type transferServer struct {
	ServerID             string `json:"ServerId"`
	Arn                  string `json:"Arn"`
	EndpointType         string `json:"EndpointType"`
	IdentityProviderType string `json:"IdentityProviderType"`
	State                string `json:"State"`
	CreatedAt            string `json:"CreatedAt"`
}

type transferUser struct {
	ServerID      string `json:"ServerId"`
	UserName      string `json:"UserName"`
	Arn           string `json:"Arn"`
	Role          string `json:"Role"`
	HomeDirectory string `json:"HomeDirectory,omitempty"`
	Policy        string `json:"Policy,omitempty"`
}

func (s *Service) createServer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		EndpointType         string `json:"EndpointType"`
		IdentityProviderType string `json:"IdentityProviderType"`
	}
	if !decodeJSONBody(w, r, &in) {
		return
	}
	kvs, err := s.store.Scan(r.Context(), nsServers, "")
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	id := fmt.Sprintf("s-%08d", len(kvs)+1)
	server := transferServer{
		ServerID:             id,
		Arn:                  s.serverARN(id),
		EndpointType:         defaultString(in.EndpointType, "PUBLIC"),
		IdentityProviderType: defaultString(in.IdentityProviderType, "SERVICE_MANAGED"),
		State:                "ONLINE",
		CreatedAt:            s.clk.Now().Format(time.RFC3339),
	}
	if aerr := s.putServer(r.Context(), &server); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"ServerId": server.ServerID})
}

func (s *Service) describeServer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ServerID string `json:"ServerId"`
	}
	if !decodeJSONBody(w, r, &in) {
		return
	}
	server, exists, aerr := s.getServer(r.Context(), in.ServerID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if !exists {
		notFound(w, r, "Server not found")
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Server": server})
}

func (s *Service) listServers(w http.ResponseWriter, r *http.Request) {
	kvs, err := s.store.Scan(r.Context(), nsServers, "")
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	out := make([]map[string]any, 0, len(kvs))
	for _, kv := range kvs {
		var server transferServer
		if json.Unmarshal([]byte(kv.Value), &server) == nil {
			out = append(out, map[string]any{
				"ServerId":     server.ServerID,
				"Arn":          server.Arn,
				"State":        server.State,
				"EndpointType": server.EndpointType,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["ServerId"].(string) < out[j]["ServerId"].(string) })
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Servers": out})
}

func (s *Service) updateServer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ServerID             string `json:"ServerId"`
		EndpointType         string `json:"EndpointType"`
		IdentityProviderType string `json:"IdentityProviderType"`
	}
	if !decodeJSONBody(w, r, &in) {
		return
	}
	server, exists, aerr := s.getServer(r.Context(), in.ServerID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if !exists {
		notFound(w, r, "Server not found")
		return
	}
	if in.EndpointType != "" {
		server.EndpointType = in.EndpointType
	}
	if in.IdentityProviderType != "" {
		server.IdentityProviderType = in.IdentityProviderType
	}
	if aerr := s.putServer(r.Context(), server); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (s *Service) deleteServer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ServerID string `json:"ServerId"`
	}
	if !decodeJSONBody(w, r, &in) {
		return
	}
	if _, exists, aerr := s.getServer(r.Context(), in.ServerID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	} else if !exists {
		notFound(w, r, "Server not found")
		return
	}
	if err := s.store.Delete(r.Context(), nsServers, in.ServerID); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	users, err := s.store.Scan(r.Context(), nsUsers, in.ServerID+"/")
	if err == nil {
		for _, u := range users {
			_ = s.store.Delete(r.Context(), nsUsers, u.Key)
		}
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (s *Service) createUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ServerID      string `json:"ServerId"`
		UserName      string `json:"UserName"`
		Role          string `json:"Role"`
		HomeDirectory string `json:"HomeDirectory"`
		Policy        string `json:"Policy"`
	}
	if !decodeJSONBody(w, r, &in) {
		return
	}
	if in.ServerID == "" || in.UserName == "" || in.Role == "" {
		validationError(w, r, "ServerId, UserName, and Role are required")
		return
	}
	if _, exists, aerr := s.getServer(r.Context(), in.ServerID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	} else if !exists {
		notFound(w, r, "Server not found")
		return
	}
	key := userKey(in.ServerID, in.UserName)
	if _, exists, aerr := s.getUser(r.Context(), key); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	} else if exists {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "ConflictException", Message: "User already exists", HTTPStatus: http.StatusConflict})
		return
	}
	user := transferUser{
		ServerID:      in.ServerID,
		UserName:      in.UserName,
		Arn:           s.userARN(in.ServerID, in.UserName),
		Role:          in.Role,
		HomeDirectory: in.HomeDirectory,
		Policy:        in.Policy,
	}
	if aerr := s.putUser(r.Context(), key, &user); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"ServerId": in.ServerID, "UserName": in.UserName})
}

func (s *Service) describeUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ServerID string `json:"ServerId"`
		UserName string `json:"UserName"`
	}
	if !decodeJSONBody(w, r, &in) {
		return
	}
	user, exists, aerr := s.getUser(r.Context(), userKey(in.ServerID, in.UserName))
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if !exists {
		notFound(w, r, "User not found")
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"User": user})
}

func (s *Service) listUsers(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ServerID string `json:"ServerId"`
	}
	if !decodeJSONBody(w, r, &in) {
		return
	}
	kvs, err := s.store.Scan(r.Context(), nsUsers, in.ServerID+"/")
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	out := make([]map[string]any, 0, len(kvs))
	for _, kv := range kvs {
		var user transferUser
		if json.Unmarshal([]byte(kv.Value), &user) == nil {
			out = append(out, map[string]any{"Arn": user.Arn, "HomeDirectory": user.HomeDirectory, "Role": user.Role, "UserName": user.UserName})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["UserName"].(string) < out[j]["UserName"].(string) })
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Users": out})
}

func (s *Service) updateUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ServerID      string `json:"ServerId"`
		UserName      string `json:"UserName"`
		Role          string `json:"Role"`
		HomeDirectory string `json:"HomeDirectory"`
		Policy        string `json:"Policy"`
	}
	if !decodeJSONBody(w, r, &in) {
		return
	}
	key := userKey(in.ServerID, in.UserName)
	user, exists, aerr := s.getUser(r.Context(), key)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if !exists {
		notFound(w, r, "User not found")
		return
	}
	if in.Role != "" {
		user.Role = in.Role
	}
	if in.HomeDirectory != "" {
		user.HomeDirectory = in.HomeDirectory
	}
	if in.Policy != "" {
		user.Policy = in.Policy
	}
	if aerr := s.putUser(r.Context(), key, user); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"ServerId": in.ServerID, "UserName": in.UserName})
}

func (s *Service) deleteUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ServerID string `json:"ServerId"`
		UserName string `json:"UserName"`
	}
	if !decodeJSONBody(w, r, &in) {
		return
	}
	key := userKey(in.ServerID, in.UserName)
	if _, exists, aerr := s.getUser(r.Context(), key); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	} else if !exists {
		notFound(w, r, "User not found")
		return
	}
	if err := s.store.Delete(r.Context(), nsUsers, key); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (s *Service) getServer(ctx context.Context, id string) (*transferServer, bool, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsServers, id)
	if err != nil {
		return nil, false, protocol.ErrInternalError
	}
	if !ok {
		return nil, false, nil
	}
	var server transferServer
	if err := json.Unmarshal([]byte(raw), &server); err != nil {
		return nil, false, protocol.ErrInternalError
	}
	return &server, true, nil
}

func (s *Service) putServer(ctx context.Context, server *transferServer) *protocol.AWSError {
	b, err := json.Marshal(server)
	if err != nil {
		return protocol.ErrInternalError
	}
	if err := s.store.Set(ctx, nsServers, server.ServerID, string(b)); err != nil {
		return protocol.ErrInternalError
	}
	return nil
}

func (s *Service) getUser(ctx context.Context, key string) (*transferUser, bool, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsUsers, key)
	if err != nil {
		return nil, false, protocol.ErrInternalError
	}
	if !ok {
		return nil, false, nil
	}
	var user transferUser
	if err := json.Unmarshal([]byte(raw), &user); err != nil {
		return nil, false, protocol.ErrInternalError
	}
	return &user, true, nil
}

func (s *Service) putUser(ctx context.Context, key string, user *transferUser) *protocol.AWSError {
	b, err := json.Marshal(user)
	if err != nil {
		return protocol.ErrInternalError
	}
	if err := s.store.Set(ctx, nsUsers, key, string(b)); err != nil {
		return protocol.ErrInternalError
	}
	return nil
}

func (s *Service) region() string {
	if s.cfg != nil && s.cfg.Region != "" {
		return s.cfg.Region
	}
	return defaultRegion
}

func (s *Service) accountID() string {
	if s.cfg != nil && s.cfg.AccountID != "" {
		return s.cfg.AccountID
	}
	return "000000000000"
}

func (s *Service) serverARN(serverID string) string {
	return fmt.Sprintf("arn:aws:transfer:%s:%s:server/%s", s.region(), s.accountID(), serverID)
}

func (s *Service) userARN(serverID, userName string) string {
	return fmt.Sprintf("arn:aws:transfer:%s:%s:user/%s/%s", s.region(), s.accountID(), serverID, userName)
}

func userKey(serverID, userName string) string { return serverID + "/" + userName }

func defaultString(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, out any) bool {
	if r.Body == nil {
		return true
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(out); err != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "SerializationException", Message: "Invalid JSON request body", HTTPStatus: http.StatusBadRequest})
		return false
	}
	return true
}

func validationError(w http.ResponseWriter, r *http.Request, msg string) {
	protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "ValidationException", Message: msg, HTTPStatus: http.StatusBadRequest})
}

func notFound(w http.ResponseWriter, r *http.Request, msg string) {
	protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "ResourceNotFoundException", Message: msg, HTTPStatus: http.StatusNotFound})
}
