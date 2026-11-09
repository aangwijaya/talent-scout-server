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

// plannerUser builds the planner's user message. The job description and
// rubric are trusted; the CV is applicant-controlled and only ever appears
// inside a capped prompts.CVBlock.
func plannerUser(job domain.Job, cvText string, feedback []PlanCheckError) string {
	var b strings.Builder
	b.WriteString("Job title: ")
	b.WriteString(job.Title)
	b.WriteString("\n\nJob description:\n")
	b.WriteString(job.Description)
	b.WriteString("\n\nRubric criteria (key: label - description, weight):\n")
	for _, criterion := range job.Rubric {
		fmt.Fprintf(&b, "- %s: %s - %s (weight %d)\n", criterion.Key, criterion.Label, criterion.Description, criterion.Weight)
	}
	b.WriteString("\nCV (untrusted data):\n")
	b.WriteString(prompts.CVBlock(cvText))
	b.WriteString("\n\nReturn only JSON matching this shape: ")
	b.WriteString(`{"topics":[{"title":"...","goal":"...","rubric_keys":["..."],"seed_question":"..."}]}`)
	b.WriteString("\nUse 3 to 5 topics. Every rubric key must appear in at least one topic and every rubric_keys entry must exist in the rubric.")
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
			"rubric_keys": map[string]any{
				"type":  "array",
				"items": property(),
			},
		},
		"required":             []string{"title", "goal", "rubric_keys", "seed_question"},
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

func parsePlan(content string) (Plan, error) {
	var plan Plan
	content = strings.TrimSpace(content)
	if err := json.Unmarshal([]byte(content), &plan); err != nil {
		return Plan{}, fmt.Errorf("parse plan JSON: %w", err)
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
