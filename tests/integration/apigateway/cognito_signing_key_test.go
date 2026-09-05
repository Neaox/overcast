package apigateway_test

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// nsCognitoSigningKeys is the state namespace Cognito persists one RSA signing
// key record per user pool in. Reading it directly through srv.Store is the
// only observation hook for issue #1731: the growth it describes is invisible
// on the wire — the request is rejected either way — and no AWS API lists
// signing keys, so a Cognito call could only reveal it for a pool the caller
// can name, which is exactly what a made-up issuer does not have.
const nsCognitoSigningKeys = "cognito:sigkeys"

// signingKeyRecords returns the sorted store keys currently held in
// cognito:sigkeys.
func signingKeyRecords(t *testing.T, srv *helpers.TestServer) []string {
	t.Helper()
	kvs, err := srv.Store.Scan(context.Background(), nsCognitoSigningKeys, "")
	if err != nil {
		t.Fatalf("scan %s: %v", nsCognitoSigningKeys, err)
	}
	keys := make([]string, 0, len(kvs))
	for _, kv := range kvs {
		keys = append(keys, kv.Key)
	}
	slices.Sort(keys)
	return keys
}

// TestExecuteAPI_cognitoAuthorizer_foreignIssuerLeavesNoSigningKey is the
// integration-level regression test for #1731. Both authorizer kinds hand the
// raw bearer token to Cognito's validator before anything is verified, and the
// validator used to derive a pool ID from the unverified "iss" claim and
// *create* a signing key for it: one RSA-2048 generation and one permanent,
// caller-keyed cognito:sigkeys record per distinct made-up issuer path. The
// 401 was never in question; the state growth behind it was the defect.
func TestExecuteAPI_cognitoAuthorizer_foreignIssuerLeavesNoSigningKey(t *testing.T) {
	// A syntactically valid RS256 token, signed by a key this server has never
	// seen, whose issuer path names a pool that does not exist.
	const foreignIssuer = "https://attacker.example/us-east-1_nosuchpool"

	cases := []struct {
		name string
		// protectedPath builds the API and returns the path of its
		// Cognito-protected route on srv.
		protectedPath func(t *testing.T, srv *helpers.TestServer, poolID string) string
	}{
		{
			name: "REST v1 COGNITO_USER_POOLS authorizer",
			protectedPath: func(t *testing.T, srv *helpers.TestServer, poolID string) string {
				t.Helper()
				apiID, stageName := setupRestAPIWithCognitoAuthorizer(t, srv, poolID, "us-east-1")
				return "/restapis/" + apiID + "/" + stageName + "/_user_request_/protected"
			},
		},
		{
			name: "HTTP v2 JWT authorizer",
			protectedPath: func(t *testing.T, srv *helpers.TestServer, poolID string) string {
				t.Helper()
				apiID, _ := setupV2APIWithJWTAuthorizer(t, srv, poolID, "us-east-1")
				return "/v2/apis/" + apiID + "/stages/dev/protected"
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a deployed API whose route is protected by a real user pool.
			srv := helpers.NewTestServer(t)
			poolID, _ := setupCognitoPool(t, srv)
			path := tc.protectedPath(t, srv, poolID)
			before := signingKeyRecords(t, srv)

			// When: a bearer token for an issuer naming no existing pool arrives.
			token := fakeRSASignedJWT(t, map[string]any{
				"sub":       "attacker",
				"iss":       foreignIssuer,
				"token_use": "access",
				"exp":       float64(9999999999),
			})
			req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			req.Header.Set("Authorization", "Bearer "+token)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer resp.Body.Close()

			// Then: the request is rejected...
			helpers.AssertStatus(t, resp, http.StatusUnauthorized)

			// ...and the authorizer left no signing key behind for the pool
			// the caller invented.
			after := signingKeyRecords(t, srv)
			if !slices.Equal(before, after) {
				t.Errorf("%s changed from %v to %v; a rejected token must mint no signing key",
					nsCognitoSigningKeys, before, after)
			}
		})
	}
}
