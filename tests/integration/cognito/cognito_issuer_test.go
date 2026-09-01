// cognito_issuer_test.go covers the origin Cognito embeds in the JWT "iss"
// claim and in the OIDC discovery document.
//
// This is the same defect class as the Lambda function-URL bug in
// docs/plans/host-routing-precedence.md: a client-facing URL built from the
// address the caller happened to dial rather than the address Overcast is
// configured to advertise. It was undetectable while the harness left
// OVERCAST_HOSTNAME unset, because the dial address and the configured hostname
// were then the same string. With the harness defaulting Hostname to "localhost"
// while httptest still dials 127.0.0.1, the two are distinguishable.
//
// Why it matters beyond tidiness: "iss" is not decoration. An OIDC client
// validates it against its configured issuer and, for key discovery, fetches
// {iss}/.well-known/jwks.json. A token minted with an issuer the validating
// party cannot dial — a sibling container handed 127.0.0.1, or a host CLI
// handed a compose service name — fails verification rather than degrading.
package cognito_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	cborlib "github.com/fxamacker/cbor/v2"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// issuerTestPool creates a pool, client and confirmed user, then authenticates,
// returning the pool ID and the id/access tokens.
func issuerTestPool(t *testing.T, srv *helpers.TestServer) (poolID, idToken, accessToken string) {
	t.Helper()
	poolID = createPool(t, srv, "issuer-pool")
	clientID := createClient(t, srv, poolID, "issuer-app")
	cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId": poolID, "Username": "issueruser",
		"UserAttributes": []map[string]string{{"Name": "email", "Value": "issuer@example.com"}},
	}).Body.Close()
	cognitoCall(t, srv, "AdminSetUserPassword", map[string]any{
		"UserPoolId": poolID, "Username": "issueruser",
		"Password": "IssuerTest1!", "Permanent": true,
	}).Body.Close()

	resp := cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID, "AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{"USERNAME": "issueruser", "PASSWORD": "IssuerTest1!"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		AuthenticationResult struct {
			AccessToken string
			IdToken     string
		} `json:"AuthenticationResult"`
	}
	helpers.DecodeJSON(t, resp, &out)
	return poolID, out.AuthenticationResult.IdToken, out.AuthenticationResult.AccessToken
}

// TestIssuer_usesConfiguredHostnameNotDialAddress asserts the "iss" claim is
// built on the configured external origin rather than the request's Host header.
//
// (*cognito.Service).issuerURL is "http://" + r.Host + "/" + poolID, matching the path portion of AWS's issuer
// (internal/services/cognito/handler_auth.go:307), so today the claim carries
// whatever address the authenticating client dialed — here 127.0.0.1 — and
// ignores OVERCAST_HOSTNAME entirely. Every other service that mints a
// client-facing URL routes through serviceutil.ClientBaseURL for exactly this
// reason; issuerURL is the outlier. There are 14 call sites through issuerURL.
func TestIssuer_usesConfiguredHostnameNotDialAddress(t *testing.T) {
	// Given: an emulator with a configured external hostname ("localhost", the
	// harness default) reached over its dial address (127.0.0.1).
	srv := helpers.NewTestServer(t)

	// When: a user authenticates and receives tokens.
	poolID, idToken, accessToken := issuerTestPool(t, srv)
	if idToken == "" || accessToken == "" {
		t.Fatal("expected id and access tokens")
	}

	// Then: both tokens carry an issuer on the configured external origin.
	want := srv.ExternalBase() + "/" + poolID
	for _, tc := range []struct{ name, token string }{
		{"IdToken", idToken},
		{"AccessToken", accessToken},
	} {
		if got, _ := decodeJWTPayload(t, tc.token)["iss"].(string); got != want {
			t.Errorf("%s iss = %q, want %q", tc.name, got, want)
		}
	}
}

// TestOIDCDiscovery_usesConfiguredHostname covers the same defect on the
// discovery document, which is the surface an OIDC library actually reads. Its
// issuer and jwks_uri come from the same hardcoded "http://" + r.Host
// expression (internal/services/cognito/handler_managed_login.go:1334-1335),
// duplicated rather than shared with issuerURL.
//
// The hardcoded scheme is a second, independent defect: with OVERCAST_TLS_CERT
// and OVERCAST_TLS_KEY set the server speaks https, but the issuer and jwks_uri
// it advertises still say http, so an OIDC client either fetches keys over a
// scheme the server does not serve or rejects the issuer outright. No test in
// the suite enables TLS, so nothing pins the scheme; this test at least pins the
// host, and the scheme follows once these URLs are minted through
// serviceutil.ClientBaseURL, which already resolves it from cfg.TLSEnabled().
func TestOIDCDiscovery_usesConfiguredHostname(t *testing.T) {
	// Given: an authenticated pool on a server with a configured hostname.
	srv := helpers.NewTestServer(t)
	poolID, _, _ := issuerTestPool(t, srv)

	// When: a client fetches the OIDC discovery document.
	resp, err := http.Get(srv.URL + "/" + poolID + "/.well-known/openid-configuration")
	if err != nil {
		t.Fatalf("fetch discovery document: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode discovery document: %v", err)
	}

	// Then: every advertised endpoint is on the configured external origin, so a
	// client that discovers keys here can dial them.
	base := srv.ExternalBase()
	wantIssuer := base + "/" + poolID
	if got, _ := doc["issuer"].(string); got != wantIssuer {
		t.Errorf("issuer = %q, want %q", got, wantIssuer)
	}
	if got, _ := doc["jwks_uri"].(string); got != wantIssuer+"/.well-known/jwks.json" {
		t.Errorf("jwks_uri = %q, want %q", got, wantIssuer+"/.well-known/jwks.json")
	}
	for _, key := range []string{"authorization_endpoint", "token_endpoint", "userinfo_endpoint"} {
		got, _ := doc[key].(string)
		if !strings.HasPrefix(got, base+"/") {
			t.Errorf("%s = %q, want prefix %q", key, got, base+"/")
		}
	}
}

