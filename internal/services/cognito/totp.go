package cognito

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // TOTP (RFC 6238) mandates HMAC-SHA1
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// generateTOTPSecret returns a cryptographically random base32-encoded TOTP
// secret (20 bytes / 160 bits), compatible with authenticator apps.
func generateTOTPSecret() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
}

// computeTOTP returns the 6-digit TOTP code for the given base32 secret at
// time t, using a 30-second window (RFC 6238 with HMAC-SHA1).
func computeTOTP(secretBase32 string, t time.Time) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secretBase32))
	if err != nil {
		return "", fmt.Errorf("totp: decode secret: %w", err)
	}

	counter := uint64(t.Unix() / 30)
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)

	mac := hmac.New(sha1.New, key) //nolint:gosec // required by RFC 6238
	_, _ = mac.Write(buf)
	h := mac.Sum(nil)

	offset := h[len(h)-1] & 0x0f
	code := (int(h[offset])&0x7f)<<24 |
		int(h[offset+1])<<16 |
		int(h[offset+2])<<8 |
		int(h[offset+3])
	return fmt.Sprintf("%06d", code%1_000_000), nil
}

// verifyTOTP returns true if code matches the current or immediately preceding
// 30-second window for secretBase32. The two-window tolerance compensates for
// clock skew between the emulator and the authenticator app.
func verifyTOTP(secretBase32, code string, at time.Time) bool {
	for _, delta := range []time.Duration{0, -30 * time.Second} {
		expected, err := computeTOTP(secretBase32, at.Add(delta))
		if err != nil {
			return false
		}
		if code == expected {
			return true
		}
	}
	return false
}
