package cognito

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// ─── key construction ─────────────────────────────────────────────────────────

// region extracts the per-request region from context, falling back to the default.
func (s *Service) region(ctx context.Context) string {
	return middleware.RegionFromContext(ctx, s.cfg.Region)
}

// contextWithPoolRegion parses the region prefix from a Cognito pool ID
// ("{region}_{id}") and returns a context carrying that region. This ensures
// managed-login browser requests (which lack SigV4 headers) use the correct
// region-scoped store key. If the pool ID has no underscore the context is
// returned unchanged and the normal fallback applies.
func (s *Service) contextWithPoolRegion(ctx context.Context, poolID string) context.Context {
	if idx := strings.LastIndex(poolID, "_"); idx > 0 {
		region := poolID[:idx]
		if region != "" {
			return middleware.ContextWithRegion(ctx, region)
		}
	}
	return ctx
}

// poolRegionMiddleware is a chi middleware that extracts the region from the
// {poolId} URL parameter and injects it into the request context. This allows
// managed-login routes (plain browser GETs with no SigV4 headers) to resolve
// the correct region-scoped store keys.
func (s *Service) poolRegionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		poolID := chi.URLParam(r, "poolId")
		if poolID != "" {
			ctx := s.contextWithPoolRegion(r.Context(), poolID)
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Service) poolKey(ctx context.Context, poolID string) string {
	return serviceutil.RegionKey(s.region(ctx), poolID)
}

// userKey uses the lowercase username so lookups are case-insensitive.
func (s *Service) userKey(ctx context.Context, poolID, username string) string {
	return serviceutil.RegionKey(s.region(ctx), poolID+"/"+strings.ToLower(username))
}

func (s *Service) clientKey(ctx context.Context, poolID, clientID string) string {
	return serviceutil.RegionKey(s.region(ctx), poolID+"/"+clientID)
}

func (s *Service) tokenKey(ctx context.Context, token string) string {
	return serviceutil.RegionKey(s.region(ctx), token)
}

// ─── pool operations ──────────────────────────────────────────────────────────

func (s *Service) loadPool(ctx context.Context, poolID string) (*UserPool, error) {
	raw, found, err := s.store.Get(ctx, nsPools, s.poolKey(ctx, poolID))
	if err != nil {
		return nil, fmt.Errorf("cognito: get pool %q: %w", poolID, err)
	}
	if !found {
		return nil, nil
	}
	var p UserPool
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, nil
	}
	return &p, nil
}

func (s *Service) savePool(ctx context.Context, p *UserPool) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("cognito: marshal pool: %w", err)
	}
	return s.store.Set(ctx, nsPools, s.poolKey(ctx, p.ID), string(raw))
}

func (s *Service) scanPools(ctx context.Context) ([]*UserPool, error) {
	prefix := serviceutil.RegionKey(s.region(ctx), "")
	kvs, err := s.store.Scan(ctx, nsPools, prefix)
	if err != nil {
		return nil, fmt.Errorf("cognito: scan pools: %w", err)
	}
	pools := make([]*UserPool, 0, len(kvs))
	for _, kv := range kvs {
		var p UserPool
		if err := json.Unmarshal([]byte(kv.Value), &p); err != nil {
			continue
		}
		pools = append(pools, &p)
	}
	return pools, nil
}

// requirePool is a helper that loads a pool and writes a 400 error if it is
// missing, returning (pool, true) on success or (nil, false) on failure.
func (s *Service) requirePool(ctx context.Context, w http.ResponseWriter, r *http.Request, poolID string) (*UserPool, bool) {
	pool, err := s.loadPool(ctx, poolID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return nil, false
	}
	if pool == nil {
		protocol.WriteJSONError(w, r, errPoolNotFound(poolID))
		return nil, false
	}
	return pool, true
}

// ─── user operations ──────────────────────────────────────────────────────────

func (s *Service) loadUser(ctx context.Context, poolID, username string) (*User, error) {
	ns := nsUsersPrefix + poolID
	raw, found, err := s.store.Get(ctx, ns, s.userKey(ctx, poolID, username))
	if err != nil {
		return nil, fmt.Errorf("cognito: get user %q: %w", username, err)
	}
	if !found {
		return nil, nil
	}
	var u User
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		return nil, nil
	}
	// Backward compat: assign a UUID sub if the persisted record lacks one.
	if u.Sub == "" {
		u.Sub = uuid.NewString()
	}
	return &u, nil
}

func (s *Service) saveUser(ctx context.Context, u *User) error {
	u.ModifiedAt = s.clk.Now()
	ns := nsUsersPrefix + u.UserPoolID
	raw, err := json.Marshal(u)
	if err != nil {
		return fmt.Errorf("cognito: marshal user: %w", err)
	}
	return s.store.Set(ctx, ns, s.userKey(ctx, u.UserPoolID, u.Username), string(raw))
}

