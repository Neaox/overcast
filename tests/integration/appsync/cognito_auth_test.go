package appsync_test

// cognito_auth_test.go — AMAZON_COGNITO_USER_POOLS authorization for the
// AppSync GraphQL endpoint, in both enforcement postures.
//
// Relaxed (the default, and what every other AppSync test relies on) accepts
// any bearer token and decodes its claims into $ctx.identity. Strict
// (OVERCAST_ENFORCE_APPSYNC_COGNITO_AUTH=true) verifies the token against the
// local Cognito user pool the API is configured with: RS256 signature,
// issuer, token_use, expiry, and the app client regex when one is set.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// ─── Cognito helpers ─────────────────────────────────────────────────────────

// cognitoCall issues one Cognito JSON 1.1 API call against the test server.
func cognitoCall(t *testing.T, srv *helpers.TestServer, operation string, body map[string]any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s body: %v", operation, err)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "AWSCognitoIdentityProviderService."+operation)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("cognitoCall %s: %v", operation, err)
	}
	return resp
}

// cognitoPool is one local user pool with a signed-in user, and the tokens
// Overcast's own Cognito minted for them.
type cognitoPool struct {
	poolID      string
	clientID    string
	username    string
	idToken     string
	accessToken string
}

// newCognitoPool creates a pool, an app client, a confirmed user in the
// "admins" group, and signs that user in — so the returned tokens are minted
// and signed by Overcast's Cognito exactly as a real client would obtain them.
func newCognitoPool(t *testing.T, srv *helpers.TestServer, poolName string) cognitoPool {
	t.Helper()
	return newCognitoPoolWithClient(t, srv, poolName, nil)
}

// newCognitoPoolWithClient is newCognitoPool with extra CreateUserPoolClient
// parameters merged in — token validity, for the expiry test.
func newCognitoPoolWithClient(t *testing.T, srv *helpers.TestServer, poolName string, clientExtra map[string]any) cognitoPool {
	t.Helper()

	poolResp := cognitoCall(t, srv, "CreateUserPool", map[string]any{"PoolName": poolName})
	defer poolResp.Body.Close()
	helpers.AssertStatus(t, poolResp, http.StatusOK)
	var pool struct {
		UserPool struct {
			Id string `json:"Id"`
		} `json:"UserPool"`
	}
	helpers.DecodeJSON(t, poolResp, &pool)
	if pool.UserPool.Id == "" {
		t.Fatal("CreateUserPool returned an empty pool ID")
	}

	clientReq := map[string]any{
		"UserPoolId": pool.UserPool.Id,
		"ClientName": poolName + "-client",
	}
	for k, v := range clientExtra {
		clientReq[k] = v
	}
	clientResp := cognitoCall(t, srv, "CreateUserPoolClient", clientReq)
	defer clientResp.Body.Close()
	helpers.AssertStatus(t, clientResp, http.StatusOK)
	var client struct {
		UserPoolClient struct {
			ClientId string `json:"ClientId"`
		} `json:"UserPoolClient"`
	}
	helpers.DecodeJSON(t, clientResp, &client)
	if client.UserPoolClient.ClientId == "" {
		t.Fatal("CreateUserPoolClient returned an empty client ID")
	}

	const username = "alice"
	createResp := cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId":    pool.UserPool.Id,
		"Username":      username,
		"MessageAction": "SUPPRESS",
		"UserAttributes": []map[string]string{
			{"Name": "email", "Value": "alice@example.com"},
			{"Name": "email_verified", "Value": "true"},
		},
	})
	createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)

	pwResp := cognitoCall(t, srv, "AdminSetUserPassword", map[string]any{
		"UserPoolId": pool.UserPool.Id,
		"Username":   username,
		"Password":   "AlicePass1!",
		"Permanent":  true,
	})
	pwResp.Body.Close()
	helpers.AssertStatus(t, pwResp, http.StatusOK)

	groupResp := cognitoCall(t, srv, "CreateGroup", map[string]any{
		"UserPoolId": pool.UserPool.Id,
		"GroupName":  "admins",
	})
	groupResp.Body.Close()
	helpers.AssertStatus(t, groupResp, http.StatusOK)

	addResp := cognitoCall(t, srv, "AdminAddUserToGroup", map[string]any{
		"UserPoolId": pool.UserPool.Id,
		"Username":   username,
		"GroupName":  "admins",
	})
	addResp.Body.Close()
	helpers.AssertStatus(t, addResp, http.StatusOK)

	authResp := cognitoCall(t, srv, "AdminInitiateAuth", map[string]any{
		"UserPoolId": pool.UserPool.Id,
		"ClientId":   client.UserPoolClient.ClientId,
		"AuthFlow":   "ADMIN_USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME": username,
			"PASSWORD": "AlicePass1!",
		},
	})
	defer authResp.Body.Close()
	helpers.AssertStatus(t, authResp, http.StatusOK)
	var auth struct {
		AuthenticationResult struct {
			IdToken     string `json:"IdToken"`
			AccessToken string `json:"AccessToken"`
		} `json:"AuthenticationResult"`
	}
	helpers.DecodeJSON(t, authResp, &auth)
	if auth.AuthenticationResult.IdToken == "" || auth.AuthenticationResult.AccessToken == "" {
		t.Fatal("AdminInitiateAuth returned no tokens")
	}

	return cognitoPool{
		poolID:      pool.UserPool.Id,
		clientID:    client.UserPoolClient.ClientId,
		username:    username,
		idToken:     auth.AuthenticationResult.IdToken,
		accessToken: auth.AuthenticationResult.AccessToken,
	}
}

