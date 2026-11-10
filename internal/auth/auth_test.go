package auth_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/auth"
)

const (
	validSecret = "0123456789abcdef0123456789abcdef"
	otherSecret = "fedcba9876543210fedcba9876543210"
)

func TestIssueAndVerifyAccessToken(t *testing.T) {
	now := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	manager := auth.NewWithClock(validSecret, func() time.Time { return now })

	token, expiresAt, err := manager.IssueAccessToken()
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	if token == "" {
		t.Fatal("token is empty")
	}
	if want := now.Add(auth.AccessTokenTTL); !expiresAt.Equal(want) {
		t.Errorf("expiresAt = %v, want %v", expiresAt, want)
	}

	claims, err := manager.VerifyAccessToken(token)
	if err != nil {
		t.Fatalf("VerifyAccessToken: %v", err)
	}
	if claims.Audience != auth.AudienceAccess {
		t.Errorf("audience = %q, want %q", claims.Audience, auth.AudienceAccess)
	}
	if !claims.ExpiresAt.Equal(expiresAt) {
		t.Errorf("claims expiry = %v, want %v", claims.ExpiresAt, expiresAt)
	}
}

func TestVerifyAccessTokenRejectsExpired(t *testing.T) {
	now := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	manager := auth.NewWithClock(validSecret, func() time.Time { return now })

	token, _, err := manager.IssueAccessToken()
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	now = now.Add(auth.AccessTokenTTL)
	if _, err := manager.VerifyAccessToken(token); !errors.Is(err, auth.ErrExpiredToken) {
		t.Fatalf("VerifyAccessToken error = %v, want ErrExpiredToken", err)
	}
}

func TestVerifyAccessTokenRejectsTamperedSignature(t *testing.T) {
	manager := auth.NewWithClock(validSecret, time.Now)

	token, _, err := manager.IssueAccessToken()
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	last := token[len(token)-1]
	replacement := byte('A')
	if last == 'A' {
		replacement = 'B'
	}
	tampered := token[:len(token)-1] + string(replacement)

	if _, err := manager.VerifyAccessToken(tampered); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("VerifyAccessToken error = %v, want ErrInvalidToken", err)
	}
}

func TestVerifyAccessTokenRejectsWrongSecret(t *testing.T) {
	issuer := auth.NewWithClock(validSecret, time.Now)
	verifier := auth.NewWithClock(otherSecret, time.Now)

	token, _, err := issuer.IssueAccessToken()
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	if _, err := verifier.VerifyAccessToken(token); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("VerifyAccessToken error = %v, want ErrInvalidToken", err)
	}
}

func TestVerifyAccessTokenRejectsGarbage(t *testing.T) {
	manager := auth.NewWithClock(validSecret, time.Now)

	for _, token := range []string{"", "not-a-token", "a.b.c"} {
		if _, err := manager.VerifyAccessToken(token); !errors.Is(err, auth.ErrInvalidToken) {
			t.Errorf("VerifyAccessToken(%q) error = %v, want ErrInvalidToken", token, err)
		}
	}
}

func TestWeakSecretRefusesTokens(t *testing.T) {
	for _, secret := range []string{"", "too-short"} {
		t.Run(strconv.Quote(secret), func(t *testing.T) {
			manager := auth.NewWithClock(secret, time.Now)
			if _, _, err := manager.IssueAccessToken(); !errors.Is(err, auth.ErrWeakSecret) {
				t.Fatalf("IssueAccessToken error = %v, want ErrWeakSecret", err)
			}

			forged := forgeAccessToken(t, secret, time.Now().Add(time.Hour))
			if _, err := manager.VerifyAccessToken(forged); !errors.Is(err, auth.ErrInvalidToken) {
				t.Fatalf("VerifyAccessToken error = %v, want ErrInvalidToken for a token signed with a weak key", err)
			}
		})
	}
}

func forgeAccessToken(t *testing.T, secret string, expiresAt time.Time) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"aud": auth.AudienceAccess, "exp": expiresAt.Unix()})
	if err != nil {
		t.Fatalf("marshal forged payload: %v", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// A token signed with the right secret but another audience (such as the
// 60-second SSE token from server#19) must never work as a recruiter token.
func TestVerifyAccessTokenRejectsOtherAudience(t *testing.T) {
	now := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	manager := auth.NewWithClock(validSecret, func() time.Time { return now })

	body, err := json.Marshal(map[string]any{"aud": "sse", "run_id": "r1", "exp": now.Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, []byte(validSecret))
	mac.Write([]byte(encoded))
	token := encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	if _, err := manager.VerifyAccessToken(token); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("VerifyAccessToken(aud=sse) error = %v, want ErrInvalidToken", err)
	}
}
