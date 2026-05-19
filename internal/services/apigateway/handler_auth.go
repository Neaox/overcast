package apigateway

// handler_auth.go — Cognito/JWT authorizer enforcement during request execution.
//
// Supports:
//   REST v1:  COGNITO_USER_POOLS — Bearer token validated against the pool's
//             RS256 signing key; pool must be in the authorizer's ProviderARNs.
//   HTTP v2:  JWT — Bearer token validated against the pool's RS256 signing key;
//             optionally checks issuer and audience from JwtConfiguration.
//
// Both cases are permissive-on-misconfiguration: if the validator is not wired
// (Cognito service disabled) or the authorizer record is missing, the request
// passes through un-checked. This mirrors the behaviour of other optional
// integrations (e.g. Lambda invoker) in this package.

import (
	"net/http"
	"strings"
)

// checkRestCognitoAuthorizer enforces the COGNITO_USER_POOLS authorizer for a
// REST v1 method. Returns true when the request is authorised (or when no
// Cognito authorizer is configured). On failure it writes a 401/403 response
// and returns false; the caller must stop processing.
func (h *Handler) checkRestCognitoAuthorizer(w http.ResponseWriter, r *http.Request, apiID string, method *Method) bool {
	if method.AuthorizationType != "COGNITO_USER_POOLS" || method.AuthorizerID == "" {
		return true
	}
	if h.cognitoValidator == nil {
		// Cognito service not wired — allow through (permissive degradation).
		return true
	}

	auth, aerr := h.store.getAuthorizer(r.Context(), apiID, method.AuthorizerID)
	if aerr != nil {
		// Authorizer config missing — allow through.
		return true
	}

	token := extractIdentitySourceToken(r, auth.IdentitySource)
	if token == "" {
		writeGatewayError(w, http.StatusUnauthorized, "Unauthorized")
		return false
	}

	claims, err := h.cognitoValidator.ValidateCognitoToken(r.Context(), token)
	if err != nil {
		writeGatewayError(w, http.StatusUnauthorized, "Unauthorized")
		return false
	}

	// If ProviderARNs is set, the token's pool must be listed.
	if len(auth.ProviderARNs) > 0 {
		iss, _ := claims["iss"].(string)
		if !poolInProviderARNs(poolIDFromIssuerPath(iss), auth.ProviderARNs) {
			writeGatewayError(w, http.StatusForbidden, "User is not authorized to access this resource")
			return false
		}
	}

	return true
}

// checkV2JWTAuthorizer enforces the JWT authorizer for an HTTP v2 route.
// Returns true when authorised; on failure writes the error response and
// returns false.
func (h *Handler) checkV2JWTAuthorizer(w http.ResponseWriter, r *http.Request, apiID string, route *RouteV2) bool {
	if route.AuthorizationType != "JWT" || route.AuthorizerID == "" {
		return true
	}
	if h.cognitoValidator == nil {
		return true
	}

	auth, aerr := h.store.getV2Authorizer(r.Context(), apiID, route.AuthorizerID)
	if aerr != nil {
		return true
	}

	token := extractIdentitySourceToken(r, auth.IdentitySource)
	if token == "" {
		writeGatewayError(w, http.StatusUnauthorized, "Unauthorized")
		return false
	}

	claims, err := h.cognitoValidator.ValidateCognitoToken(r.Context(), token)
	if err != nil {
		writeGatewayError(w, http.StatusUnauthorized, "Unauthorized")
		return false
	}

	if auth.JwtConfiguration != nil {
		// Validate issuer when configured.
		if auth.JwtConfiguration.Issuer != "" {
			iss, _ := claims["iss"].(string)
			if iss != auth.JwtConfiguration.Issuer {
				writeGatewayError(w, http.StatusUnauthorized, "Unauthorized")
				return false
			}
		}

		// Validate audience when configured.
		if len(auth.JwtConfiguration.Audience) > 0 {
			aud, _ := claims["aud"].(string)
			if !stringInSlice(aud, auth.JwtConfiguration.Audience) {
				writeGatewayError(w, http.StatusForbidden, "User is not authorized to access this resource")
				return false
			}
		}
	}

	return true
}

// checkAPIKey enforces method-level apiKeyRequired=true for REST v1. It reads
// the x-api-key header, looks up the matching API key in the current region,
// and verifies the key is enabled and attached to a usage plan that covers
// the {apiID, stageName} pair. On failure it writes a 403 Forbidden response
// and returns false; the caller must stop processing.
//
// AWS uses 403 Forbidden (not 401) for missing/invalid API keys.
func (h *Handler) checkAPIKey(w http.ResponseWriter, r *http.Request, apiID, stageName string) bool {
	value := r.Header.Get("x-api-key")
	if value == "" {
		writeGatewayError(w, http.StatusForbidden, "Forbidden")
		return false
	}

	key, aerr := h.store.getAPIKeyByValue(r.Context(), value)
	if aerr != nil {
		writeGatewayError(w, http.StatusInternalServerError, "Internal Server Error")
		return false
	}
	if key == nil || !key.Enabled {
		writeGatewayError(w, http.StatusForbidden, "Forbidden")
		return false
	}

	plan, aerr := h.store.findUsagePlanForAPIKey(r.Context(), key.ID, apiID, stageName)
	if aerr != nil {
		writeGatewayError(w, http.StatusInternalServerError, "Internal Server Error")
		return false
	}
	if plan == nil {
		writeGatewayError(w, http.StatusForbidden, "Forbidden")
		return false
	}

	return true
}

// extractIdentitySourceToken extracts the bearer token from the request using
// the configured identitySource expression and strips the "Bearer " prefix.
//
//	REST v1 format: "method.request.header.Authorization"
//	HTTP v2 format: "$request.header.authorization"
//
// Falls back to the Authorization header when identitySource is empty or has
// an unrecognised format.
func extractIdentitySourceToken(r *http.Request, identitySource string) string {
	headerName := "Authorization"
	switch {
	case strings.HasPrefix(identitySource, "method.request.header."):
		headerName = identitySource[len("method.request.header."):]
	case strings.HasPrefix(identitySource, "$request.header."):
		headerName = identitySource[len("$request.header."):]
	}

	raw := r.Header.Get(headerName)
	if raw == "" {
		return ""
	}
	// Strip "Bearer " prefix (case-insensitive, 7 chars).
	if len(raw) > 7 && strings.EqualFold(raw[:7], "Bearer ") {
		return raw[7:]
	}
	return raw
}

// poolIDFromIssuerPath extracts the pool ID (last path segment) from a Cognito
// issuer URL. Format: http(s)://{host}/{region}/{poolId}
// Returns an empty string when extraction is not possible.
func poolIDFromIssuerPath(iss string) string {
	idx := strings.LastIndex(iss, "/")
	if idx < 0 || idx == len(iss)-1 {
		return ""
	}
	return iss[idx+1:]
}

// poolInProviderARNs reports whether poolID is referenced by any of the given
// Cognito User Pool ARNs.
// ARN format: arn:aws:cognito-idp:{region}:{account}:userpool/{poolId}.
func poolInProviderARNs(poolID string, arns []string) bool {
	if poolID == "" {
		return false
	}
	for _, arn := range arns {
		if strings.HasSuffix(arn, "/"+poolID) {
			return true
		}
	}
	return false
}

// stringInSlice reports whether s is present in slice (linear scan; audience
// lists are small in practice).
func stringInSlice(s string, slice []string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
