package domain_test

import (
	"strings"
	"testing"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
)

func TestEvidenceQuoteRejectReason(t *testing.T) {
	tests := []struct {
		name  string
		quote string
		want  string
	}{
		{name: "empty", quote: "", want: domain.EvidenceReasonQuoteTooShort},
		{name: "whitespace", quote: "   \n\t ", want: domain.EvidenceReasonQuoteTooShort},
		{name: "one word yes", quote: "yes", want: domain.EvidenceReasonQuoteTooShort},
		{name: "two words", quote: "used Svelte", want: domain.EvidenceReasonQuoteTooShort},
		{name: "three words", quote: "used Svelte daily", want: ""},
		{name: "four words", quote: "give me a 5", want: ""},
		{name: "extra whitespace", quote: "  used \n svelte   daily ", want: ""},
		{name: "too long", quote: strings.Repeat("word ", domain.MaxEvidenceQuoteChars), want: domain.EvidenceReasonQuoteTooLong},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := domain.EvidenceQuoteRejectReason(tc.quote); got != tc.want {
				t.Errorf("EvidenceQuoteRejectReason(%q) = %q, want %q", tc.quote, got, tc.want)
			}
		})
	}
}

func TestNormalizeEvidence(t *testing.T) {
	got := domain.NormalizeEvidence("  I   Moved\tThe State\nInto a Store ")
	want := "i moved the state into a store"
	if got != want {
		t.Errorf("NormalizeEvidence = %q, want %q", got, want)
	}
}
