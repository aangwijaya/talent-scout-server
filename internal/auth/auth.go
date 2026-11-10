package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// AccessTokenTTL is the recruiter bearer token lifetime from docs/API.md.
	AccessTokenTTL = 12 * time.Hour
	// AudienceAccess marks a full recruiter access token, distinct from the
	// short-lived SSE tokens minted for graph run streams.
	AudienceAccess = "recruiter"
	// MinSecretBytes is the minimum AUTH_TOKEN_SECRET length from
	// docs/TECH-STACK.md. A shorter HMAC key is rejected rather than used.
	MinSecretBytes = 32
)

var (
	ErrInvalidToken = errors.New("invalid token")
	ErrExpiredToken = errors.New("expired token")
	ErrWeakSecret   = fmt.Errorf("auth token secret must be at least %d bytes", MinSecretBytes)
)

// Claims are the payload of a signed token.
type Claims struct {
	Audience  string
	RunID     string
	ExpiresAt time.Time
}

type payload struct {
	Audience  string `json:"aud"`
	RunID     string `json:"run_id,omitempty"`
	ExpiresAt int64  `json:"exp"`
}

// Manager signs and verifies stateless HMAC-SHA256 tokens with one secret.
type Manager struct {
	secret    []byte
	secretErr error
	now       func() time.Time
}

func New(secret string) *Manager {
	return NewWithClock(secret, time.Now)
}

// NewWithClock is New with an injectable clock, for tests. A secret shorter
// than MinSecretBytes leaves the manager unable to issue or verify tokens.
func NewWithClock(secret string, now func() time.Time) *Manager {
	if now == nil {
		now = time.Now
	}
	m := &Manager{secret: []byte(secret), now: now}
	if len(secret) < MinSecretBytes {
		m.secretErr = ErrWeakSecret
	}
	return m
}

func (m *Manager) IssueAccessToken() (string, time.Time, error) {
	if m.secretErr != nil {
		return "", time.Time{}, m.secretErr
	}
	expiresAt := m.now().UTC().Add(AccessTokenTTL)
	token, err := m.sign(Claims{Audience: AudienceAccess, ExpiresAt: expiresAt})
	if err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

func (m *Manager) VerifyAccessToken(token string) (Claims, error) {
	if m.secretErr != nil {
		return Claims{}, ErrInvalidToken
	}
	claims, err := m.verify(token)
	if err != nil {
		return Claims{}, err
	}
	if claims.Audience != AudienceAccess {
		return Claims{}, ErrInvalidToken
	}
	return claims, nil
}

func (m *Manager) sign(claims Claims) (string, error) {
	data, err := json.Marshal(payload{
		Audience:  claims.Audience,
		RunID:     claims.RunID,
		ExpiresAt: claims.ExpiresAt.Unix(),
	})
	if err != nil {
		return "", fmt.Errorf("marshal token claims: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(data)
	return encoded + "." + m.signature(encoded), nil
}

func (m *Manager) verify(token string) (Claims, error) {
	encoded, signature, ok := strings.Cut(token, ".")
	if !ok || encoded == "" || signature == "" {
		return Claims{}, ErrInvalidToken
	}
	if !hmac.Equal([]byte(signature), []byte(m.signature(encoded))) {
		return Claims{}, ErrInvalidToken
	}

	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	var p payload
	if err := json.Unmarshal(data, &p); err != nil {
		return Claims{}, ErrInvalidToken
	}

	claims := Claims{
		Audience:  p.Audience,
		RunID:     p.RunID,
		ExpiresAt: time.Unix(p.ExpiresAt, 0).UTC(),
	}
	if !claims.ExpiresAt.After(m.now()) {
		return Claims{}, ErrExpiredToken
	}
	return claims, nil
}

func (m *Manager) signature(encoded string) string {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(encoded))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
