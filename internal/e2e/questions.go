package e2e

import (
	"regexp"
	"strings"
)

// answerChoiceMarker flags answer choices the interviewer supplies: option letters, "Option A",
// "just the letter", "one of these three". Asking the applicant to pick one real example of
// their own ("pick one interaction where users felt lag") is an open question and passes.
var answerChoiceMarker = regexp.MustCompile(`(?i)(?:\([a-e]\)|(?:^|\s)[a-e]\)\s|\boption\s+[a-e]\b|\bjust the letter\b|\bone of (?:these|the) (?:two|three|four)\b)`)

func interviewerTurnProblems(content string) []string {
	var problems []string
	if strings.Count(content, "?") > 1 {
		problems = append(problems, "asks more than one question")
	}
	if answerChoiceMarker.MatchString(content) {
		problems = append(problems, "offers answer choices")
	}
	return problems
}