// ─── AppSync helpers ─────────────────────────────────────────────────────────

// identityEchoSDL exposes the documented AMAZON_COGNITO_USER_POOLS identity
// fields as a GraphQL type so a resolver can be asserted against them.
const identityEchoSDL = `
type Identity {
	sub: String
	username: String
	issuer: String
	tokenUse: String
	groups: [String]
}
type Query { whoami: Identity }
`

// identityEchoTemplate copies $ctx.identity into the resolver payload.
const identityEchoTemplate = `{"version":"2018-05-29","payload":{` +
	`"sub":$util.toJson($ctx.identity.sub),` +
	`"username":$util.toJson($ctx.identity.username),` +
	`"issuer":$util.toJson($ctx.identity.issuer),` +
	`"tokenUse":$util.toJson($ctx.identity.claims.get("token_use")),` +
	`"groups":$util.toJson($ctx.identity.claims.get("cognito:groups"))}}`

// newCognitoAPI creates an AppSync API whose given authentication config is
// applied verbatim, uploads identityEchoSDL and wires the echo resolver.
func newCognitoAPI(t *testing.T, srv *helpers.TestServer, authConfig map[string]any) string {
	t.Helper()
	apiID, _ := createTestAPI(t, srv)

	update := map[string]any{"name": "cognito-strict-api"}
	for k, v := range authConfig {
		update[k] = v
	}
	appsyncPost(t, srv, "/v1/apis/"+apiID, update).Body.Close()

	b64SDL := base64.StdEncoding.EncodeToString([]byte(identityEchoSDL))
	appsyncPost(t, srv, "/v1/apis/"+apiID+"/schemacreation", map[string]any{"definition": b64SDL}).Body.Close()
	appsyncPost(t, srv, "/v1/apis/"+apiID+"/datasources", map[string]any{
		"name": "NoneDS", "type": "NONE",
	}).Body.Close()
	appsyncPost(t, srv, "/v1/apis/"+apiID+"/types/Query/resolvers", map[string]any{
		"fieldName":              "whoami",
		"dataSourceName":         "NoneDS",
		"requestMappingTemplate": identityEchoTemplate,
	}).Body.Close()

	return apiID
}

// whoamiIdentity is the decoded resolver view of $ctx.identity.
type whoamiIdentity struct {
	Sub      string   `json:"sub"`
	Username string   `json:"username"`
	Issuer   string   `json:"issuer"`
	TokenUse string   `json:"tokenUse"`
	Groups   []string `json:"groups"`
}