func (s *Service) removeUser(ctx context.Context, poolID, username string) error {
	ns := nsUsersPrefix + poolID
	return s.store.Delete(ctx, ns, s.userKey(ctx, poolID, username))
}

func (s *Service) scanUsers(ctx context.Context, poolID string) ([]*User, error) {
	ns := nsUsersPrefix + poolID
	prefix := serviceutil.RegionKey(s.region(ctx), poolID+"/")
	kvs, err := s.store.Scan(ctx, ns, prefix)
	if err != nil {
		return nil, fmt.Errorf("cognito: scan users %q: %w", poolID, err)
	}
	users := make([]*User, 0, len(kvs))
	for _, kv := range kvs {
		var u User
		if err := json.Unmarshal([]byte(kv.Value), &u); err != nil {
			continue
		}
		users = append(users, &u)
	}
	return users, nil
}

func filterAndPageUsers(users []*User, filter string, attributesToGet []string, limit int, paginationToken string) ([]userWire, string, *protocol.AWSError) {
	sort.Slice(users, func(i, j int) bool { return users[i].Username < users[j].Username })
	filtered, aerr := filterUsers(users, filter)
	if aerr != nil {
		return nil, "", aerr
	}
	start := 0
	if paginationToken != "" {
		offset, err := strconv.Atoi(paginationToken)
		if err != nil || offset < 0 || offset > len(filtered) {
			return nil, "", &protocol.AWSError{Code: "InvalidParameterException", Message: "Invalid PaginationToken.", HTTPStatus: 400}
		}
		start = offset
	}
	if limit <= 0 || limit > 60 {
		limit = 60
	}
	end := start + limit
	nextToken := ""
	if end < len(filtered) {
		nextToken = strconv.Itoa(end)
	} else {
		end = len(filtered)
	}
	out := make([]userWire, 0, end-start)
	for _, u := range filtered[start:end] {
		wire := toUserWire(u)
		if len(attributesToGet) > 0 {
			shaped, aerr := userWireWithAttributesToGet(wire, attributesToGet)
			if aerr != nil {
				return nil, "", aerr
			}
			wire = shaped
		}
		out = append(out, wire)
	}
	return out, nextToken, nil
}

func userWireWithAttributesToGet(wire userWire, attributesToGet []string) (userWire, *protocol.AWSError) {
	attrsByName := make(map[string]UserAttribute, len(wire.Attributes))
	for _, attr := range wire.Attributes {
		attrsByName[attr.Name] = attr
	}
	attrs := make([]UserAttribute, 0, len(attributesToGet))
	for _, name := range attributesToGet {
		attr, ok := attrsByName[name]
		if !ok || attr.Value == "" {
			return userWire{}, &protocol.AWSError{Code: "InvalidParameterException", Message: "AttributesToGet contains an attribute that isn't set for every user.", HTTPStatus: 400}
		}
		attrs = append(attrs, attr)
	}
	wire.Attributes = attrs
	return wire, nil
}

func pageGroupWires(items []groupWire, limit int, nextToken string) ([]groupWire, string, *protocol.AWSError) {
	sort.Slice(items, func(i, j int) bool { return items[i].GroupName < items[j].GroupName })
	start, end, token, aerr := pageBounds(len(items), limit, nextToken)
	if aerr != nil {
		return nil, "", aerr
	}
	return items[start:end], token, nil
}

func pageUserWires(items []userWire, limit int, nextToken string) ([]userWire, string, *protocol.AWSError) {
	sort.Slice(items, func(i, j int) bool { return items[i].Username < items[j].Username })
	start, end, token, aerr := pageBounds(len(items), limit, nextToken)
	if aerr != nil {
		return nil, "", aerr
	}
	return items[start:end], token, nil
}

func pageBounds(length int, limit int, token string) (int, int, string, *protocol.AWSError) {
	start := 0
	if token != "" {
		offset, err := strconv.Atoi(token)
		if err != nil || offset < 0 || offset > length {
			return 0, 0, "", &protocol.AWSError{Code: "InvalidParameterException", Message: "Invalid NextToken.", HTTPStatus: 400}
		}
		start = offset
	}
	if limit <= 0 || limit > 60 {
		limit = 60
	}
	end := start + limit
	next := ""
	if end < length {
		next = strconv.Itoa(end)
	} else {
		end = length
	}
	return start, end, next, nil
}

func filterUsers(users []*User, filter string) ([]*User, *protocol.AWSError) {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return users, nil
	}
	attr, op, value, ok := parseListUsersFilter(filter)
	if !ok || !listUsersFilterAttribute(attr) {
		return nil, &protocol.AWSError{Code: "InvalidParameterException", Message: "Invalid search filter.", HTTPStatus: 400}
	}
	out := make([]*User, 0, len(users))
	for _, u := range users {
		candidate := listUsersFilterValue(u, attr)
		matched := false
		switch op {
		case "=":
			if attr == "cognito:user_status" {
				matched = strings.EqualFold(candidate, value)
			} else {
				matched = candidate == value
			}
		case "^=":
			matched = strings.HasPrefix(candidate, value)
		}
		if matched {
			out = append(out, u)
		}
	}
	return out, nil
}

