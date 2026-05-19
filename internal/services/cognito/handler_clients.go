package cognito

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// createUserPoolClient — CreateUserPoolClient.
func (s *Service) createUserPoolClient(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserPoolID                      string                  `json:"UserPoolId"`
		ClientName                      string                  `json:"ClientName"`
		GenerateSecret                  bool                    `json:"GenerateSecret"`
		AccessTokenValidity             int                     `json:"AccessTokenValidity"`
		IdTokenValidity                 int                     `json:"IdTokenValidity"`
		RefreshTokenValidity            int                     `json:"RefreshTokenValidity"`
		TokenValidityUnits              *TokenValidityUnitsType `json:"TokenValidityUnits"`
		CallbackURLs                    []string                `json:"CallbackURLs"`
		LogoutURLs                      []string                `json:"LogoutURLs"`
		AllowedOAuthFlows               []string                `json:"AllowedOAuthFlows"`
		AllowedOAuthScopes              []string                `json:"AllowedOAuthScopes"`
		AllowedOAuthFlowsUserPoolClient bool                    `json:"AllowedOAuthFlowsUserPoolClient"`
		ExplicitAuthFlows               []string                `json:"ExplicitAuthFlows"`
		SupportedIdentityProviders      []string                `json:"SupportedIdentityProviders"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.UserPoolID, "UserPoolId") {
		return
	}
	if !serviceutil.RequireString(w, r, req.ClientName, "ClientName") {
		return
	}
	pool, ok := s.requirePool(r.Context(), w, r, req.UserPoolID)
	if !ok {
		return
	}
	if aerr := validateExplicitAuthFlows(req.ExplicitAuthFlows); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if aerr := validateExplicitAuthFlowsForTier(pool, req.ExplicitAuthFlows); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	secret := ""
	if req.GenerateSecret {
		secret = generateClientSecret()
	}
	c := &UserPoolClient{
		ClientID:                        generateClientID(),
		ClientName:                      req.ClientName,
		UserPoolID:                      req.UserPoolID,
		CreatedAt:                       s.clk.Now(),
		ClientSecret:                    secret,
		AccessTokenValidity:             req.AccessTokenValidity,
		IdTokenValidity:                 req.IdTokenValidity,
		RefreshTokenValidity:            req.RefreshTokenValidity,
		TokenValidityUnits:              req.TokenValidityUnits,
		CallbackURLs:                    req.CallbackURLs,
		LogoutURLs:                      req.LogoutURLs,
		AllowedOAuthFlows:               req.AllowedOAuthFlows,
		AllowedOAuthScopes:              req.AllowedOAuthScopes,
		AllowedOAuthFlowsUserPoolClient: req.AllowedOAuthFlowsUserPoolClient,
		ExplicitAuthFlows:               req.ExplicitAuthFlows,
		SupportedIdentityProviders:      req.SupportedIdentityProviders,
	}
	applyClientDefaults(c)
	if err := s.saveClient(r.Context(), c); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	s.log.Info("user pool client created",
		zap.String("poolId", req.UserPoolID), zap.String("clientId", c.ClientID))
	s.publish(r, events.CognitoClientCreated, events.ResourcePayload{Name: c.ClientName})
	s.writeJSON(w, r, http.StatusOK, map[string]any{"UserPoolClient": toClientWire(c)})
}

// generateClientSecret returns a cryptographically random 51-character base64 string
// matching the format AWS Cognito uses for app client secrets.
func generateClientSecret() string {
	b := make([]byte, 38) // 38 bytes → 51 base64 chars (round(38*4/3))
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)[:51]
}

// describeUserPoolClient — DescribeUserPoolClient.
func (s *Service) describeUserPoolClient(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserPoolID string `json:"UserPoolId"`
		ClientID   string `json:"ClientId"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.UserPoolID, "UserPoolId") {
		return
	}
	if !serviceutil.RequireString(w, r, req.ClientID, "ClientId") {
		return
	}
	if _, ok := s.requirePool(r.Context(), w, r, req.UserPoolID); !ok {
		return
	}
	c, err := s.loadClient(r.Context(), req.UserPoolID, req.ClientID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if c == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Client " + req.ClientID + " does not exist.",
			HTTPStatus: 400,
		})
		return
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{"UserPoolClient": toClientWire(c)})
}