// queryWhoami runs the whoami query with the given Authorization header value
// ("" sends no header at all) and returns the raw response.
func queryWhoami(t *testing.T, srv *helpers.TestServer, apiID, authorization string) *http.Response {
	t.Helper()
	headers := map[string]string{}
	if authorization != "" {
		headers["Authorization"] = authorization
	}
	return appsyncPostWithHeaders(t, srv, "/_overcast/appsync/apis/"+apiID+"/graphql",
		map[string]any{"query": `{ whoami { sub username issuer tokenUse groups } }`}, headers)
}

// assertWhoamiUnauthorized asserts AppSync's UnauthorizedException shape:
// HTTP 401 with the AWS JSON error envelope naming the exception.
func assertWhoamiUnauthorized(t *testing.T, resp *http.Response) {
	t.Helper()
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d\nbody: %s", resp.StatusCode, raw)
	}
	var body struct {
		Type    string `json:"__type"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode error body %s: %v", raw, err)
	}
	if body.Type != "UnauthorizedException" {
		t.Errorf("__type = %q, want UnauthorizedException (message %q)", body.Type, body.Message)
	}
}

// assertWhoamiOK asserts the query succeeded and returns the resolver identity.
func assertWhoamiOK(t *testing.T, resp *http.Response) whoamiIdentity {
	t.Helper()
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Data struct {
			Whoami whoamiIdentity `json:"whoami"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Errors) > 0 {
		t.Fatalf("unexpected GraphQL errors: %v", result.Errors)
	}
	return result.Data.Whoami
}

// ─── Strict mode ─────────────────────────────────────────────────────────────

func TestAuthCognitoStrict_acceptsTokenMintedByLocalCognito(t *testing.T) {
	// Given: strict enforcement, and an API bound to a local Cognito pool
	srv := helpers.NewTestServer(t, helpers.WithEnforceAppSyncCognitoAuth(true))
	pool := newCognitoPool(t, srv, "strict-pool")
	apiID := newCognitoAPI(t, srv, map[string]any{
		"authenticationType": "AMAZON_COGNITO_USER_POOLS",
		"userPoolConfig": map[string]any{
			"userPoolId": pool.poolID, "awsRegion": srv.Config.Region, "defaultAction": "ALLOW",
		},
	})

	// When: the ID token that pool minted is presented
	identity := assertWhoamiOK(t, queryWhoami(t, srv, apiID, "Bearer "+pool.idToken))

	// Then: the resolver identity carries the documented Cognito fields
	if identity.Sub == "" {
		t.Error("expected identity.sub to be populated")
	}
	if identity.Username != pool.username {
		t.Errorf("identity.username = %q, want %q", identity.Username, pool.username)
	}
	if !strings.HasSuffix(identity.Issuer, "/"+pool.poolID) {
		t.Errorf("identity.issuer = %q, want it to end in /%s", identity.Issuer, pool.poolID)
	}
	if identity.TokenUse != "id" {
		t.Errorf("claims.token_use = %q, want %q", identity.TokenUse, "id")
	}
	if len(identity.Groups) != 1 || identity.Groups[0] != "admins" {
		t.Errorf("claims.cognito:groups = %v, want [admins]", identity.Groups)
	}
}

func TestAuthCognitoStrict_acceptsAccessToken(t *testing.T) {
	// Given: strict enforcement and an API bound to a local Cognito pool
	srv := helpers.NewTestServer(t, helpers.WithEnforceAppSyncCognitoAuth(true))
	pool := newCognitoPool(t, srv, "strict-access-pool")
	apiID := newCognitoAPI(t, srv, map[string]any{
		"authenticationType": "AMAZON_COGNITO_USER_POOLS",
		"userPoolConfig":     map[string]any{"userPoolId": pool.poolID},
	})

	// When: the access token from the same sign-in is presented
	identity := assertWhoamiOK(t, queryWhoami(t, srv, apiID, "Bearer "+pool.accessToken))

	// Then: it is accepted, and username comes from the access token's claim
	if identity.TokenUse != "access" {
		t.Errorf("claims.token_use = %q, want %q", identity.TokenUse, "access")
	}
	if identity.Username != pool.username {
		t.Errorf("identity.username = %q, want %q", identity.Username, pool.username)
	}
}

