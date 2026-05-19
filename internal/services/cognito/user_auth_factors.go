package cognito

import (
	"encoding/base64"
	"encoding/json"
	"slices"
	"time"

	"github.com/Neaox/overcast/internal/protocol"
)

const userAuthOTPCodeTTL = 3 * time.Minute
const webAuthnChallengeTTL = 5 * time.Minute

func allowedFirstAuthFactor(pool *UserPool, factor string) bool {
	if pool == nil || pool.Policies == nil || pool.Policies.SignInPolicy == nil || len(pool.Policies.SignInPolicy.AllowedFirstAuthFactors) == 0 {
		return true
	}
	return slices.Contains(pool.Policies.SignInPolicy.AllowedFirstAuthFactors, factor)
}

func availableUserAuthChallenges(pool *UserPool, u *User) []string {
	challenges := make([]string, 0, 3)
	if allowedFirstAuthFactor(pool, "PASSWORD") && u.PasswordHash != "" {
		challenges = append(challenges, "PASSWORD")
		challenges = append(challenges, "PASSWORD_SRP")
	}
	if allowedFirstAuthFactor(pool, "EMAIL_OTP") && u.email() != "" {
		challenges = append(challenges, "EMAIL_OTP")
	}
	if allowedFirstAuthFactor(pool, "SMS_OTP") && u.phoneNumber() != "" {
		challenges = append(challenges, "SMS_OTP")
	}
	if allowedFirstAuthFactor(pool, "WEB_AUTHN") && webAuthnEnabled(pool) && len(u.WebAuthnCredentials) > 0 {
		challenges = append(challenges, "WEB_AUTHN")
	}
	return challenges
}

func webAuthnEnabled(pool *UserPool) bool {
	if pool == nil || pool.WebAuthnConfiguration == nil {
		return false
	}
	return webAuthnRelyingPartyID(pool) != ""
}

func webAuthnRelyingPartyID(pool *UserPool) string {
	if pool == nil || pool.WebAuthnConfiguration == nil {
		return ""
	}
	if pool.WebAuthnConfiguration.RelyingPartyID != "" {
		return pool.WebAuthnConfiguration.RelyingPartyID
	}
	return pool.Domain
}

func webAuthnUserVerification(pool *UserPool) string {
	if pool == nil || pool.WebAuthnConfiguration == nil || pool.WebAuthnConfiguration.UserVerification == "" {
		return "preferred"
	}
	return pool.WebAuthnConfiguration.UserVerification
}

func webAuthnCredentialCreationOptions(pool *UserPool, u *User, challenge string) map[string]any {
	exclude := make([]map[string]string, 0, len(u.WebAuthnCredentials))
	for _, credential := range u.WebAuthnCredentials {
		exclude = append(exclude, map[string]string{"id": credential.ID, "type": "public-key"})
	}
	rpID := webAuthnRelyingPartyID(pool)
	return map[string]any{
		"authenticatorSelection": map[string]any{
			"requireResidentKey": true,
			"residentKey":        "required",
			"userVerification":   webAuthnUserVerification(pool),
		},
		"challenge":          challenge,
		"excludeCredentials": exclude,
		"pubKeyCredParams": []map[string]any{
			{"alg": -7, "type": "public-key"},
			{"alg": -257, "type": "public-key"},
		},
		"rp":      map[string]string{"id": rpID, "name": rpID},
		"timeout": 60000,
		"user": map[string]string{
			"displayName": u.Username,
			"id":          base64.RawURLEncoding.EncodeToString([]byte(u.Sub)),
			"name":        u.Username,
		},
	}
}

