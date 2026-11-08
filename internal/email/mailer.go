package email

import "context"

// Mailer delivers the screening invite link to an applicant.
type Mailer interface {
	SendInvite(ctx context.Context, to, name, link string) error
}
