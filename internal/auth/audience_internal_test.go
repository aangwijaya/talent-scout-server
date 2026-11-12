package auth

import (
	"errors"
	"testing"
	"time"
)

// A correctly signed token for the same run but another audience must not open
// a run stream. The public tests can only mint access tokens, which carry no
// run_id, so they never reach the audience check on their own.
func TestVerifySSETokenRejectsOtherAudienceForSameRun(t *testing.T) {
	manager := New("0123456789abcdef0123456789abcdef")
	token, err := manager.sign(Claims{Audience: AudienceAccess, RunID: "run-1", ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := manager.VerifySSEToken(token, "run-1"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("VerifySSEToken(aud=recruiter, same run) error = %v, want ErrInvalidToken", err)
	}
}