// TestIssuer_sameAcrossWireProtocols asserts a CBOR client and a JSON client are
// told the same issuer for the same pool.
//
// Cognito has two dispatch paths. JSON goes through dispatchLegacy and the
// request-based issuerURL; Smithy RPC v2 CBOR goes through the typed operation
// table (service.go, Dispatch), where issuerURLTyped has only a context and no
// *http.Request. That function returned a bare "{region}/{poolId}" — no scheme,
// no host — so the same user authenticating with the same pool got a
// structurally different "iss" depending only on which wire protocol their SDK
// speaks. AWS SDKs are migrating to RPCv2 CBOR, so this is a live path, not a
// hypothetical one.
//
// A bare issuer is not merely cosmetic. An API Gateway JWT authorizer compares
// the claim to its configured issuer exactly (see apigateway/handler_auth.go),
// and an OIDC client resolves signing keys from "{iss}/.well-known/jwks.json" —
// neither works with a relative string.
//
// The assertion is protocol-to-protocol rather than against a literal, so it
// keeps holding if the issuer format itself is ever revised: the invariant is
// that the wire protocol must not change the answer.
func TestIssuer_sameAcrossWireProtocols(t *testing.T) {
	// Given: a pool with a confirmed user, set up over JSON.
	srv := helpers.NewTestServer(t)
	poolID := createPool(t, srv, "cbor-issuer-pool")
	clientID := createClient(t, srv, poolID, "cbor-issuer-app")
	cognitoCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId": poolID, "Username": "cboruser",
		"UserAttributes": []map[string]string{{"Name": "email", "Value": "cbor@example.com"}},
	}).Body.Close()
	cognitoCall(t, srv, "AdminSetUserPassword", map[string]any{
		"UserPoolId": poolID, "Username": "cboruser",
		"Password": "CborTest1!", "Permanent": true,
	}).Body.Close()

	authParams := map[string]string{"USERNAME": "cboruser", "PASSWORD": "CborTest1!"}

	// When: the same user authenticates over JSON.
	jsonResp := cognitoCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID, "AuthFlow": "USER_PASSWORD_AUTH", "AuthParameters": authParams,
	})
	defer jsonResp.Body.Close()
	helpers.AssertStatus(t, jsonResp, http.StatusOK)
	var jsonOut struct {
		AuthenticationResult struct{ IdToken string } `json:"AuthenticationResult"`
	}
	helpers.DecodeJSON(t, jsonResp, &jsonOut)

	// And: over Smithy RPC v2 CBOR.
	cborResp := cognitoCBORCall(t, srv, "InitiateAuth", map[string]any{
		"ClientId": clientID, "AuthFlow": "USER_PASSWORD_AUTH", "AuthParameters": authParams,
	})
	defer cborResp.Body.Close()
	helpers.AssertStatus(t, cborResp, http.StatusOK)
	var cborOut struct {
		AuthenticationResult struct {
			IdToken string `cbor:"IdToken"`
		} `cbor:"AuthenticationResult"`
	}
	if err := cborlib.NewDecoder(cborResp.Body).Decode(&cborOut); err != nil {
		t.Fatalf("decode CBOR response: %v", err)
	}
	if cborOut.AuthenticationResult.IdToken == "" {
		t.Fatal("CBOR InitiateAuth returned no IdToken")
	}

	// Then: both tokens carry the same issuer, on the configured external origin.
	jsonIss, _ := decodeJWTPayload(t, jsonOut.AuthenticationResult.IdToken)["iss"].(string)
	cborIss, _ := decodeJWTPayload(t, cborOut.AuthenticationResult.IdToken)["iss"].(string)

	if cborIss != jsonIss {
		t.Errorf("issuer differs by wire protocol:\n  JSON = %q\n  CBOR = %q", jsonIss, cborIss)
	}
	want := srv.ExternalBase() + "/" + poolID
	if cborIss != want {
		t.Errorf("CBOR iss = %q, want %q", cborIss, want)
	}
}