func TestAuthCognitoStrict_rejectsBadTokens(t *testing.T) {
	// Given: strict enforcement, an API bound to pool A, and a second pool
	srv := helpers.NewTestServer(t, helpers.WithEnforceAppSyncCognitoAuth(true))
	pool := newCognitoPool(t, srv, "strict-reject-pool")
	other := newCognitoPool(t, srv, "strict-other-pool")
	apiID := newCognitoAPI(t, srv, map[string]any{
		"authenticationType": "AMAZON_COGNITO_USER_POOLS",
		"userPoolConfig":     map[string]any{"userPoolId": pool.poolID},
	})

	// A signature that no longer matches the payload.
	parts := strings.Split(pool.idToken, ".")
	if len(parts) != 3 {
		t.Fatalf("minted token is not a JWT: %q", pool.idToken)
	}
	tampered := parts[0] + "." + parts[1] + "." + flipLastRune(parts[2])

	// A well-formed token from an issuer that is no local pool at all.
	foreignIssuer := unsignedJWTForTest(t, map[string]any{
		"sub":       "user-123",
		"iss":       "https://accounts.example.com/tenant-1",
		"token_use": "id",
		"exp":       time.Now().Add(time.Hour).Unix(),
	})

	tests := []struct {
		name          string
		authorization string
	}{
		{"missing Authorization header", ""},
		{"non-bearer Authorization header", pool.idToken},
		{"malformed token", "Bearer not-a-jwt"},
		{"tampered signature", "Bearer " + tampered},
		{"token from another local pool", "Bearer " + other.idToken},
		{"token from a non-local issuer", "Bearer " + foreignIssuer},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// When: the request carries that token
			resp := queryWhoami(t, srv, apiID, tc.authorization)

			// Then: AppSync answers 401 UnauthorizedException
			assertWhoamiUnauthorized(t, resp)
		})
	}
}

func TestAuthCognitoStrict_rejectsExpiredToken(t *testing.T) {
	// Given: strict enforcement on a server whose clock can be wound forward,
	// and an app client minting ID tokens with AWS's minimum 5-minute validity
	srv := helpers.NewTestServer(t, helpers.WithMockClock(), helpers.WithEnforceAppSyncCognitoAuth(true))
	pool := newCognitoPoolWithClient(t, srv, "strict-expiry-pool", map[string]any{
		"IdTokenValidity":    5,
		"TokenValidityUnits": map[string]string{"IdToken": "minutes"},
	})
	apiID := newCognitoAPI(t, srv, map[string]any{
		"authenticationType": "AMAZON_COGNITO_USER_POOLS",
		"userPoolConfig":     map[string]any{"userPoolId": pool.poolID},
	})

	// When: the token is presented after its expiry has passed
	srv.Clock.Add(6 * time.Minute)
	resp := queryWhoami(t, srv, apiID, "Bearer "+pool.idToken)

	// Then: AppSync answers 401 UnauthorizedException
	assertWhoamiUnauthorized(t, resp)
}

func TestAuthCognitoStrict_honoursAppIdClientRegex(t *testing.T) {
	// Given: strict enforcement and an API whose app client regex cannot match
	srv := helpers.NewTestServer(t, helpers.WithEnforceAppSyncCognitoAuth(true))
	pool := newCognitoPool(t, srv, "strict-regex-pool")
	deniedAPI := newCognitoAPI(t, srv, map[string]any{
		"authenticationType": "AMAZON_COGNITO_USER_POOLS",
		"userPoolConfig": map[string]any{
			"userPoolId": pool.poolID, "appIdClientRegex": "^someotherclient$",
		},
	})

	// When: a valid token from the pool's own client is presented
	resp := queryWhoami(t, srv, deniedAPI, "Bearer "+pool.idToken)

	// Then: the client ID does not match, so the request is unauthorized
	assertWhoamiUnauthorized(t, resp)

	// And: the same token is accepted when the regex does match the client
	allowedAPI := newCognitoAPI(t, srv, map[string]any{
		"authenticationType": "AMAZON_COGNITO_USER_POOLS",
		"userPoolConfig": map[string]any{
			"userPoolId": pool.poolID, "appIdClientRegex": "^" + pool.clientID + "$",
		},
	})
	if identity := assertWhoamiOK(t, queryWhoami(t, srv, allowedAPI, "Bearer "+pool.idToken)); identity.Sub == "" {
		t.Error("expected the matching client regex to authorize the request")
	}
}