func parseListUsersFilter(filter string) (string, string, string, bool) {
	for _, op := range []string{"^=", "="} {
		idx := strings.Index(filter, op)
		if idx < 0 {
			continue
		}
		attr := strings.Trim(strings.TrimSpace(filter[:idx]), `"`)
		value := strings.TrimSpace(filter[idx+len(op):])
		if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
			return "", "", "", false
		}
		value = strings.ReplaceAll(value[1:len(value)-1], `\"`, `"`)
		return attr, op, value, attr != ""
	}
	return "", "", "", false
}

func listUsersFilterAttribute(attr string) bool {
	switch attr {
	case "username", "email", "phone_number", "name", "given_name", "family_name", "preferred_username", "cognito:user_status", "status", "sub":
		return true
	default:
		return false
	}
}

func listUsersFilterValue(u *User, attr string) string {
	switch attr {
	case "username":
		return u.Username
	case "cognito:user_status":
		return string(u.Status)
	case "status":
		if u.Enabled {
			return "true"
		}
		return "false"
	case "sub":
		if sub := u.getAttr("sub"); sub != "" {
			return sub
		}
		return u.Sub
	default:
		return u.getAttr(attr)
	}
}

// resolveUser looks up a user by username. If no direct username key exists, it
// scans for the user's sub, UsernameAttributes, and verified AliasAttributes.
func (s *Service) resolveUser(ctx context.Context, pool *UserPool, username string) (*User, error) {
	// 1. Direct key lookup (always tried first).
	u, err := s.loadUser(ctx, pool.ID, username)
	if err != nil {
		return nil, err
	}
	if u != nil {
		return u, nil
	}

	users, err := s.scanUsers(ctx, pool.ID)
	if err != nil {
		return nil, err
	}
	lowerInput := strings.ToLower(username)
	for _, candidate := range users {
		if strings.EqualFold(candidate.Sub, username) || strings.EqualFold(candidate.getAttr("sub"), username) {
			return candidate, nil
		}
		for _, attr := range pool.UsernameAttributes {
			if strings.ToLower(candidate.getAttr(attr)) == lowerInput {
				return candidate, nil
			}
		}
		for _, attr := range pool.AliasAttributes {
			if strings.ToLower(candidate.getAttr(attr)) == lowerInput && aliasVerified(candidate, attr) {
				return candidate, nil
			}
		}
	}
	return nil, nil
}

func aliasVerified(u *User, attr string) bool {
	switch attr {
	case "email":
		return strings.EqualFold(u.getAttr("email_verified"), "true")
	case "phone_number":
		return strings.EqualFold(u.getAttr("phone_number_verified"), "true")
	case "preferred_username":
		return true
	default:
		return false
	}
}

func verifiedAliasValue(attrs []UserAttribute, attr string) (string, bool) {
	value := ""
	verified := false
	for _, a := range attrs {
		switch a.Name {
		case attr:
			value = a.Value
		case attr + "_verified":
			verified = strings.EqualFold(a.Value, "true")
		}
	}
	if attr == "preferred_username" {
		verified = true
	}
	return value, value != "" && verified
}

func attributesAfterConfirmation(u *User) []UserAttribute {
	attrs := append([]UserAttribute(nil), u.Attributes...)
	if u.email() != "" {
		setAttrIfMissing(&attrs, "email_verified", "true")
		for i := range attrs {
			if attrs[i].Name == "email_verified" {
				attrs[i].Value = "true"
			}
		}
	}
	if u.phoneNumber() != "" {
		setAttrIfMissing(&attrs, "phone_number_verified", "true")
		for i := range attrs {
			if attrs[i].Name == "phone_number_verified" {
				attrs[i].Value = "true"
			}
		}
	}
	return attrs
}

func attributesAfterUpdate(u *User, updates []UserAttribute) []UserAttribute {
	attrs := append([]UserAttribute(nil), u.Attributes...)
	for _, update := range updates {
		found := false
		for i := range attrs {
			if attrs[i].Name == update.Name {
				attrs[i].Value = update.Value
				found = true
				break
			}
		}
		if !found {
			attrs = append(attrs, update)
		}
	}
	return attrs
}

func (s *Service) findVerifiedAliasOwner(ctx context.Context, pool *UserPool, attrs []UserAttribute) (*User, string, error) {
	if len(pool.AliasAttributes) == 0 {
		return nil, "", nil
	}
	users, err := s.scanUsers(ctx, pool.ID)
	if err != nil {
		return nil, "", err
	}
	for _, attr := range pool.AliasAttributes {
		value, ok := verifiedAliasValue(attrs, attr)
		if !ok {
			continue
		}
		lowerValue := strings.ToLower(value)
		for _, u := range users {
			if strings.ToLower(u.getAttr(attr)) == lowerValue && aliasVerified(u, attr) {
				return u, attr, nil
			}
		}
	}
	return nil, "", nil
}

