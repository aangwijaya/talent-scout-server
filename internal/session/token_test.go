package session

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestNewTokenLengths(t *testing.T) {
	raw, hash, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if len(raw) != 43 {
		t.Errorf("raw token length = %d, want 43 (base64url of 32 bytes)", len(raw))
	}
	if len(hash) != 64 {
		t.Errorf("hash length = %d, want 64 hex chars", len(hash))
	}
	if _, err := hex.DecodeString(hash); err != nil {
		t.Errorf("hash is not valid hex: %v", err)
	}
}

func TestNewTokenUniqueness(t *testing.T) {
	seen := make(map[string]struct{})
	for i := 0; i < 100; i++ {
		raw, _, err := NewToken()
		if err != nil {
			t.Fatalf("NewToken: %v", err)
		}
		if _, ok := seen[raw]; ok {
			t.Fatalf("duplicate token generated")
		}
		seen[raw] = struct{}{}
	}
}

func TestHashTokenMatchesRaw(t *testing.T) {
	raw, hash, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if got := HashToken(raw); got != hash {
		t.Errorf("HashToken(raw) = %q, want %q", got, hash)
	}
}

func TestInviteURL(t *testing.T) {
	got := InviteURL("https://talentscout.vercel.app", "abc123")
	want := "https://talentscout.vercel.app/s/abc123"
	if got != want {
		t.Errorf("InviteURL = %q, want %q", got, want)
	}
}

func TestInviteURLTrimsTrailingSlash(t *testing.T) {
	got := InviteURL("https://talentscout.vercel.app/", "abc123")
	if !strings.Contains(got, "talentscout.vercel.app/s/abc123") {
		t.Errorf("InviteURL = %q, want no double slash", got)
	}
}
