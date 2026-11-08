package email

import (
	"context"
	"log/slog"
)

// Log prints the invite link to the log (EMAIL_MODE=log). Printing the link is
// the point of log mode, so it is logged in full here; the dashboard shows the
// same link via resend-invite.
type Log struct {
	logger *slog.Logger
}

func NewLog(logger *slog.Logger) *Log {
	return &Log{logger: logger}
}

func (l *Log) SendInvite(_ context.Context, to, _, link string) error {
	l.logger.Info("screening invite (EMAIL_MODE=log)", "to", to, "link", link)
	return nil
}