func (s *Service) migrateVerifiedAlias(ctx context.Context, owner *User, attr string) error {
	switch attr {
	case "email":
		owner.setAttr("email_verified", "false")
	case "phone_number":
		owner.setAttr("phone_number_verified", "false")
	case "preferred_username":
		owner.setAttr("preferred_username", "")
	}
	return s.saveUser(ctx, owner)
}

func (s *Service) resolveUserInPool(ctx context.Context, poolID, username string) (*User, error) {
	pool, err := s.loadPool(ctx, poolID)
	if err != nil {
		return nil, err
	}
	if pool != nil {
		return s.resolveUser(ctx, pool, username)
	}
	return s.loadUser(ctx, poolID, username)
}

// requireUser loads a user and writes a 400 error if missing.
func (s *Service) requireUser(ctx context.Context, w http.ResponseWriter, r *http.Request, poolID, username string) (*User, bool) {
	u, err := s.resolveUserInPool(ctx, poolID, username)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return nil, false
	}
	if u == nil {
		protocol.WriteJSONError(w, r, errUserNotFound(username))
		return nil, false
	}
	return u, true
}

// ─── client operations ────────────────────────────────────────────────────────

// Clients are stored in two places:
//   - per-pool namespace (nsClientsPrefix+poolID) for ListUserPoolClients
//   - global lookup namespace (nsClientLookup) keyed by clientId, for reverse lookup

func (s *Service) loadClientByID(ctx context.Context, clientID string) (*UserPoolClient, error) {
	raw, found, err := s.store.Get(ctx, nsClientLookup, s.tokenKey(ctx, clientID))
	if err != nil {
		return nil, fmt.Errorf("cognito: get client lookup %q: %w", clientID, err)
	}
	if !found {
		return nil, nil
	}
	var c UserPoolClient
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, nil
	}
	return &c, nil
}

func (s *Service) loadClient(ctx context.Context, poolID, clientID string) (*UserPoolClient, error) {
	ns := nsClientsPrefix + poolID
	raw, found, err := s.store.Get(ctx, ns, s.clientKey(ctx, poolID, clientID))
	if err != nil {
		return nil, fmt.Errorf("cognito: get client %q: %w", clientID, err)
	}
	if !found {
		return nil, nil
	}
	var c UserPoolClient
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, nil
	}
	return &c, nil
}

func (s *Service) saveClient(ctx context.Context, c *UserPoolClient) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("cognito: marshal client: %w", err)
	}
	rawStr := string(raw)
	// Per-pool namespace for listing.
	ns := nsClientsPrefix + c.UserPoolID
	if err := s.store.Set(ctx, ns, s.clientKey(ctx, c.UserPoolID, c.ClientID), rawStr); err != nil {
		return err
	}
	// Global lookup namespace for clientId → UserPoolClient reverse lookup.
	return s.store.Set(ctx, nsClientLookup, s.tokenKey(ctx, c.ClientID), rawStr)
}

func (s *Service) removeClient(ctx context.Context, poolID, clientID string) error {
	ns := nsClientsPrefix + poolID
	if err := s.store.Delete(ctx, ns, s.clientKey(ctx, poolID, clientID)); err != nil {
		return err
	}
	return s.store.Delete(ctx, nsClientLookup, s.tokenKey(ctx, clientID))
}

func (s *Service) scanClients(ctx context.Context, poolID string) ([]*UserPoolClient, error) {
	ns := nsClientsPrefix + poolID
	prefix := serviceutil.RegionKey(s.region(ctx), poolID+"/")
	kvs, err := s.store.Scan(ctx, ns, prefix)
	if err != nil {
		return nil, fmt.Errorf("cognito: scan clients %q: %w", poolID, err)
	}
	clients := make([]*UserPoolClient, 0, len(kvs))
	for _, kv := range kvs {
		var c UserPoolClient
		if err := json.Unmarshal([]byte(kv.Value), &c); err != nil {
			continue
		}
		clients = append(clients, &c)
	}
	return clients, nil
}

// requireClientByID looks up a client by its GlobalID and writes a 400 if missing.
func (s *Service) requireClientByID(ctx context.Context, w http.ResponseWriter, r *http.Request, clientID string) (*UserPoolClient, bool) {
	c, err := s.loadClientByID(ctx, clientID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return nil, false
	}
	if c == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("UserPool client %s does not exist.", clientID),
			HTTPStatus: 400,
		})
		return nil, false
	}
	return c, true
}

// ─── token operations ─────────────────────────────────────────────────────────

func (s *Service) loadToken(ctx context.Context, tokenValue string) (*Token, error) {
	raw, found, err := s.store.Get(ctx, nsTokens, s.tokenKey(ctx, tokenValue))
	if err != nil {
		return nil, fmt.Errorf("cognito: get token: %w", err)
	}
	if !found {
		return nil, nil
	}
	var t Token
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		return nil, fmt.Errorf("cognito: unmarshal token: %w", err)
	}
	return &t, nil
}

