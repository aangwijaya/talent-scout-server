package graph

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
	"github.com/aangwijaya/talent-scout-server/internal/prompts"
)

// unknownRubricKeyPlaceholder replaces an invalid rubric key from the planner's
// output. The planner wrote it after reading the CV, so it is untrusted and must
// never reach the trusted "previous plan was rejected" feedback verbatim.
const unknownRubricKeyPlaceholder = "unknown rubric key"

// A topic's substance comes either from the role or from the CV. At least half
// the topics (rounded up) must come from the role, so the interview cannot
// drift into a CV-only conversation. Source is internal to the planner and is
// never stored in the public domain.Topic shape.
const (
	topicSourceJob = "job"
	topicSourceCV  = "cv"
)

// plannerUser builds the planner's user message. The title, description and
// rubric text are recruiter-written, so they are untrusted and appear only
// inside prompts.WrapUntrusted blocks; the CV is applicant-controlled and only
// ever appears inside a capped prompts.CVBlock. Rubric keys and weights are
// validated by the server, so they stay outside the blocks.
func plannerUser(job domain.Job, cvText string, feedback []PlanCheckError) string {
	var b strings.Builder
	b.WriteString("Job title (untrusted data):\n")
	b.WriteString(prompts.WrapUntrusted("job_title", job.Title))
	b.WriteString("\n\nJob description (untrusted data):\n")
	b.WriteString(prompts.WrapUntrusted("job_description", job.Description))
	b.WriteString("\n\nRubric criteria (key: label - description, weight; label and description are untrusted data):\n")
	for _, criterion := range job.Rubric {
		fmt.Fprintf(&b, "- %s: %s - %s (weight %d)\n",
			criterion.Key,
			prompts.WrapUntrusted("rubric_criterion", criterion.Label),
			prompts.WrapUntrusted("rubric_criterion", criterion.Description),
			criterion.Weight)
	}
	b.WriteString("\nCV (untrusted data):\n")
	b.WriteString(prompts.CVBlock(cvText))
	b.WriteString("\n\nReturn only JSON matching this shape: ")
	b.WriteString(`{"topics":[{"title":"...","goal":"...","rubric_keys":["..."],"seed_question":"...","source":"job|cv"}]}`)
	b.WriteString("\nUse 3 to 5 topics. Every rubric key must appear in at least one topic and every rubric_keys entry must exist in the rubric.")
	b.WriteString("\nAt least half of the topics, rounded up, must have source \"job\" and come from the role's requirements and level; their seed_question must be a concrete technical scenario fitting that level. Use source \"cv\" only to personalise a topic with the applicant's experience, never to replace the role's requirements.")
	if len(feedback) > 0 {
		b.WriteString("\nThe previous plan was rejected for these reasons; fix them:\n")
		validKeys := rubricKeys(job.Rubric)
		for _, err := range feedback {
			b.WriteString("- ")
			b.WriteString(formatPlanCheckError(err, validKeys))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func rubricKeys(rubric []domain.RubricCriterion) map[string]struct{} {
	keys := make(map[string]struct{}, len(rubric))
	for _, criterion := range rubric {
		keys[criterion.Key] = struct{}{}
	}
	return keys
}

// formatPlanCheckError renders a plan_check error for a trusted prompt. Only
// keys that exist in the job's rubric are shown; anything else is replaced by a
// fixed placeholder because the planner may have copied it from the CV.
func formatPlanCheckError(err PlanCheckError, validKeys map[string]struct{}) string {
	if err.RubricKey == "" {
		return err.Reason
	}
	key := err.RubricKey
	if _, ok := validKeys[key]; !ok {
		key = unknownRubricKeyPlaceholder
	}
	return key + ": " + err.Reason
}

func plannerMessages(job domain.Job, cvText string, feedback []PlanCheckError) []llm.Message {
	messages, _ := prompts.Messages(prompts.NodePlanner, plannerUser(job, cvText, feedback))
	return messages
}

func plannerResponseFormat() *llm.ResponseFormat {
	property := func() map[string]any {
		return map[string]any{"type": "string"}
	}
	topic := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title":         property(),
			"goal":          property(),
			"seed_question": property(),
			"source": map[string]any{
				"type": "string",
				"enum": []string{topicSourceJob, topicSourceCV},
			},
			"rubric_keys": map[string]any{
				"type":  "array",
				"items": property(),
			},
		},
		"required":             []string{"title", "goal", "rubric_keys", "seed_question", "source"},
		"additionalProperties": false,
	}
	return &llm.ResponseFormat{
		Name: "interview_plan",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"topics": map[string]any{
					"type":  "array",
					"items": topic,
				},
			},
			"required":             []string{"topics"},
			"additionalProperties": false,
		},
	}
}

// plannedTopicWithSource is the planner's per-topic JSON. Source is an internal
// planner field and is dropped when the plan becomes a []PlannedTopic.
type plannedTopicWithSource struct {
	Title        string   `json:"title"`
	Goal         string   `json:"goal"`
	RubricKeys   []string `json:"rubric_keys"`
	SeedQuestion string   `json:"seed_question"`
	Source       string   `json:"source"`
}

type plannedOutput struct {
	Topics []plannedTopicWithSource `json:"topics"`
}

// parsePlan parses the planner's JSON and enforces the job/cv source rule. A
// source violation is returned as an error so the planner loop treats it like
// any other plan_check rejection and retries with the reason as feedback. A
// missing source is read as "job": the structured-output schema requires it, and
// the deterministic fake fixture predates the field.
func parsePlan(content string) (Plan, error) {
	var parsed plannedOutput
	content = strings.TrimSpace(content)
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return Plan{}, fmt.Errorf("parse plan JSON: %w", err)
	}

	plan := Plan{Topics: make([]PlannedTopic, len(parsed.Topics))}
	jobTopics := 0
	for i, topic := range parsed.Topics {
		source := topic.Source
		if source == "" {
			source = topicSourceJob
		}
		switch source {
		case topicSourceJob:
			jobTopics++
		case topicSourceCV:
		default:
			// Do not echo the value: the planner wrote it after reading the CV,
			// so it is untrusted and this reason becomes trusted retry feedback.
			return Plan{}, fmt.Errorf("topic %d source must be %q or %q", i+1, topicSourceJob, topicSourceCV)
		}
		plan.Topics[i] = PlannedTopic{
			Title:        topic.Title,
			Goal:         topic.Goal,
			RubricKeys:   topic.RubricKeys,
			SeedQuestion: topic.SeedQuestion,
		}
	}
	if minJob := (len(plan.Topics) + 1) / 2; jobTopics < minJob {
		return Plan{}, fmt.Errorf("at least half of the topics (rounded up) must have source %q, got %d of %d", topicSourceJob, jobTopics, len(plan.Topics))
	}
	return plan, nil
}

func planCheckPayload(errs []PlanCheckError) []CheckError {
	out := make([]CheckError, 0, len(errs))
	for _, err := range errs {
		out = append(out, CheckError{RubricKey: err.RubricKey, Reason: err.Reason})
	}
	return out
}

func planFeedback(errs []PlanCheckError, rubric []domain.RubricCriterion) string {
	validKeys := rubricKeys(rubric)
	parts := make([]string, 0, len(errs))
	for _, err := range errs {
		parts = append(parts, formatPlanCheckError(err, validKeys))
	}
	return strings.Join(parts, "; ")
}