// deleteUserPoolClient — DeleteUserPoolClient.
func (s *Service) deleteUserPoolClient(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserPoolID string `json:"UserPoolId"`
		ClientID   string `json:"ClientId"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.UserPoolID, "UserPoolId") {
		return
	}
	if !serviceutil.RequireString(w, r, req.ClientID, "ClientId") {
		return
	}
	if _, ok := s.requirePool(r.Context(), w, r, req.UserPoolID); !ok {
		return
	}
	if err := s.removeClient(r.Context(), req.UserPoolID, req.ClientID); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	s.log.Info("user pool client deleted",
		zap.String("poolId", req.UserPoolID), zap.String("clientId", req.ClientID))
	s.publish(r, events.CognitoClientDeleted, events.ResourcePayload{Name: req.ClientID})
	s.writeJSON(w, r, http.StatusOK, map[string]any{})
}

// listUserPoolClients — ListUserPoolClients.
func (s *Service) listUserPoolClients(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserPoolID string `json:"UserPoolId"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.UserPoolID, "UserPoolId") {
		return
	}
	if _, ok := s.requirePool(r.Context(), w, r, req.UserPoolID); !ok {
		return
	}
	clients, err := s.scanClients(r.Context(), req.UserPoolID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	type clientDesc struct {
		ClientID   string `json:"ClientId"`
		ClientName string `json:"ClientName"`
		UserPoolId string `json:"UserPoolId"`
	}
	out := make([]clientDesc, 0, len(clients))
	for _, c := range clients {
		out = append(out, clientDesc{ClientID: c.ClientID, ClientName: c.ClientName, UserPoolId: c.UserPoolID})
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{"UserPoolClients": out})
}

// updateUserPoolClient — UpdateUserPoolClient.
func (s *Service) updateUserPoolClient(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserPoolID                      string                  `json:"UserPoolId"`
		ClientID                        string                  `json:"ClientId"`
		AccessTokenValidity             int                     `json:"AccessTokenValidity"`
		IdTokenValidity                 int                     `json:"IdTokenValidity"`
		RefreshTokenValidity            int                     `json:"RefreshTokenValidity"`
		TokenValidityUnits              *TokenValidityUnitsType `json:"TokenValidityUnits"`
		CallbackURLs                    *[]string               `json:"CallbackURLs"`
		LogoutURLs                      *[]string               `json:"LogoutURLs"`
		AllowedOAuthFlows               *[]string               `json:"AllowedOAuthFlows"`
		AllowedOAuthScopes              *[]string               `json:"AllowedOAuthScopes"`
		AllowedOAuthFlowsUserPoolClient *bool                   `json:"AllowedOAuthFlowsUserPoolClient"`
		ExplicitAuthFlows               *[]string               `json:"ExplicitAuthFlows"`
		SupportedIdentityProviders      *[]string               `json:"SupportedIdentityProviders"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.UserPoolID, "UserPoolId") {
		return
	}
	if !serviceutil.RequireString(w, r, req.ClientID, "ClientId") {
		return
	}
	pool, ok := s.requirePool(r.Context(), w, r, req.UserPoolID)
	if !ok {
		return
	}
	c, err := s.loadClient(r.Context(), req.UserPoolID, req.ClientID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if c == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Client " + req.ClientID + " does not exist.",
			HTTPStatus: 400,
		})
		return
	}

	// Apply provided values (non-zero means caller set it).
	if req.AccessTokenValidity > 0 {
		c.AccessTokenValidity = req.AccessTokenValidity
	}
	if req.IdTokenValidity > 0 {
		c.IdTokenValidity = req.IdTokenValidity
	}
	if req.RefreshTokenValidity > 0 {
		c.RefreshTokenValidity = req.RefreshTokenValidity
	}
	if req.TokenValidityUnits != nil {
		if c.TokenValidityUnits == nil {
			c.TokenValidityUnits = defaultTokenValidityUnits()
		}
		if req.TokenValidityUnits.AccessToken != "" {
			c.TokenValidityUnits.AccessToken = req.TokenValidityUnits.AccessToken
		}
		if req.TokenValidityUnits.IdToken != "" {
			c.TokenValidityUnits.IdToken = req.TokenValidityUnits.IdToken
		}
		if req.TokenValidityUnits.RefreshToken != "" {
			c.TokenValidityUnits.RefreshToken = req.TokenValidityUnits.RefreshToken
		}
	}

	// Apply OAuth fields when explicitly provided (pointer != nil means present in request).
	if req.CallbackURLs != nil {
		c.CallbackURLs = *req.CallbackURLs
	}
	if req.LogoutURLs != nil {
		c.LogoutURLs = *req.LogoutURLs
	}
	if req.AllowedOAuthFlows != nil {
		c.AllowedOAuthFlows = *req.AllowedOAuthFlows
	}
	if req.AllowedOAuthScopes != nil {
		c.AllowedOAuthScopes = *req.AllowedOAuthScopes
	}
	if req.AllowedOAuthFlowsUserPoolClient != nil {
		c.AllowedOAuthFlowsUserPoolClient = *req.AllowedOAuthFlowsUserPoolClient
	}
	if req.ExplicitAuthFlows != nil {
		if aerr := validateExplicitAuthFlows(*req.ExplicitAuthFlows); aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		if aerr := validateExplicitAuthFlowsForTier(pool, *req.ExplicitAuthFlows); aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		c.ExplicitAuthFlows = *req.ExplicitAuthFlows
	}
	if req.SupportedIdentityProviders != nil {
		c.SupportedIdentityProviders = *req.SupportedIdentityProviders
	}

	if err := s.saveClient(r.Context(), c); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	s.log.Info("user pool client updated",
		zap.String("poolId", req.UserPoolID), zap.String("clientId", req.ClientID))
	s.writeJSON(w, r, http.StatusOK, map[string]any{"UserPoolClient": toClientWire(c)})
}