func (s *Service) saveToken(ctx context.Context, t *Token) error {
	raw, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("cognito: marshal token: %w", err)
	}
	return s.store.Set(ctx, nsTokens, s.tokenKey(ctx, t.Value), string(raw))
}

func (s *Service) removeToken(ctx context.Context, tokenValue string) error {
	return s.store.Delete(ctx, nsTokens, s.tokenKey(ctx, tokenValue))
}

// issueTokens creates RS256-signed access and ID JWTs plus an opaque refresh
// token for the given user. The issuer URL is embedded in JWT claims so that
// jwt-validation libraries can locate the JWKS endpoint.
func (s *Service) issueTokens(ctx context.Context, u *User, client *UserPoolClient, issuer, originJTI, nonce string) (*authResultWire, error) {
	now := s.clk.Now()

	priv, kid, err := s.getOrCreateSigningKey(ctx, u.UserPoolID)
	if err != nil {
		return nil, err
	}

	// Compute token lifetimes from client configuration, falling back to AWS defaults.
	accessTTL := time.Hour
	idTTL := time.Hour
	refreshTTL := 30 * 24 * time.Hour
	if client.AccessTokenValidity > 0 {
		unit := "hours"
		if client.TokenValidityUnits != nil && client.TokenValidityUnits.AccessToken != "" {
			unit = client.TokenValidityUnits.AccessToken
		}
		accessTTL = tokenDuration(client.AccessTokenValidity, unit)
	}
	if client.IdTokenValidity > 0 {
		unit := "hours"
		if client.TokenValidityUnits != nil && client.TokenValidityUnits.IdToken != "" {
			unit = client.TokenValidityUnits.IdToken
		}
		idTTL = tokenDuration(client.IdTokenValidity, unit)
	}
	if client.RefreshTokenValidity > 0 {
		unit := "days"
		if client.TokenValidityUnits != nil && client.TokenValidityUnits.RefreshToken != "" {
			unit = client.TokenValidityUnits.RefreshToken
		}
		refreshTTL = tokenDuration(client.RefreshTokenValidity, unit)
	}

	clientID := client.ClientID
	authTime := now.Unix()
	accessExp := now.Add(accessTTL).Unix()
	idExp := now.Add(idTTL).Unix()

	// Shared event ID across access + ID tokens for the same auth event.
	eventID := uuid.NewString()

	// ── Access token ──────────────────────────────────────────────────────────
	accessJTI := uuid.NewString()
	if originJTI == "" {
		originJTI = accessJTI
	}
	accessClaims := map[string]any{
		"sub":        u.Sub,
		"iss":        issuer,
		"client_id":  clientID,
		"token_use":  "access",
		"scope":      "aws.cognito.signin.user.admin",
		"auth_time":  authTime,
		"exp":        accessExp,
		"iat":        authTime,
		"jti":        accessJTI,
		"origin_jti": originJTI,
		"event_id":   eventID,
		"username":   u.Username,
		"version":    2,
	}
	if len(u.Groups) > 0 {
		accessClaims["cognito:groups"] = u.Groups
	}
	accessJWT, err := signJWT(priv, kid, accessClaims)
	if err != nil {
		return nil, err
	}
	if err := s.saveToken(ctx, &Token{
		Value: accessJTI, Type: "access",
		Username: u.Username, UserPoolID: u.UserPoolID,
		CreatedAt: now, ExpiresAt: now.Add(accessTTL),
	}); err != nil {
		return nil, err
	}

	// ── ID token ──────────────────────────────────────────────────────────────
	idJTI := uuid.NewString()
	idClaims := map[string]any{
		"sub":              u.Sub,
		"iss":              issuer,
		"aud":              clientID,
		"token_use":        "id",
		"auth_time":        authTime,
		"exp":              idExp,
		"iat":              authTime,
		"jti":              idJTI,
		"event_id":         eventID,
		"cognito:username": u.Username,
	}
	// Flatten user attributes into ID token claims.
	for _, a := range u.Attributes {
		// Coerce "email_verified" / "phone_number_verified" to boolean.
		if (a.Name == "email_verified" || a.Name == "phone_number_verified") && a.Value == "true" {
			idClaims[a.Name] = true
		} else if (a.Name == "email_verified" || a.Name == "phone_number_verified") && a.Value == "false" {
			idClaims[a.Name] = false
		} else {
			idClaims[a.Name] = a.Value
		}
	}
	if len(u.Groups) > 0 {
		idClaims["cognito:groups"] = u.Groups
	}
	if nonce != "" {
		idClaims["nonce"] = nonce
	}
	idJWT, err := signJWT(priv, kid, idClaims)
	if err != nil {
		return nil, err
	}

	// ── Refresh token (opaque) ────────────────────────────────────────────────
	refreshValue := generateToken()
	if err := s.saveToken(ctx, &Token{
		Value: refreshValue, Type: "refresh",
		Username: u.Username, UserPoolID: u.UserPoolID,
		CreatedAt: now, ExpiresAt: now.Add(refreshTTL),
		OriginJTI: originJTI,
	}); err != nil {
		return nil, err
	}

	// Store ID token JTI for completeness (not validated server-side, but keeps store symmetric).
	_ = s.saveToken(ctx, &Token{
		Value: idJTI, Type: "id",
		Username: u.Username, UserPoolID: u.UserPoolID,
		CreatedAt: now, ExpiresAt: now.Add(idTTL),
	})

	return &authResultWire{
		AccessToken:  accessJWT,
		IdToken:      idJWT,
		RefreshToken: refreshValue,
		TokenType:    "Bearer",
		ExpiresIn:    int(accessTTL.Seconds()),
	}, nil
}