func TestAuthCognitoStrict_appliesToAdditionalAuthProvider(t *testing.T) {
	// Given: strict enforcement and an API_KEY API with Cognito as an
	// additional authentication provider
	srv := helpers.NewTestServer(t, helpers.WithEnforceAppSyncCognitoAuth(true))
	pool := newCognitoPool(t, srv, "strict-multi-pool")
	additional, _ := json.Marshal([]map[string]any{{
		"authenticationType": "AMAZON_COGNITO_USER_POOLS",
		"userPoolConfig":     map[string]any{"userPoolId": pool.poolID},
	}})
	apiID := newCognitoAPI(t, srv, map[string]any{
		"authenticationType":                "API_KEY",
		"additionalAuthenticationProviders": json.RawMessage(additional),
	})

	// When: an unsigned token is presented on the additional provider
	fake := unsignedJWTForTest(t, map[string]any{"sub": "user-123", "iss": "https://example.test/" + pool.poolID})
	assertWhoamiUnauthorized(t, queryWhoami(t, srv, apiID, "Bearer "+fake))

	// Then: only the token Cognito actually minted is accepted
	if identity := assertWhoamiOK(t, queryWhoami(t, srv, apiID, "Bearer "+pool.idToken)); identity.Username != pool.username {
		t.Errorf("identity.username = %q, want %q", identity.Username, pool.username)
	}
}

// ─── Relaxed mode ────────────────────────────────────────────────────────────

func TestAuthCognitoRelaxed_acceptsUnsignedToken(t *testing.T) {
	// Given: the default (relaxed) posture and a Cognito-authenticated API
	srv := helpers.NewTestServer(t)
	apiID := newCognitoAPI(t, srv, map[string]any{
		"authenticationType": "AMAZON_COGNITO_USER_POOLS",
		"userPoolConfig":     map[string]any{"userPoolId": "us-east-1_fake"},
	})

	// When: an unsigned, expired token from an unknown issuer is presented
	token := unsignedJWTForTest(t, map[string]any{
		"sub":              "user-123",
		"iss":              "https://cognito-idp.us-east-1.amazonaws.com/us-east-1_nosuchpool",
		"cognito:username": "bob",
		"cognito:groups":   []string{"admins"},
		"token_use":        "id",
		"exp":              time.Now().Add(-time.Hour).Unix(),
	})
	identity := assertWhoamiOK(t, queryWhoami(t, srv, apiID, "Bearer "+token))

	// Then: it is accepted and its claims still reach the resolver
	if identity.Sub != "user-123" || identity.Username != "bob" {
		t.Errorf("identity = %+v, want sub=user-123 username=bob", identity)
	}
	if len(identity.Groups) != 1 || identity.Groups[0] != "admins" {
		t.Errorf("claims.cognito:groups = %v, want [admins]", identity.Groups)
	}
}

// ─── Local helpers ───────────────────────────────────────────────────────────

// unsignedJWTForTest builds a JWT with a real header and payload but a
// placeholder signature — the shape local development uses today.
func unsignedJWTForTest(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`)) + "." +
		base64.RawURLEncoding.EncodeToString(payload) + ".fakesig"
}

// flipLastRune changes the final character of a base64url segment so the
// signature no longer verifies while the token stays well formed.
func flipLastRune(s string) string {
	if s == "" {
		return "A"
	}
	last := s[len(s)-1]
	if last == 'A' {
		return s[:len(s)-1] + "B"
	}
	return s[:len(s)-1] + "A"
}
