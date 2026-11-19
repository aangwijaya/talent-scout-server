package e2e

import (
	"strings"
	"testing"
)

func TestInterviewerTurnProblems(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "one open question",
			content: "Tell me about a time you measured a performance problem and what you changed.",
			want:    nil,
		},
		{
			name:    "vague answer follow-up",
			content: "What concrete example can you give, with a number that shows the outcome?",
			want:    nil,
		},
		{
			name:    "closing message",
			content: "Thanks, that is all I need.",
			want:    nil,
		},
		{
			name:    "recorded multiple choice",
			content: "A product manager says a slow filter should ship anyway. You can only pick one of these three responses: (a) explain the slowdown with numbers, (b) ship it and fix it later, or (c) escalate to a tech lead. Which one would you pick — a, b, or c? Just the letter, and one short sentence on why.",
			want:    []string{"offers answer choices"},
		},
		{
			name:    "lettered options",
			content: "Which option matches your approach: a) rewrite it, b) ship it, or c) ask the team?",
			want:    []string{"offers answer choices"},
		},
		{
			name:    "two questions",
			content: "What did you change? Why did you choose that approach?",
			want:    []string{"asks more than one question"},
		},
		{
			name:    "pick one real example (live run, 2026-11-20)",
			content: "Let's separate that from the first-load numbers: pick one interaction on the console where users actually felt lag — say opening a filtered table or switching tenants. How did you capture the slowdown and what did you change?",
			want:    nil,
		},
		{
			name:    "choose one piece of your own work",
			content: "Choose one piece of shared state from the dashboard, for example a filter or a session, and tell me which component owned it and why?",
			want:    nil,
		},
		{
			name:    "pick from supplied options",
			content: "Pick one of these three: rewrite it, ship it, or escalate it?",
			want:    []string{"offers answer choices"},
		},
		{
			name:    "option a",
			content: "Would you choose Option A or Option B for this migration?",
			want:    []string{"offers answer choices"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := interviewerTurnProblems(tc.content)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("problems = %v, want %v", got, tc.want)
			}
		})
	}
}