// issueOpaqueToken creates and persists a short-lived opaque token for auth
// challenges. tokenType should be "session" (NEW_PASSWORD_REQUIRED) or "mfa".
func (s *Service) issueOpaqueToken(ctx context.Context, poolID, username, tokenType string, ttl time.Duration) (string, error) {
	now := s.clk.Now()
	t := &Token{
		Value:      generateToken(),
		Type:       tokenType,
		Username:   username,
		UserPoolID: poolID,
		CreatedAt:  now,
		ExpiresAt:  now.Add(ttl),
	}
	if err := s.saveToken(ctx, t); err != nil {
		return "", err
	}
	return t.Value, nil
}

// issueSession creates a short-lived session token for NEW_PASSWORD_REQUIRED challenges.
func (s *Service) issueSession(ctx context.Context, poolID, username string) (string, error) {
	return s.issueOpaqueToken(ctx, poolID, username, "session", 5*time.Minute)
}

// validateAccessToken parses a JWT access token, verifies its RS256 signature
// against the pool's signing key, and checks for revocation + expiry.
func (s *Service) validateAccessToken(ctx context.Context, w http.ResponseWriter, r *http.Request, tokenStr string) (*Token, bool) {
	// 1. Parse claims (no sig check yet — need pool ID to load the key).
	claims, err := parseJWTClaims(tokenStr)
	if err != nil {
		protocol.WriteJSONError(w, r, errNotAuthorized("Invalid access token."))
		return nil, false
	}

	// 2. Extract pool ID from the issuer claim.
	iss, _ := claims["iss"].(string)
	poolID, err := poolIDFromIssuer(iss)
	if err != nil {
		protocol.WriteJSONError(w, r, errNotAuthorized("Invalid access token issuer."))
		return nil, false
	}

	// 3. Load the signing key and verify signature.
	priv, _, err := s.getOrCreateSigningKey(ctx, poolID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return nil, false
	}
	if err := verifyJWTSignature(tokenStr, &priv.PublicKey); err != nil {
		protocol.WriteJSONError(w, r, errNotAuthorized("Invalid access token signature."))
		return nil, false
	}

	// 4. Validate token_use == "access".
	if tu, _ := claims["token_use"].(string); tu != "access" {
		protocol.WriteJSONError(w, r, errNotAuthorized("Token is not an access token."))
		return nil, false
	}

	// 5. Check expiry.
	exp, _ := claims["exp"].(float64)
	if s.clk.Now().Unix() > int64(exp) {
		protocol.WriteJSONError(w, r, errNotAuthorized("Access token has expired."))
		return nil, false
	}

	// 6. Check JTI revocation in the store.
	jti, _ := claims["jti"].(string)
	storedTok, err := s.loadToken(ctx, jti)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return nil, false
	}
	if storedTok == nil {
		protocol.WriteJSONError(w, r, errNotAuthorized("Access token has been revoked."))
		return nil, false
	}

	// 7. Check GlobalSignOut: reject tokens issued before the sign-out timestamp.
	username, _ := claims["username"].(string)
	u, err := s.loadUser(ctx, poolID, username)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return nil, false
	}
	if u == nil {
		protocol.WriteJSONError(w, r, errNotAuthorized("User does not exist."))
		return nil, false
	}
	if u.GlobalSignOutAt != nil {
		iat, _ := claims["iat"].(float64)
		if int64(iat) < u.GlobalSignOutAt.Unix() {
			protocol.WriteJSONError(w, r, errNotAuthorized("Token revoked by global sign-out."))
			return nil, false
		}
	}

	return &Token{
		Value:      jti,
		Type:       "access",
		Username:   username,
		UserPoolID: poolID,
		ExpiresAt:  time.Unix(int64(exp), 0),
	}, true
}

// ─── error constructors ───────────────────────────────────────────────────────

func errPoolNotFound(poolID string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ResourceNotFoundException",
		Message:    fmt.Sprintf("User pool %s does not exist.", poolID),
		HTTPStatus: 400,
	}
}

func errUserNotFound(username string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "UserNotFoundException",
		Message:    fmt.Sprintf("User %s does not exist.", username),
		HTTPStatus: 400,
	}
}

