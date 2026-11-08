package session

import (
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
)

func TestEffectiveStatusInvitedNotExpired(t *testing.T) {
	s := domain.Session{Status: domain.SessionStatusInvited, ExpiresAt: time.Now().Add(time.Hour)}
	if got := EffectiveStatus(s, time.Now()); got != domain.SessionStatusInvited {
		t.Errorf("EffectiveStatus = %q, want invited", got)
	}
}

func TestEffectiveStatusInvitedPastExpiry(t *testing.T) {
	s := domain.Session{Status: domain.SessionStatusInvited, ExpiresAt: time.Now().Add(-time.Hour)}
	if got := EffectiveStatus(s, time.Now()); got != domain.SessionStatusExpired {
		t.Errorf("EffectiveStatus = %q, want expired", got)
	}
}

func TestEffectiveStatusActiveKept(t *testing.T) {
	s := domain.Session{Status: domain.SessionStatusActive, ExpiresAt: time.Now().Add(-time.Hour)}
	if got := EffectiveStatus(s, time.Now()); got != domain.SessionStatusActive {
		t.Errorf("EffectiveStatus = %q, want active", got)
	}
}

func TestEffectiveStatusCompletedKept(t *testing.T) {
	s := domain.Session{Status: domain.SessionStatusCompleted}
	if got := EffectiveStatus(s, time.Now()); got != domain.SessionStatusCompleted {
		t.Errorf("EffectiveStatus = %q, want completed", got)
	}
}
