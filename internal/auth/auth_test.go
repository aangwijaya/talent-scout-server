package auth_test

import (
	"errors"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/auth"
)

func TestIssueAndVerifyAccessToken(t *testing.T) {
	now := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	manager := auth.NewWithClock("test-secret", func() time.Time { return now })

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
	manager := auth.NewWithClock("test-secret", func() time.Time { return now })

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
	manager := auth.NewWithClock("test-secret", time.Now)

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
	issuer := auth.NewWithClock("secret-a", time.Now)
	verifier := auth.NewWithClock("secret-b", time.Now)

	token, _, err := issuer.IssueAccessToken()
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	if _, err := verifier.VerifyAccessToken(token); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("VerifyAccessToken error = %v, want ErrInvalidToken", err)
	}
}

func TestVerifyAccessTokenRejectsGarbage(t *testing.T) {
	manager := auth.NewWithClock("test-secret", time.Now)

	for _, token := range []string{"", "not-a-token", "a.b.c"} {
		if _, err := manager.VerifyAccessToken(token); !errors.Is(err, auth.ErrInvalidToken) {
			t.Errorf("VerifyAccessToken(%q) error = %v, want ErrInvalidToken", token, err)
		}
	}
}