func webAuthnCredentialRequestOptions(pool *UserPool, u *User, challenge string) (string, error) {
	allow := make([]map[string]any, 0, len(u.WebAuthnCredentials))
	for _, credential := range u.WebAuthnCredentials {
		allow = append(allow, map[string]any{"id": credential.ID, "type": "public-key", "transports": []string{}})
	}
	raw, err := json.Marshal(map[string]any{
		"challenge":        challenge,
		"timeout":          180000,
		"rpId":             webAuthnRelyingPartyID(pool),
		"allowCredentials": allow,
		"userVerification": webAuthnUserVerification(pool),
	})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func webAuthnCredentialID(credential any) string {
	switch v := credential.(type) {
	case map[string]any:
		id, _ := v["id"].(string)
		return id
	case string:
		var decoded map[string]any
		if err := json.Unmarshal([]byte(v), &decoded); err == nil {
			id, _ := decoded["id"].(string)
			return id
		}
	}
	return ""
}

func hasWebAuthnCredential(u *User, id string) bool {
	for _, credential := range u.WebAuthnCredentials {
		if credential.ID == id {
			return true
		}
	}
	return false
}

func srpChallengeParameters(username string) map[string]string {
	return map[string]string{
		"USER_ID_FOR_SRP": username,
		"SALT":            generateToken(),
		"SRP_B":           generateToken() + generateToken(),
		"SECRET_BLOCK":    base64.StdEncoding.EncodeToString([]byte(generateToken())),
	}
}

func containsChallenge(challenges []string, challenge string) bool {
	return slices.Contains(challenges, challenge)
}

func setAuthChallengeCode(u *User, challengeName, code string, expiresAt time.Time) {
	for i, existing := range u.AuthChallengeCodes {
		if existing.ChallengeName == challengeName {
			u.AuthChallengeCodes[i] = AuthChallengeCode{ChallengeName: challengeName, Code: code, ExpiresAt: expiresAt}
			return
		}
	}
	u.AuthChallengeCodes = append(u.AuthChallengeCodes, AuthChallengeCode{ChallengeName: challengeName, Code: code, ExpiresAt: expiresAt})
}

func removeAuthChallengeCode(u *User, challengeName string) {
	for i, existing := range u.AuthChallengeCodes {
		if existing.ChallengeName == challengeName {
			u.AuthChallengeCodes = append(u.AuthChallengeCodes[:i], u.AuthChallengeCodes[i+1:]...)
			return
		}
	}
}

func authChallengeCode(u *User, challengeName string) (AuthChallengeCode, bool) {
	for _, existing := range u.AuthChallengeCodes {
		if existing.ChallengeName == challengeName {
			return existing, true
		}
	}
	return AuthChallengeCode{}, false
}

func (s *Service) issueUserAuthOTP(pool *UserPool, u *User, challengeName string) (map[string]string, *protocol.AWSError) {
	code := generateCode()
	switch challengeName {
	case "EMAIL_OTP":
		if u.email() == "" {
			return nil, errNoSupportedFirstAuthFactors()
		}
		setAuthChallengeCode(u, challengeName, code, s.clk.Now().Add(userAuthOTPCodeTTL))
		s.sendVerificationEmail(pool, u.email(), u.Username, code)
		return map[string]string{"CODE_DELIVERY_DELIVERY_MEDIUM": "EMAIL", "CODE_DELIVERY_DESTINATION": u.email(), "USERNAME": u.Username}, nil
	case "SMS_OTP":
		if u.phoneNumber() == "" {
			return nil, errNoSupportedFirstAuthFactors()
		}
		setAuthChallengeCode(u, challengeName, code, s.clk.Now().Add(userAuthOTPCodeTTL))
		s.sendVerificationSMS(pool, u.phoneNumber(), u.Username, code)
		return map[string]string{"CODE_DELIVERY_DELIVERY_MEDIUM": "SMS", "CODE_DELIVERY_DESTINATION": u.phoneNumber(), "USERNAME": u.Username}, nil
	default:
		return nil, &protocol.AWSError{Code: "InvalidParameterException", Message: "Unsupported challenge answer: " + challengeName, HTTPStatus: 400}
	}
}
