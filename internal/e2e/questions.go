package e2e

import (
	"regexp"
	"strings"
)

var answerChoiceMarker = regexp.MustCompile(`(?i)(?:\([a-e]\)|(?:^|\s)[a-e]\)\s|\boption\s+[a-e]\b|\bwhich one\b|\bpick one\b|\bchoose one\b|\bselect one\b|\bjust the letter\b|\bone of (?:these|the) (?:two|three|four)\b)`)

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
