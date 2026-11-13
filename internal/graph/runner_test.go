package graph

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSummarizeCutsOnRuneBoundary(t *testing.T) {
	// Each "é" is two bytes, so a cut at 81 bytes lands inside a rune.
	text := strings.Repeat("é", 50)
	got := summarize(text, 81)
	if !utf8.ValidString(got) {
		t.Fatalf("summarize produced invalid UTF-8: %q", got)
	}
	if len(got) != 80 {
		t.Errorf("len(summarize) = %d, want 80", len(got))
	}
	if utf8.RuneCountInString(got) != 40 {
		t.Errorf("rune count = %d, want 40", utf8.RuneCountInString(got))
	}

	emoji := strings.Repeat("🙂", 30) // four bytes each
	got = summarize(emoji, 41)
	if !utf8.ValidString(got) {
		t.Fatalf("summarize produced invalid UTF-8 for emoji: %q", got)
	}
	if len(got) != 40 {
		t.Errorf("len(summarize(emoji)) = %d, want 40", len(got))
	}

	if got := summarize("short", 80); got != "short" {
		t.Errorf("summarize(short) = %q, want short", got)
	}
}