func errUsernameExists(username string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "UsernameExistsException",
		Message:    fmt.Sprintf("User already exists: %s", username),
		HTTPStatus: 400,
	}
}

func errAliasExists() *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "AliasExistsException",
		Message:    "An account with the given email already exists.",
		HTTPStatus: 400,
	}
}

func errNotAuthorized(msg string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "NotAuthorizedException",
		Message:    msg,
		HTTPStatus: 400,
	}
}

func errCodeMismatch() *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "CodeMismatchException",
		Message:    "Invalid verification code provided, please try again.",
		HTTPStatus: 400,
	}
}

func errExpiredCode() *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ExpiredCodeException",
		Message:    "Invalid code provided, please request a code again.",
		HTTPStatus: 400,
	}
}

// ─── random helpers ───────────────────────────────────────────────────────────

// generateToken returns a cryptographically random 64-char hex string.
func generateToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// generateCode returns a random 6-digit decimal verification code.
func generateCode() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	n := (int(b[0])<<16 | int(b[1])<<8 | int(b[2])) % 1_000_000
	return fmt.Sprintf("%06d", n)
}

// generatePoolSuffix returns a random 8-char uppercase hex suffix for pool IDs.
func generatePoolSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return strings.ToUpper(hex.EncodeToString(b))
}

// generateClientID returns a random 26-char hex app-client ID.
func generateClientID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:26]
}

// generateTempPassword returns a random temporary password that conforms to the
// pool password policy. Per AWS AdminCreateUser docs, generated temporary
// passwords must satisfy the user pool's password policy.
func generateTempPassword(pool *UserPool) string {
	const (
		upper   = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
		lower   = "abcdefghijklmnopqrstuvwxyz"
		digits  = "0123456789"
		symbols = "^$*.[]{}()?\"!@#%&/\\,><':;|_~`=+-"
	)
	all := upper + lower + digits + symbols

	length := 23
	if min := effectivePasswordPolicy(pool).MinimumLength; min > length {
		length = min
	}

	password := []byte{
		randomChar(upper),
		randomChar(lower),
		randomChar(digits),
		randomChar(symbols),
	}
	for len(password) < length {
		password = append(password, randomChar(all))
	}
	for i := len(password) - 1; i > 0; i-- {
		j := randomIndex(i + 1)
		password[i], password[j] = password[j], password[i]
	}
	return string(password)
}

func randomChar(chars string) byte {
	return chars[randomIndex(len(chars))]
}

func randomIndex(n int) int {
	if n <= 1 {
		return 0
	}
	b := []byte{0}
	_, _ = rand.Read(b)
	return int(b[0]) % n
}

// ─── group operations ─────────────────────────────────────────────────────────

const nsGroupsPrefix = "cognito:groups:"

func (s *Service) groupKey(ctx context.Context, poolID, groupName string) string {
	return serviceutil.RegionKey(s.region(ctx), poolID+"/"+groupName)
}

func (s *Service) loadGroup(ctx context.Context, poolID, groupName string) (*Group, error) {
	ns := nsGroupsPrefix + poolID
	raw, found, err := s.store.Get(ctx, ns, s.groupKey(ctx, poolID, groupName))
	if err != nil {
		return nil, fmt.Errorf("cognito: get group %q: %w", groupName, err)
	}
	if !found {
		return nil, nil
	}
	var g Group
	if err := json.Unmarshal([]byte(raw), &g); err != nil {
		return nil, fmt.Errorf("cognito: unmarshal group %q: %w", groupName, err)
	}
	return &g, nil
}

func (s *Service) saveGroup(ctx context.Context, g *Group) error {
	ns := nsGroupsPrefix + g.UserPoolID
	raw, err := json.Marshal(g)
	if err != nil {
		return fmt.Errorf("cognito: marshal group: %w", err)
	}
	return s.store.Set(ctx, ns, s.groupKey(ctx, g.UserPoolID, g.GroupName), string(raw))
}

func (s *Service) removeGroup(ctx context.Context, poolID, groupName string) error {
	ns := nsGroupsPrefix + poolID
	return s.store.Delete(ctx, ns, s.groupKey(ctx, poolID, groupName))
}

func (s *Service) scanGroups(ctx context.Context, poolID string) ([]*Group, error) {
	ns := nsGroupsPrefix + poolID
	prefix := serviceutil.RegionKey(s.region(ctx), poolID+"/")
	kvs, err := s.store.Scan(ctx, ns, prefix)
	if err != nil {
		return nil, fmt.Errorf("cognito: scan groups %q: %w", poolID, err)
	}
	groups := make([]*Group, 0, len(kvs))
	for _, kv := range kvs {
		var g Group
		if err := json.Unmarshal([]byte(kv.Value), &g); err != nil {
			continue
		}
		groups = append(groups, &g)
	}
	return groups, nil
}

