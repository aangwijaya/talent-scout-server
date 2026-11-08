package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
)

// TokenBytes is the number of random bytes in a session token.
const TokenBytes = 32

// NewToken returns a random base64url session token and its hex SHA-256 hash.
// Only the hash is stored; the raw token appears only in the invite link (the
// email or the server log, depending on EMAIL_MODE).
func NewToken() (raw, hash string, err error) {
	b := make([]byte, TokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generate session token: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, HashToken(raw), nil
}

// HashToken returns the hex SHA-256 hash of a raw token, used for lookup.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// InviteURL builds the public applicant link for a raw token.
func InviteURL(publicAppURL, raw string) string {
	return strings.TrimRight(publicAppURL, "/") + "/s/" + raw
}

// EffectiveStatus treats an invited session past its expiry as expired, so
// callers don't have to wait for the sweeper to mark it.
func EffectiveStatus(s domain.Session, now time.Time) domain.SessionStatus {
	if s.Status == domain.SessionStatusInvited && !s.ExpiresAt.After(now) {
		return domain.SessionStatusExpired
	}
	return s.Status
}
