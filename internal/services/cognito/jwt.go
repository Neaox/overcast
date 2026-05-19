package cognito

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

const nsSigningKeys = "cognito:sigkeys"

// poolSigningKey is the persisted RSA key record for one user pool.
type poolSigningKey struct {
	KID        string `json:"kid"`
	PrivKeyPEM string `json:"priv_pem"`
}

// getOrCreateSigningKey returns the RSA-2048 signing key for a pool, lazily
// generating and persisting one on first use.
func (s *Service) getOrCreateSigningKey(ctx context.Context, poolID string) (*rsa.PrivateKey, string, error) {
	storeKey := serviceutil.RegionKey(s.region(ctx), poolID)
	raw, found, err := s.store.Get(ctx, nsSigningKeys, storeKey)
	if err != nil {
		return nil, "", fmt.Errorf("cognito: read signing key for %s: %w", poolID, err)
	}
	if found {
		var sk poolSigningKey
		if err := json.Unmarshal([]byte(raw), &sk); err != nil {
			return nil, "", fmt.Errorf("cognito: unmarshal signing key: %w", err)
		}
		block, _ := pem.Decode([]byte(sk.PrivKeyPEM))
		if block == nil {
			return nil, "", fmt.Errorf("cognito: invalid PEM for pool %s", poolID)
		}
		iface, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, "", fmt.Errorf("cognito: parse signing key: %w", err)
		}
		priv, ok := iface.(*rsa.PrivateKey)
		if !ok {
			return nil, "", fmt.Errorf("cognito: unexpected key type for pool %s", poolID)
		}
		return priv, sk.KID, nil
	}

	// Generate a new RSA-2048 key pair for this pool.
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, "", fmt.Errorf("cognito: generate signing key: %w", err)
	}
	kid := strings.ReplaceAll(poolID, "_", "-") + "-key"
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, "", fmt.Errorf("cognito: marshal signing key: %w", err)
	}
	privPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	sk := poolSigningKey{KID: kid, PrivKeyPEM: privPEM}
	rawBytes, _ := json.Marshal(sk)
	if err := s.store.Set(ctx, nsSigningKeys, storeKey, string(rawBytes)); err != nil {
		return nil, "", fmt.Errorf("cognito: save signing key: %w", err)
	}
	s.log.Info("generated RSA-2048 signing key",
		zap.String("poolId", poolID), zap.String("kid", kid))
	return priv, kid, nil
}

// removeSigningKey deletes the stored signing key for a pool, called on pool deletion.
func (s *Service) removeSigningKey(ctx context.Context, poolID string) error {
	storeKey := serviceutil.RegionKey(s.region(ctx), poolID)
	return s.store.Delete(ctx, nsSigningKeys, storeKey)
}

// signJWT creates an RS256-signed JWT from the given claims map.
func signJWT(priv *rsa.PrivateKey, kid string, claims map[string]any) (string, error) {
	header := map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"}
	hdr, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	pl, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	msg := base64.RawURLEncoding.EncodeToString(hdr) + "." + base64.RawURLEncoding.EncodeToString(pl)
	hash := sha256.Sum256([]byte(msg))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, hash[:])
	if err != nil {
		return "", fmt.Errorf("cognito: sign JWT: %w", err)
	}
	return msg + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// parseJWTClaims decodes the payload of a JWT without verifying the signature.
// Used to extract the issuer before loading the signing key.
func parseJWTClaims(tokenStr string) (map[string]any, error) {
	parts := strings.SplitN(tokenStr, ".", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("malformed JWT: expected 3 parts, got %d", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("JWT payload decode: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("JWT payload JSON: %w", err)
	}
	return claims, nil
}

// verifyJWTSignature checks the RS256 signature of a JWT against the given public key.
func verifyJWTSignature(tokenStr string, pub *rsa.PublicKey) error {
	parts := strings.SplitN(tokenStr, ".", 3)
	if len(parts) != 3 {
		return fmt.Errorf("malformed JWT")
	}
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return fmt.Errorf("JWT signature decode: %w", err)
	}
	hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	return rsa.VerifyPKCS1v15(pub, crypto.SHA256, hash[:], sigBytes)
}

// poolIDFromIssuer extracts the user pool ID from a Cognito issuer URL.
// Expected format: http(s)://{host}/{region}/{poolId}.
func poolIDFromIssuer(iss string) (string, error) {
	idx := strings.LastIndex(iss, "/")
	if idx < 0 || idx == len(iss)-1 {
		return "", fmt.Errorf("cannot extract pool ID from issuer %q", iss)
	}
	return iss[idx+1:], nil
}

// ─── JWKS endpoint ────────────────────────────────────────────────────────────

// jwkEntry is one RSA key entry in a JWKS document.
type jwkEntry struct {
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	Kid string `json:"kid"`
	N   string `json:"n"` // base64url-encoded modulus
	E   string `json:"e"` // base64url-encoded public exponent
}

func jwkFromPublicKey(pub *rsa.PublicKey, kid string) jwkEntry {
	return jwkEntry{
		Kty: "RSA",
		Alg: "RS256",
		Use: "sig",
		Kid: kid,
		N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
}

// serveJWKS handles GET /{region}/{poolId}/.well-known/jwks.json.
// This endpoint is called by JWT validation libraries (e.g. aws-jwt-verify) to
// fetch the RSA public key for a user pool.
func (s *Service) serveJWKS(w http.ResponseWriter, r *http.Request) {
	poolID := chi.URLParam(r, "poolId")
	if _, ok := s.requirePool(r.Context(), w, r, poolID); !ok {
		return
	}
	priv, kid, err := s.getOrCreateSigningKey(r.Context(), poolID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	jwks := map[string]any{
		"keys": []jwkEntry{jwkFromPublicKey(&priv.PublicKey, kid)},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jwks)
}

// ─── Token validation (events.CognitoTokenValidator) ─────────────────────────

// ValidateCognitoToken satisfies events.CognitoTokenValidator.
// It parses the JWT, derives the user pool ID from the issuer claim, fetches the
// pool's RSA signing key, verifies the RS256 signature, and checks that the token
// has not expired. Returns the decoded claims on success.
//
// tokenStr must be the raw JWT without a "Bearer " prefix.
func (s *Service) ValidateCognitoToken(ctx context.Context, tokenStr string) (map[string]any, error) {
	claims, err := parseJWTClaims(tokenStr)
	if err != nil {
		return nil, fmt.Errorf("cognito: malformed JWT: %w", err)
	}

	iss, _ := claims["iss"].(string)
	poolID, err := poolIDFromIssuer(iss)
	if err != nil {
		return nil, fmt.Errorf("cognito: %w", err)
	}

	priv, _, err := s.getOrCreateSigningKey(ctx, poolID)
	if err != nil {
		return nil, fmt.Errorf("cognito: signing key for pool %s: %w", poolID, err)
	}

	if err := verifyJWTSignature(tokenStr, &priv.PublicKey); err != nil {
		return nil, fmt.Errorf("cognito: invalid signature: %w", err)
	}

	// Reject expired tokens (uses the injected clock so tests can fast-forward time).
	if exp, ok := claims["exp"].(float64); ok {
		if s.clk.Now().Unix() > int64(exp) {
			return nil, fmt.Errorf("cognito: token has expired")
		}
	}

	return claims, nil
}