// requireGroup loads a group and writes a 400 error if it is missing.
func (s *Service) requireGroup(ctx context.Context, w http.ResponseWriter, r *http.Request, poolID, groupName string) (*Group, bool) {
	g, err := s.loadGroup(ctx, poolID, groupName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return nil, false
	}
	if g == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Group %s does not exist.", groupName),
			HTTPStatus: 400,
		})
		return nil, false
	}
	return g, true
}

// ─── domain operations ────────────────────────────────────────────────────────

const nsDomains = "cognito:domains"

func (s *Service) domainKey(ctx context.Context, domain string) string {
	return serviceutil.RegionKey(s.region(ctx), domain)
}

func (s *Service) loadDomain(ctx context.Context, domain string) (*UserPoolDomain, error) {
	raw, found, err := s.store.Get(ctx, nsDomains, s.domainKey(ctx, domain))
	if err != nil {
		return nil, fmt.Errorf("cognito: get domain %q: %w", domain, err)
	}
	if !found {
		return nil, nil
	}
	var d UserPoolDomain
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return nil, fmt.Errorf("cognito: unmarshal domain %q: %w", domain, err)
	}
	return &d, nil
}

func (s *Service) saveDomain(ctx context.Context, d *UserPoolDomain) error {
	raw, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("cognito: marshal domain: %w", err)
	}
	return s.store.Set(ctx, nsDomains, s.domainKey(ctx, d.Domain), string(raw))
}

func (s *Service) removeDomain(ctx context.Context, domain string) error {
	return s.store.Delete(ctx, nsDomains, s.domainKey(ctx, domain))
}

// loadDomainForPool scans all domains and returns the one associated with the given pool.
func (s *Service) loadDomainForPool(ctx context.Context, poolID string) (*UserPoolDomain, error) {
	prefix := serviceutil.RegionKey(s.region(ctx), "")
	kvs, err := s.store.Scan(ctx, nsDomains, prefix)
	if err != nil {
		return nil, fmt.Errorf("cognito: scan domains: %w", err)
	}
	for _, kv := range kvs {
		var d UserPoolDomain
		if err := json.Unmarshal([]byte(kv.Value), &d); err != nil {
			continue
		}
		if d.UserPoolID == poolID {
			return &d, nil
		}
	}
	return nil, nil
}

// ─── auth code operations ─────────────────────────────────────────────────────

const nsAuthCodes = "cognito:authcodes"

func (s *Service) authCodeKey(ctx context.Context, code string) string {
	return serviceutil.RegionKey(s.region(ctx), code)
}

func (s *Service) loadAuthCode(ctx context.Context, code string) (*AuthCode, error) {
	raw, found, err := s.store.Get(ctx, nsAuthCodes, s.authCodeKey(ctx, code))
	if err != nil {
		return nil, fmt.Errorf("cognito: get auth code: %w", err)
	}
	if !found {
		return nil, nil
	}
	var ac AuthCode
	if err := json.Unmarshal([]byte(raw), &ac); err != nil {
		return nil, fmt.Errorf("cognito: unmarshal auth code: %w", err)
	}
	return &ac, nil
}

func (s *Service) saveAuthCode(ctx context.Context, ac *AuthCode) error {
	raw, err := json.Marshal(ac)
	if err != nil {
		return fmt.Errorf("cognito: marshal auth code: %w", err)
	}
	return s.store.Set(ctx, nsAuthCodes, s.authCodeKey(ctx, ac.Code), string(raw))
}

func (s *Service) removeAuthCode(ctx context.Context, code string) error {
	return s.store.Delete(ctx, nsAuthCodes, s.authCodeKey(ctx, code))
}

// ─── login session operations ─────────────────────────────────────────────────

const nsLoginSessions = "cognito:loginsessions"

func (s *Service) loginSessionKey(ctx context.Context, sessionID string) string {
	return serviceutil.RegionKey(s.region(ctx), sessionID)
}

func (s *Service) loadLoginSession(ctx context.Context, sessionID string) (*LoginSession, error) {
	raw, found, err := s.store.Get(ctx, nsLoginSessions, s.loginSessionKey(ctx, sessionID))
	if err != nil {
		return nil, fmt.Errorf("cognito: get login session: %w", err)
	}
	if !found {
		return nil, nil
	}
	var ls LoginSession
	if err := json.Unmarshal([]byte(raw), &ls); err != nil {
		return nil, fmt.Errorf("cognito: unmarshal login session: %w", err)
	}
	return &ls, nil
}

func (s *Service) saveLoginSession(ctx context.Context, ls *LoginSession) error {
	raw, err := json.Marshal(ls)
	if err != nil {
		return fmt.Errorf("cognito: marshal login session: %w", err)
	}
	return s.store.Set(ctx, nsLoginSessions, s.loginSessionKey(ctx, ls.SessionID), string(raw))
}

func (s *Service) removeLoginSession(ctx context.Context, sessionID string) error {
	return s.store.Delete(ctx, nsLoginSessions, s.loginSessionKey(ctx, sessionID))
}
