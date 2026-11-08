package domain

import (
	"strings"
	"unicode/utf8"
)

// Evidence quotes must be long enough to carry information. A one-word answer
// such as "yes" is a real substring of the transcript but proves nothing about
// a rubric criterion.
const (
	MinEvidenceQuoteWords = 3
	MaxEvidenceQuoteChars = 300
)

const (
	EvidenceReasonQuoteTooShort = "quote_too_short"
	EvidenceReasonQuoteTooLong  = "quote_too_long"
)

// NormalizeEvidence collapses whitespace and lowercases text so quotes can be
// compared as substrings of an applicant message.
func NormalizeEvidence(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// EvidenceQuoteRejectReason returns the check.failed reason for an unusable
// quote, or "" when the quote is acceptable. It only validates the quote
// itself; the caller still has to prove it is a substring of the referenced
// applicant message.
func EvidenceQuoteRejectReason(quote string) string {
	switch {
	case len(strings.Fields(quote)) < MinEvidenceQuoteWords:
		return EvidenceReasonQuoteTooShort
	case utf8.RuneCountInString(quote) > MaxEvidenceQuoteChars:
		return EvidenceReasonQuoteTooLong
	default:
		return ""
	}
}
