package graph

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/prompts"
)

// PlannedTopic is one topic produced by the planner.
type PlannedTopic struct {
	Title        string   `json:"title"`
	Goal         string   `json:"goal"`
	RubricKeys   []string `json:"rubric_keys"`
	SeedQuestion string   `json:"seed_question"`
}

// Plan is the planner's JSON output.
type Plan struct {
	Topics []PlannedTopic `json:"topics"`
}

// PlanCheckError is one reason a plan is invalid.
type PlanCheckError struct {
	RubricKey string
	Reason    string
}

func (e PlanCheckError) String() string {
	if e.RubricKey == "" {
		return e.Reason
	}
	return fmt.Sprintf("%s: %s", e.RubricKey, e.Reason)
}

const (
	minTopics = 3
	maxTopics = 5

	// Topic fields are shown to the interviewer, so a planner that echoes
	// applicant text must not smuggle in a delimiter or an essay.
	maxTopicTitleChars = 120
	maxTopicTextChars  = 400
)

// CheckPlan validates the plan against ARCHITECTURE §2: 3-5 topics, every
// rubric_keys entry is a real rubric key, and every rubric criterion is covered
// by at least one topic. It is a code check and never calls the LLM.
func CheckPlan(plan Plan, rubric []domain.RubricCriterion) []PlanCheckError {
	var errs []PlanCheckError

	if len(plan.Topics) < minTopics || len(plan.Topics) > maxTopics {
		errs = append(errs, PlanCheckError{Reason: fmt.Sprintf("topic_count must be between %d and %d, got %d", minTopics, maxTopics, len(plan.Topics))})
	}

	validKeys := make(map[string]struct{}, len(rubric))
	for _, criterion := range rubric {
		validKeys[criterion.Key] = struct{}{}
	}
	covered := make(map[string]bool, len(rubric))

	for i, topic := range plan.Topics {
		position := i + 1
		if strings.TrimSpace(topic.Title) == "" {
			errs = append(errs, PlanCheckError{Reason: fmt.Sprintf("topic %d title must not be empty", position)})
		}
		if strings.TrimSpace(topic.Goal) == "" {
			errs = append(errs, PlanCheckError{Reason: fmt.Sprintf("topic %d goal must not be empty", position)})
		}
		if strings.TrimSpace(topic.SeedQuestion) == "" {
			errs = append(errs, PlanCheckError{Reason: fmt.Sprintf("topic %d seed_question must not be empty", position)})
		}
		for _, field := range []struct {
			name  string
			value string
			max   int
		}{
			{"title", topic.Title, maxTopicTitleChars},
			{"goal", topic.Goal, maxTopicTextChars},
			{"seed_question", topic.SeedQuestion, maxTopicTextChars},
		} {
			if prompts.HasUntrustedDelimiter(field.value) {
				errs = append(errs, PlanCheckError{Reason: fmt.Sprintf("topic %d %s must not contain an untrusted-data delimiter", position, field.name)})
			}
			if utf8.RuneCountInString(field.value) > field.max {
				errs = append(errs, PlanCheckError{Reason: fmt.Sprintf("topic %d %s must be at most %d characters", position, field.name, field.max)})
			}
		}
		if len(topic.RubricKeys) == 0 {
			errs = append(errs, PlanCheckError{Reason: fmt.Sprintf("topic %d rubric_keys must not be empty", position)})
		}
		for _, key := range topic.RubricKeys {
			if _, ok := validKeys[key]; !ok {
				errs = append(errs, PlanCheckError{RubricKey: key, Reason: fmt.Sprintf("topic %d rubric_keys contains a key not in the rubric", position)})
				continue
			}
			covered[key] = true
		}
	}

	for _, criterion := range rubric {
		if !covered[criterion.Key] {
			errs = append(errs, PlanCheckError{RubricKey: criterion.Key, Reason: "rubric criterion is not covered by any topic"})
		}
	}
	return errs
}
